// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const usage = `rustdesk-server: RustDesk Server macOS user-service helper

Usage:
  rustdesk-server setup [--address HOST] [--data-dir ABSOLUTE_PATH] [--yes]
  rustdesk-server start
  rustdesk-server stop
  rustdesk-server status
  rustdesk-server version

setup asks before installing/starting login services. --yes requires an explicit
--address and approves the printed plan. stop also disables start at next login;
start restores it. No sudo, router changes, or firewall changes are performed.
`

func Run(args []string, in io.Reader, out io.Writer, version string) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return nil
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprint(out, usage)
		return nil
	case "version", "--version":
		fmt.Fprintf(out, "rustdesk-server %s (upstream 1.1.16)\n", version)
		return nil
	}
	if runtime.GOOS != "darwin" {
		return errors.New("service management requires macOS; this command made no service changes")
	}
	if os.Geteuid() == 0 {
		return errors.New("run as the logged-in user without sudo; system-wide/pre-login services are not supported")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if home, err = cleanPath(home); err != nil {
		return err
	}
	m := newManager(home, os.Getuid())
	switch args[0] {
	case "run-service":
		if len(args) != 3 {
			return errors.New("internal service invocation requires state directory and daemon name")
		}
		return runDaemon(args[1], args[2], home)
	case "setup":
		return setupCLI(m, args[1:], in, out, version)
	case "start", "stop", "status":
		if len(args) != 1 {
			return fmt.Errorf("%s accepts no arguments", args[0])
		}
		if args[0] == "start" {
			if err = m.Start(); err != nil {
				return err
			}
			fmt.Fprintln(out, "Started; will start automatically at login.")
		}
		if args[0] == "stop" {
			if err = m.Stop(); err != nil {
				return err
			}
			fmt.Fprintln(out, "Stopped; automatic start at login disabled. Data and keys kept.")
			return nil
		}
		return showStatus(m, out)
	default:
		return fmt.Errorf("unknown command %q; run rustdesk-server help", args[0])
	}
}

func setupCLI(m *Manager, args []string, in io.Reader, out io.Writer, version string) error {
	f := flag.NewFlagSet("setup", flag.ContinueOnError)
	f.SetOutput(out)
	var address, dataDir string
	var yes bool
	f.StringVar(&address, "address", "", "LAN address or hostname clients can reach")
	f.StringVar(&dataDir, "data-dir", "", "persistent absolute data path")
	f.BoolVar(&yes, "yes", false, "approve the displayed plan (requires --address)")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments to setup")
	}
	if yes && address == "" {
		return errors.New("--yes requires --address; LAN detection must be reviewed")
	}
	if err := m.available(); err != nil {
		return err
	}
	old, err := readConfig(m.configPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if old != nil {
		if address == "" {
			address = old.Address
		}
		if dataDir == "" {
			dataDir = old.DataDir
		}
	}
	reader := bufio.NewReader(in)
	if address == "" {
		candidates, err := lanAddresses()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Detected private IPv4 addresses:")
		for _, c := range candidates {
			fmt.Fprintf(out, "  %s (%s)\n", c.Address, c.Interface)
		}
		if len(candidates) > 0 {
			address = candidates[0].Address
		}
		address, err = prompt(reader, out, "Client-facing address", address)
		if err != nil {
			return err
		}
	}
	address = strings.TrimSpace(address)
	if err = validateAddress(address); err != nil {
		return err
	}
	if dataDir == "" {
		dataDir = filepath.Join(m.Root, "data")
		if !yes {
			dataDir, err = prompt(reader, out, "Persistent data directory", dataDir)
			if err != nil {
				return err
			}
		}
	}
	if dataDir, err = cleanPath(dataDir); err != nil {
		return err
	}
	if err = validateDataDir(m, dataDir); err != nil {
		return err
	}
	if old != nil && old.DataDir != dataDir {
		return errors.New("existing data directory cannot be changed by setup; keep the current path and back it up before manual migration")
	}
	c := &Config{Schema: 1, Address: address, DataDir: dataDir, PackageVersion: version}
	fmt.Fprintf(out, "\nSetup plan\n  Client ID server: %s\n  Relay server: %s\n  Persistent data: %s (owner-only permissions)\n  Runtime/logs: %s\n  User LaunchAgents: %s\n", net.JoinHostPort(address, "21116"), net.JoinHostPort(address, "21117"), dataDir, m.Root, m.Agents)
	fmt.Fprintln(out, "  Creates or preserves one Ed25519 key pair; copies the bundled hbbs/hbbr.\n  Starts hbbs/hbbr now and on this user's login; an existing setup is restarted.\n  Opens TCP 21115–21119 and UDP 21116 on ALL network interfaces.\n  This is not a LAN-only bind. Use a trusted LAN; no router/firewall settings change.\n  macOS may ask you to allow incoming connections/background items.\n  Upstream hbbs performs its own outbound version check to RustDesk.\n  Server key identifies the server; use strong RustDesk client access passwords.")
	if !yes {
		answer, err := prompt(reader, out, "Apply and start? [y/N]", "")
		if err != nil {
			return err
		}
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Fprintln(out, "Cancelled; no changes made.")
			return nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	if err = m.Setup(c, filepath.Dir(exe)); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nSetup complete. Existing data and server identity are kept on later setup runs.")
	return showStatus(m, out)
}

func prompt(r *bufio.Reader, w io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return "", errors.New("input ended before confirmation; no service changes made (use explicit flags and --yes for automation)")
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = def
	}
	return line, nil
}
func validateDataDir(m *Manager, dir string) error {
	if dir == "/" || dir == m.Home || dir == filepath.Join(m.Home, "Library") || dir == m.Root || dir == m.Agents {
		return errors.New("choose a dedicated data subdirectory")
	}
	for _, base := range []string{filepath.Join(m.Root, "current"), filepath.Join(m.Root, "releases"), filepath.Join(m.Root, "logs")} {
		if dir == base || strings.HasPrefix(dir, base+string(os.PathSeparator)) {
			return errors.New("data must be outside managed runtime and log directories")
		}
	}
	return nil
}
func validateAddress(s string) error {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " \t\r\n\x00/,;@") || strings.HasPrefix(s, "-") {
		return errors.New("address must be a reachable IP or hostname, without scheme or port")
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			return errors.New("choose a client-reachable non-loopback address")
		}
		return nil
	}
	if strings.Contains(s, ":") {
		return errors.New("provide only a hostname/IP, not a port or URL")
	}
	s = strings.TrimSuffix(s, ".")
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid hostname")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("invalid hostname")
			}
		}
	}
	return nil
}

type candidate struct{ Address, Interface string }

func lanAddresses() ([]candidate, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var found []candidate
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.To4() != nil && ip.IsPrivate() {
				found = append(found, candidate{ip.String(), iface.Name})
			}
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Interface == found[j].Interface {
			return found[i].Address < found[j].Address
		}
		return found[i].Interface < found[j].Interface
	})
	return found, nil
}
func showStatus(m *Manager, out io.Writer) error {
	if err := m.available(); err != nil {
		return err
	}
	c, err := readConfig(m.configPath())
	if os.IsNotExist(err) {
		fmt.Fprintln(out, "Not configured. Run rustdesk-server setup.")
		return nil
	}
	if err != nil {
		return err
	}
	healthy := true
	for _, name := range daemonNames {
		text, err := m.Command("print", m.target(name))
		if err != nil && !errors.Is(err, errServiceAbsent) {
			return err
		}
		state := "stopped"
		if err == nil {
			state = "loaded, not running"
			if running(text) {
				state = "running"
			} else {
				healthy = false
			}
		} else {
			healthy = false
		}
		fmt.Fprintf(out, "%s: %s\n", name, state)
	}
	key, err := publicKey(c.DataDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nRustDesk client settings → Network → ID/Relay server\n  ID server: %s\n  Relay server: %s\n  Key (PUBLIC): %s\n  API server: leave blank\n\nData: %s\nLogs: %s\nPackage: %s\n", net.JoinHostPort(c.Address, "21116"), net.JoinHostPort(c.Address, "21117"), key, c.DataDir, filepath.Join(m.Root, "logs"), c.PackageVersion)
	fmt.Fprintln(out, "Status checks local launchd processes, not end-to-end remote connectivity.\nIf DHCP/VPN changes the reachable address, rerun setup --address NEW_ADDRESS.")
	if !healthy {
		return errors.New("one or more services are stopped; run start, or inspect the logs")
	}
	return nil
}
