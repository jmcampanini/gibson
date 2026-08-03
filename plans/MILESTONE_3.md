# MILESTONE_3 — Minimal browser chat

Status: **provisional**. This forecast requires the plan-gate review in
[PROCESS.md](PROCESS.md) before it becomes the active milestone contract.

Implements MILESTONES.md **M3** exactly. Normative behavior: [SPEC.md](SPEC.md).
Binding seams: [MILESTONE_CONVENTIONS.md](MILESTONE_CONVENTIONS.md) (cited below as CONV §n).
Rationale reference: [BACKGROUND.md](BACKGROUND.md). This plan adds no server code paths
beyond what prior milestones deliver; M3 is the browser layer over M2's frozen
HTTP contract.

---

## 1. Goal & capability

**You can now:** open the UI, start a session (type + checkout picker), chat with
streaming responses, and abort — from any device the bind allows.

This is the first full vertical: browser → Go → pi → browser, daily-usable. Rendering is
deliberately plain — tool calls and thinking may appear as raw placeholder rows
(MILESTONES M3; enrichment is M4).

## 2. Preconditions

**M0 is complete.** M3 starts from the current implementation and binding conventions.

**From M1 (indirect — behind the API):** `internal/pisession`, `internal/store`; the
browser never touches these.

**From M2 — the routes and contract M3 consumes (CONV §3, §4):**
- `GET /api/config/session-types` → `{"sessionTypes":[...]}`
- `GET /api/checkouts` → `{"checkouts":[{"name","path","branch","isPrimary"}]}`
- `GET /api/sessions` → `{"sessions":[SessionSummary]}`
- `POST /api/sessions` `{"type","checkout","name"?,"message"}` → 201 `{"session":SessionSummary}`,
  returned only after the first prompt is accepted (CONV §6 spawn sequence)
- `GET /api/sessions/{id}/history` → `{"session","entries","leafId","cursor","pendingDialog","uiState"}`
- `POST /api/sessions/{id}/message` `{"message","behavior"?}` → `{"session":SessionSummary}`
- `POST /api/sessions/{id}/abort` `{}` → `{"session":SessionSummary}`
- `GET /api/sessions/{id}/events?since=<entryId>` — SSE per the full CONV §4 contract:
  `{"type","data"}` envelope; SSE `id:` only on `entry` events carrying the pi entry id;
  subscribe-first connect algorithm with gapless `entry` replay and fetched-id dedup;
  connect-time priming with a `status` event (and `dialog` if pending — ignored in M3);
  `reset` + close on invalid cursor; `:hb` every 15s; 256-event buffer, slow clients
  dropped. `Last-Event-ID` takes precedence over `?since=` (CONV §4.3 step 1).
- `status` SSE events emitted on every wire-status transition, data
  `{"status","lastActivityAt"}` with the wire enum `idle | streaming | blocked-on-dialog
  | stopped | closed` (CONV §3, §4.2).
- Error envelope `{"error":{"code","message"}}` on every non-2xx (CONV §2).

**Not consumed in M3** (exist or arrive later): `POST .../dialogs/{dialogId}` (M5),
`POST .../close` (UI in M6), `GET .../stats` (M4), resume-on-demand semantics of
`POST message` to `stopped`/`closed` sessions (M6 proves them).

## 3. Deliverables

All frontend, under `web/` (paths repo-relative):

