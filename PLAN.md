# M1 Execution Plan

This is the human-readable execution companion to
`~/Code/github.com/jmcampanini/gibson/main/plans/MILESTONE_1.md`. That milestone document
remains authoritative for detailed behavior, interfaces, and rationale. This plan organizes
its implementation into reviewable chunks without changing its scope.

## How we will work

- Complete one chunk at a time, in order.
- Create each chunk branch from `feature/m1` and target its PR back to `feature/m1`; do
  not stack a later chunk on an unmerged chunk branch.
- Merge `feature/m1` into the default branch only after every chunk PR has landed and M1
  has passed its final approval gate.
- Keep the repository green at every chunk boundary.
- At each approval gate, present what now works, meaningful decisions or deviations,
  verification evidence, and remaining concerns.
- Stop after every chunk and wait for explicit human approval before beginning the next.
- Do not create commits unless requested.
- Keep HTTP, SSE, the session manager, browser work, resume behavior, and full dialog UX
  out of M1.

## Progress

- [x] Chunk 1 — Reliable pi test environment
- [ ] Chunk 2 — Correct RPC transport and launch arguments
- [ ] Chunk 3 — Basic live pi session
- [ ] Chunk 4 — Session lifecycle resilience
- [ ] Chunk 5 — Durable session records and checkout targeting
- [ ] Chunk 6 — Complete one-shot run workflow
- [ ] Chunk 7 — CLI boundary and complete M1 proof

## Chunk 1 — Reliable pi test environment

### Objective and tasks

Create a deterministic substitute for pi so later work can exercise real subprocess,
JSONL, and session-file boundaries without requiring a network or LLM.

- [x] Let scratch workspaces configure a pi binary, session types, and sibling checkouts
  while preserving their current zero-option behavior.
- [x] Build a fake pi executable that reports the verified pi version and speaks enough
  RPC for a basic session.
- [x] Make the basic scenario accept a prompt, emit a realistic event sequence, and write
  a valid v3 session file with a consistent entry chain.
- [x] Provide one reusable helper that builds fake pi and another that enables real-pi
  checks only when explicitly requested.
- [x] Keep later slow-stream, large-entry, crash, and dialog scenarios out until Chunk 4
  needs them.

### Dependencies

The completed M0 test workspace, configuration, and pi-version behavior.

### Verification criteria

- [x] Fake pi builds once and answers both `--version` and a readiness request correctly.
- [x] Its basic run writes a valid session header and ordered entries.
- [x] Existing scratch-workspace behavior remains unchanged when no options are supplied.
- [x] Default verification requires neither a network nor an LLM.
- [x] `make check` passes.

### Mandatory approval gate

- [x] Present Chunk 1's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 2.

## Chunk 2 — Correct RPC transport and launch arguments

### Objective and tasks

Build the protocol layer that exchanges JSONL commands, responses, and events with pi
without corrupting records or interleaving concurrent writes.

- [ ] Read records by LF only while accepting CRLF and a final unterminated record.
- [ ] Preserve embedded Unicode line separators and records larger than 1 MB.
- [ ] Route every outbound write through one serializer.
- [ ] Correlate command responses by ID, including out-of-order replies, command failures,
  timeouts, and late unmatched responses.
- [ ] Forward non-response records as untouched events and apply bounded backpressure
  rather than dropping them.
- [ ] Assemble pi launch arguments in the required order, omitting unset options and
  preserving extra arguments verbatim at the end.

### Dependencies

Chunk 1's deterministic test environment and the existing pi-version package.

### Verification criteria

- [ ] Chunked, CRLF, Unicode-separator, large-record, and EOF framing cases all retain
  their exact JSON payloads.
- [ ] Concurrent commands cannot interleave bytes and can resolve in any response order.
- [ ] Timeouts and pi-declared failures surface as the correct errors without forwarding
  responses as events.
- [ ] Launch arguments exactly match the M1 contract for every optional-value combination.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present Chunk 2's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 3.

## Chunk 3 — Basic live pi session

### Objective and tasks

