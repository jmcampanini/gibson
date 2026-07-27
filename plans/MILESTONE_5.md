# MILESTONE_5 — Extension dialogs and surfaces

Conforms to [MILESTONE_CONVENTIONS.md](MILESTONE_CONVENTIONS.md) (all §-references to "conventions"
mean that file; "SPEC" means [SPEC.md](SPEC.md)). Scope is exactly MILESTONES.md M5.

---

## 1. Goal & capability

**You can now:** run session types whose extensions ask questions — approve/deny gates,
selects, inputs — from the browser, on whichever device answers first.

Concretely: `extension_ui_request`/`extension_ui_response` is bridged over SSE/REST
(SPEC §6.4, §7). The four blocking dialogs (`select`/`confirm`/`input`/`editor`) render
as modals; `notify` renders as toasts; `setStatus`/`setWidget`/`setTitle` render in a
status strip; `set_editor_text` prefills the composer. Dialogs broadcast to all clients,
first answer wins, the resolution event closes every other client's modal, and a session
waiting on a dialog is loudly `blocked-on-dialog` in session state (SPEC §10.3).

## 2. Preconditions

**M0 is complete.** M5 starts from the current implementation and consumes later
milestone contracts through the shared seams.

- **M1**: `internal/pisession` per conventions §6–§7: spawn/argv assembly (`extra_args`
  verbatim, last), LF-only framing, single-writer goroutine, command correlation,
  `Events() <-chan pisession.Event` (`{Type, Raw}` — `extension_ui_request` events arrive
  on this channel like any other pi event), `Close(ctx)`. The conventions-§7 method
  `RespondUI(id, resolution)` is already **implemented and exercised** by M1: its fakepi
  `dialog_confirm` scenario blocks until the response arrives on stdin and M1's tests
  drive it end-to-end, with the exported type
  `pisession.UIResolution{Value *string; Confirmed *bool; Cancelled bool}` (MILESTONE_1 §6).
  M5 consumes both as-is except for one deliberate amendment: `Cancelled` becomes
  `*bool` (step 1 below). `internal/fakepi` + `pitest.BuildFakePi(t)` exist with the
  scenario machinery (scenario via `FAKEPI_SCENARIO`, real v3 JSONL output), including
  the dialog await/response plumbing and the `dialog_confirm` scenario, which M5
  extends (§4.8).
- **M2**: `internal/session.Manager` + per-session Broker + `session.StreamEvent`;
  `internal/httpapi` with every conventions-§3 route **except**
  `POST /api/sessions/{id}/dialogs/{dialogId}` (dialogs are explicitly deferred to M5 by
  MILESTONES M2); SSE per conventions §4 including the connect algorithm steps 1–6 (step 6
  emitted only `status` so far — no dialogs existed); wire-status derivation with the
  streaming flag (`agent_start` → `agent_settled`, `agent_end` fallback). The `/history`
  response already carries the pinned field names `pendingDialog` (always `null` in M2)
  and `uiState` (always empty) — if M2 omitted them instead, M5 adds them; the names and
  shapes are pinned by conventions §3 either way, so this is not a breaking change.
- **M3**: SPA skeleton — `src/api/{types,client,stream}.ts`, `sessionStore.ts` with the
  single `sessionReducer` code path (snapshot folded as synthetic events), `SessionPage`,
  `Composer`, a session list (`SessionListPage`) that renders `SessionSummary.status`,
  and the web test runner M3 established (assumed Vitest; whatever M3 chose, M5 follows).
- **M4**: full chat rendering; `Composer` offers steer/follow-up keyed off wire
  `status === "streaming"` (MILESTONE_4 §4.6) — there is **no** client-side streaming flag
  in the reducer yet. M5 must build one (§4.9, step 8), because M5's `blocked-on-dialog`
  masking (§4.5) makes wire status the wrong key for the composer mid-run.

