# Temporary Divergence Intake

`SPEC.md` is normative for product behavior and `MILESTONE_CONVENTIONS.md` binds
cross-milestone seams. This file is only an intake queue for temporary contradictions
with those authorities, including failures to escalate a missing cross-milestone seam as
required by the conventions. It must not become a parallel specification or permanent
decision archive.

Historical M0 deviations used D-001 through D-012. Identifiers remain monotonic across the
repository and are never reused.

## Protocol

1. Record an actual contradiction with a planning authority as soon as it is discovered.
   Include the affected sections, temporary behavior or decision, an owner, and a
   consolidation deadline.
2. Update ordinary implementation details or chunking directly in the active root
   `PLAN.md`; update noncanonical milestone implementation notes directly when they change.
   Those edits do not need divergence entries unless they expose a contradiction with
   `SPEC.md`, `MILESTONE_CONVENTIONS.md`, or the conventions' seam-escalation rule.
3. Before the deadline, reconcile the decision into every affected milestone plan and the
   relevant authority, align code and tests where applicable, and verify the resulting
   behavior.
4. Delete the entry during milestone consolidation. Do not retain a closed section; git
   history preserves the temporary discussion.

Entry format:

```text
## D-NNN: short title
- Recorded: date and planning/implementation context
- Diverges from: authoritative document and section
- Temporary behavior or decision: what differs and why
- Owner: role or person responsible for reconciliation
- Consolidate by: milestone boundary
- Reconciliation: planning, authority, code, and test surfaces that must align
```

## Intake

## D-013: Durable entry synchronization descriptions disagree

- Recorded: 2026-07-29 during the planning-structure review in active M1.
- Diverges from: `SPEC.md` §6.3 and `MILESTONE_CONVENTIONS.md` §4.2.
- Temporary behavior or decision: M1's open-question text and parts of the provisional M2
  and M3 plans still describe ordinary durable entries as arriving directly through
  `entry_appended`; the authorities instead require Gibson's cursor-based `get_entries`
  synchronization path.
- Owner: M1 milestone owner.
- Consolidate by: M1 boundary.
- Reconciliation: align M1's completed contract and the provisional M2/M3 descriptions
  with the authoritative entry-feed design, then verify affected code and tests before M1
  planning artifacts are retired.

## D-014: Frontend streaming-state seam is inconsistent

- Recorded: 2026-07-29 during the planning-structure review in active M1.
- Diverges from: `MILESTONE_CONVENTIONS.md` §8 and §10's requirement to escalate an
  unpinned cross-milestone seam instead of inventing local variants.
- Temporary behavior or decision: provisional M4 defines a client `isStreaming` flag and
  says M5 consumes it, while provisional M5 says no such flag exists and keys behavior
  from wire status.
- Owner: M1 milestone owner.
- Consolidate by: M1 boundary.
- Reconciliation: settle and pin the frontend streaming-state seam in the conventions,
  align M4/M5, and preserve the single reducer path before either plan is activated.

## D-015: UIResolution cancellation shape is inconsistent

- Recorded: 2026-07-29 during the planning-structure review in active M1.
- Diverges from: `MILESTONE_CONVENTIONS.md` §7 and §10's requirement to escalate an
  unpinned shared Go seam instead of inventing local variants.
- Temporary behavior or decision: active M1 defines `UIResolution.Cancelled` as `*bool`,
  while provisional M5 describes M1 as exposing `bool` and schedules a later conversion
  to `*bool`.
- Owner: M1 milestone owner.
- Consolidate by: M1 boundary.
- Reconciliation: verify the implemented M1 type, pin the shared resolution shape in the
  conventions, and align every provisional consumer and test plan.

## D-016: Frontend test-runner question is already settled

- Recorded: 2026-07-29 during the planning-structure review in active M1.
- Diverges from: `MILESTONE_CONVENTIONS.md` §9.
- Temporary behavior or decision: provisional M3 still presents the frontend unit-test
  runner as an open question, while the binding conventions already select Vitest and its
  file and command conventions.
- Owner: M1 milestone owner.
- Consolidate by: M1 boundary.
- Reconciliation: remove the obsolete question, align M3 and later frontend plans with the
  binding Vitest contract, and verify links and commands during M1 consolidation.

_The next entry number is D-017._
