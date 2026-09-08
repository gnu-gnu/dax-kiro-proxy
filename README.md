# dax-kiro-proxy specification repository

This repository is the implementation boundary for a new, standalone Kiro ACP proxy.
It currently contains specifications only. It intentionally contains no source code copied from,
derived line-by-line from, or linked to another repository.

The intended product presents an Anthropic Messages-compatible loopback gateway to a client such as
Claude Code and uses the locally installed Kiro CLI through Agent Client Protocol (ACP) as its model
backend. The client remains responsible for executing tools and enforcing its own permission and hook
policies.

## Document order

Read these documents before implementation:

1. `CLEAN_ROOM_BOUNDARY.md` — provenance, licensing boundary, and prohibited inputs.
2. `PRODUCT_SPEC.md` — goals, scope, external behavior, and security posture.
3. `PROTOCOL_SPEC.md` — HTTP, SSE, ACP, and relay wire contracts.
4. `FLOWS_AND_STATE.md` — request flows, state machines, recovery, and concurrency.
5. `IMPLEMENTATION_REQUIREMENTS.md` — component boundaries and staged delivery plan.
6. `ACCEPTANCE_SPEC.md` — black-box requirements and release gates.
7. `LANGUAGE_DECISION.md` — criteria for selecting Go or Rust before implementation.
8. `PHASE_0_REVIEW.md` — specification findings, proposed resolutions, and remaining gates.
9. `LANGUAGE_DECISION_RECORD.md` — current Go recommendation and the Go/Rust experiment design.
10. `DEPENDENCY_REVIEW.md` — candidate licenses, provenance checklist, and outstanding review work.

`AGENTS.md` makes this reading order mandatory for coding agents.

## Current phase

The current phase is Phase 0 review and experiment design. The review recommends Go; the language
record is proposed, not final. Experiments, fixture freeze, owner rights confirmations, and the full
dependency review remain open. The Phase 0 documents record proposed specification changes and do not
silently override the original requirements or acceptance criteria.

Do not add production implementation code until the Phase 0 gate is complete and the language decision
is finalized. Once implementation begins, it must be based only on these documents and independently
obtained public protocol documentation identified here.

## Naming

`dax-kiro-proxy` is the working product and executable name. Names, environment variables, endpoint
prefixes, and temporary artifacts belonging to an earlier host project are not part of this contract.
