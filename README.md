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
./dist/dax-kiro-proxy run
```

The diagnostic checks support Kiro CLI 2.21.2 (including its adjacent `kiro-cli-chat` helper) and Claude Code
2.1.263. They use the installed CLIs' finite version/account/catalog commands, without an ACP session
or model prompt. `--kiro` and `--client` accept absolute executable paths. Private scope/cache state
defaults to `~/.dax-kiro-proxy`; `--state-dir` selects a different private directory. Temporary runtime
files are removed when the command finishes. Source client settings are not modified.

Development `run` is enabled on macOS arm64 for the measured Kiro 2.21.2/v2 and Claude Code 2.1.263
combination. Run it from a foreground terminal. Other versions, mismatched main/helper pairs and
unverified execution platforms are rejected; there is no trust override. Each launch owns a temporary
Kiro configuration with default-resource suppression, and each ACP process has a separate relay-only
agent directory. The client continues to decide tool permissions and execute tools.

`doctor` succeeding means its checks completed; inspect `launch_available` and `policy` separately.
The current installed combination reports `launch_available: true` and `policy: verified`. This means
the measured development policy is available, not that the client has initialized or all release
gates have passed. The compiled run command's full terminal/startup/tool/shutdown composition passes
with independent fake processes. This development build is not a release or installation procedure.

The initial-session native-effect challenge and real-client Read-hook refusal both pass freshly on
Kiro 2.21.2, with unchanged files and joined process cleanup. Six actual Kiro 2.21.2 / Claude 2.1.263
cases now also verify allowed Read/Write/Bash, denied Write/Bash and Bash hook vetoes, including
matching results, effects and cleanup. Relay descriptions explicitly associate opaque wire names
with original client names. Interactive Write/Bash approval and refusal with a comment pass with fake ACP;
the Write content/permission screen is checked before input. Bare refusal now verifies cleanup at
the existing deadline and safe recreation for a following question, using actual Claude with fake
ACP. The shared launcher configuration also passes actual Bash approval and hook refusal. Further
live cancellation, process-loss, model-switch, authentication and resume checks remain alpha work.

On Kiro 2.21.2, seven initial-session file-resource controls pass: active inherited files disappear
when default-resource inheritance is disabled, including with a separate session workspace. An
override in the process launch directory can re-enable inheritance; the launch directory must stay
owned and isolated. Initial skill controls now also pass for the owned launch and KIRO_HOME roots:
both are actively inherited with suppression off and absent with it on. Persisted-session loading
is disabled in this prepared launch path. Dynamic configuration changes and unmeasured source paths
are outside the initial-session evidence; the proxy issues no configuration reload command.

Default Claude Code traffic with 25 tools and its ordinary system prompt now passes text and a Read
hook-denial round trip through the real gateway/validator/relay with fake ACP. The client changes its
standing system suffix after that tool result. One exact full-history continuation can therefore
recreate the backend after joined cleanup, retaining new instructions and the original deadline.
This can add provider work and loses hidden backend context; actual Kiro validation of this new
recovery path remains an alpha check. No general Messages/API or user-asset preservation is implied.

The temporary client profile now retains standard-HOME user/local MCP declarations and decisions
at their native scopes. Installed-client controls verify all three MCP scopes, disabled/re-enabled
servers, project refusal and local/project/user precedence, with unchanged source files and joined
process cleanup. Plugins, skills, existing status commands and remote MCP OAuth remain separate
preservation work; see decision D64 for the measured limits.

Standard user plugins now also have a read-only seed path into the temporary profile. Full print
startup checks an owned plugin's MCP initialization/discovery, disable/re-enable behavior and source
preservation. Finite list commands do not establish that startup behavior. The first request still
lacks this asynchronously loaded tool; later tool discovery/round trips and broader plugin assets
remain open (D65).

Development launch and release readiness are separate milestones in ACCEPTANCE_SPEC.md. The next
priority is client-environment preservation, broader client request compatibility and the
live alpha lifecycle checks. Optional web/account-usage
features, full Anthropic API coverage and release soak tests are not development-launch prerequisites.

## Naming

`dax-kiro-proxy` is the working product and executable name. Names, environment variables, endpoint
prefixes, and temporary artifacts belonging to an earlier host project are not part of this contract.
