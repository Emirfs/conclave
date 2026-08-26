# Context

Conclave is a local, daemon-first orchestrator for parallel AI providers and deterministic test pipelines.
The interface is a desktop client built on an infinite canvas: conversations and notes are draggable
nodes the daemon persists, so a layout survives a restart.

| Path | Responsibility |
|---|---|
| `cmd/conclave/` | CLI entry point and command parsing. |
| `cmd/conclave-desktop/` | Wails v2 host; React/TypeScript canvas client under `frontend/`. |
| `internal/api/` | Versioned local HTTP server and client contracts. |
| `internal/daemon/` | Worker lifecycle, pipeline and chat execution. |
| `internal/domain/` | Runtime-neutral provider, conversation, canvas and pipeline data. |
| `internal/provider/` | Provider CLI discovery and invocation. |
| `internal/statedir/` | State directory and daemon token shared by every local client. |
| `internal/store/` | SQLite persistence, migrations and restart recovery. |
| `internal/vcs/` | Read-only git status and diff for a card's project. |

## Invariants

- Runtime state belongs to the daemon, never a client. The desktop app is a client like any other.
- The frontend never speaks HTTP and never holds the daemon token. JavaScript calls Go through Wails
  bindings; only the Go side reads the token and reaches the local API. This is what keeps the
  browser-origin rejection in `internal/api` meaningful.
- Pipeline stages are ordered and stop at the first failure.
- Commands are argument arrays and bypass the command shell.
- Chat context is scoped by conversation *and* provider. Two conversations with the same provider must
  never see each other's history.
- A provider runs at most one chat job at a time.
- Each card carries its own project directory and access level; providers run there, as they would in
  a terminal. `edit` access auto-approves file changes and commands, because `--print` runs cannot ask.
- Client-supplied paths are untrusted: reject absolute paths and parent traversal before touching disk.
- SQLite is operational state; Mnemo is shared semantic memory. Neither stores credentials.
- Schema changes are append-only migrations tracked with `PRAGMA user_version`. Never edit an applied
  migration.