| Path | Contents |
|---|---|
| `web/src/api/types.ts` | TS mirrors of CONV §3/§4 wire types (see §6) |
| `web/src/api/client.ts` | `ApiError` + typed fetch wrappers for the routes M3 consumes |
| `web/src/api/stream.ts` | `SessionStream` — EventSource wrapper: `?since=` bootstrap, reconnect, `reset` handling |
| `web/src/state/sessionStore.ts` | `sessionReducer` (pure), `SessionState`, `SessionAction`, `useSessionState(sessionId)` hook |
| `web/src/App.tsx`, `web/src/main.tsx` | react-router-dom routes: `/` → `SessionListPage`, `/new` → `LaunchFlow`, `/sessions/:id` → `SessionPage` |
| `web/src/pages/SessionListPage.tsx` | Minimal navigation list (see §4.7) |
| `web/src/pages/LaunchFlow.tsx` | Type + checkout pickers, optional name, first message |
| `web/src/pages/SessionPage.tsx` | History fetch + stream + `MessageList` + `Composer` + abort |
| `web/src/components/MessageList.tsx`, `MessageCard.tsx`, `StreamingText.tsx` | Transcript rendering |
| `web/src/components/ThinkingBlock.tsx`, `ToolCallCard.tsx` | **Placeholder** single-row components (M4 enriches in place, same filenames) |
| `web/src/components/Composer.tsx` | Send when idle; disabled while streaming; inline error |
| `web/package.json` | + `react-router-dom`, `react-markdown` (CONV §8 pinned libs); Vitest test runner (CONV §9) |
| `web/src/state/sessionStore.test.ts`, `web/src/api/stream.test.ts` | Unit tests (§7) |

No new Go packages, routes, wire fields, event types, or statuses. Directory layout
`web/src/pages/` + `web/src/components/` is a local choice (CONV §8 pins component
*names*, not directories); later milestone plans should reuse it.

## 4. Design & rationale

### 4.1 One reducer, one code path (CONV §8)

`sessionReducer` is the single fold for both the history snapshot and the live SSE
stream. This is what makes SPEC §9.3.b ("second client reproduces exact state via
snapshot + cursor replay") provable: a tab that attaches mid-stream runs the *same*
transitions as a tab that watched from the start.

```ts
type ConnectionState = "connecting" | "live" | "reconnecting" | "error";

type SessionAction =
  | { kind: "hydrate"; history: HistoryResponse } // snapshot: reset, then fold entries via applyEntry
  | { kind: "stream"; event: StreamEvent }        // live SSE envelope, verbatim
  | { kind: "connection"; value: ConnectionState };

interface SessionState {
  session: SessionSummary | null;
  entries: SessionEntry[];              // verbatim pi entries, append order
  entryIds: Set<string>;                // dedup guard
  inFlightMessage: AgentMessage | null; // last message_update.message — replaced wholesale
  status: WireStatus;
  lastActivityAt: string | null;
  connection: ConnectionState;
  sendError: string | null;             // inline composer error
}
```

`hydrate` resets state and folds `history.entries` through the same internal
`applyEntry(state, entry)` used by `stream`/`entry` — the snapshot is "synthetic entry
events" exactly as CONV §8 requires. (`SessionAction` is a client-internal wrapper; the
wire `StreamEvent` envelope is untouched.)

**Open question (CONV §10):** CONV §8 pins the reducer signature as
`sessionReducer(state, StreamEvent)`; the `SessionAction` wrapper varies that pinned
shape so `hydrate` and connection-state changes flow through the same fold. Flagged for
the conventions author: either bless the wrapper (M4/M5 then extend via `SessionAction`,
with `{ kind: "stream"; event: StreamEvent }` carrying every wire event) or repin.
Field names/shapes above (`inFlightMessage`, `entryIds: Set<string>`) match the state
shape MILESTONE_4 extends.

Stream fold (complete for M3):

- `entry` → `applyEntry`: skip if `entryIds.has(entry.id)` (belt-and-braces; the server
  already guarantees no duplicates, CONV §4.3); append; if `entry.type === "message" &&
  entry.message.role === "assistant"`, clear `inFlightMessage` (the finalized entry
  supersedes the partial).
- `pi` with `data.type === "message_update"` → `inFlightMessage = data.message`. Pi's
  `message_update` carries the **cumulative partial message** in its `message` field
  (rpc.md "Contains both the partial message and a streaming delta event"), so wholesale
  replacement is both the simplest and the reconnect-correct move: a mid-stream attacher
  repaints the full partial on the *first* delta after attach, with zero buffering
  anywhere (CONV §4.3 "deliberately lossy"). Never append deltas across reconnects.
