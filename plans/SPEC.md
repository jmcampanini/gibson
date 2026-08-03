# Gibson v1 Specification

Gibson is a Go CLI that runs a localhost web server for driving [pi](https://github.com/badlogic/pi-mono) coding-agent sessions from a browser. This document is the complete v1 specification, written for implementers who have no other context. Requirements use MUST / SHOULD / MAY in the RFC 2119 sense. Sections are numbered for reference (e.g. "requirement 4.2").

---

## 1. Overview

### 1.1 What gibson is

A single Go binary that:

1. Runs from inside a checkout of a git repository.
2. Serves an embedded React single-page application over HTTP.
3. Spawns and supervises `pi --mode rpc` subprocesses — one per live agent session.
4. Fans out pi's event stream to any number of connected browser clients via SSE.
5. Accepts actions (send prompt, abort, answer dialog, …) via REST.

The server owns sessions; browser tabs are disposable viewers. A session outlives every tab and survives server restarts (§6.4).

### 1.2 Non-goals for v1

Gibson v1 MUST NOT include:

- Authentication or authorization of any kind (network exposure is controlled solely by the bind address, §4.2).
- Prompt management (templates, prompt libraries).
- Session tree / branch navigation or fork UI.
- Compaction controls.
- Mid-session model or thinking-level switching.
- Slash-command palette (`get_commands`-driven autocomplete).
- Bash console or PTY terminal.
- File browser.
- HTML export.
- Serving more than one workspace from one server instance.
- Layered / multi-file configuration.

### 1.3 Design lineage (context, non-normative)

The architecture follows the convergent pattern of OpenCode, Happy Coder, the Claude Agent SDK, Codex app-server, and pi's own experimental server package: agent-as-machine-protocol, a long-lived ownership process decoupled from the UI, attach-by-snapshot-plus-subscribe, spawn-with-resume as the universal recovery move, and approvals as first-class async protocol messages. Closest prior art: `@earendil-works/pi-server` (first-party, experimental) and `jmfederico/pi-web` (third-party).

---

## 2. Environment model

### 2.1 Workspace layout

Gibson targets a grove-style workspace (as managed by grove-cli):

```
~/Code/<host>/<org-or-owner>/<repo>/     ← workspace root
├── main/                                 ← primary checkout (default branch)
├── wt-<name>/                            ← sibling worktrees
└── wt-<other>/
```

- 2.1.1 Gibson MUST be launched from inside a checkout (typically `main/`).
- 2.1.2 Gibson MUST read `gibson.toml` from the root of the checkout it is launched in (the git repository root of that checkout).
- 2.1.3 Gibson MUST derive the **workspace root** as the parent directory of that checkout.
- 2.1.4 One gibson server instance governs exactly one workspace (all sibling checkouts of that repo).

### 2.2 Checkout discovery

- 2.2.1 Gibson MUST enumerate available checkouts itself (via `git worktree list` from the launch checkout), not from config.
- 2.2.2 The target checkout for a session is chosen **at launch time** in the UI, orthogonally to the session type. Session-type config MUST NOT pin a checkout.

---

## 3. Configuration — `gibson.toml`

### 3.1 Location and lifecycle

- 3.1.1 Exactly one config file: `gibson.toml` at the repository root, committed to version control (it therefore exists in every checkout).
- 3.1.2 No config layering, no XDG/global fallback, no per-user overrides in v1.
- 3.1.3 Gibson MUST fail with a clear error if `gibson.toml` is missing or invalid.
- 3.1.4 Unknown configuration keys MUST be accepted without startup diagnostics.

### 3.2 Schema

```toml
[server]
port = 7311            # required
bind = "127.0.0.1"     # optional, default "127.0.0.1"

pi_bin = "/path/to/pi" # optional, default: resolve `pi` from $PATH

[sessions.review]
description = "Adversarial code review"   # required
model = "anthropic/claude-opus-5"          # optional
thinking = "high"                          # optional
extra_args = ["-e", "~/.pi/agent/git/github.com/earendil-works/pi-review/review.ts"]  # optional

[sessions.quick]
description = "Quick one-off task"
```

- 3.2.1 `server.port` is required. `--port` as a CLI flag MUST override it. If the port is taken, gibson MUST exit with a clear error (no auto-increment).
- 3.2.2 `server.bind` defaults to `127.0.0.1`. The user may set a LAN/tailnet IP or `0.0.0.0`; gibson performs no auth regardless (§1.2).
- 3.2.3 `pi_bin` defaults to resolving `pi` from `$PATH`.
- 3.2.4 Each `[sessions.<name>]` table defines a **session type**. `description` is required. `model` maps to pi's `--model`, `thinking` to `--thinking`. `extra_args` is appended verbatim to the pi command line — this is the escape hatch to pi's full flag surface (extensions, tools, skills, system prompt, …). Gibson MUST NOT parse or validate `extra_args`.
- 3.2.5 At least one session type MUST be defined for gibson to be useful; zero types is a startup warning, not an error.

---

## 4. Storage — per-checkout `.gibson/`

### 4.1 Layout

All session data lives inside the checkout the session runs in:

```
<checkout>/.gibson/
├── sessions/        # passed to pi as --session-dir; pi's JSONL session files
├── state.json       # gibson's session registry for this checkout
└── logs/
    └── <session-id>.stderr.log
```

- 4.1.1 Pi session JSONL files are the ground truth for conversation content. `state.json` holds only gibson-owned metadata per session: gibson session id, display name, session type name, status (`live` | `stopped` | `closed`), created-at, last-activity.
- 4.1.2 The registry MUST be rebuildable: if `state.json` is lost, gibson SHOULD still list the sessions found in `sessions/` (with degraded metadata).
- 4.1.3 A session is fully self-contained with its worktree: deleting/pruning the worktree deletes its sessions. This is intended behavior.
- 4.1.4 "List all sessions" = enumerate checkouts (§2.2.1), scan each checkout's `.gibson/`. There is no workspace-level registry.

### 4.2 Git hygiene

- 4.2.1 `.gibson/` MUST be excluded from version control via a committed `.gitignore` entry.

---

## 5. Process model

### 5.1 One subprocess per live session

- 5.1.1 A live session is backed by exactly one subprocess: `pi --mode rpc --session-id <id> --session-dir <checkout>/.gibson/sessions [--model …] [--thinking …] [extra_args…]` spawned with the target checkout as its working directory.
- 5.1.2 Gibson assigns the session id (it MUST match pi's id rules: `^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`).
- 5.1.3 **Single-writer rule:** pi does no session-file locking. Gibson's subprocess MUST be the only writer to a session's file. Gibson MUST NOT spawn two processes for the same session id.
- 5.1.4 Pi stderr MUST be captured to `logs/<session-id>.stderr.log`.

### 5.2 Lifetime

- 5.2.1 A live session's process is **kept alive until the user explicitly closes the session** from the UI. No idle-timeout reaping.
- 5.2.2 Closing a session gracefully terminates the process (SIGTERM; pi exits 143) and marks the registry entry `closed`. Closed sessions remain listed and can be reopened (§5.3).

### 5.3 Resume on demand

- 5.3.1 When gibson stops, its pi subprocesses die with it. Session files persist.
- 5.3.2 On startup, gibson MUST mark any registry entries still recorded as `live` as `stopped` (orphan cleanup). Gibson MUST NEVER attempt to re-attach to a running orphan process.
- 5.3.3 A `stopped` or `closed` session is resumed lazily: the next user message (or explicit reopen) respawns pi with the same `--session-id` and `--session-dir`. Pi reconstructs context from the session file.
- 5.3.4 Gibson MUST NOT eagerly respawn sessions at startup.

### 5.4 Pi binary compatibility

- 5.4.1 Gibson v1 requires **pi 0.82.0 or newer** and MUST check `pi --version` at startup. The 0.82.x line is verified. Versions below 0.82.0 MUST fail with a clear error naming the found and minimum versions; later minor or major versions MUST be allowed to start with a warning that they are not yet verified. The pi ecosystem has a history of breaking renames, so version drift MUST remain visible.

---

## 6. Pi RPC integration

Reference documentation ships inside the installed pi package — implementers MUST read it: `~/.local/lib/node_modules/@earendil-works/pi-coding-agent/docs/rpc.md` (also `sdk.md`, `session-format.md`). The facts below are the load-bearing subset.

### 6.1 Framing

- 6.1.1 The protocol is strict JSONL over stdin/stdout: one JSON object per line, LF-terminated.
- 6.1.2 Readers MUST split on `\n` ONLY (stripping an optional trailing `\r`). Unicode-aware line splitting (U+2028/U+2029) corrupts legal JSON strings and MUST NOT be used.
- 6.1.3 Writers MUST respect stdout/stdin backpressure; pi applies backpressure on its side rather than dropping events.

### 6.2 Commands (stdin → pi)

Commands are `{"id": "<optional-correlation-id>", "type": "<command>", …}`. Pi replies `{"type":"response","command":"<command>","id":…,"success":true|false,…}`. Commands gibson v1 uses:

| Command | Notes |
| --- | --- |
| `prompt` | `{message, images?, streamingBehavior?}`. `streamingBehavior: "steer" \| "followUp"` is REQUIRED when the agent is already streaming; the command errors otherwise. Response arrives when the prompt is accepted/queued, not when it completes. |
| `steer` / `follow_up` | Dedicated variants of mid-stream sends. |
| `abort` | Stops the current run; aborted assistant messages carry `stopReason:"aborted"`. |
| `get_state` | Model, thinking level, `isStreaming`, session file/id/name, message counts. |
| `get_entries` | `{since?: <entryId>}` → `{entries, leafId}`. Entry ids are durable cursors across restarts. Invalid `since` → `success:false` (client must full-refetch). |
| `get_session_stats` | Includes `contextUsage: {tokens, contextWindow, percent}`. |
| `set_session_name` | For the launch-flow name field. |

### 6.3 Events (pi → stdout)

Pi forwards every `AgentSessionEvent` verbatim. The ones gibson consumes:

- Lifecycle: `agent_start`, `agent_end`, `agent_settled`, `turn_start`, `turn_end`.
- Streaming: `message_start`, `message_update`, `message_end`. Deltas ride `message_update.assistantMessageEvent` (`text_delta`, `thinking_delta`, `toolcall_delta`, plus start/end markers and a cumulative `partial` snapshot).
- Tools: `tool_execution_start` / `update` / `end`. `partialResult` in `update` is **cumulative** — replace, don't append.
- Session metadata: `session_info_changed` (`{name}`).

**Durable entry feed — gibson-constructed.** Pi 0.82.1 emits **no per-append event for ordinary entries**. An undocumented `entry_appended` (`{entry}`) event exists but fires only for extension-appended custom entries (`ctx.appendEntry`; verified in the 0.82.1 source) — it MUST NOT be treated as a general entry feed. Gibson MUST therefore build the entry feed itself: keep a per-session cursor (last known entry id) and, on each event that signals persisted entries — `message_end` (pi persists user / assistant / toolResult / custom messages here), `entry_appended`, `session_info_changed`, `compaction_end`, plus `agent_end` / `agent_settled` as catch-alls — issue `get_entries {since: cursor}`, emit the newly returned entries to clients in order, and advance the cursor. Pi appends the entry in the same synchronous step that emits these events, so a `get_entries` issued after observing one always sees the corresponding entries. All durable entries reach clients through this single ordered path (§7.2.2); `entry_appended.entry` is only a sync trigger, never forwarded directly.

### 6.4 Extension UI sub-protocol (dialogs)

Pi has **no built-in permission system**; approvals and all interactive prompts come from extensions and surface as:

```json
{"type":"extension_ui_request","id":"<uuid>","method":"select|confirm|input|editor|notify|setStatus|setWidget|setTitle|set_editor_text", …}
```

- 6.4.1 `select`, `confirm`, `input`, `editor` **block the agent** until the client writes `{"type":"extension_ui_response","id":"<uuid>", value|confirmed|cancelled}` to stdin.
- 6.4.2 `notify`, `setStatus`, `setWidget`, `setTitle`, `set_editor_text` are fire-and-forget (no response).
- 6.4.3 If a request carries `timeout` (ms), **pi auto-resolves it with the default** when no answer arrives; gibson does not track timeouts.
- 6.4.4 Extension `ui.custom()` (arbitrary terminal components) returns `undefined` over RPC — it silently no-ops. This is an accepted degradation (§10.4).

---

## 7. HTTP API

The server exposes a JSON REST API plus one SSE stream per session. Exact paths MAY differ; the semantics below MUST hold.

### 7.1 REST actions

| Endpoint (indicative) | Semantics |
| --- | --- |
| `GET /api/config/session-types` | Session types from `gibson.toml` (name, description, model, thinking). |
| `GET /api/checkouts` | Enumerated checkouts of the workspace. |
| `GET /api/sessions` | All sessions across all checkouts: id, name, type, checkout, status (`idle` / `streaming` / `blocked-on-dialog` / `stopped` / `closed`), last activity. |
| `POST /api/sessions` | Create: `{type, checkout, name?, message}`. Spawns pi, sends first prompt, returns session id. |
| `GET /api/sessions/:id/history` | Snapshot of the session's entries (for initial render), plus the current cursor (`leafId` / last entry id) and any currently pending dialog. |
| `POST /api/sessions/:id/message` | `{message, behavior?: "steer"\|"followUp"}`. Respawns pi first if the session is `stopped`/`closed` (§5.3.3). `behavior` is required when the session is streaming. |
| `POST /api/sessions/:id/abort` | Abort the current run. |
| `POST /api/sessions/:id/dialogs/:dialogId` | Answer a blocking dialog: `{value?, confirmed?, cancelled?}`. First answer wins; later answers get `409`. |
| `POST /api/sessions/:id/close` | Terminate the pi process, mark `closed`. |

### 7.2 SSE stream

- 7.2.1 `GET /api/sessions/:id/events` — one stream per open session per client. Carries the session's pi events (translated or verbatim), dialog requests/resolutions, and gibson lifecycle events (status changes).
- 7.2.2 Durable entry events (from the gibson-constructed entry feed, §6.3) MUST use the pi entry id as the SSE event `id`, so `Last-Event-ID` on reconnect (and an equivalent `?since=` query parameter for first connect after a snapshot) resumes the stream gaplessly via `get_entries since:<cursor>`. Ephemeral events (deltas, status) ride between durable ids.
- 7.2.3 The server MUST send a heartbeat comment at least every 30 seconds on idle streams.
- 7.2.4 Per-client send buffers MUST be bounded; a slow or dead client MUST be disconnected rather than allowed to stall the pi stdout pump or other clients. Disconnected clients recover via reconnect + cursor replay.

### 7.3 Multi-client semantics

- 7.3.1 All connected clients are equal peers — any client may prompt, steer, abort, answer dialogs, or close.
- 7.3.2 Blocking dialogs are broadcast to all clients of a session. The first answer wins and is forwarded to pi; all clients then receive a dialog-resolved event (open modals close everywhere).
- 7.3.3 New/reconnecting clients attach by **snapshot + subscribe**: fetch `/history`, then open SSE from the returned cursor. No socket-level replay.

---

## 8. Frontend

### 8.1 Stack

- 8.1.1 React + TypeScript + Vite.
- 8.1.2 Production build is embedded in the Go binary via `go:embed`; `gibson` ships as a single artifact.
- 8.1.3 Development mode MUST support the Vite dev server (gibson proxies or CORS-allows it) for hot reload.

### 8.2 v1 UI scope

**Session list** (home): all sessions across checkouts showing name, type, checkout, status (`idle` / `streaming` / `blocked-on-dialog` — the last MUST be visually loud, §10.3), and last activity; actions: open, close, new session.

**Launch flow**: choose session type (from config) + target checkout (from enumeration) + optional display name + first message.

**Chat view**:

- Markdown-rendered user and assistant messages.
- Live streaming text (token deltas).
- Thinking blocks collapsed by default, expandable.
- Tool calls as collapsible cards: tool name + args, live cumulative `partialResult` updates, final result, error state.
- The four blocking dialogs (`select`, `confirm`, `input`, `editor`) as modals; answered/resolved state syncs across clients.
- `notify` as toasts; `setStatus` / `setWidget` strings in a status strip.
- Abort button while streaming.
- Context-usage meter (from `get_session_stats`).
- Composer: plain send when idle; when streaming, send = steer and an alternate action queues a follow-up (pi requires the distinction, §6.2).
- Generic fallback card for `custom_message` entries (e.g. `customType: "subagent_result"`): labeled, rendered as formatted JSON/text. Per-`customType` renderers are post-v1.

---

## 9. Acceptance criteria

### 9.1 Config & startup

- a. `gibson` run in a checkout with a valid `gibson.toml` serves the SPA on the configured bind/port.
- b. Missing/invalid config, occupied port, missing pi binary, or a pi version below 0.82.0 each produce a distinct, clear error.
- c. Pi 0.82.x starts as verified; a later minor or major version starts with a visible unverified-version warning.

### 9.2 Sessions

- a. Creating a session of a configured type in a chosen checkout spawns exactly one pi process whose cwd is that checkout and whose session file appears under `<checkout>/.gibson/sessions/`.
- b. Streaming responses render token-by-token; tool calls show live progress; abort stops the run and the UI reflects it.
- c. Sending while streaming steers (or queues a follow-up when chosen).
- d. Closing a session terminates its process; reopening/resuming respawns pi with the same session id and full history intact.
- e. After Gibson creates `.gibson/` data in a checkout with the required committed ignore entry, `git status --porcelain` is empty.

### 9.3 Multi-client

- a. Two clients on one session both see the same stream; a dialog appears on both; answering on one closes it on the other.
- b. Killing and reopening a client mid-stream loses nothing: snapshot + cursor replay reproduces the exact state.

### 9.4 Restart

- a. Restarting gibson marks previously-live sessions `stopped`; sending a message to one respawns pi and continues the conversation coherently.

### 9.5 End-to-end agent-verified workflow

v1 is **done** when an agent (not a human) completes this workflow against a real build, using browser automation:

1. Create a scratch grove-style workspace with a git repo checkout, a committed `.gitignore` entry for `.gibson/`, and a `gibson.toml` defining at least one session type whose `extra_args` loads a test extension that calls `ctx.ui.confirm()` before running a tool.
2. Launch gibson; open the UI in a browser.
3. Create a session (type + checkout + first message); observe the streamed assistant response.
4. Trigger the extension dialog; answer it from the browser; verify the agent proceeds.
5. While a response streams, open a second browser client; verify it renders identical state via snapshot + cursor replay and receives the remainder of the stream live.
6. Kill the gibson server; restart it; verify the session lists as `stopped`; send a follow-up message; verify pi respawns and the conversation continues with full context.
7. Verify the session JSONL file and stderr log exist under `<checkout>/.gibson/`, and that `git status --porcelain` in the checkout is empty.

All seven steps passing proves v1.

---

## 10. Watch-outs

- **10.1 Single-writer rule.** Never open a gibson-owned session with terminal `pi` while its gibson process is alive — pi has no locking and concurrent writers corrupt the entry tree. The terminal escape hatch (`pi --session-dir .gibson/sessions --resume`) is safe only for `stopped`/`closed` sessions.
- **10.2 SSE stream hygiene.** Heartbeat idle streams (§7.2.3); bound per-client buffers and drop slow clients (§7.2.4). A stalled phone must never block the pi pump.
- **10.3 Dialog-blocked visibility.** A blocking dialog with no connected client simply waits (unless the extension set a timeout). With keep-alive processes this is correct — but the session list MUST surface `blocked-on-dialog` loudly so a waiting session is never invisible.
- **10.4 `custom()` degradation.** Extensions using `ui.custom()` silently no-op over RPC. On this machine that affects: pi-review's triage UI, fuzzy-explorer, interactive-subagents' status screen, and pi-sidequest's command palette. Session types intended for web use should omit extensions whose core value is a custom TUI.
- **10.5 Protocol churn.** Pi renamed its org and packages within months of this spec. Enforce and report the compatibility policy in §5.4, read the docs shipped with the _installed_ version, and treat undocumented event fields as unstable.
