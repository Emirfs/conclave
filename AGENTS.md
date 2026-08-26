# AGENTS.md

<!-- ghd:core:start -->
<!-- Emirfs/ghd tarafindan uretildi. Elle duzenleme; kaynak: rules/GITHUB-RULES.md -->
GitHub islemlerinde `Emirfs/ghd` reposundaki `rules/GITHUB-RULES.md` kurallari gecerlidir.
<!-- ghd:core:end -->

## Repository

- **Purpose:** Daemon-first local AI provider and test pipeline orchestrator.
- **Type:** Go
- **Build:** `go build ./...`
- **Test:** `go test ./...`
- **Run:** `go run ./cmd/conclave daemon`, then `wails dev` in `cmd/conclave-desktop`

Read `CONTEXT.md` before changing package boundaries.

### Invariants

- The daemon owns runtime and persisted state; the desktop client and headless commands are clients.
- The frontend never speaks HTTP and never holds the daemon token; it calls Go through Wails bindings.
- Chat context is scoped by conversation and provider; conversations never leak into each other.
- Schema changes are append-only migrations tracked with `PRAGMA user_version`.
- Test commands execute directly as argument arrays. Never introduce implicit shell evaluation.
- Provider credentials, tokens, and session data must not enter logs, SQLite, prompts, or Mnemo.
- Mnemo writes are drafts and project-scoped; verified memory remains human-controlled.