- `pi` with any other `data.type` → ignored in M3 (`tool_execution_*` live rendering is
  M4; `agent_*` is subsumed by server `status` events).
- `status` → set `status` + `lastActivityAt`; if `status !== "streaming"`, also clear
  `inFlightMessage` (safety net for runs that end without a finalized assistant entry,
  e.g. pre-content errors).
- `dialog` / `dialog_resolved` / `ui` → ignored (return state unchanged, never throw) —
  M5 adds cases without restructuring.
- `reset` → handled in `SessionStream`, never reaches the reducer.

The reducer must be **total**: unknown event types, unknown entry types, and unknown
message roles pass through or render as labeled placeholders — never throw. Real
sessions contain `session_info`, `custom`, `model_change`, … entries from day one.

### 4.2 SSE client: `SessionStream` (CONV §4.3, §8)

```ts
start(cursor: string | null) {
  const qs = cursor ? `?since=${encodeURIComponent(cursor)}` : "";
  this.es = new EventSource(`/api/sessions/${this.id}/events${qs}`);
  this.es.onopen = () => this.onConnection("live");
  this.es.onmessage = (e) => {
    if (e.lastEventId) this.lastEntryId = e.lastEventId;   // persists across id-less events
    const ev: StreamEvent = JSON.parse(e.data);
    if (ev.type === "reset") { this.es.close(); this.onReset(); return; }
    this.onEvent(ev);
  };
  this.es.onerror = () => {
    if (this.es.readyState === EventSource.CLOSED) this.scheduleRebootstrap();
    else this.onConnection("reconnecting");                // ES auto-retry in flight
  };
}
```

- **Automatic cursor-based reconnect costs nothing:** on transient drops, `EventSource`
  auto-reconnects to the same URL and sends `Last-Event-ID` (the last `entry` id — the
  SSE spec persists it across id-less events), and the server resolves `Last-Event-ID`
  **before** the stale `?since=` (CONV §4.3 step 1). Replay resumes gaplessly with no
  client bookkeeping; missed deltas are repainted by the next cumulative partial.
- **`reset`** (invalid cursor, e.g. after any future history rewrite): close, notify;
  the controller refetches `/history`, dispatches `hydrate`, and calls
  `start(history.cursor)` fresh (CONV §4.3 step 7).
- **Fatal close** (`readyState === CLOSED`): the browser permanently closes an
  `EventSource` only on a fatal response — non-200 status or wrong content type. A
  mid-stream server disconnect, **including the server dropping a slow client on
  256-event buffer overflow**, is *not* fatal: the browser auto-reconnects
  (`readyState CONNECTING`) with `Last-Event-ID`, and the previous bullet's replay
  recovers it per CONV §4.3 — kicked clients never take this path. On a genuinely fatal
  close, `scheduleRebootstrap()` fires the existing `onReset` callback after a backoff
  delay (1s, doubling to a 10s cap; reset to 1s on a successful `onopen`), so fatal
  close and `reset` share one controller-side recovery path: refetch `/history` →
  `hydrate` → `start(cursor)`. Re-bootstrapping via snapshot instead of trusting
  `lastEntryId` keeps one recovery path for every failure mode (SPEC §7.3.3:
  attach = snapshot + subscribe).
- Heartbeat `:hb` lines are SSE comments; the browser discards them — no client code.

`useSessionState(sessionId)` (in `sessionStore.ts`) wires it together:
`useReducer` + on mount `getHistory(id)` → `hydrate` → `stream.start(history.cursor)`;
cleanup closes the stream. `SessionPage` is fully self-sufficient from the URL param —
that is what makes a deep-linked second tab correct with zero shared state.

### 4.3 Second-tab-mid-stream correctness (SPEC §9.3.b) — the exact sequence

1. Tab 2 opens `/sessions/{id}` → `GET /history` returns entries `0..n` + `cursor = id(n)`.
2. `SessionStream.start(id(n))` → server subscribes tab 2 to the Broker *first*, then
   `get_entries {since: id(n)}`, replays any entries finalized in the gap, drains the
   buffer deduping by fetched-id set, primes `status` (CONV §4.3 steps 2–6).
