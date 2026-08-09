# MILESTONES

Vertical slices building to the full [SPEC.md](SPEC.md). Each milestone ends with a new
capability you can actually use, proven by an agent-verified workflow. Later milestones
stack on earlier ones; nothing is built speculatively for a later slice.

[PROCESS.md](PROCESS.md) governs activation, execution, consolidation, and retirement.
M0 and M1 are complete; M2–M7 are provisional forecasts until their plan-gate reviews.

## Principles behind this slicing

1. **Risk-first, not UI-first.** Gibson's risk lives in its seams: Go↔pi RPC integration
   (framing, event stream, process lifecycle), then server↔browser transport (SSE cursors,
   fan-out, reconnect), then rendering. Each seam gets proven before the next layer is
   built on it.
2. **Every layer drivable without the layer above it.** The RPC core is usable from the
   CLI before any HTTP exists (M1). The HTTP API is fully drivable with `curl` before any
   SPA exists (M2). This is what makes each slice provable in isolation — and it leaves
   permanent debugging tools behind.
3. **Usable at every stop.** Each milestone's capability statement is phrased as "you can
   now …" — something you'd genuinely do, not a demo.
4. **Proof is part of the milestone.** A milestone is done when its verification workflow
   passes, run by an agent (Go integration tests against real pi; browser automation for
   UI slices). The final milestone's proof is SPEC.md §9's seven-step acceptance workflow,
   so finishing M7 *is* finishing v1.

---

## M0 — Walking skeleton

**Status: complete.**

## M1 — Headless session core (pi over RPC, no HTTP)

**Status: complete** (2026-08-03).

**You can now:** run `gibson run <type> "<prompt>"` in a checkout — a one-shot pi run
honoring your session-type config, with the session stored in `.gibson/sessions/`.

Delivered across eight chunks: the `pisession` RPC core (LF-only framing, command
correlation, process lifecycle and descendant ownership), `.gibson/` storage with the
locked `state.json` registry, and the `gibson run` one-shot CLI with durable
interrupt, crash, hostile-record, and named-checkout semantics. Proven by the
agent-verified end-to-end workflow against real pi plus the compiled-binary CLI proof
through `make cli-proof`; the fakepi/pitest/testws test bed carries forward to M2–M7.

## M2 — Curl-drivable HTTP API

**Status: provisional — requires plan-gate review before activation.**

**You can now:** drive full sessions with `curl` — create, stream, prompt, abort — with
multiple concurrent clients seeing identical streams.

Scope: REST surface per SPEC §7 (create session from type+checkout+name+message, send with
steer/follow-up, abort, close, list sessions/types/checkouts, history snapshot); worktree
enumeration; per-session SSE with entry-id cursors on `Last-Event-ID`, gapless catch-up,
30s heartbeats, bounded per-client buffers; keep-alive process ownership; equal-peer
fan-out. Resume-on-demand and dialogs are *not* here (M5/M6 prove them separately).

Why before UI: transport semantics (cursoring, reconnect, fan-out, backpressure) are far
easier to verify with two `curl` processes than through a rendering layer, and the API
becomes the SPA's stable contract.

Proof: agent script creates a session, tails SSE from two clients, sends a prompt from a
third, verifies both streams carry identical ordered events; kills one stream mid-turn,
reconnects with `Last-Event-ID`, verifies no gap and no duplicates; verifies heartbeats
and abort.

## M3 — Minimal browser chat

**Status: provisional — requires plan-gate review before activation.**

**You can now:** open the UI, start a session (type + checkout picker), chat with
streaming responses, and abort — from any device the bind allows.

Scope: SPA session view wired to M2's API: launch flow, message list (user/assistant,
markdown, streaming text), composer, abort button, SSE reconnect with cursor catch-up.
Deliberately plain rendering — tool calls and thinking may appear as raw placeholder rows.

Why minimal: gets the full vertical (browser → Go → pi → browser) proven and *daily-usable*
at the earliest possible moment; everything after this is enrichment.

Proof: browser-automation agent launches a session, sends a prompt, watches text stream,
aborts a long turn, opens a second tab mid-stream and verifies identical state via replay.

