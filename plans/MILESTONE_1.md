# MILESTONE_1 — Headless session core (pi over RPC, no HTTP)

Status: **active** under [PROCESS.md](PROCESS.md). This file owns M1's outcomes and
acceptance boundary; root [PLAN.md](../PLAN.md) owns implementation, chunking, and
verification.

Implementation plan for MILESTONES.md M1. SPEC.md is normative for behavior;
MILESTONE_CONVENTIONS.md is binding (cited as "conventions §N"). Pi protocol facts below are
taken from the docs shipped with verified pi version 0.82.1
(`~/.local/lib/node_modules/@earendil-works/pi-coding-agent/docs/rpc.md`,
`session-format.md`, `sessions.md`) — implementers should re-read `rpc.md` before writing
`internal/pisession`.

## 1. Goal & capability

**You can now:** run `gibson run <type> "<prompt>"` in a checkout — a one-shot pi run
honoring your session-type config, with the session stored in `.gibson/sessions/`.

This milestone builds the single riskiest seam — Go ↔ `pi --mode rpc` over JSONL — in
isolation, drivable from the CLI without adding or using HTTP (MILESTONES.md M1). The
existing server remains intact. The `run` command doubles as a permanent debugging
tool.

## 2. Preconditions

**M0 is complete.** M1 starts from the current implementation and binding conventions.

External prerequisite for the proof workflow only (not for `go test ./...`): a real
`pi` at least 0.82.0 on `$PATH` with a working default model. The 0.82.x line is verified;
a later minor or major version is allowed and must produce the existing unverified-version warning.

## 3. Deliverables

New files (repo-relative):

- `internal/pisession/event.go` — `Event{Type string; Raw json.RawMessage}` (conventions §7).
- `internal/pisession/rpc.go` — transport core: LF-only framing reader, serialized
  writer, command/response correlation, demux. Operates on plain `io.Reader`/`io.WriteCloser`
  so it is unit-testable against in-memory pipes.
- `internal/pisession/argv.go` — argv assembly from session-type config (conventions §6).
- `internal/pisession/session.go` — `Config`, `Spawn()`, `Session` (process lifecycle,
  typed command methods, `Events()`, `Close`), stderr capture.
- `internal/pisession/errors.go` — `ErrInvalidCursor`, `ErrProcessExited`,
  `ErrCommandTimeout`.
- `internal/pisession/rpc_test.go`, `argv_test.go`, `session_test.go` (fakepi),
  `session_realpi_test.go` (gated).
- `internal/store/store.go` — `.gibson/` layout creation and path helpers (SPEC §4.1).
- `internal/store/registry.go` — `state.json` registry per conventions §5: load/save
  (mutex + temp-then-rename), record CRUD, status transitions.
- `internal/store/id.go` — session id generation per conventions §5.
- `internal/store/registry_test.go`, `id_test.go`.
- `internal/workspace/checkout.go` — `ResolveCheckout(workspaceRoot, name)` (see §4.10).
- `internal/app/run.go` + `internal/app/run_test.go` — one-shot workflow, dependency
  composition, signal handling, process lifecycle, and terminal I/O.
- `cmd/run.go` + `cmd/run_test.go` — thin Cobra adapter for
  `gibson run <type> <message> [--checkout <name>]`.
- `internal/fakepi/main.go` (+ `internal/fakepi/scenarios/` package) — the fake pi
  executable (conventions §9).
- `internal/pitest/pitest.go` — `BuildFakePi(t)`, `RequireRealPi(t)`.

Extended files:

- `main.go` — map the run workflow's interrupt outcome to process exit 130 while
  preserving one-time top-level error presentation.
- `cmd/root.go` — wire in `run`.
- `internal/testws/testws.go` — extend the existing helper with functional options
  (§4.11), preserving its exported surface.

## 4. Design & rationale

### 4.1 Package split: transport core vs. process

`internal/pisession` splits into a transport core (`rpc.go`: framing, write
serialization, correlation, demux, generic over `io.Reader`/`io.WriteCloser`) and the
process layer (`session.go`: exec.Cmd, spawn/readiness, signals, reaping, stderr file).
Rationale: the framing and correlation logic is where SPEC §10.1/§6.1 corruption risk
lives; it must be testable with byte-exact table tests and no subprocess. The process
layer then gets exercised against fakepi.

### 4.2 Framing reader (SPEC §6.1, conventions §6)

Read pi stdout with `bufio.Reader.ReadBytes('\n')` — **never** `bufio.Scanner` (64KB
default token cap breaks large entries) and never any Unicode-aware line splitter
(pi's docs: U+2028/U+2029 are legal inside JSON strings; splitting on them corrupts
records). The reader loop:

```go
r := bufio.NewReaderSize(stdout, 64<<10)
for {
    line, err := r.ReadBytes('\n') // returns whatever was read even when err != nil
    if trimmed := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'}); len(trimmed) > 0 {
        c.demux(trimmed) // ReadBytes returns a fresh slice; safe to retain
    }
    if err != nil { // io.EOF (process exit) or read error → pump exits
        return
    }
}
```