Required reading for the implementer, verbatim per SPEC §6:
`~/.local/lib/node_modules/@earendil-works/pi-coding-agent/docs/rpc.md` (esp. "Extension
UI Protocol"), and as worked examples:
`~/.local/lib/node_modules/@earendil-works/pi-coding-agent/examples/rpc-extension-ui.ts`
(a complete RPC client handling every UI method) and
`~/.local/lib/node_modules/@earendil-works/pi-coding-agent/examples/extensions/permission-gate.ts`
(the canonical gate extension M5's fixture is modeled on).

## 3. Deliverables

Server:

- `internal/session/dialogs.go` (+ `dialogs_test.go`) — per-session dialog registry:
  pending set, resolved ring, first-answer-wins claim, stale-dialog sweep, uiState fold.
- `internal/session/manager.go` — extended: `extension_ui_request` classification in the
  event pump, `AnswerDialog` implementation, status derivation gains the pending-dialog
  input, `History` populates `pendingDialog` + `uiState`, exit/close sweeps.
- `internal/pisession/` (wherever M1 defined `UIResolution`) — **deliberate amendment
  of M1's exported type**: `Cancelled bool` → `Cancelled *bool` (§4.4's validation must
  distinguish an absent `cancelled` from an explicit `cancelled:false`, which is
  rejected), plus a new `Validate(method string) error`; M1's marshal and
  `dialog_confirm` tests are updated in the same change. `RespondUI` itself ships from
  M1 unchanged.
- `internal/httpapi/dialogs.go` (+ `_test.go`) — `POST /api/sessions/{id}/dialogs/{dialogId}`.
- `internal/httpapi/sse.go` — connect-algorithm step 6 now also emits the pending
  `dialog` event (conventions §4.3.6).
- `internal/fakepi/` — M1's dialog plumbing (await `extension_ui_response` by id) gains
  unmatched-id dropping (mirrors pi, §4.1.4); M1's existing `dialog_confirm` scenario is
  extended (notify echo, resolution-encoding entry, `agent_end`/`agent_settled` tail —
  §4.8); new scenarios `dialog_all`, `dialog_timeout` under `internal/fakepi/scenarios/`.

Fixtures:

- `test/fixtures/extensions/confirm-gate.ts` — the conventions-§9 pinned fixture
  (`ctx.ui.confirm()` before tool execution), with an env-controlled optional timeout.
- `test/fixtures/extensions/dialog-demo.ts` — **new fixture M5 owns**: exercises every
  dialog type and every fire-and-forget surface on demand via extension commands, one
  blocking dialog per command so the withheld `prompt` response (§4.1.8) never trips
  the 30s correlation timeout on an answered send (design in §4.7). Only
  `confirm-gate.ts` is a cross-milestone seam (M7 uses it); `dialog-demo.ts` is M5-local.

Frontend (`web/src/`):

- `api/types.ts` — `ExtensionUIRequest`, `DialogResolution`, `DialogResolvedData`,
  `UIState`; `api/client.ts` — `answerDialog()`.
- `state/sessionStore.ts` — reducer handling for `dialog`, `dialog_resolved`, `ui`
  event types; new state: `pendingDialog`, `uiState`, `toasts`, `editorText`, and
  `isStreaming` derived from `pi` events (`agent_start` sets, `agent_settled` clears,
  `agent_end` fallback — §4.9).
- Components: `DialogModal` (new), `ToastHost` (new), `StatusStrip` (new), `Composer`
  (set_editor_text application; steer/follow-up and abort rekeyed off `isStreaming`),
  `SessionPage` (mount points), `SessionListPage` (loud `blocked-on-dialog` badge).

## 4. Design & rationale

### 4.1 Verified pi facts this design rests on (pi 0.82.1, from source + rpc.md)

These were read from the installed package, not memory:

1. Blocking dialog methods `select`/`confirm`/`input`/`editor` emit
   `{"type":"extension_ui_request","id":"<uuid>","method":...}` on stdout and block the
   extension's `await` until a matching `{"type":"extension_ui_response","id":...}`
   arrives on stdin (rpc.md "Extension UI Protocol").
2. Response shapes: `value` (string) for select/input/editor; `confirmed` (bool) for
   confirm; `cancelled: true` dismisses any dialog — the extension then receives
   `undefined` (select/input/editor) or `false` (confirm).
3. **Timeout auto-resolve is silent.** `select`/`confirm`/`input` accept
   `{timeout: ms}` (`editor` takes no options and can never carry a timeout —
   `dist/core/extensions/types.d.ts`). On expiry, pi's `createDialogPromise` resolves
   the extension with the default (`undefined`/`false`), deletes the pending entry, and
   **emits nothing on stdout** (`dist/modes/rpc/rpc-mode.js`). Gibson gets no signal.
4. **Unmatched `extension_ui_response` ids are silently dropped** by pi
   (`pendingExtensionRequests.get(id)` miss → return). A late answer after auto-resolve
   is harmless; there is no error response to correlate. This is why conventions §6 pins
   `RespondUI` as expecting no reply.
5. Fire-and-forget methods `notify` (`message`, `notifyType: info|warning|error`),
   `setStatus` (`statusKey`, `statusText` — absent text clears the key), `setWidget`
   (`widgetKey`, `widgetLines` — absent/empty clears; `widgetPlacement:
   "aboveEditor"|"belowEditor"`, default above), `setTitle` (`title`),
   `set_editor_text` (`text`) each emit an `extension_ui_request` with a fresh uuid and
   expect no response. Note the method-name casing is exactly as pi sends it:
   `setStatus`/`setWidget`/`setTitle` camelCase but `set_editor_text` snake_case.
6. Extension commands (a `prompt` whose message is `/name`) execute immediately, even
   mid-stream, and run **outside** the agent loop — a dialog raised from a command
   handler is not bracketed by `agent_start`/`agent_end` (rpc.md "prompt").
7. A blocking dialog raised from a `tool_call` hook stalls the run at that await: no
   further run events (`tool_execution_*`, `turn_end`, `agent_end`) can arrive while it
   is truly pending. Therefore any **hook-raised** dialog observed pending at
   `agent_end`/`agent_settled` is stale (already auto-resolved or abandoned by pi).
   The carve-out: a dialog raised from an extension *command* (fact 6) lives outside
   the agent loop, so a **concurrent** run can end while it is genuinely pending — and
   gibson cannot distinguish the two cases from the wire. This is why the §4.4a sweeps
   write their cancellation to pi instead of assuming staleness.
8. **`prompt` responses can be withheld by dialogs.** The `prompt` response is emitted
   only once the prompt is "accepted, queued, or handled" (rpc.md "prompt"); for an
   extension command, "handled" means the handler ran to completion — pi `await`s the
   handler before responding (`dist/core/agent-session.js`,
   `await this._tryExecuteExtensionCommand(text)` inside prompt handling). A command
   handler that raises a blocking dialog therefore keeps the `prompt` response
   outstanding until that dialog resolves, and gibson's pinned 30s command correlation
   (conventions §6) turns an unanswered command dialog into a 502 `pi_error` on the
   triggering `POST /message`. The handler keeps running and the dialog stays
   answerable — only the sender's HTTP response is affected. §4.7's fixture design and
   proof steps 4–5 are shaped around this fact.

### 4.2 Server-side dialog registry (`internal/session/dialogs.go`)

Per-session, owned by the Manager's session record, guarded by its own mutex:

```go
type dialogRegistry struct {
    mu       sync.Mutex
    pending  []*pendingDialog          // arrival order; in practice length ≤ 1
    resolved map[string]UIResolutionRec // ring-capped at 128, FIFO eviction
}
type pendingDialog struct {
    ID         string          // pi's request uuid
    Method     string          // select|confirm|input|editor
    Raw        json.RawMessage // the extension_ui_request verbatim (conventions §2)
    ReceivedAt time.Time
}
```

- **Registration** happens on the Manager's single event-pump goroutine (same goroutine
  that folds all other pi events — preserves event ordering into the Broker).
