// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const usage = `rustdesk-server: RustDesk Server macOS user-service helper

Usage:
  rustdesk-server [--lang auto|ja|en] setup [--address HOST] [--data-dir ABSOLUTE_PATH] [--yes]
  rustdesk-server start
  rustdesk-server stop
  rustdesk-server status
  rustdesk-server version

setup asks before installing/starting login services. --yes requires an explicit
--address and approves the printed plan. stop also disables start at next login;
start restores it. No sudo, router changes, or firewall changes are performed.
--lang defaults to auto: LC_ALL, LC_MESSAGES, LANG, then macOS preferred language.
Japanese locales use Japanese; other or unknown locales use English.
Interactive setup offers LAN or an already-connected Tailscale address. All clients
must be able to reach the selected address. Tailscale is never installed or changed.
`

func Run(args []string, in io.Reader, out io.Writer, version string) (err error) {
	args, selected, parseErr := languageArgs(args)
	lang := invocationLanguage(selected)
	defer func() {
		if err != nil {
			err = &localizedFailure{lang, err}
		}
	}()
	if parseErr != nil {
		return parseErr
	}
	if len(args) == 0 {
		fmt.Fprint(out, tr(lang, usage))
		return nil
	}
	switch args[0] {
	case "help", "--help", "-h":
		if len(args) > 1 && args[1] == "setup" && len(args) == 2 {
			fmt.Fprint(out, tr(lang, setupUsage))
			return nil
		}
		if len(args) != 1 {
			return problem("help accepts only an optional command: rustdesk-server help setup")
		}
		fmt.Fprint(out, tr(lang, usage))
		return nil
	case "version", "--version":
		if len(args) != 1 {
			return problem("%s accepts no arguments", args[0])
		}
		fmt.Fprintf(out, "rustdesk-server %s (upstream 1.1.16)\n", version)
		return nil
	case "setup":
		_, help, e := parseSetupArgs(args[1:])
		if e != nil {
			return e
		}
		if help {
			fmt.Fprint(out, tr(lang, setupUsage))
			return nil
		}
	case "start", "stop", "status":
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprint(out, tr(lang, usage))
			return nil
		}
		if len(args) != 1 {
			return problem("%s accepts no arguments", args[0])
		}
	case "run-service":
		if len(args) != 3 {
			return problem("internal service invocation requires state directory and daemon name")
		}
	default:
		return problem("unknown command %q; run rustdesk-server help", args[0])
	}
	if runtime.GOOS != "darwin" {
		return problem("service management requires macOS; this command made no service changes")
	}
	if os.Geteuid() == 0 {
		return problem("run as the logged-in user without sudo; system-wide/pre-login services are not supported")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return problem("could not find the home directory; run from your normal login session: %w", err)
	}
	if home, err = cleanPath(home); err != nil {
		return err
	}
	if args[0] == "run-service" {
		return runDaemon(args[1], args[2], home)
	}
	m := newManager(home, os.Getuid())
	m.Language = lang
	return runManaged(m, args, in, out, version)
}

func runManaged(m *Manager, args []string, in io.Reader, out io.Writer, version string) error {
	switch args[0] {
	case "setup":
		return setupCLI(m, args[1:], in, out, version)
	case "start":
		if err := m.Start(); err != nil {
			return err
		}
		fmt.Fprintln(out, tr(m.Language, "Started; will start automatically at login."))
	case "stop":
		if err := m.Stop(); err != nil {
			return err
		}
		fmt.Fprintln(out, tr(m.Language, "Stopped; automatic start at login disabled. Data and keys kept."))
		return nil
	}
	return showStatus(m, out)
}

const setupUsage = `Usage: rustdesk-server [--lang auto|ja|en] setup [options]

  --address HOST          IP or hostname ALL clients can reach (LAN or Tailscale)
  --data-dir ABSOLUTE_PATH Persistent data directory (dedicated, owner-only)
  --yes                   Approve the displayed plan (requires --address)
  --lang auto|ja|en        Display language (default: auto)
  --help                  Show this help

