package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	releaseTestCommit             = "73523b31cfd25d77dee862e6fc9f5e1fb5e485ef"
	releaseTestCommon             = "83419b6549636ee39dacef7776c473f5802e08d6"
	releaseTestDistributionCommit = "1234567890123456789012345678901234567890"
)

func releaseTestPin() Pin {
	return Pin{
		Repository: "https://github.com/rustdesk/rustdesk-server.git", Version: "1.1.16", Tag: "1.1.16",
		Commit: releaseTestCommit, Submodules: map[string]string{"libs/hbb_common": releaseTestCommon},
		Rust: "1.98.1", Go: "1.27.1", MinimumMac: "15.0",
	}
}

func releaseTestWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func releaseTestRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func releaseTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	releaseTestWrite(t, path, string(data), 0o644)
}

func releaseTestDecode(t *testing.T, path string, value any) {
	t.Helper()
	if err := json.Unmarshal(releaseTestRead(t, path), value); err != nil {
		t.Fatal(err)
	}
}

func releaseTestHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func releaseTestNew(t *testing.T) *Tool {
	t.Helper()
	root := t.TempDir()
	releaseTestJSON(t, filepath.Join(root, "upstream.json"), releaseTestPin())
	tool, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	tool.run = func(cwd, name string, args ...string) (string, error) {
		t.Fatalf("unexpected external command in test: cwd=%q %s %q", cwd, name, args)
		return "", errors.New("unexpected command")
	}
	return tool
}

func TestUpstreamExactPins(t *testing.T) {
	tool, err := New(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if want := releaseTestPin(); !reflect.DeepEqual(tool.Pin, want) {
		t.Fatalf("upstream pin changed:\ngot  %+v\nwant %+v", tool.Pin, want)
	}
}

func TestNewRejectsMissingAndMalformedPins(t *testing.T) {
	for _, content := range []string{"", "{broken", `{"submodules": 42}`} {
		t.Run(fmt.Sprintf("pin-%q", content), func(t *testing.T) {
			root := t.TempDir()
			if content != "" {
				releaseTestWrite(t, filepath.Join(root, "upstream.json"), content, 0o644)
			}
			if _, err := New(root); err == nil {
				t.Fatal("invalid pin accepted")
			}
		})
	}
}

func TestReleaseRunRoutesPinsAndRejectsInvalidCommands(t *testing.T) {
	tool := releaseTestNew(t)
	for key, want := range map[string]string{
		"repository": tool.Pin.Repository, "version": tool.Pin.Version, "tag": tool.Pin.Tag,
		"commit": tool.Pin.Commit, "rust_toolchain": tool.Pin.Rust, "go_toolchain": tool.Pin.Go,
		"minimum_macos": "15.0",
	} {
		var out bytes.Buffer
		if err := tool.Run([]string{"pin", key}, &out); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != want+"\n" {
			t.Fatalf("pin %s = %q, want %q", key, got, want)
		}
	}
	for _, args := range [][]string{nil, {"unknown"}, {"pin"}, {"pin", "submodules"}, {"checksums", "extra"}, {"archive", "only-source"}, {"metadata"}, {"validate-version", "../bad"}} {
		if err := tool.Run(args, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted: %q", args)
		}
	}
	if err := tool.Run([]string{"validate-version", "1.2.3-rc.1+build.4"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

type releaseTestArchiveMember struct {
	header tar.Header
	data   []byte
}

func releaseTestReadArchive(t *testing.T, filename string) (map[string]releaseTestArchiveMember, []string, gzip.Header) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(releaseTestRead(t, filename)))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	r := tar.NewReader(gz)
	members := make(map[string]releaseTestArchiveMember)
	var order []string
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := members[header.Name]; exists {
			t.Fatalf("duplicate archive entry %q", header.Name)
		}
		members[header.Name] = releaseTestArchiveMember{*header, data}
		order = append(order, header.Name)
	}
	return members, order, gz.Header
}

func TestArchiveReproducibleNormalizedAndExecutable(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	root := t.TempDir()
	source := filepath.Join(root, "source")
	releaseTestWrite(t, filepath.Join(source, "bin", "tool"), "executable fixture", 0o710)
	releaseTestWrite(t, filepath.Join(source, "NOTICE"), "notice", 0o600)
	if err := os.Symlink("bin/tool", filepath.Join(source, "tool-link")); err != nil {
		t.Fatal(err)
	}
	one, two := filepath.Join(root, "one.tar.gz"), filepath.Join(root, "two.tar.gz")
	if err := Archive(source, one); err != nil {
		t.Fatal(err)
	}
	// Filesystem timestamps and non-executable permission differences cannot
	// leak into archive headers or alter the deterministic compressed output.
	if err := os.Chtimes(filepath.Join(source, "NOTICE"), time.Unix(123, 0), time.Unix(456, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(source, "NOTICE"), 0o664); err != nil {
		t.Fatal(err)
	}
	if err := Archive(source, two); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(releaseTestRead(t, one), releaseTestRead(t, two)) {
		t.Fatal("archives differ for identical source contents")
	}
	members, order, gz := releaseTestReadArchive(t, one)
	if want := []string{"NOTICE", "bin/", "bin/tool", "tool-link"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("archive order = %q, want %q", order, want)
	}
	if gz.ModTime.Unix() != 1700000000 || gz.OS != 255 || gz.Name != "" || gz.Comment != "" {
		t.Fatalf("nondeterministic gzip header: %+v", gz)
	}
	for name, wantMode := range map[string]int64{"NOTICE": 0o644, "bin/": 0o755, "bin/tool": 0o755, "tool-link": 0o777} {
		header := members[name].header
		if header.Mode != wantMode || header.Uid != 0 || header.Gid != 0 || header.Uname != "root" || header.Gname != "root" {
			t.Errorf("%s has unexpected ownership/mode: %+v", name, header)
		}
		if header.ModTime.Unix() != 1700000000 || !header.AccessTime.IsZero() || !header.ChangeTime.IsZero() {
			t.Errorf("%s has non-normalized timestamps: %+v", name, header)
		}
	}
	if string(members["bin/tool"].data) != "executable fixture" {
		t.Fatal("binary contents changed")
	}
	if h := members["tool-link"].header; h.Typeflag != tar.TypeSymlink || h.Linkname != "bin/tool" {
		t.Fatalf("symlink not preserved: %+v", h)
	}
}

func TestSourceEpochValidation(t *testing.T) {
	for _, value := range []string{"", "0", "1700000000", "4294967295"} {
		t.Run("valid-"+value, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", value)
			got, err := sourceEpoch()
			if err != nil || got.Unix() < 0 {
				t.Fatalf("sourceEpoch(%q) = %v, %v", value, got, err)
			}
		})
	}
	for _, value := range []string{"-1", "4294967296", "abc", "1.5", " 12", "999999999999999999999"} {
		t.Run("invalid-"+value, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", value)
			if _, err := sourceEpoch(); err == nil {
				t.Fatalf("invalid epoch %q accepted", value)
			}
		})
	}
}

