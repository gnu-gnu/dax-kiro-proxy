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
For this measured combination, successful login and policy checks report `launch_available: true`
and `policy: verified`. This means
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
ACP. The shared launcher configuration also passes actual Bash approval and hook refusal. Actual
Kiro/client runtime cancellation after a delivered Read handoff and held client hook joins all
observed processes/artifacts (D74). Foreground Ctrl+C, streamed disconnect, process-loss, model-switch,
authentication and resume checks remain alpha work.

On Kiro 2.21.2, seven initial-session file-resource controls pass: active inherited files disappear
when default-resource inheritance is disabled, including with a separate session workspace. An
override in the process launch directory can re-enable inheritance; the launch directory must stay
owned and isolated. Initial skill controls now also pass for the owned launch and KIRO_HOME roots:
both are actively inherited with suppression off and absent with it on. Persisted-session loading
is disabled in this prepared launch path. Dynamic configuration changes and unmeasured source paths
are outside the initial-session evidence; the proxy issues no configuration reload command.

Default Claude Code traffic with 25 tools and its ordinary system prompt passes text and a Read
hook-denial round trip through the real gateway/validator/relay with fake ACP. Exact full-history
continuations can recreate after a changed standing instruction, validated tool registry (D70), or
successful tool results followed by client text (D73), with joined cleanup, one original deadline
and a default limit of sixteen recreations per turn. Unchanged result-only continuations keep their
prompt. Recreation adds provider work and loses hidden backend
context. Actual Kiro also passes default-tool Read hook refusal and one joined recreation (D71), with
all 25 client tools and ordinary thinking/context declarations. D72's actual registry experiment
reaches the wait, expanded tools, one plugin call and joined cleanup, but fails its final-answer
marker condition; the full live plugin gate remains open. Other lifecycle paths remain alpha
checks. No general Messages/API compatibility is implied.

Actual Claude with fake ACP also verifies process loss before Read delivery, a client-visible error,
joined old cleanup and a fresh text request in a new process (D75). Its actual-Kiro invocation stops
before model work because both CLI entry points currently report no logged-in account. Login renewal
is required for further live work; development execution-policy admission remains enabled.

The temporary client profile now retains standard-HOME user/local MCP declarations and decisions
at their native scopes. Installed-client controls verify all three MCP scopes, disabled/re-enabled
servers, project refusal and local/project/user precedence, with unchanged source files and joined
process cleanup. Plugins, skills, existing status commands and remote MCP OAuth remain separate
preservation work; see decision D64 for the measured limits.

Standard user plugins now also have a read-only seed path into the temporary profile. Full print
startup checks an owned plugin's MCP initialization/discovery, disable/re-enable behavior and source
preservation. Finite list commands do not establish that startup behavior. Plugin tool success and
hook refusal also pass through the real gateway/relay with fake ACP. The current interactive control
checks the client's `/mcp` connected screen before input; server initialization/listing alone proved
insufficient. A second print launch of the same private profile also completes a tool round trip.
The held-startup wait-to-plugin flow also passes through the real gateway/relay with fake ACP, for
both allowance and hook refusal: three main requests, two backend processes and one/zero native
calls. Other dynamic changes and arbitrary immediate-input coverage remain separate work (D70).

The private profile now also snapshots the two bounded native plugin registration files. This lets
the first session load installed plugin skills and hooks while retaining the read-only content seed.
Installed-client checks cover namespaced skill invocation, SessionStart/Stop hooks, disable/re-enable,
hook suppression and source preservation. Private uninstall leaves the original source intact; a Git
seed rejects marketplace update/removal even when a real local revision is available. A model-selected
owned skill now also completes through the real gateway/relay with fake ACP (D73), preserving its
successful result and separate client-expanded instructions through one joined recreation. Other
plugin assets, custom roots and actual Kiro skills remain unverified; full actual Kiro plugin
acceptance remains open after D72's partial observation.

Personal skills, legacy commands and agent definitions now retain their native user scope in the
temporary profile (D76). Installed-client controls verify explicit skill/command expansion and
same-name precedence against natural execution, with unchanged sources and joined cleanup. Each
launch snapshots the standard `~/.claude/{skills,commands,agents}` trees: at most 1,024 entries,
depth sixteen, 2 MiB per file and 32 MiB total. Unsafe links, special files, writable sources and
exceeded limits reject preparation. Changes to the original assets take effect on a new launch;
custom roots, actual agent execution and relative helper execution remain separate work.

Personal `~/.claude/rules` now remains available at its native scope through a validated source
reference (D77). Installed-client checks cover relative imports, original-path exclusions,
conditional activation after Read and the full four-hop import limit. The source reference is
not an immutable snapshot; preparation validates the shared asset bounds and cleanup removes only
the private link. Personal `~/.claude/CLAUDE.md` is still not loaded by the temporary profile.
Tested adapters either bypass its original exclusion, lose one import hop or omit a root body
with path frontmatter. None is enabled. This remains a client-environment compatibility gap.

The product status display now yields to existing user/project/local status choices, including
commands without a refresh interval. It supplies a bounded user-scope default only when no such
choice or uncertain project source is found. Linked worktrees and unsafe/ambiguous sources suppress
this optional default; native settings resolution and separate notice/metrics hooks remain with
the client. Installed UI checks verify precedence, unchanged sources, no extra periodic execution
and process cleanup (D67). Dynamic changes and managed/custom source coverage remain separate work.

Development launch and release readiness are separate milestones in ACCEPTANCE_SPEC.md. The next
priority is client-environment preservation, broader client request compatibility and the
live alpha lifecycle checks. Optional web/account-usage
features, full Anthropic API coverage and release soak tests are not development-launch prerequisites.

## Naming

`dax-kiro-proxy` is the working product and executable name. Names, environment variables, endpoint
prefixes, and temporary artifacts belonging to an earlier host project are not part of this contract.
