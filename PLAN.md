# M0 Implementation Plan

## Objective

Build M0 as a maintainable walking skeleton while keeping the human owner involved in its design and construction.

M0 is complete when a single `build/gibson` binary can be launched from inside a Git checkout, load and validate that checkout's `gibson.toml`, derive the workspace root, perform the required startup checks, and serve the React application shell and health API. Development mode must proxy the application shell to Vite while Gibson continues to own `/api/*`.

The product outcomes in `plans/SPEC.md`, `plans/BACKGROUND.md`, and `plans/MILESTONES.md` remain fixed. Implementation details in the existing milestone plans and conventions are design inputs rather than unquestioned requirements.

## Working agreement

Implementation proceeds through capability checkpoints. At the end of each checkpoint, stop and present:

1. The capability that now works.
2. A concise map of the files and package responsibilities.
3. The primary control flow through the new code.
4. Important design choices and their trade-offs.
5. Deviations added to `DEVIATIONS_M0.md`.
6. Automated test results and an agent-run functional proof.
7. Any decisions required before the next checkpoint.

Do not begin the next checkpoint until the human owner approves the current one.

Existing plans remain unchanged during M0 implementation. Record concrete departures in `DEVIATIONS_M0.md` as they arise. Reconcile `plans/PLAN_M0.md`, `plans/PLAN_CONVENTIONS.md`, and downstream milestone plans only after M0 is working and has been evaluated as a whole.

## Locked outcomes

M0 must preserve these externally meaningful outcomes:

- `gibson serve [--port N] [--dev]` uses Cobra and reports failures clearly.
- Gibson can be launched from the root or a subdirectory of a Git checkout.
- The launch checkout is the Git root; the workspace root is its parent.
- Gibson reads exactly one committed `gibson.toml` from the launch checkout root.
- Configuration supports the specified server and session-type schema, including opaque `extra_args`.
- Missing or invalid configuration, an occupied port, a missing pi binary, and an unsupported pi version remain distinguishable failures.
- `--port` overrides the configured port; occupied ports do not auto-increment.
- Missing `.gibson/` ignore coverage and zero configured session types produce warnings rather than failures.
- Gibson checks the supported pi version at startup.
- Production builds embed the React application and ship as one binary.
- `GET /api/health` reports health and build version.
- Unknown `/api/*` paths return the JSON error envelope rather than SPA HTML.
- Non-API routes support SPA history fallback.
- `--dev` proxies non-API traffic to Vite while Gibson retains `/api/*`.
- SIGINT and SIGTERM shut the server down gracefully.
- M0 does not implement pi RPC, sessions, persistence, SSE, checkout enumeration, or the full chat interface.

## Architecture

### Control flow

```text
main.go
  -> cmd.Execute
    -> newRootCommand
      -> newServeCommand
        -> internal/app.Serve
          -> internal/config
          -> internal/workspace
          -> internal/pisession
          -> internal/store
          -> internal/httpapi
          -> web
```

`main.go` owns process exit and top-level error presentation. `cmd` owns Cobra command construction, argument and flag parsing, and translation into application inputs. `internal/app` owns startup order, dependency composition, server lifetime, and shutdown. Focused domain packages own their respective behavior.

### Package map

- `cmd/` — Cobra adapter; fresh command constructors and one file per command.
- `internal/app/` — application workflows, beginning with `Serve`.
- `internal/config/` — `gibson.toml` decoding, defaults, validation, and unknown-key reporting.
- `internal/workspace/` — checkout discovery and workspace-root derivation.
- `internal/pisession/` — pi binary resolution and compatibility checking in M0.
- `internal/store/` — `.gibson/` Git-ignore verification in M0.
- `internal/httpapi/` — health and API handlers, SPA serving, and development proxying.
- `internal/testws/` — reusable scratch grove-style workspaces for tests that need real Git behavior.
- `web/` — React, TypeScript, Vite, and the embedded production distribution.

These are durable responsibility boundaries, not permission to speculate. Add only APIs required by current M0 consumers. When a concrete API differs from a future-facing signature in the existing plans, record that difference separately in `DEVIATIONS_M0.md`.

### CLI conventions

Follow the maintainable conventions shared by Command K and Overlay:

- A thin `main.go`.
- A newly constructed Cobra tree rather than package-global command state.
- `SilenceErrors` and `SilenceUsage` on the root command.
- A build-time `Version` value defaulting to `"n/a"`.
- One command per file with focused sibling tests where command behavior exists.
- Errors returned through Cobra and formatted once at the process boundary.
- A discoverable Makefile with `help`, `web`, `build`, `test`, formatting, tidy, lint, aggregate `check`, and `clean` targets.
- Race detection in the Go test target.
- Build output at `build/gibson`; Vite output remains under `web/dist/`.

## Verification strategy

Each behavior has one primary automated test owner. Higher-level tests prove meaningful composition rather than repeating every lower-level assertion.

