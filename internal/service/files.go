// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The helper never shells out with user-controlled input. File names and plist
// strings are handled as data, including paths containing spaces or XML tokens.
func cleanPath(p string) (string, error) {
	if !filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\r\n") {
		return "", errors.New("use an absolute path without control characters")
	}
	return filepath.Clean(p), nil
}

func regular(path string) (os.FileInfo, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular file: %s", path)
	}
	return st, nil
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing non-directory or symlink: %s", path)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("directory is not owned by this user: %s", path)
	}
	return os.Chmod(path, 0700)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if st, err := os.Lstat(path); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular destination: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func readRegular(path string) ([]byte, error) {
	if _, err := regular(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// RustDesk 1.1.16 stores libsodium's Ed25519 secret as base64(seed || public).
// Go's ed25519.PrivateKey has the same 64-byte representation. Generating it
// before either daemon starts avoids both a startup race and key-only listeners.
func ensureKeys(dir string) (string, error) {
	secretPath := filepath.Join(dir, "id_ed25519")
	pubPath := secretPath + ".pub"
	data, err := readRegular(secretPath)
	var secret ed25519.PrivateKey
	if os.IsNotExist(err) {
		if _, e := os.Lstat(pubPath); e == nil {
			return "", errors.New("public key exists without private key; restore the private key from backup, do not rotate silently")
		} else if !os.IsNotExist(e) {
			return "", e
		}
		// Match upstream's preference for a base64 public key without '/' or ':'.
		for attempt := 0; attempt < 300; attempt++ {
			_, secret, err = ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return "", errors.New("could not generate server key")
			}
			if !strings.ContainsAny(base64.StdEncoding.EncodeToString(secret[ed25519.SeedSize:]), "/:") {
				break
			}
		}
		// O_EXCL never replaces a key from a concurrent/previous setup.
		f, e := os.OpenFile(secretPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return "", e
		}
		_, e = f.WriteString(base64.StdEncoding.EncodeToString(secret))
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return "", errors.New("could not persist private key; inspect data directory before retrying")
		}
	} else if err != nil {
		return "", err
	} else {
		decoded, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if e != nil || len(decoded) != ed25519.PrivateKeySize {
			return "", errors.New("invalid private key; restore a valid backup (existing key was not replaced)")
		}
		secret = ed25519.PrivateKey(decoded)
		derived := ed25519.NewKeyFromSeed(secret[:ed25519.SeedSize])
		if !bytes.Equal(derived, secret) {
			return "", errors.New("private key failed Ed25519 consistency check (not replaced)")
		}
	}
	public := base64.StdEncoding.EncodeToString(secret[ed25519.SeedSize:])
	if p, e := readRegular(pubPath); e == nil {
		if strings.TrimSpace(string(p)) != public {
			return "", errors.New("public/private key mismatch; restore the matching pair (not replaced)")
		}
	} else if os.IsNotExist(e) {
		if e = atomicWrite(pubPath, []byte(public), 0600); e != nil {
			return "", e
		}
	} else {
		return "", e
	}
	if err := os.Chmod(secretPath, 0600); err != nil {
		return "", err
	}
	if err := os.Chmod(pubPath, 0600); err != nil {
		return "", err
	}
	return public, nil
}

func publicKey(dir string) (string, error) {
	b, err := readRegular(filepath.Join(dir, "id_ed25519.pub"))
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return "", errors.New("invalid public key file")
	}
	return s, nil
}

func readConfig(path string) (*Config, error) {
	b, err := readRegular(path)
	if err != nil {
		return nil, err
	}
	var c Config
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return nil, errors.New("invalid config.json")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid trailing data in config.json")
	}
	if c.Schema != 1 {
		return nil, errors.New("unsupported config schema")
	}
	if cleaned, err := cleanPath(c.DataDir); err != nil {
		return nil, err
	} else if cleaned != c.DataDir {
		return nil, errors.New("data_dir must be a canonical absolute path")
	}
	if err := validateAddress(c.Address); err != nil {
		return nil, err
	}
	return &c, nil
}

func stageBinaries(sourceDir, root string) (string, error) {
	h := sha256.New()
	for _, name := range packageNames {
		src := filepath.Join(sourceDir, name)
		st, err := regular(src)
		if err != nil {
			return "", fmt.Errorf("find bundled %s next to rustdesk-server: %w", name, err)
		}
		if st.Mode()&0111 == 0 {
			return "", fmt.Errorf("not executable: %s", src)
		}
		f, err := os.Open(src)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	id := hex.EncodeToString(h.Sum(nil))
	releases := filepath.Join(root, "releases")
	if err := privateDir(releases); err != nil {
		return "", err
	}
	dest := filepath.Join(releases, id)
	if st, err := os.Lstat(dest); err == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("cached runtime is not a real directory")
		}
		// Verify cached copies; never trust a directory merely because its name matches.
		existing := sha256.New()
		for _, name := range packageNames {
			st, e := regular(filepath.Join(dest, name))
			if e != nil {
				return "", e
			}
			if st.Mode()&0111 == 0 {
				return "", errors.New("cached runtime is not executable")
			}
			b, e := readRegular(filepath.Join(dest, name))
			if e != nil {
				return "", e
			}
			existing.Write(b)
		}
		if hex.EncodeToString(existing.Sum(nil)) != id {
			return "", errors.New("cached runtime checksum mismatch")
		}
		return dest, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	stage, err := os.MkdirTemp(releases, ".stage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	for _, name := range packageNames {
		b, err := readRegular(filepath.Join(sourceDir, name))
		if err != nil {
			return "", err
		}
		if err = atomicWrite(filepath.Join(stage, name), b, 0700); err != nil {
			return "", err
		}
	}
	// Verify the copied bytes, in case the installation changed while copying.
	copied := sha256.New()
	for _, name := range packageNames {
		b, e := readRegular(filepath.Join(stage, name))
		if e != nil {
			return "", e
		}
		copied.Write(b)
	}
	if hex.EncodeToString(copied.Sum(nil)) != id {
		return "", errors.New("package changed during setup; retry after installation completes")
	}
	if err = os.Rename(stage, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func switchRuntime(root, target string) error {
	current := filepath.Join(root, "current")
	if st, err := os.Lstat(current); err == nil && st.Mode()&os.ModeSymlink == 0 {
		return errors.New("current runtime path is not a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	temp, err := os.MkdirTemp(root, ".link-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	link := filepath.Join(temp, "current")
	if err := os.Symlink(target, link); err != nil {
		return err
	}
	return os.Rename(link, current)
}

func lock(root string) (func(), error) {
	if err := privateDir(root); err != nil {
		return nil, err
	}
	p := filepath.Join(root, ".setup.lock")
	if st, err := os.Lstat(p); err == nil && !st.Mode().IsRegular() {
		return nil, errors.New("unsafe setup lock")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another rustdesk-server operation is running")
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
