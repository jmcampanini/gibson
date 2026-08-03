# BACKGROUND

This file is the record of the design session that shaped gibson. If you are wondering _why_ something is the way it is, the answer is probably here. The companion SPEC.md says _what_ to build; this file says how we got there.

## The session

**Date:** 2026-07-26. **Format:** a question-by-question design interview, walking the decision tree one branch at a time, with codebase exploration substituted for questions wherever the answer was discoverable.

The premise: build a web interface for the pi coding agent — a Go CLI that runs a localhost server, serves a React SPA, and creates/drives pi sessions that outlive any browser tab. The opening brief established several fixed points before any questions were asked: localhost-only, server-owned sessions, multiple tabs and devices attached to the same session as a hard requirement, a full SPA with a real toolchain, workspace-level configuration defining launchable "session types" (review session vs. quick session vs. long session), personal single-user tooling, and prompt management explicitly out of scope.

Two background research efforts ran alongside the interview and fed evidence into it:

1. **pi internals** — read directly from the installed package (`@earendil-works/pi-coding-agent` v0.82.1, which ships its full docs and source in the tarball): the RPC protocol, session file format, CLI flags, extension system, and concurrency behavior.
2. **Landscape survey** — how OpenCode, Happy Coder, the Claude Agent SDK / claude.ai/code, Codex, vibe-kanban, and pi's own ecosystem (pi-server, pi-web) architect web/remote UIs over terminal agents.

## What the research established

These findings constrained or validated nearly every decision below.

### pi's programmatic surface (from the installed package)

- **`pi --mode rpc` is a real, documented protocol**: strict JSONL over stdin/stdout, LF-delimited only — the docs explicitly warn that Unicode-aware line readers (Node `readline` splits on U+2028/U+2029) corrupt JSON strings. Commands in (`prompt`, `steer`, `follow_up`, `abort`, `get_state`, `get_entries`, `get_tree`, `fork`, `compact`, `set_model`, ~30 total), responses and a verbatim stream of every agent event out: turn lifecycle, per-token `text_delta`/`thinking_delta`, `tool_execution_start/update/end`, plus the undocumented-but-emitted `entry_appended`.
- **Sessions are append-only JSONL trees** at `~/.pi/agent/sessions/--<encoded-cwd>--/`, entries linked by `id`/`parentId`. Entry ids are durable cursors: `get_entries since:<id>` gives true incremental replay across client restarts — better than what any surveyed project has.
- **Resume is a spawn-time affair**: `pi --mode rpc --session-id <id> --session-dir <dir>` opens-or-creates a session with a stable, client-assigned id in a client-chosen directory. There is no runtime "attach."
- **pi does zero session-file locking.** Writes are bare `appendFileSync`; two processes on one file silently corrupt the tree. One process must be the single writer per session; any fan-out to multiple viewers has to happen in a layer above pi.
- **Permissions do not exist in pi core.** Approval flows are extensions, and extension dialogs surface over RPC as `extension_ui_request`/`extension_ui_response`: four blocking methods (`select`, `confirm`, `input`, `editor`, each with optional timeout auto-resolve) plus fire-and-forget `notify`, `setStatus`, `setWidget`, `setTitle`, `set_editor_text`. That is the entire modal surface a client must render.
- **`ui.custom()` — arbitrary terminal UI components — cannot render over RPC** (returns `undefined`). A scan of this machine's installed extensions found exactly four affected call sites: pi-review's triage UI, fuzzy-explorer (inherently a TUI), interactive-subagents' status screen, and pi-sidequest's command palette. Everything else those extensions do (82 `notify` calls, widgets, the four dialogs) renders fine.
- **pi also exports a full embeddable SDK** (`createAgentSession`, `AgentSession`) — but it is Node-only.

### The convergent architecture (from the landscape survey)

Every surveyed system decomposes into the same layers:

1. **Agent as machine protocol, not screen** — "the TUI is just another client" everywhere; projects that started by scraping terminal output (Happy v1, Omnara v1) abandoned it as unmaintainable.
2. **NDJSON event stream** with the same taxonomy pi already has.
3. **A long-lived ownership process decoupled from the UI** — browser tabs never own the agent.
4. **Attach = durable snapshot + live subscribe, never socket replay** — Happy deliberately disabled Socket.IO's replay; recovery is always "refetch from storage, then tail."
5. **Spawn-with-`--resume` as the universal recovery move** — nobody re-attaches to an orphaned live process; stale "running" state is marked dead on startup and processes are respawned on demand.
6. **Approvals as first-class async protocol messages** traveling the same channel as events.

Transport split three ways: SSE + REST replies (OpenCode local), WebSocket (Happy, claudecodeui), or both (vibe-kanban: SSE for state, WS for logs/approvals/PTY).

