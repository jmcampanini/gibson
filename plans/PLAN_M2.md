# PLAN_M2 — Curl-drivable HTTP API

Implements MILESTONES.md M2 exactly. Governing documents: SPEC.md (normative behavior),
PLAN_CONVENTIONS.md (binding names/shapes — cited below as CONV §n), pi's RPC reference at
`~/.local/lib/node_modules/@earendil-works/pi-coding-agent/docs/rpc.md` (cited as rpc.md),
and `session-format.md` in the same directory for the JSONL file layout. Read all four
before implementing.

## 1. Goal & capability

**You can now:** drive full sessions with `curl` — create, stream, prompt, abort — with
multiple concurrent clients seeing identical streams.

Concretely: `gibson serve` exposes the full REST surface of SPEC §7.1 (minus the dialog
answer route, which M5 owns) plus the per-session SSE stream of SPEC §7.2 with entry-id
cursors, gapless `Last-Event-ID` reconnect, heartbeats, bounded per-client buffers, and
equal-peer fan-out. The pi processes are server-owned and kept alive until closed
(SPEC §5.2.1). No SPA changes — the proof is pure curl/scripting.

## 2. Preconditions

From **M0** (built per PLAN_M0.md):

- Go module `github.com/jmcampanini/gibson`, `main.go` → `cmd.Execute()` (CONV §1).
- `cmd/serve.go`: `gibson serve [--port N] [--dev]` starts an `http.Server` on
  `bind:port` from config, serves the embedded SPA for non-`/api` paths (history-API
  fallback), `--dev` reverse-proxies non-`/api` to Vite :5173 (CONV §1, §3). M2 extends
  this command; it does not replace it.
- `internal/config`: parsed `gibson.toml` — server port/bind, `pi_bin`, and the
  `[sessions.<name>]` session types with `description` (required), optional `model`,
  `thinking`, `extra_args` (SPEC §3). Assumed shape (M0 §4.2 — `pi_bin` lives in the
  `[server]` table):
  `config.Config{Server config.Server{Port int; Bind string; PiBin string}; Sessions map[string]config.SessionType}`.
- `internal/workspace`: workspace-root derivation from the launch checkout (SPEC §2.1).
  Assumed: a `workspace.Workspace` value carrying the launch-checkout path and workspace
  root. M2 **extends** this package with checkout enumeration (SPEC §2.2.1 — the coverage
  map assigns worktree enumeration to M2).
- Startup checks: gitignore warning, pi presence/version check (`0.82.` prefix, CONV §6).
- `GET /api/health` may already exist from M0's skeleton; if not, M2 adds it per CONV §3.

From **M1** (built per PLAN_M1.md; the CONV §7 seam is binding, helper names are assumed
and may be adapted at the Manager boundary if M1 named them differently):

- `internal/pisession`: `pisession.Session` with the pinned methods `Prompt(msg, behavior)`,
  `Abort()`, `GetState()`, `GetEntries(since)`, `GetSessionStats()`, `SetSessionName(name)`,
  `RespondUI(id, resolution)`, `Events() <-chan pisession.Event`, `Close(ctx)`
  (CONV §7); `pisession.Event = {Type string; Raw json.RawMessage}`. Spawn per CONV §6:
  exact argv order, cwd = target checkout, stderr → `.gibson/logs/<id>.stderr.log`,
  `ReadBytes('\n')` framing, `c-<n>` command correlation with 30s timeout,
  SIGTERM→5s→SIGKILL shutdown. M2 additionally relies on:
  - `Events()` closing after process exit (so the pump can detect death).
  - The ability to distinguish a **pi command failure** (`success:false` response — e.g.
    `get_entries` with an unknown `since`, rpc.md "get_entries") from a transport/timeout
    error. Assumed as a typed error (e.g. `pisession.CommandError`); if M1 shaped this
    differently, wrap at the Manager boundary. This distinction drives the SSE `reset`
    path (§4.5).
  - Raw entries from `GetEntries` (verbatim `json.RawMessage` per CONV §2); M2 extracts
    each entry's `id` itself with a minimal `{"id":...}` unmarshal, so it does not depend
    on M1 having parsed entries.
- `internal/store`: `.gibson/` layout creation, `state.json` registry read-modify-write
  (mutex + write-temp-then-rename, CONV §5), session id generation
  (`s-<YYYYMMDD>-<6 [a-z0-9]>` with collision regeneration), registry record shape per
  CONV §5. Assumed surface: `store.Open(checkoutPath)` returning a handle with
  `List() / Get(id) / Put(record)` and layout/log-path helpers.
- `internal/fakepi` + `internal/pitest`: fakepi binary honoring `--session-id` /
  `--session-dir`, writing a real v3 session JSONL, answering `get_state`, `get_entries`
  (including `since` and `success:false` on unknown cursor), `get_session_stats`,
  `set_session_name`, `prompt`/`steer`/`follow_up`, `abort`; scenario via
  `FAKEPI_SCENARIO`; `pitest.BuildFakePi(t)`; `pitest.RequireRealPi(t)` (CONV §9).
- `internal/testws`: `testws.New(t)` scratch grove-style workspace (CONV §9).

If any assumed non-pinned helper is absent, add it in this milestone rather than
redesigning M1's package.

## 3. Deliverables

New or extended files (paths repo-root-relative):

