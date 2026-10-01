// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const connectedTailscaleJSON = `{"BackendState":"Running","Self":{"TailscaleIPs":["fd7a:115c:a1e0::1","100.101.102.103"],"DNSName":"my-mac.example.ts.net.","Online":true},"Peer":{"private-peer":{"TailscaleIPs":["100.100.100.200"],"DNSName":"do-not-print.example.ts.net."}},"AuthURL":"https://do-not-print.invalid"}`

func TestParseTailscaleSelfAddresses(t *testing.T) {
	got := parseTailscaleStatus([]byte(connectedTailscaleJSON))
	want := []candidate{
		{"100.101.102.103", "Tailscale IPv4"},
		{"fd7a:115c:a1e0::1", "Tailscale IPv6"},
		{"my-mac.example.ts.net", "MagicDNS hostname; requires client DNS"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for _, c := range got {
		if err := validateAddress(c.Address); err != nil {
			t.Errorf("detected address %q rejected: %v", c.Address, err)
		}
	}
}

func TestTailscaleUnavailableStatesAndInvalidJSON(t *testing.T) {
	for name, data := range map[string]string{
		"stopped":        strings.Replace(connectedTailscaleJSON, "Running", "Stopped", 1),
		"needs-login":    strings.Replace(connectedTailscaleJSON, "Running", "NeedsLogin", 1),
		"needs-approval": strings.Replace(connectedTailscaleJSON, "Running", "NeedsMachineAuth", 1),
		"offline":        strings.Replace(connectedTailscaleJSON, `"Online":true`, `"Online":false`, 1),
		"malformed":      `{"BackendState":"Running",`,
		"wrong-type":     `{"BackendState":"Running","Self":{"TailscaleIPs":"100.101.102.103"}}`,
		"wrong-online":   strings.Replace(connectedTailscaleJSON, `"Online":true`, `"Online":"true"`, 1),
		"empty":          `{}`,
		"no-self":        `{"BackendState":"Running","TailscaleIPs":["100.101.102.103"],"Peer":{"x":{"TailscaleIPs":["100.100.100.200"]}}}`,
		"no-ips":         `{"BackendState":"Running","Self":{"DNSName":"my-mac.example.ts.net."}}`,
		"invalid-ips":    `{"BackendState":"Running","Self":{"TailscaleIPs":["127.0.0.1","0.0.0.0","169.254.1.2","::1","::","ff02::1","not-an-ip","100.1.2.3;echo"]}}`,
		"oversized":      connectedTailscaleJSON + strings.Repeat(" ", tailscaleStatusLimit),
	} {
		t.Run(name, func(t *testing.T) {
			if got := parseTailscaleStatus([]byte(data)); len(got) != 0 {
				t.Fatalf("unusable status returned addresses: %#v", got)
			}
		})
	}
}

func TestTailscaleOptionalFieldsDuplicatesAndUnsafeHostname(t *testing.T) {
	data := `{"BackendState":"Running","Self":{"TailscaleIPs":["100.101.102.103","100.101.102.103"],"DNSName":"unsafe\nname.example.ts.net."},"FutureField":true}`
	got := parseTailscaleStatus([]byte(data))
	if len(got) != 1 || got[0].Address != "100.101.102.103" {
		t.Fatalf("missing optional Online, duplicates, or unsafe DNS mishandled: %#v", got)
	}
}

func TestTailscaleDiscoveryUsesReadOnlyBoundedProbe(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			var looked []string
			look := func(name string) (string, error) {
				looked = append(looked, name)
				return "/test/tailscale", nil
			}
			calls := 0
			run := func(ctx context.Context, path string, args ...string) ([]byte, error) {
				calls++
				if path != "/test/tailscale" || !reflect.DeepEqual(args, []string{"status", "--json", "--peers=false"}) {
					t.Fatalf("unexpected command: %s %v", path, args)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > tailscaleProbeTimeout {
					t.Fatal("probe deadline absent or unbounded")
				}
				return []byte(connectedTailscaleJSON), nil
			}
			if got := discoverTailscale(look, run, goos); len(got) != 3 || calls != 1 || !reflect.DeepEqual(looked, []string{"tailscale"}) {
				t.Fatal("PATH discovery failed", got, calls, looked)
			}
		})
	}
}