Without --yes, review the address, data directory, and network exposure before
confirming with y. Enter cancel at a question to leave without service changes.
No sudo, router, firewall, or Tailscale settings are changed.
`

type setupOptions struct {
	address, dataDir string
	yes              bool
}

func parseSetupArgs(args []string) (setupOptions, bool, error) {
	var opts setupOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && i == len(args)-1 {
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "--" {
			return opts, false, problem("unexpected argument %q; run rustdesk-server setup --help", arg)
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		switch name {
		case "h", "help":
			if hasValue {
				return opts, false, problem("--%s does not accept a value", name)
			}
			return opts, true, nil
		case "yes":
			opts.yes = true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return opts, false, problem("invalid value %q for --yes; use true or false", value)
				}
				opts.yes = parsed
			}
		case "address", "data-dir":
			if !hasValue {
				if i+1 == len(args) {
					return opts, false, problem("--%s requires a value; run rustdesk-server setup --help", name)
				}
				i++
				value = args[i]
			}
			if name == "address" {
				opts.address = value
			} else {
				opts.dataDir = value
			}
		default:
			return opts, false, problem("unknown option %q; run rustdesk-server setup --help", arg)
		}
	}
	if opts.yes && opts.address == "" {
		return opts, false, problem("--yes requires --address; address detection must be reviewed")
	}
	return opts, false, nil
}

func setupCLI(m *Manager, args []string, in io.Reader, out io.Writer, version string) error {
	return setupCLIWithDiscovery(m, args, in, out, version, addressDiscovery{
		lan: lanAddresses, tailscale: tailscaleAddresses,
	})
}

type addressDiscovery struct {
	lan       func() ([]candidate, error)
	tailscale func() []candidate
}

var errSetupCancelled = problem("setup cancelled")

func setupCLIWithDiscovery(m *Manager, args []string, in io.Reader, out io.Writer, version string, discovery addressDiscovery) (result error) {
	defer func() {
		if errors.Is(result, errSetupCancelled) {
			fmt.Fprintln(out, tr(m.Language, "Cancelled; no changes made."))
			result = nil
		}
	}()
	opts, help, err := parseSetupArgs(args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(out, tr(m.Language, setupUsage))
		return nil
	}
	address, dataDir, yes := opts.address, opts.dataDir, opts.yes
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
			fmt.Fprintf(out, tr(m.Language, "Keeping configured client-facing address %s; use setup --address HOST to change it.\n"), address)
		}
		if dataDir == "" {
			dataDir = old.DataDir
		}
	}
	reader := bufio.NewReader(in)
	interactiveAddress := address == ""
	if address == "" {
		address, err = chooseSetupAddressLocalized(reader, out, discovery, m.Language)
		if errors.Is(err, errSetupCancelled) {
			fmt.Fprintln(out, tr(m.Language, "Cancelled; no changes made."))
			return nil
		}
		if err != nil {
			return err
		}
	}
	address = strings.TrimSpace(address)
	for {
		if err = validateAddress(address); err == nil {
			break
		}
		if !interactiveAddress {
			return err
		}
		fmt.Fprintln(out, renderError(m.Language, err))
		address, err = prompt(reader, out, tr(m.Language, "Client-facing address"), "")
		if err != nil {
			return err
		}
	}
	if dataDir == "" {
		dataDir = filepath.Join(m.Root, "data")
		if !yes {
			dataDir, err = prompt(reader, out, tr(m.Language, "Persistent data directory"), dataDir)
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
		return problem("existing data directory cannot be changed by setup; keep the current path and back it up before manual migration")
	}
	c := &Config{Schema: 1, Address: address, DataDir: dataDir, PackageVersion: version}
	fmt.Fprintf(out, tr(m.Language, "\nSetup plan\n  Client ID server: %s\n  Relay server: %s\n  Persistent data: %s (owner-only permissions)\n  Runtime/logs: %s\n  User LaunchAgents: %s\n"), net.JoinHostPort(address, "21116"), net.JoinHostPort(address, "21117"), dataDir, m.Root, m.Agents)
	fmt.Fprintln(out, tr(m.Language, "  Creates or preserves one Ed25519 key pair; copies the bundled hbbs/hbbr.\n  Starts hbbs/hbbr now and on this user's login; an existing setup is restarted.\n  Opens TCP 21115–21119 and UDP 21116 on ALL network interfaces.\n  This is not a LAN-only bind. Use a trusted LAN; no router/firewall settings change.\n  macOS may ask you to allow incoming connections/background items.\n  Upstream hbbs performs its own outbound version check to RustDesk.\n  Server key identifies the server; use strong RustDesk client access passwords."))
	fmt.Fprintln(out, tr(m.Language, clientReachabilityNotice))
	if !yes {
		answer, err := prompt(reader, out, tr(m.Language, "Apply and start? [y/N]"), "")
		if err != nil {
			return err
		}
		if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") && answer != "はい" {
			fmt.Fprintln(out, tr(m.Language, "Cancelled; no changes made."))
			return nil
		}
	}
	exe, err := m.executable()
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
	fmt.Fprintln(out, tr(m.Language, "\nSetup complete. Existing data and server identity are kept on later setup runs."))
	return showStatus(m, out)
}

const clientReachabilityNotice = "All clients must reach the chosen ID/relay address. Tailscale addresses require tailnet access\nand policy allowing the RustDesk ports; MagicDNS also requires working client DNS.\nWindows without Tailscale cannot directly reach a Tailscale 100.x address; use a reachable\nLAN address or a separately approved network route. Setup does not configure either route."

func chooseSetupAddress(reader *bufio.Reader, out io.Writer, discovery addressDiscovery) (string, error) {
	return chooseSetupAddressLocalized(reader, out, discovery, english)
}
func chooseSetupAddressLocalized(reader *bufio.Reader, out io.Writer, discovery addressDiscovery, lang language) (string, error) {
	lan, err := discovery.lan()
	if err != nil {
		fmt.Fprintln(out, tr(lang, "LAN address detection unavailable; you can enter an address manually."))
		lan = nil
	} else {
		fmt.Fprintln(out, tr(lang, "Detected private LAN IPv4 addresses:"))
		for _, c := range lan {
			fmt.Fprintf(out, tr(lang, "  %s (%s)\n"), c.Address, c.Interface)
		}
	}
	lanDefault := ""
	if len(lan) > 0 {
		lanDefault = lan[0].Address
	}
	tailscale := discovery.tailscale()
	if len(tailscale) == 0 {
		return prompt(reader, out, tr(lang, "Client-facing address"), lanDefault)
	}
	fmt.Fprintln(out, tr(lang, "Connected Tailscale addresses for this Mac:"))
	for _, c := range tailscale {
		fmt.Fprintf(out, tr(lang, "  %s (%s)\n"), c.Address, tr(lang, c.Interface))
	}
	fmt.Fprintln(out, tr(lang, clientReachabilityNotice))
	for {
		choice, err := prompt(reader, out, tr(lang, "Client network (lan/tailscale/manual/cancel)"), "")
		if err != nil {
			return "", err
		}
		switch strings.ToLower(choice) {
		case "lan":
			return prompt(reader, out, tr(lang, "Client-facing LAN address"), lanDefault)
		case "tailscale":
			return prompt(reader, out, tr(lang, "Client-facing Tailscale IP or MagicDNS hostname"), tailscale[0].Address)
		case "manual":
			return prompt(reader, out, tr(lang, "Client-facing address"), "")
		case "cancel":
			return "", errSetupCancelled
		default:
			fmt.Fprintln(out, tr(lang, "Choose lan, tailscale, manual, or cancel."))
		}
	}
}

func prompt(r *bufio.Reader, w io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return "", problem("input ended before confirmation; no service changes made (use explicit flags and --yes for automation)")
	}
	line = strings.TrimSpace(line)
	if strings.EqualFold(line, "cancel") || line == "取消" || line == "キャンセル" {
		return "", errSetupCancelled
	}
	if line == "" {
		line = def
	}
	return line, nil
}
func validateDataDir(m *Manager, dir string) error {
	if dir == "/" || dir == m.Home || dir == filepath.Join(m.Home, "Library") || dir == m.Root || dir == m.Agents {
		return problem("choose a dedicated data subdirectory")
	}
	for _, base := range []string{filepath.Join(m.Root, "current"), filepath.Join(m.Root, "releases"), filepath.Join(m.Root, "logs")} {
		if dir == base || strings.HasPrefix(dir, base+string(os.PathSeparator)) {
			return problem("data must be outside managed runtime and log directories")
		}
	}
	return nil
}
func validateAddress(s string) error {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " \t\r\n\x00/,;@") || strings.HasPrefix(s, "-") {
		return problem("address must be a reachable IP or hostname, without scheme or port")
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			return problem("choose a client-reachable non-loopback address")
		}
		return nil
	}
	if strings.Contains(s, ":") {
		return problem("provide only a hostname/IP, not a port or URL")
	}
	s = strings.TrimSuffix(s, ".")
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return problem("invalid hostname")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return problem("invalid hostname")
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
		fmt.Fprintln(out, tr(m.Language, "Not configured. Run rustdesk-server setup."))
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
		fmt.Fprintf(out, tr(m.Language, "%s: %s\n"), name, tr(m.Language, state))
	}
	key, err := publicKey(c.DataDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, tr(m.Language, "\nRustDesk client settings → Network → ID/Relay server\n  ID server: %s\n  Relay server: %s\n  Key (PUBLIC): %s\n  API server: leave blank\n\nData: %s\nLogs: %s\nPackage: %s\n"), net.JoinHostPort(c.Address, "21116"), net.JoinHostPort(c.Address, "21117"), key, c.DataDir, filepath.Join(m.Root, "logs"), c.PackageVersion)
	fmt.Fprintln(out, tr(m.Language, "Status checks local launchd processes, not end-to-end remote connectivity.\nIf DHCP/VPN changes the reachable address, rerun setup --address NEW_ADDRESS."))
	fmt.Fprintln(out, tr(m.Language, clientReachabilityNotice))
	if !healthy {
		return problem("one or more services are stopped; run start, or inspect the logs")
	}
	return nil
}
