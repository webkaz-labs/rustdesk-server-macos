// Package release implements distribution build tooling with only the Go standard library.
package release

import (
	"crypto/sha1" // UUID v5 identity only, never used for artifact verification.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var binaryNames = []string{"rustdesk-server", "hbbs", "hbbr"}

type Pin struct {
	Repository string            `json:"repository"`
	Version    string            `json:"version"`
	Tag        string            `json:"tag"`
	Commit     string            `json:"commit"`
	Submodules map[string]string `json:"submodules"`
	Rust       string            `json:"rust_toolchain"`
	Go         string            `json:"go_toolchain"`
	MinimumMac string            `json:"minimum_macos"`
}

type Tool struct {
	Root string
	Pin  Pin
	// Tests inject command results without running compilers or touching a remote.
	run func(cwd, name string, args ...string) (string, error)
}

func New(root string) (*Tool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "upstream.json"))
	if err != nil {
		return nil, err
	}
	tool := &Tool{Root: root, run: runCommand}
	if err := json.Unmarshal(data, &tool.Pin); err != nil {
		return nil, fmt.Errorf("read upstream pin: %w", err)
	}
	return tool, nil
}

func runCommand(cwd, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (t *Tool) Run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: release-tool pin|validate-version|bootstrap|check-upstream|metadata|archive|source|manifest|checksums [arguments]")
	}
	need := map[string]int{"pin": 1, "validate-version": 1, "bootstrap": 2, "check-upstream": 1,
		"metadata": 5, "archive": 2, "source": 4, "manifest": 1, "checksums": 0}
	n, ok := need[args[0]]
	if !ok || len(args)-1 != n {
		return fmt.Errorf("unknown command or incorrect argument count: %q", args[0])
	}
	a := args[1:]
	switch args[0] {
	case "pin":
		pins := map[string]string{"repository": t.Pin.Repository, "version": t.Pin.Version, "tag": t.Pin.Tag,
			"commit": t.Pin.Commit, "rust_toolchain": t.Pin.Rust, "go_toolchain": t.Pin.Go, "minimum_macos": t.Pin.MinimumMac}
		value, exists := pins[a[0]]
		if !exists {
			return fmt.Errorf("unknown pin %q", a[0])
		}
		_, err := fmt.Fprintln(out, value)
		return err
	case "validate-version":
		return ValidateVersion(a[0])
	case "bootstrap":
		return Bootstrap(t.Root, a[0], a[1])
	case "check-upstream":
		return t.CheckUpstream(a[0])
	case "metadata":
		return t.Metadata(a[0], a[1], a[2], a[3], a[4])
	case "archive":
		return Archive(a[0], a[1])
	case "source":
		return t.SourceBundle(a[0], a[1], a[2], a[3])
	case "manifest":
		return t.Manifest(a[0])
	case "checksums":
		return t.Checksums()
	}
	return errors.New("unreachable command")
}

func (t *Tool) CheckUpstream(upstream string) error {
	commit, err := t.run(upstream, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if commit != t.Pin.Commit {
		return errors.New("upstream commit does not match upstream.json")
	}
	status, err := t.run(upstream, "git", "submodule", "status", "--recursive")
	if err != nil {
		return err
	}
	actual := make(map[string]string)
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.ContainsRune("-+U", rune(line[0])) {
			return fmt.Errorf("submodule uninitialized, changed or conflicted: %s", line)
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return fmt.Errorf("malformed submodule status: %s", line)
		}
		actual[fields[1]] = fields[0]
	}
	if len(actual) != len(t.Pin.Submodules) {
		return fmt.Errorf("unexpected recursive submodule set: %v", actual)
	}
	paths := []string{upstream}
	for path, sha := range t.Pin.Submodules {
		if actual[path] != sha {
			return fmt.Errorf("submodule %s does not match recorded SHA", path)
		}
		paths = append(paths, filepath.Join(upstream, path))
	}
	for _, path := range paths {
		diff, err := t.run(path, "git", "diff", "--name-only", "HEAD", "--")
		if err != nil {
			return err
		}
		if diff != "" {
			return fmt.Errorf("tracked upstream source modified in %s: %s", path, strings.ReplaceAll(diff, "\n", ", "))
		}
	}
	return requireFile(filepath.Join(upstream, "Cargo.lock"))
}

