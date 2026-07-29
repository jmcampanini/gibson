# M1 Execution Plan

Under `~/Code/github.com/jmcampanini/gibson/main/plans/PROCESS.md`, the active
`~/Code/github.com/jmcampanini/gibson/main/plans/MILESTONE_1.md` owns M1's outcomes and
acceptance boundary. This root plan owns implementation, chunking, progress, and
verification without changing that scope.

PROCESS.md was adopted after Chunks 1–3 landed. Their approved boundaries are
historic and remain unchanged; Chunks 4–8 also retain their approved definitions. The
new chunk-design rules govern the remaining work prospectively and every future root
plan. Known planning inconsistencies are recorded in
`~/Code/github.com/jmcampanini/gibson/main/plans/DIVERGENCES.md` for M1 consolidation.

## How we will work

- Follow the shared lifecycle in `plans/PROCESS.md`; the rules below are M1-specific.
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
- Keep `gibson run <type> <message>` as a permanent one-shot terminal and debugging tool:
  it starts pi, submits one message, streams one answer, persists the session, stops pi,
  and exits. It does not provide a follow-up prompt loop.
- Treat `<type>` as the name of a user-configured `[sessions.<type>]` table. `quick` is
  only an example configuration name, not a built-in or temporary Gibson mode.
- Deliver sustained conversations in later milestones through `gibson serve`, the session
  manager, and the browser rather than by turning `gibson run` into another mode.

## Progress

- [x] Chunk 1 — Reliable pi test environment
- [x] Chunk 2 — Correct RPC transport and launch arguments
- [x] Chunk 3 — Basic live pi session
- [ ] Chunk 4 — First human-drivable run
- [ ] Chunk 5 — Interruptible and crash-safe runs
- [ ] Chunk 6 — Named-checkout runs and storage hardening
- [ ] Chunk 7 — Hostile records and extension boundaries
- [ ] Chunk 8 — Complete M1 acceptance

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
- [x] Keep later slow-stream, large-entry, crash, and dialog scenarios out until the
  chunks that exercise them.

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

- [x] Read records by LF only while accepting CRLF and a final unterminated record.
- [x] Preserve embedded Unicode line separators and records larger than 1 MB.
- [x] Route every outbound write through one serializer.
- [x] Correlate command responses by ID, including out-of-order replies, command failures,
  timeouts, and late unmatched responses.
- [x] Forward non-response records as untouched events and apply bounded backpressure
  rather than dropping them.
- [x] Assemble pi launch arguments in the required order, omitting unset options and
  preserving extra arguments verbatim at the end.

### Dependencies

Chunk 1's deterministic test environment and the existing pi-version package.

### Verification criteria

- [x] Chunked, CRLF, Unicode-separator, large-record, and EOF framing cases all retain
  their exact JSON payloads.
- [x] Concurrent commands cannot interleave bytes and can resolve in any response order.
- [x] Timeouts and pi-declared failures surface as the correct errors without forwarding
  responses as events.
- [x] Launch arguments exactly match the M1 contract for every optional-value combination.
- [x] `make check` passes.

### Mandatory approval gate

- [x] Present Chunk 2's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 3.

## Chunk 3 — Basic live pi session

### Objective and tasks

Wrap the transport in a process-backed session that can complete and persist a normal
one-shot interaction.

- [x] Start pi in the target checkout with inherited environment and captured stderr.
- [x] Start protocol processing, use `get_state` as the readiness probe, and return only
  after pi is ready.
- [x] Expose the M1 typed command surface while preserving pi-owned data as raw JSON.
- [x] Prompt the basic fake-pi scenario and consume its events through `agent_settled`.
- [x] Expose process identity, completion, and exit information safely.
- [x] Shut down with SIGTERM first, reap the process, and close event delivery only after
  final exit information is available.

### Dependencies

Chunks 1–2.

### Verification criteria

- [x] A basic session completes from spawn through readiness, prompt acceptance,
  settlement, and shutdown.