3. First live `message_update` carries the cumulative partial → tab 2's
   `inFlightMessage` instantly equals the full streamed-so-far text. Both tabs now hold identical state and
   receive identical events. Entries can be neither missed nor duplicated (server
   guarantees; client `entryIds` guards the seam between steps 1 and 2 anyway).

### 4.4 Composer gating; no steer/follow-up yet

Pi rejects a `prompt` sent mid-stream without `streamingBehavior` (rpc.md), and the API
mirrors this (SPEC §7.1). The steer-vs-follow-up affordance is **M4 scope**
(MILESTONES M4), so M3 gates honestly: while `status === "streaming"`, the send button
is disabled (input stays editable) and the Abort button is shown. If a stale-status race
still slips a send through, the server's error envelope surfaces as an inline message
next to the composer (ToastHost is M5) and the draft is preserved. `behavior` is never
sent in M3.

### 4.5 No optimistic echo

The transcript renders **only** what arrives as entries. A sent message appears when its
durable `entry` event (gibson's entry-feed sync, CONV §4.2) flows back over SSE
(locally: milliseconds). One source of truth means
two tabs can never disagree, and send-failure needs no rollback. The composer clears on
2xx only.

### 4.6 Rendering rules (deliberately plain)

- Only `entry.type === "message"` entries render in `MessageList`; every other entry
  type (`session_info`, `custom`, `model_change`, …) is stored in state but skipped —
  M4's fallback cards own them.
- `user` messages: `content` string → react-markdown; content-block array → text blocks
  concatenated as markdown, image blocks → `[image]` placeholder row.
- `assistant` messages, per content block: `text` → react-markdown; `thinking` →
  `ThinkingBlock` placeholder (one muted row: `thinking · <n> chars`); `toolCall` →
  `ToolCallCard` placeholder (one row: tool name + one-line JSON args). If
  `stopReason === "aborted"` or `"error"`, append a plain `(aborted)` / `(error)` marker
  row — this is what makes abort visible (SPEC §9.2.b).
- `toolResult` messages: one placeholder row (`tool result · <toolName>` + error flag).
- Other roles (`bashExecution`, `custom`, …): one labeled placeholder row. Total, never
  crash.
- `inFlightMessage` renders after the last entry via `StreamingText`: **plain
  whitespace-preserving text while streaming, markdown only on the finalized entry**.
  This avoids re-parsing markdown per token; the finalize repaint is an accepted M3
  visual. Thinking/toolCall blocks inside the partial render as the same placeholders.

### 4.7 Minimal `SessionListPage`

Home renders `GET /api/sessions` as plain rows (name, type, checkout, wire status text,
lastActivityAt) linking to `/sessions/{id}`, plus a "New session" link. It exists purely
so the UI is navigable end-to-end; the *real* session list (loud `blocked-on-dialog`,
close/reopen, non-live polish) is M6 scope. No styling beyond legibility.

### 4.8 `data-testid` contract

Pinned here so the proof workflow (and M4–M7 proofs) can assert deterministically:

| testid | On |
|---|---|
| `session-list`, `session-row-{id}`, `new-session` | SessionListPage |
| `launch-type`, `launch-checkout`, `launch-name`, `launch-message`, `launch-submit`, `launch-error` | LaunchFlow |
| `session-page`, `session-status`, `connection-state` | SessionPage |
| `message-list`, `entry-{entryId}`, `streaming-text` | transcript (`entry-{entryId}` on each rendered message entry) |
| `composer-input`, `composer-send`, `composer-error`, `abort-button` | Composer row |

`session-status` renders the wire status string verbatim; `connection-state` renders the
`ConnectionState` string; `abort-button` exists only while `status === "streaming"`.

## 5. Implementation steps

1. **Deps** — `web/package.json`: add `react-router-dom`, `react-markdown`
   (CONV §8 pinned; nothing else). Add Vitest, the pinned unit-test runner (CONV §9).
2. **`web/src/api/types.ts`** — wire mirrors of CONV §3/§4: `SessionSummary`,
   `WireStatus`, `SessionType`, `Checkout`, `HistoryResponse`, `StreamEvent`,
   `ApiErrorBody`; structural-minimum pi types: `SessionEntry`
   (`{type, id, parentId, timestamp} & Record<string, unknown>`), `MessageEntry`,
   `AgentMessage` narrowed for roles `user`/`assistant`/`toolResult` with
   `TextContent`/`ThinkingContent`/`ToolCall`/`ImageContent` blocks, everything else
   `unknown` passthrough. Do **not** exhaustively remodel pi payloads (CONV §2 churn
   guard); type only what M3 renders.
3. **`web/src/api/client.ts`** — `class ApiError extends Error {code; message}` parsed
   from the envelope; wrappers: `listSessionTypes()`, `listCheckouts()`,
   `listSessions()`, `createSession(req)`, `getHistory(id)`,
   `sendMessage(id, message, behavior?)`, `abortSession(id)`. Same-origin relative
   `/api/...` URLs (the browser always talks to gibson, dev included — CONV §8).
4. **`web/src/api/stream.ts`** — `SessionStream` per §4.2, with the `EventSource`
   constructor injectable for tests.
5. **`web/src/state/sessionStore.ts`** — `sessionReducer` + `applyEntry` per §4.1;
   `initialSessionState`; `useSessionState(sessionId)` hook per §4.2 (owns the
   history-fetch → hydrate → subscribe lifecycle and the re-bootstrap path).
6. **Routing** — `web/src/main.tsx` + `web/src/App.tsx`: `BrowserRouter`, routes
   `/`, `/new`, `/sessions/:id`.
7. **`web/src/pages/LaunchFlow.tsx`** — load types + checkouts in parallel; selects
   default to first entries; submit → `createSession` (button shows a pending state:
   create returns only after spawn + probe + prompt accept, CONV §6 — it can take
   seconds) → `navigate("/sessions/" + session.id)`; `ApiError` → `launch-error` text,
   form values preserved.
8. **`web/src/pages/SessionListPage.tsx`** — per §4.7.
9. **`web/src/pages/SessionPage.tsx`** — `useSessionState(id)`; header (session name,
   `session-status`, `connection-state` when not `"live"`); `MessageList`; `Composer`;
   `abort-button` (calls `abortSession(id)`; errors surface inline like send errors).
10. **Chat components** — `web/src/components/`: `MessageList.tsx` (entries →
    `MessageCard`, then `StreamingText` for `inFlightMessage`; auto-scroll to bottom when
    already at bottom), `MessageCard.tsx` (per §4.6), `StreamingText.tsx`,
    `ThinkingBlock.tsx` + `ToolCallCard.tsx` (placeholder rows — props
    `{text}` / `{name, args}` respectively; M4 enriches in place), `Composer.tsx` (per §4.4;
    Enter submits, Shift+Enter newline).
11. **testids + minimal CSS** — apply §4.8; single stylesheet, legibility only
    (readable measure, monospace for placeholders, muted rows).
12. **Unit tests** — §7.
13. **Build integration** — run `make build` and `go test ./...`; run the canonical
    `build/gibson` artifact with `serve` in a scratch workspace and click through once. Verify
    the `--dev` path still proxies and cold deep links such as `/sessions/{id}` still
    use the existing SPA fallback.

## 6. Interfaces exposed to later milestones

No new server surface. Frontend seams later milestones program against:

- **`web/src/api/types.ts`**: `SessionSummary`, `WireStatus`, `SessionType`, `Checkout`,
  `HistoryResponse`, `StreamEvent`, `SessionEntry`, `MessageEntry`, `AgentMessage`,
  content-block types. M4/M5 extend by narrowing `unknown`, never by renaming.
- **`web/src/api/client.ts`**: `ApiError`; `listSessionTypes`, `listCheckouts`,
  `listSessions`, `createSession`, `getHistory`, `sendMessage`, `abortSession`.
  M4 adds `getSessionStats`; M5 adds `answerDialog`; M6 adds `closeSession` — same file, same
  one-function-per-route pattern (CONV §8).
- **`web/src/api/stream.ts`**: `SessionStream` — `constructor(sessionId, callbacks:
  {onEvent, onConnection, onReset}, esFactory?)`, `start(cursor)`, `stop()`. `onReset`
  is the single re-bootstrap channel: it fires on a wire `reset` event (immediately) and
  on a fatal close (after the §4.2 backoff); the controller reacts identically. M5
  consumes `dialog`/`dialog_resolved`/`ui` events through the existing `onEvent`
  unchanged.
- **`web/src/state/sessionStore.ts`**: `sessionReducer`, `SessionState`,
  `SessionAction`, `initialSessionState`, `useSessionState(sessionId)`. Extension
  points: M4 renders `pi`/`tool_execution_*` (add reducer cases), M5 adds
  `pendingDialog`/`uiState` state (dialog events already flow through and are ignored).
- **Components**: `ThinkingBlock.tsx`, `ToolCallCard.tsx` placeholders — M4 rewrites
  internals under the same filenames/locations. `MessageCard.tsx` is the M4 insertion
  point for `CustomMessageCard`.
- **`data-testid` contract** (§4.8) — M4–M7 proof workflows build on it; treat as
  append-only.

## 7. Testing

The frontend unit-test runner is pinned by CONV §9: **Vitest** (Vite-native,
zero-config), with `*.test.ts` files beside the sources they cover under `web/src/`,
run via `npm test`. M4/M5 reducer tests build on the same runner.

**`web/src/state/sessionStore.test.ts`** (pure reducer — the high-value surface):
- *Replay-equals-render*: build a canonical event sequence `S` (user entry →
  `status:streaming` → 3× `message_update` with growing cumulative partials → assistant
  entry → `status:idle`); for every split point `k`: `hydrate(historyOf(S[..k]))` then
  folding `S[k+1..]` yields state deep-equal to folding all of `S` from
  `initialSessionState`. This is the unit-level proof of SPEC §9.3.b.
- Cumulative replacement: two `message_update`s → `inFlightMessage` equals the last
  partial exactly (no concatenation).
- Finalization: assistant `entry` appends and clears `inFlightMessage`; duplicate entry
  id is a no-op; non-streaming `status` clears `inFlightMessage`.
- Abort shape: assistant entry with `stopReason:"aborted"` folds normally (marker
  derivation is rendering, but state must carry it verbatim).
- Totality: unknown `StreamEvent.type`, unknown entry `type`, unknown message role —
  state unchanged / stored-skipped, nothing throws.
- `hydrate` fully resets prior state (reset-path reuse).

**`web/src/api/stream.test.ts`** (fake `EventSource` via injected factory):
- `?since=` present iff cursor non-null; URL shape.
- `reset` event → stream closed + `onReset` fired, no further `onEvent`.
- `lastEventId` tracked from entry events and retained across id-less messages.
- `onerror` with `readyState CLOSED` (fatal response: non-200/bad content type) →
  `onReset` fired after the backoff delay; backoff doubles to the 10s cap and resets to
  1s on a successful `onopen`. Non-closed error (mid-stream drop, including a
  broker-kicked slow client) → `onConnection("reconnecting")` only, no re-bootstrap
  (EventSource auto-reconnect + `Last-Event-ID` replay per CONV §4.3).

**Component smoke** (same runner, happy-dom/jsdom): `MessageCard` renders each role and
each assistant block type from fixture entries without throwing; aborted marker appears
when `stopReason === "aborted"`.

**Go:** none new. Existing `internal/httpapi` tests own the SPA-fallback contract
(`GET /sessions/anything` → 200 `text/html`, `GET /api/nope` → 404 JSON envelope).

**fakepi / real-pi:** no new Go integration tests — M2 already covers the HTTP contract
with fakepi (CONV §9). M3's end-to-end proof is the browser workflow below against real
pi (milestone acceptance proofs use real pi per CONV §9).

## 8. Agent-verified proof workflow

Run by an agent with browser automation (e.g. the `agent-browser` CLI). Requires real
pi 0.82.0 or newer on `$PATH` with working LLM credentials. The 0.82 minor line is
verified; later minor or major versions are allowed with Gibson's unverified-version warning.
`$REPO` = this repo's checkout.

1. **Build**
   ```sh
   cd $REPO/web && npm ci
   cd $REPO && make build && ./build/gibson --version
   pi --version   # expect 0.82.0 or newer
   ```
2. **Scratch workspace** (house rule: `.sandbox/`, not `/tmp`)
   ```sh
   WS=$REPO/.sandbox/m3-ws/demo && rm -rf $WS && mkdir -p $WS/main && cd $WS/main && git init -b main
   printf '[server]\nport = 7391\n\n[sessions.quick]\ndescription = "Quick task"\n' > gibson.toml
   printf '.gibson/\n' > .gitignore
   git add -A && git commit -m init
   ```
3. **Serve** — from `$WS/main`, run `$REPO/build/gibson serve` in the background.
   Expect a startup log naming `127.0.0.1:7391`.
4. **Load app** — browser → `http://127.0.0.1:7391/`. Expect `[data-testid=session-list]`
   (empty) and `[data-testid=new-session]`.
5. **Launch flow** — click `new-session`. Assert `launch-type` offers `quick` and
   `launch-checkout` offers `main`. Fill name `m3-proof`; message:
   `Write a numbered markdown list counting from 1 to 40, one short sentence each. No tools.`
   Click `launch-submit`.
6. **Session page** — expect navigation to `/sessions/s-<date>-<6>`; capture the session
   id from the URL as `$SID` (used in steps 12–13); within ~10s
   `session-status` = `streaming`, `abort-button` present, `composer-send` disabled, and
   the user message visible as an `entry-*` node.
7. **Streaming assertion** — read `[data-testid=streaming-text].textContent` twice, 2s
   apart; assert non-empty and strictly growing (token streaming, SPEC §9.2.b).
8. **Second tab mid-stream** (SPEC §9.3.b) — while `session-status` is still
   `streaming`, open the same URL in a new tab. Assert in tab 2: (a) the finalized
   `entry-*` testid list equals tab 1's exactly; (b) `streaming-text` is immediately
   non-empty and equals a prefix-consistent snapshot of tab 1's (cumulative-partial
   repaint); (c) 2s later its `textContent` has grown (live tail).