- `pending` is a slice, not a single slot: pi serializes blocking dialogs in practice
  (each blocks its extension await), but nothing in the protocol forbids two extensions'
  requests overlapping, and correctness is cheap. The conventions-§3 `pendingDialog`
  history field is singular — it and the SSE connect prime (§4.6) expose only the
  **oldest unresolved** entry, matching the pinned singular contract; any additional
  simultaneous dialog (never observed with pi 0.82.1) remains answerable by `dialogId`
  and surfaces as the exposed one the moment the first resolves. This keeps the wire
  contract exactly as pinned while making the server race-proof.
- `resolved` exists solely to distinguish 409 `dialog_already_answered` from 404
  `not_found` (conventions §2 error codes) and to absorb late answers after sweeps.
  Capped at 128 per session; an answer for an evicted id gets 404. Not persisted —
  dialogs are process-scoped ephemera by design (conventions §4.2: `dialog` is
  non-durable, recovered via history + connect-time re-emit).

### 4.3 Event classification and uiState fold (Manager pump)

When `pisession.Event.Type == "extension_ui_request"`, minimally parse
`{id, method, statusKey?, statusText?, widgetKey?, widgetLines?, title?}` — the raw
payload is still forwarded verbatim (conventions §2, churn guard):

- `select|confirm|input|editor` → register in `dialogRegistry.pending`; publish
  `StreamEvent{Type:"dialog", Data: raw}`; recompute wire status and publish a
  `status` event (now `blocked-on-dialog`).
- `notify|setStatus|setWidget|setTitle|set_editor_text` → publish
  `StreamEvent{Type:"ui", Data: raw}`; additionally fold `setStatus`/`setWidget`/
  `setTitle` into the in-memory `uiState`:
  - `setStatus`: `statusText` present → `statuses[statusKey] = statusText`; absent/null
    → delete key.
  - `setWidget`: `widgetLines` non-empty → `widgets[widgetKey] = widgetLines`;
    absent/empty → delete key. Placement is **not** stored — conventions §3 pins
    `uiState.widgets` as `{key:[lines]}` with no placement field; snapshot-restored
    widgets therefore default to above-editor on the client, refined by live `ui`
    events which carry `widgetPlacement` verbatim. Accepted, pinned limitation.
  - `setTitle`: `title` → `uiState.title`.
  - `notify` and `set_editor_text` are transient — never folded into uiState.
- Unknown `method` (future pi drift): log at `warn`, forward as `ui` (clients ignore
  unknown methods), never register as blocking — gibson must not manufacture a block it
  cannot answer. Gibson rejects pi versions below 0.82.0; later-version drift remains
  visible through the unverified-version warning and real-pi verification (SPEC §5.4,
  §10.5).

`uiState` is in-memory only, dies with the process. `History` for `stopped`/`closed`
sessions returns empty `statuses`/`widgets` maps and `title: null` (and
`pendingDialog: null`) — the pinned shape, vacuous when non-live.

### 4.4 First-answer-wins, double answers, and the deadlock discipline

`Manager.AnswerDialog(id, dialogID, res)` — the single resolution point:

```go
// 1. session lookup; non-live session -> not_found (404): dialogs cannot outlive the
//    process, so a non-live session has no pending dialog by definition (the same
//    semantics MILESTONE_6 §4.7 pins for POST /dialogs on non-live sessions).
// 2. shape-validate res against the stored method (see below); else invalid_request (400).
// 3. CLAIM under dialogRegistry.mu:
//      - dialogID in resolved  -> ErrDialogAlreadyAnswered (409 dialog_already_answered)
//      - dialogID not pending  -> ErrDialogNotFound        (404 not_found)
//      - else: remove from pending, record into resolved with res. Claim won.
// 4. OUTSIDE the lock: sess.RespondUI(dialogID, res)   // fire-and-forget stdin write
// 5. Publish StreamEvent{Type:"dialog_resolved", Data:{"dialogId":..., "resolution":res}}
//    then recompute + publish "status"; bump lastActivityAt (conventions §3).
```

- Step 3 is the entire race: N concurrent answers serialize on the mutex; exactly one
  transitions pending→resolved; every later one gets a clean
  `409 {"error":{"code":"dialog_already_answered",...}}`. No partial states.