func TestArchiveRejectsSourceDestinationAndEscapingLinks(t *testing.T) {
	for _, link := range []string{"../outside", "/tmp/outside"} {
		t.Run(link, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			if err := os.Mkdir(source, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link, filepath.Join(source, "unsafe")); err != nil {
				t.Fatal(err)
			}
			if err := Archive(source, filepath.Join(root, "out.tar.gz")); err == nil {
				t.Fatal("escaping symlink accepted")
			}
		})
	}
	root := t.TempDir()
	releaseTestWrite(t, filepath.Join(root, "input"), "fixture", 0o644)
	if err := Archive(root, filepath.Join(root, "nested", "out.tar.gz")); err == nil {
		t.Fatal("archive output inside input accepted")
	}
	if err := Archive(filepath.Join(root, "missing"), filepath.Join(t.TempDir(), "out.tar.gz")); err == nil {
		t.Fatal("missing source accepted")
	}
}

type releaseTestTarEntry struct {
	name, link, data string
	kind             byte
	mode             int64
}

func releaseTestTar(t *testing.T, entries ...releaseTestTarEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Linkname: entry.link, Typeflag: entry.kind, Mode: entry.mode}
		if entry.kind == tar.TypeReg || entry.kind == tar.TypeRegA {
			header.Size = int64(len(entry.data))
		}
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.WriteString(w, entry.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestExtractSourcePreservesSafeFilesAndLinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "extract")
	data := releaseTestTar(t,
		releaseTestTarEntry{name: "bin/", kind: tar.TypeDir, mode: 0o777},
		releaseTestTarEntry{name: "bin/tool", kind: tar.TypeReg, mode: 0o711, data: "run"},
		releaseTestTarEntry{name: "README", kind: tar.TypeReg, mode: 0o666, data: "read"},
		releaseTestTarEntry{name: "bin/readme", kind: tar.TypeSymlink, link: "../README"},
	)
	if err := extractSource(bytes.NewReader(data), root); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"bin/tool": "run", "README": "read", "bin/readme": "read"} {
		if got := string(releaseTestRead(t, filepath.Join(root, name))); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]os.FileMode{"bin": 0o755, "bin/tool": 0o755, "README": 0o644} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", name, got, want)
		}
	}
	if link, err := os.Readlink(filepath.Join(root, "bin/readme")); err != nil || link != "../README" {
		t.Fatalf("safe link = %q, %v", link, err)
	}
}

func TestExtractSourceRejectsUnsafeArchiveEntries(t *testing.T) {
	cases := map[string][]releaseTestTarEntry{
		"parent traversal":             {{name: "../escape", kind: tar.TypeReg, data: "bad"}},
		"nested traversal":             {{name: "a/../../escape", kind: tar.TypeReg, data: "bad"}},
		"absolute path":                {{name: "/absolute", kind: tar.TypeReg, data: "bad"}},
		"backslash path":               {{name: `..\escape`, kind: tar.TypeReg, data: "bad"}},
		"dot component":                {{name: "a/./file", kind: tar.TypeReg, data: "bad"}},
		"escaping symlink":             {{name: "link", kind: tar.TypeSymlink, link: "../escape"}},
		"absolute symlink":             {{name: "link", kind: tar.TypeSymlink, link: "/tmp/escape"}},
		"backslash symlink":            {{name: "link", kind: tar.TypeSymlink, link: `..\escape`}},
		"hardlink":                     {{name: "link", kind: tar.TypeLink, link: "target"}},
		"device":                       {{name: "device", kind: tar.TypeChar}},
		"file through earlier symlink": {{name: "target/", kind: tar.TypeDir}, {name: "link", kind: tar.TypeSymlink, link: "target"}, {name: "link/file", kind: tar.TypeReg, data: "bad"}},
		"overwrite earlier symlink":    {{name: "link", kind: tar.TypeSymlink, link: "target"}, {name: "link", kind: tar.TypeReg, data: "bad"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := extractSource(bytes.NewReader(releaseTestTar(t, entries...)), filepath.Join(root, "extract")); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
				t.Fatalf("archive wrote outside root: %v", err)
			}
		})
	}
	t.Run("preexisting symlink parent", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}
		if err := extractSource(bytes.NewReader(releaseTestTar(t, releaseTestTarEntry{name: "link/file", kind: tar.TypeReg, data: "bad"})), root); err == nil {
			t.Fatal("preexisting symlink parent accepted")
		}
		if _, err := os.Stat(filepath.Join(outside, "file")); !os.IsNotExist(err) {
			t.Fatal("wrote through symlink parent")
		}
	})
}

func releaseTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	// Git commands operate only on disposable local fixture repositories.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Packaging Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Packaging Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("local fixture git %q: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func releaseTestRepository(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	releaseTestGit(t, root, "init", "-q")
	for name, value := range files {
		releaseTestWrite(t, filepath.Join(root, name), value, 0o644)
	}
	releaseTestGit(t, root, "add", "--all")
	releaseTestGit(t, root, "commit", "-qm", "fixture")
}

