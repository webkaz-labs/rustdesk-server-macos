package release

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// within rejects paths outside a root without following a symlink.
func within(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func sourceEpoch() (time.Time, error) {
	epoch := int64(0)
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		var err error
		epoch, err = strconv.ParseInt(s, 10, 64)
		if err != nil || epoch < 0 || epoch > 0xffffffff {
			return time.Time{}, errors.New("SOURCE_DATE_EPOCH must fit a nonnegative gzip timestamp")
		}
	}
	return time.Unix(epoch, 0).UTC(), nil
}

// Archive writes a reproducibly ordered archive with normalized owners/modes.
// It does not claim that binaries built with different SDKs are reproducible.
func Archive(source, destination string) error {
	epoch, err := sourceEpoch()
	if err != nil {
		return err
	}
	if within(source, destination) {
		return errors.New("archive destination cannot be inside its source tree")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	gz.Header.ModTime = epoch
	gz.Header.OS = 255
	tarWriter := tar.NewWriter(gz)
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || !within(source, filepath.Join(filepath.Dir(path), link)) {
				return fmt.Errorf("symlink escapes source archive: %s", rel)
			}
		} else if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported archive entry: %s", rel)
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "root", "root"
		header.ModTime, header.AccessTime, header.ChangeTime = epoch, time.Time{}, time.Time{}
		header.Mode = 0o644
		if info.IsDir() || info.Mode().Perm()&0o111 != 0 {
			header.Mode = 0o755
		}
		if info.Mode()&os.ModeSymlink != 0 {
			header.Mode = 0o777
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
	return errors.Join(err, tarWriter.Close(), gz.Close(), out.Close())
}

// safeParents prevents a later archive entry from traversing an earlier symlink.
func safeParents(root, path string) error {
	if !within(root, path) {
		return fmt.Errorf("path escapes source root: %s", path)
	}
	for parent := filepath.Dir(path); parent != filepath.Clean(root); parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive parent is not a real directory: %s", parent)
		}
	}
	return nil
}

func extractSource(reader io.Reader, destination string) error {
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("source extraction root must be a real directory")
	}
	r := tar.NewReader(reader)
	for {
		header, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// git archive emits a global PAX comment recording its commit SHA.
		// Metadata headers are not filesystem entries and need no extraction.
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || name == "." || strings.Contains(name, "\\") || !fs.ValidPath(name) {
			return fmt.Errorf("unsafe archive path: %q", header.Name)
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := safeParents(root, path); err != nil {
			return err
		}
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to overwrite archive symlink: %s", name)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, r)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(header.Linkname) || strings.Contains(header.Linkname, "\\") || !within(root, filepath.Join(filepath.Dir(path), header.Linkname)) {
				return fmt.Errorf("unsafe source symlink: %s", name)
			}
			if err := os.Symlink(header.Linkname, path); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported source tar entry type %d: %s", header.Typeflag, name)
		}
	}
}

func exportGit(repository, destination string) error {
	cmd := exec.Command("git", "archive", "HEAD")
	cmd.Dir = repository
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err = extractSource(stdout, destination)
	if err != nil {
		_ = cmd.Process.Kill()
	}
	return errors.Join(err, cmd.Wait())
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || !within(source, filepath.Join(filepath.Dir(path), link)) {
				return fmt.Errorf("vendored symlink escapes source: %s", path)
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported vendored entry: %s", path)
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		return os.Chmod(target, info.Mode().Perm())
	})
}

