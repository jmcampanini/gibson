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

_The next entry number is D-018._
