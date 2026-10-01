# RustDesk Server for macOS

[日本語](README.md) | [English](README.en.md)

This project builds the RustDesk OSS Server binaries, `hbbs` and `hbbr`, for macOS and distributes them through mise. A small companion CLI written in Go manages keys, storage locations, and automatic startup at login. This is not an official RustDesk distribution.

- Native builds for Apple Silicon only. The declared minimum OS is macOS 15. CI builds and tests run on macOS 26; runtime compatibility with earlier versions, including macOS 15, has not been verified
- Bundled upstream: **RustDesk Server 1.1.16** ([pinned commit](https://github.com/rustdesk/rustdesk-server/commit/73523b31cfd25d77dee862e6fc9f5e1fb5e485ef))
- No repository clone, Rust/Go toolchain, Homebrew, or manual editing of `mise.toml` is required to use it
- Packages are signed using GitHub Actions OIDC / Sigstore. This is separate from Apple Developer ID signing and notarization

## Install and start

**Release status:** [v0.1.0](https://github.com/webkaz-labs/rustdesk-server-macos/releases/tag/v0.1.0) is published. The [release CI run](https://github.com/webkaz-labs/rustdesk-server-macos/actions/runs/36761715023) passed native builds on macOS 26 / Apple Silicon, unit and launchd tests, signature verification, and installation of the published package through mise / Packslip. End-to-end connections between real clients, including connections over Tailscale, remain unverified.

Run these commands on a Mac with [mise](https://mise.jdx.dev/getting-started.html) already installed and activated. A recent version of mise with Packslip support is required (the version pinned for CI is 2026.9.18).

```sh
mise use -g packslip:github.com/webkaz-labs/rustdesk-server-macos@0.1.0
rustdesk-server setup
```

`setup` is a command provided by this repository. It asks you to review the address advertised to clients and the storage location, then registers and starts the services after you confirm with `y`. If you have multiple network interfaces or a VPN, choose an address that your clients can reach.

Enter the **ID server / Relay server / Key (PUBLIC)** values displayed when setup finishes into your RustDesk client's Settings → Network → ID/Relay Server settings. Leave API server blank. You do not need to copy the private key to clients.

```sh
rustdesk-server status  # Show service status and client settings again
rustdesk-server stop    # Stop services and disable automatic startup at the next login
rustdesk-server start   # Start services and restore automatic startup at login
```

The services run as LaunchAgents for the logged-in user. Do not use `sudo`. They do not run while you are logged out or before login, and they are unavailable while the Mac is asleep.

### Using Tailscale

Tailscale must already be installed and connected, and your tailnet policy must allow the connection. Only during the first interactive `setup`, with no existing configuration or explicit `--address`, setup reads the existing [Tailscale CLI](https://tailscale.com/docs/reference/tailscale-cli?tab=macos) status using `status --json --peers=false`. It also supports the executable inside the official macOS app with `TAILSCALE_BE_CLI=1`. If a valid connected status is available, you can choose LAN / Tailscale / manual entry / cancel. For Tailscale, select this Mac's `100.x` IP or MagicDNS name confirmed in its own status.

If Tailscale is missing, stopped, or returns malformed status, setup falls back to the normal LAN / manual flow. An existing configured address or an explicit `--address` takes precedence, without probing Tailscale. To switch, run either example below after replacing the example address with your Mac's actual address. Existing keys and data are retained.

```sh
# Example: replace with your Mac's Tailscale IP or MagicDNS name
rustdesk-server setup --address 100.100.100.100
# Or:
rustdesk-server setup --address my-mac.example-tailnet.ts.net
```

- **Every client must be able to reach the chosen ID / Relay server address.** MagicDNS names also require access to tailnet DNS on the client. A Windows client that does not use Tailscale needs a reachable LAN address or an approved, configured route. Routes between separate networks are not added automatically
- Setup does not install Tailscale, log in, or change ACLs, routing, or firewalls. Choosing a Tailscale address does not restrict the daemons to listening only on Tailscale. Real end-to-end connections over Tailscale have not been verified

### Update

```sh
mise use -g packslip:github.com/webkaz-labs/rustdesk-server-macos@NEW_VERSION
rustdesk-server setup
```

Running `setup` from the new package replaces the runtime and restarts the services. Keys and the database are preserved. Removing an older version from mise does not remove the copies used by the services or their data. Old runtimes are not automatically deleted, so they remain available for rollback checks.

## Storage locations

The default location is `~/Library/Application Support/rustdesk-server/`.

| Path | Contents |
| --- | --- |
| `data/` | Private key `id_ed25519`, public key `id_ed25519.pub`, and database |
| `config.json` | Address, data location, and package version |
| `releases/<digest>/` | Three verified and copied executables |
| `current` | Stable link to the active runtime |
| `logs/` | `hbbs.log` / `hbbr.log` and error logs |

LaunchAgents are created at `~/Library/LaunchAgents/com.webkaz-labs.rustdesk-server.{hbbs,hbbr}.plist`. Access to data, configuration, and logs is restricted to the owner, and the private key is stored with `0600` permissions. The selected data directory is also changed to `0700`, so choose a dedicated location.

Changing the data location of an existing setup is rejected to prevent accidental key regeneration. Include the entire data directory in backups, and never send the private key to a public repository or a third party.

## Network and security boundaries

- **The LAN address is the address advertised to clients, not a restriction on listening interfaces.** Upstream 1.1.16 listens on TCP ports 21115–21119 and UDP port 21116 on all interfaces. `-r` advertises the relay; this project does not use a nonexistent bind flag
- This project does not change your firewall, router, or port forwarding. Use it on a trusted LAN. If your environment has a public IP address or port forwarding, check that the server is not unintentionally reachable from the Internet
- macOS may ask you to allow incoming connections or background items. Review each prompt before deciding
- `hbbs` and `hbbr` share one Ed25519 key pair, and both receive `-k _`. Keyless relaying is not configured. The public key does not replace user authentication; client-side access controls and strong passwords are still necessary
- Each time a service starts, the native CLI checks that the keys exist, are consistent, and have the correct permissions, and that no `.env` file is present. It then runs the daemon with a known, minimal environment. If keys are missing, it stops instead of silently creating a new identity
- Compatible keys are generated only on the first run, using Go's cryptographically secure random number generator. If setup fails partway through, generated keys are kept and reused on the next run. Corrupt or mismatched existing keys are not overwritten
- Upstream `hbbs` makes outbound version-check requests to RustDesk
- This is not a sandbox against attacks in which the same macOS user modifies files while they are being executed

## Troubleshooting

- **Command not found:** Check that mise is activated in your current shell. You can also run `mise exec packslip:github.com/webkaz-labs/rustdesk-server-macos@0.1.0 -- rustdesk-server setup`
- **Unable to download a newly published release:** mise's default minimum release age is **24 hours**. Installation may be held back during the first 24 hours after publication; retry after that period. Rate limits may also apply. Check [Releases](https://github.com/webkaz-labs/rustdesk-server-macos/releases) and the [mise Packslip documentation](https://mise.jdx.dev/dev-tools/backends/packslip.html). Do not globally disable release-age protection or disable signature verification
- **macOS blocks execution:** This distribution uses ad-hoc signing only and is not notarized. Verify its signatures and source, then use macOS's normal approval flow. You do not need to disable Gatekeeper globally
- **A GUI login is required:** Run the command without `sudo` in Terminal as the user who is logged in to the Mac
- **Port already in use:** Check for conflicts with an existing RustDesk Server, Docker, or another service. Stop the unneeded instance before trying again
- **LAN address changed:** Update it with `rustdesk-server setup --address NEW_ADDRESS`. A DHCP reservation on your router can help keep the address stable
- **Service fails to start:** Check `status` and `logs/*.error.log` / `logs/*.log`. `status` checks local processes; it does not guarantee that clients can connect successfully. Logs are not automatically rotated, so check their size occasionally
- **Missing or mismatched keys:** Restore the original data from a backup. Do not try to fix this by deleting only the private key or only the public key
- **Setup fails:** Setup attempts to restore the previous configuration and service state. It reports explicitly if recovery also fails. Data, keys, and staged runtimes are retained

For automation only, after approving the changes that setup displays, you can use `setup --address 192.168.1.20 --data-dir '/absolute/dedicated/path' --yes`.

## Build, verification, and licensing

The [release guide](docs/RELEASING.md) documents the pinned toolchains, build process, signing, and corresponding-source procedures.

The CLI and packaging tools are built with **Go 1.27.1** and use only the Go standard library. Shell scripts coordinate the steps, and Rust builds upstream; there are no project-authored Python tools. Native CI uses the GitHub Actions **`macos-26` (Apple Silicon / arm64)** runner. The declared macOS 15 minimum does not mean this CI verifies execution on macOS 15.

```sh
go test -race ./...
go vet ./...
# Native macOS CI / disposable test environments only:
RUSTDESK_MACOS_INTEGRATION=1 go test ./internal/service -run TestLaunchdIntegration -count=1 -v
```

The regular Go tests cover the CLI and Go-based packaging tools; service tests mock `launchctl`. The macOS integration test uses unique launchd labels in the logged-in user's domain, temporary directories, and a sleep fixture that does not listen on the network; it unloads the services when it finishes. These tests do not include client-to-client connections through the actual daemons.

Upstream is licensed under AGPL-3.0, and this CLI is licensed under **AGPL-3.0-or-later** ([LICENSE](LICENSE)). Each release includes corresponding source containing the pinned upstream commit, recursive submodules, vendored source for locked dependencies, CLI source, and build scripts, together with a dependency-license inventory. Public GitHub Actions logs provide the native build and verification results for the relevant commit.
