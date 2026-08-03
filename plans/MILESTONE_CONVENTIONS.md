# MILESTONE_CONVENTIONS

Binding conventions for the remaining milestone plans (`MILESTONE_1.md` …
`MILESTONE_7.md`), including the seams established by the current implementation.
[PROCESS.md](PROCESS.md) governs plan authority, activation, execution, consolidation, and
retirement. SPEC.md is normative for behavior; this file pins every cross-milestone seam
SPEC.md left open so independently written plans compose. Where this file and SPEC.md
conflict, SPEC.md wins.

An active milestone file owns its outcomes and acceptance boundary; its implementation
notes are suggestive. Root `PLAN.md` owns the active implementation, chunks, and
verification. Inactive milestone files are provisional until their plan-gate review.
All plans MUST use the names, paths, and shapes here verbatim — no local variants.

## 1. Module, layout, CLI, libraries

- **Module path:** `github.com/jmcampanini/gibson`. Go 1.26. `main.go` is the thin process
  boundary at the repository root. The canonical binary output is `build/gibson`.
- **Directory / package layout** (one-line responsibility each):
  - `cmd/` — thin Cobra adapters, one file per command + `_test.go` sibling: `root.go`,
    `serve.go`, `run.go`. Commands parse arguments and flags, then invoke `internal/app`.
  - `internal/app/` — application workflows, dependency composition, startup ordering,
    server/process lifetime, and shutdown. Workflow loggers are injected by the process
    boundary; Cobra does not own lifecycle orchestration.
  - `internal/config/` — parse, default, and validate `gibson.toml` (SPEC §3).
    `Load(checkoutRoot string) (Config, error)` is the validation entry point and returns
    only a fully validated value; unknown TOML keys are accepted silently.
  - `internal/workspace/` — workspace-root derivation and checkout enumeration via
    `git worktree list --porcelain` (SPEC §2).
  - `internal/pisession/` — spawn/supervise one `pi --mode rpc` process: argv assembly,
    JSONL framing, command/response correlation, typed client, event fan-in (SPEC §5–6).
  - `internal/store/` — `.gibson/` layout, `state.json` registry, session id generation,
    and registry rebuild (SPEC §4).
  - `internal/session/` — the Manager: session lifecycle (create/resume/close), status
    machine, per-session event Broker (fan-out, ring of subscribers), dialog registry.
  - `internal/httpapi/` — REST handlers + SSE endpoint + dev proxy; owns wire types.
  - `internal/fakepi/` — `package main`: the fake pi executable for tests (§10).
  - `internal/pitest/` — test helpers: build fakepi, drive scenarios.
  - `internal/testws/` — test helper: scratch grove-style workspace + git repo + gibson.toml.
  - `web/` — the Vite React SPA (§9). The tracked sibling `web/dist.bootstrap` keeps the
    embed pattern valid in fresh clones; `web/embed.go` uses `//go:embed dist*`, while
    production serving selects only the generated `dist/` subtree.
  - `test/fixtures/` — shared non-Go fixtures (notably `extensions/confirm-gate.ts`).
- **CLI tree:** `gibson serve [--port N] [--dev]` (the server; `--dev` reverse-proxies
  non-`/api` paths to the Vite dev server at `http://localhost:5173` — single origin, no
  CORS); `gibson run <type> <message> [--checkout <name>]` (M1 one-shot, prints streamed
  text, defaults to the launch checkout). Cobra uses `SilenceErrors`/`SilenceUsage` and
  ldflags version injection. Every command remains an adapter to an `internal/app`
  workflow.
- **Libraries (pinned):** `spf13/cobra` (CLI), `BurntSushi/toml` directly (single file, no
  layering, so go-config-loader is not needed), `net/http` stdlib with Go 1.22+
  `ServeMux` method+pattern routing (no router dependency), hand-rolled SSE via
  `http.Flusher`, an injected Charm Log v2 (`charm.land/log/v2`) logger for operational
  output, and `stretchr/testify` for tests. No frameworks.

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

The existing `httpapi.New` constructor returns `(http.Handler, error)`.
`httpapi.Options` carries `Version` plus exactly one frontend input: a production
`StaticFS fs.FS` or a development `DevProxy *url.URL`. `New` rejects zero or two frontend
inputs and validates production asset readiness before returning. Later routes add only
the concrete dependencies they consume; configuration and workspace orchestration remain
in `internal/app`.