- The pi write (step 4) happens **outside** the registry lock and goes through M1's
  single-writer goroutine — the registry mutex never waits on pipe backpressure
  (deadlock discipline; MILESTONES calls dialogs' failure modes out by name). If the
  write fails (process died between claim and write), the claim stands, the
  `dialog_resolved` still broadcasts (clients must unblock), the process-exit path
  (§4.4a) handles the session; the HTTP response is still `{"resolved":true}` — the
  user's answer was accepted by gibson even if pi no longer needed it.
- Clients close modals on the `dialog_resolved` event, not on their own POST response —
  one code path for "I answered" and "someone else answered" (conventions §8 reducer
  discipline).
- **Shape validation** (step 2), keyed by stored method — gibson validates shape only,
  never semantics (no option-membership checks; verbatim-forwarding philosophy):
  exactly one of the three fields may be present; `cancelled` must be `true` if present;
  `select|input|editor` accept `value: string` or `cancelled: true`; `confirm` accepts
  `confirmed: bool` or `cancelled: true`. Anything else → 400.

**4.4a Sweeps.** Three places clear pending dialogs without a user answer, all
resolving each swept dialog as `{"cancelled": true}` — a faithful stand-in for pi's
auto-resolve defaults, since `cancelled` yields exactly `undefined`/`false`, the same
values the timeout default produces (§4.1.3, §4.1.2). **A sweep is a full §4.4
resolution, steps 3–5 included**: each swept dialog is claimed pending→`resolved`,
**gibson sends `RespondUI(dialogID, {cancelled:true})` to pi** (step 4; skipped only in
sweep 2, where the process is already dead and the write would just fail), then the
`dialog_resolved` broadcast with that resolution and one `status` event follow. The pi
write is what makes the sweep safe against §4.1.7's carve-out: for a genuinely stale id
pi silently drops the unmatched response (§4.1.4, harmless), while for a dialog that is
in fact still pending inside pi — a command-raised dialog surviving a concurrent run's
end — it cancels the await and unblocks the extension instead of stranding it forever
with a dialog no client can answer any more.

1. **Run end**: on `agent_end` and `agent_settled` in the pump — a hook-raised dialog
   still pending here is stale by §4.1.7 (already auto-resolved inside pi or abandoned);
   a command-raised one may be genuinely live, and the sweep's `RespondUI
   {cancelled:true}` cancels it in pi (accepted edge below). This is the *only* timeout
   handling gibson does: **no gibson-side timers**, exactly as SPEC §6.4.3 mandates
   ("gibson does not track timeouts").
2. **Process exit** (unexpected exit, and `CloseSession`): a dead pi can never resolve
   anything; sweep (no pi write) before the registry flips to `stopped`/`closed` so
   clients' modals close alongside the status change.
3. There is deliberately **no sweep on abort**: pi's `abort` does not documentedly
   resolve pending extension awaits; if the run ends because of the abort, sweep (1)
   fires anyway.

Accepted edges (documented, not solved): a dialog raised from an extension *command*
(§4.1.6) that pi auto-resolves has no bracketing `agent_end`, so its gibson mirror
persists until answered (harmlessly forwarded, dropped by pi, state converges via the
normal resolution broadcast) or until process end. The converse edge: a command-raised
dialog **genuinely pending** when a *concurrent* run ends is force-cancelled by sweep
(1) — the extension receives the cancel default and every client's modal closes. That
is a deliberate trade: no modal outlives a run's end, and no extension await is ever
stranded. M5's fixtures put timeouts only on tool-gate dialogs, where sweep (1) applies
cleanly, and raise command dialogs only outside concurrent runs. Post-timeout answers
can diverge cosmetically from what the extension actually received (it got the
default); the transcript entries remain the truth. All of these behaviors follow
directly from pi giving no auto-resolve signal and no wire-level way to tell hook- from
command-raised dialogs.

### 4.5 Status derivation (conventions §3, completed)

M5 adds the missing input: `live` + `len(pending) > 0` → `blocked-on-dialog`,
**masking** the streaming flag (a gated tool call is mid-run: `agent_start` has fired,
yet the actionable truth is "a human must answer"). Otherwise unchanged from M2:
`live`+streaming → `streaming`; `live` → `idle`; `stopped`/`closed` pass through.
`status` events are published on: dialog registered, dialog resolved (any path), plus
the M2 triggers. `GET /api/sessions` picks the derived status up for free — that is what
makes the session list surface blocked sessions (SPEC §10.3).

### 4.6 Wire: REST route + SSE priming

- `POST /api/sessions/{id}/dialogs/{dialogId}` with body
  `{"value"?, "confirmed"?, "cancelled"?}` → `Manager.AnswerDialog`. 200
  `{"resolved":true}`; errors map per §4.4 (conventions §2/§3 exactly).
- SSE (conventions §4): `dialog`, `dialog_resolved`, `ui` events are default messages
  with **no `id:` line** (only `entry` carries ids — M2's writer already keys off
  `StreamEvent.EntryID == ""`). Connect-algorithm step 6 becomes fully realized: after
  replay+drain, emit current `status`, then — if a dialog is pending — one `dialog`
  event (oldest unresolved, raw verbatim). A pure-SSE reconnect thus recovers the
  actionable modal without REST; `/history`'s `pendingDialog` covers the
  snapshot-first path (SPEC §7.3.3).
- `/history` now returns `pendingDialog` (raw verbatim or `null`) and populated
  `uiState` for live sessions.

### 4.7 Fixture extensions

**`test/fixtures/extensions/confirm-gate.ts`** (pinned seam, conventions §9; M7 reuses
it for SPEC §9.5.1). Modeled on `examples/extensions/permission-gate.ts`, reduced to a
deterministic gate on *every* tool call, with an env-tunable timeout so the same file
proves both the answered and the auto-resolve paths (extensions inherit pi's env, which
inherits gibson's — conventions §6):

```ts
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  const t = Number(process.env.CONFIRM_GATE_TIMEOUT_MS);
  const opts = Number.isFinite(t) && t > 0 ? { timeout: t } : undefined;

  pi.on("tool_call", async (event, ctx) => {
    const ok = await ctx.ui.confirm(
      `Run ${event.toolName}?`, JSON.stringify(event.input).slice(0, 200), opts);
    if (!ok) {
      ctx.ui.notify("Tool call blocked by user", "warning");
      return { block: true, reason: "Blocked by user" };
    }
    return undefined;
  });
}
```

**`test/fixtures/extensions/dialog-demo.ts`** (M5-local). Purpose-built to exercise
every dialog type and surface *on demand* — extension commands fire immediately on a
plain message (§4.1.6), so browser automation triggers each flow by sending `/…` text.
**One blocking dialog per command, deliberately** (§4.1.8): pi withholds the command's
`prompt` response until the handler completes, so a single multi-dialog handler would
keep `POST /message` hanging unless all four modals were answered inside the pinned 30s
correlation window. One dialog per command keeps each answered send comfortably inside
that window; a deliberately unanswered dialog fails only its own triggering send with
the documented 502 (proof step 5):

```ts
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  const last: { sel?: string; ok?: boolean; name?: string } = {};

  pi.registerCommand("demo-select", {
    description: "One select dialog",
    handler: async (_args, ctx) => {
      const sel = await ctx.ui.select("Pick a color", ["Red", "Green", "Blue"]);
      last.sel = sel;
      ctx.ui.notify(`select: ${sel ?? "cancelled"}`, "info");
    },
  });
  pi.registerCommand("demo-confirm", {
    description: "One confirm dialog",
    handler: async (_args, ctx) => {
      const ok = await ctx.ui.confirm("Proceed?", "This is the confirm step.");
      last.ok = ok;
      ctx.ui.notify(`confirm: ${ok}`, ok ? "info" : "warning");
    },
  });
  pi.registerCommand("demo-input", {
    description: "One input dialog",
    handler: async (_args, ctx) => {
      const name = await ctx.ui.input("Enter a name", "type here...");
      last.name = name;
      ctx.ui.notify(`input: ${name ?? "cancelled"}`, "info");
    },
  });
  pi.registerCommand("demo-editor", {
    description: "One editor dialog",
    handler: async (_args, ctx) => {
      const text = await ctx.ui.editor("Edit the text", "line 1\nline 2");
      ctx.ui.notify(`editor: ${text?.split("\n").length ?? 0} lines`, "info");
    },
  });
  pi.registerCommand("demo-surfaces", {
    description: "Fire every fire-and-forget surface",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("demo", `done: ${last.sel}/${last.ok}/${last.name}`);
      ctx.ui.setWidget("demo", ["--- dialog-demo ---", `select=${last.sel}`, `input=${last.name}`]);
      ctx.ui.setTitle("gibson dialog-demo");
      ctx.ui.setEditorText("prefilled by dialog-demo");
    },
  });
  pi.registerCommand("demo-clear", {
    description: "Clear demo status + widget",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("demo", undefined);
      ctx.ui.setWidget("demo", undefined);
    },
  });
}
```

Notes: each dialog's outcome is echoed via `notify`, so browser automation asserts
resolutions from toast text alone; `demo-surfaces` renders the module-scoped `last`
answers, so the status chip asserts what the extension actually received across the
separate commands; `demo-clear` proves the clear semantics of the uiState fold.
Commands run outside the agent loop — this also exercises `blocked-on-dialog` while
*not* streaming.

### 4.8 fakepi dialog support

M1's RPC loop already ships the core dialog plumbing: emit an `extension_ui_request`
line, then block the scenario script until the matching `extension_ui_response` arrives
on stdin (that is what proved `RespondUI`'s write path in M1). M5 refines it —
deterministic ids (`fp-d-1`, `fp-d-2`, … — gibson treats dialog ids as opaque) and
silent dropping of unmatched ids (mirrors pi, §4.1.4). Of the scenarios below,
`dialog_confirm` exists since M1 (request + block + finish stream); M5 extends its
script with the notify echo, the resolution-encoding entry, and the
`agent_end`/`agent_settled` tail. `dialog_all` and `dialog_timeout` are new. Scenario
data under `internal/fakepi/scenarios/`:

| Scenario | Script on `prompt` |
|---|---|
| `dialog_confirm` (extends M1's) | `agent_start` → confirm request (`fp-d-1`) → **block** → on response: `notify` echoing `confirmed=<v>`, `entry_appended` assistant entry whose text encodes the resolution, `agent_end`, `agent_settled`. JSONL written as always. |
| `dialog_all` | `agent_start` → select → confirm → input → editor (each awaited; each echoed via `notify <method>=<resolution>`) → `setStatus`/`setWidget`(with `widgetPlacement`)/`setTitle`/`set_editor_text` batch → summary entry → `agent_end`/`agent_settled`. |
| `dialog_timeout` | `agent_start` → confirm request with `"timeout":1500` → wait ≤1.5s for a response; on none, **emit nothing** (pi's exact behavior) and proceed with the default (`false`): entry `confirmed=false (timeout)` → `agent_end` → `agent_settled`. Exercises gibson's run-end sweep. |

The resolution-echoing entries are what integration tests assert to prove the response
actually reached the "extension" side.

### 4.9 Frontend

State (`sessionStore.ts`) — all through the one reducer; history snapshot folds
`pendingDialog` as a synthetic `dialog` event (identical code path, conventions §8) and
assigns `uiState` in the snapshot-init action:

- `dialog` → `pendingDialog = {request, receivedAt: Date.now()}` (ignore if same id
  already pending or id in a small recently-resolved set — the connect-time re-emit may
  duplicate what history already delivered).
- `dialog_resolved` → clear `pendingDialog` when ids match; remember id briefly.
- `ui` → switch on `method`: `notify` → append to `toasts` (id, message, notifyType;
  list capped); `setStatus`/`setWidget`/`setTitle` → fold into `uiState` with the same
  clear semantics as the server (§4.3), keeping `widgetPlacement` from live events;
  `set_editor_text` → `editorText = {text, seq: seq+1}`.
- `status` → as in M2/M3, now including `blocked-on-dialog`.
- `pi` → the reducer now also derives a client-side `isStreaming` flag:
  `data.type === "agent_start"` sets it, `"agent_settled"` clears it, `"agent_end"`
  clears it as fallback if settled never arrives; the snapshot-init action seeds it
  from the snapshot's wire status (`"streaming"` → `true`). M4 §4.6 keyed the composer
  off wire status; M5's §4.5 masking breaks that key — a gated mid-run session reads
  `blocked-on-dialog`, the inherited composer would offer plain Send, and pi rejects a
  behavior-less mid-stream prompt (502). Streaming truth therefore moves client-side,
  onto the agent events themselves.

Components:

- **`DialogModal`** — rendered whenever `pendingDialog` is set; blocks nothing else on
  the page (the composer stays usable; steer-vs-follow-up is keyed off the reducer's
  new `isStreaming` flag (above), *not* off `blocked-on-dialog` — a command-raised
  dialog is answerable while idle). Per method: `select` = title + one button per
  `options[]`; `confirm` = title + `message` + Confirm/Deny (→ `confirmed:true/false`);
  `input` = title + single-line input with `placeholder`; `editor` = title + textarea
  prefilled from `prefill`. Every method gets a Cancel affordance (Esc / ✕) →
  `{cancelled:true}`. Submit → `answerDialog()`; buttons disable while the POST is in
  flight; the modal closes **only** on `dialog_resolved` (§4.4); a 409
  `dialog_already_answered` is benign — show a quiet "answered on another device" toast
  and keep waiting for the (already in-flight) resolution event.
  **Countdown**: if the request carries `timeout` (ms), render a live countdown from
  `receivedAt + timeout` — display only, per SPEC §6.4.3 the client tracks nothing; at
  zero show "timed out — the agent continues with the default" and leave the buttons
  enabled (a late click harmlessly converges, §4.4a). The modal then closes when the
  run-end sweep broadcasts `dialog_resolved`.
- **`ToastHost`** — stacked toasts from `notify`, styled by `notifyType`
  (info/warning/error), auto-dismiss ~5s (errors sticky until clicked).
- **`StatusStrip`** — adjacent to the composer: `statuses` as `key: text` chips (sorted
  by key), `title` text (also mirrored into `document.title`), and widget blocks
  rendered as monospace line groups — `aboveEditor` group above the composer,
  `belowEditor` below (snapshot-restored widgets default to above, §4.3).
- **`Composer`** — applies `editorText` when `seq` changes: replaces the draft.
  Additionally rekeyed: the steer-vs-follow-up choice and the abort button's visibility
  now follow `isStreaming` (falling back to `status === "streaming"` before any agent
  event has been seen), replacing M4 §4.6's wire-status keying — so a gated mid-run
  session (`blocked-on-dialog`, `isStreaming: true`) still offers Steer / Queue
  follow-up, and an idle session blocked on a command-raised dialog offers plain Send.
  M4's race-handling and queue-chip behavior are otherwise unchanged.
- **`SessionListPage`** — `blocked-on-dialog` gets the loud treatment (SPEC §10.3):
  high-contrast pulsing "needs input" badge, sorted/flagged so it can't be missed. The
  full cross-checkout list remains M6; M5 only restyles the status M3 already renders.

## 5. Implementation steps

1. `internal/pisession/`: amend M1's exported `UIResolution` — `Cancelled bool` →
   `Cancelled *bool` (deliberate change to M1's type: the §4.4/§7 validation must tell
   an absent `cancelled` from an explicit `cancelled:false`, which is rejected); keep
   `omitempty` JSON tags so marshaling still emits exactly one field; add
   `Validate(method string) error` implementing §4.4's shape matrix; update M1's
   marshal and `dialog_confirm` tests for the pointer change. `RespondUI` itself is
   M1's, unchanged — it already marshals
   `{"type":"extension_ui_response","id":<id>, <exactly-one-field>}` through the
   single-writer goroutine, no correlation id, no reply awaited (conventions §6).
2. `internal/fakepi/`: unmatched-id dropping + the scenario work of §4.8 (extend M1's
   `dialog_confirm`; add `dialog_all`, `dialog_timeout`); extend `internal/pitest`
   helpers if scenario selection needs args.
3. `internal/session/dialogs.go`: `dialogRegistry` (§4.2), claim logic, sweeps (§4.4a),
   uiState fold (§4.3); exported errors `ErrDialogAlreadyAnswered`, `ErrDialogNotFound`.
4. `internal/session/manager.go`: pump classification (§4.3), `AnswerDialog` (§4.4),
   status derivation input + new `status`-event triggers (§4.5), sweeps wired into
   `agent_end`/`agent_settled` handling, process-exit, and `CloseSession`;
   `History` gains `PendingDialog json.RawMessage` and `UIState` on its snapshot struct.
5. `internal/httpapi/dialogs.go`: the route, request decode, error mapping;
   `internal/httpapi/sse.go`: step-6 `dialog` prime. Register route in the mux.
6. `test/fixtures/extensions/confirm-gate.ts` and `dialog-demo.ts` per §4.7.
7. `web/src/api/types.ts` + `client.ts`: wire types and `answerDialog`.
8. `web/src/state/sessionStore.ts`: the three new event types + snapshot fold, plus the
   `isStreaming` derivation from `agent_start`/`agent_settled`/`agent_end` `pi` events
   with the snapshot seed (§4.9).
9. `web/src/components/`: `DialogModal.tsx`, `ToastHost.tsx`, `StatusStrip.tsx`;
   `Composer` editorText application and steer/follow-up + abort rekeyed off
   `isStreaming` (§4.9); `SessionPage` mounts; `SessionListPage` badge.
10. Tests of §7 alongside each step; proof workflow of §8 last.

## 6. Interfaces exposed to later milestones

Exactly the conventions seams, now real (no new names beyond them except where noted):

- Route `POST /api/sessions/{id}/dialogs/{dialogId}` (conventions §3, final row semantics).
- SSE event types `dialog`, `dialog_resolved`, `ui` live on the wire; connect step 6
  emits pending dialogs (conventions §4.2/§4.3) — M6's non-live history path and M7's
  acceptance run consume these as-is.
- `/history` fields `pendingDialog` and `uiState` populated (conventions §3).
- Wire status `blocked-on-dialog` derivable and emitted (conventions §3) — M6's session
  list consumes it.
- `session.Manager.AnswerDialog(id, dialogID string, res pisession.UIResolution) error`
  (conventions §7 name, signature now concrete) + exported errors
  `session.ErrDialogAlreadyAnswered`, `session.ErrDialogNotFound`.
- `pisession.Session.RespondUI(id string, res pisession.UIResolution) error` (shipped
  by M1) with `pisession.UIResolution` now `{Value *string; Confirmed *bool;
  Cancelled *bool}` — M5's deliberate amendment of M1's exported type (step 1), plus
  its new `Validate(method string) error`; the wire shape is pinned by conventions
  §3/§4.2 and unchanged.
- `test/fixtures/extensions/confirm-gate.ts` — the M7/SPEC §9.5 fixture, honoring
  optional env `CONFIRM_GATE_TIMEOUT_MS`.
- fakepi scenarios `dialog_confirm` (M1's, extended per §4.8), `dialog_all`,
  `dialog_timeout` (new) available to any later test.

## 7. Testing

Unit (`testify`, table style, `_test.go` siblings):

- `internal/pisession`: `UIResolution` marshal (exactly one field — M1's marshal tests
  updated for the `Cancelled *bool` amendment) + `Validate` matrix (per-method
  accept/reject, `cancelled:false` rejected, two-fields rejected).
- `internal/session/dialogs_test.go`: register/claim/404/409; **race test** — one
  pending dialog, 32 goroutines calling `AnswerDialog` concurrently, assert exactly one
  nil error and 31 `ErrDialogAlreadyAnswered`, exactly one `RespondUI` write and one
  `dialog_resolved` broadcast; sweep-on-`agent_end` resolves as `{cancelled:true}`,
  sends exactly one `RespondUI {cancelled:true}` write to pi (§4.4a), and produces
  `dialog_resolved` + `status`; resolved-ring eviction → 404; uiState fold
  incl. clear semantics for `setStatus`/`setWidget`; status derivation:
  pending-dialog masks streaming, resolution restores `streaming` (mid-run) or `idle`.
- `internal/httpapi`: route decode/error mapping (400/404/409 bodies per conventions
  §2), `{"resolved":true}` happy path.
- Frontend (M3's runner): reducer cases — `dialog` sets pending (dedup on re-emit),
  `dialog_resolved` clears, `ui` folds each method (incl. clears and `editorText` seq),
  snapshot fold of `pendingDialog`/`uiState` equals live-event fold (replay-equals-render);
  `isStreaming` set by `agent_start`, cleared by `agent_settled` (and by `agent_end`
  fallback), seeded from a snapshot whose status is `"streaming"`, and **not** touched
  by `status` events — a `blocked-on-dialog` status arriving mid-run leaves
  `isStreaming: true` so the composer keeps offering steer/follow-up (§4.9).

Integration (fakepi; no network/LLM, conventions §9):

- `dialog_confirm`: create session via REST; SSE client observes `dialog` event (no
  `id:` line) and `status: blocked-on-dialog`; `GET /sessions` shows it; answer
  `{"confirmed":true}` → 200, then `dialog_resolved` + `status` on SSE, and the
  scenario's echo entry proves `confirmed=true` reached fakepi's stdin; second POST →
  409 `dialog_already_answered`.
- Two SSE clients: both receive `dialog`; answer once; both receive the same
  `dialog_resolved`.
- Reconnect-while-pending: open a *new* SSE connection (with `Last-Event-ID`) while the
  dialog is pending → replayed entries, then `status: blocked-on-dialog`, then the
  `dialog` re-emit (connect step 6); `/history` shows non-null `pendingDialog`.
- `dialog_all`: all four dialog methods answered in sequence (incl. `cancelled` on one
  to prove the cancel path); all five `ui` events observed verbatim; afterwards
  `/history.uiState` = `{statuses:{demo-ish...}, widgets:{...}, title:...}`.
- `dialog_timeout`: never answer; after fakepi's run ends, assert `dialog_resolved`
  with `{"cancelled":true}` and status recovery, and that a subsequent answer gets 409.
- Close-with-pending: `dialog_confirm`, then `POST /close` → `dialog_resolved` sweep
  observed before the `closed` status.

Real-pi gated (`pitest.RequireRealPi(t)`, env `GIBSON_TEST_REAL_PI=1`): spawn a session
type whose `extra_args` load `confirm-gate.ts`; prompt for a trivial `bash` tool use;
assert an `extension_ui_request` `confirm` arrives, answer `{"confirmed":true}` via the
full Manager path, assert the tool executes and the run completes.

## 8. Agent-verified proof workflow

Real pi + browser automation, per MILESTONES M5.
`REPO=~/Code/github.com/jmcampanini/gibson/main`;
`WS=$REPO/.sandbox/m5/ws` (`.sandbox/` per house temp-storage rule, matching the other
milestones' proofs).

1. **Build**: `cd $REPO/web && npm ci && cd $REPO && make build` — expect the web
   build and canonical `build/gibson` artifact build to exit 0.
2. **Scratch workspace**:
   ```sh
   mkdir -p $WS/proj/main && cd $WS/proj/main && git init -b main
   printf '.gibson/\n' > .gitignore
   cat > gibson.toml <<EOF
   [server]
   port = 7411
   [sessions.gate]
   description = "confirm-gated tools"
   extra_args = ["-e", "$REPO/test/fixtures/extensions/confirm-gate.ts"]
   [sessions.demo]
   description = "dialog demo"
   extra_args = ["-e", "$REPO/test/fixtures/extensions/dialog-demo.ts"]
   EOF
   git add -A && git commit -m init
   ```
3. **Serve**: from `$WS/proj/main`, run `$REPO/build/gibson serve` in the background;
   `curl -s localhost:7411/api/health` → `{"ok":true,...}`.
4. **All four dialogs + surfaces** (browser automation, client A at
   `http://localhost:7411`): create a session — type `demo`, first message
   `Reply with the single word READY.`; watch the reply stream. Then, asserting each:
   (a) send `/demo-select` — **select** modal "Pick a color" with three option
   buttons — click `Green`; toast `select: Green`; (b) send `/demo-confirm` —
   **confirm** modal with message text — click Confirm; toast `confirm: true`;
   (c) send `/demo-input` — **input** modal with placeholder — type `gibson`, submit;
   toast `input: gibson`; (d) send `/demo-editor` — **editor** modal prefilled
   `line 1\nline 2` — append a line, submit; toast `editor: 3 lines`. Answer each
   modal promptly: pi withholds each command's `prompt` response until its handler —
   and therefore its one dialog — completes (§4.1.8), so answering inside the 30s
   correlation window keeps every send a clean 200. Send `/demo-surfaces`:
   (e) status strip shows chip `demo: done: Green/true/gibson` and the widget block
   `--- dialog-demo ---`; page/document title contains `gibson dialog-demo`;
   (f) composer draft now reads `prefilled by dialog-demo` (set_editor_text). Send
   `/demo-clear`; assert chip and widget disappear (clear semantics).
