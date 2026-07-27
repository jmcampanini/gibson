# M0 Deviations

This ledger records design deviations discovered while implementing M0. The existing plans remain unchanged during implementation; reconciliation is deferred until M0 is complete and the resulting design can be evaluated from working code.

## At a glance

- We moved serve orchestration from `cmd/serve.go` to `internal/app/serve.go` to keep Cobra thin and lifecycle logic maintainable.
- We made future-facing interfaces provisional so working code, not speculative consumers, shapes M0's APIs.
- We chose contract-focused tests to preserve confidence without duplicating every assertion across layers.
- We changed the binary output from `bin/gibson` to `build/gibson` to match Command K and Overlay conventions.
- We deferred the functional `make web` target until the web project exists in Checkpoint 2.
- We bootstrap `go:embed` with a sibling sentinel rather than a tracked file inside generated `web/dist/`.
- We use Charm Log v2 for structured operational output instead of `log/slog`.
- We treat pi 0.82 as a minimum verified minor line rather than rejecting all newer versions.
- We will reconcile these differences with the existing plans after M0 is complete.

## D-001 — Application orchestration

- **Planned design:** `cmd/serve.go` owns the `serve` startup workflow; M1 similarly places its one-shot workflow in `cmd/run.go`.
- **M0 decision:** Cobra remains an adapter. `internal/app/serve.go` owns startup ordering, dependency composition, server lifetime, and shutdown.
- **Reason:** This keeps command parsing and presentation separate from application lifecycle orchestration, making the primary control flow easier to find, test, and maintain.
- **Potential downstream impact:** M1's `run` workflow and M2's application composition may benefit from the same boundary, but no downstream plan will be changed until M0 is complete.
- **Reconciliation:** Deferred until the end of M0.

## D-002 — Future-facing interfaces

- **Planned design:** `PLAN_CONVENTIONS.md` treats cross-milestone exported names and signatures as binding during M0.
- **M0 decision:** Planned signatures are design targets, not APIs to implement speculatively. M0 introduces only interfaces required by current consumers.
- **Tracking rule:** When an implemented API concretely differs from a planned signature, add a separate ledger entry naming both forms, the reason, and affected downstream consumers.
- **Reason:** This preserves useful planning direction while allowing working code to supply evidence for maintainable interfaces.
- **Potential downstream impact:** M1 and M2 preconditions may require reconciliation with the APIs established by M0.
- **Reconciliation:** Deferred until the end of M0.

## D-003 — Contract-focused verification

- **Planned design:** `PLAN_M0.md` repeats many behaviors across package tests, command integration tests, and its detailed acceptance workflow.
- **M0 decision:** Begin with contract-focused verification. Each behavior has one primary automated test layer; higher-level tests are added when they prove meaningful wiring rather than duplicating lower-level assertions.
- **Tracking rule:** Every locked acceptance outcome remains covered. Checkpoints identify the primary test owner, and concrete omissions or changes to plan-prescribed tests are added to this ledger when they occur.
- **Reason:** This keeps the suite maintainable through structural refactoring while retaining end-to-end confidence.
- **Potential downstream impact:** Later milestone test plans may benefit from the same ownership model instead of treating every listed test as mandatory at every layer.
- **Reconciliation:** Deferred until the end of M0.

## D-004 — Build output directory

- **Planned design:** `PLAN_M0.md` builds the Gibson binary as `bin/gibson` and uses that path throughout its proof workflow.
- **M0 decision:** Build the binary as `build/gibson`.
- **Reason:** `build/<binary>` is the shared convention in Command K and Overlay and remains clearly distinct from Vite's `web/dist/` output.
- **Potential downstream impact:** M0 and later proof commands, ignore rules, and scripts that name `bin/gibson` will require reconciliation.
- **Reconciliation:** Deferred until the end of M0.

## D-005 — Web Make target sequencing

- **Planned design:** The initial Makefile exposes `make web` before the web project is scaffolded.
- **M0 decision:** Checkpoint 1 exposed only functional targets. Checkpoint 2 adds `make web` with the React/Vite scaffold and makes the production build depend on it.
- **Reason:** A no-op target would claim to build assets it does not produce, while the real recipe cannot work before `web/package.json` exists.
- **Potential downstream impact:** Build automation must install frontend dependencies before invoking the production build.
- **Reconciliation:** Revisit the planned implementation order after M0 is complete.

## D-006 — Fresh-clone embed bootstrap

- **Planned design:** Force-commit `web/dist/.gitkeep`, embed `all:dist`, and recreate the placeholder after every Vite build.
- **M0 decision:** Commit `web/dist.bootstrap` beside the generated directory and embed the `dist*` pattern. Production startup uses only the `dist/` subtree and verifies `index.html` is present.
- **Reason:** The sibling sentinel lets a fresh clone compile without putting a tracked file inside generated output, so Vite cannot delete it and failed builds cannot leave placeholder noise.
- **Potential downstream impact:** Build and packaging documentation must retain the sentinel and wildcard as one mechanism.
- **Reconciliation:** Revisit the planned embed directive and placeholder workflow after M0 is complete.

## D-007 — Configuration load result

- **Planned design:** `config.Load(checkoutRoot)` returns `(*Config, toml.MetaData, error)` so startup can inspect undecoded keys immediately.
- **M0 decision:** Checkpoint 2 returned `(Config, error)`. Checkpoint 3 completes the contract with a domain `LoadResult` containing the validated `Config` and sorted unknown-key paths.
- **Reason:** Startup needs unknown-key diagnostics, but consumers do not need the TOML decoder's metadata API. A domain result keeps that dependency inside `internal/config`.
- **Potential downstream impact:** Later config consumers receive `LoadResult` and can apply the warning policy appropriate to their workflow.
- **Reconciliation:** Revisit the planned signature after M0 is complete.

## D-008 — HTTP server inputs

- **Planned design:** `httpapi.Options` carries configuration, workspace, version, static assets, and an optional development proxy, and `httpapi.New` always returns a handler.
- **M0 decision:** Checkpoint 2 accepts only `Version` and a distribution-rooted `StaticFS`; `New` returns `(http.Handler, error)` so asset readiness is established before listening.
- **Reason:** Configuration and workspace discovery belong to `internal/app`, while the development proxy is deferred to Checkpoint 4. Returning readiness errors prevents a production server from starting without its shell.
- **Potential downstream impact:** Later checkpoints will add only concrete HTTP dependencies as their routes and development mode arrive.
- **Reconciliation:** Revisit the planned options shape after M0 exposes all of its HTTP modes.

## D-009 — Operational logging library

- **Planned design:** Use the standard library's `log/slog` for structured server logs.
- **M0 decision:** Use `charm.land/log/v2` directly with an injected text logger for startup information and warnings.
- **Reason:** Charm Log retains structured key-value output while providing the preferred human-facing local CLI presentation.
- **Potential downstream impact:** Later application workflows should receive the same logger rather than introduce a second logging system.
- **Reconciliation:** Revisit the logging convention after M0 is complete.

## D-010 — Pi compatibility policy

- **Planned design:** Accept only version strings beginning with `0.82.` and fail every other version.
- **M0 decision:** Require pi 0.82.0 or newer, treat the 0.82 minor line as verified, and warn without blocking when a newer version is installed.
- **Reason:** The runtime check should fail fast for an environment below Gibson's tested baseline, not lock upgrades to one feature line. Newer versions remain visible until real integration tests verify them.
- **Potential downstream impact:** Real-pi verification in later milestones determines when another minor line becomes verified or a known-incompatible range must be rejected.
- **Reconciliation:** Revisit the supported-version convention after M0 is complete.