func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	return nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func idHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func serialUUID(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	namespace, _ := hex.DecodeString("6ba7b8119dad11d180b400c04fd430c8")
	h := sha1.New()
	_, _ = h.Write(namespace)
	_, _ = h.Write(data)
	u := h.Sum(nil)[:16]
	u[6] = u[6]&0x0f | 0x50
	u[8] = u[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", u[:4], u[4:6], u[6:8], u[8:10], u[10:]), nil
}

type cargoPackage struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Version      string  `json:"version"`
	ManifestPath string  `json:"manifest_path"`
	Source       *string `json:"source"`
	License      *string `json:"license"`
	LicenseFile  *string `json:"license_file"`
}

type cargoNode struct {
	ID   string `json:"id"`
	Deps []struct {
		Package string `json:"pkg"`
	} `json:"deps"`
}

type cargoMetadata struct {
	Packages []cargoPackage `json:"packages"`
	Resolve  struct {
		Root  string      `json:"root"`
		Nodes []cargoNode `json:"nodes"`
	} `json:"resolve"`
}

func (t *Tool) sourceMetadata(upstream, version, arch string) (map[string]any, error) {
	commit, err := t.run(t.Root, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	lockHash, err := fileHash(filepath.Join(upstream, "Cargo.lock"))
	if err != nil {
		return nil, err
	}
	build := map[string]any{"architecture": arch, "os": "darwin", "minimum_macos": t.Pin.MinimumMac}
	commands := []struct {
		key, name string
		args      []string
	}{
		{"go", "go", []string{"version"}}, {"rustc", "rustc", []string{"+" + t.Pin.Rust, "--version", "--verbose"}},
		{"macos", "sw_vers", []string{"-productVersion"}}, {"xcode", "xcodebuild", []string{"-version"}},
		{"sdk", "xcrun", []string{"--show-sdk-version"}},
	}
	for _, c := range commands {
		value, err := t.run(t.Root, c.name, c.args...)
		if err != nil {
			return nil, err
		}
		build[c.key] = value
	}
	pinJSON, _ := json.Marshal(t.Pin)
	var upstreamDetails map[string]any
	if err := json.Unmarshal(pinJSON, &upstreamDetails); err != nil {
		return nil, err
	}
	upstreamDetails["cargo_lock_sha256"] = lockHash
	repository := os.Getenv("GITHUB_REPOSITORY")
	if repository == "" {
		repository = "webkaz-labs/rustdesk-server-macos"
	}
	return map[string]any{
		"distribution": map[string]any{"repository": repository, "version": version, "commit": commit},
		"upstream":     upstreamDetails, "build": build,
		"corresponding_source_asset": "rustdesk-server-macos-" + version + "-source.tar.gz",
	}, nil
}

func (t *Tool) Metadata(upstream, metadataFile, stage, version, arch string) error {
	if err := ValidateVersion(version); err != nil {
		return err
	}
	if arch != "arm64" {
		return fmt.Errorf("unsupported architecture: %s", arch)
	}
	if err := t.CheckUpstream(upstream); err != nil {
		return err
	}
	data, err := os.ReadFile(metadataFile)
	if err != nil {
		return err
	}
	var raw cargoMetadata
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	active := make(map[string]bool)
	for _, node := range raw.Resolve.Nodes {
		active[node.ID] = true
	}
	var packages []cargoPackage
	for _, p := range raw.Packages {
		if active[p.ID] {
			packages = append(packages, p)
		}
	}
	if raw.Resolve.Root == "" || !active[raw.Resolve.Root] {
		return errors.New("Cargo metadata has no resolved root package")
	}
	out := filepath.Join(stage, "share", "rustdesk-server")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	details, err := t.sourceMetadata(upstream, version, arch)
	if err != nil {
		return err
	}
	binaries := make(map[string]any)
	for _, name := range binaryNames {
		hash, err := fileHash(filepath.Join(stage, "bin", name))
		if err != nil {
			return err
		}
		binaries[name] = map[string]string{"sha256": hash}
	}
	details["binaries"] = binaries
	if err := writeJSON(filepath.Join(out, "source.json"), details); err != nil {
		return err
	}
	if err := collectLicenses(packages, filepath.Join(out, "licenses")); err != nil {
		return err
	}
	for _, paths := range [][2]string{
		{filepath.Join(upstream, "LICENSE"), filepath.Join(out, "licenses", "AGPL-3.0-upstream.txt")},
		{filepath.Join(t.Root, "LICENSE"), filepath.Join(out, "licenses", "distribution-LICENSE.txt")},
		{filepath.Join(upstream, "Cargo.lock"), filepath.Join(out, "Cargo.lock")},
	} {
		if err := copyFile(paths[0], paths[1]); err != nil {
			return err
		}
	}
	ids := make(map[string]string)
	components := []any{}
	for _, p := range packages {
		ids[p.ID] = "cargo:" + idHash(p.ID)
		component := map[string]any{"type": "library", "bom-ref": ids[p.ID], "name": p.Name, "version": p.Version,
			"properties": []any{map[string]string{"name": "cargo:package-id", "value": p.ID}}}
		if p.License != nil && *p.License != "" {
			component["licenses"] = []any{map[string]any{"license": map[string]string{"name": *p.License}}}
		}
		if p.Source != nil && strings.HasPrefix(*p.Source, "registry+") {
			component["purl"] = "pkg:cargo/" + p.Name + "@" + p.Version
		}
		components = append(components, component)
	}
	for _, name := range binaryNames {
		v := t.Pin.Version
		if name == "rustdesk-server" {
			v = version
		}
		hash := binaries[name].(map[string]string)["sha256"]
		components = append(components, map[string]any{"type": "application", "bom-ref": "binary:" + name,
			"name": name, "version": v, "hashes": []any{map[string]string{"alg": "SHA-256", "content": hash}}})
	}
	dependencies := []any{}
	for _, node := range raw.Resolve.Nodes {
		id, exists := ids[node.ID]
		if !exists {
			continue
		}
		set := make(map[string]bool)
		for _, dep := range node.Deps {
			if ref, ok := ids[dep.Package]; ok {
				set[ref] = true
			}
		}
		deps := []string{}
		for ref := range set {
			deps = append(deps, ref)
		}
		sort.Strings(deps)
		dependencies = append(dependencies, map[string]any{"ref": id, "dependsOn": deps})
	}
	rootID, exists := ids[raw.Resolve.Root]
	if !exists {
		return errors.New("Cargo root absent from package inventory")
	}
	for _, name := range binaryNames {
		deps := []string{}
		if name != "rustdesk-server" {
			deps = append(deps, rootID)
		}
		dependencies = append(dependencies, map[string]any{"ref": "binary:" + name, "dependsOn": deps})
	}
	serial, err := serialUUID(details)
	if err != nil {
		return err
	}
	build := details["build"].(map[string]any)
	bom := map[string]any{"bomFormat": "CycloneDX", "specVersion": "1.5", "version": 1, "serialNumber": serial,
		"metadata": map[string]any{"component": map[string]string{"type": "application", "name": "rustdesk-server-macos", "version": version},
			"properties": []any{
				map[string]string{"name": "inventory:scope", "value": "Cargo target-filtered dependency graph including build dependencies; Go helper uses only standard library; system SDK libraries are not vendored"},
				map[string]string{"name": "build:go", "value": build["go"].(string)},
				map[string]string{"name": "build:target", "value": "darwin-" + arch},
			}}, "components": components, "dependencies": dependencies}
	return writeJSON(filepath.Join(out, "bom.cdx.json"), bom)
}

func collectLicenses(packages []cargoPackage, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	packages = append([]cargoPackage(nil), packages...)
	sort.Slice(packages, func(i, j int) bool {
		a, b := packages[i], packages[j]
		return a.Name+"\x00"+a.Version+"\x00"+a.ID < b.Name+"\x00"+b.Version+"\x00"+b.ID
	})
	records := []any{}
	for _, p := range packages {
		base := filepath.Dir(p.ManifestPath)
		key := p.Name + "-" + p.Version + "-" + idHash(p.ID)[:8]
		if filepath.Base(key) != key || strings.ContainsAny(key, "/\\") {
			return fmt.Errorf("unsafe package name/version: %s", key)
		}
		files := make(map[string]bool)
		for _, pattern := range []string{"LICENSE*", "LICENCE*", "COPYING*", "NOTICE*", "COPYRIGHT*", "UNLICENSE*", "license*", "licence*"} {
			matches, err := filepath.Glob(filepath.Join(base, pattern))
			if err != nil {
				return err
			}
			for _, match := range matches {
				files[match] = true
			}
		}
		if p.LicenseFile != nil {
			candidate := *p.LicenseFile
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(base, candidate)
			}
			files[candidate] = true
		}
		paths := []string{}
		for path := range files {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || !within(base, path) || !within(base, resolved) || requireFile(resolved) != nil {
				continue
			}
			paths = append(paths, path)
		}
		sort.Strings(paths)
		copied := []string{}
		for _, source := range paths {
			rel, err := filepath.Rel(base, source)
			if err != nil {
				return err
			}
			target := filepath.Join(out, key, rel)
			if err := copyFile(source, target); err != nil {
				return err
			}
			copied = append(copied, key+"/"+filepath.ToSlash(rel))
		}
		records = append(records, map[string]any{"name": p.Name, "version": p.Version, "id": p.ID, "license": p.License, "files": copied})
	}
	if err := writeJSON(filepath.Join(out, "index.json"), records); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "README.txt"), []byte("License declarations are package authors' Cargo metadata. Available top-level\nlicense and notice files are copied here; this is not a legal conclusion.\nSee the matching complete source archive for nested notices, bundled C sources,\nand the full source of registry and Git dependencies. Some packages do not\nship separate license files; index.json records those entries explicitly.\n"), 0o644)
}