Rules pinned: split on `\n` ONLY; strip exactly one trailing `\r` (accepts `\r\n`);
`ReadBytes` grows without bound so multi-megabyte single-line entries pass; a final
unterminated line at EOF (possible only on pi crash) is still handed to demux — a parse
failure there is logged at error level and dropped. Writes to pi stdin are one compact
JSON object + `\n`.

### 4.3 Write serialization and command correlation (conventions §6)

- All stdin writes go through a single writer goroutine fed by a channel — commands and
  `extension_ui_response` writes are serialized, never interleaved mid-line.
- Every command gets `id: "c-<n>"` from a per-process atomic counter. A mutex-guarded
  `map[string]chan json.RawMessage` resolves replies. Per rpc.md, pi echoes `id` on the
  matching `{"type":"response","command":...,"success":...}` line.
- Every outbound write has a 30s bound. Ordinary commands use one 30s budget across
  enqueue, write, and response (a tighter caller context wins): on a response timeout the
  id is removed, `ErrCommandTimeout` is returned, and a late response is logged and
  dropped. Prompt submission keeps the write bound but its response remains pending until
  caller cancellation, process exit, transport closure, or acceptance so pre-acceptance
  extension dialogs can wait. A write timeout is fatal to the transport and fails every
  pending command. The session method selects this policy; D-017 owns its conventions,
  upstream-disposition, and M2 plan-gate reconciliation.
- `success:false` responses resolve the waiter with an error wrapping pi's `error` string.
  rpc.md note: pi parse errors come back as `{"type":"response","command":"parse",
  "success":false,...}` with no id — unmatched, logged at error level (it indicates a
  gibson framing/encoding bug), never forwarded as an event.
- `extension_ui_response` writes use pi's request uuid as `id` and expect **no** reply
  (rpc.md Extension UI Protocol) — `RespondUI` returns after the write is queued.

### 4.4 Demux and the Events channel

The stdout pump goroutine only demuxes (conventions §6):

```go
var head struct{ Type, ID string }
_ = json.Unmarshal(line, &head)
if head.Type == "response" {
    c.resolvePending(head.ID, line)    // unmatched → Charm Log error, dropped
    return
}
select {
case c.events <- Event{Type: head.Type, Raw: line}:
case <-c.closing:                      // unsticks the pump if the consumer is gone
}
```

Everything that is not a `response` — lifecycle events, streaming deltas,
`extension_ui_request`, `queue_update`, retries, even the rare `entry_appended`
(extension custom entries only; see the open question below) — flows to
`Events() <-chan Event` verbatim as `json.RawMessage` (conventions §2: gibson never
re-models pi payloads). The channel is buffered (256); when full, the pump **blocks**:
that is the deliberate backpressure mechanism — pi applies backpressure on its side
rather than dropping events (SPEC §6.1.3), and per-client buffering is the M2 Broker's
job, not this layer's. Contract stated on `Events()`: the owner must drain promptly;
in M1 the single owner is `internal/app`'s run stream loop. A pending response behind a
stalled event in the stdout pipe would time out — the drain-promptly contract prevents
that, and `closing` guarantees `Close` can always terminate the pump.

Events generally carry no `id` field; the one exception, `bash_execution_update`
(unused in v1), still routes as a normal event because its `type` is not `"response"`.

**Open question flagged for M2 (conventions §10).** SPEC §6.3 calls `entry_appended`
"the primary feed for cursor-based client sync" and conventions §4.2 sources the SSE
`entry` event from `entry_appended.entry` — but pi 0.82.1 does not emit
`entry_appended` for normal conversation: rpc.md's event table does not list it, and
its only emission site in `dist/core/agent-session.js` is the extension-runtime
`appendEntry` helper for extension custom entries. Ordinary user/assistant/toolResult
entries are persisted at `message_end` with no per-entry event, and `set_session_name`
emits only `session_info_changed`. M1 therefore uses `message_end` as its
durable-append signal (§4.8) and reads entry ids via `get_entries`; the SSE
`entry`-feed design in SPEC §6.3 / conventions §4.2 must be re-derived from events pi
actually emits (e.g. `message_end` plus `get_entries` id confirmation) before M2 is
implemented — M1 deliberately decides nothing about the SSE shape.

### 4.5 Spawn, readiness, argv (SPEC §5.1, conventions §6)

Argv assembly (`argv.go`), exact order, table-tested:
`<pi_bin> --mode rpc --session-id <id> --session-dir <checkout>/.gibson/sessions`
+ `--model <m>` (only if set) + `--thinking <t>` (only if set) + `extra_args...` verbatim,
last, unparsed (SPEC §3.2.4 — gibson must not parse or validate them). `cwd` = target
checkout; environment inherited; `pi_bin` from config else `exec.LookPath("pi")`.
`--name` is never passed — display names go through `set_session_name` (conventions §5);
`gibson run` has no name flag (conventions §1 CLI tree) so it skips that step entirely.