- `internal/config` owns schema, default, validation, unknown-key, and opaque-value tests.
- `internal/workspace` owns Git-root and workspace derivation tests, including subdirectories and worktrees where relevant.
- `internal/pisession` owns pi resolution, execution failure, and supported-version tests.
- `internal/store` owns Git-ignore detection tests.
- `internal/httpapi` owns health, API 404, static asset, history fallback, and proxy-routing tests.
- `internal/app` owns representative startup ordering, dependency composition, warnings, listener lifecycle, and shutdown tests.
- `cmd` owns Cobra shape, flags, version, and process-facing presentation where those contracts are not already proven below it.
- The web shell is validated through its production build and browser-level behavior; add frontend unit infrastructure only when M0 contains logic that benefits from it.

Tests should assert stable contracts: error categories, required field names, status codes, envelope shapes, and actionable details. Avoid pinning incidental prose or private call order unless that order is itself user-visible behavior.

## Pull request workflow

### Milestone trunk

`feature/m0` is the integration trunk for the milestone. Checkpoint work does not target `main` directly. Each checkpoint is integrated into `feature/m0` before the next checkpoint begins, so every branch starts from the complete and approved result of the preceding checkpoint.

Use branch names that identify both the milestone and checkpoint:

- `feature/m0-c1-cli-spine`
- `feature/m0-c2-production-serve`
- `feature/m0-c3-startup-contracts`
- `feature/m0-c4-development-loop`
- `feature/m0-c5-acceptance`

### Checkpoint cycle

For each checkpoint:

1. Update the local `feature/m0` from its remote branch.
2. Create the checkpoint branch from that exact `feature/m0` head before implementation starts.
3. Implement the checkpoint in coherent commits. Keep unrelated work out of the branch.
4. Run the checkpoint's automated tests and capability proof.
5. Update `DEVIATIONS_M0.md` with any concrete departure discovered by the working code.
6. Once the checkpoint is complete and proven, open a non-draft pull request from the checkpoint branch into `feature/m0`.
7. In the pull request description, include the delivered capability, code map, primary control flow, important design choices, deviations, verification commands and results, and any known follow-up work.
8. Stop for human review. Address feedback on the same checkpoint branch and rerun affected verification.
9. After human approval and green checks, squash-merge the checkpoint pull request so `feature/m0` gains one durable commit for that checkpoint, then delete the checkpoint branch.
10. Begin the next checkpoint only after the preceding pull request has merged.

Do not stack checkpoint branches or develop checkpoints concurrently unless the human owner explicitly changes this workflow. The sequential dependency is intentional: each review can reshape the next checkpoint before its branch exists.

### Reconciliation and promotion

After Checkpoint 5 is accepted and merged:

1. Create `feature/m0-reconcile-plans` from the resulting `feature/m0`.
2. Reconcile `plans/PLAN_M0.md`, `plans/PLAN_CONVENTIONS.md`, and affected downstream plans against the completed `DEVIATIONS_M0.md` and working implementation.
3. Open a separate reconciliation pull request into `feature/m0`, stop for human review, and squash-merge it after approval.
4. Open the final milestone pull request from `feature/m0` into `main`. Its description links all checkpoint pull requests, summarizes the final architecture and acceptance proof, and explains how the deviation ledger was reconciled.
5. Merge the final milestone pull request with a merge commit. Do not squash or rebase it: the merge commit marks the M0 boundary while preserving one commit per checkpoint and the reconciliation commit in `main`.

## Capability checkpoints

### Checkpoint 1 — Repository and CLI spine

Create the project foundation:

- Go module and dependency metadata.
- Thin process entrypoint.
- Fresh Cobra root and `serve` command skeleton.
- Build-time version injection.
- Root ignore rules.
- Makefile quality and build targets using `build/gibson`.
- Initial `internal/app` boundary, without speculative application APIs.

Capability proof:

- `make help` documents the workflow.
- `make check` passes for the foundation.
- `make build` creates `build/gibson`.
- `build/gibson --help`, `--version`, and `serve --help` behave correctly.

Human review focuses on the repository map, Cobra construction, process error ownership, and the `cmd` to `internal/app` boundary.

### Checkpoint 2 — First end-to-end production serve

Build the thinnest complete production vertical:

- Decode and validate `gibson.toml`.
- Locate the launch checkout and derive its workspace root.
- Resolve effective bind and port settings.
- Add the application-owned serve lifecycle.
- Add the health route and API 404 boundary.
- Scaffold the React/Vite shell and production build.
- Embed the production distribution and serve it with history fallback.
- Add a dogfood `gibson.toml` for this repository.

Choose the smallest maintainable solution to the fresh-clone `go:embed` bootstrap problem during this checkpoint. Record a deviation if the implemented solution differs from `plans/PLAN_M0.md`.

Capability proof:

- Build one binary from a clean tree.
- Launch it from a scratch checkout and from a nested subdirectory.
- Fetch health JSON, the embedded shell, a built asset, and a deep client route.
- Confirm an unknown API route returns JSON rather than HTML.
- Confirm the shell displays the health-derived version in a browser.

Human review focuses on the complete request path, application lifecycle, package dependencies, and Go/web build boundary.

### Checkpoint 3 — Startup contracts and operational behavior

Complete the startup experience:

- Configuration defaults and field-specific validation.
- Unknown-key and zero-session-type warnings.
- `.gibson/` ignore verification and actionable warning.
- Pi binary resolution and supported-version check.
- `--port` override behavior.
- Occupied-port failure without auto-increment.
- Embedded-asset readiness validation where needed.
- Structured startup logging.
- Graceful SIGINT and SIGTERM shutdown.

Capability proof:

- Exercise each locked failure category and confirm it is distinguishable and actionable.
- Confirm warnings do not prevent serving health.
- Confirm the port override works.
- Confirm an occupied port fails and no adjacent port is selected.
- Confirm server shutdown leaves no process behind.

Human review focuses on startup order as user experience, error ownership, warning policy, and lifecycle cleanup.

### Checkpoint 4 — Development loop

Add development-mode serving:

- `--dev` selects a reverse proxy for non-API requests.
- Vite runs at the documented development address.
- Gibson remains the sole owner of `/api/*`.
- No second proxy configuration or CORS layer is introduced.
- Missing embedded production assets do not block development mode.

Capability proof:

- Run Vite and Gibson together.
- Confirm the root response comes from Vite.
- Confirm `/api/health` and unknown API routes still come from Gibson.
- Confirm the browser loads the application through Gibson's origin and hot reload works.
- Stop both processes cleanly.

Human review focuses on local development ergonomics and the production/development handler seam.

### Checkpoint 5 — M0 acceptance and evaluation

Finish the milestone without adding later-milestone behavior:

- Run the complete quality gate and acceptance workflow.
- Remove accidental duplication, dead code, and speculative APIs.
- Audit package responsibilities and dependency direction.
- Audit `DEVIATIONS_M0.md` for every concrete departure discovered during implementation.
- Produce a final code map and explain how to trace CLI startup, configuration, HTTP handling, and web serving.
- Evaluate the working design with the human owner before changing any existing plans.

After M0 is accepted, perform a separate reconciliation pass over the deviation ledger and decide which existing plans and conventions should change.

## Agent-verified end-to-end workflow

1. From `~/Code/github.com/jmcampanini/gibson/main`, run the aggregate non-mutating quality gate and production build. Assert that all checks pass, `build/gibson` exists, its version is populated, and the working tree contains no deleted embed placeholder or generated-file noise.
2. Under `.sandbox/`, create a grove-style workspace containing a committed Git checkout, `.gitignore` entry for `.gibson/`, and valid `gibson.toml` with one session type. Use a supported real pi installation for the environment proof; automated tests continue to use local stubs and no network.
3. Launch the absolute `build/gibson serve` path from the scratch checkout. Assert that `/api/health` returns healthy JSON with the build version, `/` serves the embedded React shell, a built asset is reachable, a deep client route falls back to the shell, and an unknown `/api/*` route returns the JSON 404 envelope.
4. Repeat the launch from a nested checkout directory. Assert that Gibson identifies the same launch checkout and the parent workspace root.
5. Exercise missing config, malformed config, invalid required fields, occupied port, missing pi, and unsupported pi version. Assert that each fails without starting the server and reports the distinguishing actionable details required by the locked outcomes.
6. Exercise a missing `.gibson/` ignore entry and zero session types. Assert that each warns while the health endpoint remains available. Exercise `--port` and assert that it overrides configuration without auto-increment behavior.
7. Send SIGINT and SIGTERM in separate runs. Assert graceful exit and no surviving Gibson process.
8. Start Vite and `gibson serve --dev`. Through Gibson's origin, assert that the application shell comes from Vite while `/api/health` and unknown API routes remain Gibson-owned. Verify hot reload through browser automation when available, then stop both processes.
9. Open the production shell with browser automation and assert that it renders the Gibson heading and health-derived version. If browser automation is unavailable, pause and ask the human owner to perform this browser check before declaring M0 complete.
10. Confirm the scratch checkout has no `.gibson/` Git noise, the Gibson checkout has no unexpected generated changes, and no background processes remain. Remove `.sandbox/` proof artifacts.
11. Present the final code map, proof results, and complete `DEVIATIONS_M0.md` in the Checkpoint 5 pull request. Merge it into `feature/m0` only after human approval.
12. Complete the separate plan-reconciliation pull request into `feature/m0`, rerunning checks affected by documentation or workflow changes.
13. Open the final `feature/m0` to `main` milestone pull request with links to the five checkpoint pull requests, reconciliation pull request, final code map, and acceptance evidence. After green checks and human approval, merge it with a merge commit so the checkpoint commits remain visible in `main`.
