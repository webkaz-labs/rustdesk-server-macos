# Project guidance

Read [development and usability principles](docs/DEVELOPMENT_PRINCIPLES.en.md)
([日本語](docs/DEVELOPMENT_PRINCIPLES.ja.md)) before implementation or documentation changes.
Keep these linked principles authoritative rather than copying another checklist here.

- Keep Japanese and English human interfaces aligned, with automatic locale selection, explicit overrides, and language-independent command/configuration values
- Preserve the user LaunchAgent lifecycle, key identity, owner-only storage, explicit setup confirmation, rollback behavior, and network boundaries documented in both READMEs
- Run gofmt for Go changes, `go test -race ./...`, and `go vet ./...`; retain native Apple Silicon macOS build and isolated launchd CI
- Never treat mocked launchctl tests or local status as real-client connection evidence, or macOS 26 CI as macOS 15 runtime verification
- Keep source pins, complete corresponding source, release signatures, provenance, native audits, and actual mise-installed binary verification intact
- Keep unreleased changes distinct from published features, and follow [release engineering](docs/RELEASING.md) before an authorized publication
