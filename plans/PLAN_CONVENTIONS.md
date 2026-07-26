# PLAN_CONVENTIONS

Binding conventions for the eight milestone plans (PLAN_M0.md … PLAN_M7.md). SPEC.md is
normative for behavior; this file pins every cross-milestone seam SPEC.md left open so
independently written plans compose. Where this file and SPEC.md conflict, SPEC.md wins.
Plans MUST use these names, paths, and shapes verbatim — no local variants.

## 1. Module, layout, CLI, libraries

- **Module path:** `github.com/jmcampanini/gibson`. Go 1.26. `main.go` at repo root calling
  `cmd.Execute()` (grove-cli idiom).
- **Directory / package layout** (one-line responsibility each):
  - `cmd/` — cobra commands, one file per command + `_test.go` sibling: `root.go`,
    `serve.go`, `run.go`.
  - `internal/config/` — parse + validate `gibson.toml` (SPEC §3). Struct-tagged TOML with
    a `Validate()` method naming the offending field (grove style).
  - `internal/workspace/` — workspace-root derivation and checkout enumeration via
    `git worktree list --porcelain` (SPEC §2).
  - `internal/pisession/` — spawn/supervise one `pi --mode rpc` process: argv assembly,
    JSONL framing, command/response correlation, typed client, event fan-in (SPEC §5–6).
  - `internal/store/` — `.gibson/` layout, `state.json` registry, session id generation,
    registry rebuild, gitignore check (SPEC §4).
  - `internal/session/` — the Manager: session lifecycle (create/resume/close), status
    machine, per-session event Broker (fan-out, ring of subscribers), dialog registry.
  - `internal/httpapi/` — REST handlers + SSE endpoint + dev proxy; owns wire types.
  - `internal/fakepi/` — `package main`: the fake pi executable for tests (§10).
  - `internal/pitest/` — test helpers: build fakepi, drive scenarios.
  - `internal/testws/` — test helper: scratch grove-style workspace + git repo + gibson.toml.
  - `web/` — the Vite React SPA (§9). `web/embed.go` (`package web`) exposes the built
    `dist/` via `//go:embed dist`.
  - `test/fixtures/` — shared non-Go fixtures (notably `extensions/confirm-gate.ts`).
- **CLI tree:** `gibson serve [--port N] [--dev]` (the server; `--dev` reverse-proxies
  non-`/api` paths to the Vite dev server at `http://localhost:5173` — single origin, no
  CORS); `gibson run <type> <message> [--checkout <name>]` (M1 one-shot, prints streamed
  text, defaults to the launch checkout). Cobra with `SilenceErrors/SilenceUsage`,
  version via ldflags — all per grove-cli.
- **Libraries (pinned):** `spf13/cobra` (CLI), `BurntSushi/toml` directly (single file, no
  layering, so go-config-loader is not needed), `net/http` stdlib with Go 1.22+
  `ServeMux` method+pattern routing (no router dependency), hand-rolled SSE via
  `http.Flusher`, `log/slog` for server logs, `stretchr/testify` for tests. No frameworks.

## 2. Wire conventions

- All JSON on the wire (REST + SSE) uses **camelCase** field names (matches pi's protocol).
- Timestamps: RFC 3339 UTC strings.
- Error envelope, every non-2xx response:
  `{"error": {"code": "<snake_case>", "message": "<human readable>"}}`.
  Codes: `invalid_request` (400), `not_found` (404), `conflict` (409),
  `dialog_already_answered` (409), `pi_error` (502), `internal` (500).
- Pi-originated objects (entries, events, dialog requests, stats) are forwarded
  **verbatim** as `json.RawMessage` — gibson never re-models pi's payloads (churn guard,
  SPEC §10.5).

## 3. REST route table (SPEC §7.1)

All under `/api`, JSON bodies. `{id}` is the session id; `{dialogId}` is pi's
`extension_ui_request` uuid.