func TestExportGitContainsCommittedSourceOnly(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repo")
	releaseTestRepository(t, repository, map[string]string{"source.txt": "committed source", ".gitignore": "ignored-secret\n"})
	releaseTestWrite(t, filepath.Join(repository, "private-runtime-key"), "never export", 0o600)
	releaseTestWrite(t, filepath.Join(repository, "ignored-secret"), "never export either", 0o600)
	releaseTestWrite(t, filepath.Join(repository, "source.txt"), "uncommitted modification", 0o644)
	out := filepath.Join(root, "export")
	if err := exportGit(repository, out); err != nil {
		t.Fatal(err)
	}
	if got := string(releaseTestRead(t, filepath.Join(out, "source.txt"))); got != "committed source" {
		t.Fatalf("export source = %q", got)
	}
	for _, name := range []string{"private-runtime-key", "ignored-secret", ".git"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("unexpected export entry %s: %v", name, err)
		}
	}
	if err := exportGit(t.TempDir(), filepath.Join(root, "bad-export")); err == nil {
		t.Fatal("non-repository export accepted")
	}
}

func TestBuildNativeIsolatesSQLxSchemaFromTrackedSource(t *testing.T) {
	script := string(releaseTestRead(t, filepath.Join("..", "..", "scripts", "build-native.sh")))
	start := strings.Index(script, "# SQLx schema-copy setup")
	end := strings.Index(script, "# End SQLx schema-copy setup.")
	if start < 0 || end <= start {
		t.Fatal("schema-copy setup block missing from native build")
	}
	root := t.TempDir()
	upstream := filepath.Join(root, "upstream")
	database := "SQLite format 3\x00committed schema bytes\x00"
	releaseTestRepository(t, upstream, map[string]string{"db_v2.sqlite3": database})
	commit := releaseTestGit(t, upstream, "rev-parse", "HEAD")
	copyPath := filepath.Join(root, ".build", "build-schema-arm64.sqlite3")
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		releaseTestWrite(t, copyPath+suffix, "stale build output", 0o644)
	}
	cmd := exec.Command("bash", "-euo", "pipefail", "-c", script[start:end]+"\nprintf '%s' \"$DATABASE_URL\"\n")
	cmd.Env = append(os.Environ(), "ROOT="+root, "UPSTREAM="+upstream, "ARCH=arm64", "COMMIT="+commit,
		"DATABASE_URL=sqlite://"+filepath.Join(upstream, "db_v2.sqlite3"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("schema setup: %v\n%s", err, output)
	}
	if string(output) != "sqlite://"+copyPath {
		t.Fatalf("DATABASE_URL does not select isolated build copy: %s", output)
	}
	if got := string(releaseTestRead(t, copyPath)); got != database {
		t.Fatalf("committed schema bytes changed during copy: %q", got)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Stat(copyPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("stale SQLite sidecar remains: %s", suffix)
		}
	}
	// Simulate SQLx/SQLite header and journal writes against its selected DB.
	releaseTestWrite(t, copyPath, "modified build copy", 0o644)
	releaseTestWrite(t, copyPath+"-journal", "build journal", 0o644)
	if got := string(releaseTestRead(t, filepath.Join(upstream, "db_v2.sqlite3"))); got != database {
		t.Fatal("tracked source DB changed through build copy")
	}
	if dirty := releaseTestGit(t, upstream, "diff", "--name-only", "HEAD", "--"); dirty != "" {
		t.Fatalf("schema build contaminated pinned source: %s", dirty)
	}
}

func TestRestoredTargetCacheSurvivesFreshPinnedCheckout(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	releaseTestRepository(t, source, map[string]string{".gitignore": "/target/\n", "Cargo.lock": "locked fixture"})
	commit := releaseTestGit(t, source, "rev-parse", "HEAD")
	checkout := filepath.Join(root, "upstream-arm64")
	cacheFile := filepath.Join(checkout, "target", "cached-object")
	releaseTestWrite(t, cacheFile, "restored build object", 0o644)
	// The native script restores only target, then initializes/fetches the source.
	releaseTestGit(t, checkout, "init", "-q")
	releaseTestGit(t, checkout, "fetch", "--depth=1", source, commit)
	releaseTestGit(t, checkout, "checkout", "--detach", "FETCH_HEAD")
	if got := string(releaseTestRead(t, cacheFile)); got != "restored build object" {
		t.Fatal("source checkout removed restored compiler output")
	}
	if got := releaseTestGit(t, checkout, "rev-parse", "HEAD"); got != commit {
		t.Fatalf("checkout commit = %s", got)
	}
	if dirty := releaseTestGit(t, checkout, "status", "--porcelain"); dirty != "" {
		t.Fatalf("restored target polluted tracked source: %s", dirty)
	}
}

func TestCollectLicensesPreservesDeclarationsAndNotices(t *testing.T) {
	root := t.TempDir()
	crate := filepath.Join(root, "crate")
	files := map[string]string{"LICENSE": "a license", "NOTICE": "a notice", "COPYRIGHT.txt": "copyright", "licenses/custom.txt": "custom terms"}
	for name, data := range files {
		releaseTestWrite(t, filepath.Join(crate, name), data, 0o644)
	}
	license, licenseFile := "MIT/Apache-2.0", "licenses/custom.txt"
	packages := []cargoPackage{{ID: "example 1.0", Name: "example", Version: "1.0", ManifestPath: filepath.Join(crate, "Cargo.toml"), License: &license, LicenseFile: &licenseFile}}
	out := filepath.Join(root, "notices")
	if err := collectLicenses(packages, out); err != nil {
		t.Fatal(err)
	}
	var index []struct {
		Name, Version, ID string
		License           *string
		Files             []string
	}
	releaseTestDecode(t, filepath.Join(out, "index.json"), &index)
	if len(index) != 1 || index[0].License == nil || *index[0].License != license || len(index[0].Files) != len(files) {
		t.Fatalf("license inventory = %+v", index)
	}
	wantContents := make(map[string]bool)
	for _, content := range files {
		wantContents[content] = true
	}
	for _, name := range index[0].Files {
		content := string(releaseTestRead(t, filepath.Join(out, filepath.FromSlash(name))))
		if !wantContents[content] {
			t.Fatalf("unexpected notice content %q", content)
		}
		delete(wantContents, content)
	}
	if len(wantContents) != 0 {
		t.Fatalf("lost notices: %v", wantContents)
	}
	before := releaseTestRead(t, filepath.Join(out, "index.json"))
	if err := collectLicenses(packages, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, releaseTestRead(t, filepath.Join(out, "index.json"))) {
		t.Fatal("license inventory changed across identical runs")
	}
	if !strings.Contains(string(releaseTestRead(t, filepath.Join(out, "README.txt"))), "not a legal conclusion") {
		t.Fatal("missing license inventory limitations")
	}
}

