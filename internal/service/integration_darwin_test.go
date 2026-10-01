//go:build darwin

// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in on a disposable macOS runner. Uses a unique login-user service and a
// harmless sleep fixture, not the user's production labels or network daemons.
func TestLaunchdIntegration(t *testing.T) {
	if os.Getenv("RUSTDESK_MACOS_INTEGRATION") != "1" {
		t.Skip("set RUSTDESK_MACOS_INTEGRATION=1 on a disposable macOS CI runner")
	}
	m, _, c, src := fixture(t)
	real := newManager(m.Home, os.Getuid())
	m.Command = func(args ...string) (string, error) {
		if len(args) == 3 && args[0] == "bootstrap" {
			if output, err := exec.Command("/usr/bin/plutil", "-lint", args[2]).CombinedOutput(); err != nil {
				return string(output), fmt.Errorf("invalid fixture plist: %w (%s)", err, output)
			}
		}
		output, err := real.Command(args...)
		if err != nil && len(args) == 3 && args[0] == "bootstrap" {
			// Restrict diagnostic logs to this disposable fixture's unique label.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			diagnostic, _ := exec.CommandContext(ctx, "/usr/bin/log", "show", "--last", "1m", "--style", "compact", "--predicate", fmt.Sprintf("process == \"launchd\" AND eventMessage CONTAINS \"%s\"", m.Prefix)).CombinedOutput()
			t.Logf("scoped launchd diagnostic: %s", diagnostic)
		}
		return output, err
	}
	// Exercise the exact GUI/login-session domain used by production rather
	// than relying on different behavior in the user/UID domain.
	m.Domain = fmt.Sprintf("gui/%d", os.Getuid())
	if err := m.available(); err != nil {
		t.Fatalf("macOS runner requires a GUI login session for this fixture: %v", err)
	}
	m.Prefix = fmt.Sprintf("com.webkaz-labs.rustdesk-server.ci.%d.%d", os.Getpid(), time.Now().UnixNano())
	for _, name := range packageNames {
		if err := os.WriteFile(filepath.Join(src, name), []byte("#!/bin/sh\nexec /bin/sleep 3600\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	m.Wait = func(m *Manager) error {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			ok := true
			for _, name := range daemonNames {
				out, err := m.Command("print", m.target(name))
				if err != nil || !running(out) {
					ok = false
				}
			}
			if ok {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("fixture did not start")
	}
	t.Cleanup(func() {
		for _, name := range daemonNames {
			m.Command("bootout", m.target(name))
		}
	})
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	key := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
	if err := m.Setup(c, src); err != nil {
		t.Fatal("repeat setup", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, name := range daemonNames {
		loaded, err := m.loaded(name)
		if err != nil || loaded {
			t.Fatalf("still loaded: %s %v", name, err)
		}
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	if string(key) != string(mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
		t.Fatal("identity changed")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
}