5. **Blocked visibility**: send `/demo-select` again and do **not** answer.
   **Expected and asserted around**: after ~30s the triggering `POST /message` fails
   with 502 `pi_error` (§4.1.8 — the withheld `prompt` response outlives the pinned
   correlation timeout) and the composer shows the inline error; this is benign — the
   handler keeps running and the dialog stays pending and answerable. Then:
   `curl -s localhost:7411/api/sessions` → this session's `status` is
   `"blocked-on-dialog"`; the session list page shows the loud "needs input" badge.
   `curl -s localhost:7411/api/sessions/<id>/history | jq .pendingDialog.method` →
   `"select"`.
6. **Broadcast + first-answer-wins**: open client B (second browser context) on the
   same session — the select modal is already up in B (snapshot/prime path). Read
   `dialogId` from the history call above, then race by REST:
   `curl -X POST .../dialogs/<dialogId> -d '{"value":"Red"}'` → `{"resolved":true}`;
   repeat the same POST → HTTP 409 body
   `{"error":{"code":"dialog_already_answered",...}}`. Assert **both** A's and B's
   modals closed without any browser click. Then send `/demo-confirm` and
   `/demo-input` from A, answering each modal from B only; assert A's modal closes
   each time and A shows the outcome toasts.
7. **Gate before a tool** (SPEC §9.5.4 shape): create a session — type `gate`, message
   `Run 'ls' with the bash tool and tell me one filename.` When the confirm modal
   `Run bash?` appears, assert no tool output has rendered yet; click Confirm; assert
   the tool card runs and the assistant answer streams to completion.
