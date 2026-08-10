# MILESTONES

Vertical slices toward the [SPEC.md](SPEC.md) product. Each milestone ends with a new
capability you actually use, proven by an agent-verified workflow.
[PROCESS.md](PROCESS.md) governs activation, execution, consolidation, and retirement.

M0 and M1 are complete. The original M2–M7 forecast was retired on 2026-08-09 and
replaced by the N-series below (see BACKGROUND.md's re-path addendum for the full
rationale); N1–N3 are provisional forecasts until their plan-gate reviews.

## Principles behind this slicing

1. **Usefulness first, correctness where it protects use.** The original path proved
   transport correctness (cursors, fan-out, reconnect) before any daily-usable surface
   existed. The N-series inverts that: the daily driver lands first, and each later
   slice is built while the earlier one is in real use. Correctness work is scheduled
   when a usable surface depends on it, not before.
2. **The daily driver is protected by a standing gate.** N1's proof is codified as a
   permanent fakepi-based check inside `make check` and the CLI proof script. Every
   later PR must keep `gibson new` working — mechanically enforced, not promised.
3. **Usable at every stop.** Each milestone's capability statement is phrased as "you
   can now …" — something you'd genuinely do the day it lands.
4. **Learning flows forward.** N2's web is specified after living in N1's TUI; N3's
   session management is specified after living in both. Plan-gate reviews are where
   that usage knowledge enters the plan.

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
through `make cli-proof`; the fakepi/pitest/testws test bed carries forward to all
later milestones.

## N1 — TUI sessions in minted worktrees

**Status: provisional — requires plan-gate review before activation.**
Plan: [MILESTONE_N1.md](MILESTONE_N1.md).

**You can now:** run `gibson new <type>` from `main` — gibson mints a fresh sibling
worktree branched from latest, registers the session centrally, and drops you into the
pi TUI working inside that worktree. Transcripts live in `main/.gibson/` and survive
worktree pruning.

Scope: worktree minting (fetch, branch from the remote default's head, grove-style
`wt-<slug>` sibling); the storage move to a central `main/.gibson/` with `checkout` and
`owner` fields in the registry (no absolute paths); the `gibson new` command (mint →
register → exec interactive pi → mark stopped on exit). Deliberately naive: no `list`,
no `open`; resume via `pi --session-dir main/.gibson/sessions -r` from the worktree
(safe for sessions without a live owner, SPEC §10.1).

Proof: agent-verified workflow with fakepi in a scratch workspace (worktree minted from
latest, correct argv/cwd, registry lifecycle, central transcript, clean `git status`),
codified as a standing gate in `make check` and `scripts/cli-proof.sh`; human proof
against real pi.

## N2 — Web over live sessions, read-first

**Status: provisional — requires plan-gate review before activation.**
Plan: [MILESTONE_N2.md](MILESTONE_N2.md).

**You can now:** open the web UI from any device the bind allows and: see every session
with its status; read any transcript (including TUI-owned and stopped sessions);
launch a server-owned session — which mints its worktree exactly like the CLI — and
chat with it live: streaming text, steer/follow-up, abort, and the four blocking
extension dialogs.

Scope, in build order (each layer usable when it lands): session-file reader (parse pi
JSONL from disk — the permanent path for any session the server doesn't own); read-only
session list + transcript view; server-owned RPC sessions with create/message/abort/
close routes and SSE streaming (single-client grade: snapshot + live tail, refresh
re-snapshots); dialog bridging with `blocked-on-dialog` loud in the list. The N1 gate
runs green throughout the milestone.

Deferred from this slice: multi-client equal-peer proofs, gapless cursor replay,
reconnect dedup guarantees, tool-call cards and thinking rendering (raw placeholder
rows suffice), context meter, live tailing of TUI-owned sessions.

Proof: browser-automation agent reads a TUI-created session, launches a server-owned
session and verifies its minted worktree, watches text stream, steers mid-stream,
aborts, and answers a `confirm-gate` dialog; N1's gate green on the same branch.

## N3 — Session management in the CLI

**Status: provisional — requires plan-gate review before activation.**
Plan: [MILESTONE_N3.md](MILESTONE_N3.md).

**You can now:** treat the CLI as a complete session home — `gibson list` shows every
session with status and worktree; `gibson open <id>` resumes a stopped session in the
pi TUI with history intact (and refuses live ones, single-writer rule).

Scope: registry enumeration and presentation; TUI resume with the original session id
and worktree; whatever daily N1/N2 use has demonstrated is actually needed — this
milestone's plan gate is explicitly expected to re-scope it with usage knowledge.

Proof: agent-verified CLI workflow (list reflects live/stopped/closed truthfully;
open resumes with history; open refuses a live session) via fakepi, plus human proof
against real pi.

---

## After N3

SPEC.md §9.5 remains the v1 horizon — multi-client equal peers, restart resilience,
full conversation rendering. Whether and in what order that horizon is scheduled is
re-planned after N3 with real usage knowledge; nothing is built speculatively for it.

## Coverage map

| SPEC section | Milestone |
|---|---|
| §1 Overview / non-goals | all (scope guard) |
| §2 Environment model & worktree minting | M0 (complete), N1 (minting) |
| §3 Configuration | M0 (complete), M1 (session types) |
| §4 Storage | M1 (`.gibson/` layout), N1 (central move, ownership) |
| §5 Process model | M1 (spawn), N1 (TUI-owned), N2 (keep-alive server sessions), N3 (resume) |
| §6 pi RPC integration | M1 (framing, commands), N2 (events, dialogs) |
| §7 HTTP API | N2 (single-client grade) |
| §8 Frontend | N2 (list, transcript, chat, dialogs) |
| §9 Acceptance | each milestone's proof; §9.5 horizon re-planned after N3 |
| §10 Watch-outs | M1 (framing, single-writer), N1 (single-writer across owners), N2 (SSE hygiene, blocked visibility) |