9. **Abort** — click `abort-button` in tab 1. Within ~5s assert in **both** tabs:
   `session-status` = `idle`; `streaming-text` gone; a new assistant `entry-*` present
   containing the `(aborted)` marker; identical `entry-*` lists; `composer-send`
   enabled; `abort-button` absent.
10. **Send from tab 2** — type `Reply with exactly the word: pong` into
    `composer-input`, click `composer-send`. Assert the user entry then a streamed
    assistant entry containing `pong` appear in **both** tabs, and status returns to
    `idle`.
11. **Reload mid-stream (snapshot + cursor replay)** — from tab 1 send
    `Write the tokens COUNT-1 through COUNT-40 in order, one per line, plain text, no
    list formatting, no tools.`; while streaming, reload tab 1. Assert after reload:
    transcript re-renders with no duplicate `entry-*` testids (collect all, assert
    set-size equals list-length), `streaming-text` reappears non-empty and continues
    growing, and after completion the final assistant entry's `textContent` contains
    both `COUNT-1` and `COUNT-40` (no gap). Sentinel tokens, not `1.`/`40.` markers:
    react-markdown renders ordered-list markers as `<ol>`/`<li>` structure, so numeric
    markers survive in neither `textContent` nor `innerHTML`.
12. **Storage sanity** — `ls $WS/main/.gibson/sessions/` shows exactly one `*.jsonl`
    containing the session id; `git -C $WS/main status --porcelain` is empty.
