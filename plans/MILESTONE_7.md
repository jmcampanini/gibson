# MILESTONE_7 — v1 hardening and full acceptance

Status: **provisional**. This forecast requires the plan-gate review in
[PROCESS.md](PROCESS.md) before it becomes the active milestone contract.

Conforms to MILESTONE_CONVENTIONS.md (binding) and SPEC.md (normative). Section numbers cited
as "SPEC §n" and "CONV §n". This is the final milestone: its proof IS SPEC §9.5's
seven-step agent-verified acceptance workflow, so passing this plan's proof workflow is
the definition of done for gibson v1.

---

## 1. Goal & capability

**You can now:** call it v1 — SPEC.md's acceptance workflow passes end-to-end.

Per MILESTONES.md M7: scope is whatever the acceptance run flushes out, plus the
deliberately-deferred edges — multi-device via non-localhost `bind`, slow-client
backpressure verification, pi compatibility-policy behavior, the `.gibson/`
self-containment audit, and a docs pass (README: install, configure, run).

---

## 2. Preconditions

**M0 is complete.** M1–M6 are implemented per their own plans. M7 builds no new product
features; it verifies, hardens, and documents. Concretely, M7 consumes:

**From M1** — `internal/pisession` per CONV §6/§7: `pisession.Session` with `Prompt`,
`Abort`, `GetState`, `GetEntries`, `GetSessionStats`, `SetSessionName`, `RespondUI`,
`Events()`, `Close(ctx)`; LF-only `ReadBytes('\n')` framing; `c-<n>` command correlation;
stderr capture to `.gibson/logs/<id>.stderr.log`; `internal/store` registry (CONV §5);
`gibson run` CLI; `internal/fakepi` + `internal/pitest` (`pitest.BuildFakePi(t)`,
`pitest.RequireRealPi(t)`, `FAKEPI_SCENARIO`) and `internal/testws` (`testws.New(t)`).

**From M2** — the full REST route table and SSE contract of CONV §3/§4:
subscribe-first connect algorithm, 256-event bounded per-client buffer with
overflow-disconnect, `:hb` heartbeat every 15s, `Last-Event-ID`/`?since=` cursor replay,
`reset` on invalid cursor; `session.Manager` and `session.Broker` (CONV §7);
`session.StreamEvent`.

**From M3–M4** — the SPA: `SessionListPage`, `LaunchFlow`, `SessionPage`, `MessageList`,
`StreamingText`, `ThinkingBlock`, `ToolCallCard`, `CustomMessageCard`, `Composer`,
`ContextMeter` (CONV §8); `sessionReducer` folding snapshot + SSE through one code path.

**From M5** — dialog bridging (SPEC §6.4, §7.3.2): `DialogModal`, `ToastHost`,
`StatusStrip`; `POST /api/sessions/{id}/dialogs/{dialogId}` with first-answer-wins /
409 `dialog_already_answered`; `blocked-on-dialog` wire status surfaced loudly in the
session list (SPEC §10.3); the fixture `test/fixtures/extensions/confirm-gate.ts`
(calls `ctx.ui.confirm()` before tool execution).

**From M6** — cross-checkout session list, close/reopen, resume-on-demand respawn with
the same `--session-id` (SPEC §5.3), startup orphan sweep (`live` → `stopped`, pid
zeroed; CONV §5), history-from-JSONL for non-live sessions (CONV §3).

If any precondition is missing or diverges from CONV, that is an M7 defect to fix during
the triage loop (step 5.9), not a reason to redesign.