- [x] Typed commands return the expected data without remodeling pi payloads.
- [x] Event order and raw payloads survive the subprocess boundary.
- [x] The session file is valid and the stderr destination is closed after process exit.
- [x] No process or goroutine remains after clean shutdown.
- [x] `make check` passes.

### Mandatory approval gate

- [x] Present Chunk 3's behavior and verification evidence, then receive explicit approval
  before beginning Chunk 4.

## Chunk 4 — First human-drivable run

### Objective and tasks

Deliver the first meaningful M1 capability outside internal tests: a human can invoke the
compiled `gibson run <type> <message>` command in the launch checkout, watch one answer
stream, and inspect the durable result after Gibson exits.

- [ ] Add final-shaped core storage under the launch checkout: the `.gibson/sessions/` and
  `.gibson/logs/` layout, readable cryptographically random pi-compatible session IDs, and
  an in-process-serialized, atomically replaced versioned registry with session identity,
  configured type, timestamps, status, and diagnostic PID.
- [ ] Add `internal/app/run.go` to locate the workspace, load validated configuration,
  resolve the configured session type, resolve and check pi, create the session, prompt
  once, drain through `agent_settled`, and always stop the process.
- [ ] Add `gibson run <type> <message>` as a thin Cobra adapter without changing `serve`;
  do not add `--checkout` until Chunk 6.
- [ ] Stream only assistant text deltas to stdout, adding a trailing newline only when
  needed, and send session details, tool activity, notifications, errors, and a visible
  blocking-dialog warning to stderr.
- [ ] Record the process as live after spawn, update activity when work becomes durable,
  and leave every completed one-shot stopped, resumable later, and with PID zero.
- [ ] Map normal, failed, and interrupted outcomes to exit codes 0, 1, and 130 while
  preserving exactly one top-level presentation of failures.
- [ ] On Ctrl+C, safely close immediately and exit 130 for this first slice; defer graceful
  durable abort and second-interrupt semantics to Chunk 5.
- [ ] Add the opt-in real-pi lifecycle check without issuing a prompt or incurring LLM cost.

The storage packages and on-disk schema introduced here are the final M1 shape. Early runs
remain disposable while collision, leftover-temp-file, exhaustive transition, and hostile
permission hardening are deliberately deferred to Chunk 6.

### Dependencies

Chunks 1–3.

### Verification criteria

- [ ] Root help exposes `run`; a configured type completes one prompt through the compiled
  binary and streams the expected assistant text.
- [ ] `quick`, when used in a proof, comes from `[sessions.quick]`; an unknown type fails
  clearly, lists configured types, and does not spawn pi.
- [ ] The session JSONL, registry record, and stderr log have matching identity and remain
  under the launch checkout's `.gibson/` directory.
- [ ] Normal completion exits 0 with stopped status and PID zero; a basic failure exits 1;
  an interrupt exits 130 and leaves no matching pi process.
- [ ] Stdout remains pipeable assistant text while human diagnostics remain on stderr.
- [ ] Blocking dialogs produce a visible warning that `gibson run` cannot answer them.
- [ ] The opt-in no-prompt real-pi lifecycle check passes when enabled.
- [ ] Generated `.gibson/` state causes no Git status noise.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present the compiled one-shot command, its artifacts, and verification evidence, then
  receive explicit approval before beginning Chunk 5.

## Chunk 5 — Interruptible and crash-safe runs

### Objective and tasks

Make the one-shot command trustworthy during slow work, repeated interruption, unexpected
pi exit, and repeated cleanup without expanding it into a sustained conversation.

- [ ] Add slow-stream and crash fake-pi scenarios that exercise real subprocess timing and
  failure boundaries.
- [ ] On the first interrupt, send `abort`, keep draining until the aborted assistant
  message is durable and the agent settles, then stop and exit 130.
- [ ] On a second interrupt, force immediate shutdown and exit 130.
- [ ] Fail pending commands consistently when pi exits or a command times out.
- [ ] Make close idempotent and escalate from SIGTERM to SIGKILL only after the graceful
  timeout.
