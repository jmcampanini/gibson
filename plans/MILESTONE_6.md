# MILESTONE_6 — Session management and restart resilience

Status: **provisional**. This forecast requires the plan-gate review in
[PROCESS.md](PROCESS.md) before it becomes the active milestone contract.

Conforms to MILESTONE_CONVENTIONS.md (binding). SPEC.md is normative; section references
below are to SPEC unless prefixed `CONV` (MILESTONE_CONVENTIONS.md) or `MS` (MILESTONES.md).

---

## 1. Goal & capability

**You can now:** treat gibson as the durable home for all sessions in the workspace —
browse them across checkouts, close and reopen them, restart the server without losing
anything. (MS M6)

Concretely: the session list enumerates every checkout's `.gibson/` and merges it with
live process state; closing a session kills its pi process but keeps it resumable;
sending a message to a `stopped`/`closed` session lazily respawns pi with the same
`--session-id` and full context; a crashed or restarted server marks its orphaned
`live` registry entries `stopped` and never re-attaches; non-live sessions render their
full history from the pi session JSONL with no process running.

## 2. Preconditions

**M0 is complete.** M6 starts from the current implementation and programs against the
later milestones' CONV §7 seam interfaces and CONV §3/§4 wire contract.

From **M1**:
- `internal/pisession`: `pisession.Session` per CONV §7 — spawn with the CONV §6 argv
  assembly (`--mode rpc --session-id <id> --session-dir <checkout>/.gibson/sessions
  [--model] [--thinking] extra_args...`, cwd = checkout), LF-only framing via
  `ReadBytes('\n')`, `c-<n>` command correlation, `Prompt/Abort/GetState/GetEntries/
  GetSessionStats/SetSessionName/RespondUI/Events/Close`, SIGTERM→5s→SIGKILL shutdown,
  stderr capture to `logs/<id>.stderr.log`; startup compatibility requires pi 0.82.0
  or newer, treats the 0.82 minor line as verified, and warns without blocking for a
  later minor or major version.
- `internal/store`: `.gibson/` layout creation, `state.json` registry with the CONV §5
  schema (`version:1`, per-session `{id,name,type,status,createdAt,lastActivityAt,pid}`,
  status `live|stopped|closed`), process-local serialization plus per-checkout
  cross-process locking with reload-under-lock and atomic write-temp-then-rename
  replacement, allocation-locked `CreateSession`, lifecycle-enforcing `SetLive` /
  `SetStatus`, session id generation `s-<YYYYMMDD>-<6 [a-z0-9]>` with collision
  regeneration, and `FindSessionFile(id)` locating a session's JSONL by its **header** id,
  never by filename.
- `internal/fakepi` + `internal/pitest` (`BuildFakePi(t)`) + `internal/testws`
  (`testws.New(t)`): fakepi answers `get_state/get_entries/get_session_stats/
  set_session_name/prompt/steer/follow_up/abort` and **writes a real v3 session JSONL**
  honoring `--session-id`/`--session-dir` (CONV §9).

From **M2**:
- `internal/workspace` checkout enumeration via `git worktree list --porcelain`
  (`name` = directory basename, unique key; CONV §3 `GET /api/checkouts`).
- `internal/session`: `session.Manager` per CONV §7 (`Create/Get/List/Send/Abort/
  AnswerDialog/CloseSession/History/Subscribe`), the per-session Broker with bounded
  256-event per-client buffers, the status machine deriving wire status
  `idle|streaming|blocked-on-dialog|stopped|closed` per CONV §3 (streaming flag set on
  `agent_start`, cleared on `agent_settled`, fallback `agent_end`), unexpected-exit
  handling (registry → `stopped`, `status` event, stderr tail logged; CONV §6),
  graceful server shutdown marking live sessions `stopped` (CONV §6).
- `internal/httpapi`: the full CONV §3 route table and the CONV §4 SSE contract,
  including the connect algorithm (subscribe-first, fetch, replay, drain+dedup, prime,
  `reset` on invalid cursor). `/history` and the SSE fetch step already serve
  **non-live** sessions too, through `Manager.History`'s single code path
  (`internal/session/history.go`, MILESTONE_2 §4.6): live → `get_entries` on the process;
  non-live → parse the session JSONL (header-scan locate, `since` filtering,
  `session.ErrInvalidCursor`). `List()` already enumerates every checkout and merges
  each registry with in-memory state, sorted by `lastActivityAt` desc (MILESTONE_2 §4.7).
  **What M2 leaves for M6:** resume (`POST /message` to a non-live session is a 409 in
  M2); registry rebuild-when-missing; the startup orphan sweep and read-time orphan
  guard (M2 shows a stale `live` record as `idle`); and resolution of sessions this
  server never spawned — M2's handles (hence `Get`/`History`/`Subscribe`) exist only
  for sessions created by the running server.

