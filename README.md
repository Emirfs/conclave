# Conclave

Conclave is a daemon-first local orchestrator for AI providers and test pipelines. It keeps work running when a client disconnects, exposes the same local API to a desktop canvas client and headless commands, and integrates with Mnemo for project-scoped shared memory.

## Status

The initial vertical slice provides:

- a persistent local daemon with bounded concurrent workers;
- ordered test pipelines stored in SQLite;
- direct process execution without an intermediate shell;
- discovery of Claude, Codex, Antigravity, Ollama, and Mnemo CLIs;
- a JSON headless client and a desktop canvas client;
- conversations as draggable nodes on an infinite canvas, with sticky notes;
- solo conversations per provider and group conversations that fan one message out to all of them;
- a provider-neutral boundary for future subscription CLI and API adapters.

Provider chat runs the official subscription CLIs in isolated temporary directories with write tools
disabled. Conversation history is currently replayed into each prompt; real provider sessions
(`claude --resume`, `codex exec resume`, `gemini --resume`) are the next step. Credentials and provider
session data are not persisted in SQLite or Mnemo. Mnemo read/write integration is intentionally not
enabled yet.

## Requirements

- Go 1.26 or newer
- Wails v2 and Bun, to build the desktop client
- Optional provider CLIs: `claude`, `codex`, `agy`, `ollama`
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

Open the desktop client in another terminal:

```powershell
cd cmd/conclave-desktop
wails dev
```

The client draws an infinite canvas. Click a provider in the left rail to open a solo conversation with
it, use `Grup konuşması` to open one that every available provider answers, and double-click empty
canvas to drop a sticky note. Nodes are dragged and resized freely; positions live in the daemon, so
they survive a restart.

Queue a message without the desktop client. This opens a conversation, which then appears on the
canvas like any other:

```powershell
go run ./cmd/conclave chat --provider claude --provider openai "Compare these two approaches"
```

Continue an existing conversation:

```powershell
go run ./cmd/conclave chat --conversation 12 "And which one would you pick?"
```

Inspect daemon state from the terminal:

```powershell
go run ./cmd/conclave status --json
```

Submit a pipeline. Each `--stage` is `name=executable,arg,...`; commands are executed directly, not through a shell.

```powershell
go run ./cmd/conclave run --project . `
  --stage "build=go,build,./..." `
  --stage "test=go,test,./..."
```

Build a single desktop executable:

```powershell
cd cmd/conclave-desktop
wails build
```

State is stored under `%LOCALAPPDATA%\conclave` on Windows and the platform user config directory elsewhere. The local HTTP API listens on `127.0.0.1:7331`, requires the generated state-directory bearer token, and rejects browser-origin requests.