8. **Timeout auto-resolve**: stop the server; relaunch with the env set:
   `CONFIRM_GATE_TIMEOUT_MS=8000 $REPO/build/gibson serve`. New `gate` session, same
   tool prompt. When the confirm modal appears, assert it renders a countdown from ~8s.
   Do not answer. Assert: countdown reaches 0 with the "agent continues with the
   default" hint; the agent proceeds without the tool (gate default `false` blocks it,
   a "Blocked by user" toast/notify appears) and finishes; at run end the modal closes
   by itself (`dialog_resolved {"cancelled":true}` sweep) and status returns to `idle`
   — with no further clicks anywhere.
9. **Hygiene**: `go test ./...` from the repo root passes with no network and no
   `GIBSON_TEST_REAL_PI` (fakepi covers all dialog paths); `git -C $WS/proj/main
   status --porcelain` shows no `.gibson/` noise.

## 9. Success criteria checklist

- [ ] `select`, `confirm`, `input`, `editor` requests each render as a modal and block
      the agent until answered from the browser (SPEC §6.4.1, §8.2; proof 4, 7).
- [ ] `notify` renders as typed toasts; `setStatus`/`setWidget`/`setTitle` render in
      the status strip with correct clear semantics; `set_editor_text` prefills the
      composer; none of these expect a response (SPEC §6.4.2, §8.2; proof 4).