Spawn sequence (conventions §6): start pi in a dedicated process group so terminal
signals sent to Gibson's foreground group cannot preempt RPC abort; stderr is already
wired to the log file (§4.7). Then start pump/writer/waiter goroutines → `get_state` as
readiness probe (proves the process is up and protocol-speaking; its response includes
`sessionId`, `isStreaming`, `sessionFile` per rpc.md) → return the live `Session`. The
caller then issues `prompt`.
Per rpc.md, the `prompt` response means **accepted/queued, not completed** — completion
is observed via events; and `streamingBehavior` (`"steer"`|`"followUp"`) is REQUIRED if
the agent is already streaming, otherwise the command errors. `Prompt(ctx, msg, behavior)`
sets the field only when `behavior != ""`; `run` always sends `""` (fresh session, never
streaming).

Session identity: gibson generates the id (`store.NewSessionID`, conventions §5 format
`s-<YYYYMMDD>-<6 of [a-z0-9] via crypto/rand>`, matching pi's id regex SPEC §5.1.2,
regenerated on collision against registry + `sessions/` headers) and passes it via
`--session-id`; gibson session id **is** the pi session id. The session **file name**
under `sessions/` is treated as opaque — pi's docs pin `<timestamp>_<uuid>.jsonl` only
for the default dir — so gibson identifies a session's file by reading each file's
header line (`{"type":"session","version":3,"id":...}`) and matching `id`, never by
filename. `store.Store` (already rooted at one checkout, §4.8) exposes
`FindSessionFile(id string) (string, error)` for this.

### 4.6 Shutdown, abort, unexpected exit (SPEC §5.2, conventions §6)

Goroutine topology per Session: writer, stdout pump, waiter. Pi stdout uses an explicitly
owned `os.Pipe`, not `StdoutPipe`, so the waiter calls `cmd.Wait()` independently of EOF
without racing `exec.Cmd`'s pipe cleanup. After process exit it records the exit error,
allows up to 500ms for buffered final records to drain, then closes the owned reader to
unblock a descriptor inherited by a helper. It then fails all pending command waiters
with `ErrProcessExited`, zeroes/clears the pending map, closes the stderr log file, closes
`done`, and finally closes the events channel. Consumers therefore see `range Events()`
end and can then read a fully-populated `ExitErr()`.

A lightweight process tracker snapshots pi descendants while pi is live and retains
PID/PGID/start-time identities after reparenting. Before signaling a retained identity,
shutdown requires the current process to match its tracked PGID and start time, limiting
PID-reuse risk. The reaper kills matched tracked groups/processes on every exit path
before publishing completion.

- `Close(ctx)`: mark closing (unsticks pump sends), signal pi's dedicated process group
  with SIGTERM, and wait up to 5s. Forced escalation freezes pi, takes a final descendant
  snapshot, SIGKILLs owned descendant groups/processes and pi's group, then reaps. This
  covers pi's detached tool process groups when pi cannot run its own SIGTERM cleanup
  handler. Graceful pi exit under SIGTERM is status 143 (SPEC §5.2.2). Idempotent.
- `Abort(ctx)`: sends the `abort` command; pi replies `success:true` and the aborted
  assistant message carries `stopReason:"aborted"` (SPEC §6.2, rpc.md). Abort does NOT
  terminate the process — the caller keeps consuming events until `agent_settled`.
- Unexpected exit (crash): same waiter path; the caller (here the `internal/app` run
  workflow, later the M2 Manager) observes `Done()`/channel close, marks the registry
  entry `stopped`, and logs
  the tail of the stderr log at error level (conventions §6).

Run-loop termination event: `agent_settled`, not `agent_end` — rpc.md is explicit that
`agent_end` "may still be followed by retry, compaction, or queued continuations" while
`agent_settled` means nothing automatic remains (this matches conventions §3's
streaming-flag rule: cleared on `agent_settled`, `agent_end` only as fallback).

### 4.7 Stderr capture (SPEC §5.1.4)

At spawn: create `<checkout>/.gibson/logs/` (0755) and open
`logs/<session-id>.stderr.log` with `O_CREATE|O_WRONLY|O_APPEND`, 0644 (append — a later
resume of the same id keeps one log); assign the `*os.File` to `cmd.Stderr`; close it in
the waiter after reap.

### 4.8 Store: layout, ids, registry writes (SPEC §4, conventions §5)

`store.Store` is rooted at one checkout path. `EnsureLayout()` creates
`.gibson/{sessions,logs}` (0755). The registry is exactly the conventions §5 `state.json`
schema and status enum (`live|stopped|closed`); Go record:

```go
type Record struct {
    ID             string `json:"id"`
    Name           string `json:"name"`
    Type           string `json:"type"`
    Status         Status `json:"status"` // "live" | "stopped" | "closed"
    CreatedAt      string `json:"createdAt"`      // RFC 3339 UTC
    LastActivityAt string `json:"lastActivityAt"`
    PID            int    `json:"pid"`            // diagnostic only
}
```

Final M1 writes use process-local serialization plus a per-checkout cross-process lock.
Every full-file read-modify-write mutation reloads the latest registry while holding the
lock and completes its write-temp-then-`os.Rename` replacement before unlocking. This
prevents concurrent one-shot commands, or a command overlapping the later server, from
losing records while preserving atomic snapshots for readers. Chunk 4 establishes the
process-local and atomic-replacement shape; Chunk 6 completes the cross-process protocol.
What `run` writes, when:

1. at spawn: `Put(Record{status: live, pid, createdAt=lastActivityAt=now})`;
2. on accepted prompt and on each `message_end`: `Touch(id, now)` — `message_end` is
   the moment pi persists the finalized message to the session JSONL; pi emits no
   per-entry append event for normal conversation (§4.4, §4.11). (Atomic rename is
   cheap at one-shot scale; M2 may batch for long-lived sessions.)
3. at exit (normal, aborted, or crash): `SetStatus(id, stopped)` + pid zeroed. A
   completed one-shot is `stopped` — resumable later (SPEC §5.3.3), safe for the
   terminal escape hatch (SPEC §10.1). `closed` is reserved for explicit user close (M2+).

Registry rebuild-from-JSONL and the startup live→stopped orphan sweep are **not** built
here (M6, "registry lifecycle" per MILESTONES coverage map); the schema above is what
they will operate on.

### 4.9 `gibson run` UX

`gibson run <type> <message> [--checkout <name>]` (conventions §1). `cmd/run.go` only
parses arguments/flags and invokes the application workflow. `internal/app/run.go` owns:
locate workspace → `config.Load` (already validated) → resolve session type (unknown type
→ error listing configured types) → resolve target checkout (§4.10) →
`pisession.ResolvePiBin` + `pisession.CheckPiVersion` (every invocation that spawns pi
checks first; versions newer than the verified 0.82.x line log a Charm warning) →
`EnsureLayout` → `NewSessionID` → spawn → registry `live` → `Prompt` → stream loop →
shutdown (`Close`, registry `stopped`).

Output contract (pipeable stdout, human stderr):

- **stdout**: only assistant text — `message_update` events whose
  `assistantMessageEvent.type == "text_delta"`, written incrementally
  (`assistantMessageEvent.delta`); a trailing `\n` is added at the end if the text
  didn't end with one. Thinking deltas are not printed.
- **stderr**: session id + session file + log path (one line at start, one at exit);
  `tool_execution_start/end` as `[tool <name>] running` / `[tool <name>] done|error`
  lines; `extension_ui_request` with `method` `notify` as `[notify] <message>`; a
  blocking dialog method (`select|confirm|input|editor`) prints a loud warning —
  `gibson run` cannot answer dialogs (M5 owns them); pi auto-resolves only if the
  request carries a `timeout` (SPEC §6.4.3), otherwise Ctrl+C is the way out.

Interrupts: first SIGINT → send `abort`, keep consuming until `agent_settled` (the
aborted message with `stopReason:"aborted"` lands in the session file), then normal
shutdown, exit 130; second SIGINT → immediate `Close` (SIGTERM→5s→SIGKILL), exit 130.

Exit codes (M1-local CLI design; no later milestone consumes them): `0` — agent settled
without error; `1` — any gibson/pi error (bad config, unknown type/checkout, version
mismatch, spawn failure, command failure, premature pi exit, or a final assistant
message with `stopReason:"error"`); `130` — user interrupt.

Structure the application workflow as a small `internal/app` function with injected
process/session dependencies and an injectable signal channel, returning an outcome the
process boundary maps to exit codes 0/1/130. Tests drive interrupts without real signals;
the Cobra adapter does not own or retest lifecycle behavior.

### 4.10 Checkout resolution without enumeration

