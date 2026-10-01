# Development and usability principles

[日本語](DEVELOPMENT_PRINCIPLES.ja.md) · [User guide](../README.en.md) · [Release engineering](RELEASING.md)

**Make common goals take few steps, with detail available when needed.** Apply these principles to implementation, review, and documentation. They govern the current CLI and any future graphical interface; a design goal is not evidence that a feature has shipped.

## First-class Japanese and English

- Localize onboarding, help, prompts, confirmations, status, errors, and next actions equally in Japanese and English
- Default to automatic locale selection, with an explicit `--lang ja|en|auto` override before or after the command. Keep the exact precedence and safe fallback documented in the [user guide](../README.en.md#display-language) and covered by tests
- Keep command/flag names, confirmation tokens, saved configuration, and machine-readable formats language-independent. Never translate addresses, paths, keys, identifiers, or user-supplied values
- Keep upstream `hbbs` / `hbbr` logs and OS diagnostics intact. Translate the helper's explanation without disguising the original diagnostic or inventing its cause
- Keep output readable in a narrow terminal. Do not depend on color, a browser, or a graphical desktop to explain the next command

## Simple and flexible

- Put install → setup → client settings first. Normal use should not require cloning the repository, compiling software, editing JSON, or understanding launchd internals
- Offer detected LAN and connected Tailscale addresses as candidates, not proof of client reachability; retain manual entry and an explicit address override
- Ask only for inputs necessary to the goal. Show common actions before advanced settings and diagnostics
- Before applying setup, show the address, data location, permissions, login lifetime, restart behavior, and all-interface listening boundary. Keep cancellation before confirmation free of service or configuration changes
- Repeated setup must preserve existing keys and data. Make consequential address or storage changes explicit, reject unsafe migration, and avoid redundant approval for unchanged information
- Where possible, let users correct input at the relevant question. Provide a clear route to cancel, retry, or return to a safe state; do not claim an edit/back flow unless it exists and has been tested

## Clear state and next actions

- Show which services are running, the advertised ID/relay addresses, public key, data location, and login behavior without exposing the private key
- Distinguish saved configuration, local process state, listener checks, and successful connections between real clients. A local status check is not end-to-end verification
- State what is known about a failure and one concrete next step. Report partial failures and rollback failures rather than presenting them as success
- Preserve explicit language choices in guidance where needed; keep example commands safe to copy and make placeholders obvious
- For any future graphical interface, verify information hierarchy, control placement, back navigation, narrow layouts, contrast, and readable text

## Documentation starts with the answer

- Identify what works, the relevant released version, unreleased source changes, and remaining uncertainty near the top
- Put the shortest normal path first. Move detailed conditions, advanced configuration, and exceptions later
- Keep Japanese and English guides equivalent, use consistent terms, and validate links and command examples
- Separate implementation, mocked tests, native automated tests, distribution verification, and real-client verification. Never claim an unperformed check passed
- Keep these principles authoritative by linking here instead of maintaining conflicting copies

## Change checklist

- [ ] Check Japanese and English help, setup questions, confirmation, status, errors, and next actions
- [ ] Test locale precedence, Japanese locale variants, English/unknown/`C`/`POSIX` fallback, empty/unset variables, macOS preferred-language fallback and lookup failure, and overrides on both sides of a command
- [ ] Walk through setup → client settings → status → stop → start; verify the saved configuration and login behavior at each step
- [ ] Test cancellation, EOF, invalid input, retry, repeated setup, existing configuration, unavailable discovery, partial failure, and rollback
- [ ] Verify port conflicts and permission failures offer an actionable next step without silently changing network or system settings
- [ ] Check narrow-terminal output and that addresses, paths, keys, command tokens, and configuration values survive localization unchanged
- [ ] Confirm private keys never appear in status, prompts, logs, public examples, or installable release artifacts
- [ ] Run Go race tests and vet; separately record native launchd/build/signature/install checks and any unperformed real-client or older-macOS checks
- [ ] Match both guides' commands, versions, links, behavior, and verification claims to the exact source or published artifact

See the [user guide's security boundaries](../README.en.md#network-and-security-boundaries) and [release engineering](RELEASING.md) for operational and publication requirements.
