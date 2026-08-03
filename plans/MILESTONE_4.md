# MILESTONE_4 — Full conversation rendering

Status: **provisional**. This forecast requires the plan-gate review in
[PROCESS.md](PROCESS.md) before it becomes the active milestone contract.

Conforms to [MILESTONE_CONVENTIONS.md](MILESTONE_CONVENTIONS.md) (cited below as CONV §n). SPEC.md is
normative (cited as SPEC §n). pi RPC facts below are taken from the installed pi docs:
`~/.local/lib/node_modules/@earendil-works/pi-coding-agent/docs/rpc.md` (cited as rpc.md)
and `docs/session-format.md` (cited as session-format.md), pi v0.82.1.

## 1. Goal & capability

**You can now:** read real working sessions comfortably — tool calls as live collapsible
cards, thinking collapsed by default, context meter, steer vs queued follow-up sends.
(MILESTONES.md M4.)

This milestone completes the SPEC §8.2 chat view. It is almost entirely frontend work plus
test infrastructure; the server surface it consumes is already pinned by CONV §3–§4.

## 2. Preconditions

**M0 is complete.** M4 starts from the current implementation and consumes later
milestone contracts through the shared seams in CONV §3, §4, §7, and §8.

- **M1:** `internal/pisession` with the CONV §7 `pisession.Session` interface — M4 relies
  specifically on `GetSessionStats()` and `Prompt(msg, behavior)` existing with those
  signatures; `internal/store`; base `internal/fakepi` + `internal/pitest`
  (`pitest.BuildFakePi(t)`, `FAKEPI_SCENARIO` env, real v3 session JSONL output) and
  `internal/testws` (`testws.New(t)`).
- **M2:** the REST + SSE surface of CONV §3/§4: `POST /api/sessions`,
  `GET /api/sessions/{id}/history`, `POST /api/sessions/{id}/message` accepting
  `{"message","behavior"?:"steer"|"followUp"}`, `POST /api/sessions/{id}/abort`,
  `GET /api/sessions/{id}/events?since=` with the CONV §4 envelope
  (`entry` / `pi` / `status` / `reset` are the types M4 consumes), connect algorithm,
  heartbeats, bounded buffers. The durable `entry` lane is produced by gibson's
  `get_entries` entry-feed sync (CONV §4.2), while pi events ride the `pi` lane verbatim
  as `{"type":"pi","data":<pi event verbatim>}` — M4 depends on `message_start`,
  `message_update`, `message_end`, `tool_execution_start/update/end`, `agent_start`,
  `agent_end`, `agent_settled`, `queue_update` being forwarded verbatim (CONV §4.2 table).
  - **`GET /api/sessions/{id}/stats`** is in the CONV §3 route table and M2's scope is
    "REST surface"; however MILESTONES.md's M2 paragraph does not name it. M4 *requires*
    this route (context meter). Implementation step 1 verifies it exists and implements it
    if M2's plan deferred it (spec below, §5 step 1). Either way the 200 shape is the
    pinned one (CONV §3): `200 {"stats": <pi get_session_stats data verbatim>}`, with
    `pi_error` (502) if the pi command fails (CONV §2). CONV pins nothing for non-live
    sessions; **M4 decision, flagged as a CONV open question per CONV §10:** `conflict`
    (409) when the session is not live — a stopped/closed session has no pi process to
    ask, and the verbatim-from-live-pi contract precludes serving stats without one.
- **M3:** the SPA skeleton per CONV §8: `src/api/types.ts`, `src/api/client.ts`,
  `src/api/stream.ts` (`SessionStream` with reconnect + `reset` handling),
  `src/state/sessionStore.ts` with `sessionReducer(state, StreamEvent)` folding the
  history snapshot (entries replayed as synthetic `entry` events) and live SSE through one
  code path; `SessionListPage` (minimal), `LaunchFlow`, `SessionPage`, `MessageList`,
  `Composer` (plain send + abort button), markdown rendering of user/assistant text,
  streaming text deltas. M3 explicitly allowed tool calls and thinking to render as raw
  placeholder rows — M4 replaces those placeholders.

Nothing in M4 requires dialogs (M5), the cross-checkout session list, resume, or
non-live-session history rendering (M6).

## 3. Deliverables

Go (small):

- `internal/httpapi/` — `GET /api/sessions/{id}/stats` handler **iff** M2 deferred it,
  plus its `_test.go`.
- `internal/fakepi/scenarios/` — four new scenarios: `tool_stream`, `tool_error`,
  `custom_message`, `steer_redirect` (details §7). The scenario *mechanism* exists from
  M1/M2; M4 adds scenario data + any small event-scripting support they need (e.g. fakepi
  reacting to a mid-stream `prompt` with `streamingBehavior`).
- Integration tests exercising the above through httpapi (§7).

