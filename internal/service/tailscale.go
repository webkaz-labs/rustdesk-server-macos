// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	// Official macOS app CLI path and environment switch:
	// https://tailscale.com/docs/reference/tailscale-cli?tab=macos
	tailscaleMacCLI       = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	tailscaleProbeTimeout = 3 * time.Second
	tailscaleStatusLimit  = 1 << 20
)

type tailscaleRunner func(context.Context, string, ...string) ([]byte, error)

func tailscaleAddresses() []candidate {
	return discoverTailscale(exec.LookPath, runTailscaleStatus, runtime.GOOS)
}

// Discovery is optional and read-only. Failures never prevent the LAN/manual
// flow, and no raw status, peer information, or command error is printed.
func discoverTailscale(lookPath func(string) (string, error), run tailscaleRunner, goos string) []candidate {
	path, err := lookPath("tailscale")
	if err != nil && goos == "darwin" {
		path, err = lookPath(tailscaleMacCLI)
	}
	if err != nil || path == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tailscaleProbeTimeout)
	defer cancel()
	data, err := run(ctx, path, "status", "--json", "--peers=false")
	if err != nil || ctx.Err() != nil {
		return nil
	}
	return parseTailscaleStatus(data)
}

func runTailscaleStatus(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	// The macOS application executable otherwise may open its GUI from a script.
	cmd.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	output := &boundedStatusBuffer{}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type boundedStatusBuffer struct{ buffer bytes.Buffer }

func (b *boundedStatusBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedStatusBuffer) Write(p []byte) (int, error) {
	if len(p) > tailscaleStatusLimit-b.buffer.Len() {
		return 0, errors.New("Tailscale status exceeded size limit")
	}
	return b.buffer.Write(p)
}

func parseTailscaleStatus(data []byte) []candidate {
	// Do not decode or display peers, user identities, authentication URLs, or
	// top-level address fields. Only a running daemon's Self record is usable.
	var status struct {
		BackendState string
		Self         *struct {
			TailscaleIPs []string
			DNSName      string
			Online       *bool
		}
	}
	if len(data) > tailscaleStatusLimit || json.Unmarshal(data, &status) != nil || status.BackendState != "Running" || status.Self == nil {
		return nil
	}
	// Some CLI versions omit Online. An explicit false must never be presented
	// as connected merely because the daemon still has cached addresses.
	if status.Self.Online != nil && !*status.Self.Online {
		return nil
	}
	var ipv4, ipv6 []candidate
	seen := make(map[string]bool)
	for _, address := range status.Self.TailscaleIPs {
		ip := net.ParseIP(address)
		if ip == nil || !ip.IsGlobalUnicast() || validateAddress(address) != nil {
			continue
		}
		address = ip.String()
		if seen[address] {
			continue
		}
		seen[address] = true
		if ip.To4() != nil {
			ipv4 = append(ipv4, candidate{address, "Tailscale IPv4"})
		} else {
			ipv6 = append(ipv6, candidate{address, "Tailscale IPv6"})
		}
	}
	found := append(ipv4, ipv6...)
	// A DNS name alone is not enough to offer a disconnected/addressless device.
	if len(found) == 0 {
		return nil
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if strings.Contains(dns, ".") && net.ParseIP(dns) == nil && validateAddress(dns) == nil {
		found = append(found, candidate{dns, "MagicDNS hostname; requires client DNS"})
	}
	return found
}
