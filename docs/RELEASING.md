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
SQLx compile-time query checks use a disposable `.build/build-schema-arm64.sqlite3`
copied from the pinned commit, through an explicit `DATABASE_URL`. This keeps
SQLite header/journal writes outside the tracked upstream source. The strict
source-cleanliness check remains enabled and reports modified filenames.

The toolchains are Rust `1.98.1` and Go `1.27.1`. Changing either is an explicit
maintenance change: update `upstream.json` and workflow setup inputs together.
Actions are pinned to reviewed full commits. Runner image contents are not
immutable, so release metadata records the actual SDK, Xcode, macOS, and compiler
versions. The archive serialization is stable; this does not claim bit-for-bit
reproducibility across different Apple SDKs or runner-image revisions.

Native builds and published-install smoke tests use `macos-26` for Apple Silicon.
This is a standard GitHub-hosted runner, free for public repositories. No paid larger
runner labels, self-hosted machines, Rosetta, or cross-linked release builds are
used. Native jobs intentionally skip private repositories to avoid billed macOS
minutes. GitHub repository/account quotas and policy still apply.

The declared deployment minimum is **macOS 15.0** on Apple Silicon.
The build exports `MACOSX_DEPLOYMENT_TARGET=15.0`; release checks reject any
Mach-O minimum newer than the declared requirement. The workflow runtime-tests
**macOS 26 only**; load-command checks do not establish macOS 15 runtime
compatibility. Intel artifacts are not built or published.

Project-authored application and release tooling use Go's standard library.
Upstream server code remains Rust; small shell scripts coordinate native commands.
There is no Python build or runtime dependency in this distribution's tooling.

### Compiler caches

Pinned `actions/cache/restore` and `actions/cache/save` actions reuse Cargo registry
sources, Git dependency databases/checkouts, and `.build/upstream-arm64/target`.
The Rust key includes OS/CPU, the native target, source/submodule/toolchain/minimum-OS
pins, build-script/workflow hashes, and the actual macOS/Xcode/SDK/clang/rustc
fingerprint. The exact pinned upstream commit fixes `Cargo.lock`; its lockfile is
still checked with `--locked`. Rust caches have no broad fallback across these
inputs. A restored target directory may exist before the upstream Git checkout;
the build initializes Git alongside it and still verifies every source pin.

Go caches the directory reported by `go env GOCACHE`, keyed by OS, CPU, actual Go
version and Go-source/module hashes. Compatible earlier source-cache entries may
warm a changed build; Go's own content checks decide which entries can be reused.
No `go.sum` or third-party Go module is required. Unit/native jobs populate the
caches; bootstrap and release jobs only restore them.

Only successful `push` jobs on `main` save caches, after their tests/build/audits
and (for native jobs) source packaging and artifact upload finish. PRs, manual
dispatches and tag releases never save. GitHub's branch/ref cache isolation lets
tag releases restore default-branch caches while excluding PR merge-ref caches.
Credentials, GitHub tokens, server keys, databases and release-signing material
are not cache paths. A miss or cache-service failure falls back to a cold build;
no build, lockfile, native audit or signing check is skipped. GitHub may evict
unused caches. Cache restore logs identify exact/partial hits; claim a speedup
only after comparing successful build timings, not merely after adding caching.

## What CI checks

1. Go unit/race tests, vet, and packaging unit tests
2. Native Rust `cargo build --release --locked --target … --bin hbbs --bin hbbr`
3. Native standard-library-only Go helper build with `-trimpath` and version info
4. Exact Mach-O architecture via `lipo`, loader dependencies via `otool -L`, and
   deployment target via `otool -l`
5. No Homebrew, developer-directory, `@rpath`, or other non-system shared-library
   dependencies; only `/usr/lib/` and `/System/Library/` are allowed
6. Ad-hoc code signing, signature verification, and each binary's `--help`
7. An isolated, nonprivileged launchd fixture test in the login user's GUI domain,
   using temporary private paths and unique labels; CI does not start real
   RustDesk server listeners
8. Offline resolution of the complete corresponding-source tree with an empty
   Cargo cache
9. Packslip signature and all installable artifact/SBOM digests before publication
10. Actual mise Packslip installation from the published release on Apple Silicon
    macOS 26, followed by version/help, architecture, and signature checks

Ad-hoc signing is **not Apple Developer ID signing or notarization**. The
Packslip OIDC signature independently authenticates the release manifest and
artifact bytes; it does not assert Apple notarization or absence of vulnerabilities.

## Release assets