Carried from M1 consolidation (resolve at this plan's gate): `internal/testws`
hand-mirrors the config schema and its server encoder omits `bind`, so a scratch
workspace with a non-localhost bind is inexpressible today. Add `WithBind` (or switch to
encoding from `internal/config` types) before the multi-device proof.

---

## 3. Deliverables

New or extended files (repo-relative):

| Path | What |
|---|---|
| `README.md` | The v1 docs pass: install, configure, run (outline in §4.8). |
| `internal/app/serve_test.go` (extend) | Startup error matrix (SPEC §9.1.b), bind matrix (SPEC §3.2.2), full-lifecycle `.gibson/` self-containment audit test. |
| `internal/pisession/version_test.go` (extend) | Verified/unverified/rejected compatibility table; real-pi-gated `pi --version` output-parse guard. |
| `internal/httpapi/backpressure_test.go` | Slow-client disconnect + fast-client isolation under flood (SPEC §7.2.4). |
| `internal/httpapi/heartbeat_test.go` | `:hb` emission on idle streams with injectable interval (SPEC §7.2.3). |
| `internal/session/manager_test.go` (extend) | Single-writer guard: no second spawn for a live session id (SPEC §5.1.3). |
| `internal/fakepi/scenarios/flood` (+ scenario wiring) | Scenario emitting a large event burst to force buffer overflow. |
| `internal/fakepi/` (small extension) | `FAKEPI_SPAWN_LOG` env: append one line per process start (test-infra only, §4.10). |
| `internal/httpapi/` (small change, if needed) | Heartbeat interval as an injectable field defaulting to 15s (wire behavior unchanged). |

Plus: fixes for whatever the acceptance run flushes out (tracked in the triage loop,
step 5.9). No new routes, wire fields, event types, statuses, or persisted schema —
M7 MUST NOT extend the CONV §3/§4/§5 surfaces.

---

## 4. Design & rationale

### 4.1 M7 is a verification milestone

Every M7 line item is a SPEC claim that earlier milestones deliberately deferred proving
(MILESTONES.md coverage map rows "§9 full workflow at M7" and "§10 churn/version at
M7"). The shape of the work is therefore: (a) convert each deferred claim into an
automated fakepi test where a fake suffices, (b) expand SPEC §9.5 into an exact runbook
executed with real pi + browser automation, (c) run it, fix what breaks, re-run until
green. Hardening fixes stay inside the existing seams — if a fix would require a new
wire field or route, it is out of scope and must be raised as an open question instead
(CONV §10).

### 4.2 pi compatibility policy (SPEC §5.4.1, §10.5)

At `gibson serve` startup, run
`<pi_bin> --version`, require semantic version 0.82.0 or newer, treat the 0.82 minor line
as verified, and allow later minor or major versions while logging an unverified-version warning that
names the found, verified, and minimum versions. M7 locks that behavior with tests:

- **Parse rule:** take the first token-boundary semantic version in the output, allowing
  an optional `v` prefix plus valid prerelease/build suffixes (tolerates both bare
  `0.82.1` and `pi version v0.82.1 (release)`). A real-pi-gated test asserts the parse
  against the actually-installed binary's output, so a future pi that
  changes its `--version` format fails loudly in the gated suite rather than silently in
  the field (SPEC §10.5).
- **Compatibility table** (pure unit test on parse + compare): accept verified
  `0.82.0`, `0.82.1`, and `0.82.99`; accept `0.83.0` and `1.0.0` as unverified; reject
  `0.81.9`, `0.82.0-rc.1`, empty output, and non-semver garbage. Too-old errors name the
  found version and minimum 0.82.0; unparseable output remains a distinct failure.
- **Startup path tests:** a shim answering `0.81.9` makes `gibson serve` exit non-zero
  with an error naming the found and minimum versions. A shim answering `0.99.0` is
  allowed to serve and emits the unverified-version warning. An absent/non-executable
  shim proves *missing pi* remains distinct from *too-old pi* (SPEC §9.1.b).

### 4.3 Non-localhost bind (SPEC §3.2.2, §9.1.a; BACKGROUND #14)

Two properties to prove, both with fakepi (no LLM):

1. **Default is loopback-only.** With `bind` unset, `GET /api/health` succeeds on
   `127.0.0.1:<port>` and the same request against a non-loopback interface address of
   this host is refused (connection refused/timeout).
2. **Configured bind is honored.** With `bind = "0.0.0.0"`, both the loopback and the
   non-loopback interface address serve `/api/health` and the SPA shell.

The test discovers a non-loopback IPv4 via `net.Interfaces()`; if the host has none, it
skips with an explicit message (never silently passes). macOS caveat: the application
firewall may prompt on first non-loopback listen; the runbook notes this and the
automated test binds to the discovered concrete interface IP (not `0.0.0.0`) as a
fallback if `0.0.0.0` is blocked, asserting the same reachability property.

### 4.4 Slow-client backpressure (SPEC §7.2.4, §10.2; CONV §4.3)

The contract: a client whose 256-event Broker buffer overflows gets its stream closed;
the pi stdout pump and other clients are never blocked. The subtlety in testing it is
kernel socket buffering — a non-reading client does not immediately block the server's
writes, so small events would drain into TCP buffers without ever filling the Broker
channel. Design:

- **`flood` scenario** in `internal/fakepi/scenarios/`: on `prompt`, emit ~600
  `message_update` events whose `text_delta` payloads are ~8 KiB each (≈5 MB total —
  comfortably exceeding default macOS/Linux socket buffers), with an `entry_appended`
  every 50 events, ending with the finalized assistant entry, `agent_end`,
  `agent_settled`. Written like every scenario: real v3 session JSONL honoring
  `--session-id`/`--session-dir` (CONV §9).
- **Test choreography** (`internal/httpapi/backpressure_test.go`): start the server on
  fakepi+flood via `testws.New(t)`; open SSE client **A** with raw `net/http`, read the
  response headers, then stop reading entirely; open SSE client **B** that reads
  normally; POST the triggering message. Assert:
  - B receives every `entry` event (ids match the session JSONL) and the final
    `agent_settled`-adjacent events within a hard deadline (e.g. 30s) — proving A's
    stall never propagated to the pump or to B (SPEC §10.2's "stalled phone").
  - A's connection is closed by the server: a subsequent read on A's body returns
    EOF/reset well before A could have consumed the full burst.
  - `GET /api/health` still answers 200 afterwards.
  - **Recovery:** reconnect A with `Last-Event-ID` = the last entry id it actually
    processed; assert gapless, duplicate-free entry replay (the CONV §4.3 reconnect
    path is the entire recovery story).

### 4.5 `.gibson/` self-containment audit (SPEC §4.1.3, §9.5 step 7)

Claim: a full gibson lifecycle writes nothing outside `<checkout>/.gibson/` — not in the
checkout tree, not at the workspace root, not in sibling worktrees, and (because
`--session-dir` is always passed) not in pi's default `~/.pi/agent/sessions/`.

- **Automated (fakepi):** in `internal/app/serve_test.go`, build a `testws` workspace,
  take a recursive file inventory of the workspace root (pruning `.git/`), then run the
  full lifecycle through REST: create session → message → then, **with the session still
  live**, SIGKILL the serve process (no graceful shutdown — CONV §6's shutdown path
  would itself write `stopped`, so an unclean kill is what leaves the stale `live`
  registry entry the sweep exists for) → start server, asserting the startup sweep
  marked the entry `stopped` (SPEC §5.3.2) → message (resume from `stopped`) → close.
  Re-inventory and diff: every added/modified path MUST be under
  `<checkout>/.gibson/`. This catches gibson-side strays (temp files, logs in cwd,
  registry misplacement).
- **Real-pi (runbook step 7):** the same inventory-diff on the scratch workspace, plus
  `git status --porcelain` empty in both checkouts (proves SPEC §4.2's committed
  `.gitignore` entry actually covers everything gibson produced), plus
  `find ~/.pi/agent/sessions -name "*<session-id>*"` empty (proves pi honored
  `--session-dir` and the session truly lives with its worktree).

Allowed exceptions, by construction: `gibson.toml` and the `.gitignore` line are
committed repo citizens created at workspace setup, not by Gibson (SPEC §3.1.1,
§4.2.1).

As part of this audit, re-evaluate whether log creation should use `O_NOFOLLOW` and
whether directory setup needs stronger protection than the M1 `Lstat`-then-`Chmod`
checks. M1's run path validates the layout before spawn, so this is defense in depth
against same-user filesystem races rather than a prerequisite for the one-shot path.

### 4.6 Single-writer guard (SPEC §5.1.3, §10.1)

Gibson must never have two processes on one session id. The Manager's live-process map
is the guard: `Send` to a live session routes to the existing `pisession.Session`;
respawn happens only from `stopped`/`closed` (SPEC §5.3.3). To make "exactly one spawn"
observable, fakepi gains `FAKEPI_SPAWN_LOG`: when set, append one
`<pid> <session-id>\n` line at startup. The test creates a session, fires several
concurrent `POST .../message` calls plus a concurrent `GET .../history`, and asserts the
spawn log has exactly one line; then close → message → exactly two lines (the legal
resume respawn). The human-facing half of §10.1 (terminal escape hatch only for
stopped/closed sessions) is a README warning (§4.8), not code.

### 4.7 Heartbeat verification (SPEC §7.2.3; CONV §4.1)

The 15s `:hb` comment interval is wire-pinned but too slow for a default test run. If
M2 hard-coded it, M7 makes the interval an unexported field on the SSE handler config
in `internal/httpapi`, defaulting to 15s (no wire change, no new seam). If M2 placed
the ticker inside `internal/session`'s Broker instead, the interval is plumbed through
the handler's constructor so the `internal/httpapi`-package test can still set it —
either way, no exported API is added.
`internal/httpapi/heartbeat_test.go` sets ~50ms and asserts ≥2 `:hb` comment lines on an
idle stream and that `:hb` never carries an `id:` line. The runbook additionally
observes a real `:hb` within 20s via `curl -N` (step E3).

### 4.8 README (the docs pass)

`README.md` at repo root, written for a user who has pi installed and has never seen
SPEC.md. Outline (binding for the implementer; prose is theirs):

1. **What gibson is** — one paragraph: Go binary, localhost web UI for pi sessions,
   one server per grove-style workspace; browser tabs are disposable viewers.
2. **Requirements** — pi 0.82.0 or newer on `$PATH` (or `pi_bin`), git, a grove-style
   workspace; for building from source: Go 1.26 + Node.
3. **Install** — build from source with `cd web && npm ci`, then `make build`; the
   canonical artifact is `build/gibson` with the SPA embedded via `go:embed`. Note that
   `go install` only works from a tree with `web/dist` built.
4. **Configure** — full `gibson.toml` example (SPEC §3.2 schema verbatim: `server.port`
   required, `server.bind` default `127.0.0.1`, `pi_bin`, `[sessions.<name>]` with
   `description`/`model`/`thinking`/`extra_args`); the committed `.gitignore` line
   `.gibson/`; note that `extra_args` is the verbatim escape hatch to pi's full flag
   surface.
5. **Run** — `gibson serve` from inside a checkout; open `http://127.0.0.1:<port>`;
   `gibson serve --dev` for Vite hot reload; `gibson run <type> <message>` as the
   headless debugging tool.
6. **Multi-device & security** — setting `bind` to a tailnet/LAN address; explicit
   warning: gibson has **no auth** (SPEC §1.2) — whatever can reach the bind address
   controls your sessions and your checkout.
7. **Where sessions live** — `<checkout>/.gibson/` layout; sessions die with their
   worktree by design (SPEC §4.1.3); statuses idle/streaming/blocked-on-dialog/
   stopped/closed; restart = resume-on-demand.
8. **Single-writer warning** — never open a live gibson session with terminal `pi`;
   `pi --session-dir .gibson/sessions --resume` is safe only for stopped/closed
   sessions (SPEC §10.1).
9. **pi version support** — minimum 0.82.0; the 0.82 minor line is verified and newer
   versions run with an unverified-version warning; `pi_bin` selects the executable
   checked at startup (SPEC §5.4, §10.5).
10. **Limitations (v1)** — SPEC §1.2 non-goals in user terms, plus the `ui.custom()`
    degradation: extensions whose core value is a custom TUI silently lose it over RPC;
    session types intended for web use should omit them (SPEC §10.4, §6.4.4).
11. **Troubleshooting** — the distinct startup errors (§9.1.b) and what each means;
    where stderr logs live.

### 4.9 SPEC §10 watch-out sweep methodology

A code-inspection + test cross-reference pass, executed as implementation step 5.8. For
each watch-out: name the enforcing code site and the test that would fail if it
regressed. Results recorded in the §9 checklist here. Any watch-out found unenforced is
a triage-loop defect.

### 4.10 Test-infra extensions, and what M7 does not decide

`flood` (scenario) and `FAKEPI_SPAWN_LOG` (env) extend fakepi's explicitly open-ended
test surface (CONV §9 lists scenarios as "e.g."); the heartbeat-interval field is an
unexported knob. None are wire, registry, or seam changes, and no later milestone
exists to consume them. Nothing in M7 requires a seam CONV leaves unpinned; there are
no open questions to escalate.

---

## 5. Implementation steps

Ordered; paths repo-relative.

1. **Preflight.** `cd web && npm ci`; `cd .. && make build`; `go test ./...` — the
   canonical `build/gibson` artifact must exist and the prior milestone suites must be
   green before hardening starts. Fix any drift from CONV found here as triage defects (record in step
   9's list).
2. **Compatibility-policy tests** (§4.2): extend `internal/pisession/version_test.go`
   with the verified/unverified/rejected table (test-accessible parse + compare function
   in `internal/pisession/version.go`); add the real-pi-gated output-parse test using
   `pitest.RequireRealPi(t)`.
3. **Startup error matrix** (SPEC §9.1.b) in `internal/app/serve_test.go`: five cases,
   each asserting a *distinct* error string — (a) missing `gibson.toml`, (b) invalid
   `gibson.toml` naming the bad field, (c) occupied port (pre-`net.Listen` the port,
   expect exit, no auto-increment per SPEC §3.2.1), (d) missing pi binary
   (`pi_bin = "/nonexistent"`), (e) too-old pi version (shim from step 2). Add a sixth
   non-failing case proving a newer unverified pi logs a Charm Log v2 warning and serves.
4. **Bind matrix** (§4.3) in `internal/app/serve_test.go`: default-loopback negative
   probe; `bind = "0.0.0.0"` (fallback: concrete interface IP) positive probe; skip-with-reason
   when no non-loopback interface exists. Fakepi-backed; asserts `/api/health` and the
   SPA shell (`GET /` contains the app root div).
5. **Backpressure + heartbeat** (§4.4, §4.7): add `internal/fakepi/scenarios/flood` and
   its wiring; write `internal/httpapi/backpressure_test.go`; make the heartbeat
   interval injectable if it is not already; write `internal/httpapi/heartbeat_test.go`.
6. **Single-writer guard** (§4.6): add `FAKEPI_SPAWN_LOG` to `internal/fakepi`; extend
   `internal/session/manager_test.go` with the one-spawn / legal-resume-respawn test.
7. **Self-containment audit test** (§4.5) in `internal/app/serve_test.go`:
   inventory-diff over the full fakepi lifecycle including an unclean kill while the session is live, a
   restart that demonstrably runs the orphan sweep, and resume from `stopped`.
8. **SPEC §10 sweep** (§4.9): for each of §10.1–§10.5, record enforcing code site +
   guarding test:
   - §10.1 single-writer → Manager live-map guard; test from step 6; README §4.8 item 8.
   - §10.2 SSE hygiene → Broker bounded buffer + overflow disconnect + heartbeat; tests
     from step 5.
   - §10.3 blocked-visibility → `blocked-on-dialog` derivation (CONV §3) and the loud
     `SessionListPage` treatment; asserted live in runbook step 4.
   - §10.4 `custom()` degradation → no code (pi returns `undefined` internally; nothing
     crosses the wire); README §4.8 item 10 documents it.
   - §10.5 churn → compatibility policy (step 2); verbatim `json.RawMessage`
     forwarding (CONV §2 — spot-check no handler re-models pi payloads); docs read from the installed
     package (this plan's own sourcing).
   Fix any gap found; add the missing test alongside the fix.
9. **Triage loop.** Execute the §8 runbook end to end with real pi. For every failure:
   record (symptom → SPEC § → fix), fix within existing seams, add a fakepi regression
   test when the failure is fake-able, re-run the full runbook from the top. Repeat
   until all steps pass in a single uninterrupted run.
10. **README.md** (§4.8). Written last so it documents observed, not intended, behavior
    (exact error texts, real startup output).
11. **Final gate.** `go test ./...` green; `GIBSON_TEST_REAL_PI=1 go test ./...` green
    on this machine; one final clean §8 runbook pass; §9 checklist fully ticked.

---

## 6. Interfaces exposed to later milestones

None — M7 is the terminal milestone. No new routes, wire event types, statuses,
registry fields, or exported Go APIs are added (CONV §3/§4/§5/§7 are frozen as shipped).
Artifacts left behind for post-v1 work: `README.md`; the `flood` fakepi scenario;
`FAKEPI_SPAWN_LOG`; the heartbeat-interval test knob; and the §8 runbook as the
repeatable regression harness for future releases.

---

## 7. Testing

**Unit (no subprocess):**
- `internal/pisession/version_test.go`: parse/compare table — accepts `0.82.0/1/99` as
  verified, accepts `0.83.0` and `1.0.0` as unverified, and rejects `0.81.9`, the
  minimum prerelease, empty, and garbage; too-old errors name found + minimum
  (SPEC §5.4.1).

**fakepi integration (default `go test ./...`, no network/LLM — CONV §9):**
- `internal/app/serve_test.go`: startup error matrix (5 distinct errors) plus the
  non-failing later-version warning case; bind matrix (SPEC §3.2.2); self-containment
  lifecycle audit (SPEC §4.1.3) including unclean kill with the session live + swept
  restart + resume.
- `internal/httpapi/backpressure_test.go`: flood scenario; slow client disconnected on
  buffer overflow, fast client complete and gapless within deadline, server healthy
  after, slow client recovers via `Last-Event-ID` replay with no gap/duplicate
  (SPEC §7.2.4, §10.2; CONV §4.3).
- `internal/httpapi/heartbeat_test.go`: `:hb` at injected interval on idle stream; no
  `id:` on heartbeats (SPEC §7.2.3; CONV §4.1).
- `internal/session/manager_test.go`: exactly one spawn per live session under
  concurrent sends; exactly one respawn after close (SPEC §5.1.3, §5.3.3).

**Real-pi gated (`GIBSON_TEST_REAL_PI=1`, via `pitest.RequireRealPi(t)`):**
- `internal/pisession/version_test.go`: parse the installed `pi --version` output;
  assert it satisfies the minimum-version policy and classify it as verified or
  unverified (guards SPEC §10.5 format drift).

**Agent-verified:** the §8 runbook (real pi, real LLM, browser automation) — the
acceptance itself. Not part of any `go test` target.

---

## 8. Agent-verified proof workflow

This is SPEC §9.5 expanded into an executable runbook, plus M7's deferred-edge checks
(E1–E4). Run by an agent with browser automation (e.g. the agent-browser CLI: navigate,
click, type, read DOM text, screenshot). Requirements: real pi 0.82.0 or newer on
`$PATH` with a working LLM provider; macOS/Linux. The 0.82 minor line is verified;
later minor or major versions are allowed with the startup warning. All seven numbered steps map 1:1
to SPEC §9.5.

Environment (fish-agnostic; plain sh):

```sh
REPO=~/Code/github.com/jmcampanini/gibson/main
GIBSON="$REPO/build/gibson"         # canonical project artifact
SB="$REPO/.sandbox/m7-accept"        # temp storage per house rules
WS="$SB/ws/demo"                     # scratch grove-style workspace root
PORT=7391
BASE="http://127.0.0.1:$PORT"
```

### Step 1 — scratch workspace (SPEC §9.5.1)

```sh
rm -rf "$SB" && mkdir -p "$WS"
cd "$REPO/web" && npm ci
cd "$REPO" && make build
test -x "$GIBSON"

git init -b main "$WS/main"
cd "$WS/main"
printf '.gibson/\n' > .gitignore
echo "hello gibson" > notes.txt
cat > gibson.toml <<EOF
[server]
port = $PORT

[sessions.gated]
description = "Demo with confirm gate before tools"
extra_args = ["-e", "$REPO/test/fixtures/extensions/confirm-gate.ts"]

[sessions.quick]
description = "Plain quick task"
EOF
git add -A && git commit -m "init"
git worktree add ../wt-b -b wt-b          # the second worktree (SPEC §2.1)
find "$WS" -path '*/.git' -prune -o -type f -print | sort > "$SB/inventory-before.txt"
```

Assert: `$WS` contains `main/` and `wt-b/`; `git -C "$WS/main" worktree list` shows
both; commit includes `gibson.toml` and `.gitignore`.

### Step 2 — launch gibson, open the UI (SPEC §9.5.2)

```sh
cd "$WS/main" && "$GIBSON" serve > "$SB/serve.log" 2>&1 &
sleep 1
curl -sf "$BASE/api/health"               # expect {"ok":true,"version":...}
curl -s  "$BASE/api/config/session-types" # expect gated + quick
curl -s  "$BASE/api/checkouts"            # expect main (isPrimary) + wt-b
```

Assert: health OK; both session types and both checkouts listed; `serve.log` shows the
bind address. Browser: open `$BASE/`, assert the session-list page renders with an empty
list and a new-session action.

### Step 3 — create a session, watch it stream (SPEC §9.5.3)

Browser: start the launch flow; select type **gated**, checkout **main**, name
`accept-1`, first message:
`List the files in this repository using your tools, then summarize notes.txt in one sentence.`
Submit.

Assert (browser): navigation to the session view; user message rendered; assistant
activity visible (streamed text and/or the confirm dialog). Do **not** assert
incremental text growth here: the first message deliberately triggers a tool, and
`confirm-gate.ts` blocks before tool execution, so the model may emit little or no
visible text before the session goes `blocked-on-dialog`. The token-by-token growth
assertion (SPEC §9.2.b) runs in Step 5 on a tool-free stream that cannot block.
Assert (REST):

```sh
SID=$(curl -s "$BASE/api/sessions" | jq -r '.sessions[0].id')
echo "$SID" | grep -Eq '^s-[0-9]{8}-[a-z0-9]{6}$'          # CONV §5 id format
curl -s "$BASE/api/sessions" | jq -r '.sessions[0].status'  # streaming (or blocked-on-dialog already)
pgrep -f -- "--session-id $SID" | wc -l                     # exactly 1 pi process
```

Assert the pi process's cwd is `$WS/main` (`lsof -p <pid> | grep cwd` on macOS) —
SPEC §9.2.a.

### Step 4 — extension dialog: block, surface loudly, answer, proceed (SPEC §9.5.4)

The first message triggers a tool, so `confirm-gate.ts` fires `ctx.ui.confirm()` before
execution.

Assert while the dialog is pending:
- Browser: a modal is visible with the confirm-gate title and confirm/deny controls.
- `curl -s "$BASE/api/sessions" | jq -r '.sessions[0].status'` → `blocked-on-dialog`.
- Browser: the session **list** page (second tab or navigate back) shows `accept-1`
  with the visually-loud blocked treatment (screenshot as evidence — SPEC §10.3).

Answer: click confirm/allow in the modal. Assert: modal closes; a tool card appears and
finalizes with the file listing; the assistant's final text summarizes `notes.txt`
(mentions its content, e.g. "hello gibson"); status returns to `streaming` then `idle`
after settle. Cross-check `GET $BASE/api/sessions/$SID/history` → `pendingDialog` is
`null` and entries include the tool result.

### Step 5 — second client mid-stream: snapshot + cursor replay (SPEC §9.5.5)

From client 1's composer send:
`Without using tools, write the numbers 1 to 40, one per line, then a one-line summary.`

While status is `streaming`, open a **second browser context** (separate tab/profile)
at the session URL. Assert:
- Client 2 immediately renders the identical prior conversation (same message count and
  identical last-finalized-message text as client 1 — snapshot + replay, SPEC §9.3.b).
- Client 2's streaming region continues growing live (two DOM polls ~1s apart).
- Client 1's streaming region likewise grows across two DOM polls ~1s apart — this is
  the token-by-token incremental-render assertion (SPEC §9.2.b) deferred from Step 3,
  performed on a stream the confirm gate cannot block.
- After settle, the final assistant text is byte-identical in both clients, and client
  2 shows no duplicated messages (message count equal to client 1's).

### Step 6 — kill, restart, resume with full context (SPEC §9.5.6)

```sh
pkill -9 -f "$GIBSON serve" ; sleep 2   # unclean kill — no graceful shutdown, so the
                                         # registry keeps a stale `live` entry (MILESTONE_6 step 8)
pgrep -f -- "--session-id $SID" | wc -l    # 0 — pi died with the server (SPEC §5.3.1)
jq -e --arg sid "$SID" '.sessions[$sid].status == "live"' \
  "$WS/main/.gibson/state.json"            # the orphan under test survived the kill
cd "$WS/main" && "$GIBSON" serve >> "$SB/serve.log" 2>&1 &
sleep 1
grep -i sweep "$SB/serve.log"              # startup sweep log line (SPEC §5.3.2)
curl -s "$BASE/api/sessions" | jq -r '.sessions[0].status'   # stopped (orphan sweep, SPEC §5.3.2)
```

Browser: reload the session list — `accept-1` listed as **stopped**; open it — full
history renders (served from the JSONL, CONV §3). Send:
`Earlier you summarized notes.txt — repeat that summary from memory, without tools.`
Assert: status transitions to `streaming`; a **new** pi process exists for `$SID`
(pgrep shows 1, different pid); the response correctly recalls the earlier summary
content ("hello gibson") — proving pi respawned with the same `--session-id` and
reconstructed context (SPEC §9.4.a, §9.2.d).

### Step 7 — filesystem truth and git silence (SPEC §9.5.7)

Close the session first (browser close action, or
`curl -X POST "$BASE/api/sessions/$SID/close" -d '{}'`); assert status `closed` and the
pi process gone. Then:

```sh
grep -l "$SID" "$WS/main/.gibson/sessions/"*.jsonl          # session JSONL exists
test -f "$WS/main/.gibson/logs/$SID.stderr.log"             # stderr log exists
test -f "$WS/main/.gibson/state.json"                       # registry exists
[ -z "$(git -C "$WS/main" status --porcelain)" ]            # no .gibson/ noise
[ -z "$(git -C "$WS/wt-b" status --porcelain)" ]            # sibling worktree clean
find "$WS" -path '*/.git' -prune -o -type f -print | sort > "$SB/inventory-after.txt"
comm -13 "$SB/inventory-before.txt" "$SB/inventory-after.txt" \
  | grep -v "^$WS/main/.gibson/" | wc -l                    # 0 — self-containment
find ~/.pi/agent/sessions -name "*$SID*" 2>/dev/null | wc -l  # 0 — pi honored --session-dir
```

**All seven steps passing = SPEC §9.5 satisfied = v1 done.** The extras below are M7's
deferred-edge verifications and must also pass.

### Step E1 — non-localhost bind (SPEC §3.2.2)

```sh
LAN_IP=$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null \
  || ifconfig 2>/dev/null | awk '/inet / && $2 !~ /^127\./ {print $2; exit}')
[ -n "$LAN_IP" ] || { echo "SKIP E1: no non-loopback IPv4"; }
# If SKIP printed, record the skip and go straight to Step E2 — do NOT run the probes
# below (an empty $LAN_IP would make the negative probe vacuously print refused-ok).
# Negative first, while still bound to the default:
curl -s --max-time 3 "http://$LAN_IP:$PORT/api/health" && echo UNEXPECTED || echo refused-ok
pkill -TERM -f "$GIBSON serve"; sleep 1
printf '\n' >> "$WS/main/gibson.toml"   # then set bind:
python3 - <<EOF
import re,io
p="$WS/main/gibson.toml"; s=open(p).read()
open(p,"w").write(s.replace("[server]", '[server]\nbind = "0.0.0.0"', 1))
EOF
cd "$WS/main" && "$GIBSON" serve >> "$SB/serve.log" 2>&1 & sleep 1
curl -sf "http://$LAN_IP:$PORT/api/health"                  # now reachable
```

Browser: load `http://$LAN_IP:$PORT/` and assert the session list renders (a
"second device" view). The no-non-loopback-IP case is handled by the scripted guard
above — it must print the SKIP line, never silently pass (§4.3).
Revert the `gibson.toml` edit afterwards (`git -C "$WS/main" checkout gibson.toml`).

### Step E2 — pi compatibility behavior (SPEC §5.4.1)

```sh
pkill -TERM -f "$GIBSON serve"; sleep 1
printf '#!/bin/sh\n[ "$1" = "--version" ] && { echo 0.81.9; exit 0; }\nexit 1\n' > "$SB/oldpi"
chmod +x "$SB/oldpi"
python3 - <<EOF
p="$WS/main/gibson.toml"; s=open(p).read()
open(p,"w").write(s.replace("[server]", '[server]\npi_bin = "$SB/oldpi"', 1))
EOF
cd "$WS/main" && "$GIBSON" serve > "$SB/oldver.log" 2>&1; echo "exit=$?"
```

Assert: non-zero exit; `oldver.log` names both found `0.81.9` and minimum `0.82.0`.
Repeat after pointing `pi_bin` at `$SB/nonexistent`; assert the missing-binary error is
distinct. Then prove later minor or major versions are allowed:

```sh
git -C "$WS/main" checkout gibson.toml
printf '#!/bin/sh\n[ "$1" = "--version" ] && { echo 0.99.0; exit 0; }\nexit 1\n' > "$SB/newpi"
chmod +x "$SB/newpi"
python3 - <<EOF
p="$WS/main/gibson.toml"; s=open(p).read()
open(p,"w").write(s.replace("[server]", '[server]\npi_bin = "$SB/newpi"', 1))
EOF
cd "$WS/main" && "$GIBSON" serve > "$SB/newver.log" 2>&1 & sleep 1
curl -sf "$BASE/api/health"
grep -F 'pi version has not been verified with Gibson' "$SB/newver.log"
grep -F 'found=0.99.0' "$SB/newver.log"
pkill -TERM -f "$GIBSON serve"; sleep 1
git -C "$WS/main" checkout gibson.toml
cd "$WS/main" && "$GIBSON" serve >> "$SB/serve.log" 2>&1 & sleep 1
curl -sf "$BASE/api/health"                # healthy before the E3 probes
```

### Step E3 — SSE hygiene live probes (SPEC §7.2.3–§7.2.4)

```sh
# Heartbeat on an idle stream (server restarted at the end of E2; $SID is closed —
# SSE on a non-live session replays its entries then idles, M6 §4.6):
curl -sN --max-time 20 "$BASE/api/sessions/$SID/events" | grep -m1 '^:hb'   # seen within 20s
# Backpressure + heartbeat + reconnect-recovery automated proof:
cd "$REPO" && go test ./internal/httpapi/ -run 'Backpressure|Heartbeat' -v
```

Assert: `:hb` observed; both tests pass.

### Step E4 — full automated gate

```sh
cd "$REPO" && go test ./... && GIBSON_TEST_REAL_PI=1 go test ./...
```

Assert: both green (the second requires this machine's real pi).

Teardown: `pkill -TERM -f "$GIBSON serve"; rm -rf "$SB"` (keep `$SB` if evidence
screenshots/logs should be retained for the acceptance record).

---

## 9. Success criteria checklist

Acceptance (SPEC §9.5 — each is a runbook step above):
- [ ] §9.5.1 Scratch grove workspace with second worktree, committed `gibson.toml`
      defining a type whose `extra_args` loads `confirm-gate.ts` (Step 1)
- [ ] §9.5.2 gibson serves the SPA on the configured port; UI loads (Step 2)
- [ ] §9.5.3 Session created (type+checkout+message); one pi process, cwd = target
      checkout (Step 3; §9.2.a); token-by-token streaming asserted on Step 5's
      tool-free stream (§9.2.b)
- [ ] §9.5.4 Dialog raised, surfaced loudly (`blocked-on-dialog`), answered from the
      browser, agent proceeds (Step 4; §10.3, §7.3.2)
- [ ] §9.5.5 Second client mid-stream renders identical state via snapshot + cursor
      replay and receives the remainder live, no gaps or duplicates (Step 5; §9.3.a–b)
- [ ] §9.5.6 Kill + restart → session `stopped`; follow-up respawns pi with the same id
      and full context (Step 6; §9.4.a, §5.3)
- [ ] §9.5.7 Session JSONL + stderr log under `<checkout>/.gibson/`; `git status`
      silent in both checkouts; nothing written outside `.gibson/`; nothing in
      `~/.pi/agent/sessions` (Step 7; §4.1, §4.2)

M7 deferred edges:
- [ ] §3.2.2 Default bind is loopback-only; configured non-localhost bind serves a
      second device; no auth, documented (Step E1; README §6)
- [ ] §5.4.1 Compatibility policy: pi below 0.82.0 → distinct startup error naming
      found + minimum; 0.82.x is verified; a later pi line serves with an unverified-version
      warning (Step E2; version tests)
- [ ] §7.2.4 / §10.2 Slow client disconnected at 256-event overflow; pump and fast
      clients unaffected; recovery via cursor reconnect (Step E3; backpressure test)
- [ ] §7.2.3 Heartbeat ≤30s honored (`:hb` @15s) (Step E3; heartbeat test)
- [ ] §9.1.b Five distinct startup errors: missing config, invalid config, occupied
      port, missing pi, too-old pi version (error-matrix test)
- [ ] §4.1.3 Automated self-containment audit green across create/stream/unclean-kill/
      swept-restart/resume/close (audit test)
- [ ] §5.1.3 Single-writer guard: one spawn per live id under concurrency; respawn only
      from stopped/closed (manager test)
- [ ] README.md complete per §4.8 outline (install, configure, run, security,
      single-writer warning, compatibility policy, limitations)

SPEC §10 sweep (each = enforcing code site + guarding test/doc recorded in step 5.8):
- [ ] §10.1 single-writer honored  — [ ] §10.2 SSE hygiene honored —
      [ ] §10.3 blocked-visibility honored — [ ] §10.4 `custom()` degradation
      documented — [ ] §10.5 compatibility policy enforced + payloads forwarded verbatim

Gates:
- [ ] `go test ./...` green (no network/LLM — CONV §9)
- [ ] `GIBSON_TEST_REAL_PI=1 go test ./...` green on this machine
- [ ] One uninterrupted clean pass of runbook Steps 1–7 + E1–E4

---

## 10. Explicitly out of scope

- Everything in SPEC §1.2 (auth, prompt management, tree/fork UI, compaction controls,
  mid-session model/thinking switching, command palette, bash console/PTY, file
  browser, HTML export, multi-workspace serving, layered config) — post-v1.
- Per-`customType` renderers beyond the generic fallback card (SPEC §8.2).
- Any new wire fields, SSE event types, statuses, routes, or registry schema changes —
  the CONV §3/§4/§5 surfaces are frozen; hardening fixes must fit inside them.
- Supporting pi versions below 0.82.0, declaring additional minor lines verified, or
  adapting to future pi protocol renames (SPEC §10.5 — a future compatibility milestone's
  problem).
- Auth or TLS for non-localhost binds (explicitly user-owned risk, BACKGROUND #14).
- Packaging/distribution (Homebrew, release automation) — README covers build-from-
  source only.
- Performance work beyond the backpressure contract (no load testing, no benchmark
  suite).