- [ ] Guarantee shutdown can unblock a full event channel while preserving deterministic
  pump, reap, completion, and channel-close ordering.
- [ ] Preserve useful pi stderr diagnostics on crashes and clear the registry PID on every
  normal, aborted, forced, or failed exit path.

### Dependencies

Chunks 1–4, including the complete one-shot application workflow and storage lifecycle.

### Verification criteria

- [ ] A first interrupt stops further deltas, persists an assistant entry with
  `stopReason:"aborted"`, reaches `agent_settled`, exits 130, and leaves no orphan.
- [ ] A second interrupt forces prompt shutdown, exits 130, clears the PID, and leaves no
  orphan.
- [ ] A crash exits 1, fails pending work, records final exit information, closes event
  delivery last, preserves a useful stderr tail, and leaves stopped registry state.
- [ ] Repeated close calls neither leak, double-reap, nor corrupt completion state.
- [ ] Slow, crashed, and interrupted runs keep stdout and stderr within their contracts.
- [ ] `make check` passes, including repeated, shuffled, and race-enabled lifecycle tests.

### Mandatory approval gate

- [ ] Present interrupt and crash behavior with process, session, registry, and log
  evidence, then receive explicit approval before beginning Chunk 6.

## Chunk 6 — Named-checkout runs and storage hardening

### Objective and tasks

Extend the same one-shot command to a named sibling checkout and finish the adversarial
storage guarantees deferred from the first human-drivable slice.

- [ ] Add the final `--checkout <name>` flag, defaulting to the checkout from which Gibson
  was launched.
- [ ] Resolve a name as a sibling beneath the workspace root, reject traversal, and accept
  either an ordinary Git directory or a linked-worktree `.git` marker without enumerating
  all worktrees.
- [ ] Keep the selected checkout self-contained: pi runs there and its session JSONL,
  registry, and stderr log all live only under that checkout's `.gibson/` directory.
- [ ] Detect session-ID collisions against both registry records and existing session-file
  headers, regenerating after every collision.
- [ ] Harden registry metadata preservation, exhaustive status transitions, file and
  directory permissions, and interrupted or leftover temporary-file behavior.
- [ ] Find pi session files by reading their header IDs rather than trusting filenames.
- [ ] Reject unknown, missing, traversing, and non-Git targets before spawning pi.

### Dependencies

Chunks 1–5.

### Verification criteria

- [ ] `gibson run <type> <message> --checkout wt-x` runs in `wt-x` and places every
  artifact only under `wt-x/.gibson/`.
- [ ] Omitting `--checkout` preserves launch-checkout behavior.
- [ ] Ordinary repositories and linked worktrees resolve correctly; invalid targets fail
  clearly before pi starts.
- [ ] Storage paths, formats, and permissions match the M1 contract.
- [ ] IDs match the required format and regenerate for every collision source.
- [ ] Leftover temporary files cannot replace or corrupt a readable registry.
- [ ] Registry updates preserve metadata and enforce every intended status transition.
- [ ] Session files are found by header identity even when filenames are opaque.
- [ ] Generated state produces no Git status noise in either checkout.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present named-checkout isolation and hardened on-disk evidence, then receive explicit
  approval before beginning Chunk 7.

## Chunk 7 — Hostile records and extension boundaries

### Objective and tasks

Prove the completed one-shot path against protocol records and extension behavior most
likely to corrupt a subprocess integration, while keeping full dialog UX outside M1.

- [ ] Add fake-pi scenarios for a single record larger than 1 MB and for a blocking
  extension confirmation dialog.
- [ ] Prove large records, CRLF boundaries, final unterminated records, and embedded Unicode
  line separators survive the complete process boundary without truncation or splitting.
- [ ] Send extension UI responses through the shared writer without waiting for a command
  reply, and prove at the process layer that a response releases a blocked fake-pi dialog.