- `rustdesk-server-macos-VERSION-darwin-arm64.tar.gz`
- `.cdx.json` CycloneDX SBOM and `.source.json` build/source inventory for arm64
- `rustdesk-server-macos-VERSION-source.tar.gz`, complete corresponding source
- `SHA256SUMS`, `packslip.toml`, and `packslip.sigstore.json`

Each installable archive contains `bin/rustdesk-server`, `bin/hbbs`, `bin/hbbr`,
and `share/rustdesk-server/` with source metadata, Cargo.lock, SBOM, and notices.
All three executable paths are declared in the signed Packslip manifest. The
archives have no generated server identity, runtime database, configured hostname,
or user-specific plist.

The source archive contains the distribution's tracked Go source/tooling and build scripts,
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

The existing CI/release workflow publishes only when running at a `vSEMVER` tag,
whether triggered by a tag push or explicitly dispatched at that tag. Review the
native build before releasing. Creating a release tag and starting publication are
release operations and require the repository owner's authorization.

### Start a release through GitHub's Run workflow button

Use **Start release from tested main** (`release-bootstrap.yml`) with branch
**main**, `release_tag` such as `v0.1.0`, and `tested_commit` containing the full
40-character main commit SHA. That SHA must equal the bootstrap run's own
`github.sha`; if main advances before dispatch, first wait for the new commit's CI
and use its SHA instead. The bootstrap workflow itself must already be on main.

Before making changes, the bootstrap checks that exact commit has a completed,
successful main/push run of `ci-release.yml`, including successful `unit` and
`macos-26` native Apple Silicon jobs. A merely queued, skipped, or partially
successful run is insufficient. It then creates a lightweight tag at exactly that
commit, only if absent. An existing tag must resolve to the same commit; annotated
tags are explicitly peeled, and mismatched/noncommit/cyclic targets are refused.
There is no force-update or tag-moving operation.

The bootstrap uses only its job's existing `GITHUB_TOKEN` with `contents: write`
and `actions: write`; it adds no PAT, secret, or repository-setting change.
GitHub suppresses push workflows for tags created by `GITHUB_TOKEN`, so bootstrap
explicitly calls `workflow_dispatch` for `ci-release.yml` with the tag as `ref`.
GitHub permits that event to create a workflow run. The unchanged release workflow
then builds and tests Apple Silicon again, and signs/publishes only artifacts
from that new tag run. Its tag, source commit, Packslip URLs, and signing identity
therefore remain those of a normal tag release.

A successful bootstrap means the release run was requested, not that publication
finished. Follow the resulting **Native macOS build and signed release** run
through its final `verify-published-install` jobs. If bootstrap cannot dispatch
after creating the tag, inspect the error and safely rerun it; the exact existing
tag is checked before any dispatch.

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
go test -race ./...
go vet ./...
PACKAGE_SOURCE=1 scripts/build-native.sh 0.1.0 arm64
```

The source export uses committed distribution files. Commit the intended changes
before local packaging. The native build is a prerequisite to any statement that
a release is runnable; configuration review alone is not native build evidence.

## Authoritative references checked for this implementation

- [GitHub standard hosted runner labels and public-repository pricing](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- [macOS 26 standard runner availability](https://github.blog/changelog/2026-02-26-macos-26-is-now-generally-available-for-github-hosted-runners/)
- [Official Go downloads](https://go.dev/dl/)
- [RustDesk Server 1.1.16](https://github.com/rustdesk/rustdesk-server/tree/73523b31cfd25d77dee862e6fc9f5e1fb5e485ef)
- [Rust 1.98.1 release](https://blog.rust-lang.org/2026/09/03/Rust-1.98.1/)
- [Cargo vendor](https://doc.rust-lang.org/cargo/commands/cargo-vendor.html)
- [mise Packslip backend](https://mise.jdx.dev/dev-tools/backends/packslip.html)
- [Packslip publishing and permissions](https://packslip.dev/docs/publishing/)
- [Packslip artifact TOML schema](https://packslip.dev/docs/describing-releases/)
- [Packslip host requirements](https://packslip.dev/docs/host-requirements/)
- [Packslip verification semantics](https://packslip.dev/docs/verifying/)
- [Exact action source used](https://github.com/jdx/packslip/blob/87479dfc6443253dff69601cace5fc6ea07e6df5/action.yml)
- [GitHub's GITHUB_TOKEN workflow-dispatch exception](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow)
- [Workflow dispatch API](https://docs.github.com/en/rest/actions/workflows#create-a-workflow-dispatch-event)
- [Pinned cache restore/save action](https://github.com/actions/cache/tree/55cc8345863c7cc4c66a329aec7e433d2d1c52a9)
- [GitHub cache matching and branch isolation](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching)
