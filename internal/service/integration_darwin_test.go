//go:build darwin

// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in on a disposable macOS runner. Uses a unique user-domain service and a
// harmless sleep fixture, not the user's production labels or network daemons.
func TestLaunchdIntegration(t *testing.T) {
	if os.Getenv("RUSTDESK_MACOS_INTEGRATION") != "1" {
		t.Skip("set RUSTDESK_MACOS_INTEGRATION=1 on a disposable macOS CI runner")
	}
	m, _, c, src := fixture(t)
	real := newManager(m.Home, os.Getuid())
	m.Command = real.Command
	m.Domain = fmt.Sprintf("user/%d", os.Getuid())
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
