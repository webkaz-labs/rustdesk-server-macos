# Release engineering

The release workflow builds the official RustDesk Server source and this helper.
No upstream prebuilt server binary is downloaded. Distribution versions are
independent of the upstream RustDesk Server version.

## Pins and native runners

`upstream.json` pins RustDesk Server `1.1.16` to commit
`73523b31cfd25d77dee862e6fc9f5e1fb5e485ef` and its recursive `hbb_common` submodule
to `83419b6549636ee39dacef7776c473f5802e08d6`. Fetches use the full commit, recursive
submodules are checked against the recorded map, and every Cargo build uses the
upstream `Cargo.lock` with `--locked`. A changed lockfile fails the build.

The toolchains are Rust `1.98.1` and Go `1.26.8`. Changing either is an explicit
maintenance change: update `upstream.json` and workflow setup inputs together.
Actions are pinned to reviewed full commits. Runner image contents are not
immutable, so release metadata records the actual SDK, Xcode, macOS, and compiler
versions. The archive serialization is stable; this does not claim bit-for-bit
reproducibility across different Apple SDKs or runner-image revisions.

Native jobs use `macos-15` for Apple Silicon and `macos-15-intel` for Intel. These
are standard GitHub-hosted runners, free for public repositories. No paid larger
runner labels, self-hosted machines, Rosetta, or cross-linked release builds are
used. Native jobs intentionally skip private repositories to avoid billed macOS
minutes. GitHub repository/account quotas and policy still apply.

The supported minimum is conservatively **macOS 15.0** on both architectures.
The build exports `MACOSX_DEPLOYMENT_TARGET=15.0`; release checks reject any
Mach-O minimum newer than the declared requirement. Testing on macOS 15 does not
establish compatibility with older macOS versions.

## What CI checks

1. Go unit/race tests, vet, and packaging unit tests
2. Native Rust `cargo build --release --locked --target … --bin hbbs --bin hbbr`
3. Native standard-library-only Go helper build with `-trimpath` and version info
4. Exact Mach-O architecture via `lipo`, loader dependencies via `otool -L`, and
   deployment target via `otool -l`
5. No Homebrew, developer-directory, `@rpath`, or other non-system shared-library
   dependencies; only `/usr/lib/` and `/System/Library/` are allowed
6. Ad-hoc code signing, signature verification, and each binary's `--help`
7. An isolated, nonprivileged launchd fixture test, using temporary private paths
   and unique labels; CI does not start real RustDesk server listeners
8. Offline resolution of the complete corresponding-source tree with an empty
   Cargo cache
9. Packslip signature and all installable artifact/SBOM digests before publication
10. Actual mise Packslip installation from the published release, on both native
    architectures, followed by version/help, architecture, and signature checks

Ad-hoc signing is **not Apple Developer ID signing or notarization**. The
Packslip OIDC signature independently authenticates the release manifest and
artifact bytes; it does not assert Apple notarization or absence of vulnerabilities.

## Release assets

- `rustdesk-server-macos-VERSION-darwin-arm64.tar.gz`
- `rustdesk-server-macos-VERSION-darwin-amd64.tar.gz`
- Per-architecture `.cdx.json` CycloneDX SBOM and `.source.json` build/source inventory
- `rustdesk-server-macos-VERSION-source.tar.gz`, complete corresponding source
- `SHA256SUMS`, `packslip.toml`, and `packslip.sigstore.json`

Each installable archive contains `bin/rustdesk-server`, `bin/hbbs`, `bin/hbbr`,
and `share/rustdesk-server/` with source metadata, Cargo.lock, SBOM, and notices.
All three executable paths are declared in the signed Packslip manifest. The
archives have no generated server identity, runtime database, configured hostname,
or user-specific plist.

The source archive contains the distribution's tracked source and build scripts,
the exact upstream tree, every recursive submodule tree, Cargo.lock, and complete
`cargo vendor --locked --versioned-dirs` output for registry and Git dependencies.
Vendored packages retain their embedded native sources and original notices.
Its generated Cargo source replacement config permits offline dependency
resolution and its `BUILDING.txt` documents native rebuild commands. Rust/Go
toolchains and Apple's system SDK remain external build prerequisites. Upstream's
tracked sample `.env` and `db_v2.sqlite3` remain in the upstream source snapshot;
neither ships in an installable binary archive.

The SBOM is a target-filtered Cargo dependency graph, including build dependencies,
plus binary hashes and Go toolchain metadata. The Go helper has no third-party Go
modules. System SDK libraries are not vendored components. Package-author Cargo
license declarations are preserved without asserting a legal conclusion. The
notice inventory records packages that lack a separate top-level license file;
full vendored source includes nested notices. RustDesk's AGPL text is included.

## Publication and trust boundaries

Only a pushed `vSEMVER` tag enables publication. Review both native jobs before
tagging a release commit. Pushing a tag is a release operation and requires the
repository owner's authorization.

The workflow creates an unpublished draft release and uploads final bytes. It
attests binaries, source, SBOMs, and metadata with GitHub build provenance. A
separate job with `contents: read` and `id-token: write`, but no checkout and no
release write access, invokes pinned `jdx/packslip` and verifies the manifest. A
small final job uploads only that bundle and publishes the release. The source
archive's SHA-256 is also recorded in a namespaced, signed manifest extension.
Packslip provenance links must be independently verified when required; merely
having a link is not proof that a consumer verified it.

A rerun may resume a draft. The workflow refuses to replace an already-published
release: use a new version instead. If post-publication mise smoke fails, the
release already exists; investigate, then publish a corrected new version or
explicitly withdraw the bad release. Do not claim the release passed until this
last job succeeds.

The `verify-published-install` job disables mise's normal 24-hour release-age delay
only for that freshly published version. It does not disable signatures, signer
identity, hashes, or platform requirements. Normal users retain mise's default
age protection.

## Local verification

On Linux, the packaging tests and Go unit tests can run; native Mac/launchd,
codesign, and loader checks cannot. On a Mac with the pinned toolchains and Xcode:

```sh
python3 -m unittest discover -s scripts -p 'test_*.py' -v
go test -race ./...
go vet ./...
PACKAGE_SOURCE=1 scripts/build-native.sh 0.1.0 arm64 # amd64 on a native Intel Mac
```

The source export uses committed distribution files. Commit the intended changes
before local packaging. The native build is a prerequisite to any statement that
a release is runnable; configuration review alone is not native build evidence.

## Authoritative references checked for this implementation

- [GitHub standard hosted runner labels and public-repository pricing](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- [RustDesk Server 1.1.16](https://github.com/rustdesk/rustdesk-server/tree/73523b31cfd25d77dee862e6fc9f5e1fb5e485ef)
- [Rust 1.98.1 release](https://blog.rust-lang.org/2026/09/03/Rust-1.98.1/)
- [Cargo vendor](https://doc.rust-lang.org/cargo/commands/cargo-vendor.html)
- [mise Packslip backend](https://mise.jdx.dev/dev-tools/backends/packslip.html)
- [Packslip publishing and permissions](https://packslip.dev/docs/publishing/)
- [Packslip artifact TOML schema](https://packslip.dev/docs/describing-releases/)
- [Packslip host requirements](https://packslip.dev/docs/host-requirements/)
- [Packslip verification semantics](https://packslip.dev/docs/verifying/)
- [Exact action source used](https://github.com/jdx/packslip/blob/87479dfc6443253dff69601cace5fc6ea07e6df5/action.yml)
