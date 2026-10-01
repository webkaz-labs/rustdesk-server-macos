// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var daemonNames = []string{"hbbs", "hbbr"}
var packageNames = []string{"hbbs", "hbbr", "rustdesk-server"}

const managedMarker = "rustdesk-server-macos/v1"

type Config struct {
	Schema         int    `json:"schema"`
	Address        string `json:"address"`
	DataDir        string `json:"data_dir"`
	PackageVersion string `json:"package_version"`
}

var errServiceAbsent = problem("launchd service is not loaded")

type Commander func(args ...string) (string, error)
type Manager struct {
	executable                         func() (string, error)
	Language                           language
	Home, Root, Agents, Domain, Prefix string
	Command                            Commander
	Wait                               func(*Manager) error
	CheckPorts                         func() error
}

func newManager(home string, uid int) *Manager {
	m := &Manager{Home: home, Root: filepath.Join(home, "Library", "Application Support", "rustdesk-server"), Agents: filepath.Join(home, "Library", "LaunchAgents"), Domain: "gui/" + strconv.Itoa(uid), Prefix: "com.webkaz-labs.rustdesk-server"}
	m.executable = os.Executable
	m.Command = func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		b, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
		if err != nil {
			var exitErr *exec.ExitError
			if len(args) == 2 && args[0] == "print" && strings.Count(args[1], "/") == 2 && errors.As(err, &exitErr) && exitErr.ExitCode() == 113 && strings.Contains(string(b), "Could not find service") {
				return string(b), errServiceAbsent
			}
			return string(b), problem("launchctl %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
		}
		return string(b), nil
	}
	m.Wait = waitReady
	m.CheckPorts = checkPorts
	return m
}
func (m *Manager) label(name string) string  { return m.Prefix + "." + name }
func (m *Manager) target(name string) string { return m.Domain + "/" + m.label(name) }
func (m *Manager) plistPath(name string) string {
	return filepath.Join(m.Agents, m.label(name)+".plist")
}
func (m *Manager) configPath() string { return filepath.Join(m.Root, "config.json") }
func (m *Manager) loaded(name string) (bool, error) {
	_, err := m.Command("print", m.target(name))
	if errors.Is(err, errServiceAbsent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
func (m *Manager) available() error {
	if _, err := m.Command("print", m.Domain); err != nil {
		return problem("a macOS graphical login session is required; run this in Terminal as the logged-in user, without sudo")
	}
	return nil
}
func (m *Manager) bootout(name string) error {
	loaded, err := m.loaded(name)
	if err != nil {
		return err
	}
	if !loaded {
		return nil
	}
	_, err = m.Command("bootout", m.target(name))
	return err
}
func (m *Manager) bootstrap(name string) error {
	_, err := m.Command("bootstrap", m.Domain, m.plistPath(name))
	return err
}
func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func plist(m *Manager, c *Config, name string) []byte {
	args := []string{filepath.Join(m.Root, "current", "rustdesk-server"), "run-service", m.Root, name}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
	for _, kv := range [][2]string{{"Label", m.label(name)}, {"RustDeskServerManaged", managedMarker}, {"WorkingDirectory", c.DataDir}, {"StandardOutPath", filepath.Join(m.Root, "logs", name+".log")}, {"StandardErrorPath", filepath.Join(m.Root, "logs", name+".error.log")}} {
		fmt.Fprintf(&b, "<key>%s</key><string>%s</string>\n", kv[0], xmlText(kv[1]))
	}
	b.WriteString("<key>ProgramArguments</key><array>\n")
	for _, arg := range args {
		fmt.Fprintf(&b, "<string>%s</string>\n", xmlText(arg))
	}
	b.WriteString("</array>\n")
	// The native run-service entry point replaces the environment before exec.
	b.WriteString("<key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin:/bin:/usr/sbin:/sbin</string></dict>\n<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n<key>ThrottleInterval</key><integer>10</integer>\n<key>Umask</key><integer>63</integer>\n<key>ProcessType</key><string>Background</string>\n</dict></plist>\n")
	return []byte(b.String())
}

type snapshot struct {
	files   map[string][]byte
	link    string
	hasLink bool
	loaded  map[string]bool
}

func (m *Manager) snapshot() (*snapshot, error) {
	s := &snapshot{files: map[string][]byte{}, loaded: map[string]bool{}}
	paths := []string{m.configPath()}
	for _, name := range daemonNames {
		p := m.plistPath(name)
		paths = append(paths, p)
		if b, err := readRegular(p); err == nil && !bytes.Contains(b, []byte("<string>"+managedMarker+"</string>")) {
			return nil, problem("refusing to replace an unmanaged LaunchAgent: %s", p)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		loaded, err := m.loaded(name)
		if err != nil {
			return nil, err
		}
		s.loaded[name] = loaded
		if s.loaded[name] {
			if _, err := regular(p); err != nil {
				return nil, problem("loaded service has no managed plist: %s", name)
			}
		}
	}
	for _, p := range paths {
		b, err := readRegular(p)
		if err == nil {
			s.files[p] = b
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	target, err := os.Readlink(filepath.Join(m.Root, "current"))
	if err == nil {
		s.link = target
		s.hasLink = true
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}
func (m *Manager) restore(s *snapshot) error {
	var errs []error
	for _, name := range daemonNames {
		if err := m.bootout(name); err != nil {
			errs = append(errs, err)
		}
	}
	paths := []string{m.configPath()}
	for _, name := range daemonNames {
		paths = append(paths, m.plistPath(name))
	}
	for _, p := range paths {
		if b, ok := s.files[p]; ok {
			if err := atomicWrite(p, b, 0600); err != nil {
				errs = append(errs, err)
			}
		} else if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if s.hasLink {
		if err := switchRuntime(m.Root, s.link); err != nil {
			errs = append(errs, err)
		}
	} else if err := os.Remove(filepath.Join(m.Root, "current")); err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	}
	for _, name := range daemonNames {
		if s.loaded[name] {
			if err := m.bootstrap(name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Give every mutating operation the same explicit recovery result.
func (m *Manager) restoreError(s *snapshot, cause error) error {
	if restoreErr := m.restore(s); restoreErr != nil {
		return problem("%w; rollback also failed: %v; inspect status before retrying", cause, restoreErr)
	}
	return problem("%w; previous service configuration restored; data and keys preserved", cause)
}

func (m *Manager) Setup(c *Config, sourceDir string) (err error) {
	unlock, err := lock(m.Root)
	if err != nil {
		return err
	}
	defer unlock()
	if err = m.available(); err != nil {
		return err
	}
	if old, e := readConfig(m.configPath()); e == nil {
		if old.DataDir != c.DataDir {
			return problem("data directory differs from existing setup; keep it unchanged to preserve server identity (manual migration requires a backup)")
		}
		if _, e = regular(filepath.Join(c.DataDir, "id_ed25519")); e != nil {
			return problem("existing setup private key is missing; restore a backup before setup (identity will not be regenerated)")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if err = privateDir(c.DataDir); err != nil {
		return err
	}
	// .env can override keys/ports upstream. This managed mode deliberately refuses it.
	if _, e := os.Lstat(filepath.Join(c.DataDir, ".env")); e == nil {
		return problem("managed data directory contains .env; remove or migrate it before setup because upstream environment overrides can disable key checks")
	} else if !os.IsNotExist(e) {
		return e
	}
	if _, err = ensureKeys(c.DataDir); err != nil {
		return err
	}
	runtime, err := stageBinaries(sourceDir, m.Root)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(m.Agents, 0700); err != nil {
		return err
	}
	if st, e := os.Lstat(m.Agents); e != nil || !st.IsDir() {
		return problem("LaunchAgents directory is invalid")
	}
	if err = privateDir(filepath.Join(m.Root, "logs")); err != nil {
		return err
	}
	s, err := m.snapshot()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = m.restoreError(s, err)
		}
	}()
	for _, name := range daemonNames {
		if err = m.bootout(name); err != nil {
			return err
		}
	}
	if err = m.CheckPorts(); err != nil {
		return err
	}
	if err = switchRuntime(m.Root, runtime); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicWrite(m.configPath(), append(b, '\n'), 0600); err != nil {
		return err
	}
	for _, name := range daemonNames {
		if err = atomicWrite(m.plistPath(name), plist(m, c, name), 0600); err != nil {
			return err
		}
	}
	for _, name := range daemonNames {
		if err = m.bootstrap(name); err != nil {
			return err
		}
	}
	return m.Wait(m)
}

func (m *Manager) Start() (err error) {
	unlock, err := lock(m.Root)
	if err != nil {
		return err
	}
	defer unlock()
	if err = m.available(); err != nil {
		return err
	}
	c, err := readConfig(m.configPath())
	if err != nil {
		return problem("run rustdesk-server setup first: %w", err)
	}
	if _, err = regular(filepath.Join(c.DataDir, "id_ed25519")); err != nil {
		return problem("private key missing; restore a backup before start")
	}
	if err = validateIdentity(c.DataDir); err != nil {
		return err
	}
	if _, e := os.Lstat(filepath.Join(c.DataDir, ".env")); e == nil {
		return problem("managed data directory contains .env; refusing to start")
	} else if !os.IsNotExist(e) {
		return e
	}
	for _, name := range packageNames {
		st, e := regular(filepath.Join(m.Root, "current", name))
		if e != nil {
			return problem("runtime missing; rerun setup: %w", e)
		}
		if st.Mode()&0111 == 0 {
			return problem("runtime %s is not executable; rerun setup", name)
		}
	}
	s, err := m.snapshot()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = m.restoreError(s, err)
		}
	}()
	if !s.loaded["hbbs"] && !s.loaded["hbbr"] {
		if err = m.CheckPorts(); err != nil {
			return err
		}
	}
	for _, name := range daemonNames {
		if s.loaded[name] {
			continue
		}
		if err = atomicWrite(m.plistPath(name), plist(m, c, name), 0600); err != nil {
			return err
		}
		if err = m.bootstrap(name); err != nil {
			return err
		}
	}
	return m.Wait(m)
}

func (m *Manager) Stop() (err error) {
	unlock, err := lock(m.Root)
	if err != nil {
		return err
	}
	defer unlock()
	if err = m.available(); err != nil {
		return err
	}
	s, err := m.snapshot()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = m.restoreError(s, err)
		}
	}()
	for _, name := range daemonNames {
		if err = m.bootout(name); err != nil {
			return err
		}
		if err = os.Remove(m.plistPath(name)); os.IsNotExist(err) {
			err = nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func checkPorts() error {
	// Test all listeners together, then close before launchd takes ownership.
	var closers []interface{ Close() error }
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	for p := 21115; p <= 21119; p++ {
		l, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(p)))
		if err != nil {
			return problem("TCP port %d is unavailable; stop the other server first", p)
		}
		closers = append(closers, l)
	}
	u, err := net.ListenPacket("udp", ":21116")
	if err != nil {
		return problem("UDP port 21116 is unavailable")
	}
	closers = append(closers, u)
	return nil
}
func running(out string) bool {
	return strings.Contains(out, "state = running") && strings.Contains(out, "pid = ")
}
func waitReady(m *Manager) error {
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, name := range daemonNames {
			out, err := m.Command("print", m.target(name))
			if err != nil || !running(out) {
				ready = false
			}
		}
		if ready {
			for p := 21115; p <= 21119; p++ {
				c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), 150*time.Millisecond)
				if err != nil {
					ready = false
				} else {
					c.Close()
				}
			}
		}
		if ready {
			time.Sleep(400 * time.Millisecond)
			for _, name := range daemonNames {
				out, err := m.Command("print", m.target(name))
				if err != nil || !running(out) {
					ready = false
				}
			}
			if ready {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return problem("services did not become ready; inspect logs under %s", filepath.Join(m.Root, "logs"))
}