| Method + path | Request body | Response (200/201) |
|---|---|---|
| `GET /api/health` | — | `{"ok":true,"version":"..."}` |
| `GET /api/config/session-types` | — | `{"sessionTypes":[{"name","description","model","thinking"}]}` (model/thinking `null` when unset) |
| `GET /api/checkouts` | — | `{"checkouts":[{"name","path","branch","isPrimary"}]}` — `name` = directory basename, unique key |
| `GET /api/sessions` | — | `{"sessions":[SessionSummary]}` |
| `POST /api/sessions` | `{"type","checkout","name"?,"message"}` | 201 `{"session":SessionSummary}` |
| `GET /api/sessions/{id}/history` | — | `{"session":SessionSummary,"entries":[<pi entry verbatim>],"leafId":string\|null,"cursor":string\|null,"pendingDialog":<request verbatim>\|null,"uiState":{"statuses":{key:text},"widgets":{key:[lines]},"title":string\|null}}` |
| `POST /api/sessions/{id}/message` | `{"message","behavior"?:"steer"\|"followUp"}` | `{"session":SessionSummary}` |
| `POST /api/sessions/{id}/abort` | `{}` | `{"session":SessionSummary}` |
| `POST /api/sessions/{id}/dialogs/{dialogId}` | `{"value"?,"confirmed"?,"cancelled"?}` | `{"resolved":true}`; second answer → 409 `dialog_already_answered` |
| `POST /api/sessions/{id}/close` | `{}` | `{"session":SessionSummary}` |
| `GET /api/sessions/{id}/stats` | — | `{"stats":<pi get_session_stats data verbatim>}` |
| `GET /api/sessions/{id}/events?since=<entryId>` | — | SSE stream (§4) |

`SessionSummary` = `{"id","name","type","checkout","status","createdAt","lastActivityAt"}`.

**Wire status enum** (SPEC §7.1 literally): `"idle" | "streaming" | "blocked-on-dialog" |
"stopped" | "closed"`. Derivation from registry status (§5): `live` + pending dialog →
`blocked-on-dialog`; `live` + streaming flag → `streaming`; `live` otherwise → `idle`;
`stopped`/`closed` pass through. A registry `live` entry with **no live in-memory
process** derives as wire `stopped` wherever it is read (list, history, resume) — the
derivation never reports `idle` for a process gibson does not hold. Streaming flag: set on pi `agent_start`, cleared on
`agent_settled` (fall back to `agent_end` if settled never arrives). `lastActivityAt`
updates on every appended entry, accepted prompt, and dialog answer.

`POST message` to a `stopped`/`closed` session respawns pi first (SPEC §5.3.3), then sends.
`GET /history` for a **live** session calls `get_entries` on the process; for
`stopped`/`closed` it parses the session JSONL directly (file order = append order).
`cursor` = id of the last entry in append order (`null` if empty) — this, not `leafId`, is
what feeds `?since=`. Everything not `/api/*` serves the embedded SPA (history-API
fallback to `index.html`).

## 4. SSE wire contract (SPEC §7.2) — pinned hard

### 4.1 Envelope

Every event is a default SSE message (no `event:` line). `data:` is one JSON object:

```
{"type": "<gibson event type>", "data": { ... }}
```