Prior art worth a skim before writing code: pi's experimental first-party `packages/server` (supervisor spawning RPC subprocesses, orphan recovery on restart — same shape gibson needs, but unstable and aimed at a cloud rendezvous service), and third-party **pi-web** (jmfederico/pi-web: session daemon split from restartable UI, Machine → Project → Workspace (worktree) → Session model — the closest existing thing to gibson).

### Watch-outs carried into the spec

- Split RPC frames on `\n` only.
- One writer per session file; the terminal escape hatch (`pi --session-dir ... -r`) is for _closed_ sessions only.
- Heartbeat SSE streams (~30s); apply per-client backpressure so a slow phone can't stall the pi stdout pump.
- A blocking dialog with no connected client simply waits — correct with keep-alive processes, but "blocked on dialog" must be loud in the session list.
- Monotonic cursor ordering and dedup on reconnect.
- Protocol churn is the top maintenance risk (pi renamed orgs and packages within months); check the pi version at startup, require at least 0.82.0, treat 0.82.x as verified, and keep later minor or major versions visible with a warning rather than blocking them.

## The decisions

### 1. One server per workspace, at the workspace root

_Question: what does one gibson instance govern?_ Options: the grove-style workspace root (`~/Code/github.com/org/repo/` with `main` and worktrees as sibling checkouts), a single checkout, or just "wherever you run it." The opening brief had said both "one place, one project" and "the grove layout is how I like to keep these," which pulled in different directions. **Chosen: workspace root** (also the recommendation) — one server governs a repo's whole family of checkouts, matching the grove-cli mental model.

### 2. Target checkout chosen dynamically at launch

_Question: does a session-type config name its checkout?_ The recommendation was a `dir` field per session type (cheap now, avoids schema rework later); alternatives were hardcoding `main` or picking the checkout in the UI at launch. **Chosen: dynamic at launch — diverging from the recommendation.** Session type and target checkout are orthogonal: config defines _how_ to run pi, and gibson itself discovers the available checkouts (worktree enumeration is the tool's job, not the config's). This keeps session types reusable across any worktree.

### 3. Restart story: resume on demand

_Question: what happens to live sessions when the gibson server restarts?_ Options: resume on demand (processes die with the server; transcripts persist; respawn lazily on next message), auto-reattach (eagerly respawn everything at startup), or sessions simply die. **Chosen: resume on demand** (the recommendation). It matches pi's native spawn-time resume model exactly, and the landscape survey later confirmed it as the universal pattern — nobody re-attaches to orphans. Known cost, accepted: a server death mid-turn loses that turn's in-flight work regardless of choice.

### 4. Idle sessions: keep the pi process alive until closed

_Question: reap idle pi subprocesses or keep them?_ Options: keep alive until explicitly closed, reap on an idle timer and respawn via resume, or defer pending the RPC research. **Chosen: keep alive** (the recommendation). For a single-user localhost tool, a handful of idle node processes is negligible; keep-alive avoids resume edge cases and preserves extension in-memory state. The reap path exists anyway — it's the crash-recovery path — it just isn't triggered by a timer.

### 5. Frontend: React

_Question: framework for the SPA?_ Options: React (industry default, biggest streaming-chat ecosystem, best agent-maintainability), Svelte 5 (leaner, single-author-friendly), SolidJS (best raw fit for token streaming, smallest community). All assumed TypeScript + Vite + `go:embed`. **Chosen: React** — "React feels right." The boring, correct default.

### 6. Config: a single `gibson.toml`, committed to the repository

_Question: adopt grove-cli's convention wholesale or simplify?_ Exploration of grove-cli established the local convention: TOML, layered discovery (XDG → ancestors → git root → worktree → cwd). The recommendation was to mirror grove exactly and keep config out of the versioned repo. **Chosen: a single file, and checked into the repo — diverging from the recommendation on both counts.** No layering in v1; and because the file is committed, it exists at every checkout's root and travels with the repo. Consequence adopted with it: gibson is _launched from inside a checkout_ (typically `main/`), reads that checkout's `gibson.toml`, and derives the workspace root as the parent directory.

### 7. Transport: SSE for events + REST for actions

_Question: how do server and browser talk?_ Options from the research: SSE + plain HTTP POSTs (OpenCode's local model), a single WebSocket (Happy's model), or both (vibe-kanban). **Chosen: SSE + REST** (the recommendation). It won on the brief's own criteria — easy, snappy, resilient, debuggable: pi's durable entry cursors slot directly into SSE's native `Last-Event-ID` reconnect, everything the browser _does_ is a curl-able POST, and nothing in v1 needs bidirectional. A WS endpoint can be added later just for a PTY feature without touching the core.

### 8. Multi-device: all clients equal peers

