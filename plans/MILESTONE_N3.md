# MILESTONE N3 — Session management in the CLI

`provisional`

## 1. Goal & capability

You can now: treat the CLI as a complete session home — `gibson list` shows every
session with status and worktree; `gibson open <id>` resumes a stopped session in the
pi TUI with history intact (and refuses live ones, single-writer rule).

This milestone's plan gate is explicitly expected to re-scope it with N1/N2 usage
knowledge; the outcomes below are the floor, not the ceiling.

## 2. Preconditions

N1 complete (central registry with `checkout`/`owner`, minting, the standing gate).
N2 complete (status derivation and the file-backed history path, reused for
presentation).

## 3. Deliverables

- `cmd/list.go` + `internal/app/list.go`: registry enumeration with status, type,
  worktree, and last activity.
- `cmd/open.go` + `internal/app/open.go`: TUI resume — respawn interactive pi with
  the original `--session-id` and central `--session-dir`, cwd = the session's
  recorded worktree; refuse when the record is `live`; clear error when the worktree
  no longer exists.
- Gate extension: list/open scenarios added to the standing fakepi coverage and
  `scripts/cli-proof.sh`.

## 4. Design & rationale

- `open` is spawn-time resume (SPEC §5.3) applied to the TUI owner: same id, same
  session dir, pi reconstructs context. No runtime attach exists in pi.
- Refusing `live` records enforces the single-writer rule across owners
  (SPEC §5.1.3); the error names the owner and, for TUI sessions, the worktree where
  it is running.
- A session whose worktree was pruned still lists (transcripts are central); `open`
  for it fails with a clear message. Whether re-minting a worktree for it belongs in
  scope is a plan-gate question informed by usage.

## 5. Implementation steps

1. `internal/app/list.go` (+`_test.go`): enumeration + presentation ordering
   (most recent activity first).
2. `cmd/list.go` (+`_test.go`): Cobra adapter, plain and scriptable output.
3. `internal/app/open.go` (+`_test.go`): record validation → exec interactive pi →
   lifecycle `stopped → live → stopped`.
4. `cmd/open.go` (+`_test.go`).
5. Gate: extend the fakepi workflow test and `cli-proof.sh`.

## 6. Interfaces exposed to later milestones

None new — this milestone consumes N1/N2 seams. Any new seam it needs must be raised
at the plan gate, not invented here (MILESTONE_CONVENTIONS §10).

## 7. Testing

- `internal/app` owns list ordering/filtering and open's validation matrix (live
  refusal, missing worktree, stopped/closed resume) via fakepi.
- `cmd` owns output shape. Real-pi gated test: resume shows prior history in the TUI.

## 8. Agent-verified proof workflow

In a scratch workspace with fakepi:

1. Create two sessions via `gibson new`; exit one, keep one running.
2. `gibson list`: both appear; statuses `stopped` and `live` are truthful.
3. `gibson open <stopped-id>`: pi respawns with the same session id and session dir,
   cwd = its worktree; registry goes `live`, then `stopped` on exit.
4. `gibson open <live-id>`: refused with a clear single-writer error.
5. Prune one session's worktree; `list` still shows it; `open` fails with a clear
   missing-worktree message.
6. `git status --porcelain` clean; full standing gate green.

Human proof: Javier lists and reopens a real day-old session and continues it.

## 9. Success criteria checklist

- [ ] `gibson list` reflects registry truth, including sessions with pruned worktrees.
- [ ] `gibson open` resumes with full history via spawn-time resume (SPEC §5.3).
- [ ] Live sessions are refused with an error naming the owner (SPEC §5.1.3).
- [ ] Gate coverage extended; `make check` and `make cli-proof` green.

## 10. Explicitly out of scope

Web session-management polish; worktree cleanup automation; re-minting worktrees for
pruned sessions (plan-gate question); close/reopen semantics beyond resume; the
post-N3 horizon (multi-client, restart resilience, full rendering).