Frontend (the bulk):

- `web/src/api/types.ts` — TS types added: `SessionStats` (mirror of rpc.md
  `get_session_stats` data, all pi-owned fields optional/loose), loose discriminated
  unions for the pi events and `assistantMessageEvent` deltas the reducer switches on,
  session-entry shapes (`message`, `custom_message`, catch-all).
- `web/src/api/client.ts` — `getSessionStats(id: string): Promise<SessionStats>`.
- `web/src/state/sessionStore.ts` — reducer extended to the full event taxonomy:
  in-flight assistant message (cumulative replacement), tool-execution map, queue state,
  `statsEpoch` trigger counter, entry dedup, defensive finalization (§4).
- Components (names pinned by CONV §8; paths follow M3's established `web/src/`
  component layout): `ThinkingBlock`, `ToolCallCard`, `CustomMessageCard` (new);
  `MessageCard`, `StreamingText`, `MessageList`, `Composer` (extended); `ContextMeter`
  (new, owns the stats fetch).
- Frontend unit tests for the reducer and render-model selector (§7).

Fixture:

- `test/fixtures/extensions/custom-note.ts` — M4-owned pi extension fixture that appends
  a `custom_message` entry on command, for the real-pi proof (§8). (CONV §9 names
  `confirm-gate.ts` as the notable fixture; the directory is shared, this adds a second.)

## 4. Design & rationale

### 4.1 One render model, derived from entries + in-flight state

The chat view renders a **render model** derived (pure selector in `sessionStore.ts`)
from two reducer regions:

1. `entries` — finalized pi session entries in append order, deduped by id. Durable truth
   (CONV §4.3).
2. The **in-flight region** — ephemeral state keyed by `contentIndex`/`toolCallId`
   (CONV §8), populated only from `pi` events, always superseded by finalized entries.

Because the model derives from `entries` first, replay-equals-render (SPEC §9.3.b) holds
by construction, and M6's non-live history rendering later works by feeding history alone.