func TestTailscaleDiscoveryMacAppFallbackAndFailures(t *testing.T) {
	var looked []string
	look := func(name string) (string, error) {
		looked = append(looked, name)
		if name == tailscaleMacCLI {
			return name, nil
		}
		return "", os.ErrNotExist
	}
	run := func(_ context.Context, path string, _ ...string) ([]byte, error) {
		if path != tailscaleMacCLI {
			t.Fatalf("unexpected app path %q", path)
		}
		return []byte(connectedTailscaleJSON), nil
	}
	if got := discoverTailscale(look, run, "darwin"); len(got) != 3 || !reflect.DeepEqual(looked, []string{"tailscale", tailscaleMacCLI}) {
		t.Fatal("app fallback failed", got, looked)
	}
	looked = nil
	if got := discoverTailscale(look, run, "linux"); len(got) != 0 || !reflect.DeepEqual(looked, []string{"tailscale"}) {
		t.Fatal("tried macOS app on another platform", got, looked)
	}
	missing := func(string) (string, error) { return "", os.ErrNotExist }
	neverRun := func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("ran absent CLI")
		return nil, nil
	}
	if got := discoverTailscale(missing, neverRun, "darwin"); len(got) != 0 {
		t.Fatal("missing CLI returned addresses")
	}
	for _, failure := range []error{errors.New("private CLI error"), context.DeadlineExceeded} {
		run := func(context.Context, string, ...string) ([]byte, error) {
			return []byte(connectedTailscaleJSON), failure
		}
		if got := discoverTailscale(look, run, "darwin"); len(got) != 0 {
			t.Fatal("failed probe returned cached addresses")
		}
	}
}

func TestTailscaleCommandEnvironmentOutputAndTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake tailscale")
	write := func(script string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TAILSCALE_BE_CLI", "0")
	write(`test "$TAILSCALE_BE_CLI" = 1 || exit 2
test "$1" = status && test "$2" = --json && test "$3" = --peers=false || exit 3
printf '{"BackendState":"Running","Self":{"TailscaleIPs":["100.101.102.103"]}}'
printf 'private diagnostic' >&2
`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := runTailscaleStatus(ctx, path, "status", "--json", "--peers=false")
	if err != nil || len(parseTailscaleStatus(data)) != 1 || bytes.Contains(data, []byte("private")) {
		t.Fatal("command/env/stdout handling failed", string(data), err)
	}
	write("exec sleep 10\n")
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	data, err = runTailscaleStatus(ctx, path, "status", "--json", "--peers=false")
	if err == nil || len(data) != 0 || time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not stop probe promptly", time.Since(start), err)
	}
}

func TestTailscaleStatusOutputIsSizeBounded(t *testing.T) {
	var buffer boundedStatusBuffer
	if _, err := buffer.Write(bytes.Repeat([]byte("x"), tailscaleStatusLimit)); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("x")); err == nil || len(buffer.Bytes()) != tailscaleStatusLimit {
		t.Fatal("output size cap failed")
	}
	path := filepath.Join(t.TempDir(), "fake tailscale")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nhead -c 1048577 /dev/zero\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if data, err := runTailscaleStatus(ctx, path, "status", "--json", "--peers=false"); err == nil || len(data) != 0 {
		t.Fatal("exec output bypassed cap", len(data), err)
	}
}

func testAddressDiscovery(tailscale bool) addressDiscovery {
	return addressDiscovery{
		lan: func() ([]candidate, error) { return []candidate{{"192.168.1.20", "en0"}}, nil },
		tailscale: func() []candidate {
			if tailscale {
				return parseTailscaleStatus([]byte(connectedTailscaleJSON))
			}
			return nil
		},
	}
}

func TestSetupAddressChoice(t *testing.T) {
	for _, test := range []struct{ name, input, want string }{
		{"lan", "lan\n\n", "192.168.1.20"},
		{"tailscale-ip", "tailscale\n\n", "100.101.102.103"},
		{"tailscale-dns", "tailscale\nmy-mac.example.ts.net\n", "my-mac.example.ts.net"},
		{"manual", "manual\nother.example.com\n", "other.example.com"},
		{"explicit-choice", "\ninvalid\nLAN\n\n", "192.168.1.20"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := chooseSetupAddress(bufio.NewReader(strings.NewReader(test.input)), &out, testAddressDiscovery(true))
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
			for _, want := range []string{"Client network (lan/tailscale/manual/cancel)", "Windows without Tailscale", "All clients", "client DNS"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing choice/warning %q", want)
				}
			}
			if strings.Contains(out.String(), "do-not-print") || strings.Contains(out.String(), "100.100.100.200") {
				t.Fatal("peer or authentication information leaked")
			}
		})
	}
}