Wrap the transport in a process-backed session that can complete and persist a normal
one-shot interaction.

- [ ] Start pi in the target checkout with inherited environment and captured stderr.
- [ ] Start protocol processing, use `get_state` as the readiness probe, and return only
  after pi is ready.
- [ ] Expose the M1 typed command surface while preserving pi-owned data as raw JSON.
- [ ] Prompt the basic fake-pi scenario and consume its events through `agent_settled`.
- [ ] Expose process identity, completion, and exit information safely.
- [ ] Shut down with SIGTERM first, reap the process, and close event delivery only after
  final exit information is available.

### Dependencies

Chunks 1–2.

### Verification criteria

- [ ] A basic session completes from spawn through readiness, prompt acceptance,
  settlement, and shutdown.
- [ ] Typed commands return the expected data without remodeling pi payloads.
- [ ] Event order and raw payloads survive the subprocess boundary.
- [ ] The session file is valid and the stderr destination is closed after process exit.
- [ ] No process or goroutine remains after clean shutdown.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present Chunk 3's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 4.

## Chunk 4 — Session lifecycle resilience

### Objective and tasks

Make a live session safe under interruption, blocking UI requests, large records,
unexpected exits, and repeated shutdown attempts.

- [ ] Abort an active turn while continuing to consume events until the aborted assistant
  message is durable and the agent settles.
- [ ] Send extension UI responses through the shared writer without waiting for a reply.
- [ ] Fail pending commands consistently when pi exits or a command times out.
- [ ] Make close idempotent and escalate from SIGTERM to SIGKILL only after the graceful
  timeout.
- [ ] Guarantee that shutdown can unblock a full event channel and preserve deterministic
  pump, reap, completion, and channel-close ordering.
- [ ] Add slow-stream, large-entry, crash, and blocking-dialog fake-pi scenarios alongside
  the behavior that consumes them.
- [ ] Capture useful crash diagnostics in the session's stderr log.
- [ ] Add the opt-in real-pi protocol check without issuing a prompt or incurring LLM cost.

### Dependencies

Chunks 1–3.

### Verification criteria

- [ ] Abort stops further deltas, persists an aborted assistant entry, and reaches
  `agent_settled`.
- [ ] A UI response releases a blocked fake-pi dialog.
- [ ] A record larger than 1 MB survives the complete process boundary intact.
- [ ] A crash fails pending work, records exit information, closes event delivery last,
  and preserves stderr diagnostics.
- [ ] Repeated close calls neither leak nor double-reap the process.
- [ ] The opt-in no-prompt real-pi lifecycle check passes when enabled.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present Chunk 4's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 5.

## Chunk 5 — Durable session records and checkout targeting

### Objective and tasks

Give each checkout self-contained session storage and let a run target a named sibling
checkout without pulling full worktree enumeration into M1.

- [ ] Create the required sessions and logs layout under the target checkout.
- [ ] Generate readable, pi-compatible session IDs with cryptographic randomness and
  regenerate after collisions with either registry or session data.
- [ ] Persist the versioned registry with atomic replacement and in-process serialization.
- [ ] Record live, stopped, and closed states with activity timestamps and a diagnostic PID.
- [ ] Find a pi session file by reading its header ID rather than depending on its filename.
- [ ] Resolve a named sibling checkout beneath the workspace root and accept either a Git
  directory or linked-worktree marker.

### Dependencies

Chunks 1–4, including realistic fake-pi session files.

### Verification criteria

- [ ] Storage paths and permissions match the M1 contract.
- [ ] Leftover temporary files cannot replace or corrupt the readable registry.
- [ ] IDs match the required format and regenerate on every collision source.
- [ ] Registry updates preserve metadata and enforce the intended status transitions.
- [ ] Session files are found by identity even when filenames are opaque.
- [ ] Ordinary repositories and linked worktrees resolve correctly; invalid names and
  non-checkouts fail clearly.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present Chunk 5's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 6.

## Chunk 6 — Complete one-shot run workflow

### Objective and tasks

