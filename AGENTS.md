# When writing tests

- Before adding a test, state the realistic regression or explicit requirement it protects and why this is the cheapest layer that proves it faithfully. If you cannot, do not add it. Optimize for confidence, not coverage itself.
- Give each behavior one primary test owner at the layer closest to the likely defect. Use higher layers to prove confidence unavailable below, not to duplicate lower-layer assertion matrices.
- Assert stable, observable contracts and semantics. Do not pin private details, incidental wording, internal call order, temporary behavior, or framework implementation. Pure refactoring should not require broad test rewrites.
- Keep unit tests fast, deterministic, isolated, and independent of ambient machine state. Explicitly control time, randomness, mutable state, files, external dependencies, and test order.
- Use test doubles only at genuine boundaries. Do not mock the subject under test or reproduce its implementation through interaction assertions.
- Use integration, system, or end-to-end tests when the risk depends on real boundaries, processes, lifecycles, execution environments, external systems, or complete wiring. Treat a green suite as evidence, not proof, that requirements are satisfied.

# When validating browser behavior

- Use `agent-browser` for browser-based validation and end-to-end browser proofs.