From **M3–M5** (frontend and dialogs):
- `web/src/api/{types,client,stream}.ts`, `sessionStore.ts` reducer folding snapshot +
  SSE through one path; `SessionPage`, `LaunchFlow`, `Composer`, `StatusStrip`,
  `DialogModal`, `ToastHost`, `ContextMeter` components; `SessionListPage` exists as a
  skeleton route (M3's entry point for LaunchFlow) but not the full list UI.
- M5: dialog bridging, pending-dialog tracking in the Manager (feeds
  `blocked-on-dialog` derivation and `/history.pendingDialog`), `dialog`/
  `dialog_resolved`/`ui` SSE events.

If any assumption above diverges from the prior milestone's actual implementation,
reconcile toward CONV §3–§7 — those are the binding seams; the notes here only locate
where M6 picks up.

## 3. Deliverables

Go:
- `internal/store/sessionfile.go` (new) + `internal/store/sessionfile_test.go`:
  rebuild helpers only — extract a session file's header id/timestamp and its latest
  `session_info` name; per-store id→path cache over M1's `FindSessionFile`. The entry
  reader is **not** duplicated here: M2's `internal/session/history.go` (header-scan
  locate, `since` filtering, `session.ErrInvalidCursor`) stays the one file-read path
  and is extended in place (§4.5).
- `internal/store/registry.go` (extend, M1 file) + tests: orphan sweep
  (`live`→`stopped`, pid zeroed), registry rebuild from `sessions/*.jsonl` when
  `state.json` is missing (CONV §5), corrupt-registry quarantine.
