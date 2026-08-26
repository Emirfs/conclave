# Conclave

Conclave is a daemon-first local orchestrator for AI providers and test pipelines. It keeps work running when the terminal UI disconnects, exposes the same local API to a desktop-like TUI and headless commands, and integrates with Mnemo for project-scoped shared memory.

## Status

The initial vertical slice provides:

- a persistent local daemon with bounded concurrent workers;
- ordered test pipelines stored in SQLite;
- direct process execution without an intermediate shell;
- discovery of Claude, Codex, Gemini, Ollama, and Mnemo CLIs;
- a JSON headless client and a Bubble Tea TUI;
- a provider-neutral boundary for future subscription CLI and API adapters.

Provider execution and Mnemo read/write are intentionally not enabled yet. Credentials and provider session data will never be persisted in Mnemo.

## Requirements

- Go 1.26 or newer
- Optional provider CLIs: `claude`, `codex`, `gemini`, `ollama`
- Optional shared memory CLI: `mnemo`

## Build

```powershell
go build ./...
```

## Run

Start the daemon:

```powershell
go run ./cmd/conclave daemon
```

Open the TUI in another terminal:

```powershell
go run ./cmd/conclave tui
```

Inspect daemon state without a TUI:

```powershell
go run ./cmd/conclave status --json
```

Submit a pipeline. Each `--stage` is `name=executable,arg,...`; commands are executed directly, not through a shell.

```powershell
go run ./cmd/conclave run --project . `
  --stage "build=go,build,./..." `
  --stage "test=go,test,./..."
```

State is stored under `%LOCALAPPDATA%\conclave` on Windows and the platform user config directory elsewhere. The local HTTP API listens on `127.0.0.1:7331`, requires the generated state-directory bearer token, and rejects browser-origin requests.