_Question: when the same session is open on laptop + phone + second tab, who can act?_ Options: all clients equal (every client gets the same SSE fan-out; any can prompt, steer, abort, answer dialogs; first dialog answer wins), or Happy-style control ownership (one client holds the session, others read-only until takeover). **Chosen: all equal** (the recommendation). Control arbitration exists in Happy because a terminal and a phone contend for stdin; gibson has no terminal client contending, and one human can't meaningfully race themself. pi's own steer/follow-up queueing absorbs overlapping prompts.

### 9. Session data lives inside the worktree it runs in

_Question: where do pi session files and gibson's metadata live?_ The recommendation was a workspace-root `.gibson/` (sibling to checkouts, outside every repo). Alternatives: pi's default `~/.pi/agent/sessions/` location, or a hybrid. **Chosen: per-worktree `.gibson/` inside each checkout — diverging from the recommendation.** Each checkout gets its own `.gibson/` (passed as `--session-dir`, plus metadata and logs), so a session is fully self-contained with the worktree it ran in: prune the worktree and its sessions go with it. "List all sessions" becomes: enumerate checkouts, scan each `.gibson/`. No global registry.

### 10. `.gibson/` ignored via a committed `.gitignore` entry

_Consequence of #9: `.gibson/` now sits inside a git repo._ Options: a committed `.gitignore` entry (visible, versioned, zero magic — matches `gibson.toml` already being a repo citizen), silently auto-writing `.git/info/exclude`, or both. **Chosen: committed `.gitignore` entry** (the recommendation). Repository proofs create `.gibson/` data and require a clean `git status --porcelain`.

### 11. Session types: hybrid schema

_Question: how is a session type expressed in `gibson.toml`?_ Options: fully structured (a TOML field per pi option — prettier, but gibson chases pi's flag surface forever, and protocol churn is the top maintenance risk), pure passthrough (just a name and raw args — UI can't display anything meaningful), or a hybrid. **Chosen: hybrid** (the recommendation): structured `description`/`model`/`thinking`, everything else verbatim in `extra_args`. Full pi surface on day one, no schema treadmill. No gibson-owned per-type knobs in v1.

### 12. The v1 UI cut line

_Question: what's in and out of "run and interact with a session"?_ The proposed cut was accepted as-is. **In:** session list across all checkouts (name, type, checkout, status idle/streaming/blocked-on-dialog, last activity); launch flow (type + checkout + optional name + first message); chat view with markdown, streaming text, collapsed-by-default thinking, collapsible tool cards with live partial results, the four blocking dialogs as modals, notify toasts, status/widget strip, abort button, context-usage meter; steer-vs-follow-up composer (pi requires the choice mid-stream); generic fallback renderer for `custom_message` entries (sessions on this machine already contain `subagent_result`). **Deferred:** tree/branch navigation and fork UI, compaction controls, mid-session model/thinking switching, slash-command palette, bash console, file browser, HTML export, rename/labels. Two cheap extras (model switcher, command palette) were explicitly offered into v1 and declined.

### 13. Port in `gibson.toml`; pi from `$PATH`

_Question: with several workspaces running concurrently, how does each instance get its address?_ Options: a committed per-repo `port` (stable, bookmarkable URLs — `localhost:7311` _is_ this workspace), one fixed default + manual flags, or auto-assigned ports (kills bookmarks and phone access). **Chosen: port in `gibson.toml`** (the recommendation), `--port` flag as override. Folded in: gibson finds `pi` on `$PATH`, with an optional `pi_bin` override for pinning if churn ever bites.

### 14. Bind address configurable in `gibson.toml`

_Question: "localhost only" vs. "multiple devices" — a phone can't reach `127.0.0.1`._ Options: hard-bind localhost and rely on an overlay network like Tailscale (the recommendation, keeping gibson off the network entirely), a configurable `bind` with no auth, or LAN binding plus a shared-secret auth layer. **Chosen: configurable `bind` — diverging from the recommendation.** Default `127.0.0.1`, settable to a tailnet/LAN address; no auth in v1; the user owns the risk of what they expose.

## Dead ends and rejected paths

- **Embedding pi's SDK** — rejected because the SDK is Node-only and gibson is Go; it would force a sidecar process. Spawning `pi --mode rpc` gives everything needed. (The landscape survey independently confirmed the long-lived-subprocess-speaking-NDJSON pattern as the standard: Claude Agent SDK, Codex app-server, vibe-kanban all work this way.)
- **Terminal-output/PTY scraping** — never seriously considered; the survey showed every project that tried it (Happy v1, Omnara v1) rebuilt on the official protocol.
- **Control-ownership arbitration** for multi-device — rejected as machinery without a problem (see #8).
- **Grove-style layered config discovery** — recognized as the house convention, offered, declined in favor of one committed file (see #6).
- **Workspace-root `.gibson/` storage** — recommended, declined in favor of per-worktree self-containment (see #9).
- **Hybrid storage** (pi files in default location, gibson registry elsewhere) — rejected outright as the worst of both.
- **WebSocket or dual transport** — rejected for v1; nothing needs bidirectional (see #7).
