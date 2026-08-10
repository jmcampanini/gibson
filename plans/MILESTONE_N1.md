# MILESTONE N1 — TUI sessions in minted worktrees

`provisional`

## 1. Goal & capability

You can now: run `gibson new <type>` from `main` — gibson mints a fresh sibling
worktree branched from latest, registers the session centrally, and drops you into the
pi TUI working inside that worktree. Transcripts live in `main/.gibson/` and survive
worktree pruning.

## 2. Preconditions

M1 complete: `internal/pisession` (argv assembly, version check, binary resolution),
`internal/store` (registry, locking, session ids), `internal/workspace` (workspace
derivation), `internal/config` (session types), and the fakepi/pitest/testws test bed.
No HTTP surface is consumed.

## 3. Deliverables

- `internal/workspace/`: worktree minting (fetch, branch from the remote default's
  head with local fallback, `git worktree add` of a grove-style `wt-<slug>` sibling).
- `internal/store/`: storage root moves to the launch checkout
  (MILESTONE_CONVENTIONS §5); registry records gain `checkout` and `owner`
  (MILESTONE_CONVENTIONS §5); rebuild and lock semantics preserved.
- `cmd/new.go` + `internal/app/new.go`: the `gibson new` workflow.
- Standing gate: fakepi-based end-to-end coverage of the `gibson new` workflow inside
  `make check`, and a `gibson new` scenario added to `scripts/cli-proof.sh`.
- `internal/pisession/version.go`: verified line raised to 0.84.x (SPEC §5.4).

## 4. Design & rationale

- Session-per-worktree is normative (SPEC §2.2): `gibson new` always mints; there is
  no checkout picker. Rationale in BACKGROUND.md (re-path addendum).
- "Latest" = fetch the remote default branch, branch from its head; fall back to the
  local default-branch head when offline or remote-less (SPEC §2.2.2).
- Minting is native `git worktree add ../wt-<slug> -b <slug>` following grove's
  visible conventions — no runtime dependency on the grove binary (SPEC §2.2.3).
- The TUI process owns the session (SPEC §5.5): gibson execs interactive pi (no
  `--mode rpc`) with gibson's `--session-id` and the central `--session-dir`, cwd =
  the minted worktree, waits, then marks the record `stopped`. Single-writer rule
  holds across owners (SPEC §5.1.3).
- Deliberately naive: no `list`, no `open`, no cleanup. Resume via the SPEC §10.1
  escape hatch until N3.

## 5. Implementation steps

1. `internal/workspace/mint.go` (+`_test.go`): slug derivation, collision handling,
   fetch/branch/worktree-add against scratch repos.
2. `internal/store/`: root derivation change (single call site), `checkout`/`owner`
   fields per MILESTONE_CONVENTIONS §5, migrationless acceptance of M1-era records.
3. `internal/app/new.go` (+`_test.go`): compose config → version check → mint →
   register (`owner:"tui"`, `live`) → exec pi → wait → `stopped`; rollback the record
   (and leave the worktree, with a printed path) on spawn failure.
4. `cmd/new.go` (+`_test.go`): Cobra adapter, `gibson new <type> [--name <name>]`.
5. Gate: fakepi workflow test in `internal/app`, `cli-proof.sh` scenario, version
   constant bump.

## 6. Interfaces exposed to later milestones

- `workspace.Mint(ws *Workspace, slug string) (checkoutPath string, err error)` —
  N2's create route mints through the same function.
- Registry records carry `checkout` and `owner` per MILESTONE_CONVENTIONS §5; N2's
  list/read surfaces consume them.
- The central storage root derivation (MILESTONE_CONVENTIONS §5) is the single seam
  every later surface reads.

## 7. Testing

- `internal/workspace` owns minting behavior (scratch git repos; offline fallback).
- `internal/store` owns the central layout, new fields, and old-record acceptance.
- `internal/app` owns the composed workflow via fakepi: argv shape, cwd, registry
  lifecycle including exec-failure rollback.
- `cmd` owns flag shape. Real-pi test gated by `GIBSON_TEST_REAL_PI=1` covers spawn
  of a genuine interactive pi with `--session-id`/`--session-dir`.

## 8. Agent-verified proof workflow

In a `testws` scratch workspace (fakepi as `pi_bin`):

1. `gibson new quick --name fix-flaky-test` from the primary checkout.
2. Verify a sibling `wt-fix-flaky-test/` exists, branched from the remote default's
   head, on branch `fix-flaky-test`.
3. Verify fakepi was exec'd with cwd `wt-fix-flaky-test/`, gibson's session id, and
   `--session-dir <main>/.gibson/sessions`, without `--mode rpc`.
4. Verify the registry entry: `owner:"tui"`, `live` while running, `stopped` after
   exit; the transcript JSONL exists under `main/.gibson/sessions/`.
5. `git worktree remove` the minted worktree; verify the transcript and registry
   entry survive.
6. `git status --porcelain` clean in both checkouts.

Human proof: Javier runs `gibson new` against real pi and works in the TUI.

## 9. Success criteria checklist

- [ ] `gibson new <type>` mints a `wt-<slug>` sibling branched from latest (SPEC §2.2).
- [ ] Interactive pi runs in the minted worktree with gibson's id and the central
      session dir (SPEC §5.5).
- [ ] Registry lifecycle `live → stopped` with `owner:"tui"`; no absolute paths
      persisted (SPEC §4).
- [ ] Transcripts survive worktree pruning (SPEC §4.1.3).
- [ ] The workflow gate runs inside `make check` and `make cli-proof`.
- [ ] Verified pi line is 0.84.x; below 0.82.0 still fails clearly (SPEC §5.4).

## 10. Explicitly out of scope

`gibson list` / `gibson open` (N3); any HTTP or web change (N2); worktree cleanup or
pruning automation; carrying dirty state into minted worktrees (grove-cli#124); the
`--in <checkout>` escape hatch; multi-session orchestration.
