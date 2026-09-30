// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type fakeLaunchd struct {
	loaded   map[string]bool
	failName string
	calls    [][]string
	domain   string
}

func (f *fakeLaunchd) command(args ...string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	switch args[0] {
	case "print":
		if args[1] == f.domain {
			return "domain", nil
		}
		if f.loaded[args[1]] {
			return "state = running\n pid = 1234", nil
		}
		return "", errServiceAbsent
	case "bootstrap":
		name := strings.TrimSuffix(filepath.Base(args[2]), ".plist")
		if strings.HasSuffix(name, f.failName) && f.failName != "" {
			f.failName = ""
			return "", errors.New("injected bootstrap failure")
		}
		if _, err := os.Stat(args[2]); err != nil {
			return "", err
		}
		if f.loaded[f.domain+"/"+name] {
			return "", errors.New("already loaded")
		}
		f.loaded[f.domain+"/"+name] = true
		return "", nil
	case "bootout":
		delete(f.loaded, args[1])
		return "", nil
	}
	return "", fmt.Errorf("unexpected command %v", args)
}
func fixture(t *testing.T) (*Manager, *fakeLaunchd, *Config, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "Home space & <literal> 日本")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	m := newManager(home, 501)
	fake := &fakeLaunchd{loaded: map[string]bool{}, domain: m.Domain}
	m.Command = fake.command
	m.Wait = func(*Manager) error { return nil }
	m.CheckPorts = func() error { return nil }
	src := filepath.Join(t.TempDir(), "package bin")
	os.MkdirAll(src, 0700)
	for _, name := range packageNames {
		os.WriteFile(filepath.Join(src, name), []byte("#!/bin/sh\nexit 0\n#"+name), 0700)
	}
	c := &Config{Schema: 1, Address: "192.168.1.20", DataDir: filepath.Join(home, "Persistent data"), PackageVersion: "1.1.16-1"}
	return m, fake, c, src
}
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestKeysCompatibleAndPreserved(t *testing.T) {
	dir := t.TempDir()
	public, err := ensureKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	secret := mustRead(t, filepath.Join(dir, "id_ed25519"))
	decoded, err := base64.StdEncoding.DecodeString(string(secret))
	if err != nil || len(decoded) != 64 {
		t.Fatal("wrong secret format")
	}
	pub, _ := base64.StdEncoding.DecodeString(public)
	sig := ed25519.Sign(ed25519.PrivateKey(decoded), []byte("fixture"))
	if !ed25519.Verify(pub, []byte("fixture"), sig) {
		t.Fatal("not Ed25519 compatible")
	}
	got, err := ensureKeys(dir)
	if err != nil || got != public || !bytes.Equal(secret, mustRead(t, filepath.Join(dir, "id_ed25519"))) {
		t.Fatal("key changed")
	}
	for _, name := range []string{"id_ed25519", "id_ed25519.pub"} {
		st, _ := os.Stat(filepath.Join(dir, name))
		if st.Mode().Perm() != 0600 {
			t.Fatal("key permissions")
		}
	}
}
func TestKeysMissingPublicRecoverWithoutRotation(t *testing.T) {
	dir := t.TempDir()
	pub, _ := ensureKeys(dir)
	secret := mustRead(t, filepath.Join(dir, "id_ed25519"))
	os.Remove(filepath.Join(dir, "id_ed25519.pub"))
	got, err := ensureKeys(dir)
	if err != nil || got != pub || !bytes.Equal(secret, mustRead(t, filepath.Join(dir, "id_ed25519"))) {
		t.Fatal("recovery changed identity", err)
	}
}
func TestKeysRefuseCorruption(t *testing.T) {
	for _, kind := range []string{"bad-secret", "mismatched-public", "public-only", "symlink-secret", "inconsistent-secret"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			ensureKeys(dir)
			p := filepath.Join(dir, "id_ed25519")
			switch kind {
			case "bad-secret":
				os.WriteFile(p, []byte("bad"), 0600)
			case "mismatched-public":
				os.WriteFile(p+".pub", []byte("not-the-key"), 0600)
			case "public-only":
				os.Remove(p)
			case "symlink-secret":
				os.Remove(p)
				os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), p)
			case "inconsistent-secret":
				b := make([]byte, 64)
				os.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(b)), 0600)
			}
			before, _ := os.ReadFile(p)
			if _, err := ensureKeys(dir); err == nil {
				t.Fatal("expected failure")
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Fatal("modified bad secret")
			}
		})
	}
}
func TestSetupRepeatUpgradeAndMiseRemoval(t *testing.T) {
	m, f, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	key := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
	first, _ := os.Readlink(filepath.Join(m.Root, "current"))
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	second, _ := os.Readlink(filepath.Join(m.Root, "current"))
	if first != second {
		t.Fatal("identical package should reuse runtime")
	}
	os.WriteFile(filepath.Join(src, "hbbs"), []byte("#!/bin/sh\nexit 0\n#new"), 0700)
	c.PackageVersion = "next"
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	third, _ := os.Readlink(filepath.Join(m.Root, "current"))
	if third == first {
		t.Fatal("upgrade did not switch runtime")
	}
	if !bytes.Equal(key, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
		t.Fatal("upgrade rotated key")
	}
	os.RemoveAll(src)
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if len(f.loaded) != 0 {
		t.Fatal("still loaded")
	}
	for _, name := range daemonNames {
		if _, err := os.Stat(m.plistPath(name)); !os.IsNotExist(err) {
			t.Fatal("stop should prevent next login autostart")
		}
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	if len(f.loaded) != 2 {
		t.Fatal("runtime depended on mise installation")
	}
	if err := m.Start(); err != nil {
		t.Fatal("start should be idempotent", err)
	}
}
func TestSetupRollbackOnSecondBootstrap(t *testing.T) {
	m, f, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	oldConfig := mustRead(t, m.configPath())
	oldPlist := mustRead(t, m.plistPath("hbbs"))
	oldLink, _ := os.Readlink(filepath.Join(m.Root, "current"))
	key := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
	os.WriteFile(filepath.Join(src, "hbbs"), []byte("new-binary"), 0700)
	c.Address = "192.168.1.99"
	f.failName = "hbbr"
	err := m.Setup(c, src)
	if err == nil {
		t.Fatal("expected failure")
	}
	if !bytes.Equal(oldConfig, mustRead(t, m.configPath())) || !bytes.Equal(oldPlist, mustRead(t, m.plistPath("hbbs"))) {
		t.Fatal("configuration not restored")
	}
	link, _ := os.Readlink(filepath.Join(m.Root, "current"))
	if oldLink != link || len(f.loaded) != 2 {
		t.Fatal("runtime or services not restored")
	}
	if !bytes.Equal(key, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
		t.Fatal("key changed")
	}
}
func TestFirstSetupFailureKeepsKeysAndCleansServices(t *testing.T) {
	m, f, c, src := fixture(t)
	f.failName = "hbbr"
	if err := m.Setup(c, src); err == nil {
		t.Fatal("expected failure")
	}
	if len(f.loaded) != 0 {
		t.Fatal("partial service leaked")
	}
	if _, err := os.Stat(m.configPath()); !os.IsNotExist(err) {
		t.Fatal("partial config leaked")
	}
	key := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
		t.Fatal("retry rotated identity")
	}
}
func TestRollbackOnReadinessAndPortFailures(t *testing.T) {
	for _, which := range []string{"ports", "readiness"} {
		t.Run(which, func(t *testing.T) {
			m, f, c, src := fixture(t)
			if which == "ports" {
				m.CheckPorts = func() error { return errors.New("in use") }
			} else {
				m.Wait = func(*Manager) error { return errors.New("not ready") }
			}
			if err := m.Setup(c, src); err == nil {
				t.Fatal("expected failure")
			}
			if len(f.loaded) != 0 {
				t.Fatal("service leaked")
			}
		})
	}
}
func TestRefuseDataMigrationAndMissingIdentity(t *testing.T) {
	m, _, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	copy := *c
	copy.DataDir = filepath.Join(t.TempDir(), "new")
	if err := m.Setup(&copy, src); err == nil {
		t.Fatal("allowed implicit migration")
	}
	os.Remove(filepath.Join(c.DataDir, "id_ed25519"))
	os.Remove(filepath.Join(c.DataDir, "id_ed25519.pub"))
	if err := m.Setup(c, src); err == nil {
		t.Fatal("regenerated configured identity")
	}
	if err := m.Start(); err == nil {
		t.Fatal("start regenerated identity")
	}
}
func TestRejectUnmanagedPlist(t *testing.T) {
	m, _, c, src := fixture(t)
	os.MkdirAll(m.Agents, 0700)
	os.WriteFile(m.plistPath("hbbs"), []byte("unmanaged"), 0600)
	if err := m.Setup(c, src); err == nil {
		t.Fatal("overwrote unmanaged plist")
	}
	if string(mustRead(t, m.plistPath("hbbs"))) != "unmanaged" {
		t.Fatal("changed unmanaged file")
	}
}
func TestRejectDataEnvAndSymlink(t *testing.T) {
	for _, which := range []string{"env", "symlink"} {
		t.Run(which, func(t *testing.T) {
			m, _, c, src := fixture(t)
			if which == "env" {
				os.MkdirAll(c.DataDir, 0700)
				os.WriteFile(filepath.Join(c.DataDir, ".env"), []byte("KEY="), 0600)
			} else {
				os.Symlink(t.TempDir(), c.DataDir)
			}
			if err := m.Setup(c, src); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}
func TestPlistEscapesArgumentsAndNeverSecret(t *testing.T) {
	m, _, c, _ := fixture(t)
	b := plist(m, c, "hbbs")
	decoder := xml.NewDecoder(bytes.NewReader(b))
	var stringsFound []string
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if el, ok := tok.(xml.StartElement); ok && el.Name.Local == "string" {
			var v string
			if err := decoder.DecodeElement(&v, &el); err != nil {
				t.Fatal(err)
			}
			stringsFound = append(stringsFound, v)
		}
	}
	for _, want := range []string{filepath.Join(m.Root, "current", "rustdesk-server"), c.DataDir, m.Root, "hbbs"} {
		found := false
		for _, got := range stringsFound {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("lost exact argument %q", want)
		}
	}
	if bytes.Contains(b, []byte("/bin/sh")) || bytes.Contains(b, []byte("id_ed25519")) {
		t.Fatal("shell or secret in plist")
	}
}
func TestAddressValidation(t *testing.T) {
	for _, good := range []string{"192.168.1.2", "10.0.0.1", "server.local", "fd00::123", "my-mac"} {
		if err := validateAddress(good); err != nil {
			t.Errorf("reject %s: %v", good, err)
		}
	}
	for _, bad := range []string{"", "127.0.0.1", "0.0.0.0", "localhost:21116", "http://host", "x\ny", "x;y", "-x", "x..y", "a b", "::", "::1", "[fd00::123]", "169.254.1.1"} {
		if err := validateAddress(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
func TestPromptRequiresCompleteInput(t *testing.T) {
	m, _, _, _ := fixture(t)
	var out bytes.Buffer
	if err := setupCLI(m, []string{"--yes"}, strings.NewReader(""), &out, "test"); err == nil {
		t.Fatal("yes without address")
	}
	if err := setupCLI(m, []string{"--address", "192.168.1.2"}, strings.NewReader(""), &out, "test"); err == nil {
		t.Fatal("EOF should abort")
	}
	if _, err := os.Stat(m.Root); !os.IsNotExist(err) {
		t.Fatal("aborted before confirmation wrote files")
	}
}
func TestDeclinePlanMakesNoChanges(t *testing.T) {
	m, _, _, _ := fixture(t)
	var out bytes.Buffer
	err := setupCLI(m, []string{"--address", "192.168.1.2", "--data-dir", filepath.Join(m.Home, "data")}, strings.NewReader("n\n"), &out, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Root); !os.IsNotExist(err) {
		t.Fatal("cancel wrote files")
	}
	if !strings.Contains(out.String(), "ALL network interfaces") {
		t.Fatal("exposure warning missing")
	}
}
func TestNoPrivateKeyOutput(t *testing.T) {
	m, _, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := showStatus(m, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), string(mustRead(t, filepath.Join(c.DataDir, "id_ed25519")))) {
		t.Fatal("private key leaked")
	}
	if !strings.Contains(out.String(), string(mustRead(t, filepath.Join(c.DataDir, "id_ed25519.pub")))) {
		t.Fatal("public key omitted")
	}
}
func TestNonMacRefusalAndHelp(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"version"}, strings.NewReader(""), &out, "test"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "darwin" {
		if err := Run([]string{"setup", "--yes", "--address", "192.168.1.2"}, strings.NewReader(""), &out, "test"); err == nil || !strings.Contains(err.Error(), "requires macOS") {
			t.Fatal("non-Mac accepted")
		}
	}
}
func TestLockAndAtomicSymlinkRefusal(t *testing.T) {
	root := t.TempDir()
	unlock, err := lock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if u, err := lock(root); err == nil {
		u()
		t.Fatal("concurrent lock accepted")
	}
	p := filepath.Join(root, "link")
	dest := filepath.Join(root, "dest")
	os.WriteFile(dest, []byte("keep"), 0600)
	os.Symlink(dest, p)
	if err := atomicWrite(p, []byte("replace"), 0600); err == nil {
		t.Fatal("symlink accepted")
	}
	if !reflect.DeepEqual(mustRead(t, dest), []byte("keep")) {
		t.Fatal("modified target")
	}
}

func TestRunnerValidatesIdentityAndCleansEnvironment(t *testing.T) {
	m, _, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEY", "evil")
	t.Setenv("DB_URL", "outside-data.db")
	t.Setenv("PORT", "9999")
	got, args, env, err := prepareDaemon(m.Root, "hbbs", m.Home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(m.Root, "current", "hbbs"), "-k", "_", "-r", "192.168.1.20:21117", "-p", "21116"}
	if !reflect.DeepEqual(want, args) || got.DataDir != c.DataDir {
		t.Fatal("bad daemon args", args)
	}
	if strings.Contains(strings.Join(env, "\n"), "DB_URL=") || strings.Contains(strings.Join(env, "\n"), "KEY=") || strings.Contains(strings.Join(env, "\n"), "PORT=") {
		t.Fatal("inherited unsafe environment")
	}
	for _, kind := range []string{"key-missing", "env", "public-missing", "public-mismatch", "data-symlink"} {
		t.Run(kind, func(t *testing.T) {
			m, _, c, src := fixture(t)
			if err := m.Setup(c, src); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(c.DataDir, "id_ed25519")
			switch kind {
			case "key-missing":
				os.Remove(p)
			case "env":
				os.WriteFile(filepath.Join(c.DataDir, ".env"), []byte("KEY="), 0600)
			case "public-missing":
				os.Remove(p + ".pub")
			case "public-mismatch":
				os.WriteFile(p+".pub", []byte(base64.StdEncoding.EncodeToString(make([]byte, 32))), 0600)
			case "data-symlink":
				os.Rename(c.DataDir, c.DataDir+"-moved")
				os.Symlink(c.DataDir+"-moved", c.DataDir)
			}
			if _, _, _, err := prepareDaemon(m.Root, "hbbs", m.Home); err == nil {
				t.Fatal("runner did not fail closed")
			}
			if kind == "key-missing" {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatal("runner generated key")
				}
			}
		})
	}
}
func TestTransientPrintErrorDoesNotPretendStopped(t *testing.T) {
	m, f, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, m.plistPath("hbbs"))
	m.Command = func(args ...string) (string, error) {
		if args[0] == "print" && args[1] == m.target("hbbs") {
			return "", errors.New("transient launchctl timeout")
		}
		return f.command(args...)
	}
	if err := m.Stop(); err == nil {
		t.Fatal("reported false success")
	}
	if !bytes.Equal(before, mustRead(t, m.plistPath("hbbs"))) || len(f.loaded) != 2 {
		t.Fatal("modified service after unknown status")
	}
}
func TestStopRollbackAndPartialStart(t *testing.T) {
	m, f, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	fail := true
	m.Command = func(args ...string) (string, error) {
		if fail && args[0] == "bootout" && args[1] == m.target("hbbr") {
			fail = false
			return "", errors.New("injected bootout failure")
		}
		return f.command(args...)
	}
	if err := m.Stop(); err == nil {
		t.Fatal("expected stop failure")
	}
	if len(f.loaded) != 2 {
		t.Fatal("stop rollback did not restore both")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	f.failName = "hbbr"
	if err := m.Start(); err == nil {
		t.Fatal("expected partial-start failure")
	}
	if len(f.loaded) != 0 {
		t.Fatal("failed start leaked service")
	}
	for _, name := range daemonNames {
		if _, err := os.Stat(m.plistPath(name)); !os.IsNotExist(err) {
			t.Fatal("failed start left autostart")
		}
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	m.bootout("hbbr")
	f.failName = "hbbr"
	if err := m.Start(); err == nil {
		t.Fatal("expected partial-start failure")
	}
	if !f.loaded[m.target("hbbs")] || f.loaded[m.target("hbbr")] {
		t.Fatal("partial old state not restored")
	}
}

func TestMissingRunnerAndUnsafeCacheRefused(t *testing.T) {
	m, _, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(m.Root, "current", "rustdesk-server"))
	if err := m.Start(); err == nil {
		t.Fatal("accepted missing native runner")
	}
	m, _, c, src = fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(m.Root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	os.Rename(target, target+"-elsewhere")
	os.Symlink(target+"-elsewhere", target)
	if _, err := stageBinaries(src, m.Root); err == nil {
		t.Fatal("cached symlink accepted")
	}
}
func TestConfigRejectsNoncanonicalDataPath(t *testing.T) {
	m, _, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	c.DataDir += "/../different"
	b, _ := json.Marshal(c)
	os.WriteFile(m.configPath(), b, 0600)
	if _, err := readConfig(m.configPath()); err == nil {
		t.Fatal("accepted noncanonical data path")
	}
}