`--checkout <name>` resolves as `<workspaceRoot>/<name>` (name = directory basename,
matching conventions §3's checkout key), validated to exist and contain a `.git` entry
(file or dir — worktrees have a file). Full `git worktree list --porcelain` enumeration
is deliberately **not** pulled forward — MILESTONES pins it to M2; this join-and-verify
is sufficient for a named sibling checkout and lives in
`workspace.ResolveCheckout(workspaceRoot, name) (path string, err error)`. Default:
the launch checkout.

### 4.11 fakepi (conventions §9)

`internal/fakepi` is a `package main` Go program speaking enough of the RPC protocol to
carry all default-run automated tests. Invoked with `--version` it prints `0.82.1` to
stdout and exits 0 — this special case runs **before** any other argv validation because
`run` checks the version before every spawn and M2+'s serve-with-fakepi startup tests hit
the same gate. Otherwise it: reads
LF-JSONL commands from stdin; validates
that argv contains `--mode rpc`, `--session-id`, `--session-dir` (exits 2 with a stderr
message if not — an integration-level argv-assembly tripwire); answers `get_state`
(shape per rpc.md: `model`, `thinkingLevel`, `isStreaming`, `sessionFile`, `sessionId`,
`messageCount`, `pendingMessageCount`), `get_entries` (append order, honors `since`
strictly-after, invalid `since` → `success:false`, includes `leafId`),
`get_session_stats` (including `contextUsage`), `set_session_name` (appends a
`session_info` entry and emits `session_info_changed`), `prompt`/`steer`/`follow_up`
(responds accepted, then plays the scenario), `abort`.

It **writes a real v3 session JSONL** honoring `--session-id`/`--session-dir` — header
line `{"type":"session","version":3,"id":"<session-id>","timestamp":...,"cwd":...}`
then entries with 8-char-hex `id`/`parentId` chains — so gibson's storage,
header-matching (`FindSessionFile`), and later history-from-file paths are exercised
against real files. Appends emit **no** per-entry event, mirroring real pi (§4.4: pi
0.82.1 emits `entry_appended` only for extension custom entries; ordinary
user/assistant/toolResult persistence happens silently at `message_end`). No M1
scenario emits `entry_appended`; appended entries are observed via `get_entries` and
the JSONL file.

Scenario via env `FAKEPI_SCENARIO` (default `basic`); scenario definitions live in the
`internal/fakepi/scenarios` package as Go data (a `map[string]Scenario` of scripted
steps: delay / text delta / tool start-update-end / dialog / crash). M1 implements the
conventions-named scenarios:

- `basic` — `agent_start` → `message_start` → several `text_delta` `message_update`s →
  `message_end` → `agent_end` → `agent_settled`; the user entry is appended to the
  JSONL when the prompt is accepted and the assistant entry at `message_end`, with no
  event (matching pi).
- `slow_stream` — same with per-delta delays (abort/interrupt tests).
- `huge_entry` — one entry >1 MB written to the JSONL and returned as a single >1 MB
  `get_entries` response line, plus a delta containing literal U+2028/U+2029 inside
  the JSON string (framing proof).
- `crash_mid_stream` — exits 1 mid-stream after writing to stderr (unexpected-exit and
  stderr-capture proof).
- `dialog_confirm` — emits an `extension_ui_request` `confirm` and blocks until the
  matching `extension_ui_response` arrives on stdin, then finishes the stream (proves
  `RespondUI`'s write path now; M5 builds its dialog tests on this same scenario).

On abort: stop emitting deltas, emit `message_end` with `stopReason:"aborted"`, append
the aborted assistant entry, then `agent_end` → `agent_settled`, and answer the `abort`
command `success:true` (mirrors SPEC §6.2). On SIGTERM: exit 143 promptly.

`pitest.BuildFakePi(t) string` compiles `./internal/fakepi` once per test process
(package-level `sync.Once`, output in a shared temp dir) and returns the binary path to
use as `pi_bin`. `pitest.RequireRealPi(t) string` skips unless `GIBSON_TEST_REAL_PI=1`
and returns the resolved real `pi` path. M1 extends the existing `internal/testws`
helper by making the constructor variadic — `New(t testing.TB, opts ...Option) *WS` —
which is backward-compatible with every `testws.New(t)` call site and preserves
`WS{Root, Checkout}`, `WS.WriteConfig(t testing.TB, source string)`, and the default
workspace shape (temp grove-style root, `main/` checkout with `git init` + initial
commit, committed `gibson.toml` + `.gitignore` containing `.gibson/`) unchanged. New
options: `WithPiBin(path)` (points
`pi_bin` at fakepi), `WithSessionType(name, cfg)`, `WithSiblingCheckout(name)` (plain
`git worktree add`).

### 4.12 Real-pi gated tests

Gated behind `GIBSON_TEST_REAL_PI=1` (conventions §9), same test files. One gated test
avoids prompting: spawn real pi in a testws checkout, probe readiness and entries, set the
session name, verify the resulting entry and session header, and close cleanly with exit
143. A second gated test loads a temporary deterministic provider and handled extension
command, both local to the test: after an accepted real agent run, immediate `get_state`
must report streaming until the provider is released; after the extension handles a
command without a run, it must report idle. Both tests avoid network access and LLM cost.
A full prompted run against an external provider remains in the §8 agent workflow rather
than `go test`.

## 5. Implementation steps

1. `internal/testws/testws.go` — extend the existing helper with the §4.11 `Option`
   funcs (`WithPiBin`, `WithSessionType`, `WithSiblingCheckout`) via
   `New(t testing.TB, opts ...Option)`; existing `New(t)` calls and
   `WS.WriteConfig(t, source)` keep working. Everything else in M1 tests against it.
2. `internal/fakepi/scenarios/scenarios.go` + `internal/fakepi/main.go` — the fake pi
   per §4.11, including the `--version` → `0.82.1` special case ahead of the argv
   tripwire (framing per §4.2 rules on its own stdin reader; it is also a reference
   client of the protocol).
3. `internal/pitest/pitest.go` — `BuildFakePi`, `RequireRealPi`; smoke-test that the
   built fakepi answers `get_state` over a pipe and prints `0.82.1` for `--version`.
4. `internal/pisession/event.go`, `errors.go`, `rpc.go` — transport core + unit tests
   (`rpc_test.go`) against in-memory pipes: framing table (chunked writes, `\r\n`,
   embedded U+2028/U+2029, >1MB line, EOF without trailing `\n`), correlation
   (in-order/out-of-order replies, timeout, unmatched response dropped, `success:false`
   error surface), write serialization under concurrent commands.
5. `internal/pisession/argv.go` + `argv_test.go` — assembly table per §4.5
   (model/thinking present/absent, `extra_args` verbatim and last).
6. `internal/pisession/session.go` — `Config`, `Spawn`, lifecycle per §4.5–§4.7;
   `session_test.go` against fakepi: spawn+probe, prompt→settled event stream, abort
   mid-`slow_stream`, `huge_entry`, `crash_mid_stream` (pending command gets
   `ErrProcessExited`, `Done` fires, events channel closes last), `dialog_confirm` via
   `RespondUI`, stderr capture file contents, `GetEntries` since-cursor + invalid cursor
   → `ErrInvalidCursor`, `Close` idempotence and 143 exit.
7. `internal/store/` — `store.go`, `id.go`, `registry.go` + tests: id format/regex/
   collision-regeneration, layout creation, atomic write (crash-safe temp file), status
   transitions, `FindSessionFile` header matching against fakepi-written files.
8. `internal/workspace/checkout.go` — `ResolveCheckout` + test (worktree `.git` file
   case included).
9. `internal/app/run.go` — implement the §4.9 workflow. `run_test.go` owns the composed
   fakepi contract: streamed stdout, stopped registry state, sibling-checkout targeting,
   interrupt→abort outcome, and crash cleanup/logging. It reuses lower-layer guarantees
   instead of repeating their framing, argv, or registry assertion matrices.
10. `cmd/run.go` — add the Cobra adapter and wire it from `cmd/root.go`; `cmd/run_test.go`
    proves only argument/flag/application-input adaptation and outcome propagation. Keep
    process-exit mapping at the `main.go` boundary.
11. `internal/pisession/session_realpi_test.go` — gated no-network real-pi lifecycle and
    deterministic prompt-ordering tests (§4.12).
12. Run the §8 proof workflow end-to-end; fix what it flushes out.

## 6. Interfaces exposed to later milestones

Exactly the conventions §7 `pisession.Session` surface, plus M1-added exports (named
here so later plans may rely on them):

- `pisession.Event` = `{Type string; Raw json.RawMessage}` (conventions §7).
- `pisession.Config` = `{PiBin, SessionID, SessionDir, Cwd, Model, Thinking string;
  ExtraArgs []string; StderrPath string; Logger *log.Logger}`, where `log` is
  `charm.land/log/v2`.
- `pisession.Spawn(ctx, Config) (*Session, error)` — spawns, starts pumps, runs the
  `get_state` readiness probe.
- `pisession.Session` methods:
  - `Prompt(ctx, message, behavior string) error` (`behavior` ∈ `""|"steer"|"followUp"`,
    maps to `streamingBehavior`)
  - `StartPrompt(ctx, message, behavior string) (<-chan error, error)` writes the same
    prompt before returning its buffered acceptance-result channel, so signal handling can
    order `abort` after prompt submission while continuing to drain events
  - `Abort(ctx) error`
  - `GetState(ctx) (json.RawMessage, error)` (response `data` verbatim)
  - `GetEntries(ctx, since string) (entries []json.RawMessage, leafID string, err error)`
    (`ErrInvalidCursor` on pi `success:false` for a `since` miss — M2's SSE `reset` hook)
  - `GetSessionStats(ctx) (json.RawMessage, error)`
  - `SetSessionName(ctx, name string) error`
  - `RespondUI(id string, res UIResolution) error`, with
    `UIResolution{Value *string; Confirmed *bool; Cancelled *bool}` (json tags
    `value`/`confirmed`/`cancelled`, all `omitempty`, matching conventions §3's
    dialog-answer body; pointer `Cancelled` lets M5's validation distinguish absent
    from an explicit `false`)
  - `Events() <-chan Event` (buffered 256; owner must drain; closed after exit is fully
    recorded)
  - `Close(ctx) error` (SIGTERM→5s→SIGKILL), `Done() <-chan struct{}`,
    `ExitErr() error`, `PID() int`, `ID() string`
- `pisession.ErrInvalidCursor`, `ErrProcessExited`, `ErrCommandTimeout`;
  `pisession.ResolvePiBin(configured string) (string, error)` and
  `pisession.CheckPiVersion(ctx, bin) (VersionResult, error)` reused by `run`, with
  minimum/verified/newer behavior from §2.
- `store.Store` (`store.Open(checkoutPath)`): `EnsureLayout()`, `SessionsDir()`,
  `StderrLogPath(id)`, `NewSessionID() (string, error)`, `Put(Record)`,
  `SetStatus(id string, s Status)`, `Touch(id string, t time.Time)`,
  `Get(id) (Record, bool)`, `List() []Record`, `FindSessionFile(id) (string, error)`;
  `store.Record`, `store.Status` (`StatusLive/StatusStopped/StatusClosed`, persisted as
  conventions §5's `live|stopped|closed`).
- `workspace.ResolveCheckout(workspaceRoot, name string) (string, error)`.
- Test infra consumed by every later milestone: `pitest.BuildFakePi(t)`,
  `pitest.RequireRealPi(t)`, `testws.New(t, opts...)`, fakepi scenarios
  `basic|slow_stream|huge_entry|crash_mid_stream|dialog_confirm` via `FAKEPI_SCENARIO`.

No routes, no wire types, no SSE — M1 adds nothing to conventions §3/§4 surfaces.

## 7. Testing

All default tests run with `go test ./...`, no network, no LLM (conventions §9);
testify `require`/`assert`, table tests, `_test.go` next to code. Each behavior has one
primary owner: transport tests own byte/correlation contracts, process tests own RPC
lifecycle, store tests own persistence, `internal/app` owns the composed one-shot
workflow, and `cmd` owns only CLI adaptation. Higher layers prove wiring not duplicate
lower-layer assertion matrices.

Unit (no subprocess):
- Framing: byte-exact table per step 4 of §5 — the U+2028/U+2029 and >1MB cases are the
  SPEC §6.1.2 / §10.1-adjacent regression guards.
- Correlation: timeout → `ErrCommandTimeout`; late reply dropped; `success:false` error
  text propagation; concurrent senders serialized (race detector on: `go test -race`).
- Argv: exact order and verbatim `extra_args` (SPEC §3.2.4, conventions §6).
- Store: id format `^s-\d{8}-[a-z0-9]{6}$` + pi regex conformance; collision regen;
  atomic registry writes (interrupted-write simulation: temp file left behind is
  ignored); status transitions.

fakepi integration (default run):
- Full spawn→probe→prompt→settled flow; deltas concatenate to scenario text; session
  JSONL exists with header `id` == session id; `GetEntries` results match the file
  contents.
- Abort mid-`slow_stream`: stream stops, `stopReason:"aborted"` entry lands,
  `agent_settled` arrives, `Close` reaps cleanly.
- `crash_mid_stream`: pending command errors `ErrProcessExited`; `Done` then channel
  close ordering; stderr log captured.
- `dialog_confirm`: `RespondUI` releases the block (write path for M5).
- `internal/app` one-shot integration per step 9 of §5, including outcomes that the
  process boundary maps to exit codes 0/1/130; `cmd/run_test.go` checks adaptation only.

Real-pi gated (`GIBSON_TEST_REAL_PI=1`): the no-network lifecycle and deterministic
prompt-ordering tests of §4.12.

## 8. Agent-verified proof workflow

Run by an agent against a real build with real pi (MILESTONES M1 proof). Commands are
bash; run from the repo root `~/Code/github.com/jmcampanini/gibson/main` unless stated. Scratch space under `.sandbox/` (house convention).

1. **Build and default test suite (no network):**
   ```sh
   go vet ./... && go test -race ./...
   make build
   test -x build/gibson
   ```
   Expect: all tests PASS; the canonical project binary exists at `build/gibson`.
2. **Verify pi compatibility:**
   ```sh
   pi --version
   ```
   Expect: version ≥0.82.0. The 0.82.x line is verified; a later minor or major version
   is accepted and Gibson warns that it is unverified. Stop only for an older or
   unparseable environment.
3. **Scaffold a scratch grove workspace:**
   ```sh
   GIBSON=$PWD/build/gibson
   WS=$PWD/.sandbox/m1-proof/code/github.com/proof/scratch
   mkdir -p "$WS/main" && cd "$WS/main"
   git init -b main -q
   printf '[server]\nport = 7911\n\n[sessions.quick]\ndescription = "Quick one-off task"\n' > gibson.toml
   printf '.gibson/\n' > .gitignore
   git add -A && git commit -qm init
   ```
4. **Happy-path one-shot run:**
   ```sh
   "$GIBSON" run quick "Reply with exactly: GIBSON-M1-OK" ; echo "exit=$?"
   ```
   Expect: assistant text streams incrementally to stdout and contains `GIBSON-M1-OK`;
   stderr names the session id, session file, and stderr log; `exit=0`.
5. **Verify on-disk artifacts (SPEC §4.1, §5.1):**
   ```sh
   ls .gibson/sessions/*.jsonl
   SID=$(python3 -c "import json;d=json.load(open('.gibson/state.json'));print(list(d['sessions']))" | tr -d "[]' ")
   head -1 .gibson/sessions/*.jsonl | python3 -c "import json,sys;h=json.loads(sys.stdin.readline());print(h['type'],h['id'])"
   python3 -c "import json;d=json.load(open('.gibson/state.json'));s=list(d['sessions'].values())[0];print(d['version'],s['type'],s['status'],s['pid'])"
   test -f ".gibson/logs/$SID.stderr.log" && echo log-ok
   git status --porcelain
   ```
   Expect: exactly one session file; header line is `session <SID>` (header id equals
   the registry id, which matches `s-YYYYMMDD-xxxxxx`); registry prints
   `1 quick stopped 0`; `log-ok`; `git status` output empty (no `.gibson/` noise).
6. **Abort mid-stream terminates cleanly:**
   ```sh
   "$GIBSON" run quick "Count from 1 to 2000 slowly, one number per line." & PID=$!
   sleep 8 && kill -INT $PID
   wait $PID; echo "exit=$?"
   pgrep -fl -- "$PWD/.gibson/sessions" || echo no-orphans
   grep -c '"stopReason":"aborted"' .gibson/sessions/*.jsonl
   python3 -c "import json;d=json.load(open('.gibson/state.json'));print(all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values()))"
   ```
   Expect: `exit=130`; `no-orphans` (the pgrep is scoped to this checkout's
   `.gibson/sessions` in the pi argv, so unrelated pi processes elsewhere on the
   machine cannot fail the check); the aborted-run session file contains a
   `stopReason":"aborted"` message (grep ≥ 1 on one of the files); registry check
   prints `True`.
7. **Error paths and `--checkout`:**
   ```sh
   "$GIBSON" run nope "hi" ; echo "exit=$?"                     # expect exit=1, message lists "quick"
   git worktree add ../wt-x -b x -q
   "$GIBSON" run quick "Reply with exactly: WT-OK" --checkout wt-x ; echo "exit=$?"
   ls ../wt-x/.gibson/sessions/*.jsonl
   ```
   Expect: unknown type → exit 1 naming available types; the `--checkout` run exits 0,
   streams `WT-OK`, and its `.gibson/` lands inside `wt-x` (session self-contained with
   its worktree, SPEC §4.1.3).
8. **Gated real-pi protocol tests:**
   ```sh
   cd ~/Code/github.com/jmcampanini/gibson/main
   GIBSON_TEST_REAL_PI=1 go test ./internal/pisession/ -run RealPi -v
   ```
   Expect: PASS (skipped without the env var in step 1's run).

All steps passing is the M1 done signal.

## 9. Success criteria checklist

- [ ] SPEC §3.2.4 — `model`/`thinking` map to pi flags; `extra_args` appended verbatim,
      last, never parsed (argv table test + fakepi tripwire).
- [ ] SPEC §4.1 — `.gibson/{sessions,state.json,logs/<id>.stderr.log}` created per
      layout; pi JSONL is ground truth; registry holds only gibson metadata with
      statuses `live|stopped|closed` (conventions §5 schema exactly).
- [ ] SPEC §4.1.3 — `--checkout` sessions are self-contained in that worktree (proof 7).
- [ ] Repository hygiene — proof workspaces commit `.gitignore` coverage for `.gibson/`;
      `git status --porcelain` stays empty after a run.
- [ ] SPEC §5.1.1 — exact argv shape and cwd = target checkout (proof 4/5: one process,
      session file in that checkout).
- [ ] SPEC §5.1.2 — gibson-assigned id matches pi's id rules; id format per
      conventions §5.
- [ ] SPEC §5.1.3 — single writer: fresh id per run, one process per id, no re-spawn
      paths in M1.
- [ ] SPEC §5.1.4 — stderr captured to `logs/<id>.stderr.log` (proof 5; crash test
      shows content).
- [ ] SPEC §5.2.2 (M1 slice) — SIGTERM-first shutdown; pi exits 143 (gated test).
- [ ] SPEC §5.4.1 — version checked before any spawn; <0.82.0 fails, 0.82.x is verified,
      and later minor or major versions proceed with a Charm Log warning naming found/verified versions.
- [ ] SPEC §6.1.1–6.1.3 — LF-only framing with `\r` strip; U+2028/U+2029-safe; >1MB
      lines; blocking writes/reads as backpressure (unit tables + `huge_entry`).
- [ ] SPEC §6.2 — `prompt` (accepted-not-completed semantics, `streamingBehavior`
      plumbed), `abort` (aborted message `stopReason:"aborted"`), `get_state`,
      `get_entries` (`since` cursor; invalid → `ErrInvalidCursor`),
      `get_session_stats`, `set_session_name` all implemented per rpc.md.
- [ ] Conventions §6 — spawn sequence (spawn → `get_state` probe → prompt), command ids
      `c-<n>`, bounded writes, one 30s ordinary-command budget, unbounded prompt-response
      waiting, single-goroutine writes, `ReadBytes` pump, and shutdown
      SIGTERM→5s→SIGKILL.
- [ ] Conventions §7 — `pisession.Session` exports exactly the pinned method set (plus
      the M1-added names in §6 above).
- [ ] Conventions §9 — fakepi carries the default suite (no LLM/network in
      `go test ./...`); real-pi tests gated by `GIBSON_TEST_REAL_PI=1`; scenarios
      include `dialog_confirm` for M5 reuse.
- [ ] MILESTONES M1 proof — `gibson run` streams output, settles, leaves a session
      JSONL + registry entry, and aborts cleanly mid-stream (proof steps 4–6 pass,
      agent-run).

## 10. Explicitly out of scope

Deferred to their owning milestones (do not build, even partially, beyond what §6 lists):

- Any new HTTP behavior: REST session routes, SSE, `session.Manager`/Broker, keep-alive
  multi-session ownership, worktree enumeration via `git worktree list --porcelain` — M2.
- Frontend work of any kind — M3/M4.
- Dialog bridging/UX beyond `RespondUI` + the `dialog_confirm` fixture scenario
  (`run` only warns on blocking dialogs) — M5; the `test/fixtures/extensions/confirm-gate.ts`
  fixture itself is created by M5's plan.
- Resume-on-demand respawn (`stopped`/`closed` → live), startup orphan sweep
  (live→stopped), registry rebuild from `sessions/*.jsonl`, cross-checkout session
  listing — M6.
- `steer`/`follow_up` UX (the `Prompt` behavior parameter exists and is tested at the
  transport level; no CLI surface exposes it) — M2/M4.
- Multi-device bind, slow-client backpressure verification, docs pass — M7.
- Non-pinned CLI additions (e.g. `run --name`, JSON output modes): not in conventions
  §1; would need a conventions change first.