func (t *Tool) Manifest(version string) error {
	if err := ValidateVersion(version); err != nil {
		return err
	}
	dist := filepath.Join(t.Root, "dist")
	var text strings.Builder
	text.WriteString("# Generated from final release assets. TOML input, not a signature.\nbin = [\"bin/rustdesk-server\", \"bin/hbbs\", \"bin/hbbr\"]\n\n")
	for _, a := range [][2]string{{"arm64", "aarch64"}} {
		stem := "rustdesk-server-macos-" + version + "-darwin-" + a[0]
		for _, suffix := range []string{".tar.gz", ".cdx.json", ".source.json"} {
			if err := requireFile(filepath.Join(dist, stem+suffix)); err != nil {
				return fmt.Errorf("missing final release asset: %w", err)
			}
		}
		fmt.Fprintf(&text, "[[artifact]]\npath = %q\nos = \"darwin\"\narch = %q\nformat = \"tar.gz\"\nrequires = { os_min = %q }\n\n", "dist/"+stem+".tar.gz", a[1], t.Pin.MinimumMac)
		fmt.Fprintf(&text, "[[resource]]\nkind = \"sbom\"\nformat = \"cyclonedx\"\nartifact = %q\nasset = %q\n\n", stem+".tar.gz", "dist/"+stem+".cdx.json")
	}
	source := "rustdesk-server-macos-" + version + "-source.tar.gz"
	hash, err := fileHash(filepath.Join(dist, source))
	if err != nil {
		return err
	}
	fmt.Fprintf(&text, "[extensions.\"github.com/webkaz-labs/rustdesk-server-macos\"]\nupstream_commit = %q\nsource_asset = %q\nsource_sha256 = %q\n", t.Pin.Commit, source, hash)
	return os.WriteFile(filepath.Join(dist, "packslip.toml"), []byte(text.String()), 0o644)
}

func (t *Tool) Checksums() error {
	dist := filepath.Join(t.Root, "dist")
	entries, err := os.ReadDir(dist)
	if err != nil {
		return err
	}
	var text strings.Builder
	for _, entry := range entries {
		if !entry.Type().IsRegular() || entry.Name() == "SHA256SUMS" {
			continue
		}
		hash, err := fileHash(filepath.Join(dist, entry.Name()))
		if err != nil {
			return err
		}
		fmt.Fprintf(&text, "%s  %s\n", hash, entry.Name())
	}
	return os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(text.String()), 0o644)
}