The SSE `id:` line is set **only** on `entry` events, and carries the **pi entry id**.
Per the SSE spec, the last-seen id persists across id-less events, so `Last-Event-ID`
always names the last durable entry regardless of how many ephemeral events followed it.
Heartbeat: comment line `:hb` every 15 seconds of idle (satisfies §7.2.3's ≤30s).

### 4.2 Event types

| type | data | durable? |
|---|---|---|
| `entry` | pi session entry verbatim, produced by gibson's entry-feed sync (SPEC §6.3), not by any single pi event | yes — SSE `id:` = entry id |
| `pi` | any other pi event verbatim (`message_start/update/end`, `tool_execution_*`, `agent_*`, `queue_update`, retries, …); client switches on `data.type` | no |
| `dialog` | blocking `extension_ui_request` verbatim (`select`/`confirm`/`input`/`editor`) | no (recovered via history + connect-time re-emit) |
| `dialog_resolved` | `{"dialogId","resolution":{"value"?,"confirmed"?,"cancelled"?}}` | no |
| `ui` | fire-and-forget `extension_ui_request` verbatim (`notify`, `setStatus`, `setWidget`, `setTitle`, `set_editor_text`) | no |
| `status` | `{"status":<wire status>,"lastActivityAt"}` | no |
| `reset` | `{"reason":"invalid_cursor"}` — then server closes the stream | — |

**Entry-feed sync (SPEC §6.3), pinned:** pi emits no per-append event for ordinary
entries (`entry_appended` fires only for extension-appended custom entries), so the
session process owns a per-session sync cursor (last synced entry id, distinct from any
client's cursor). On each persistence-signal event — `message_end`, `entry_appended`,
`session_info_changed`, `compaction_end`, `agent_end`/`agent_settled` — it calls
`get_entries {since: syncCursor}`, broadcasts each newly returned entry through the
Broker as an `entry` event in append order, and advances the cursor. `entry_appended.entry`
is never forwarded directly — it is only a trigger — and the trigger events themselves
still ride the `pi` lane verbatim (so clients see `message_end` both as a `pi` event and,
immediately after, the finalized `entry`).

### 4.3 Cursor, replay, gaplessness

Durable truth is the pi entry log, surfaced as `entry` events by the sync in §4.2. Only
`entry` events are replayable; everything else is deliberately ephemeral. Connect
algorithm (server side, per client):

1. Resolve the cursor: `Last-Event-ID` header if present, else `?since=`, else empty
   (empty = replay from the beginning — pure-SSE clients work without `/history`).
2. **Subscribe first**: attach the client to the session Broker with a bounded buffer
   (256 events), but hold delivery.
3. **Fetch**: live session → `get_entries {since: cursor}` via pi; non-live → parse the
   JSONL tail after the cursor. Record the set of fetched entry ids.
4. **Replay**: send each fetched entry as an `entry` event (with `id:`), in append order.
5. **Drain + dedup**: release the buffer; drop any buffered `entry` whose id is in the
   fetched set; deliver everything else. Then go fully live. Entries can never be missed
   (subscribe-before-fetch) or duplicated (fetched-id set).
6. **Prime**: emit a `status` event with the current status, and, if a dialog is pending,
   a `dialog` event — so a pure SSE reconnect recovers actionable state without REST.
7. Invalid cursor (pi returns `success:false`): send `reset`, close. Client refetches
   `/history` and reconnects fresh.

**Deliberately lossy, by design:** deltas, tool updates, queue/status/ui events missed
while disconnected are never replayed. They are superseded by: (a) the finalized entries
replayed in step 4; (b) pi's **cumulative** `partial` snapshot on the next
`message_update` and cumulative `partialResult` on the next `tool_execution_update` —
a mid-stream reconnector repaints complete partial state on the first delta after
attach, with zero server-side delta buffering; (c) `uiState` in `/history`; (d) step 6.
Clients MUST render partials by replacement, never appending across reconnects.

If a client's 256-event buffer overflows, the server closes its stream (§7.2.4); the
client recovers via the same reconnect path. `EventSource` auto-reconnect + this
algorithm is the entire recovery story; there is no other replay mechanism.

## 5. `.gibson/` storage and the registry

Layout, exactly SPEC §4.1:

```
<checkout>/.gibson/
├── sessions/                     # passed to pi as --session-dir
├── state.json                    # per-checkout registry (below)
└── logs/<session-id>.stderr.log
```

**`state.json` schema** (checkout is implicit — the registry lives in it; no checkout
field, so nothing goes stale on rename):

```json
{
  "version": 1,
  "sessions": {
    "<session-id>": {
      "id": "s-20260726-k3v9qx",
      "name": "Refactor auth",
      "type": "review",
      "status": "live",
      "createdAt": "2026-07-26T14:00:00Z",
      "lastActivityAt": "2026-07-26T14:05:00Z",
      "pid": 12345
    }
  }
}
```

- **Registry status enum:** `"live" | "stopped" | "closed"` (SPEC §4.1.1). Wire statuses
  `idle`/`streaming`/`blocked-on-dialog` are derived in-memory, never persisted.
- `pid` is diagnostic only — recorded at spawn, zeroed on exit, **never** used to
  re-attach (SPEC §5.3.2). Startup sweep: any `live` entry → `stopped`, pid zeroed.
- **Session id format:** `s-<YYYYMMDD>-<6 chars of [a-z0-9] via crypto/rand>`
  (e.g. `s-20260726-k3v9qx`). Matches pi's id regex; date prefix makes ids scannable.
  Regenerate on collision against registry + `sessions/` contents. Gibson session id
  **is** the pi session id — one id, passed via `--session-id`.
- Writes: in-process mutex + write-temp-then-rename (atomic). One server per workspace
  makes cross-process locking unnecessary.
- Rebuild (SPEC §4.1.2): if `state.json` is missing, scan `sessions/*.jsonl`, take the id
  from each file's session header line and the name from the latest `session_info` entry;
  status `stopped`, type `""`, timestamps from header/mtime.
- Display name flows: `POST /api/sessions` name → `set_session_name` command after spawn
  (not `--name` argv, keeping argv assembly uniform); pi's `session_info_changed` event
  → mirror into registry `name`.

## 6. pi process contract (SPEC §5–6)

- **Argv assembly**, exact order:
  `<pi_bin> --mode rpc --session-id <id> --session-dir <checkout>/.gibson/sessions`
  + `--model <model>` (if set) + `--thinking <thinking>` (if set) + `extra_args...`
  verbatim, last, unparsed. cwd = the target checkout. Environment inherited.
  `pi_bin` from config, else `exec.LookPath("pi")`.
- **stderr** → appended to `<checkout>/.gibson/logs/<session-id>.stderr.log`
  (dirs 0755, file 0644, created at spawn).
- **Framing:** read stdout with `bufio.Reader.ReadBytes('\n')` (no Scanner — its 64KB
  default line cap breaks large entries); strip one trailing `\r`; split on `\n` ONLY
  (SPEC §6.1.2). Write commands as one JSON object + `\n`. All writes through a single
  goroutine (serialized); reads on a dedicated pump goroutine that only demuxes —
  never blocks on downstream consumers directly (Broker buffers apply backpressure
  per-client, §4.3).
- **Command correlation:** gibson sets `id` = `c-<n>` (per-process atomic counter) on
  every command; a map `id → chan response` resolves replies; 30s default timeout →
  `pi_error`. `extension_ui_response` writes use pi's request uuid and expect no reply.
- **Spawn/readiness sequence (create or resume):** spawn → `get_state` as readiness probe
  → `set_session_name` (if name given at create) → `prompt`. REST create returns 201 only
  after the prompt is accepted.
- **Shutdown sequence** (close, and server shutdown for each live session): SIGTERM →
  wait up to 5s → SIGKILL; reap; close pipes; registry → `closed` (user close) or
  `stopped` (server shutdown). Unexpected pi exit: registry → `stopped`, emit `status`
  event, log tail of stderr at error level.
- **Version pin:** at startup run `pi --version`; require prefix `0.82.` (patch drift
  allowed); on mismatch exit with an error naming found + supported versions (SPEC §5.4).
  Constant lives in `internal/pisession/version.go`.

## 7. Shared Go seam interfaces

Later milestones program against these names; earlier milestones must export them so.

- `pisession.Session` — one live process: `Prompt(msg, behavior)`, `Abort()`,
  `GetState()`, `GetEntries(since)`, `GetSessionStats()`, `SetSessionName(name)`,
  `RespondUI(id, resolution)`, `Events() <-chan pisession.Event`, `Close(ctx)`.
  `pisession.Event` = `{Type string; Raw json.RawMessage}` (Type = pi's `type`).
- `session.Manager` — `Create(type, checkout, name, msg)`, `Get(id)`, `List()`,
  `Send(id, msg, behavior)`, `Abort(id)`, `AnswerDialog(id, dialogID, res)`,
  `CloseSession(id)`, `History(id, ...)`, `Subscribe(id) (*session.Subscription)`.
- `session.StreamEvent` = `{Type string; EntryID string; Data json.RawMessage}` — the
  exact SSE envelope precursor (§4.1); `httpapi` serializes it without transformation.

## 8. Frontend conventions (SPEC §8)

- `web/`: Vite + React 18 + TypeScript, `src/` layout:
  - `src/api/types.ts` — hand-written TS mirrors of the wire types in §3/§4 (single
    source: this file's tables).
  - `src/api/client.ts` — typed fetch wrappers, one function per route
    (`listSessions`, `createSession`, `sendMessage`, `answerDialog`, …); throws
    `ApiError{code,message}` from the error envelope.
  - `src/api/stream.ts` — `SessionStream`: EventSource wrapper owning reconnect,
    `?since=` bootstrap from the history cursor, and `reset` handling (refetch history,
    reopen).
  - `src/state/sessionStore.ts` — **the** state convention: a pure reducer
    `sessionReducer(state, StreamEvent)` folds both the history snapshot (entries
    replayed through it as synthetic `entry` events) and live SSE events. Snapshot and
    tail go through the identical code path — replay-equals-render is what makes §9.3.b
    provable. Deltas mutate an `inFlight` region keyed by `contentIndex`/`toolCallId`,
    always by replacement (cumulative partials); the finalized `entry` supersedes and
    clears it. React binding via `useReducer` + context; no Redux/Zustand.
- Pinned libraries: `react-router-dom`, `react-markdown`. Nothing else without need.
- Major surfaces (component names): `SessionListPage`, `LaunchFlow`, `SessionPage`;
  within chat: `MessageList`, `MessageCard`, `StreamingText`, `ThinkingBlock`,
  `ToolCallCard`, `CustomMessageCard`, `Composer`, `ContextMeter`, `DialogModal`,
  `ToastHost`, `StatusStrip`.
- Dev: `vite.config.ts` proxies nothing; `gibson serve --dev` proxies to Vite (§1), so
  the browser always talks to gibson. `web/dist` is committed-ignored; CI/`make build`
  runs `npm run build` before `go build`.

## 9. Test strategy conventions

- **No automated Go test may require a live LLM or network.** Enforced by construction:
  unit/integration tests use **fakepi**.
- **fakepi** (`internal/fakepi/`, a `package main` Go program): speaks enough RPC —
  reads LF-JSONL commands; answers `get_state`, `get_entries`, `get_session_stats`,
  `set_session_name`, `prompt`/`steer`/`follow_up`, `abort` with well-formed responses;
  on `prompt` emits a scripted event sequence (agent_start → message deltas →
  message_end → agent_end/settled); **writes a real v3 session JSONL** honoring
  `--session-id`/`--session-dir`, and answers `get_entries` (including `since`) from
  that JSONL — load-bearing, since the server's entry feed is built on get_entries
  sync (SPEC §6.3) — so storage, history-from-file, and entry-sync paths are exercised
  for real. Scenario selected via env `FAKEPI_SCENARIO` (e.g. `basic`, `slow_stream`,
  `dialog_confirm`, `huge_entry`, `crash_mid_stream`); scenario data lives in
  `internal/fakepi/scenarios/`. `pitest.BuildFakePi(t)` compiles it once per test run
  (`go build ./internal/fakepi` into `t.TempDir()`-shared cache) and returns the path
  to use as `pi_bin`.
- **Web unit tests:** the runner is **Vitest**. Test files are `*.test.ts` beside the
  sources they cover (under `web/src/`), run via `npm test` in `web/`.
- **Real-pi integration tests**: same test files, gated — `pitest.RequireRealPi(t)`
  skips unless env `GIBSON_TEST_REAL_PI=1`. Never run in default `go test ./...`.
- **Milestone acceptance proofs**: real pi + browser automation (agent-driven), per
  MILESTONES.md. Scratch workspaces built with `internal/testws` (`testws.New(t)`:
  temp grove-style root, git repo checkout, committed `gibson.toml` + `.gitignore`).
  The dialog-exercising extension fixture is `test/fixtures/extensions/confirm-gate.ts`
  (calls `ctx.ui.confirm()` before tool execution) — referenced from test
  `gibson.toml` via `extra_args = ["-e", "<abs path>"]`; used by M5 and M7 (SPEC §9.5).
- Test style: testify `require`/`assert`, table tests, `_test.go` next to code
  (grove-cli idiom). No mocking frameworks; fakes and real subprocesses only.

## 10. Required PLAN_M<n>.md template

Every plan MUST contain exactly these sections, in order:

1. **Goal & capability** — the "you can now …" sentence from MILESTONES.md.
2. **Preconditions** — what prior milestones must have delivered (name the §7 interfaces
   and routes consumed).
3. **Deliverables** — files/packages/routes/components this milestone creates or extends.
4. **Design & rationale** — decisions local to this milestone; MUST cite SPEC sections
   and this file's section numbers instead of restating them.
5. **Implementation steps** — ordered, with absolute-repo-relative file paths.
6. **Interfaces exposed to later milestones** — exact exported names/signatures/routes
   added, matching §3/§4/§7 here.
7. **Testing** — unit + fakepi integration tests to write; real-pi gated tests if any.
8. **Agent-verified proof workflow** — the executable end-to-end proof from
   MILESTONES.md, as concrete numbered steps an agent can run (commands, URLs,
   expected observations).
9. **Success criteria checklist** — checkboxes, each mapping to a SPEC/MILESTONES claim.
10. **Explicitly out of scope** — deferred items later milestones own.

Plans MUST NOT invent new wire fields, event types, statuses, file names, or package
names: if a needed seam is missing here, the plan must call it out as an open question
rather than deciding it unilaterally.