Compose the lower layers into the headless M1 capability: resolve a configured session
type, run one prompt, stream useful terminal output, and leave a durable resumable session.

- [ ] Locate the workspace, load validated configuration, and resolve the requested session
  type and target checkout.
- [ ] Resolve and check pi before spawning, including the existing warning for an
  unverified newer version.
- [ ] Create storage, assign an ID, spawn pi, and record the live process.
- [ ] Submit the prompt and drain events until the agent settles.
- [ ] Write only assistant text deltas to stdout and add a final newline only when needed.
- [ ] Send session details, tool activity, notifications, and blocking-dialog warnings to
  stderr.
- [ ] Update activity when prompts are accepted and messages become durable.
- [ ] Handle normal completion, agent-reported errors, unexpected exits, and first or
  second interrupts.
- [ ] Always stop the process, clear its diagnostic PID, and leave the session resumable
  with stopped status.

### Dependencies

Chunks 1–5.

### Verification criteria

- [ ] A basic run streams the expected assistant text and leaves valid session, log, and
  registry artifacts.
- [ ] A named-checkout run places every artifact inside that checkout.
- [ ] Stdout remains pipeable assistant output while human diagnostics stay on stderr.
- [ ] First interrupt aborts and settles cleanly; a second interrupt forces immediate
  shutdown; both produce the interrupt outcome.
- [ ] Crashes produce an error outcome, preserve diagnostics, update the registry, and
  leave no orphan process.
- [ ] Unknown session types and checkouts fail clearly without spawning pi.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present Chunk 6's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 7.

## Chunk 7 — CLI boundary and complete M1 proof

### Objective and tasks

Expose the workflow as `gibson run` and prove the finished milestone through the compiled
binary and a real pi installation.

- [ ] Add `gibson run <type> <message> [--checkout <name>]` as a thin adapter to the
  application workflow.
- [ ] Register the command without changing the existing `serve` behavior.
- [ ] Map successful, failed, and interrupted outcomes to process exit codes 0, 1, and 130.
- [ ] Preserve exactly one top-level presentation of failures.
- [ ] Exercise the compiled command in a scratch workspace for success, artifact
  persistence, interruption, invalid input, and sibling-checkout targeting.
- [ ] Confirm generated `.gibson` data remains ignored by Git.

### Dependencies

Chunks 1–6.

### Verification criteria

- [ ] Root help exposes `run`, and arguments and flags reach the application workflow
  unchanged.
- [ ] Exit codes and top-level error presentation match the M1 contract.
- [ ] `make verify` passes.
- [ ] `GIBSON_TEST_REAL_PI=1 go test ./internal/pisession/ -run RealPi -v` passes.
- [ ] A real prompt streams successfully and creates a matching session file, registry
  record, and stderr log.
- [ ] Interrupt, unknown-type, and sibling-checkout proof cases pass without orphaned pi
  processes.
- [ ] `git status --porcelain` remains empty in every proof checkout.

### Mandatory approval gate

- [ ] Present the complete M1 capability and all verification evidence, then receive
  explicit approval before marking M1 complete.

## Agent-verified completion workflow

After Chunk 7 is implemented, an agent must perform the following from
`~/Code/github.com/jmcampanini/gibson/main`:

1. Run `make verify` and require every formatting, module, lint, race, build, and CLI gate
   to pass.
2. Run
   `GIBSON_TEST_REAL_PI=1 go test ./internal/pisession/ -run RealPi -v` and require the
   no-prompt real-pi protocol check to pass.
3. Execute every numbered step in §8 of
   `~/Code/github.com/jmcampanini/gibson/main/plans/MILESTONE_1.md` against the compiled
   binary and a scratch workspace under `.sandbox/`.
4. Record evidence for the real-prompt happy path, on-disk identity and registry state,
   clean interrupt with an aborted durable message, absence of orphan processes, clear
   invalid-input failure, sibling-checkout isolation, and clean Git status.
5. Present the evidence at Chunk 7's mandatory approval gate. M1 is not complete until
   the agent workflow passes and receives explicit human approval.