All routes are under `/api` with JSON bodies. `{id}` is the session id; `{dialogId}` is
pi's `extension_ui_request` uuid.

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
7. Invalid cursor (pi returns the exact `Entry not found: <since>` command failure): send
   `reset`, close. Other command failures remain pi errors. The client refetches
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

Every checkout MUST contain a committed `.gitignore` entry for `.gibson/`. Every proof
that creates `.gibson/` data MUST run `git status --porcelain` in the checkout and require
empty output.

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
- Writes: process-local serialization plus an advisory cross-process lock on the stable
  per-checkout `.gibson/` directory itself; no lock file is created. Every read-modify-write mutation reloads the latest `state.json` while holding the lock and
  completes its write-temp-then-rename replacement before unlocking. Readers therefore
  see either the previous complete file or the next complete file, while concurrent
  `gibson run` invocations—or a run overlapping the workspace server—cannot lose one
  another's updates. One server still governs each workspace, but it is not the only
  process that may mutate a checkout registry.
- Fresh-session allocation holds that same lock from registry/header collision scanning
  through pi readiness and the first live-record replacement. Startup is serialized only
  within one checkout and creates no reservation artifact. A failed live replacement stops
  the spawned process through its creation rollback before unlocking, then reconciles any
  possibly committed record to stopped. Later ambiguous cleanup may stop only a live record
  whose diagnostic pid matches the process being cleaned up.
- Lifecycle transitions allow idempotent same-state writes plus `live→stopped|closed`,
  `stopped→live|closed`, and `closed→live`; `closed→stopped` is invalid. Live requires a
  positive diagnostic pid, and a same-state live write cannot replace a different live
  pid; stopped and closed force pid zero. Status changes preserve all
  other metadata.
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
  every command; a map `id → chan response` resolves replies. Every outbound write has a
  30-second bound and a write timeout is fatal to the transport. Ordinary commands keep
  one 30-second budget across enqueue, write, and response → `pi_error`. The `prompt`
  command family (including steer and follow-up sends) keeps the write bound but waits
  for its response with **no transport deadline** — until caller cancellation, process
  exit, or transport closure — because pre-acceptance extension dialogs may legitimately
  block acceptance indefinitely (SPEC §§6.4.3, 10.3). The typed session layer owns this
  wait policy; the transport never branches on command names.
  Terminal output closure fails pending commands and closes input; a response
  already demultiplexed for its command wins if closure races local write completion.
  `extension_ui_response` writes use pi's request uuid and expect no reply.
- **Spawn/readiness sequence (create or resume):** spawn → `get_state` as readiness probe
  → `set_session_name` (if name given at create) → `prompt`. REST create returns 201 only
  after the prompt is accepted.
- **Process ownership and shutdown:** pi runs in a dedicated process group so terminal
  Ctrl+C reaches Gibson without preempting RPC abort. Close signals that group with
  SIGTERM. Gibson tracks descendant ownership by PID plus a precise OS birth token,
  validates each new candidate's live ancestry chain, stops discovery when the original
  pi identity disappears, refreshes mutable PGID routing across detachment, and
  revalidates before every signal. Linux uses pidfds for
  individual signals when available. It signals a descendant group only with a current
  owned witness. After 5s, forced escalation freezes pi, takes a final descendant
  snapshot, and SIGKILLs owned descendants plus pi's group. The waiter reaps pi
  independently of stdout EOF, drains final records for
  up to 500ms, then closes the owned pipe so inherited descriptors cannot hang
  completion. Registry → `closed`
  (user close) or `stopped` (server shutdown). Unexpected pi exit: registry → `stopped`,
  emit `status`, and log the stderr tail at error level.
- **Version compatibility:** at startup run `pi --version`. The minimum is 0.82.0 and
  the 0.82.x line is verified. Versions below the minimum fail with an error naming the
  found and minimum versions; later minor or major versions are accepted and produce an unverified-line
  warning through the injected logger (SPEC §5.4). Constants live in
  `internal/pisession/version.go`.

## 7. Shared Go seam interfaces

These names are cross-milestone targets. Introduce each seam when its first real consumer
needs it; do not add speculative implementations solely for a later plan.