- `internal/session/manager.go` (extend) + `internal/session/manager_test.go`:
  cross-checkout resolution of sessions this server never spawned; `List()` gains
  rebuild-when-missing and the read-time orphan guard (M2 already merges every
  checkout's registry and sorts); resume-on-demand in `Send`; per-id serialization of
  send/resume/close; Broker persistence across process death; close semantics for
  non-live sessions; `History` (M2's one code path) now resolves non-live sessions in
  any checkout.
- `internal/app/serve.go` (extend): compose the startup orphan sweep across all
  checkouts before the listener accepts requests.
- `internal/httpapi` (extend M2's handler files in place): little new — non-live
  `/history`, the non-live SSE fetch step, non-live `/stats` → 409, and idempotent
  `/close` shipped in M2 (MILESTONE_2 §4.5–§4.7, §4.10); M6 adds `409 conflict` for
  `/abort` on non-live sessions, replaces M2's non-live `/message` 409 with the
  resume path, and extends everything to sessions resolved cross-checkout.
- `internal/fakepi` (extend): resume behavior (load an existing session file for its
  `--session-id`, append with correct `parentId` chain, serve full `get_entries`);
  log its argv to stderr at startup (lets tests assert resume argv via the captured
  stderr log).

Frontend (follow M3's established `web/src/` layout for file placement):
- `SessionListPage` completed: all sessions across checkouts — name, type, checkout,
  status badge (`blocked-on-dialog` visually loud), relative last activity; actions
  open / close / new session; periodic refresh.
- `SessionPage` non-live handling: history renders from snapshot with no process;
  stopped/closed banner ("sending a message resumes this session"); `ContextMeter`
  hidden when non-live; status transitions live via existing `status` SSE events.
- `web/src/api/client.ts`: `closeSession(id)` (if M3–M5 did not need it), no other
  additions — no new routes or wire fields exist in M6.

Routes: **none added or changed.** M6 fills in semantics behind the existing CONV §3
table.

## 4. Design & rationale

### 4.1 The cross-checkout session list (SPEC §4.1.4, §7.1)

`Manager.List()` becomes the SPEC §4.1.4 definition executed literally: enumerate
checkouts (M2 `internal/workspace`), load each checkout's registry (rebuilding per
§4.1.2 / CONV §5 if `state.json` is missing), and merge with in-memory process state.
M2 already enumerates, loads, merges, and sorts (MILESTONE_2 §4.7); M6's additions are the
rebuild and the read-time orphan guard:

- id present in the Manager's in-memory live map → wire status derived per CONV §3
  (`blocked-on-dialog` / `streaming` / `idle`).
- id absent but registry says `live` → **display and treat as `stopped`** (read-time
  orphan guard, §4.4 below).
- otherwise → registry status passes through (`stopped`/`closed`).

`checkout` in each `SessionSummary` is the basename of the checkout whose `.gibson/`
holds the entry (the registry stores no checkout field — CONV §5). Sort by
`lastActivityAt` descending. A pruned worktree simply vanishes from enumeration and
its sessions with it — intended (§4.1.3). Sessions created by `gibson run` (M1) appear
too; they are ordinary registry entries.

Cross-checkout id collisions are cryptographically negligible (id generation checks
only its own checkout); resolution order is enumeration order, first match wins, with
a warning log if a duplicate is ever observed.

There is no workspace-level cache or registry; `List()` scans on every call. A scan is
one JSON file read per checkout — cheap at personal-tool scale, and always fresh.

### 4.2 Close vs. reopen (SPEC §5.2.2, §5.3.3)

**Close** (`POST /close` → `Manager.CloseSession`): if a process is live, run the CONV
§6 shutdown sequence (SIGTERM → 5s → SIGKILL, reap, close pipes); registry → `closed`,
pid zeroed. The entry stays in `state.json` and the session file stays on disk: closed
sessions remain listed and resumable. Closing a `stopped` session just marks it
`closed` (no process to kill). Closing an already-`closed` session is **idempotent**:
200 with the current summary. Rationale: close expresses "I'm done with this", which
is meaningful state regardless of process liveness, and idempotency keeps the UI's
close button race-free across two clients. (Existing error codes only; no new wire
surface — this is an M6-local refinement of route semantics left open by CONV §3.)

**Reopen** is not an endpoint. Per SPEC §5.3.3 and CONV §3, the one and only resume
trigger is `POST /api/sessions/{id}/message` against a `stopped`/`closed` session.
Opening a session's page renders history from the file (§4.5) and **never spawns** —
this is what keeps §5.3.4's "no eager respawn" true even for browsing: you can read
ten dead sessions without creating ten node processes. The UI makes this legible with
a banner on non-live sessions: "stopped — sending a message resumes it."

### 4.3 Resume-on-demand (SPEC §5.3.3) and config drift

`Manager.Send(id, msg, behavior)` on a session with no live process:

1. Resolve `(checkout, store.Record)` across checkouts (§4.1 resolution).
2. Assemble argv **identically to create** — same CONV §6 builder, same
   `--session-id <id> --session-dir <checkout>/.gibson/sessions`, cwd = checkout. Pi
   opens the existing session file for that id and reconstructs context itself
   (open-or-create is pi's resume model, documented in the CLI reference /
   `pi --help` — `--session-id <id>: use exact project session ID, creating it if
   missing`; rpc.md documents only `--session-dir`. BACKGROUND: "resume is a
   spawn-time affair").
3. Readiness probe: `get_state`; assert `data.sessionId == id` (sanity: the process is
   on our session). Skip `set_session_name` — the name is already persisted in the
   session file as a `session_info` entry (session-format.md); if the registry's name
   is empty (rebuilt registry) and `get_state.sessionName` is set, mirror it back.
4. Registry → `live` with the new pid; emit a `status` SSE event; then `prompt` the
   user's message. `lastActivityAt` updates on the accepted prompt (CONV §3).

**Session-type config drift.** The registry stores only the type *name* (CONV §5 —
deliberately no argv snapshot), so resume must consult the **current** `gibson.toml`:

| Registry `type` | In current config? | Resume argv |
|---|---|---|
| e.g. `"review"` | yes (possibly edited since create) | current config's model/thinking/extra_args |
| e.g. `"review"` | no (deleted/renamed) | **fail: 400 `invalid_request`**, message naming the type and `gibson.toml` |
| `""` (rebuilt registry, §4.1.2) | — | bare argv (`--mode rpc --session-id --session-dir` only), warning logged |

Rationale: config is the live source of truth for *how* to run pi — adopting the
current definition means fixing a typo'd model or upgrading an extension path applies
on the next resume, which is what a user editing config expects. A named-but-missing
type fails loud rather than silently spawning without its extensions: a review session
resumed without its review extension misbehaves invisibly, and gibson's ethos is loud
failure over silent drift (§3.1.3, §5.4, §10.5). The empty type is the degraded-
metadata case SPEC §4.1.2 requires to keep working, so it spawns bare. Note pi itself
persists mid-session model/thinking changes as `model_change`/`thinking_level_change`
entries and restores them from the file when rebuilding context (session-format.md,
"Context Building") — gibson passes the type's flags identically on create and resume
and does not try to compensate; v1 has no mid-session switching anyway (§1.2).
Flag-vs-file precedence on resume is deliberately not relied upon.

**Single-writer under concurrency (SPEC §5.1.3).** Each managed session carries a
mutex serializing send/resume/close for that id:

```go
type managed struct {
    mu     sync.Mutex          // serializes Send / resume / CloseSession per id
    proc   *pisession.Session  // nil unless live
    broker *Broker             // survives proc; created on first touch, never evicted
    // checkout, registry snapshot, streaming flag, pendingDialog (M5), ...
}
```

Two simultaneous `POST /message` to a stopped session: the first respawns under the
lock; the second blocks, then finds `proc != nil` and follows normal live-session
rules (including pi's requirement of `streamingBehavior` if the first message started
streaming — the M2 error mapping applies unchanged). It is impossible to spawn two
processes for one id, and the on-disk `live` status of an orphan never blocks resume
because in-memory state is the sole authority for liveness (§4.4).

**Missing session file at resume** (user deleted it): pi's open-or-create starts the
id fresh; gibson logs a warning. History was already showing empty; nothing corrupts.

### 4.4 Startup orphan cleanup (SPEC §5.3.2) — and how orphans are detected

**Definition:** an orphan is a registry entry with `status:"live"` whose session id
has no corresponding in-memory live process in this server's Manager. That is the
entire detection rule. The recorded `pid` is *never* consulted for liveness and never
signaled (CONV §5: diagnostic only) — pid reuse makes both unsafe, and SPEC §5.3.2
forbids re-attachment outright. SPEC §5.3.1's model is that pi processes die with the
server (pi exits when its stdin pipe closes; graceful shutdown additionally SIGTERMs
them per CONV §6), so a surviving `live` entry means the server died uncleanly, not
that a process awaits us.

Two layers enforce it:

1. **Startup sweep** (`internal/app/serve.go`, after workspace enumeration, before the
   HTTP listener accepts): for every enumerated checkout whose `.gibson/state.json`
   exists, flip every `live` entry to `stopped`, zero its pid, write back atomically.
   Log one line per checkout swept through the injected Charm Log v2 logger: count
   marked. Checkouts without a `.gibson/` are
   left untouched (never litter worktrees that ran no sessions). A fresh server has an
   empty live map, so at startup *every* `live` entry is by definition an orphan.
2. **Read-time guard** (belt and braces, §4.1): any registry `live` entry without an
   in-memory process is treated as `stopped` wherever it is read — list, history,
   resume. This covers registries that appear after startup (e.g. a worktree added
   mid-run) and makes the sweep a persistence nicety rather than a correctness
   dependency.

Corrupt `state.json` (unparsable JSON): rename to `state.json.corrupt-<unix-ts>`,
rebuild per §4.1.2, log prominently. Registry lifecycle is M6's remit (MS coverage
map §4) and silent data loss is worse than a quarantined file.

M1 deliberately fails closed when a session JSONL has an empty, malformed, unreadable,
or duplicate header, so the offending checkout requires manual repair before another
session can be allocated. M6 owns a recovery policy as part of rebuild: preserve the
original bytes, identify every affected path in a prominent diagnostic, and either
quarantine or skip invalid files only under an explicit rebuild operation. Normal M1
allocation remains strict; recovery must never discard a file silently.

### 4.5 History for non-live sessions: parse the JSONL (decided, justified)

For `stopped`/`closed` sessions, `/history` and the SSE connect fetch step read the
pi session JSONL directly (CONV §3 pins this; here is why it is right, versus
spawning pi to run `get_entries`):

1. **Laziness and the zero-process invariant.** Spawning pi to *read* would mean
   browsing ten dead sessions costs ten node processes — exactly what §5.3.4's
   no-eager-respawn is protecting against.
2. **There is no read-only pi.** `--session-id` opens the file as a potential writer,
   and extensions run at startup and may append entries — viewing history could
   mutate it and races a concurrent real resume (single-writer, §5.1.3/§10.1).
3. **Cost.** Node startup is O(seconds) per page view; a file read is O(ms).
4. **Fidelity.** The file is append-only and file order **is** append order —
   byte-identical to what `get_entries` (which "gets all session entries in append
   order", rpc.md) would return for the same session, so snapshot-vs-live rendering
   cannot diverge.

Reader algorithm (shipped in M2 as `internal/session/history.go`'s non-live path,
MILESTONE_2 §4.6 — restated because M6 depends on its exact semantics):

```
open file; read lines with bufio.Reader.ReadBytes('\n')   // CONV §6 framing rules
line 1 must be the header  {"type":"session","version":3,"id":<session id>,...} — skip it
every later non-empty line → json.RawMessage entry; record its "id"
cursor = id of last entry (nil if none)
since != "": find since among entry ids → return entries strictly after it
             not found → ErrInvalidCursor        // SSE path maps this to `reset`
```

The session file for id X is located by scanning `sessions/*.jsonl` and matching the
**header's** `id` field — never by filename (pi's naming scheme is undocumented for
`--session-id` mode and filenames are exactly the kind of surface §10.5 warns about);
that is M1's `store.FindSessionFile`. M6's actual new work: resolve the owning
checkout first (§4.1), add a per-store id→path cache (one header line per file per
scan; re-verify on miss), and feed the located file to M2's reader for sessions this
server never spawned — one reader, one `session.ErrInvalidCursor`.

Non-live `/history` therefore returns: `session` (summary from registry), `entries`
(verbatim `json.RawMessage` lines, CONV §2), `cursor` = last entry id, `leafId` =
`cursor` (gibson-written sessions are linear; if a terminal escape-hatch user branched
the file, the approximation only affects the informational `leafId`, never `cursor`
correctness), `pendingDialog: null` and empty `uiState` (dialog and UI state live only
in a process's memory — a dead session cannot be blocked; its pending dialog died with
the process, which is also why `blocked-on-dialog` can never apply to non-live
sessions).

### 4.6 Broker persists across process death; SSE spans stop→resume

The Broker is keyed to the **session id**, not the process: created on first touch
(subscribe, send, or history of a resolvable session), never evicted in v1 (a few
structs per session; personal scale). A client streaming a session that stops —
or connected to a `stopped` session that later resumes — keeps its one SSE connection:
the CONV §4.3 connect algorithm's fetch step uses `get_entries` when live and the
§4.5 file reader when not (same subscribe-first/replay/dedup flow either way), the
prime step emits the current status (`stopped`/`closed` included — both are wire
statuses), and on resume the new process's event pump publishes into the existing
Broker, so subscribers see `status` → entries → deltas with no reconnect. This is what
makes resume feel like "the session came back" rather than "open a new thing."

### 4.7 Non-live semantics for the remaining routes

- `POST /abort` on non-live → `409 conflict` ("session is not live"): abort targets a
  running turn; there is none.
- `GET /stats` on non-live → `409 conflict`: `get_session_stats` requires a process,
  and spawning one violates §4.5's reasoning. The UI hides `ContextMeter` for
  non-live sessions. (Existing error code; M6-local refinement.)
- `POST /dialogs/{dialogId}` on non-live → `409 conflict`, exactly as M5 shipped it
  (MILESTONE_5 §4.4 step 1: `AnswerDialog` requires registry-status `live`). Dialogs
  cannot outlive the process (§4.5); M6 changes nothing here.
- `POST /message` → resume path (§4.3). `POST /close` → §4.2.

### 4.8 Frontend list refresh

No global SSE stream exists in the CONV §4 contract (streams are per-session), so
`SessionListPage` refreshes by polling `GET /api/sessions` every 5s while mounted,
plus on window focus. Client-local behavior; no wire surface added. Cheap because
`List()` is cheap (§4.1). A workspace-level stream is a possible post-v1 nicety, not
an M6 open question.

## 5. Implementation steps

All paths repo-relative to `~/Code/github.com/jmcampanini/gibson/main`.

1. **`internal/store/sessionfile.go`** — rebuild helpers only (the non-live entry
   reader already exists: M2's `internal/session/history.go`): extract the header
   id/timestamp and the latest `session_info` name from a session JSONL; add a
   per-store id→path cache over M1's `FindSessionFile`. Table tests in
   `internal/store/sessionfile_test.go`: header parse, latest-`session_info`-wins,
   no `session_info` present, empty file, file-for-id not found, cache re-verify
   on miss.
2. **`internal/store/registry.go`** — extend M1's registry: `SweepOrphans()` flipping
   `live`→`stopped` + pid-zeroing with atomic write; rebuild-when-missing using step
   1's helpers (id from header, name from latest `session_info`, `status:"stopped"`,
   `type:""`, `createdAt` from header timestamp, `lastActivityAt` from file mtime —
   CONV §5); corrupt-file quarantine (§4.4). Tests: sweep counts and preserves
   `stopped`/`closed`, rebuild yields degraded-but-listed entries, quarantine path.
3. **`internal/app/serve.go`** — compose the startup sweep across all enumerated
   checkouts before the listener accepts (§4.4), with one Charm Log v2 line per swept
   checkout. Test at the store level plus one `internal/app` test wiring a `testws`
   workspace with a hand-written `live` registry entry and asserting post-startup state.
4. **`internal/session/manager.go`** — cross-checkout resolution: internal
   `resolve(id) (checkout string, entry store.Record, ok bool)` scanning enumerated
   checkouts' registries (rebuilding-when-missing via step 2); `List()` per §4.1 —
   the new parts are rebuild-when-missing and the read-time orphan guard (M2 already
   merges and sorts); `Get`/`History`/`Subscribe` work for sessions this server never
   spawned by feeding `resolve` + `FindSessionFile` into M2's
   `internal/session/history.go` non-live path (one reader, one
   `session.ErrInvalidCursor`).
5. **`internal/session/manager.go`** — resume-on-demand: the `managed` struct + per-id
   mutex (§4.3), respawn inside `Send` with the drift table, readiness probe with
   session-id assertion, name mirror-back, registry→`live`, `status` emission; Broker
   creation-on-first-touch and pump re-attachment on respawn (§4.6); `CloseSession`
   semantics for non-live (§4.2). The status machine gains only transitions — no new
   statuses exist to add.
6. **`internal/httpapi`** (extend M2's handlers in their existing files): the
   live/non-live `/history` branch, the SSE fetch step, `session.ErrInvalidCursor` →
   `reset` (CONV §4.3 step 7), non-live `/stats` → 409, and idempotent `/close` all
   shipped in M2 behind `Manager.History` — no rewiring. M6 adds `/abort` non-live →
   `409`, removes M2's non-live `/message` 409 in favor of the resume path, keeps
   M5's `/dialogs` non-live `409 conflict` unchanged (§4.7), and asserts all of it
   against never-spawned, cross-checkout sessions.
7. **`internal/fakepi`** — resume support: at startup, if a JSONL whose header id
   matches `--session-id` exists in `--session-dir`, load it and chain appended
   entries via `parentId` from its last entry; `get_entries` serves the full set
   honoring `since`; log full argv to stderr on startup (assertable via the captured
   `logs/<id>.stderr.log`). Scenario behavior otherwise unchanged.
8. **Frontend** — complete `SessionListPage` (columns, status badges with
   `blocked-on-dialog` loud per §8.2/§10.3, relative timestamps, open/close/new,
   5s + focus refresh); `SessionPage` non-live banner + composer hint, hide
   `ContextMeter` when status is `stopped`/`closed`; add `closeSession` to
   `web/src/api/client.ts` if absent. All types already exist in
   `web/src/api/types.ts`.
9. **Integration tests** (fakepi; see §7) and the proof script assets.

## 6. Interfaces exposed to later milestones

No new routes, SSE event types, wire fields, or statuses (CONV §10 compliance:
everything M6 ships fits the existing CONV §3/§4/§5 shapes). Newly exported Go surface
(M7's hardening/audit work programs against these):

- `internal/store`:
  - `func (s *Store) SweepOrphans() (marked int, err error)`
  - `func (s *Store) RebuildRegistry() error` — rebuild `state.json` from
    `sessions/*.jsonl` per CONV §5, built on the step-1 helpers
  - M1's `FindSessionFile(id)` gains a per-store id→path cache; no new entry reader
    and no second invalid-cursor sentinel — M2's `internal/session/history.go` with
    `session.ErrInvalidCursor` remains the only session-file read path. (If M1/M2
    shipped an equivalent under a different name, extend it in place — package and
    file names hold; CONV pins no store or history method names.)
- `internal/session`: `Manager.List()` is now the authoritative cross-checkout list;
  `Manager.Send` resumes non-live sessions; `Manager.Subscribe` works for any
  resolvable session id regardless of liveness. Signatures unchanged from CONV §7.
- Semantics now guaranteed to M7's acceptance run: `/history` on non-live sessions,
  `409 conflict` on non-live `/abort` and `/stats`, idempotent `/close`, startup
  sweep behavior.

## 7. Testing

Unit (no subprocess): steps 1–2 tests above (`internal/store`).

fakepi integration (`pitest.BuildFakePi(t)` + `testws.New(t)`; second checkout via
`git worktree add ../wt-b -b b` inside the test workspace):

- **Resume round-trip** (`internal/session/manager_test.go`): Create → prompt →
  wait idle → `CloseSession` (assert process reaped, registry `closed`) → `Send` →
  assert exactly one new fakepi process, registry `live`, and `History` returns
  pre-close *and* post-resume entries in order with a correctly chained `parentId`.
- **Argv on resume**: after resume, read `logs/<id>.stderr.log`; assert the fakepi
  argv line matches the CONV §6 assembly with the same `--session-id`, and reflects
  the *current* config when the type's model was edited between create and resume.
- **Drift failures**: delete the type from config between create and resume →
  `Send` fails; via HTTP, `400 invalid_request` naming the type. Rebuilt-registry
  entry (`type:""`) → resume succeeds with bare argv (assert via stderr argv line).
- **Concurrent resume**: two goroutines `Send` a stopped session, both with behavior
  `"followUp"` — the loser of the race hits a possibly-streaming process, and pi
  rejects a mid-stream prompt without `streamingBehavior` (§4.3), so declaring it
  makes acceptance deterministic against a streaming scenario. Assert one spawn
  (fakepi can count spawns via a side file keyed by pid, or assert single argv line
  in the stderr log) and both prompts accepted.
- **Orphan sweep** (`cmd` or manager level): write a registry with a `live` entry +
  bogus pid into a testws checkout, run the serve startup path (or `SweepOrphans`
  directly), assert `stopped` + pid 0, and that `stopped`/`closed` entries and other
  checkouts are untouched.
- **Cross-checkout list** (`internal/httpapi`): sessions created in two checkouts
  (one live via Manager, one present only on disk as `stopped`); `GET /api/sessions`
  shows both with correct `checkout` basenames and statuses; delete `state.json` in
  one checkout → its session still lists (rebuild, degraded `type:""`, SPEC §4.1.2).
- **Blocked badge survives the list rewrite** (`internal/httpapi`): scenario
  `dialog_confirm`, leave the dialog unanswered → `GET /api/sessions` reports that
  session `blocked-on-dialog` while a stopped on-disk session lists alongside it
  (M5 proved the badge against M3's skeleton list; this pins it through M6's
  rewrite).
- **Non-live history + SSE**: `/history` for a stopped session returns file entries,
  `cursor` = last id, `pendingDialog:null`; SSE connect with no cursor replays all
  entries with `id:` lines then primes `status:"stopped"`; connect with `?since=`
  mid-file replays the tail only; bogus `?since=` → `reset` then close. Then resume
  via `POST /message` **while the SSE client stays connected** and assert the same
  stream carries `status` → new `entry` events (Broker persistence, §4.6).
- **Non-live route semantics**: `/abort` and `/stats` → 409; `/dialogs/x` → 409
  `conflict` (M5's mapping, §4.7); `/close` on stopped → `closed`; `/close` again →
  200 idempotent.

Real-pi gated (`pitest.RequireRealPi(t)`, env `GIBSON_TEST_REAL_PI=1`; no LLM call
needed): pi 0.82.1 flushes the session file lazily — a prompt-less spawn writes no
file (`dist/core/session-manager.js` buffers until the first assistant message
exists), so a spawn/close/respawn sequence would take the create-not-open path and
prove nothing. Instead: pre-seed `--session-dir` with a valid v3 session JSONL whose
header id is the target session id and which contains at least one assistant
`message` entry (generated by fakepi, or a handwritten fixture); spawn real pi with
`--session-id <id>`; assert `get_state` returns `sessionId == id`, `sessionFile`
pointing at the seeded file, and `messageCount > 0`, and that stderr carries no
"No project session found" warning. Proves pi opens an existing session for
`--session-id` without any network.

Frontend: reducer already covered by M3/M4 tests; M6 adds no reducer cases (snapshot
path is identical for non-live). List rendering is proven by the workflow below.

## 8. Agent-verified proof workflow

Real pi + real LLM + browser automation, per MS M6 and CONV §9. Scratch space in
`.sandbox/` per house rules. `$REPO` = `~/Code/github.com/jmcampanini/gibson/main`.

1. **Build**: `cd $REPO/web && npm ci`, then `cd $REPO && make build` and
   `go test ./...` (canonical `build/gibson` artifact present; all tests green with no
   network — fakepi only).
2. **Scratch workspace** (`ROOT=$REPO/.sandbox/m6/ws`):
   ```sh
   mkdir -p $ROOT/main && cd $ROOT/main && git init -b main
   cat > gibson.toml <<EOF
   [server]
   port = 7461
   [sessions.quick]
   description = "Quick one-off task"
   [sessions.gate]
   description = "Confirm-gated tools"
   extra_args = ["-e", "$REPO/test/fixtures/extensions/confirm-gate.ts"]
   EOF
   printf '.gibson/\n' > .gitignore
   git add -A && git commit -m init
   git worktree add ../wt-feature -b feature
   ```
3. **Start**: `cd $ROOT/main && $REPO/build/gibson serve` (background; record PID).
   Expect: health OK (`curl -s localhost:7461/api/health`), startup log shows
   no sweep activity (no registries yet).
4. **Create session A in `main`** via
   `curl -s -X POST localhost:7461/api/sessions -H 'content-type: application/json' -d '{"type":"quick","checkout":"main","name":"proof-a","message":"Reply with exactly the single word: pineapple"}'`
   → 201, capture `id` (`$A`). Poll `GET /api/sessions` until A is `idle`.
5. **Create session B in `wt-feature`** the same way with message
   `"The codeword is zephyr. Acknowledge in one short sentence."` → `$B`; wait `idle`.
6. **List (browser)**: open `http://localhost:7461/` — `SessionListPage` shows both
   sessions with checkout values `main` and `wt-feature`, both `idle`, non-empty
   last-activity. Assert same via `curl -s localhost:7461/api/sessions`.
7. **Close A** from the browser's close action. Expect: list shows A `closed`;
   `curl` confirms; `pgrep -f "session-id $A"` is empty;
   `$ROOT/main/.gibson/state.json` records A `"closed"` with `"pid":0`.
8. **Simulated crash**: `kill -9 <serve PID>`. Assert
   `$ROOT/wt-feature/.gibson/state.json` still records B `"live"` with a nonzero pid
   (the crash left a stale entry — the orphan under test), and no pi processes remain
   (`pgrep -f "pi --mode rpc"` empty).
9. **Restart**: start serve again from `$ROOT/main`. Expect a startup log line for
   the sweep in `wt-feature` (1 marked stopped). `curl /api/sessions`: A `closed`,
   B `stopped`, both still listed with correct checkouts (SPEC §9.4.a, list survives
   restart). No pi process spawned (`pgrep` empty — SPEC §5.3.4).
10. **Non-live history**: open session B in the browser. Expect the full prior
    conversation (codeword exchange) rendered, status `stopped` banner visible,
    context meter absent — while `pgrep -f "pi --mode rpc"` is still empty (history
    came from the JSONL, not a process).
11. **Resume with context intact**: in B's composer send `"What is the codeword?"`.
    Expect: exactly one pi process appears with `--session-id $B` (pgrep), UI status
    flips `streaming` then `idle`, and the streamed answer contains **zephyr** —
    pre-restart context survived (SPEC §9.4.a, §9.2.d). Assert `wt-feature`'s
    `state.json` now records B `"live"` with the new pid.
12. **Reopen a closed session**: open A, send `"What word did you reply with
    earlier?"` → respawn observed, answer contains **pineapple** (close kept it
    resumable — SPEC §5.2.2).
13. **Blocked badge in the completed list** (SPEC §8.2, §10.3): create session C in
    `main` via the step-4 curl shape — type `gate`, message `"Use the bash tool to
    run 'ls' and name one file."` — capture `$C`. Poll `GET /api/sessions` until C is
    `blocked-on-dialog`; assert the browser list page shows C with the loud "needs
    input" badge while A and B do not. Then answer it:
    `DID=$(curl -s localhost:7461/api/sessions/$C/history | jq -r .pendingDialog.id)`,
    then `curl -s -X POST localhost:7461/api/sessions/$C/dialogs/$DID -H 'content-type: application/json' -d '{"confirmed":true}'`
    → `{"resolved":true}`; wait until C is `idle`.
14. **Hygiene**: `git status --porcelain` in both checkouts shows no `.gibson/`
    noise; `logs/$A.stderr.log` and `logs/$B.stderr.log` exist in their respective
    checkouts; each session id has exactly one JSONL under its `sessions/`.
15. **Graceful shutdown**: SIGTERM the server; both `state.json` files show every
    previously-live session (A, B, C) `"stopped"` (not `live`, not `closed`), pids
    zeroed.

## 9. Success criteria checklist

- [ ] `go test ./...` passes with no network (fakepi resume, sweep, list, non-live
      history/SSE, drift, concurrency tests all green) — CONV §9.
- [ ] Session list enumerates checkouts and scans each `.gibson/`, merging live
      process state; no workspace-level registry exists — SPEC §4.1.4, §7.1.
- [ ] Deleting a `state.json` still lists that checkout's sessions with degraded
      metadata — SPEC §4.1.2.
- [ ] Close terminates the process, marks `closed`, and the session remains listed
      and resumable — SPEC §5.2.2, §9.2.d.
- [ ] Resume respawns with the same `--session-id`/`--session-dir` and the CONV §6
      argv from the *current* config; missing type fails loud; rebuilt `type:""`
      resumes bare — SPEC §5.3.3; drift table §4.3.
- [ ] Resume happens only on `POST /message`; browsing/`/history`/startup never
      spawn — SPEC §5.3.4.
- [ ] Startup marks all stale `live` entries `stopped` across every checkout, zeroes
      pids, and nothing ever signals or re-attaches to a recorded pid — SPEC §5.3.2,
      §9.4.a; CONV §5.
- [ ] Exactly one pi process per session id at all times, including under concurrent
      resume attempts — SPEC §5.1.3, §10.1.
- [ ] Non-live `/history` and SSE replay come from the session JSONL (header
      skipped, append order, `since` honored, invalid cursor → `reset`), and an
      SSE client connected to a stopped session sees its resume live on the same
      stream — SPEC §7.3.3; CONV §3, §4.3.
- [ ] Session list UI shows name/type/checkout/status/last-activity with
      `blocked-on-dialog` visually loud (blocked-badge fakepi list test; proof
      step 13); open/close/new actions work — SPEC §8.2, §10.3.
- [ ] The §8 workflow passes end-to-end run by an agent: restart-surviving list,
      crash-orphan sweep, pre-restart session resumed in the browser with intact
      history ("zephyr"/"pineapple" recalled) — SPEC §9.4.a; MS M6 proof.

## 10. Explicitly out of scope

- The full seven-step acceptance run, multi-device `bind`, slow-client backpressure
  verification, pi compatibility-policy proof, `.gibson/` self-containment audit,
  README/docs — M7.
- Any new routes/wire fields (e.g. an explicit reopen endpoint, a workspace-level
  SSE stream for list updates, session delete/rename) — not in SPEC v1; list
  freshness is client polling (§4.8).
- Session tree/branch/fork rendering — v1 non-goal (SPEC §1.2); the file reader
  treats sessions as linear append order (§4.5).
- Stats for non-live sessions (computing token totals from the JSONL) — v1 returns
  `409`; a file-derived fallback is post-v1 polish.
- Killing or adopting still-running orphan pi processes — forbidden territory
  (SPEC §5.3.2); the sweep only rewrites registry state.
- Idle-timeout reaping — SPEC §5.2.1 explicitly keeps processes alive until close.