- [ ] Answering posts `extension_ui_response` with pi's request uuid over stdin via the
      single-writer path; the block releases and the run proceeds (SPEC §6.4.1; proof 7).
- [ ] Dialogs broadcast to all connected clients; the first answer wins; every client
      receives `dialog_resolved` and open modals close everywhere (SPEC §7.3.1–§7.3.2;
      proof 6).
- [ ] A second answer receives `409 dialog_already_answered`; concurrent answers
      resolve to exactly one winner (SPEC §7.1 dialogs row, conventions §2/§3; proof 6,
      race unit test).
- [ ] A session with an unanswered dialog reports wire status `blocked-on-dialog` in
      `GET /api/sessions` and is visually loud in the list (SPEC §7.1, §10.3; proof 5).
- [ ] `/history` returns `pendingDialog` + `uiState`; SSE connect re-emits `status`
      then the pending `dialog` after replay, so both snapshot-first and pure-SSE
      clients recover an actionable modal (SPEC §7.2.1, §7.3.3, conventions §3/§4.3;
      proof 5–6, reconnect integration test).
- [ ] Timeout dialogs render a client-side countdown only; gibson runs no timers; pi's
      silent auto-resolve is reconciled by the run-end sweep as
      `dialog_resolved {"cancelled":true}` and status recovers (SPEC §6.4.3; proof 8,
      `dialog_timeout` test).