- `pisession.Session` — one live process: `Prompt(msg, behavior)`, `Abort()`,
  `GetState()`, `GetEntries(since)`, `GetSessionStats()`, `SetSessionName(name)`,
  `RespondUI(id, resolution)`, `Events() <-chan pisession.Event`, `Close(ctx)`.
  `pisession.Event` = `{Type string; Raw json.RawMessage}` (Type = pi's `type`).
- `pisession.UIResolution` = `{Value *string; Confirmed *bool; Cancelled *bool}` (json
  tags `value`/`confirmed`/`cancelled`, all `omitempty`) — the shared dialog-answer
  shape for `RespondUI` and §3's dialog route body. All three fields are pointers so
  validation can distinguish an absent field from an explicit `false`/empty value.
- `session.Manager` — `Create(type, checkout, name, msg)`, `Get(id)`, `List()`,
  `Send(id, msg, behavior)`, `Abort(id)`, `AnswerDialog(id, dialogID, res)`,
  `CloseSession(id)`, `History(id, ...)`, `Subscribe(id) (*session.Subscription)`.
- `session.StreamEvent` = `{Type string; EntryID string; Data json.RawMessage}` — the
  exact SSE envelope precursor (§4.1); `httpapi` serializes it without transformation.

## 8. Frontend conventions (SPEC §8)

- `web/`: Vite + React 19 + TypeScript, `src/` layout:
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
  - **Streaming state seam, pinned:** the reducer derives and owns a client-side
    `isStreaming` boolean from `pi` events — set by `agent_start`, cleared by
    `agent_settled` (`agent_end` as fallback). Streaming-dependent UI (the composer's
    Steer/Queue affordances, dialog-adjacent behavior) keys off this flag, never off the
    wire status enum: `blocked-on-dialog` masks `streaming`, and a session can be both
    mid-stream and blocked at once. Wire status remains authoritative only for coarse
    surfaces such as list badges.
- Pinned libraries: `react-router-dom`, `react-markdown`. Nothing else without need.
- Major surfaces (component names): `SessionListPage`, `LaunchFlow`, `SessionPage`;
  within chat: `MessageList`, `MessageCard`, `StreamingText`, `ThinkingBlock`,
  `ToolCallCard`, `CustomMessageCard`, `Composer`, `ContextMeter`, `DialogModal`,
  `ToastHost`, `StatusStrip`.
- Dev: `vite.config.ts` proxies nothing; `gibson serve --dev` proxies to Vite (§1), so
  the browser always talks to gibson. `web/dist` is committed-ignored; CI/`make build`
  runs `npm run build` before `go build`.

## 9. Test strategy conventions

Verification is contract-focused. Each behavior has one primary owner at the layer
closest to the likely defect; higher layers prove composition only when they add
confidence unavailable below, rather than duplicating lower-layer assertion matrices:

- `internal/config` owns schema, defaults, validation, silent unknown-key handling, and
  opaque `extra_args` behavior.
- `internal/workspace` owns checkout discovery and workspace derivation.
- `internal/pisession` owns resolution, version compatibility, RPC framing, and process
  behavior.
- `internal/store` owns persistent layout and registry behavior; repository proof owns
  clean Git status.
- `internal/session` owns lifecycle, status, replay, fan-out, and dialog semantics.
- `internal/httpapi` owns HTTP/SSE wire contracts, API boundaries, static serving, and
  development proxy routing.
- `internal/app` owns representative workflow composition, startup ordering, warnings,
  listener/process lifetime, and shutdown.
- `cmd` owns Cobra shape, flags, and process-facing presentation not already proved below.
- Browser automation owns user-visible web composition; frontend unit tests own pure
  state and rendering contracts when that is the cheapest faithful layer.

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
  temp grove-style root, git repo checkout, committed `gibson.toml` + `.gitignore`, with
  `.gibson/` ignored). Every proof that writes `.gibson/` artifacts requires an empty
  `git status --porcelain`. The dialog-exercising extension fixture is `test/fixtures/extensions/confirm-gate.ts`
  (calls `ctx.ui.confirm()` before tool execution) — referenced from test
  `gibson.toml` via `extra_args = ["-e", "<abs path>"]`; used by M5 and M7 (SPEC §9.5).
- Test style: testify `require`/`assert`, table tests, `_test.go` next to code
  (grove-cli idiom). No mocking frameworks; fakes and real subprocesses only.

## 10. Required remaining-milestone plan template

Every numbered milestone plan MUST begin with one lifecycle status after its title:
`active` for the current milestone or `provisional` for an inactive forecast. Activation
requires the plan-gate review in PROCESS.md.

Every remaining milestone plan MUST contain exactly these sections, in order:

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
