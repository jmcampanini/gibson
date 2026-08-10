# MILESTONE N2 — Web over live sessions, read-first

`provisional`

## 1. Goal & capability

You can now: open the web UI from any device the bind allows and: see every session
with its status; read any transcript (including TUI-owned and stopped sessions);
launch a server-owned session — which mints its worktree exactly like the CLI — and
chat with it live: streaming text, steer/follow-up, abort, and the four blocking
extension dialogs.

## 2. Preconditions

N1 complete: central `main/.gibson/` storage with `checkout`/`owner` registry fields,
`workspace.Mint`, and the standing `gibson new` gate. M1's `pisession` seam
(MILESTONE_CONVENTIONS §7) and M0's `httpapi`/SPA scaffolding.

## 3. Deliverables

Built in this order — each layer usable when it lands:

1. **Session-file reader** (`internal/store/`): parse pi session JSONL from disk in
   append order — the permanent read path for any session the server doesn't own.
2. **Read-only web**: `GET /api/sessions`, `GET /api/sessions/{id}/history` (file-
   backed for non-live sessions), `SessionListPage` + transcript rendering in `web/`.
3. **Interactive core**: `internal/session/` Manager (create/send/abort/close,
   keep-alive, startup orphan sweep), the remaining MILESTONE_CONVENTIONS §3 routes,
   SSE per §4 at single-client grade, `LaunchFlow` + `Composer` + streaming render.
4. **Dialogs**: `dialog`/`dialog_resolved` bridging, `DialogModal`, `blocked-on-dialog`
   loud in the list.

## 4. Design & rationale

- Read-first ordering: the JSONL reader and transcript renderer are permanent
  infrastructure (non-live sessions can never be read over RPC), and a read-only web
  is immediately useful over N1's TUI sessions. Live streaming lands into a renderer
  already proven by daily reading.
- Server-owned creation mints a worktree via `workspace.Mint` — one launch semantics
  across CLI and web (SPEC §2.2); `POST /api/sessions` takes no checkout
  (MILESTONE_CONVENTIONS §3).
- Transport is SSE + REST per SPEC §7 (reaffirmed 2026-08-09, BACKGROUND addendum).
  The wire envelope and event types follow MILESTONE_CONVENTIONS §4, but this
  milestone proves **single-client grade** only: snapshot + live tail, refresh
  re-snapshots. Cursor-replay gaplessness and multi-client equality are the
  post-N3 horizon; nothing in this slice may preclude them.
- TUI-owned live sessions are listed and readable (file-backed snapshot), never
  written to — the single-writer rule across owners (SPEC §5.1.3, §5.5).
- The N1 gate runs green throughout: registry and storage changes made for the web
  cannot break `gibson new` without failing CI.

## 5. Implementation steps

1. `internal/store/session_file.go` extension (+`_test.go`): full-entry parse in
   append order, tolerant of unknown entry types (`json.RawMessage` verbatim).
2. `internal/httpapi/`: sessions + history routes (file-backed); wire types per
   MILESTONE_CONVENTIONS §3.
3. `web/src/`: `api/types.ts`, `api/client.ts`, `SessionListPage`, transcript view
   per MILESTONE_CONVENTIONS §8 (raw placeholder rows for tool/thinking entries).
4. `internal/session/manager.go` (+tests): lifecycle, status derivation
   (MILESTONE_CONVENTIONS §3), entry-feed sync (§4.2), per-session Broker.
5. `internal/httpapi/`: create/message/abort/close/dialog routes + SSE endpoint.
6. `web/src/`: `api/stream.ts`, `sessionStore.ts` reducer, `LaunchFlow`, `Composer`,
   `StreamingText`, `DialogModal`, `ToastHost`.

## 6. Interfaces exposed to later milestones

- `session.Manager` and `session.StreamEvent` exactly per MILESTONE_CONVENTIONS §7.
- The REST + SSE surface of MILESTONE_CONVENTIONS §3–§4 (single-client grade).
- The file-backed history path N3's `list`/`open` presentation reuses.

## 7. Testing

- `internal/store` owns JSONL parsing (fixtures from real pi files + fakepi output).
- `internal/session` owns lifecycle, status derivation, entry-feed sync, and dialog
  semantics via fakepi scenarios (`dialog_confirm`, `slow_stream`, `crash_mid_stream`).
- `internal/httpapi` owns wire contracts (route shapes, error envelope, SSE framing).
- Vitest owns the `sessionReducer` contract: history snapshot and live tail through
  the identical code path.
- Browser automation owns the composed proof (below). No multi-client assertions in
  automated coverage — deferred with the horizon.

## 8. Agent-verified proof workflow

Scratch workspace, real pi, built binary, browser automation:

1. Create a session via `gibson new` (fakepi or real pi), exit the TUI.
2. `gibson serve`; open the UI: the session lists with truthful status; its
   transcript renders read-only.
3. Launch a session from the UI (type + name + message): verify a fresh `wt-<slug>`
   sibling exists and the session streams text live.
4. Steer mid-stream; verify visible redirection. Abort; verify the UI reflects it.
5. With the `confirm-gate` fixture (test/fixtures/extensions/confirm-gate.ts),
   trigger a dialog, verify `blocked-on-dialog` in the list, answer it, verify the
   agent proceeds.
6. Refresh the page mid-conversation; verify the transcript re-snapshots completely.
7. `git status --porcelain` clean; N1's gate green on the same branch.

## 9. Success criteria checklist

- [ ] All sessions listed with truthful statuses, including TUI-owned (SPEC §7.1).
- [ ] Any transcript readable in the browser, live or not (SPEC §7.1 history).
- [ ] Web launch mints a worktree with identical semantics to `gibson new` (SPEC §2.2).
- [ ] Streaming, steer/follow-up choice, and abort work from the browser (SPEC §8.2).
- [ ] The four blocking dialogs resolve from the browser; blocked state is loud
      (SPEC §6.4, §10.3).
- [ ] Refresh recovers full state via re-snapshot (single-client grade).
- [ ] N1's standing gate green throughout the milestone branch.

## 10. Explicitly out of scope

Multi-client equal-peer semantics and proofs; gapless `Last-Event-ID` replay and
reconnect dedup; tool-call cards, thinking sections, context meter (raw rows only);
live tailing of TUI-owned sessions; session close/reopen UI polish beyond the routes;
restart-resilience proofs (post-N3 horizon).
