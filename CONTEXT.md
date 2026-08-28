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
| `internal/update/` | Cached release check against GitHub. Looks only. |
| `internal/vcs/` | Read-only git status and diff for a card's project. |
| `internal/version/` | The running build's version, stamped in at link time. |
| `install.ps1` | Installs and updates a released build on Windows. |

## Invariants

- Runtime state belongs to the daemon, never a client. The desktop app is a client like any other.
- The frontend never speaks HTTP and never holds the daemon token. JavaScript calls Go through Wails
  bindings; only the Go side reads the token and reaches the local API. This is what keeps the
  browser-origin rejection in `internal/api` meaningful.
- Pipeline stages are ordered and stop at the first failure.
- A pipeline card holds the definition; running it queues an ordinary run. Nothing about execution is
  special-cased for the board, which is what keeps a pipeline queued from a terminal and one queued
  from a card the same thing. A pipeline needs a project and at least one stage before it can run,
  and is never queued twice at once: two copies would work the same tree simultaneously.
- A pipeline card carries no provider and no transcript. Deterministic work is exactly what a
  conversation card is not, and merging the two would make a card's colour mean nothing.
- Commands are argument arrays and bypass the command shell.
- Chat context is scoped by conversation *and* provider. Two conversations with the same provider must
  never see each other's history.
- A provider that resumes its own session already holds that history, so the transcript is not replayed
  into the prompt. The transcript stands in for a session, it does not accompany one.
- Linked cards are briefed once, before their first relayed message, rather than being told the
  arrangement on every hop. A briefing rides along with a real message; it never costs a turn.
- A card asking for a user decision does not end an exchange by itself: the other card is nudged to
  decide and go on. Two such requests in a row park the dialogue for the user.
- A role is only text in the briefing. It never changes a card's access or what it may decide, so any
  provider can take any position in a workflow.
- Context size is read from each provider's own usage report. A large window restates the card's role;
  a full one drops the provider session and lets the transcript carry the conversation into a new one.
- A finished exchange leaves its result on the board as its own card. The conclusion of a dialogue is
  not something the user should have to scroll a card to find.
- Branching forks an answer into one card per provider, never a single group card: the paths are
  supposed to diverge, and a group card would merge them again.
- The model a provider runs on belongs to the card, not to the session: the session only records
  what the last run reported. Changing a card's model drops that provider's session, so the
  transcript carries the conversation into a session that really is on the new model. A card that
  chooses nothing runs on the CLI's own default.
- A provider's model list is that provider's own: codex renders its catalogue, agy lists its models,
  ollama reports what is pulled. Only Claude Code, which cannot be asked, carries a list in this
  build. No list is a constraint — an unlisted name is still accepted, because a provider gains
  models between releases and a card must not be held back by this build's idea of what exists.
- An import is additive and never replaces a board: replacing one would throw away work that is
  still running. An imported transcript is written as history, never queued — a response that was
  mid-answer when the board was exported arrives canceled, because re-queueing it would send
  somebody else's prompt to a provider. An export from a newer build is refused rather than
  half-understood.
- Search matches in Go, not in SQL. SQLite's LIKE folds ASCII only, and the board is written in
  Turkish; the fold also flattens Turkish letters onto their plain forms, so a query typed without
  diacritics still finds what is there. It maps one rune to one rune, which is what lets a match be
  located back in the original text.
- Stopping a turn is a client writing a request to SQLite, never a client reaching a process. The
  worker that owns the provider polls for it and kills the tree; a queued response, which no worker
  owns, is finished on the spot. A stop keeps the partial answer, is not a failure, and relays
  nothing onwards: an interrupted answer is not a result.
- A provider runs at most one chat job at a time.
- Each card carries its own project directory and access level; providers run there, as they would in
  a terminal. `edit` access auto-approves file changes and commands, because `--print` runs cannot ask.
- Client-supplied paths are untrusted: reject absolute paths and parent traversal before touching disk.
- Nothing installs itself. The daemon only ever *looks* for a newer release, on a timer, and caches
  the answer; the bytes are fetched and the binaries replaced by `install.ps1`, and only after a
  person clicks Güncelle or runs the script. A build that cannot say which release it is reports
  `dev` and does not check at all.
- SQLite is operational state; Mnemo is shared semantic memory. Neither stores credentials.
- Schema changes are append-only migrations tracked with `PRAGMA user_version`. Never edit an applied
  migration.
