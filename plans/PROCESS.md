# Development Process

This is the canonical contract for taking a Gibson milestone from plan to `main`.
`SPEC.md` is normative for product behavior, `MILESTONE_CONVENTIONS.md` binds the
cross-milestone seams that the specification leaves open, and `BACKGROUND.md` preserves
non-normative rationale.

An active milestone file in `plans/` is authoritative for that milestone's outcomes and
acceptance boundary; any implementation detail or chunking it contains is suggestive.
Root `PLAN.md` is the authoritative implementation plan: it defines how the active
milestone will be delivered and verified. Inactive milestone files are provisional
forecasts until they pass their plan-gate review. Both the active milestone file and root
`PLAN.md` are temporary and are retired when the milestone is consolidated.

## Branch roles and ancestry

- **`main`** contains only reviewed, consolidated milestones.
- **The milestone branch** starts from the current `main` tip and integrates every chunk
  for one milestone. It is the target of chunk pull requests and, once consolidated, the
  source of the milestone pull request to `main`. It is deleted after that pull request is
  reviewed and squash-merged into `main`.
- **A chunk branch** starts from the current milestone-branch tip. Its pull request targets
  the milestone branch, never `main`. It is deleted after that pull request is
  squash-merged. The next chunk starts from the new milestone-branch tip so its ancestry
  includes all accepted work.

Branch names may follow the repository's current convention; the roles, ancestry, and
pull-request targets above are the contract, not exact name strings.

## Chunk design

Every milestone has multiple chunks, recorded in root `PLAN.md` in dependency order.
Chunks are vertical slices through the implementation, not consecutive storage, service,
and presentation layers. Prefer every chunk to add a capability a human can exercise
through Gibson's CLI, API, or browser surface. When isolated review work is necessary, an
individual chunk may omit a new human-facing capability, but two such chunks may never be
consecutive.

Each root plan begins with a progress checklist containing one unchecked item per chunk.
Each chunk states the human outcome it delivers, or why it is review-only, then records
its implementation and verification work as unchecked task-list items. Those items cover:

- its implementation boundary and dependencies;
- the primary test owner for each behavior and the cheapest faithful verification;
- a human proof for any new interaction; and
- the agent verification required before review.

Update these checkboxes as work is completed. Check a chunk in the progress list only
after all of its implementation and verification items are checked and its human proof
and local `make check` pass. Pull-request, CI, review, and merge state remain governed by
the workflow below rather than being implied by the plan checkbox.

A human proof demonstrates the built product from its real interaction surface; it does
not replace automated tests, `make check`, code review, or the final milestone acceptance
workflow. Chunk boundaries should optimize for coherent human review without withholding
working behavior merely to keep architectural layers separate.

## Milestone workflow

1. **Plan the milestone.** Review its provisional milestone file against current code,
   tests, `SPEC.md`, `MILESTONE_CONVENTIONS.md`, and the completed product baseline. Settle
   the authoritative outcomes and acceptance boundary, mark the milestone active, then
   create root `PLAN.md` before writing implementation code. Translate the milestone into
   reviewable vertical chunks, architecture and dependency decisions, test ownership,
   per-chunk human proofs, and an agent-verified end-to-end workflow. Review and approve
   `PLAN.md`; if implementation strategy changes later, update it before the affected work
   proceeds. Milestone implementation notes remain inputs, not constraints on the root
   plan.
2. **Create the milestone branch.** Branch from the current `main` tip. This is the
   integration line for the milestone.
3. **Deliver each chunk in sequence.** For each planned chunk:
   1. branch from the current milestone-branch tip;
   2. implement the chunk and its cheapest faithful verification;
   3. build the real product and run the chunk's human proof when it has one;
   4. run `make check` locally;
   5. open a pull request targeting the milestone branch and require green CI;
   6. have Javier code-review it, codify any lasting review guardrail, and squash-merge
      it; then
   7. start the next chunk from the resulting milestone-branch tip.
4. **Review the complete milestone end to end.** After every chunk is merged, run the real
   built binary through root `PLAN.md`'s documented workflow and retain a clean transcript.
   Durable automated coverage of the same boundaries belongs at the cheapest faithful
   integration or system layer and runs inside `make check`. Javier then performs the
   milestone's capability demo; the agent workflow does not replace that human acceptance
   gate.
5. **Consolidate the milestone.** Reconcile the completed implementation as described
   below, then rerun verification. If consolidation changes behavior, repeat the affected
   automated, agent-driven, and human checks.
6. **Land the milestone.** Open the consolidated milestone branch as a pull request
   targeting `main`. Review it as a complete vertical slice, require green CI, and
   squash-merge it into `main`.
7. **Begin the next milestone.** Plan and branch it only from the new `main` tip, then
   repeat this workflow.

## Milestone consolidation

Consolidation turns an execution branch into an authoritative product baseline:

- Reconcile `SPEC.md` and `MILESTONE_CONVENTIONS.md` with all decided behavior. Update
  `BACKGROUND.md` only when durable rationale would otherwise be lost.
- Resolve every temporary divergence due at this boundary. Update the affected milestone
  file or files while they are still useful, update the relevant canonical authority,
  align code and tests, verify the result, and remove the intake entry from
  `DIVERGENCES.md`. That file
  is temporary intake, not a parallel specification or permanent decision archive.
- Reconcile tests with the stable observable contract and verify the complete workflow.
  For completed capabilities, current code, tests, `SPEC.md`, and
  `MILESTONE_CONVENTIONS.md` become authoritative together.
- Reconcile affected provisional milestone files enough to remove superseded seams,
  contradictions, and references to planning artifacts that will be retired. Their full
  implementation review still occurs at their own plan gates.
- Codify review findings that must hold in future work in `AGENTS.md`, lint or build
  configuration, or tests. Do not leave permanent guardrails only in review comments or
  memory.
- Update `MILESTONES.md` with a concise completion summary and check all documentation
  links, then retire both the completed milestone file and root `PLAN.md`. **Plan deletion
  is permanent:** completed planning artifacts are deleted, not archived, and must not
  remain as alternative descriptions of shipped behavior. Git history retains the
  implementation record.

## Standard exit workflow

A milestone exits only when all of these gates, plus its own specific exit criteria, hold:

- [ ] Every planned chunk was reviewed, passed local `make check` and CI, and was
      squash-merged into the milestone branch in sequence.
- [ ] Durable automated coverage of root `PLAN.md`'s end-to-end boundaries passes inside
      `make check`.
- [ ] An agent drove the real built binary through root `PLAN.md`'s documented end-to-end
      workflow and reported a clean transcript.
- [ ] Javier successfully demonstrated the milestone's capability from its real
      interaction surface.
- [ ] Canonical documentation was reconciled with decided and shipped behavior, and
      temporary divergence entries due now were removed.
- [ ] Affected provisional milestone files were reconciled and references to retired
      artifacts were removed.
- [ ] Review-derived guardrails were codified in the repository.
- [ ] The completed milestone file and root `PLAN.md` were deleted, and the roadmap and
      document links were checked.
- [ ] `make verify` is green on the consolidated milestone branch and the final pull
      request's CI is green.
- [ ] The consolidated milestone pull request was reviewed and squash-merged into `main`.
