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
- **Run:** `go run ./cmd/conclave daemon` and `go run ./cmd/conclave tui`

Read `CONTEXT.md` before changing package boundaries.

### Invariants

- The daemon owns runtime and persisted state; TUI and headless commands are clients.
- Test commands execute directly as argument arrays. Never introduce implicit shell evaluation.
- Provider credentials, tokens, and session data must not enter logs, SQLite, prompts, or Mnemo.
- Mnemo writes are drafts and project-scoped; verified memory remains human-controlled.
