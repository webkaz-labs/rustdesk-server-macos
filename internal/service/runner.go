// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Check on EVERY launchd restart, not just interactive setup. Upstream may
// generate a new key if its key file disappears; fail closed before exec instead.
// This is not a sandbox against another process controlled by the same user.
func validateIdentity(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return problem("data path is not a real directory")
	}
	if st.Mode().Perm()&0077 != 0 {
		return problem("data directory must have owner-only permissions (0700)")
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return problem("data directory has a different owner")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".env")); err == nil {
		return problem(".env overrides are not allowed in managed data")
	} else if !os.IsNotExist(err) {
		return err
	}
	p := filepath.Join(dir, "id_ed25519")
	st, err = regular(p)
	if err != nil {
		return problem("private key missing/unreadable; restore a backup, no replacement generated")
	}
	if st.Mode().Perm()&0077 != 0 {
		return problem("private key must have owner-only permissions (0600)")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return problem("private key unreadable; no replacement generated")
	}
	secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(secret) != ed25519.PrivateKeySize {
		return problem("private key invalid; no replacement generated")
	}
	if !bytes.Equal(secret, ed25519.NewKeyFromSeed(secret[:ed25519.SeedSize])) {
		return problem("private key consistency check failed")
	}
	pub, err := publicKey(dir)
	if err != nil {
		return err
	}
	if pub != base64.StdEncoding.EncodeToString(secret[ed25519.SeedSize:]) {
		return problem("public/private key mismatch")
	}
	return nil
}

func prepareDaemon(root, name, home string) (*Config, []string, []string, error) {
	if name != "hbbs" && name != "hbbr" {
		return nil, nil, nil, problem("invalid internal daemon name")
	}
	if _, err := cleanPath(root); err != nil {
		return nil, nil, nil, err
	}
	c, err := readConfig(filepath.Join(root, "config.json"))
	if err != nil {
		return nil, nil, nil, err
	}
	if err = validateIdentity(c.DataDir); err != nil {
		return nil, nil, nil, err
	}
	path := filepath.Join(root, "current", name)
	if st, err := regular(path); err != nil || st.Mode()&0111 == 0 {
		return nil, nil, nil, problem("managed executable missing or not executable")
	}
	args := []string{path, "-k", "_"}
	if name == "hbbs" {
		args = append(args, "-r", net.JoinHostPort(c.Address, "21117"), "-p", "21116")
	} else {
		args = append(args, "-p", "21117")
	}
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "RUST_LOG=info"}
	return c, args, env, nil
}
func runDaemon(root, name, home string) error {
	c, args, env, err := prepareDaemon(root, name, home)
	if err != nil {
		return err
	}
	if err = os.Chdir(c.DataDir); err != nil {
		return err
	}
	syscall.Umask(0077)
	if err = syscall.Exec(args[0], args, env); err != nil {
		return problem("exec %s: %w", name, err)
	}
	return nil
}
