# Issue 32 Execution Plan — Complete positional-grammar verification through the real Cobra tree

Root `PLAN.md` is the authoritative implementation plan for
[issue #32](https://github.com/jmcampanini/gibson/issues/32). The issue is the problem
statement; the decisions below were settled interactively and are the contract. This file
is temporary and is retired when the issue's pull request merges.

## The fleet standard (use verbatim as the PR description)

> **Grammar verification: lean idiomatic table**
>
> This PR verifies the CLI grammar through the real Cobra tree using the fleet-standard
> "lean idiomatic table":
>
> - One table-driven test executes the real root command (fresh instance per case via the
>   constructor, with runner spies injected) using the Cobra-idiom `executeCommand` helper
>   (`SetArgs` + `SetOut`/`SetErr` + `Execute`).
> - Rows cover what we own: bare-root help, unknown command, unknown flags,
>   help/version/completion short-circuits, valid invocations reaching their runners, and
>   one rejected-operand row per command proving rejected input never calls a runner.
> - We deliberately do not enumerate arity permutations (missing/extra operand matrices) —
>   `cobra.NoArgs`/`cobra.ExactArgs` mechanics are Cobra's own tested contract
>   (`args_test.go` upstream). Re-testing the framework is what we're avoiding.
> - An inventory guard walks the freshly built root's `Commands()` and fails when a
>   command exists without a grammar row, so new commands can't land uncovered. Cobra's
>   auto-added help/completion are exercised as scenarios, not inventoried as application
>   commands.
> - Grammar/pre-work rejection is owned at this tree level; superseded per-command
>   rejection tests are removed (single owner per behavior). Leaf tests keep owning option
>   translation and command semantics. Exit codes and process-level error output stay
>   owned by main and the binary-level proof.
>
> This mirrors the pattern being standardized across the fleet (originating in gibson#32)
> so grammar tests look identical in every Go CLI.

## Settled decisions

- **Three layers.** In-process tree grammar table plus inventory guard in package `cmd`,
  runner spies through the existing `newRootCommand(serve, run, &outcome)` injection seam
  (no production changes), and a `scripts/cli-proof.sh` extension proving a rejected
  operand leaves application-managed state untouched.
- **Lean table.** One rejected-operand row per command; no arity-permutation enumeration.
  Grammar is asserted by behavior only (execute input, observe error and spy state) —
  never by inspecting `command.Args` or pinning Cobra's usage/help wording.
- **Ownership.** The tree table owns wiring, grammar, and pre-work rejection; the
  superseded leaf rejection tests are deleted. Leaf tests keep owning option translation
  and command semantics. `main.go`'s `processExitCode` and the CLI proof keep owning exit
  codes and the `gibson: error:` prefix.
- **Naming (fleet-consistent).** `cmd/grammar_test.go`, `TestApplicationCommandGrammarInventory`,
  `TestCommandGrammarMatrix`, walker `collectApplicationCommands`, helper `executeCommand`.
- **Inventory boundary.** Application-owned commands are those present on the freshly
  built root before execution (today: root, `serve`, `run`); Cobra injects `help` and
  `completion` only during `Execute`, so they are exercised as scenario rows but never
  inventoried.

## Progress

- [x] Chunk 1 — Lean grammar table, inventory guard, and rejected-operand state proof

## Chunk 1 — Lean grammar table, inventory guard, and rejected-operand state proof

### Objective

Prove Gibson's complete command grammar through a fresh real Cobra tree, guard the
inventory so new commands cannot land uncovered, and prove at the binary level that a
rejected operand exits nonzero with an actionable error while leaving `.gibson` and Git
state byte-identical.

### Tasks

- [x] Add `cmd/grammar_test.go` with the `executeCommand` helper: build a fresh root via
  `newRootCommand` with injected serve/run spies per case, wire `SetArgs` and
  `SetOut`/`SetErr` buffers, run `Execute`, and return output and error.
- [x] Add `collectApplicationCommands` (recursive walk of the freshly built, unexecuted
  root) and `TestApplicationCommandGrammarInventory`, which fails when any inventoried
  command lacks a grammar row in the matrix.
- [x] Add `TestCommandGrammarMatrix` with exactly these rows:
  - `(bare root)` → succeeds (help path), no runner called
  - `bogus` → fails (root's rejected-operand row: an operand to root is an unknown
    command), no runner called
  - `--bogus` → fails, no runner called
  - `serve --bogus` → fails, serve not called
  - `run a b --bogus` → fails, run not called
  - `--help` → succeeds, no runner called
  - `serve --help` → succeeds, serve not called
  - `run --help` → succeeds, run not called
  - `help serve` → succeeds, no runner called
  - `--version` → succeeds with non-empty output, no runner called
  - `completion fish` → succeeds with non-empty output, no runner called
  - `serve` → serve called once
  - `run review message` → run called once
  - `serve extra` → fails, serve not called (serve's rejected-operand row)
  - `run a b extra` → fails, run not called (run's rejected-operand row)
- [x] Delete the superseded leaf rejection tests `TestServeCommandRejectsPositionalArguments`
  (`cmd/serve_test.go`) and `TestRunCommandRequiresTypeAndMessage` (`cmd/run_test.go`);
  leave all other leaf tests untouched.
- [x] Extend `scripts/cli-proof.sh` directly after the invalid-checkout loop, while both
  checkouts hold rich valid state: `cp -R` both `.gibson` trees (`main` and `wt-x`) into
  the sandbox and record `git rev-parse HEAD` for both; run `gibson run quick rejected extra`;
  require exit 1 and a single `gibson: error: `-prefixed line; require `diff -r` to show
  both `.gibson` trees byte-identical to their snapshots, `git status --porcelain` empty,
  and `HEAD` unchanged in both checkouts; emit `REJECTED_OPERAND_STATE=unchanged`.
- [x] Add one binary completion smoke to `scripts/cli-proof.sh`: `gibson completion fish`
  exits 0 with non-empty output (proves the shipped binary's surface; the in-process row
  cannot see the built binary).

### Verification

- [x] `go test -race ./cmd` is green with the new table and deletions.
- [x] Guard proof: temporarily register a stub command in `newRootCommand`, observe
  `TestApplicationCommandGrammarInventory` fail, revert.
- [x] `make check` is green.
- [x] `make cli-proof` is green, including `REJECTED_OPERAND_STATE=unchanged` and the
  completion smoke.

## Out of scope — fleet follow-up

The lean idiomatic table is the fleet-wide standard for Go CLIs, with gibson as the
reference implementation. Not part of this issue:

- Codify the lean shape and naming in the fleet contract at
  `~/Code/github.com/jmcampanini/fleet/main/wiki/cli/command-contracts.md`, which today
  mandates the outcomes (real-tree verification, boundary spies, unchanged-state proof)
  but not the shape.
- Reconcile the divergent merged implementations: cmdk PR #146 (full 48-row matrix,
  valid-row inventory requirement, `runGrammarScenario` naming), grove PR #111 (generic
  property walks, no table), cubby PR #46 (`operands_test.go` naming, runner-replacement
  spies, leaves-only inventory).
- Implement the open sibling issues lean-first: brewkit#47, overlay#35, gsd#84, namo#23,
  grove#127.

## Agent-verified end-to-end workflow

1. Run `go test -race ./cmd -run 'TestApplicationCommandGrammarInventory|TestCommandGrammarMatrix'`
   and confirm both pass against the real tree.
2. Prove the guard: add a temporary stub command to `newRootCommand`, rerun the inventory
   test, confirm it fails naming the uncovered command, revert the stub, confirm green.
3. Run `make check` and confirm it passes.
4. Run `make cli-proof` and confirm it passes, emitting `REJECTED_OPERAND_STATE=unchanged`
   (rejected `run` operand against the valid Git/config/fake-pi fixture left both
   `.gibson` trees and both checkouts' Git state unchanged) alongside the existing
   `GIBSON_CLI_PROOF=PASS`.
5. Confirm the existing smoke paths inside the proof still pass unchanged: root help,
   version, serve/run help, rejected `serve` operand exit 1, plus the new
   `completion fish` smoke.
