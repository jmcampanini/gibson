# M0 Deviations

This ledger records design deviations discovered while implementing M0. The existing plans remain unchanged during implementation; reconciliation is deferred until M0 is complete and the resulting design can be evaluated from working code.

## At a glance

- We moved serve orchestration from `cmd/serve.go` to `internal/app/serve.go` to keep Cobra thin and lifecycle logic maintainable.
- We made future-facing interfaces provisional so working code, not speculative consumers, shapes M0's APIs.
- We chose contract-focused tests to preserve confidence without duplicating every assertion across layers.
- We changed the binary output from `bin/gibson` to `build/gibson` to match Command K and Overlay conventions.
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