- [ ] Keep `gibson run` deliberately unable to answer dialogs; emit a loud warning for
  blocking requests and let Ctrl+C follow Chunk 5's durable abort path.
- [ ] Exercise tool activity, notifications, agent-reported errors, backpressure, and close
  ordering through the composed application boundary.
- [ ] Preserve raw pi-owned JSON and valid session files throughout every hostile scenario.

### Dependencies

Chunks 1–6.

### Verification criteria

- [ ] A valid record and resulting session file larger than 1 MB survive intact.
- [ ] Unicode separators remain data rather than record boundaries across the subprocess.
- [ ] A UI response releases the fake-pi dialog without corrupting concurrent protocol
  writes.
- [ ] A blocking-dialog run warns visibly, exits 130 after Ctrl+C, persists the aborted
  state, and leaves no orphan process.
- [ ] Tool, notification, and error diagnostics stay on stderr while stdout remains only
  assistant text.
- [ ] Full event-channel backpressure and shutdown ordering remain race-clean.
- [ ] `make check` passes.

### Mandatory approval gate

- [ ] Present hostile-record and extension-boundary evidence, then receive explicit
  approval before beginning Chunk 8.

## Chunk 8 — Complete M1 acceptance

### Objective and tasks

Add no planned product capability. Validate the complete M1 contract from a clean
workspace through the compiled binary and a real pi installation, correcting only defects
that prevent the already-specified behavior from passing.

- [ ] Run every repository formatting, module, lint, race, build, and CLI gate.
- [ ] Run the gated no-prompt real-pi protocol lifecycle check.
- [ ] Execute the complete numbered M1 acceptance workflow with a compiled Gibson binary,
  scratch launch checkout, and sibling checkout.
- [ ] Capture evidence for the real-prompt happy path, artifact identity, registry state,
  graceful interruption, invalid inputs, checkout isolation, clean Git state, and absence
  of orphan processes.
- [ ] Sweep the implementation and plan against every M1 acceptance criterion without
  pulling HTTP, SSE, session-manager, browser, resume, or full dialog UX work forward.

### Dependencies

Chunks 1–7.

### Verification criteria

- [ ] `make verify` passes.
- [ ] `GIBSON_TEST_REAL_PI=1 go test ./internal/pisession/ -run RealPi -v` passes.
- [ ] A real one-shot prompt streams successfully and creates a matching session file,
  registry record, and stderr log.
- [ ] First and second interrupts, crashes, unknown types, invalid checkouts, large records,
  and blocking dialogs satisfy their final M1 outcomes without orphaned pi processes.
- [ ] Launch-checkout and sibling-checkout runs remain isolated and Git-clean.
- [ ] Every numbered step in §8 of
  `~/Code/github.com/jmcampanini/gibson/main/plans/MILESTONE_1.md` passes.

### Mandatory approval gate

- [ ] Present the complete M1 capability and all verification evidence, then receive
  explicit approval before marking M1 complete.

## Agent-verified completion workflow

After Chunk 8 is implemented, an agent must perform the following from
`~/Code/github.com/jmcampanini/gibson/main`:

1. Run `make verify` and require every formatting, module, lint, race, build, and CLI gate
   to pass.
2. Run
   `GIBSON_TEST_REAL_PI=1 go test ./internal/pisession/ -run RealPi -v` and require the
   no-prompt real-pi protocol check to pass.
3. Execute every numbered step in §8 of
   `~/Code/github.com/jmcampanini/gibson/main/plans/MILESTONE_1.md` against the compiled
   binary and a clean scratch workspace under `.sandbox/`.
4. Record evidence for the real-prompt happy path, on-disk identity and registry state,
   first- and second-interrupt behavior, crash diagnostics, absence of orphan processes,
   clear invalid-input failures, sibling-checkout isolation, hostile-record preservation,
   dialog warnings, and clean Git status.
5. Present the evidence at Chunk 8's mandatory approval gate. M1 is not complete until the
   agent workflow passes and receives explicit human approval.
