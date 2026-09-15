# dax-kiro-proxy documentation index

This repository is the implementation boundary for a new, standalone Kiro ACP proxy.
Implementation is proceeding in Go from these specifications. It contains no source code copied from,
derived line-by-line from, or linked to another repository.

The intended product presents a loopback gateway for a documented Claude Code Messages subset
and uses the locally installed Kiro CLI through Agent Client Protocol (ACP) as its model
backend. The client remains responsible for executing tools and enforcing its own permission and hook
policies.

## Document order

All specification and record documents live in this `docs/` directory; the repository root keeps only
the Korean overview `README.md` and `AGENTS.md`. Bare document names inside `docs/` refer to sibling
files in this directory. Read these documents before implementation:

1. [CLEAN_ROOM_BOUNDARY.md](CLEAN_ROOM_BOUNDARY.md) — provenance, licensing boundary, and prohibited inputs.
2. [PRODUCT_SPEC.md](PRODUCT_SPEC.md) — goals, scope, external behavior, and security posture.
3. [PROTOCOL_SPEC.md](PROTOCOL_SPEC.md) — HTTP, SSE, ACP, and relay wire contracts.
4. [FLOWS_AND_STATE.md](FLOWS_AND_STATE.md) — request flows, state machines, recovery, and concurrency.
5. [IMPLEMENTATION_REQUIREMENTS.md](IMPLEMENTATION_REQUIREMENTS.md) — component boundaries and staged delivery plan.
6. [ACCEPTANCE_SPEC.md](ACCEPTANCE_SPEC.md) — black-box requirements and release gates.
7. [LANGUAGE_DECISION.md](LANGUAGE_DECISION.md) — criteria for selecting Go or Rust before implementation.
8. [PHASE_0_REVIEW.md](PHASE_0_REVIEW.md) — specification findings, proposed resolutions, and remaining gates.
9. [LANGUAGE_DECISION_RECORD.md](LANGUAGE_DECISION_RECORD.md) — confirmed Go selection and the historical Go/Rust experiment design.
10. [DEPENDENCY_REVIEW.md](DEPENDENCY_REVIEW.md) — component and license inventory, retained notices, advisory scan, and
    the owner's external rights items.
11. [IMPLEMENTATION_DECISIONS.md](IMPLEMENTATION_DECISIONS.md) — adopted wire, lifecycle, and resource policies.
12. [DEVELOPMENT_STATUS.md](DEVELOPMENT_STATUS.md) — implementation evidence and remaining acceptance gates.
13. [LIVE_KIRO_TEST_PLAN.md](LIVE_KIRO_TEST_PLAN.md) — bounded opt-in client-denial/native-effect experiments and their limits.
14. [HANDOFF_REVIEW_2026-09-11.md](HANDOFF_REVIEW_2026-09-11.md) — review brief for D109–D120: decisions needing judgment,
    production changes, verification state, open items and the commit map.
15. [HANDOFF_AGENT_2026-09-12.md](HANDOFF_AGENT_2026-09-12.md) — agent handoff after D124: inherited state, the user's standing
    instructions, the batch and artifact procedure, the remaining planned batches D125–D128 with
    code positions, and decisions waiting on the user.
16. [USAGE_AND_EVIDENCE.md](USAGE_AND_EVIDENCE.md) — development commands and options in detail, the
    measured compatibility and installation behavior, and per-decision verification evidence (moved
    from the root README on 2026-09-15).

[`AGENTS.md`](../AGENTS.md) makes this reading order mandatory for coding agents.

## Current phase

The user selected Go and authorized implementation through the complete standalone product. The
specification-only baseline is `7b108dd`. Phase 0 reports retain the historical unmeasured
experiment design; the explicit language selection supersedes comparative experiments as a selection
gate. The dependency inventory is complete for the darwin/arm64 development artifact (D121); the
owner's rights and project-license decisions are outside this repository, and live-release gates
remain open. Implementation began with independent fixtures and fake-process transport tests and now
reaches an installed development artifact (D146); consult DEVELOPMENT_STATUS.md for verified
progress, `HANDOFF_REVIEW_2026-09-11.md` for the D109–D120 review brief and
`HANDOFF_AGENT_2026-09-12.md` for the agent handoff after D124.

## Naming

`dax-kiro-proxy` is the working product and executable name. Names, environment variables, endpoint
prefixes, and temporary artifacts belonging to an earlier host project are not part of this contract.