| Path | Contents |
|---|---|
| `internal/workspace/checkouts.go` (+`_test.go`) | `Checkout` type; enumeration via `git worktree list --porcelain`; porcelain parser split from exec for testability |
| `internal/session/manager.go` (+`_test.go`) | `Manager`: create/send/abort/close/list/history/stats/subscribe/shutdown; per-session handles; registry updates; pump goroutine |
| `internal/session/broker.go` (+`_test.go`) | Per-session `Broker`: subscriber set, 256-event bounded buffers, non-blocking publish, overflow kick |
| `internal/session/status.go` (+`_test.go`) | Registry-status + streaming-flag state; wire-status derivation (CONV §3) |
| `internal/session/events.go` (+`_test.go`) | pi event → `StreamEvent` classification (CONV §4.2); registry side effects (lastActivityAt, name mirror) |
| `internal/session/history.go` (+`_test.go`) | `HistorySnapshot`; live path via `get_entries`; non-live path parsing the session JSONL (CONV §3); `ErrInvalidCursor` |
| `internal/httpapi/server.go` (+`_test.go`) | `New(...)` returning `http.Handler`: ServeMux route table, `/api/` JSON-404 catch-all, SPA fallback mount, slog request logging, panic→500 envelope |
| `internal/httpapi/wire.go` | Wire types: error envelope, request bodies, responses per CONV §3 |
| `internal/httpapi/handlers.go` (+`_test.go`) | REST handlers for every CONV §3 route except dialogs |
| `internal/httpapi/sse.go` (+`_test.go`) | SSE handler: connect algorithm (CONV §4.3), heartbeats, per-write deadlines |
| `cmd/serve.go` (extend, +`_test.go`) | Wire config→workspace→Manager→httpapi; signal handling; ordered graceful shutdown |
| `internal/fakepi/scenarios/slow_stream.*` | Scenario: multi-second streamed turn (deltas at ~150ms) for mid-turn reconnect/fan-out tests (add if M1 didn't) |
| `internal/fakepi/scenarios/crash_mid_stream.*` | Scenario: begins streaming then exits non-zero without settling (add if M1 didn't) |

Routes delivered (CONV §3 verbatim; all under `/api`, camelCase JSON, pinned error
envelope): `GET /api/health`, `GET /api/config/session-types`, `GET /api/checkouts`,
`GET /api/sessions`, `POST /api/sessions`, `GET /api/sessions/{id}/history`,
`POST /api/sessions/{id}/message`, `POST /api/sessions/{id}/abort`,
`POST /api/sessions/{id}/close`, `GET /api/sessions/{id}/stats`,
`GET /api/sessions/{id}/events?since=<entryId>` (SSE).
**Not delivered:** `POST /api/sessions/{id}/dialogs/{dialogId}` — M5 registers it; in M2
it 404s via the `/api/` catch-all.

## 4. Design & rationale

### 4.1 Manager and per-session handles

`session.Manager` is the single owner of pi processes (SPEC §1.1, §5.1.3). It holds
`map[sessionID]*handle` under an `RWMutex`. A `handle` bundles: the `pisession.Session`
(nil when not live), the checkout path, the `Broker`, the status state (§4.3), and the
cached session-file path. Handles are created on `Create` and retained after close/crash
so late SSE subscribers and `/history` still work; the handle map is the guard that
enforces one-process-per-id (SPEC §5.1.3 — `Create` ids are fresh, and M2 has no respawn
path at all).

**Create flow** (CONV §6 sequence, exact): generate id via store → spawn pi with the
pinned argv in the target checkout → write registry record `{status:"live", pid}` with
the spawned pid (spawn-then-record, matching M1 §4.9) → `get_state` readiness probe
(also caches `sessionFile` from its response for §4.6) → `set_session_name` if a name
was given → `prompt` with the first message → return 201 only after the prompt is
accepted (rpc.md: the `prompt` response means accepted/queued, not completed). On any
failure before prompt acceptance: best-effort kill, remove the registry record if it was
written, return `pi_error` (local decision, §4.10).

**Pump goroutine** (one per live handle): `for ev := range sess.Events()` — classify
(§4.4), update status state, apply registry side effects, publish to the Broker. The pump
never blocks on subscribers (Broker publish is non-blocking, §4.2) — this is the whole
slow-client story (SPEC §7.2.4, §10.2). On channel close (process exit): if the user
requested close → registry `closed`; otherwise → registry `stopped`, log the stderr-log
tail at error level (CONV §6); zero `pid`; publish a `status` event either way.

**Send** maps to a single pi `prompt` command with `streamingBehavior` set iff `behavior`
was provided (rpc.md: required mid-stream, error otherwise). Gibson pre-validates
`behavior ∈ {"", "steer", "followUp"}` (400) but does **not** pre-check streaming state —
pi is the authority; a pi rejection (e.g. streaming without behavior) surfaces as 502
`pi_error`. `lastActivityAt` updates on accepted prompt and on every appended entry
(CONV §3), persisted to the registry (small file, single user — no throttling).

### 4.2 Broker and the slow-client policy

One `Broker` per session. Each `Subscribe()` allocates a `chan StreamEvent` with capacity
**256** (CONV §4.3). Publish is non-blocking:

```go
func (b *Broker) publish(ev StreamEvent) {
    b.mu.Lock(); defer b.mu.Unlock()
    for sub := range b.subs {
        select {
        case sub.ch <- ev:
        default: // overflow: kick — never stall the pump (SPEC §7.2.4)
            delete(b.subs, sub)
            close(sub.ch)
        }
    }
}
```

A kicked (or normally closed) subscriber's channel closes; the SSE handler observes
`ok == false` on receive and ends the response. The client recovers via reconnect +
cursor replay — the same path as any disconnect. Equal-peer fan-out (SPEC §7.3.1) is
simply: every subscriber gets every event in publish order; every REST action is
unauthenticated and unarbitrated. `Subscription{C <-chan StreamEvent; Close()}` with an
idempotent `Close`. Buffer capacity defaults to 256; tests shrink it through the exported
`WithSubscriberBuffer` Manager option (§6) — the only seam, since httpapi-level tests
construct a real Manager and cannot reach the Broker's unexported field.

### 4.3 Status machine

Per-handle state: registry status (`live|stopped|closed`, persisted) + in-memory
`streaming` flag + `pendingDialog` (always nil in M2; the field exists so the CONV §3
derivation is complete and M5 only fills it). Wire derivation exactly per CONV §3.
Streaming flag: set on pi `agent_start`, cleared on `agent_settled` — the settled event,
not `agent_end`, because rpc.md documents `agent_end` as "may still be followed by retry,
compaction, or queued continuations". Fallback exactly as CONV §3 pins it, for the case
where settled never arrives: on `agent_end` with `willRetry:false`, arm a short grace
timer (~2s); when it fires, clear the flag unless `agent_settled` or a new `agent_start`
arrived first. pi 0.82.1 emits `agent_settled`, so the fallback is a safety net, not the
normal path. Process exit also clears the flag — an additional belt on top of the pinned
fallback. Every wire-status transition publishes a `status` event
(`{"status", "lastActivityAt"}`, CONV §4.2) through the Broker so all clients converge.

### 4.4 Event classification (pi → StreamEvent)

The pump maps each `pisession.Event` to exactly one `session.StreamEvent` (CONV §7) using
the pinned taxonomy (CONV §4.2); payloads stay verbatim `json.RawMessage` (CONV §2,
churn guard SPEC §10.5):

| pi event | StreamEvent | notes |
|---|---|---|
| `entry_appended` | `entry`, `EntryID` = `entry.id`, `Data` = the entry verbatim | the only durable event; also bumps `lastActivityAt` |
| `extension_ui_request` method `select\|confirm\|input\|editor` | `dialog` (request verbatim) | forwarded so the wire contract is stable from day one; the pending-dialog registry, resolution, and answer route are M5 |
| `extension_ui_request` other methods | `ui` (request verbatim) | `notify`/`setStatus`/`setWidget`/`setTitle`/`set_editor_text` (rpc.md fire-and-forget) |
| `session_info_changed` | `pi` | side effect first: mirror `name` into the registry (CONV §5) |
| everything else (`message_*`, `tool_execution_*`, `agent_*`, `turn_*`, `queue_update`, retries, …) | `pi` (event verbatim) | clients switch on `data.type` |

Gibson-generated events (`status`, `reset`) never come from pi. `dialog_resolved` is M5.

### 4.5 SSE endpoint: connect algorithm and edge cases

`GET /api/sessions/{id}/events` implements CONV §4 exactly. Envelope: default SSE
messages, `data:` = `{"type","data"}`; the `id:` line **only** on `entry` events carrying
the pi entry id — the SSE spec persists the last-seen id across id-less events, so
`Last-Event-ID` always names the last durable entry (CONV §4.1). Heartbeat `:hb` after
15s without a write (satisfies SPEC §7.2.3's ≤30s).

Handler skeleton (the subtle parts are ordering and dedup — this is the design, not a dump):

```go
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    cursor := r.Header.Get("Last-Event-ID")          // header wins (CONV §4.3 step 1)
    if cursor == "" { cursor = r.URL.Query().Get("since") }

    sub, err := s.sessions.Subscribe(id)             // 1. subscribe FIRST
    // (not_found → 404 envelope before headers are committed)
    defer sub.Close()
    // text/event-stream headers, flush; rc := http.NewResponseController(w)

    snap, err := s.sessions.History(r.Context(), id, cursor)   // 2. fetch
    if errors.Is(err, session.ErrInvalidCursor) {
        writeEvent(w, rc, session.StreamEvent{Type: "reset", Data: resetInvalidCursor})
        return                                       // client refetches /history
    }
    seen := make(map[string]bool, len(snap.Entries))
    for _, e := range snap.Entries {                 // 3. replay, in append order
        writeEvent(w, rc, entryEvent(e)); seen[e.ID] = true
    }
    drainBuffered(sub, seen, ...)                    // 4. non-blocking drain + dedup
    writeEvent(w, rc, statusEvent(snap.Session))     // 5. prime status (+dialog in M5)
    liveLoop(r.Context(), sub, seen, ...)            // 6. select: sub.C / 15s tick / ctx.Done
}
```

- `writeEvent` marshals the envelope, writes `id:` iff `Type == "entry"`, flushes via
  `http.ResponseController`, and sets a **per-write deadline** (~10s) with
  `rc.SetWriteDeadline` so a dead TCP peer errors the handler out instead of hanging it.
  (The server-level `WriteTimeout` must be 0 for SSE — see §4.9.)
- `drainBuffered` loops `select { case ev, ok := <-sub.C: … default: return }`, dropping
  `entry` events whose id is in `seen`, delivering everything else. `liveLoop` keeps
  consulting `seen` for entry events — ids never repeat, so the set only ever filters the
  subscribe/fetch overlap window, and checking is O(1).

**Why this is gapless and duplicate-free** (CONV §4.3): an entry appended before the
`History` fetch resolves appears in the fetch result (and possibly also the buffer — the
`seen` set drops the buffered copy); an entry appended after the fetch appears only in
the buffer/live stream. Subscribe-before-fetch closes the gap window; the fetched-id set
closes the duplicate window.

**Edge cases, explicitly:**

1. **First attach, no cursor:** empty cursor = replay from the beginning (pure-SSE clients
   need no `/history`, CONV §4.3 step 1). Empty session → replay nothing, prime status,
   go live.
2. **Reconnect mid-turn:** replay returns entries finalized since the cursor; in-flight
   deltas missed while away are **deliberately lost** — the next `message_update` carries
   pi's cumulative `partial` and the next `tool_execution_update` carries cumulative
   `partialResult` (rpc.md: "accumulated output so far … simply replace"), so one event
   repaints full partial state. Zero server-side delta buffering (CONV §4.3 "deliberately
   lossy").
3. **Reconnect after entries were appended offline:** `get_entries since:<cursor>`
   returns exactly the missed entries; replay covers them; no dupes by the same
   mechanism as first attach.
4. **Cursor == current leaf:** fetch returns zero entries (valid); prime + live.
5. **Invalid cursor:** live session → pi answers `get_entries` with `success:false`
   (rpc.md); non-live → cursor not found in the file. Both map to `ErrInvalidCursor` →
   `reset` event `{"reason":"invalid_cursor"}` → close (CONV §4.2/§4.3 step 7). Any pi
   command failure on this fetch is treated as invalid-cursor (it is the only documented
   failure mode; worst case the client harmlessly refetches). Transport errors instead
   end the stream without `reset`.
6. **Overflow during a long replay:** the subscriber may be kicked while the handler is
   still replaying; the handler sees the closed channel at drain/live and terminates.
   Self-healing: the client reconnects with a further-along cursor, so each retry replays
   less and buffers less.
7. **Non-live session:** fetch uses the file path (§4.6); the Broker still exists on the
   handle, so the stream stays open with heartbeats and would carry future `status`
   events. Nothing else arrives — correct, the process is dead.
8. **Session closed while attached:** subscribers receive `status` = `closed`; the stream
   stays open (client's choice to leave).

### 4.6 History: live vs file (one method, two sources)

`Manager.History(ctx, id, since)` serves both `GET /history` (`since=""`) and the SSE
fetch step — one code path, per CONV §3:

- **Live** handle → `get_entries {since}` on the process (never the file: the process is
  the single writer, SPEC §5.1.3, and pinned by CONV §3). Response gives entries +
  `leafId`; `cursor` = id of the last entry in **append order** (`null` if none) — the
  cursor, not `leafId`, feeds `?since=` (CONV §3).
- **Non-live** (`stopped`/`closed`) → parse the session JSONL directly. File location:
  prefer the `sessionFile` cached from the create-time `get_state` probe; else locate by
  scanning `<checkout>/.gibson/sessions/*.jsonl` for the file whose **header line**
  carries the session id — the same rule as the CONV §5 registry rebuild, robust to pi's
  file-naming scheme (see `session-format.md`). Read with `bufio.Reader.ReadBytes('\n')`
  (same framing rules as RPC); line 1 is the session header, subsequent lines are entries
  in append order (rpc.md `get_entries` returns append order and excludes the header —
  file order matches, CONV §3). Apply `since` by scanning for the cursor id; not found →
  `ErrInvalidCursor`.

The REST response fills the pinned shape (CONV §3): `session`, `entries` (verbatim),
`leafId`, `cursor`, `pendingDialog` (always `null` in M2), `uiState` (empty maps / null
title in M2 — M5 populates both).

### 4.7 REST semantics and error mapping

All handlers: decode → validate (400 `invalid_request` naming the offending field) →
Manager call → map errors → encode. Pinned envelope and codes (CONV §2). Mappings used
in M2: unknown session → 404 `not_found`; unknown type/checkout, bad behavior, empty
message → 400; message/stats on a non-live session → 409 `conflict` (resume-on-demand is
M6 — see §4.10 and §10); pi `success:false` or command timeout → 502 `pi_error`; panic →
500 `internal`. Unknown `/api/*` paths get a JSON 404 from a `GET|POST /api/` catch-all
so the SPA fallback never serves HTML to API clients.

Endpoint notes beyond CONV §3's table:

- `GET /api/sessions` — enumerate checkouts (§4.8), read each checkout's registry, merge
  with in-memory handles: a handle's wire status wins; records without a handle (created
  by a previous server run) pass their persisted status through the CONV §3 derivation.
  Known M2 gap: a stale `live` record from a crashed previous run shows `idle` until M6's
  startup sweep marks it `stopped`; actions on it fail with 409/404. `checkout` in each
  `SessionSummary` is the checkout **name** (basename). Sort by `lastActivityAt`
  descending (deterministic; local decision).
- `POST /api/sessions` — validates `type` against config and `checkout` against
  enumeration (SPEC §2.2.2: checkout is chosen at launch, orthogonal to type), then the
  §4.1 create flow. 201 + `{"session":SessionSummary}`.
- `POST .../abort` — forwards pi `abort` (rpc.md; success even when idle), returns the
  current summary.
- `POST .../close` — Manager: `Close(ctx)` on the pisession (SIGTERM→5s→SIGKILL,
  CONV §6), registry → `closed`, pid zeroed, `status` event published. Idempotent: close
  of an already-closed session returns 200 with the current summary (local decision).
- `GET .../stats` — live only: forwards `get_session_stats` data verbatim
  (`{"stats":<data>}`); non-live → 409 `conflict` (local decision, §4.10).
- `GET /api/health` — `{"ok":true,"version":"<ldflags version>"}`.
- `GET /api/config/session-types` — from config; `model`/`thinking` serialized as `null`
  when unset (CONV §3).

### 4.8 Checkout enumeration (SPEC §2.2.1)

`workspace.Checkouts()` runs `git worktree list --porcelain` in the launch checkout and
parses stanza blocks:

```
worktree /Users/x/Code/github.com/org/repo/main
HEAD 3fa2…
branch refs/heads/main

worktree /Users/x/Code/github.com/org/repo/wt-alt
HEAD 9bc1…
branch refs/heads/alt
```

Mapping to the pinned wire shape (CONV §3): `path` = the `worktree` line; `name` =
`filepath.Base(path)` (unique key); `branch` = `refs/heads/` stripped, `null` for
`detached`; `isPrimary` = first stanza (git lists the main working tree first); `bare`
stanzas are skipped. The parser is a pure function over the porcelain text (exec split
out) for table testing. Duplicate basenames violate the pinned "name = basename, unique
key" assumption → the endpoint returns 500 `internal` with a message naming both paths
(grove layouts never produce this). Enumeration runs per request — subprocess cost is
negligible for a single-user tool, and it always reflects `git worktree add/remove`.

### 4.9 Keep-alive ownership, server lifecycle

Processes belong to the server, not to any client (SPEC §1.1, §5.2.1): SSE disconnects,
zero attached clients, idle time — none of it touches the pi process. Only
`POST .../close` (→ `closed`) and server shutdown (→ `stopped`) do.

`cmd/serve` wiring: config → workspace → `session.NewManager` → `httpapi.New` handler
mounted in front of the M0 SPA/dev-proxy fallback. `http.Server` settings that matter for
SSE: `WriteTimeout: 0` (a global write timeout would kill every stream) — liveness comes
from per-write deadlines (§4.5) plus heartbeats; keep a sane `ReadHeaderTimeout`.

**Shutdown order** (SIGINT/SIGTERM): (1) `Manager.Shutdown(ctx)` — concurrently for each
live session: pisession `Close` (SIGTERM→5s→SIGKILL), registry → `stopped` (CONV §6:
`stopped` on server shutdown, `closed` only on user close), pid zeroed, then close all
Brokers, which closes subscriber channels and unblocks every SSE handler; (2)
`http.Server.Shutdown` with a ~10s grace, which can now complete because no handler is
parked on a channel. Session files persist; M6 adds resume (SPEC §5.3.1).

### 4.10 Local decisions & open questions

Decisions local to M2 (not pinned by CONV; flagged per CONV §10):

1. **`POST .../message` to a `stopped`/`closed` session → 409 `conflict`** with a message
   noting resume arrives later. CONV §3 pins respawn-then-send; that behavior is M6's
   (MILESTONES: "Resume-on-demand … not here"). M6 replaces the 409 with the pinned
   respawn — no wire-shape change, only the failure mode disappears.
2. **`GET .../stats` on a non-live session → 409 `conflict`** — stats require a live
   process; CONV §3 doesn't pin the non-live case. If M4/M6 want file-derived stats,
   that's their call.
3. **Create-failure cleanup:** kill + remove the registry record (the session never
   usably existed). Any stray session file pi created is orphaned but harmless and would
   surface via CONV §5 rebuild semantics in M6.
4. **Close idempotency** (200, current summary) and **list ordering**
   (`lastActivityAt` desc).
5. **Any pi failure on the SSE fetch ⇒ `reset`** (§4.5 case 5) — pi documents no other
   `get_entries` failure mode.

Open question for the conventions author (does not block M2): whether `GET /api/sessions`
should mark handle-less `live` registry records as `stopped` *in the response* before
M6's startup sweep exists. M2 passes them through the pinned derivation (→ `idle`) to
avoid pre-deciding M6's cleanup semantics.

## 5. Implementation steps

1. `internal/workspace/checkouts.go` — `Checkout` struct, porcelain parser (pure), 
   `Checkouts()` exec wrapper; table tests + a real-git test via `testws.New(t)` plus
   `git worktree add`.
2. `internal/session/broker.go` — `StreamEvent` (CONV §7), `Broker`, `Subscription`,
   non-blocking publish with overflow kick, configurable capacity (default 256); unit
   tests (order, fan-out, overflow, idempotent close).
3. `internal/session/status.go` — status state + wire derivation; table tests over
   {registry status} × {streaming} × {pendingDialog nil/non-nil} (the dialog row proves
   the M5 hook without M5).
4. `internal/session/events.go` — classification per §4.4 table + registry side effects.
5. `internal/session/manager.go` — handles, create flow, pump goroutine, Send/Abort/
   CloseSession/List/Get/Stats/Subscribe/Shutdown; registry writes via `internal/store`.
6. `internal/session/history.go` — `HistorySnapshot`, live path (`get_entries`),
   file path (header-scan locate, LF-only line reads, `since` filtering),
   `ErrInvalidCursor`.
7. `internal/fakepi/scenarios/` — add `slow_stream` and `crash_mid_stream` if M1 didn't
   ship them; ensure `get_entries` honors `since` and returns `success:false` for an
   unknown cursor (needed by the reset tests).
8. `internal/httpapi/wire.go` + `server.go` — wire types, mux with the §3 route table,
   `/api/` JSON-404 catch-all, SPA fallback mount, logging + recovery middleware.
9. `internal/httpapi/handlers.go` — all REST handlers per §4.7.
10. `internal/httpapi/sse.go` — connect algorithm per §4.5, heartbeat ticker
    (configurable interval, default 15s), per-write deadlines. The handler consumes a
    narrow local interface (`Subscribe`/`History`/`Get`) satisfied by `*session.Manager`
    so the replay/dedup algorithm is unit-testable against a scripted fake (§7).
11. `cmd/serve.go` — construct and mount everything; signal-driven shutdown in the §4.9
    order; verify `--dev` still proxies non-`/api` while `/api/**` (including SSE) is
    served by gibson.
12. Integration tests (§7), then the proof workflow (§8).

## 6. Interfaces exposed to later milestones

Exact exported surface added by M2 (names per CONV §3/§4/§7; Go-only types below are new
exported names inside pinned packages, flagged here as the seam later plans consume):

- `internal/workspace`:
  - `type Checkout struct { Name, Path, Branch string; IsPrimary bool }` (`Branch == ""`
    ⇒ wire `null`)
  - `func (w *Workspace) Checkouts() ([]Checkout, error)`
- `internal/session`:
  - `type StreamEvent struct { Type string; EntryID string; Data json.RawMessage }` (CONV §7)
  - `type Subscription struct { C <-chan StreamEvent }` + `func (s *Subscription) Close()`
  - `type Entry struct { ID string; Raw json.RawMessage }`
  - `type Summary struct { ID, Name, Type, Checkout, Status string; CreatedAt, LastActivityAt time.Time }`
    — JSON tags produce exactly CONV §3's `SessionSummary`
  - `type HistorySnapshot struct { Session Summary; Entries []Entry; LeafID, Cursor *string; PendingDialog json.RawMessage; UIState UIState }`
    (`UIState{Statuses map[string]string; Widgets map[string][]string; Title *string}`)
  - `var ErrInvalidCursor error`; `var ErrNotFound error`; `var ErrNotLive error`
  - `func NewManager(cfg *config.Config, ws *workspace.Workspace, log *slog.Logger, opts ...ManagerOption) *Manager`
  - `type ManagerOption`; `func WithSubscriberBuffer(n int) ManagerOption` — overrides the
    256-event subscriber buffer capacity (test seam for the slow-client kick, §7)
  - Methods (CONV §7, minus `AnswerDialog` which M5 adds; `Stats` is an M2 addition the
    route table requires):
    `Create(ctx, typ, checkout, name, message string) (Summary, error)`,
    `Get(id string) (Summary, bool)`, `List(ctx) ([]Summary, error)`,
    `Send(ctx, id, message, behavior string) (Summary, error)`,
    `Abort(ctx, id string) (Summary, error)`, `CloseSession(ctx, id string) (Summary, error)`,
    `History(ctx, id, since string) (*HistorySnapshot, error)`,
    `Stats(ctx, id string) (json.RawMessage, error)`,
    `Subscribe(id string) (*Subscription, error)`, `Shutdown(ctx)`.
- `internal/httpapi`:
  - `func New(o Options) http.Handler` — M0's `Options` type extended (exactly as M0 §6
    anticipates) with the `session.Manager`; the config, workspace, version, and
    SPA/dev-proxy fallback fields carry over from M0 unchanged. No new constructor type.
    M5 extends the mux with the dialogs route; M3's SPA consumes the routes and SSE
    contract as-is.
- Routes + SSE contract exactly as §3 lists — M3 (client), M5 (dialogs), M6 (resume) build
  on them without change.
- `internal/fakepi` scenarios `slow_stream`, `crash_mid_stream` available via
  `FAKEPI_SCENARIO` for M3–M7 tests.

## 7. Testing

Unit (no subprocesses):

- `workspace/checkouts_test.go` — porcelain parser table: primary/linked worktrees,
  detached HEAD, bare stanza skipped, duplicate-basename error.
- `session/broker_test.go` — publish order preserved per subscriber; independent
  subscribers; overflow kicks only the full subscriber (others + publisher unaffected);
  closed-subscription safety.
- `session/status_test.go` — full derivation table (CONV §3), including
  `live`+pendingDialog ⇒ `blocked-on-dialog` (hook proven, unreachable in M2).
- `session/history_test.go` — file path: header-scan locate, `since` mid-file / at-leaf /
  unknown (⇒ `ErrInvalidCursor`), CRLF tolerance, >64KB line (guards the
  no-`bufio.Scanner` rule) using a fixture JSONL written by fakepi.
- `httpapi/sse_test.go` (algorithm-level) — scripted fake behind the handler's interface:
  fetch returns `[e1,e2]` while the subscription buffer already holds `[e2,e3]` ⇒ client
  receives exactly `e1,e2,e3` (subscribe-first + dedup proven deterministically);
  invalid-cursor ⇒ single `reset` then EOF; `Last-Event-ID` precedence over `?since=`.
- `httpapi/handlers_test.go` — error envelope shape for every code; validation 400s name
  the field; JSON 404 on unknown `/api/*`.

Integration (fakepi, via `pitest.BuildFakePi(t)` as `pi_bin`, `testws.New(t)` workspaces,
`httptest.Server` over `httpapi.New`; a small SSE-reading helper lives in
`httpapi/sse_test.go`):

- **Lifecycle:** POST create (scenario `basic`) → 201, summary `streaming` then `idle`
  (poll GET /sessions); session JSONL + stderr log exist under the checkout's `.gibson/`;
  registry record `live` with pid.
- **Fan-out:** scenario `slow_stream`; two SSE clients attach; third client POSTs a
  message; both receive byte-identical event sequences (excluding `:hb`), same entry-id
  order.
- **Mid-turn reconnect:** kill one client mid-stream, reconnect with `Last-Event-ID` =
  last seen `id:`; assert concatenated entry ids == the uninterrupted client's ids (no
  gap, no dupes); assert the first post-reconnect `pi` event with a cumulative `partial`
  repaints the in-flight message.
- **`?since=`:** GET /history, reconnect with `?since=<cursor>` ⇒ zero replayed entries,
  one `status` prime.
- **Reset:** `Last-Event-ID: bogus` ⇒ one `reset` event, stream closed.
- **Slow client:** construct the Manager with `WithSubscriberBuffer(4)`, subscribe,
  don't read, run `slow_stream` ⇒ subscriber kicked (stream EOF at the HTTP level),
  other client and pump unaffected, session finishes.
- **Heartbeat:** handler configured with 100ms interval ⇒ `:hb` observed on an idle
  stream; none interleaved mid-burst.
- **Abort:** POST abort during `slow_stream` ⇒ pi receives `abort` (fakepi records it),
  status returns to `idle`.
- **Close:** POST close ⇒ process exits (waitpid via pisession), registry `closed`, pid
  zeroed, `status` event broadcast; GET /history afterwards serves identical entries from
  the file path; POST message ⇒ 409 `conflict`; second close ⇒ 200.
- **Crash:** scenario `crash_mid_stream` ⇒ registry `stopped`, `status` event emitted,
  stderr tail logged.
- **Checkouts/list:** workspace with a second worktree ⇒ GET /checkouts shows both with
  correct `isPrimary`/`branch`; sessions created in both checkouts ⇒ GET /sessions merges
  with correct `checkout` names.
- **Shutdown:** SIGTERM the serve process (subprocess-level test or direct
  `Manager.Shutdown` + server `Shutdown`) ⇒ pi processes reaped, registry `stopped`, SSE
  handlers unblocked, clean exit.

Real-pi gated (`pitest.RequireRealPi(t)`, env `GIBSON_TEST_REAL_PI=1`):

- One end-to-end HTTP flow: create → SSE stream shows real deltas and `entry` events with
  real entry ids → `?since=` reconnect → abort → close. Never runs in default
  `go test ./...` (CONV §9).

## 8. Agent-verified proof workflow

Pure curl/scripting against a real build with **real pi** (0.82.x on `$PATH`, provider
credentials configured — CONV §9 acceptance proofs use real pi). Run from the gibson repo
root. Timing note: steps are written so every assertion holds regardless of where a kill
lands relative to turn boundaries.

1. **Build and scaffold a scratch grove workspace** (house rule: `.sandbox/`, not `/tmp`):

   ```sh
   WS="$PWD/.sandbox/m2-proof"; rm -rf "$WS"; mkdir -p "$WS/bin" "$WS/ws/demo"
   go build -o "$WS/bin/gibson" .
   cd "$WS/ws/demo" && git init -q -b main main && cd main
   printf '.gibson/\n' > .gitignore
   cat > gibson.toml <<'EOF'
   [server]
   port = 7391

   [sessions.quick]
   description = "Quick one-off task"
   EOF
   git add -A && git commit -qm init
   git worktree add -q ../wt-alt -b alt
   ```

2. **Launch and health-check:**

   ```sh
   "$WS/bin/gibson" serve > "$WS/serve.log" 2>&1 & GIBSON_PID=$!
   BASE=http://127.0.0.1:7391
   sleep 1; curl -s $BASE/api/health
   ```

   Expect `{"ok":true,"version":...}`. Define the idle-poll helper used below:

   ```sh
   wait_idle() { until curl -s $BASE/api/sessions | jq -e --arg id "$1" \
     '.sessions[] | select(.id==$id and .status=="idle")' >/dev/null; do sleep 1; done; }
   ```

3. **Static endpoints** (SPEC §7.1, §2.2.1):

   ```sh
   curl -s $BASE/api/config/session-types | jq .
   curl -s $BASE/api/checkouts | jq .
   ```

   Expect: one type `quick` with `model:null`,`thinking:null`; two checkouts — `main`
   (`isPrimary:true`, `branch:"main"`) and `wt-alt` (`branch:"alt"`).

4. **Create a session; attach two SSE clients** (snapshot-from-empty replay):

   ```sh
   SID=$(curl -s -X POST $BASE/api/sessions -H 'content-type: application/json' \
     -d '{"type":"quick","checkout":"main","name":"m2 proof","message":"Reply with exactly: ready"}' \
     | jq -r .session.id); echo "$SID"
   curl -sN $BASE/api/sessions/$SID/events > "$WS/a.log" & A=$!
   curl -sN $BASE/api/sessions/$SID/events > "$WS/b.log" & B=$!
   wait_idle "$SID"
   ```

   Expect: `$SID` matches `s-<date>-<6>`; both logs contain replayed `entry` events for
   turn 1 (attach happened after streaming began — empty cursor replays from the start),
   a `status` prime, and `ls "$WS/ws/demo/main/.gibson/sessions/"` shows one `.jsonl`,
   `ls .../logs/` shows `$SID.stderr.log`.

5. **Equal-peer identical streams** (SPEC §7.3.1; MILESTONES M2 proof): mark both logs,
   prompt from a *third* client, compare the turn's events:

   ```sh
   sleep 1   # settle: let the trailing idle status write land in both logs
   A0=$(wc -l < "$WS/a.log"); B0=$(wc -l < "$WS/b.log")
   curl -s -X POST $BASE/api/sessions/$SID/message -H 'content-type: application/json' \
     -d '{"message":"List the numbers 1 through 10, one per line."}' >/dev/null
   wait_idle "$SID"; sleep 1
   tail -n +$((A0+1)) "$WS/a.log" | grep -E '^(id:|data:)' > "$WS/a.turn"
   tail -n +$((B0+1)) "$WS/b.log" | grep -E '^(id:|data:)' > "$WS/b.turn"
   diff "$WS/a.turn" "$WS/b.turn" && echo IDENTICAL
   ```

   Expect `IDENTICAL` — both clients were attached for the whole turn, so their
   `id:`/`data:` sequences are byte-equal (heartbeat comments excluded).

6. **Mid-turn kill + `Last-Event-ID` reconnect — no gap, no dupes** (SPEC §7.2.2,
   §9.3.b analogue at the API layer):

   ```sh
   curl -s -X POST $BASE/api/sessions/$SID/message -H 'content-type: application/json' \
     -d '{"message":"Count from 1 to 120, one number per line, no other text."}' >/dev/null
   sleep 2; kill $B
   # only complete id lines — kill $B can tear b.log's final line mid-write
   LAST=$(awk '/^id: [a-z0-9]+$/{last=$2} END{print last}' "$WS/b.log"); echo "cursor: $LAST"
   curl -sN -H "Last-Event-ID: $LAST" $BASE/api/sessions/$SID/events > "$WS/b2.log" & B2=$!
   wait_idle "$SID"; sleep 1
   awk '/^id: [a-z0-9]+$/{print $2}' "$WS/a.log" > "$WS/ids.a"
   cat "$WS/b.log" "$WS/b2.log" | awk '/^id: [a-z0-9]+$/{print $2}' > "$WS/ids.b"
   diff "$WS/ids.a" "$WS/ids.b" && echo "NO GAP, NO DUPES"
   ```

   Expect `NO GAP, NO DUPES`: the concatenated entry-id sequence across B's two
   connections equals A's uninterrupted sequence exactly — whether the kill landed
   mid-turn (deltas lost by design, entries replayed) or after it.

7. **Abort** (SPEC §7.1):

   ```sh
   curl -s -X POST $BASE/api/sessions/$SID/message -H 'content-type: application/json' \
     -d '{"message":"Count from 1 to 500, one number per line."}' >/dev/null
   sleep 2
   curl -s -X POST $BASE/api/sessions/$SID/abort -H 'content-type: application/json' -d '{}' \
     | jq -r .session.status
   wait_idle "$SID"
   grep -c '"stopReason":"aborted"' "$WS/a.log"
   ```

   Expect: abort returns 200; count ≥ 1 (the aborted assistant message reaches the
   stream, rpc.md/SPEC §6.2).

8. **Heartbeats** (SPEC §7.2.3; CONV §4.1 — `:hb` at 15s idle):

   ```sh
   timeout 20 curl -sN $BASE/api/sessions/$SID/events > "$WS/hb.log" || true
   grep -c '^:hb' "$WS/hb.log"
   ```

   Expect ≥ 1.

9. **History snapshot + `?since=` handoff** (SPEC §7.3.3):

   ```sh
   CURSOR=$(curl -s $BASE/api/sessions/$SID/history | jq -r .cursor)
   # recompute from the current log — step 7's abort turn appended entries after ids.a
   LAST_A=$(awk '/^id: [a-z0-9]+$/{last=$2} END{print last}' "$WS/a.log")
   [ "$CURSOR" = "$LAST_A" ] && echo CURSOR-MATCHES
   timeout 5 curl -sN "$BASE/api/sessions/$SID/events?since=$CURSOR" > "$WS/since.log" || true
   grep -c '^id: ' "$WS/since.log"; grep -c '"type":"status"' "$WS/since.log"
   ```

   Expect `CURSOR-MATCHES`; `0` replayed entries; ≥ 1 `status` prime. Also
   `curl -s $BASE/api/sessions/$SID/history | jq '{leafId, pendingDialog, uiState}'`
   shows the pinned fields (`pendingDialog:null` in M2).

10. **Invalid cursor ⇒ reset** (CONV §4.3 step 7):

    ```sh
    timeout 5 curl -sN -H 'Last-Event-ID: bogus' $BASE/api/sessions/$SID/events > "$WS/reset.log" || true
    grep -c '"type":"reset"' "$WS/reset.log"
    ```

    Expect exactly 1, and curl exits well before the 5s timeout (server closed the stream).

11. **Stats** (SPEC §6.2 `get_session_stats`):

    ```sh
    curl -s $BASE/api/sessions/$SID/stats | jq .stats.contextUsage
    ```

    Expect `{tokens, contextWindow, percent}` (verbatim pi data).

12. **Second checkout + keep-alive + list** (SPEC §2.2.2, §5.2.1, §4.1.3):

    ```sh
    SID2=$(curl -s -X POST $BASE/api/sessions -H 'content-type: application/json' \
      -d '{"type":"quick","checkout":"wt-alt","message":"Reply with exactly: ok"}' | jq -r .session.id)
    wait_idle "$SID2"
    ls "$WS/ws/demo/wt-alt/.gibson/sessions/" "$WS/ws/demo/wt-alt/.gibson/logs/"
    curl -s $BASE/api/sessions | jq -r '.sessions[] | "\(.id) \(.checkout) \(.status)"'
    pgrep -f "$SID2" >/dev/null && echo "pi #2 alive with no client attached"
    ```

    Expect: session file + stderr log inside `wt-alt/.gibson/`; both sessions listed with
    correct `checkout` names; the second pi process alive despite zero SSE clients.

13. **Close + file-served history + M2 resume boundary** (SPEC §5.2.2; CONV §3):

    ```sh
    curl -s -X POST $BASE/api/sessions/$SID/close -H 'content-type: application/json' -d '{}' \
      | jq -r .session.status
    # match pi's argv, not the URL: clients A and B2 still hold $SID in their curl argv
    sleep 1; pgrep -f -- "--session-id $SID" || echo "pi #1 gone"
    awk '/^id: /{print $2}' "$WS/a.log" > "$WS/ids.final"
    [ "$(curl -s $BASE/api/sessions/$SID/history | jq '.entries|length')" -eq "$(wc -l < "$WS/ids.final")" ] \
      && echo "FILE HISTORY COMPLETE"
    curl -s -X POST $BASE/api/sessions/$SID/message -H 'content-type: application/json' \
      -d '{"message":"hi"}' | jq -r .error.code
    ```

    Expect: status `closed`; process gone; `FILE HISTORY COMPLETE` (the file-parse path
    returns every entry client A ever received); error code `conflict` (respawn is M6).
    Client A's log contains a `status` event carrying `"closed"` after the close call
    (the stream stays open, so `:hb` lines may follow it).

14. **Server shutdown owns its processes** (SPEC §5.3.1; CONV §6):

    ```sh
    kill -TERM $GIBSON_PID; sleep 6
    pgrep -f "$SID2" || echo "pi #2 terminated by shutdown"
    jq -r --arg id "$SID2" '.sessions[$id].status' "$WS/ws/demo/wt-alt/.gibson/state.json"
    (cd "$WS/ws/demo/main" && git status --porcelain)
    ```

    Expect: pi #2 terminated; registry status `stopped` (not `closed`); `git status`
    output empty — no `.gibson/` noise (SPEC §4.2).

15. **Cleanup:** `kill $A $B2 2>/dev/null; rm -rf "$WS"`.

All expectations passing = M2's MILESTONES proof ("creates a session, tails SSE from two
clients, sends a prompt from a third, verifies both streams carry identical ordered
events; kills one stream mid-turn, reconnects with `Last-Event-ID`, verifies no gap and
no duplicates; verifies heartbeats and abort") plus the M2-scoped process-ownership and
enumeration claims.

## 9. Success criteria checklist

- [ ] Every CONV §3 route except `POST .../dialogs/{dialogId}` responds with the pinned
      request/response shapes and error envelope (SPEC §7.1; CONV §2–3). Proof steps 2–13;
      handler tests.
- [ ] `GET /api/checkouts` enumerates worktrees via `git worktree list` with
      name/path/branch/isPrimary (SPEC §2.2.1; CONV §3). Proof step 3.
- [ ] Session creation takes type + checkout + optional name + message, spawns exactly one
      pi with cwd = the chosen checkout, session file + stderr log under that checkout's
      `.gibson/` (SPEC §2.2.2, §5.1.1–5.1.4, §7.1). Proof steps 4, 12.
- [ ] `POST .../message` supports plain and `steer`/`followUp` sends via pi's
      `streamingBehavior`; abort stops the run with `stopReason:"aborted"` visible
      (SPEC §6.2, §7.1). Proof steps 5–7; fakepi tests.
- [ ] SSE stream: `{"type","data"}` envelope, `id:` only on `entry` events carrying pi
      entry ids (SPEC §7.2.1–7.2.2; CONV §4.1–4.2). Proof steps 4–6; sse tests.
- [ ] Reconnect via `Last-Event-ID` and first-connect via `?since=` are gapless and
      duplicate-free (subscribe-first + fetched-id dedup); invalid cursor yields `reset`
      then close (SPEC §7.2.2; CONV §4.3). Proof steps 6, 9, 10; algorithm unit test.
- [ ] Missed deltas are superseded, never replayed: cumulative `partial`/`partialResult`
      repaint after reconnect (CONV §4.3; rpc.md). Fakepi mid-turn reconnect test.
- [ ] Heartbeat comment ≤ every 30s on idle streams (`:hb` at 15s) (SPEC §7.2.3;
      CONV §4.1). Proof step 8.
- [ ] Per-client buffers bounded at 256; a slow/dead client is disconnected and the pi
      stdout pump plus other clients are unaffected (SPEC §7.2.4, §10.2). Broker + slow-
      client tests.
- [ ] All clients are equal peers; any client can prompt/abort/close; concurrent streams
      carry identical ordered events (SPEC §7.3.1; MILESTONES M2). Proof step 5.
- [ ] Attach is snapshot + subscribe: `/history` returns entries + `cursor` (+ pinned
      `leafId`/`pendingDialog`/`uiState` fields), and SSE from that cursor replays nothing
      already snapshotted (SPEC §7.3.3; CONV §3). Proof step 9.
- [ ] Keep-alive ownership: processes survive zero attached clients; only close (→
      `closed`) or server shutdown (→ `stopped`, SIGTERM→5s→SIGKILL) ends them
      (SPEC §5.2.1–5.2.2, §5.3.1; CONV §6). Proof steps 12–14.
- [ ] `GET /history` on a non-live session serves entries from the session JSONL,
      identical to what live streaming delivered (CONV §3). Proof step 13.
- [ ] Wire statuses derive per CONV §3 (`idle`/`streaming` observed live; `closed`,
      `stopped` observed via close/shutdown; `blocked-on-dialog` derivation unit-proven).
      Proof steps 4–14; status table test.
- [ ] Unexpected pi death marks the session `stopped` and emits a `status` event
      (CONV §6). `crash_mid_stream` test.
- [ ] No automated Go test needs a live LLM; real-pi tests are `GIBSON_TEST_REAL_PI=1`
      gated (CONV §9).
- [ ] The §8 workflow passes end-to-end, run by an agent (MILESTONES M2 proof).

## 10. Explicitly out of scope (later milestones own these)

- **Dialogs** (M5): `POST .../dialogs/{dialogId}` route, pending-dialog registry,
  first-answer-wins + `dialog_resolved` broadcast, connect-time pending-dialog re-emit
  (CONV §4.3 step 6's dialog half), `blocked-on-dialog` occurring in practice,
  `uiState` population, `RespondUI` usage, `confirm-gate.ts` fixture flows.
- **Resume-on-demand** (M6): respawn on message to `stopped`/`closed` (replaces M2's
  409), reopen, startup orphan sweep (`live`→`stopped`), registry rebuild from
  `sessions/*.jsonl`, cross-checkout list hardening for pre-restart sessions.
- **SPA work** (M3/M4): no frontend changes; `web/` untouched beyond M0's shell.
- **Rendering-layer concerns** (M4): tool cards, thinking blocks, context meter UI.
- **Hardening** (M7): non-localhost bind verification, `.gibson/` self-containment audit,
  docs, SPEC §9's seven-step browser acceptance run.