Reducer state shape — the full merged state. M3's fields survive with their M3 names and
types verbatim (M4 still needs them: §4.5's fetch-on-reconnect keys off `connection`,
§4.6's inline error recovery off `sendError`); only the marked fields are new:

```ts
interface SessionState {
  // unchanged from M3 (names/types verbatim — M3 §4.1):
  session: SessionSummary | null;
  status: WireStatus;                    // from status events + history snapshot
  entries: SessionEntry[];               // append order
  entryIds: Record<string, true>;        // dedup guard
  inFlight: AgentMessage | null;         // cumulative partial — replaced wholesale, never merged
  lastActivityAt: string | null;
  connection: ConnectionState;
  sendError: string | null;              // inline composer error
  // new in M4:
  isStreaming: boolean;                  // pi agent_start → agent_settled (§4.6)
  toolExecs: Map<string, ToolExecState>; // keyed by toolCallId
  queue: { steering: string[]; followUp: string[] };  // latest queue_update verbatim
  statsEpoch: number;                    // bumped when stats may have changed (§4.5)
}

interface ToolExecState {
  toolCallId: string; toolName: string; args: unknown;
  partialResult?: unknown;  // CUMULATIVE — replaced on every update
  result?: unknown; isError?: boolean;
  phase: "running" | "done" | "interrupted";
}
```

`isStreaming` is set by `pi` event `agent_start` and cleared by `agent_settled`
(fallback: `agent_end`, if settled never arrives), seeded on hydrate as
`status === "streaming"` — the client streaming-state seam pinned in CONV §8. It exists separately from the wire `status` because M5's
`blocked-on-dialog` masks `streaming` on the wire while a run is still in flight —
MILESTONE_5 preconditions on this flag and keys steer-vs-follow-up off it (§4.6, §6).

### 4.2 `message_update`: replace with the cumulative snapshot, never append

Per rpc.md, every `message_update` event carries **both** the full partial
`AssistantMessage` (top-level `message` field) and an `assistantMessageEvent` delta
(`text_start/delta/end`, `thinking_start/delta/end`, `toolcall_start/delta/end`, `start`,
`done`, `error` — the delta itself also embeds a cumulative `partial`). The reducer
**stores `event.message` wholesale** and does not accumulate deltas:

```ts
case "message_update":
  // ev.message is pi's cumulative partial AssistantMessage. REPLACE — never append.
  // This is what makes mid-stream reconnect correct with zero delta buffering:
  // the first update after (re)attach repaints complete partial state (CONV §4.3).
  return { ...s, inFlight: ev.message };
```

Rationale: CONV §4.3 makes missed deltas deliberately lossy and mandates
render-by-replacement; using the snapshot means a reconnecting client is pixel-identical
to an always-connected one after one event. The `assistantMessageEvent.type` is consulted
only for cosmetics (which block shows the "hot" cursor; `error` with reason
`aborted`/`error` shows an inline notice). Unknown delta types are ignored.

Rendering cost is bounded by memoization: finalized entries render through
`MessageCard` wrapped in `React.memo` keyed by entry id, so per-token re-render is
confined to the single in-flight card.

The in-flight message renders as the last item in `MessageList` using the *same*
`MessageCard` (streaming flag set). Its content blocks render in array order:
`text` → `StreamingText` (markdown), `thinking` → `ThinkingBlock`, `toolCall` →
`ToolCallCard`.

### 4.3 Tool cards: correlation, cumulative partials, precedence

A `ToolCallCard` is owned by the assistant message's `toolCall` content block and
correlates three sources by `toolCallId`:

1. The `toolCall` block itself (name + arguments; while `toolcall_delta` is still
   streaming, arguments may be incomplete JSON — the card shows a "preparing arguments"
   state until `toolcall_end`, whose delta includes the full `toolCall` object, or until
   `tool_execution_start`, which carries complete `args` — both per rpc.md).
2. `toolExecs[toolCallId]` — from `tool_execution_start` (phase `running`, args),
   `tool_execution_update` (**`partialResult` is cumulative — replace, never append**,
   rpc.md: "contains the accumulated output so far … allowing clients to simply replace
   their display"), `tool_execution_end` (`result`, `isError`, phase `done`).
3. The finalized `toolResult` message entry (`entry.message.role === "toolResult"`,
   with `content`, `isError` — session-format.md).

**Precedence: toolResult entry > tool_execution_end > tool_execution_update.** The entry
is durable; the events are ephemeral. A client that reconnects after missing
`tool_execution_end` still finalizes the card because the replayed `toolResult` entry
wins. Consequently `toolResult` entries are **not** rendered as standalone rows in the
message list — they fold into the owning card (the render-model selector indexes them by
`toolCallId`).

Card chrome: header always visible — tool name, one-line argument summary, phase badge
(spinner / done / red error). Body **collapsed by default** (SPEC §8.2 "collapsible"),
toggled per card; while `running` the header additionally shows the last non-empty line
of the cumulative `partialResult` text so liveness is visible without expanding. Error
state (`isError` from either source, or assistant `stopReason:"error"`): red header,
body auto-expanded on finalization.

Defensive finalization: on `agent_end`/`agent_settled`, any `toolExecs` entry still
`running` with no matching `toolResult` entry flips to `interrupted` (renders as a muted
"interrupted" badge), and `inFlight` is cleared if the finalized entry never
arrived (abort-before-content edge). Normal clearing of `inFlight` happens when
the corresponding assistant `message` entry is appended — aborted messages do arrive as
entries with `stopReason:"aborted"` (SPEC §6.2), rendered with an "aborted" badge.

### 4.4 Thinking blocks

`thinking` content blocks render as `ThinkingBlock`: **collapsed by default**
(SPEC §8.2), expandable during and after streaming. Collapsed header shows a "Thinking…"
label with a live activity indicator plus a character count while `thinking_delta`s are
arriving (the count comes free from the cumulative snapshot). Body is
whitespace-preserving plain text, not markdown (thinking is not authored as markdown).

### 4.5 Context meter: event-driven refresh, not polling

`ContextMeter` displays `stats.contextUsage` (`tokens` / `contextWindow` / `percent`)
from `GET /api/sessions/{id}/stats` (verbatim `get_session_stats` data, rpc.md).

**Decision: event-driven refetch, debounced — no polling.** Rationale:

- `contextUsage` only changes when new assistant usage arrives or compaction runs. Every
  such change is announced by events the client already receives over SSE, so polling
  adds latency-vs-load tradeoffs for zero information.
- Each fetch is a live RPC round trip into the pi process (rpc.md `get_session_stats`);
  with keep-alive processes (SPEC §5.2) and many idle sessions, background polling from
  every open tab is pointless chatter against the single stdin writer (CONV §6).

Mechanism: the reducer bumps `statsEpoch` on (a) an appended `message` entry with
`message.role === "assistant"` (new usage), (b) `pi` event `agent_settled`, (c) `pi`
event `compaction_end`. `ContextMeter` effects on a 500 ms-debounced `statsEpoch`, plus
fetches once on mount when `status` is live and once on every SSE (re)connect (covers
changes missed while disconnected). Putting the trigger policy in the pure reducer makes
it unit-testable; the fetched stats live in `ContextMeter` local state, not the reducer —
they are not part of replay-equals-render.

Display edge cases (rpc.md): `contextUsage` may be absent (no model/context window), and
`tokens`/`percent` are `null` immediately after compaction until fresh usage arrives —
render an em-dash/indeterminate meter, never `NaN`. Meter hidden when `status` is
`stopped`/`closed` (no process to ask — the verbatim-from-live-pi route precludes stats
for non-live sessions).

### 4.6 Composer: steer vs queued follow-up

pi **errors** on a `prompt` sent mid-stream without `streamingBehavior` (rpc.md), so the
choice must be explicit in the UI (SPEC §8.2, §6.2):

- `!isStreaming`: single **Send** → `POST /message {"message"}` (no behavior).
- `isStreaming`: primary **Steer** → `{"behavior":"steer"}` (delivered after
  the current assistant turn finishes its tool calls, before the next LLM call — rpc.md);
  secondary **Queue follow-up** → `{"behavior":"followUp"}` (delivered only when the
  agent stops). Enter triggers the primary action; the abort button (M3) stays adjacent.
- The presentation keys off the reducer's `isStreaming` flag (§4.1), **not** the wire
  `status`: the flag tracks pi's `agent_start`/`agent_settled` directly, so it stays
  correct in M5 when a pending tool-gate dialog masks the wire status as
  `blocked-on-dialog` mid-run — a plain Send there would be rejected by pi.
  **Race handling:** if the agent starts streaming between the client's last event and a
  plain send, pi rejects the prompt and gibson surfaces `pi_error` (502, CONV §2). The
  composer must keep the draft text, show the error inline, and re-present the Steer /
  Queue follow-up buttons (by then the forwarded `agent_start` will have flipped the UI
  anyway). No toast — `ToastHost` is M5.
- Queued sends are made visible via `queue_update` `pi` events (rpc.md: emitted whenever
  the pending steering/follow-up queue changes, with `steering` and `followUp` string
  arrays): the reducer stores the latest arrays verbatim and the composer renders them as
  "queued" chips above the input; pi's next `queue_update` clears them when delivered.

### 4.7 `custom_message` fallback card — and the generic-fallback rule

`CustomMessageCard` is SPEC §8.2's "generic fallback card": a labeled card with a
formatted JSON/text body. It renders:

- **`custom_message` entries** (session-format.md `CustomMessageEntry`): label chip =
  `customType` (e.g. `subagent_result`); body = `content` rendered as markdown when it is
  a string, or its `TextContent` blocks as markdown / `ImageContent` blocks as inline
  data-URI images when it is an array; optional `details` as a collapsed pretty-printed
  JSON section. Entries with `display: false` are **hidden** (session-format.md
  semantics). Per-`customType` renderers are post-v1 (SPEC §8.2).
- **Every other non-`message`, non-`custom_message` entry type** (`custom`,
  `model_change`, `thinking_level_change`, `compaction`, `branch_summary`, `label`,
  `session_info`, and anything future) in degraded mode: label = the entry `type`, body =
  the entry pretty-printed as JSON, collapsed by default. Same for unknown
  `entry.message.role` values inside `message` entries. This reuses the pinned component
  instead of inventing a new one and makes the transcript total: nothing pi appends can
  render as a blank hole (protocol-churn guard, SPEC §10.5).

### 4.8 Robustness rules (reducer-wide)

- Unknown `pi` event `data.type` → state unchanged (SPEC §10.5; retries,
  `bash_execution_update`, summarization events all fall here in M4; compaction events
  too, **except `compaction_end`**, which bumps `statsEpoch` per §4.5(c) and renders
  nothing).
- Duplicate `entry` ids → dropped via `entryIds` (server already dedups on reconnect,
  CONV §4.3; the reducer guard makes history-then-tail folding unconditionally safe).
- `dialog` / `dialog_resolved` / `ui` StreamEvents → ignored in M4 (M5 adds their cases
  to this same reducer switch).
- `reset` → unchanged M3 behavior (refetch history, reopen stream).

### 4.9 `data-testid` additions

Extends M3 §4.8's append-only testid contract; the §8 proof asserts against these:

| testid | On |
|---|---|
| `thinking-block` | each `ThinkingBlock`; carries `data-expanded="true"/"false"` |
| `tool-card-{toolCallId}` | each `ToolCallCard` |
| `tool-card-status` | the phase badge inside a card — renders `preparing`/`running`/`done`/`error`/`interrupted` verbatim |
| `custom-card-{entryId}` | each `CustomMessageCard`, including generic-fallback rows (§4.7) |
| `context-meter` | `ContextMeter`; absent from the DOM when hidden (§4.5) |
| `composer-steer`, `composer-followup` | the `isStreaming` composer actions (§4.6) |
| `queued-chip` | each queued-message chip (§4.6) |

## 5. Implementation steps

1. **Stats route (conditional).** Check `internal/httpapi` for the
   `GET /api/sessions/{id}/stats` handler. If M2 delivered it, skip. Otherwise: CONV §7's
   `session.Manager` exposes neither a stats method nor the underlying
   `pisession.Session`, so M4 adds
   **`Manager.Stats(id string) (json.RawMessage, error)`** mirroring the Send/Abort
   delegation pattern (a CONV §7 addition, flagged in §6 per CONV §10) — it resolves the
   session, errors if not live (handler maps it to 409 `conflict`, the §2 decision), and
   otherwise delegates to `pisession.Session.GetSessionStats()` (CONV §7). Add the route
   to the existing mux registration; the handler calls `Manager.Stats` and writes
   `{"stats": <raw>}` with the data forwarded as `json.RawMessage` (CONV §2 verbatim
   rule); pi failure → 502 `pi_error`. File: the httpapi handler file layout M2
   established, plus `_test.go` sibling.
2. **fakepi scenarios** in `internal/fakepi/scenarios/` (mechanism from M1/M2; extend the
   scenario scripting only as far as these need). Carry from M1 consolidation: before
   authoring these scenarios, restructure fakepi's flat `Step` fields into typed
   per-step payload structs — M4 needs overlapping tool calls with distinct
   toolCallIds, thinking deltas, custom_message entries, and queue/compaction events
   that the current six flat fields cannot express:
   - `tool_stream`: on `prompt` → `agent_start`, `message_start`, thinking deltas
     (`thinking_start/delta/end` with cumulative `message`), text deltas, a
     `toolcall_start/delta/end` sequence, `message_end` (appends the assistant message
     with a `toolCall` block, id `e-a1`, to the JSONL — surfaced by gibson's entry
     sync as an `entry` event), then `tool_execution_start`
     (toolCallId `tc-1`, toolName `bash`), **three `tool_execution_update` events whose
     `partialResult.content[0].text` values are strictly growing prefixes**
     (`"line1\n"`, `"line1\nline2\n"`, `"line1\nline2\nline3\n"`),
     `tool_execution_end` (`isError:false`, appending the toolResult entry to the
     JSONL for the entry sync), a short final assistant text turn, `agent_end`,
     `agent_settled`. Writes all entries to the real session JSONL (CONV §9).
   - `tool_error`: same shape, `tool_execution_end` with `isError:true` and a toolResult
     entry with `isError:true`.
   - `custom_message`: a normal text turn that also appends and emits an
     `entry_appended` (legitimate here — extension-appended custom entries are the one
     case where pi emits it) for
     `{"type":"custom_message","customType":"m4-note","content":
     "hello from fakepi","display":true}` (session-format.md shape) plus one with
     `display:false` (must not render).
   - `steer_redirect`: on `prompt` → begin a slow text stream (N deltas with sleeps);
     if a `prompt` with `streamingBehavior:"steer"` (or a `steer` command) arrives
     mid-stream, respond `success:true`, emit a `queue_update` with the message in
     `steering`, finish the current message, append the steer text as a user `message`
     entry, then stream a second assistant message containing the literal `REDIRECTED`,
     then `queue_update` with empty arrays, `agent_end`, `agent_settled`. A mid-stream
     `prompt` *without* `streamingBehavior` → `success:false` (mirrors rpc.md).
3. **Go integration tests** for step 2 through the full HTTP surface (see §7).
4. **`web/src/api/types.ts`**: add `SessionStats` (with
   `contextUsage?: {tokens: number|null; contextWindow: number; percent: number|null}`),
   pi event unions (`PiEvent` discriminated on `type`, with `unknown` catch-all),
   `AssistantMessageEvent` delta union, entry/content-block/message-role types per
   session-format.md (all tolerant: extra fields allowed, unknown variants preserved).
5. **`web/src/api/client.ts`**: `getSessionStats(id)` → `GET /api/sessions/{id}/stats`,
   unwrapping `{stats}`; `ApiError` on the CONV §2 envelope as established in M3.
6. **`web/src/state/sessionStore.ts`**: extend `sessionReducer` per §4.1–§4.3, §4.5,
   §4.8 (state shape, `message_update` replacement, `tool_execution_*` handling,
   `queue_update`, `isStreaming` from `agent_start`/`agent_settled` (§4.1),
   `statsEpoch`, entry dedup, defensive finalization on
   `agent_end`/`agent_settled`). Add the pure selector `deriveRenderModel(state)`
   returning the ordered card list: user rows, assistant rows (content blocks in order,
   tool cards joined by `toolCallId` with the §4.3 precedence), custom-message cards,
   generic-fallback rows, then the in-flight card; `toolResult` entries folded, not
   listed.
7. **Components** (in M3's `web/src/` component layout): new `ThinkingBlock` (§4.4),
   `ToolCallCard` (§4.3), `CustomMessageCard` (§4.7); extend `MessageCard` to render
   block lists via those components and memoize by entry id; extend `StreamingText` for
   in-flight markdown; rewire `MessageList` onto `deriveRenderModel` keeping M3's
   stick-to-bottom autoscroll (suspended while the user has scrolled up).
8. **`ContextMeter`** (§4.5): percent bar + `tokens/contextWindow` caption; fetch on
   mount (live only), on debounced `statsEpoch`, on stream (re)connect; em-dash for
   absent/null usage; hidden for non-live status. Mount it in `SessionPage`'s header
   area.
9. **`Composer`** (§4.6): status-dependent actions, `behavior` wiring, inline `pi_error`
   recovery keeping the draft, queued chips from `queue`.
10. **Fixture** `test/fixtures/extensions/custom-note.ts` (sketch — the design point is
    "append a `custom_message` entry on demand from a browser-typed command"):

    ```ts
    import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
    export default function (pi: ExtensionAPI) {
      pi.registerCommand("note", {
        description: "Append a custom_message entry (M4 fixture)",
        handler: async (args) => {
          // Appends a custom_message entry (customType "m4-note") and, being idle,
          // triggers a turn so the entry_appended is followed by a short response.
          pi.sendMessage(
            { customType: "m4-note", content: args || "noted", display: true },
            { triggerTurn: true },
          );
        },
      });
    }
    ```

    Typing `/note …` in the composer reaches it because pi executes extension commands
    sent via `prompt` immediately, even mid-stream (rpc.md).
11. **Frontend unit tests** (§7), then `make build` produces the canonical
    `build/gibson` artifact and `go test ./...` is green.

## 6. Interfaces exposed to later milestones

- **Route:** `GET /api/sessions/{id}/stats` live and conforming to CONV §3 (whether
  implemented in M2 or here) — consumed by M7's acceptance sweep.
- **Go seam (iff the stats route is implemented here):**
  `session.Manager.Stats(id string) (json.RawMessage, error)` — a CONV §7 addition
  flagged as an open question per CONV §10; delegates to
  `pisession.Session.GetSessionStats()`.
- **`web/src/api/client.ts`:** `getSessionStats(id: string): Promise<SessionStats>`.
- **`sessionReducer` / `deriveRenderModel`:** now cover StreamEvent types
  `entry` | `pi` | `status` | `reset` across the full pi event taxonomy. M5 adds
  `dialog` | `dialog_resolved` | `ui` cases to the same switch (CONV §8's single fold
  path); M6 reuses `deriveRenderModel` fed by history alone for non-live sessions
  (guaranteed by §4.1's entries-first derivation).
- **Reducer `isStreaming` flag** (§4.1): maintained from pi `agent_start`/`agent_settled`
  (`agent_end` fallback) — the seam MILESTONE_5 §2 preconditions on; M5 keeps
  steer-vs-follow-up keyed off it while `blocked-on-dialog` masks the wire status.
- **`data-testid` additions** (§4.9): append-only extension of M3 §4.8's contract, used
  by M5–M7 proof workflows.
- **Components:** `ToolCallCard`, `ThinkingBlock`, `CustomMessageCard`, `ContextMeter`
  under CONV §8's pinned names, reusable by M5–M7 surfaces.
- **fakepi scenarios:** `tool_stream`, `tool_error`, `custom_message`, `steer_redirect`
  (selected via `FAKEPI_SCENARIO`, CONV §9) available to later milestones' tests.
- **Fixture:** `test/fixtures/extensions/custom-note.ts` (referenced from test
  `gibson.toml` via `extra_args`, like `confirm-gate.ts` per CONV §9).

## 7. Testing

Go (testify, `_test.go` next to code, fakepi only — CONV §9):

- **Stats handler** (only if implemented here): live session via fakepi → 200 with
  `stats` bytes byte-identical to fakepi's `get_session_stats` data (verbatim guard);
  stopped session → 409 `conflict`; unknown id → 404.
- **`tool_stream` end-to-end** (`internal/httpapi`): create session with
  `FAKEPI_SCENARIO=tool_stream` in a `testws.New(t)` workspace; attach SSE; assert the
  ordered arrival of `pi` events `tool_execution_start` → 3× `tool_execution_update` →
  `tool_execution_end` with each `partialResult` a strict prefix-growth of the last
  (cumulative contract), interleaved `entry` events carrying assistant + toolResult
  entries with SSE `id:` set only on `entry` events (CONV §4.1); `/history` afterwards
  contains both entries.
- **`tool_error`**: `tool_execution_end.isError == true` and toolResult entry
  `message.isError == true` arrive.
- **`custom_message`**: the `custom_message` entry (with `customType`, `display`) arrives
  verbatim over both SSE `entry` and `/history`.
- **`steer_redirect`**: start prompt; while streaming, `POST /message` with
  `{"behavior":"steer"}` → 200, and the stream subsequently carries the `queue_update`,
  the user entry, and `REDIRECTED`; `POST /message` with no behavior mid-stream → 502
  `pi_error` (fakepi rejects, mirroring rpc.md).
- **Real-pi gated** (`pitest.RequireRealPi(t)`, env `GIBSON_TEST_REAL_PI=1`): one test
  driving a real tool call ("run `ls` via bash") asserting `tool_execution_*` events and
  a nonzero `contextUsage` from `/stats`.

Frontend (pure-function tests; Vitest, the runner pinned by CONV §9). Table tests over
handwritten event-sequence fixtures:

- `message_update` folding: cumulative snapshots replace; state after events 1..n equals
  state after event n alone plus prior entries (replacement law).
- Tool precedence: update-after-end ignored; toolResult entry finalizes a card that never
  saw `tool_execution_end`; `interrupted` on settle-without-result.
- **Replay-equals-render:** folding a full recorded stream event-by-event yields the same
  `deriveRenderModel` output as folding {history-snapshot entries + tail from any cut
  point} — asserted for cut points mid-text-delta and mid-tool-update (SPEC §9.3.b
  mechanism, CONV §8).
- Entry dedup; unknown pi types are no-ops; `display:false` custom_messages absent from
  the render model; generic fallback for `model_change` etc.; `statsEpoch` bumps exactly
  on assistant entries / `agent_settled` / `compaction_end`; `isStreaming` sets on
  `agent_start` and clears on `agent_settled` (and on `agent_end` as fallback).

Component-level behavior (collapse states, chips, composer buttons) is deliberately left
to the browser-automation proof (§8), per CONV §9's acceptance-proof split.

## 8. Agent-verified proof workflow

Real pi + browser automation (MILESTONES.md; CONV §9). Requires pi 0.82.0 or newer on
`$PATH` with a configured LLM provider. The 0.82 minor line is verified; later minor or major versions
are allowed with Gibson's unverified-version warning.
`REPO=~/Code/github.com/jmcampanini/gibson/main`.

1. **Build:**
   ```sh
   cd "$REPO/web" && npm ci
   cd "$REPO" && make build && go test ./...
   ```
   Expect: builds green, all tests pass with no network/LLM use.
2. **Scratch workspace** (grove-style; `.sandbox/` per house temp-storage rule):
   ```sh
   WS="$REPO/.sandbox/m4-ws/github.com/acme/demo"
   mkdir -p "$WS/main" && cd "$WS/main"
   git init -b main && printf '.gibson/\n' > .gitignore
   cat > gibson.toml <<EOF
   [server]
   port = 7411
   [sessions.tools]
   description = "Tool-exercising session"
   thinking = "high"
   [sessions.notes]
   description = "Custom-message fixture session"
   extra_args = ["-e", "$REPO/test/fixtures/extensions/custom-note.ts"]
   EOF
   git add -A && git commit -m init
   "$REPO/build/gibson" serve   # leave running (background)
   ```
   Expect: serving on 7411. `thinking = "high"` pins reasoning on
   so step 3(a)'s ThinkingBlock assertion cannot fail on a machine whose default
   thinking level is off; if the machine's default provider/model is not
   reasoning-capable, also set an explicit reasoning-capable `model` under
   `[sessions.tools]`.
3. **Tool cards + thinking, live** (browser automation against `http://localhost:7411`):
   launch a `tools` session on checkout `main` with first message: *"Using the bash tool,
   run `ls -la`, then think carefully before listing three observations about the
   output."* Assert, in order: (a) a `ThinkingBlock` (`[data-testid=thinking-block]`)
   appears **collapsed** (`data-expanded="false"`) with a live activity indicator;
   expanding it mid-stream shows growing text; (b) a `ToolCallCard`
   (`[data-testid^=tool-card-]`) appears with tool name `bash` and the command in its
   header, `tool-card-status` = `running` with spinner, and a live output tail while
   running; (c) expanding it shows output that only ever **grows or is replaced
   wholesale — never duplicates** (screenshot before/after an update); (d) the card
   finalizes (`tool-card-status` = `done`, full result) and the assistant's prose
   follows.
4. **Error state:** send *"Run `cat /nonexistent-file-m4` with the bash tool and report
   what happened."* Assert the resulting tool card shows the error state
   (`tool-card-status` = `error`, red header, `isError` styling) and the transcript
   continues normally.
5. **Context meter:** assert the meter (`[data-testid=context-meter]`) shows a nonzero
   token count and percent after step 3, and that its value strictly increases after
   step 4's turn settles — with **no**
   `/api/sessions/{id}/stats` requests firing while the session then sits idle for 60s
   (verify via browser network log: event-driven, not polled).
6. **Steer:** send *"Using the bash tool, run `sleep 2 && echo step-N` for N from 1 to
   10 — one bash command per turn, announcing each step before running it."* While the
   step turns are executing, assert the composer shows **Steer**
   (`[data-testid=composer-steer]`, primary) and **Queue follow-up**
   (`[data-testid=composer-followup]`); type *"Stop running steps. Reply with exactly
   the word REDIRECTED."* and Steer. Assert: the steer message appears as a user row,
   and — because pi delivers a steer only after the current assistant turn finishes its
   tool calls, before the next LLM call (§4.6, rpc.md), never by aborting the in-flight
   turn — `REDIRECTED` appears in a subsequent assistant message while the transcript
   holds **fewer than ten** `step-N` tool cards (the run visibly stopped early; do not
   assert mid-turn interruption).
7. **Queued follow-up:** send another long prompt; while streaming, use **Queue
   follow-up** with *"When finished, reply FOLLOWUP-DONE."* Assert a queued chip
   (`[data-testid=queued-chip]`) appears above the composer (from `queue_update`), the
   current response completes uninterrupted, then a new turn yields `FOLLOWUP-DONE` and
   the chip clears.
8. **`custom_message` fallback card:** launch a `notes` session (first message *"hi"*);
   after the reply, send `/note Hello from the fixture`. Assert a `CustomMessageCard`
   (`[data-testid^=custom-card-]`) appears labeled `m4-note` with body "Hello from the
   fixture".
9. **Second-client parity mid-stream** (regression of M3 against the new renderer):
   during step 6-style streaming, open a second tab on the same session; assert the tool
   cards/thinking/in-flight text render identically (snapshot + cursor replay + cumulative
   repaint), and the remainder streams live in both.
10. **Teardown:** stop the server; `git -C "$WS/main" status --porcelain` shows nothing
    (`.gibson/` ignored).

Steps 3–9 are the M4 acceptance; each assertion is a concrete observable an automation
agent can screenshot or read from the DOM/network log.

## 9. Success criteria checklist

- [ ] Tool calls render as collapsible cards: name + args, live **cumulative**
      `partialResult` (replace-not-append), final result, error state (SPEC §8.2;
      SPEC §6.3 cumulative contract; proof steps 3–4).
- [ ] Thinking blocks collapsed by default, expandable, live during streaming
      (SPEC §8.2; proof step 3a).
- [ ] Streaming responses render token-by-token with tool progress live, and abort/error
      outcomes are reflected (`stopReason` badges) (SPEC §9.2.b).
- [ ] Sending while streaming steers, or queues a follow-up when chosen; plain mid-stream
      sends are impossible from the UI and recover gracefully if raced (SPEC §9.2.c,
      §8.2, §6.2; proof steps 6–7).
- [ ] `custom_message` entries render via the labeled generic fallback card;
      `display:false` ones are hidden; unknown entry types never render as blank holes
      (SPEC §8.2, §10.5; proof step 8).
- [ ] Context meter sourced from `get_session_stats`, event-driven (no idle polling),
      correct on absent/null `contextUsage` (SPEC §8.2, §6.2; proof step 5).
- [ ] Mid-stream reconnect/second client repaints identical state via snapshot + cursor
      replay + cumulative partials (SPEC §9.3.b; CONV §4.3; proof step 9; frontend
      replay-equals-render tests).
- [ ] No new wire fields/event types/statuses beyond CONV §3/§4 (CONV §10).
- [ ] `go test ./...` passes offline via fakepi; real-pi tests gated behind
      `GIBSON_TEST_REAL_PI=1` (CONV §9).
- [ ] MILESTONES.md M4 proof: tools session verified live-updating and finalizing;
      mid-stream steer visibly redirects; `custom_message` renders via fallback card.

## 10. Explicitly out of scope

- Dialog modals (`select`/`confirm`/`input`/`editor`), `notify` toasts,
  `setStatus`/`setWidget`/`setTitle` strip, `DialogModal`/`ToastHost`/`StatusStrip`,
  `blocked-on-dialog` UI, `/history.uiState` and `pendingDialog` consumption, the
  `dialog`/`dialog_resolved`/`ui` StreamEvent cases — **M5**.
- Cross-checkout session list enrichment, close/reopen UI, resume-on-demand flows,
  restart resilience, non-live-session history rendering — **M6**.
- Multi-device bind, slow-client backpressure verification, docs, full acceptance —
  **M7**.
- Rendering surfaces SPEC §1.2 excludes: tree/branch navigation, fork UI, compaction
  controls, model/thinking switching, command palette (`get_commands`), bash console —
  post-v1 / never in v1. Retry/compaction pi events are deliberately ignored no-ops in
  the reducer for v1, except that `compaction_end` bumps `statsEpoch` (§4.5, §4.8) —
  no rendering.
- Per-`customType` renderers (SPEC §8.2: post-v1).
