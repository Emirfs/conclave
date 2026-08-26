# Context

Conclave is a local, daemon-first orchestrator for parallel AI providers and deterministic test pipelines.

| Path | Responsibility |
|---|---|
| `cmd/conclave/` | CLI entry point and command parsing. |
| `internal/api/` | Versioned local HTTP server and client contracts. |
| `internal/daemon/` | Worker lifecycle and pipeline coordination. |
| `internal/domain/` | Runtime-neutral provider and pipeline data. |
| `internal/provider/` | Provider CLI discovery and future adapters. |
| `internal/store/` | SQLite persistence and restart recovery. |
| `internal/tui/` | Bubble Tea client; presentation state only. |

## Invariants

- Runtime state belongs to the daemon, never the TUI.
- Pipeline stages are ordered and stop at the first failure.
- Commands are argument arrays and bypass the command shell.
- SQLite is operational state; Mnemo is shared semantic memory. Neither stores credentials.
- Provider adapters must support capability discovery before dispatch.