13. **Teardown** — kill the serve process; expect clean exit and the session's pi child
    gone (`pgrep -f "$SID"` empty — scoped to `$SID` so unrelated pi sessions on the
    machine don't fail the proof).
14. **Dev proxy check** (SPEC §8.1.3) — start the Vite dev server (`cd $REPO/web && npm
    run dev`, background), then from `$WS/main` run `$REPO/build/gibson serve --dev`
    in the background. Assert `curl -s http://127.0.0.1:7391/` returns HTML containing
    `/@vite/client` (Vite dev-server marker, absent from the embedded build), and
    `curl -s http://127.0.0.1:7391/api/health` returns `{"ok":true,...}` (handled by
    gibson, not proxied). Kill both processes.

Any failed assertion fails the milestone.

## 9. Success criteria checklist

- [ ] `make build` produces the canonical `build/gibson` artifact serving the SPA (SPEC §8.1.1–8.1.2; existing pipeline unbroken)
- [ ] `--dev` still proxies to Vite for hot reload (SPEC §8.1.3; proof step 14)
- [ ] Launch flow: session type from config + checkout from enumeration + optional name + first message (SPEC §8.2 "Launch flow", §2.2.2)
- [ ] Creating a session from the UI spawns pi in the chosen checkout; session file under `<checkout>/.gibson/sessions/` (SPEC §9.2.a; proof step 12)
- [ ] User/assistant messages render as markdown (SPEC §8.2 chat view, plain-cut per MILESTONES M3)
- [ ] Streaming responses render token-by-token via cumulative-partial replacement (SPEC §9.2.b; CONV §4.3; proof step 7)
- [ ] Abort stops the run and the UI reflects it in all tabs (SPEC §8.2 abort, §9.2.b; proof step 9)
- [ ] Composer sends when idle; disabled while streaming; errors inline; no `behavior` sent (SPEC §6.2 mid-stream constraint honored by construction)
- [ ] Attach = snapshot + subscribe: `/history` then SSE from `cursor` (SPEC §7.3.3; CONV §4.3)
- [ ] Second tab mid-stream reproduces identical state and receives the live tail (SPEC §9.3.a/b browser-level; proof step 8)
- [ ] Reload mid-stream: no gaps, no duplicate entries (SPEC §9.3.b; proof step 11)
- [ ] `EventSource` auto-reconnect resumes via `Last-Event-ID`; `reset` → refetch history and resubscribe (SPEC §7.2.2; CONV §4.3 steps 1, 7)
- [ ] Tool calls and thinking appear as placeholder rows; unknown entry/message types never crash the UI (MILESTONES M3 "deliberately plain")
- [ ] Reducer unit suite green, incl. replay-equals-render property (§7)
- [ ] Full proof workflow (§8) passes agent-run end-to-end (MILESTONES M3 proof)

## 10. Explicitly out of scope

Owned by later milestones — M3 must not implement, even partially, beyond what §4 pins:

- **M4:** tool cards with live cumulative `partialResult` + error states; expandable
  thinking; `ContextMeter` / `GET .../stats`; steer-vs-follow-up composer while
  streaming; `CustomMessageCard` fallback; rendering of non-`message` entries.
- **M5:** dialogs (`DialogModal`, `POST .../dialogs/{dialogId}`, `pendingDialog` from
  history, connect-time `dialog` priming consumption), `ToastHost` for `notify`,
  `StatusStrip` for `setStatus`/`setWidget`/`setTitle`, `uiState` rendering,
  blocked-on-dialog loudness.
- **M6:** real session list (loud statuses, close/reopen actions), sending to
  `stopped`/`closed` sessions (resume-on-demand UX), non-live history polish, orphan
  cleanup surfacing.
- **M7:** non-localhost `bind` multi-device verification, slow-client backpressure
  verification, docs pass, SPEC §9.5 full acceptance run.

M3 renders `dialog`/`ui` SSE events and `pendingDialog`/`uiState` history fields as
no-ops (typed, ignored) — the contract flows through untouched for M5 to consume.