func (t *Tool) SourceBundle(upstream, vendor, vendorConfig, version string) error {
	if err := ValidateVersion(version); err != nil {
		return err
	}
	var err error
	upstream, err = filepath.Abs(upstream)
	if err != nil {
		return err
	}
	vendor, err = filepath.Abs(vendor)
	if err != nil {
		return err
	}
	if err := t.CheckUpstream(upstream); err != nil {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(vendor, "*", ".cargo-checksum.json"))
	if err != nil || len(matches) == 0 {
		return errors.New("Cargo vendor output is empty")
	}
	if err := os.MkdirAll(filepath.Join(t.Root, ".build"), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Join(t.Root, ".build"), "source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	upstreamStage := filepath.Join(stage, "upstream")
	if err := exportGit(upstream, upstreamStage); err != nil {
		return err
	}
	for submodule := range t.Pin.Submodules {
		if err := exportGit(filepath.Join(upstream, submodule), filepath.Join(upstreamStage, submodule)); err != nil {
			return err
		}
	}
	if err := exportGit(t.Root, filepath.Join(stage, "distribution")); err != nil {
		return err
	}
	if err := copyTree(vendor, filepath.Join(upstreamStage, "vendor")); err != nil {
		return err
	}
	cfg := filepath.Join(upstreamStage, ".cargo", "config.toml")
	legacy := filepath.Join(upstreamStage, ".cargo", "config")
	if _, err := os.Stat(legacy); err == nil {
		if _, err := os.Stat(cfg); os.IsNotExist(err) {
			if err := os.Rename(legacy, cfg); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		return err
	}
	original, err := os.ReadFile(cfg)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	vendorData, err := os.ReadFile(vendorConfig)
	if err != nil {
		return err
	}
	portable := strings.ReplaceAll(string(vendorData), vendor, "vendor")
	if err := os.WriteFile(cfg, []byte(string(original)+"\n# Distribution source replacement for offline builds.\n"+portable), 0o644); err != nil {
		return err
	}
	// Empty CARGO_HOME establishes offline source resolution independently of the
	// builder's populated crate cache. This is not a second compilation.
	cargoHome, err := os.MkdirTemp("", "rustdesk-cargo-offline-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cargoHome)
	cmd := exec.Command("cargo", "+"+t.Pin.Rust, "metadata", "--locked", "--offline", "--format-version", "1")
	cmd.Dir = upstreamStage
	cmd.Env = append(os.Environ(), "CARGO_HOME="+cargoHome)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("offline source resolution failed: %w", err)
	}
	commit, err := t.run(t.Root, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	lockHash, err := fileHash(filepath.Join(upstream, "Cargo.lock"))
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(stage, "source.json"), map[string]any{"distribution_version": version,
		"distribution_commit": commit, "upstream": t.Pin, "cargo_lock_sha256": lockHash}); err != nil {
		return err
	}
	building := fmt.Sprintf(`Complete corresponding source for rustdesk-server-macos %s

upstream/ contains RustDesk Server %s at %s, all pinned recursive submodules,
Cargo.lock, and every Cargo registry/Git dependency from cargo vendor --locked
--versioned-dirs. Original notices are retained. distribution/ contains the Go
helper, Go release tooling, and exact release workflow/build scripts.

Build prerequisites: Apple Silicon macOS, Apple's Command Line Tools/Xcode SDK,
Rust %s, Go %s, and Git. Official CI tests on macOS 26, declares a macOS %s
deployment minimum, and checks Mach-O load commands. macOS 15 is not runtime-tested
by this workflow. Toolchains and Apple's system SDK are external prerequisites.

After installing prerequisites:
  cd upstream
  cp db_v2.sqlite3 ../build-schema.sqlite3
  DATABASE_URL="sqlite://$(cd .. && pwd)/build-schema.sqlite3" MACOSX_DEPLOYMENT_TARGET=%s cargo +%s build --release --locked --offline --target aarch64-apple-darwin --bin hbbs --bin hbbr
  cd ../distribution
  CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -ldflags '-s -w -X main.version=%s' -o rustdesk-server ./cmd/rustdesk-server

The distribution's Go tooling documents packaging, Mach-O checks, source export,
and Packslip signing. Upstream's tracked sample .env and db_v2.sqlite3 remain in
its source snapshot; neither ships in an installable binary archive. The separate
build-schema.sqlite3 is disposable: SQLx compile-time macros may update it.

RustDesk Server is licensed under AGPL-3.0. See upstream/LICENSE and all dependency
notices. The helper's license is distribution/LICENSE. This source archive is
provided alongside the binaries, not as a source-availability promise.
`, version, t.Pin.Version, t.Pin.Commit, t.Pin.Rust, t.Pin.Go, t.Pin.MinimumMac,
		t.Pin.MinimumMac, t.Pin.Rust, version)
	if err := os.WriteFile(filepath.Join(stage, "BUILDING.txt"), []byte(building), 0o644); err != nil {
		return err
	}
	return Archive(stage, filepath.Join(t.Root, "dist", "rustdesk-server-macos-"+version+"-source.tar.gz"))
}