## M4 — Full conversation rendering

**Status: provisional — requires plan-gate review before activation.**

**You can now:** read real working sessions comfortably — tool calls as live collapsible
cards, thinking collapsed by default, context meter, steer vs queued follow-up sends.

Scope: SPEC §8 chat view completed: tool cards with live cumulative `partialResult` and
error states; thinking sections; `custom_message` generic fallback card (e.g.
`subagent_result`); context-usage meter from `get_session_stats`; composer offering
steer / follow-up when streaming (pi requires the choice mid-stream).

Proof: agent runs a session type that exercises tools, verifies cards update live and
finalize correctly, verifies a mid-stream steer visibly redirects the agent, and verifies
a `custom_message` renders via the fallback card.

## M5 — Extension dialogs and surfaces

**Status: provisional — requires plan-gate review before activation.**

**You can now:** run session types whose extensions ask questions — approve/deny gates,
selects, inputs — from the browser, on whichever device answers first.

Scope: `extension_ui_request/response` bridged over SSE/REST (SPEC §6.4, §7): the four
blocking dialogs (`select`/`confirm`/`input`/`editor`) as modals; `notify` toasts;
`setStatus`/`setWidget`/`setTitle` strip; broadcast-to-all with first-answer-wins and
resolution events closing everyone else's modal; blocked-on-dialog surfaced in session
state.

Why its own slice: dialogs are the only *bidirectional blocking* flow in the system —
different failure modes (deadlock, double-answer) deserving isolated proof. This is also
the milestone that makes your real extension set usable.

Proof: agent uses a test extension (e.g. a permission-gate) to trigger each dialog type,
answers from the browser, verifies the block releases; opens two clients, answers from
one, verifies the other's modal closes; verifies unanswered dialogs mark the session
blocked in the list.

## M6 — Session management and restart resilience

**Status: provisional — requires plan-gate review before activation.**

**You can now:** treat gibson as the durable home for all sessions in the workspace —
browse them across checkouts, close and reopen them, restart the server without losing
anything.

Scope: session list across all checkouts (name, type, checkout, status
idle/streaming/blocked, last activity); close (kill process, keep resumable); reopen =
resume-on-demand respawn with `--session-id` (SPEC §5); startup orphan cleanup (stale
"live" registry entries marked stopped, never re-attach); history snapshot rendering for
non-live sessions.

Proof: agent creates sessions in two checkouts, restarts the server, verifies the list
survives, sends a message to a pre-restart session and verifies it resumes with history
intact, and verifies orphan cleanup on a simulated crash.

## M7 — v1 hardening and full acceptance

**Status: provisional — requires plan-gate review before activation.**

**You can now:** call it v1 — SPEC.md's acceptance workflow passes end-to-end.

Scope: whatever the acceptance run flushes out, plus the deliberately-deferred edges:
multi-device via non-localhost `bind`, slow-client backpressure verification, pi minimum
and verified-line behavior, `.gibson/` self-containment and clean-git-status audit, and a
docs pass (README: install, configure, run).

Proof: SPEC §9's seven-step agent-verified workflow, run start to finish by a
browser-automation agent against a scratch workspace. Passing it is the definition of
done for v1.

---

## Coverage map

| SPEC section | Milestone |
|---|---|
| §1 Overview / non-goals | all (scope guard) |
| §2 Environment model | M0 (complete), M2 (worktree enumeration) |
| §3 Configuration | M0 (complete), M1 (session types) |
| §4 Storage | M1 (`.gibson/` layout), M6 (registry lifecycle) |
| §5 Process model | M1 (spawn), M2 (keep-alive), M6 (resume, orphans) |
| §6 pi RPC integration | M1 (framing, commands), M4 (events/rendering), M5 (dialogs) |
| §7 HTTP API | M2, M5 (dialog endpoints) |
| §8 Frontend | M3 (skeleton), M4 (rendering), M5 (dialogs), M6 (list) |
| §9 Acceptance | each milestone's proof; full workflow at M7 |
| §10 Watch-outs | M1 (framing, single-writer), M2 (SSE hygiene), M5 (blocked visibility), M7 (churn/version) |