func TestSetupAddressDiscoveryFallback(t *testing.T) {
	var out bytes.Buffer
	got, err := chooseSetupAddress(bufio.NewReader(strings.NewReader("\n")), &out, testAddressDiscovery(false))
	if err != nil || got != "192.168.1.20" || strings.Contains(out.String(), "Client network") {
		t.Fatal("missing Tailscale changed normal LAN flow", got, err)
	}
	discovery := testAddressDiscovery(false)
	discovery.lan = func() ([]candidate, error) { return nil, errors.New("private interface error") }
	out.Reset()
	got, err = chooseSetupAddress(bufio.NewReader(strings.NewReader("manual.example.com\n")), &out, discovery)
	if err != nil || got != "manual.example.com" || strings.Contains(out.String(), "private interface error") {
		t.Fatal("LAN failure did not preserve manual flow", got, err)
	}
}

func TestSetupAddressChoiceCancelAndEOFMakeNoChanges(t *testing.T) {
	for _, input := range []string{"cancel\n", "", "tailscale\n"} {
		t.Run(strings.ReplaceAll(input, "\n", "_"), func(t *testing.T) {
			m, f, _, _ := fixture(t)
			var out bytes.Buffer
			err := setupCLIWithDiscovery(m, nil, strings.NewReader(input), &out, "test", testAddressDiscovery(true))
			if input == "cancel\n" && (err != nil || !strings.Contains(out.String(), "Cancelled; no changes made.")) {
				t.Fatal("cancel was not graceful", err)
			}
			if input != "cancel\n" && err == nil {
				t.Fatal("EOF should stop setup")
			}
			if _, err := os.Stat(m.Root); !os.IsNotExist(err) || len(f.loaded) != 0 {
				t.Fatal("cancel/EOF changed service state")
			}
		})
	}
}

func noAddressDiscovery(t *testing.T) addressDiscovery {
	t.Helper()
	return addressDiscovery{
		lan:       func() ([]candidate, error) { t.Fatal("unexpected LAN probe"); return nil, nil },
		tailscale: func() []candidate { t.Fatal("unexpected Tailscale probe"); return nil },
	}
}

func TestSetupExplicitAddressDoesNotProbe(t *testing.T) {
	for _, address := range []string{"192.168.1.20", "100.101.102.103", "my-mac.example.ts.net"} {
		t.Run(address, func(t *testing.T) {
			m, _, _, _ := fixture(t)
			var out bytes.Buffer
			err := setupCLIWithDiscovery(m, []string{"--address", address, "--data-dir", filepath.Join(m.Home, "data")}, strings.NewReader("n\n"), &out, "test", noAddressDiscovery(t))
			if err != nil || !strings.Contains(out.String(), "Client ID server: "+address+":21116") {
				t.Fatal("explicit address was changed", err, out.String())
			}
		})
	}
	m, _, _, _ := fixture(t)
	if err := setupCLIWithDiscovery(m, []string{"--yes"}, strings.NewReader(""), &bytes.Buffer{}, "test", noAddressDiscovery(t)); err == nil {
		t.Fatal("--yes without address accepted")
	}
}

func TestSetupKeepsConfiguredAddressWithoutDiscovery(t *testing.T) {
	m, _, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, m.configPath())
	key := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
	var out bytes.Buffer
	if err := setupCLIWithDiscovery(m, nil, strings.NewReader("n\n"), &out, "test", noAddressDiscovery(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Keeping configured client-facing address "+c.Address) || !strings.Contains(out.String(), "setup --address HOST") {
		t.Fatal("address retention hint missing")
	}
	if !bytes.Equal(before, mustRead(t, m.configPath())) || !bytes.Equal(key, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
		t.Fatal("existing setup changed")
	}
}

func TestSetupTailscaleAddressChangeKeepsServerIdentity(t *testing.T) {
	m, _, c, src := fixture(t)
	if err := m.Setup(c, src); err != nil {
		t.Fatal(err)
	}
	key := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
	public := mustRead(t, filepath.Join(c.DataDir, "id_ed25519.pub"))
	for _, address := range []string{"100.101.102.103", "my-mac.example.ts.net"} {
		c.Address = address
		if err := m.Setup(c, src); err != nil {
			t.Fatal(err)
		}
		got, err := readConfig(m.configPath())
		if err != nil || got.Address != address || got.DataDir != c.DataDir {
			t.Fatal("address/data was not retained", got, err)
		}
		if !bytes.Equal(key, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) || !bytes.Equal(public, mustRead(t, filepath.Join(c.DataDir, "id_ed25519.pub"))) {
			t.Fatal("address change rotated key pair")
		}
		_, args, _, err := prepareDaemon(m.Root, "hbbs", m.Home)
		if err != nil || !strings.Contains(strings.Join(args, " "), "-r "+address+":21117") {
			t.Fatal("relay advertisement did not use selected address", args, err)
		}
	}
}