func TestCollectLicensesSkipsEscapingFilesAndRejectsUnsafePackageNames(t *testing.T) {
	root := t.TempDir()
	crate := filepath.Join(root, "crate")
	releaseTestWrite(t, filepath.Join(crate, "NOTICE"), "retained", 0o644)
	secret := filepath.Join(root, "private")
	releaseTestWrite(t, secret, "never copy", 0o600)
	if err := os.Symlink(secret, filepath.Join(crate, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	licenseFile := "../private"
	p := cargoPackage{ID: "safe 1", Name: "safe", Version: "1", ManifestPath: filepath.Join(crate, "Cargo.toml"), LicenseFile: &licenseFile}
	out := filepath.Join(root, "licenses")
	if err := collectLicenses([]cargoPackage{p}, out); err != nil {
		t.Fatal(err)
	}
	var index []struct {
		Files   []string
		License *string
	}
	releaseTestDecode(t, filepath.Join(out, "index.json"), &index)
	if len(index) != 1 || len(index[0].Files) != 1 || filepath.Base(index[0].Files[0]) != "NOTICE" || index[0].License != nil {
		t.Fatalf("unexpected filtered license index: %+v", index)
	}
	for _, unsafe := range []string{"../escape", `bad\name`, "dir/name"} {
		p.Name = unsafe
		if err := collectLicenses([]cargoPackage{p}, filepath.Join(root, "bad")); err == nil {
			t.Errorf("unsafe package %q accepted", unsafe)
		}
	}
}

func releaseTestAssets(t *testing.T, tool *Tool, version string) []string {
	t.Helper()
	stem := "rustdesk-server-macos-" + version + "-darwin-arm64"
	names := []string{stem + ".tar.gz", stem + ".cdx.json", stem + ".source.json", "rustdesk-server-macos-" + version + "-source.tar.gz"}
	for _, name := range names {
		releaseTestWrite(t, filepath.Join(tool.Root, "dist", name), "fixture: "+name, 0o644)
	}
	return names
}

func TestManifestMatchesArm64AssetsAndDeploymentMinimum(t *testing.T) {
	tool := releaseTestNew(t)
	names := releaseTestAssets(t, tool, "0.1.0")
	if err := tool.Run([]string{"manifest", "0.1.0"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data := string(releaseTestRead(t, filepath.Join(tool.Root, "dist", "packslip.toml")))
	for _, line := range []string{
		`bin = ["bin/rustdesk-server", "bin/hbbs", "bin/hbbr"]`,
		`path = "dist/` + names[0] + `"`, `os = "darwin"`, `arch = "aarch64"`, `format = "tar.gz"`,
		`requires = { os_min = "15.0" }`, `kind = "sbom"`, `format = "cyclonedx"`,
		`artifact = "` + names[0] + `"`, `asset = "dist/` + names[1] + `"`,
		`upstream_commit = "` + releaseTestCommit + `"`, `source_asset = "` + names[3] + `"`,
		`source_sha256 = "` + releaseTestHash(releaseTestRead(t, filepath.Join(tool.Root, "dist", names[3]))) + `"`,
	} {
		if !strings.Contains("\n"+data, "\n"+line+"\n") {
			t.Errorf("manifest missing exact line %s\n%s", line, data)
		}
	}
	if strings.Count(data, "[[artifact]]") != 1 || strings.Count(data, "[[resource]]") != 1 {
		t.Fatalf("manifest must contain one artifact and one SBOM: %s", data)
	}
	if strings.Contains(data, "amd64") || strings.Contains(data, "x86_64") {
		t.Fatal("manifest advertises unsupported Intel build")
	}
	if err := tool.Manifest("0.1.0"); err != nil {
		t.Fatal(err)
	}
	if got := string(releaseTestRead(t, filepath.Join(tool.Root, "dist", "packslip.toml"))); got != data {
		t.Fatal("manifest is not stable")
	}
}

func TestManifestMissingAssetsFailClosed(t *testing.T) {
	for _, missing := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("missing-%d", missing), func(t *testing.T) {
			tool := releaseTestNew(t)
			names := releaseTestAssets(t, tool, "0.1.0")
			if err := os.Remove(filepath.Join(tool.Root, "dist", names[missing])); err != nil {
				t.Fatal(err)
			}
			if err := tool.Manifest("0.1.0"); err == nil {
				t.Fatalf("missing %s accepted", names[missing])
			}
			if _, err := os.Stat(filepath.Join(tool.Root, "dist", "packslip.toml")); !os.IsNotExist(err) {
				t.Fatal("manifest published despite missing asset")
			}
		})
	}
	t.Run("directory in place of archive", func(t *testing.T) {
		tool := releaseTestNew(t)
		names := releaseTestAssets(t, tool, "0.1.0")
		path := filepath.Join(tool.Root, "dist", names[0])
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := tool.Manifest("0.1.0"); err == nil {
			t.Fatal("directory asset accepted")
		}
	})
	if err := releaseTestNew(t).Manifest("../unsafe"); err == nil {
		t.Fatal("unsafe version accepted")
	}
}

func TestChecksumsStableSortedAndExcludeSelfAndDirectories(t *testing.T) {
	tool := releaseTestNew(t)
	dist := filepath.Join(tool.Root, "dist")
	for name, content := range map[string]string{"z.tar.gz": "last", "a.cdx.json": "first", "packslip.toml": "manifest", "SHA256SUMS": "old self hash", "nested/ignored": "not a final asset"} {
		releaseTestWrite(t, filepath.Join(dist, name), content, 0o644)
	}
	if err := tool.Run([]string{"checksums"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	first := releaseTestRead(t, filepath.Join(dist, "SHA256SUMS"))
	if err := tool.Checksums(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, releaseTestRead(t, filepath.Join(dist, "SHA256SUMS"))) {
		t.Fatal("checksums change across identical runs")
	}
	want := ""
	for _, name := range []string{"a.cdx.json", "packslip.toml", "z.tar.gz"} {
		want += releaseTestHash(releaseTestRead(t, filepath.Join(dist, name))) + "  " + name + "\n"
	}
	if string(first) != want {
		t.Fatalf("SHA256SUMS:\ngot %s\nwant %s", first, want)
	}
	if err := releaseTestNew(t).Checksums(); err == nil {
		t.Fatal("missing dist accepted")
	}
}

type releaseTestCall struct {
	cwd, name string
	args      []string
}

func releaseTestMockBuild(t *testing.T, tool *Tool, upstream string) *[]releaseTestCall {
	t.Helper()
	var calls []releaseTestCall
	tool.run = func(cwd, name string, args ...string) (string, error) {
		calls = append(calls, releaseTestCall{cwd, name, append([]string(nil), args...)})
		command := name + " " + strings.Join(args, " ")
		switch command {
		case "git rev-parse HEAD":
			if cwd == upstream {
				return tool.Pin.Commit, nil
			}
			if cwd == tool.Root {
				return releaseTestDistributionCommit, nil
			}
		case "git submodule status --recursive":
			if cwd == upstream {
				return " " + releaseTestCommon + " libs/hbb_common (fixture)", nil
			}
		case "git diff --name-only HEAD --":
			if cwd == upstream || cwd == filepath.Join(upstream, "libs/hbb_common") {
				return "", nil
			}
		case "go version":
			return "go version go1.27.1 darwin/arm64", nil
		case "rustc +1.98.1 --version --verbose":
			return "rustc 1.98.1 (fixture)\nhost: aarch64-apple-darwin", nil
		case "sw_vers -productVersion":
			return "26.0", nil
		case "xcodebuild -version":
			return "Xcode 26.0\nBuild version fixture", nil
		case "xcrun --show-sdk-version":
			return "26.0", nil
		}
		t.Fatalf("unexpected command: cwd=%q %s", cwd, command)
		return "", errors.New("unexpected command")
	}
	return &calls
}

func releaseTestMetadataFixture(t *testing.T) (*Tool, string, string, string) {
	t.Helper()
	tool := releaseTestNew(t)
	upstream, stage := filepath.Join(tool.Root, "upstream"), filepath.Join(tool.Root, "stage")
	releaseTestWrite(t, filepath.Join(upstream, "Cargo.lock"), "locked fixture", 0o644)
	releaseTestWrite(t, filepath.Join(upstream, "LICENSE"), "upstream license", 0o644)
	releaseTestWrite(t, filepath.Join(tool.Root, "LICENSE"), "distribution license", 0o644)
	for _, name := range binaryNames {
		releaseTestWrite(t, filepath.Join(stage, "bin", name), "binary fixture: "+name, 0o755)
	}
	dependency := filepath.Join(tool.Root, "vendor", "dep")
	releaseTestWrite(t, filepath.Join(dependency, "NOTICE"), "dependency notice", 0o644)
	metadata := filepath.Join(tool.Root, "cargo.json")
	releaseTestJSON(t, metadata, map[string]any{
		"packages": []any{
			map[string]any{"id": "hbbs-id", "name": "hbbs", "version": "1.1.16", "manifest_path": filepath.Join(upstream, "Cargo.toml"), "license": nil, "source": nil},
			map[string]any{"id": "dep-id", "name": "target-dependency", "version": "2.0.0", "manifest_path": filepath.Join(dependency, "Cargo.toml"), "license": "MIT", "source": "registry+https://github.com/rust-lang/crates.io-index"},
			map[string]any{"id": "windows-only", "name": "windows-only", "version": "9.0.0", "manifest_path": filepath.Join(tool.Root, "missing", "Cargo.toml")},
		},
		"resolve": map[string]any{"root": "hbbs-id", "nodes": []any{
			map[string]any{"id": "hbbs-id", "deps": []any{map[string]string{"pkg": "dep-id"}, map[string]string{"pkg": "dep-id"}, map[string]string{"pkg": "windows-only"}}},
			map[string]any{"id": "dep-id", "deps": []any{}},
		}},
	})
	releaseTestMockBuild(t, tool, upstream)
	return tool, upstream, metadata, stage
}

func TestMetadataTargetFilterBinaryHashesAndBuildEvidence(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "fixture/distribution")
	tool, upstream, metadata, stage := releaseTestMetadataFixture(t)
	calls := releaseTestMockBuild(t, tool, upstream)
	if err := tool.Run([]string{"metadata", upstream, metadata, stage, "0.1.0", "arm64"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(stage, "share", "rustdesk-server")
	var bom struct {
		BOMFormat   string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Serial      string `json:"serialNumber"`
		Components  []struct {
			Name, Version, PURL string
			Ref                 string `json:"bom-ref"`
			Hashes              []struct{ Alg, Content string }
		}
		Dependencies []struct {
			Ref       string
			DependsOn []string
		}
		Metadata struct {
			Properties []struct{ Name, Value string }
		}
	}
	releaseTestDecode(t, filepath.Join(out, "bom.cdx.json"), &bom)
	if bom.BOMFormat != "CycloneDX" || bom.SpecVersion != "1.5" || !strings.HasPrefix(bom.Serial, "urn:uuid:") {
		t.Fatalf("invalid BOM identity: %+v", bom)
	}
	if len(bom.Components) != 5 {
		t.Fatalf("unexpected component count %d: %+v", len(bom.Components), bom.Components)
	}
	refs := map[string]bool{}
	for _, component := range bom.Components {
		refs[component.Ref] = true
		if component.Name == "windows-only" {
			t.Fatal("inactive target dependency leaked into BOM")
		}
		if component.Name == "target-dependency" && component.PURL != "pkg:cargo/target-dependency@2.0.0" {
			t.Fatalf("registry dependency PURL = %q", component.PURL)
		}
		if !strings.HasPrefix(component.Ref, "binary:") {
			continue
		}
		wantVersion := tool.Pin.Version
		if component.Name == "rustdesk-server" {
			wantVersion = "0.1.0"
		}
		if component.Version != wantVersion {
			t.Errorf("binary %s version = %q", component.Name, component.Version)
		}
		wantHash := releaseTestHash(releaseTestRead(t, filepath.Join(stage, "bin", component.Name)))
		if len(component.Hashes) != 1 || component.Hashes[0].Alg != "SHA-256" || component.Hashes[0].Content != wantHash {
			t.Errorf("incorrect binary hash for %s: %+v", component.Name, component.Hashes)
		}
	}
	for _, name := range binaryNames {
		if !refs["binary:"+name] {
			t.Errorf("missing binary component %s", name)
		}
	}
	if len(bom.Dependencies) != 5 {
		t.Fatalf("dependency count = %d", len(bom.Dependencies))
	}
	for _, dep := range bom.Dependencies {
		seen := map[string]bool{}
		for _, ref := range dep.DependsOn {
			if !refs[ref] || seen[ref] {
				t.Errorf("invalid or duplicate dependency %q on %q", ref, dep.Ref)
			}
			seen[ref] = true
		}
		if !sort.StringsAreSorted(dep.DependsOn) {
			t.Errorf("dependencies not sorted: %+v", dep)
		}
		if dep.Ref == "binary:rustdesk-server" && len(dep.DependsOn) != 0 {
			t.Fatal("standard-library-only Go helper claims a Cargo dependency")
		}
		if (dep.Ref == "binary:hbbs" || dep.Ref == "binary:hbbr") && !reflect.DeepEqual(dep.DependsOn, []string{"cargo:" + idHash("hbbs-id")}) {
			t.Errorf("upstream binary dependency = %+v", dep)
		}
	}
	properties := map[string]string{}
	for _, p := range bom.Metadata.Properties {
		properties[p.Name] = p.Value
	}
	if properties["build:target"] != "darwin-arm64" || properties["build:go"] != "go version go1.27.1 darwin/arm64" {
		t.Fatalf("BOM build properties = %+v", properties)
	}
	var source struct {
		Distribution struct{ Repository, Version, Commit string }
		Upstream     struct {
			Commit   string
			LockHash string `json:"cargo_lock_sha256"`
		}
		Build struct {
			Architecture, OS, Go, Rustc, MacOS, Xcode, SDK string
			MinimumMac                                     string `json:"minimum_macos"`
		}
		Binaries map[string]struct {
			SHA256 string `json:"sha256"`
		}
		CorrespondingSource string `json:"corresponding_source_asset"`
	}
	releaseTestDecode(t, filepath.Join(out, "source.json"), &source)
	if source.Distribution.Repository != "fixture/distribution" || source.Distribution.Version != "0.1.0" || source.Distribution.Commit != releaseTestDistributionCommit {
		t.Fatalf("distribution evidence = %+v", source.Distribution)
	}
	if source.Upstream.Commit != releaseTestCommit || source.Upstream.LockHash != releaseTestHash(releaseTestRead(t, filepath.Join(upstream, "Cargo.lock"))) {
		t.Fatalf("upstream evidence = %+v", source.Upstream)
	}
	if source.Build.Architecture != "arm64" || source.Build.OS != "darwin" || source.Build.MinimumMac != "15.0" || source.Build.MacOS != "26.0" || source.Build.SDK != "26.0" {
		t.Fatalf("build evidence = %+v", source.Build)
	}
	if source.CorrespondingSource != "rustdesk-server-macos-0.1.0-source.tar.gz" || len(source.Binaries) != 3 {
		t.Fatalf("missing source or binary provenance: %+v", source)
	}
	for _, name := range binaryNames {
		if source.Binaries[name].SHA256 != releaseTestHash(releaseTestRead(t, filepath.Join(stage, "bin", name))) {
			t.Errorf("source hash mismatch for %s", name)
		}
	}
	for file, want := range map[string]string{"Cargo.lock": "locked fixture", "licenses/AGPL-3.0-upstream.txt": "upstream license", "licenses/distribution-LICENSE.txt": "distribution license"} {
		if got := string(releaseTestRead(t, filepath.Join(out, file))); got != want {
			t.Errorf("%s = %q, want %q", file, got, want)
		}
	}
	var inventory []struct{ Name string }
	releaseTestDecode(t, filepath.Join(out, "licenses", "index.json"), &inventory)
	if len(inventory) != 2 {
		t.Fatalf("license inventory should use target-filtered packages: %+v", inventory)
	}
	before := releaseTestRead(t, filepath.Join(out, "bom.cdx.json"))
	if err := tool.Metadata(upstream, metadata, stage, "0.1.0", "arm64"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, releaseTestRead(t, filepath.Join(out, "bom.cdx.json"))) {
		t.Fatal("BOM is not reproducible")
	}
	for _, call := range *calls {
		if call.name != "git" && call.cwd != tool.Root {
			t.Errorf("toolchain evidence command used unexpected directory: %+v", call)
		}
	}
}

func TestMetadataRejectsUnsupportedArchitecturesBeforeCommands(t *testing.T) {
	for _, arch := range []string{"amd64", "x86_64", "aarch64", "", "arm64/../../escape"} {
		t.Run(arch, func(t *testing.T) {
			tool := releaseTestNew(t)
			if err := tool.Metadata("missing", "missing", "missing", "0.1.0", arch); err == nil {
				t.Fatalf("unsupported architecture %q accepted", arch)
			}
		})
	}
}

func TestMetadataRejectsMissingAssetsAndUnresolvedGraph(t *testing.T) {
	for _, failure := range []string{"binary", "root", "root-package", "malformed", "toolchain"} {
		t.Run(failure, func(t *testing.T) {
			tool, upstream, metadata, stage := releaseTestMetadataFixture(t)
			switch failure {
			case "binary":
				if err := os.Remove(filepath.Join(stage, "bin", "hbbr")); err != nil {
					t.Fatal(err)
				}
			case "root":
				releaseTestJSON(t, metadata, map[string]any{"packages": []any{}, "resolve": map[string]any{"root": "", "nodes": []any{}}})
			case "root-package":
				releaseTestJSON(t, metadata, map[string]any{"packages": []any{}, "resolve": map[string]any{"root": "missing", "nodes": []any{map[string]string{"id": "missing"}}}})
			case "malformed":
				releaseTestWrite(t, metadata, "{broken", 0o644)
			case "toolchain":
				original := tool.run
				tool.run = func(cwd, name string, args ...string) (string, error) {
					if name == "xcrun" {
						return "", errors.New("SDK unavailable")
					}
					return original(cwd, name, args...)
				}
			}
			if err := tool.Metadata(upstream, metadata, stage, "0.1.0", "arm64"); err == nil {
				t.Fatal("invalid metadata input accepted")
			}
			if _, err := os.Stat(filepath.Join(stage, "share", "rustdesk-server", "bom.cdx.json")); !os.IsNotExist(err) {
				t.Fatal("BOM published after incomplete metadata")
			}
		})
	}
}

func TestCheckUpstreamRejectsDriftAndUninitializedSubmodules(t *testing.T) {
	for _, failure := range []string{"none", "commit", "missing-submodule", "extra-submodule", "wrong-submodule", "uninitialized", "modified-submodule", "conflict", "malformed", "dirty-upstream", "dirty-submodule", "lock", "git-error"} {
		t.Run(failure, func(t *testing.T) {
			tool := releaseTestNew(t)
			upstream := filepath.Join(tool.Root, "upstream")
			releaseTestWrite(t, filepath.Join(upstream, "Cargo.lock"), "locked", 0o644)
			releaseTestMockBuild(t, tool, upstream)
			original := tool.run
			tool.run = func(cwd, name string, args ...string) (string, error) {
				command := name + " " + strings.Join(args, " ")
				if failure == "git-error" {
					return "", errors.New("git failed")
				}
				if command == "git rev-parse HEAD" && failure == "commit" {
					return "different", nil
				}
				if command == "git submodule status --recursive" {
					switch failure {
					case "missing-submodule":
						return "", nil
					case "extra-submodule":
						return " " + releaseTestCommon + " libs/hbb_common\n " + releaseTestCommon + " extra", nil
					case "wrong-submodule":
						return " " + strings.Repeat("b", 40) + " libs/hbb_common", nil
					case "uninitialized":
						return "-" + releaseTestCommon + " libs/hbb_common", nil
					case "modified-submodule":
						return "+" + releaseTestCommon + " libs/hbb_common", nil
					case "conflict":
						return "U" + releaseTestCommon + " libs/hbb_common", nil
					case "malformed":
						return "invalid", nil
					}
				}
				if command == "git diff --name-only HEAD --" && ((failure == "dirty-upstream" && cwd == upstream) || (failure == "dirty-submodule" && cwd != upstream)) {
					return "db_v2.sqlite3\nsrc/fixture.rs", nil
				}
				return original(cwd, name, args...)
			}
			if failure == "lock" {
				if err := os.Remove(filepath.Join(upstream, "Cargo.lock")); err != nil {
					t.Fatal(err)
				}
			}
			err := tool.CheckUpstream(upstream)
			if (err == nil) != (failure == "none") {
				t.Fatalf("CheckUpstream failure=%s: %v", failure, err)
			}
			if strings.HasPrefix(failure, "dirty-") && (!strings.Contains(err.Error(), "db_v2.sqlite3") || !strings.Contains(err.Error(), "src/fixture.rs")) {
				t.Fatalf("modified filenames absent from diagnostic: %v", err)
			}
		})
	}
}

func TestCopyTreePreservesVendorNoticesAndExecutableBits(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "vendor"), filepath.Join(root, "copy")
	releaseTestWrite(t, filepath.Join(source, "crate", "NOTICE"), "vendor notice", 0o644)
	releaseTestWrite(t, filepath.Join(source, "crate", "build.sh"), "#!/bin/sh\n", 0o755)
	if err := os.Symlink("NOTICE", filepath.Join(source, "crate", "license-link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, target); err != nil {
		t.Fatal(err)
	}
	if got := string(releaseTestRead(t, filepath.Join(target, "crate", "NOTICE"))); got != "vendor notice" {
		t.Fatal("vendor notice lost")
	}
	info, err := os.Stat(filepath.Join(target, "crate", "build.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("vendor executable mode: %v, %v", info, err)
	}
	if got, err := os.Readlink(filepath.Join(target, "crate", "license-link")); err != nil || got != "NOTICE" {
		t.Fatalf("vendor link: %q, %v", got, err)
	}
	if err := os.Symlink("../../outside", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, filepath.Join(root, "unsafe-copy")); err == nil {
		t.Fatal("escaping vendor symlink accepted")
	}
}

func TestSourceBundleCompletePortableAndOffline(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	tool := releaseTestNew(t)
	releaseTestRepository(t, tool.Root, map[string]string{"LICENSE": "distribution license", "go.mod": "module fixture\n", "cmd/helper.go": "package main\n"})
	upstream := filepath.Join(tool.Root, "upstream")
	releaseTestRepository(t, upstream, map[string]string{"LICENSE": "upstream license", "Cargo.lock": "locked", "Cargo.toml": "[package]\nname = \"fixture\"\n", ".cargo/config": "[build]\njobs = 1\n"})
	releaseTestRepository(t, filepath.Join(upstream, "libs/hbb_common"), map[string]string{"common.rs": "// pinned common source\n", "LICENSE": "common license"})
	releaseTestWrite(t, filepath.Join(tool.Root, "private-distribution-key"), "never export", 0o600)
	releaseTestWrite(t, filepath.Join(upstream, "private-runtime-key"), "never export", 0o600)
	vendor := filepath.Join(tool.Root, "cargo-vendor")
	releaseTestWrite(t, filepath.Join(vendor, "dep-1.0", ".cargo-checksum.json"), `{"files":{},"package":null}`, 0o644)
	releaseTestWrite(t, filepath.Join(vendor, "dep-1.0", "NOTICE"), "vendored notice", 0o644)
	config := filepath.Join(tool.Root, "vendor-config.toml")
	releaseTestWrite(t, config, "[source.crates-io]\nreplace-with = \"vendored-sources\"\n[source.vendored-sources]\ndirectory = \""+vendor+"\"\n", 0o644)
	fakeBin := filepath.Join(t.TempDir(), "bin")
	marker := filepath.Join(t.TempDir(), "cargo-checked")
	// This fixture command verifies the invocation without compiling anything,
	// accessing a registry, or needing a Rust toolchain on the test machine.
	releaseTestWrite(t, filepath.Join(fakeBin, "cargo"), `#!/bin/sh
set -eu
[ "$*" = "+1.98.1 metadata --locked --offline --format-version 1" ]
[ -n "$CARGO_HOME" ]
[ -d "$CARGO_HOME" ]
[ -z "$(ls -A "$CARGO_HOME")" ]
[ -f Cargo.lock ]
[ -f vendor/dep-1.0/.cargo-checksum.json ]
[ -f libs/hbb_common/common.rs ]
printf 'offline verified\n' > "$RELEASE_TEST_CARGO_MARKER"
`, 0o755)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RELEASE_TEST_CARGO_MARKER", marker)
	releaseTestMockBuild(t, tool, upstream)
	if err := tool.SourceBundle(upstream, vendor, config, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if got := string(releaseTestRead(t, marker)); got != "offline verified\n" {
		t.Fatalf("offline check marker = %q", got)
	}
	filename := filepath.Join(tool.Root, "dist", "rustdesk-server-macos-0.1.0-source.tar.gz")
	members, _, _ := releaseTestReadArchive(t, filename)
	for name, want := range map[string]string{"upstream/Cargo.lock": "locked", "upstream/LICENSE": "upstream license", "upstream/libs/hbb_common/common.rs": "// pinned common source\n", "upstream/vendor/dep-1.0/NOTICE": "vendored notice", "distribution/LICENSE": "distribution license", "distribution/cmd/helper.go": "package main\n"} {
		if got := string(members[name].data); got != want {
			t.Errorf("source archive %s = %q, want %q", name, got, want)
		}
	}
	for name := range members {
		if strings.Contains(name, ".git/") || strings.Contains(name, "private-") || strings.HasPrefix(name, "distribution/upstream/") {
			t.Errorf("untracked/private content exported: %s", name)
		}
	}
	cfg := string(members["upstream/.cargo/config.toml"].data)
	if !strings.Contains(cfg, "[build]\njobs = 1") || !strings.Contains(cfg, `directory = "vendor"`) || strings.Contains(cfg, vendor) {
		t.Fatalf("source vendor config is not portable or lost original settings: %s", cfg)
	}
	if _, exists := members["upstream/.cargo/config"]; exists {
		t.Fatal("legacy Cargo config was not migrated")
	}
	building := string(members["BUILDING.txt"].data)
	for _, detail := range []string{"Apple Silicon", "Go 1.27.1", "macOS 26", "macOS 15.0", "macOS 15 is not runtime-tested", "--offline", "aarch64-apple-darwin"} {
		if !strings.Contains(building, detail) {
			t.Errorf("build instructions missing %q", detail)
		}
	}
	var source struct {
		Version  string `json:"distribution_version"`
		Commit   string `json:"distribution_commit"`
		LockHash string `json:"cargo_lock_sha256"`
		Upstream Pin
	}
	if err := json.Unmarshal(members["source.json"].data, &source); err != nil {
		t.Fatal(err)
	}
	if source.Version != "0.1.0" || source.Commit != releaseTestDistributionCommit || source.Upstream.Commit != releaseTestCommit || source.LockHash != releaseTestHash([]byte("locked")) {
		t.Fatalf("source provenance = %+v", source)
	}
	before := releaseTestRead(t, filename)
	if err := tool.SourceBundle(upstream, vendor, config, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, releaseTestRead(t, filename)) {
		t.Fatal("source bundle changed across identical runs")
	}
	stages, err := filepath.Glob(filepath.Join(tool.Root, ".build", "source-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("source staging directories not cleaned: %q, %v", stages, err)
	}
}

func TestSourceBundleRejectsEmptyVendor(t *testing.T) {
	tool := releaseTestNew(t)
	upstream := filepath.Join(tool.Root, "upstream")
	releaseTestWrite(t, filepath.Join(upstream, "Cargo.lock"), "locked", 0o644)
	releaseTestMockBuild(t, tool, upstream)
	if err := tool.SourceBundle(upstream, filepath.Join(tool.Root, "empty"), "missing-config", "0.1.0"); err == nil || !strings.Contains(err.Error(), "vendor output is empty") {
		t.Fatalf("empty vendor error = %v", err)
	}
}

func TestExtractSourceRejectsSymlinkDestinationRoot(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	root := filepath.Join(parent, "root-link")
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	data := releaseTestTar(t, releaseTestTarEntry{name: "file", kind: tar.TypeReg, data: "must not escape"})
	if err := extractSource(bytes.NewReader(data), root); err == nil {
		t.Error("symlink extraction root accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "file")); !os.IsNotExist(err) {
		t.Error("archive wrote through symlink extraction root")
	}
}

func TestCollectLicensesPreservesCollidingBasenames(t *testing.T) {
	root := t.TempDir()
	crate := filepath.Join(root, "crate")
	releaseTestWrite(t, filepath.Join(crate, "LICENSE"), "top-level license", 0o644)
	releaseTestWrite(t, filepath.Join(crate, "custom", "LICENSE"), "custom license", 0o644)
	licenseFile := "custom/LICENSE"
	p := cargoPackage{ID: "example 1.0", Name: "example", Version: "1.0", ManifestPath: filepath.Join(crate, "Cargo.toml"), LicenseFile: &licenseFile}
	out := filepath.Join(root, "licenses")
	if err := collectLicenses([]cargoPackage{p}, out); err != nil {
		t.Fatal(err)
	}
	var index []struct{ Files []string }
	releaseTestDecode(t, filepath.Join(out, "index.json"), &index)
	if len(index) != 1 || len(index[0].Files) != 2 {
		t.Fatalf("colliding license inventory = %+v", index)
	}
	contents := map[string]bool{}
	for _, filename := range index[0].Files {
		contents[string(releaseTestRead(t, filepath.Join(out, filepath.FromSlash(filename))))] = true
	}
	if !contents["top-level license"] || !contents["custom license"] {
		t.Fatalf("license basename collision lost original contents: %v", contents)
	}
}