- [ ] Pending dialogs are swept with a broadcast resolution on process exit and close
      — no client is ever left with a modal for a dead process (SPEC §10.3 spirit;
      close-with-pending test).
- [ ] `confirm-gate.ts` exists at the pinned path and gates a real pi tool call
      end-to-end (conventions §9, SPEC §9.5.1/§9.5.4 precursors; proof 7, gated test).
- [ ] All automated Go tests pass with fakepi only — no LLM, no network (conventions
      §9; proof 9).

## 10. Explicitly out of scope

- Cross-checkout session list, close/reopen UX, resume-on-demand, orphan cleanup,
  non-live history rendering — M6 (dialog events/status are ready for it).
- SPEC §9.5's full seven-step acceptance run, multi-device `bind`, slow-client
  backpressure verification, `.gibson/` self-containment audit — M7 (M7 reuses
  `confirm-gate.ts` unchanged).
- `ui.custom()` rendering — permanent accepted degradation (SPEC §6.4.4, §10.4);
  gibson does nothing with it (no request ever arrives; pi returns `undefined`
  agent-side).
- Gibson-side dialog timeout timers or persistence of dialogs/uiState across process
  restarts — deliberately none (SPEC §6.4.3; dialogs are process-scoped).
- Per-`customType` renderers, slash-command palette (`get_commands`), images in
  prompts — post-v1 / other milestones per SPEC §1.2 and M4.
