# dax-kiro-proxy specification repository

This repository is the implementation boundary for a new, standalone Kiro ACP proxy.
Implementation is proceeding in Go from these specifications. It contains no source code copied from,
derived line-by-line from, or linked to another repository.

The intended product presents a loopback gateway for a documented Claude Code Messages subset
and uses the locally installed Kiro CLI through Agent Client Protocol (ACP) as its model
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
9. `LANGUAGE_DECISION_RECORD.md` — confirmed Go selection and the historical Go/Rust experiment design.
10. `DEPENDENCY_REVIEW.md` — candidate licenses, provenance checklist, and outstanding review work.
11. `IMPLEMENTATION_DECISIONS.md` — adopted wire, lifecycle, and resource policies.
12. `DEVELOPMENT_STATUS.md` — implementation evidence and remaining acceptance gates.
13. `LIVE_KIRO_TEST_PLAN.md` — bounded opt-in client-denial/native-effect experiments and their limits.

`AGENTS.md` makes this reading order mandatory for coding agents.

## Current phase

The user selected Go and authorized implementation through the complete standalone product. The
specification-only baseline is `7b108dd`. Phase 0 reports retain the historical unmeasured experiment
design; the explicit language selection supersedes comparative experiments as a selection gate.
Rights/license and live-release gates remain open. Implementation begins with independent fixtures
and fake-process transport tests. Consult DEVELOPMENT_STATUS.md for verified progress.

## Development commands

With Go 1.27.1, build a local development executable:

```sh
mkdir -p dist
go build -o ./dist/dax-kiro-proxy ./cmd/dax-kiro-proxy
./dist/dax-kiro-proxy --help
./dist/dax-kiro-proxy doctor --json --timing
./dist/dax-kiro-proxy models
```

The checks support Kiro CLI 2.21.1 (including its adjacent `kiro-cli-chat` helper) and Claude Code
2.1.263. They use the installed CLIs' finite version/account/catalog commands, without an ACP session
or model prompt. `--kiro` and `--client` accept absolute executable paths. Private scope/cache state
defaults to `~/.dax-kiro-proxy`; `--state-dir` selects a different private directory. Temporary runtime
files are removed when the command finishes. Source client settings are not modified.

`doctor` succeeding means its checks completed; inspect `launch_available` and `policy` separately.
The current `run` command stops with exit 3 after successful preflight because effective Kiro execution
restrictions remain unverified. There is no override. Its full startup/runtime/shutdown composition is
tested with independent fake processes. One explicitly approved live Kiro Read-denial round trip also
passes: the client refuses the tool, Kiro receives that refusal and completes the turn. Broader Kiro
execution restrictions and the complete product acceptance gates remain unfinished. This development
build is not a release or installation procedure.

The initial-session native-effect challenge also passes with unchanged files and no observed tool
effects. Six actual Claude/fake ACP cases verify allowed Read/Write/Bash, denied Write/Bash and Bash
hook vetoes. Interactive approval/diff UI, remaining isolation paths and the real Kiro variants still
need verification; these partial results do not automatically enable run.

Development launch and release readiness are separate milestones in ACCEPTANCE_SPEC.md. The next
priority is effective Kiro isolation and real client tool approval/denial/hook round trips, followed
by client-environment preservation and the documented request subset. Optional web/account-usage
features, full Anthropic API coverage and release soak tests are not development-launch prerequisites.

## Naming

`dax-kiro-proxy` is the working product and executable name. Names, environment variables, endpoint
prefixes, and temporary artifacts belonging to an earlier host project are not part of this contract.
