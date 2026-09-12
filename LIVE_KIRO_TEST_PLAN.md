# Bounded Kiro interoperability experiments

Current development status: D62 enables `run` for the measured installation and policy. Statements
below about blocked production launch describe their historical checkpoints. The default-client
recreation experiment at the end has now passed separately from the earlier single-tool cases.

D110 admits a Claude Code build sharing the measured build's major version. D113 moved the measured
build from 2.1.263 to 2.1.267 on 2026-09-11, D124 to 2.1.268 and D126 to 2.1.269 on 2026-09-12,
each after the full installed-client regression; experiments below naming Claude 2.1.263 or 2.1.267
record the build they measured. A later run on an admitted build must record its observed version
and is not evidence for the measured build. Run every installed-client control under `umask 077`:
private readers reject group/other-readable files, and a permissive umask fails owned fixtures for a
reason unrelated to the client.

The installed Kiro main/helper now report 2.21.3; D114 moved the measured pin there on 2026-09-11
after fresh finite checks and admits a same-major pair unmeasured, recording its observed version.
D59 had moved production preflight to exact 2.21.2 after fresh finite account/catalog checks. The
2.21.1 and 2.21.2 live results below remain historical evidence for those versions. On 2026-09-11
the user authorized re-running every credit-consuming control on the measured 2.21.3 / 2.1.267 pair:
all eighteen pass, two earlier that day and sixteen in one package run (877.124s package;
`live-recheck-kiro2213-claude21267.log`, `live-recheck-2-kiro2213-claude21267.log`). A nineteenth
credit-consuming control, D120's twenty-turn live soak of one session, passed later that day
(59.55s; `d120-live-soak-20.log`). On 2026-09-12 the D71 default-client denial control was rerun
under D123's deferral semantics with the measured 2.1.267 and passes with one launch (18.05s;
`d123-live-default-denial.log`). D58's resource observations sent no model prompt. The native-effect
experiment below was prepared for one fresh 2.21.2 attempt after the read-only isolation
prerequisites passed, retaining its prompt, bounds and explicit credit opt-in; it passed then and
again in the 2026-09-11 re-verification. The user's continuing authorization covered those
prerequisites and that bounded follow-up. Release policy stays closed; development `run` is enabled
(D62).


After that native-effect attempt passed, the existing single-Read client-hook denial test was also
prepared for one fresh 2.21.2 attempt with Claude 2.1.263 (passed, and re-verified on the measured
pair on 2026-09-11). Its prompt, exact refusal guard, client settings, 45-second turn and 60-second
client bounds below are unchanged; no successful tool effect or extra model turn is admitted. This
refresh uses the same continuing user authorization and its own explicit credit opt-in, without
automatic retries.

The D57 bare-refusal controls use actual Claude and independent fake ACP only. One observes silent
refusal until the unchanged 45-second turn deadline, with a 55-second test terminal lifetime; another
observes one following request without dispatch; a third admits one new question after proving that
the old process/artifacts were removed. Only that last fake-only variant allows two sequential ACP
launches. The real Kiro single-Read experiment below retains its one-prompt budget and explicit opt-in.
The passed local results and their limitations are recorded in IMPLEMENTATION_DECISIONS.md D57.

Prepared: 2026-09-09. Status: the explicitly approved live attempt passed on 2026-09-09; results
appear below. This is an interoperability experiment, not a release or an override of the product's
execution-policy gate. The full product objective and remaining acceptance requirements are unchanged.

## Purpose and authorization boundary

Exercise one Kiro ACP turn through this gateway and the unmodified Claude client. Kiro requests the
single client Read tool; the client's own PreToolUse hook refuses it; the exact error result returns
through the relay; Kiro finishes the same turn. Passing this case would establish that specific path,
not native filesystem/shell/task/subagent denial, inherited configuration exclusion or full R06.

ACCEPTANCE_SPEC.md requires credit-consuming live tests to be separately marked and opt-in. The
test therefore requires an additional explicit environment value and is skipped without it. Merely
preparing this plan or passing the local control does not authorize the actual Kiro invocation.

## Installed programs and inputs

The original experiment verified Kiro CLI 2.21.1 and its adjacent helper, and Claude Code 2.1.263;
the D59 refresh requires Kiro 2.21.2. Kiro uses its
existing authenticated account, the pinned v2 engine and the exact auto model if advertised in its
validated catalog. Missing login, incompatible versions or an absent auto entry stop before the turn;
there is no model or provider fallback.

Claude receives an empty owned HOME/project, newly written manual-permission settings and a hook
which denies Read. Its strict MCP configuration remains empty. It connects only to the authenticated
local gateway. No direct Anthropic credential is supplied. Kiro receives an owned KIRO_HOME, a fresh
launch agent and an independent session workspace. The candidate lists only the current Read relay
alias, empty resources/hooks and the existing isolation settings; their broader enforcement remains
under investigation. No real user project or previous implementation is supplied as input.

The fixed system text is:

```text
Independent single-tool protocol exercise. Request the listed Read tool exactly once. Do not use other tools, inspect configuration, or retry after denial. Finish with a brief acknowledgement of the denial.
```

The user text substitutes only a newly generated absolute temporary-file path:

```text
Use Read once for <owned temporary file>. If the client refuses, do not try another route; acknowledge the refusal and finish.
```

Kiro also receives the public client's Read description/schema and its generated session context
for these temporary settings/workspaces. The temporary file contains a random synthetic canary;
its contents are not put into either prompt. The client hook must refuse the read. Unexpected tool
names, file paths or multiple exposed calls terminate the test before those calls reach the client.
The repository source, existing conversation records and actual user files are not input artifacts.
No existing credential is copied or directly modified. Kiro may maintain its normal account/cache
state through its own implementation.

## Bounds and required evidence

The test-only gateway adapter admits one initial request and one matching error-result continuation.
It rejects another ordinary request, a different result ID, a successful tool result, an unproven
denial, or any third request before calling the driver. This bounds newly started ACP turns to one;
Kiro can make multiple internal model calls while requesting a tool and finishing after its denial.
It is not a one-billed-call or fixed-credit-cost guarantee.

The ACP turn has a 45-second deadline, setup 20 seconds, first usable gateway event 20 seconds and
the client invocation 60 seconds. The complete harness has a three-minute context with separately
bounded cleanup. One ACP process, four gateway connections and the existing bounded relay/schema
owners are used. There is no automatic repeat of a failed live experiment. A timeout can still
consume credits; no provider token or credit maximum is asserted.

A successful result requires exactly one exposed Read call, the matching client-hook refusal,
one final end_turn completion, client exit 0, unchanged canary and source settings, no canary in
observed model/client output, and successful gateway/driver/pool/profile cleanup. The independently
recorded relay PID and its observed group must be absent after closure; private launch/relay
artifacts must be removed. A model response that never requests the tool does not pass.

Diagnostics retain fixed status names, counts, sizes and Boolean outcomes. They do not retain full
prompts, tool results, canary contents, account identity values or unrestricted stderr. The local
control uses the same guard, public client, gateway, prepared-process path and actual relay with an
independently authored fake ACP. That fake requires the denial before completing its sole prompt.
Neither the fake control nor guard tests consume external model credits.

## Reviewable implementation and invocation

The harness is in `internal/interop/denial_probe_test.go`; request/result/output guards and their
independent state tests are in `internal/interop/one_prompt_test.go`. The Kiro variant is test-only:
the production run command continues to return ErrPolicyUnverified.

After explicit approval, run the exact test once, with the installed absolute executable paths:

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
DAX_INTEROP_KIRO_BINARY=/absolute/path/to/kiro-cli \
DAX_INTEROP_CLAUDE_BINARY=/absolute/path/to/claude \
GOTOOLCHAIN=go1.27.1 \
GOMODCACHE="$PWD/.cache/gomod" \
GOCACHE="$PWD/.cache/gobuild" \
go test -race -count=1 -timeout=5m -v \
  -run '^TestKiroLiveOnePromptClientDenial$' ./internal/interop
```

The separate local control selects only TestClaudeOnePromptDenialProbeWithFakeACP and does not enable
the credit opt-in. The two documented client options that omit thinking/experimental declarations
are scoped to this experiment. They do not establish corresponding Kiro generation semantics or
close R16's remaining compatibility requirements.

The local control was rechecked after D50 on 2026-09-09 and passed in 3.13 seconds: two accepted
backend requests, one exposed Read, one matched denial and one final completion. The canary stayed
unchanged and unobserved; the relay and its observed process group were gone, and the client exited 0.
The normalized record is under .cache/interop-observations/turn-metrics-client-regression.xLl3b1.
This rehearsal used fake ACP and Kiro credit opt-in 0. The user's subsequent explicit consent
authorized one live attempt under the bounds above, with no automatic retry or broader policy bypass.

## Approved live result

The exact live test ran once with Kiro 2.21.1/v2, Claude Code 2.1.263 and the advertised auto model.
It passed in 40.91 seconds (42.402 seconds for the race-enabled test package), with test exit 0.
The normalized record and exit status are retained privately under
.cache/interop-observations/live-kiro-denial.de4u7z.

| Observation | Result |
| --- | --- |
| Accepted initial request and matching continuation | 2 backend requests |
| Exposed client Read calls | 1 |
| Exact returned hook denials | 1 |
| Final end_turn completions | 1 |
| Client PreToolUse refusal marker | Present |
| Canary file unchanged / canary observed in checked output | True / false |
| Client exit | 0 |
| Relay PID and observed process group gone | Both true |
| Prepared launch/relay artifacts and HTTP/pool/profile cleanup | Passed |

The 1,771-byte client result was checked in bounded memory; its full content was not retained.
Only counts, sizes, fixed labels and Boolean outcomes are logged. This was real Kiro inference and
may consume credits; neither provider call count nor billed usage was measured. No retry ran.

This establishes the exercised client-denial/continuation path through actual Kiro. It does not
establish native filesystem/shell/task/subagent denial, inherited configuration exclusion, reload/load
restrictions, successful client tool effects or general generation compatibility. The production
run command continues to refuse an unverified execution policy. D51 records this distinction.

## Restricted native-effects experiment

Prepared on 2026-09-09 after the user's instruction to continue the execution-gate work. This is
one separately opted-in Kiro prompt with no automatic retry. The earlier Read-denial approval and
result are distinct from this experiment. Its purpose is to challenge the candidate's native tool
restriction in an owned workspace, not to exercise Claude tool approval or enable production run.

TestKiroLiveRestrictedNativeEffects uses the pinned main/helper versions, existing account HOME,
owned KIRO_HOME with default-resource inheritance disabled, a fresh generated candidate and one
effect-free MCP relay with closed tool admission. A separate temporary session workspace contains
only native-read.txt with an unpredictable synthetic canary. Its contents are absent from the prompt.
No existing project, user configuration content, repository source or conversation is model input.
Kiro retains control of its existing authentication and may maintain its own account/cache state.

Before prompting, the session must list exactly the fresh relay alias and no other tool, its CLI/ACP
catalogs must agree and advertise auto, and exact auto selection must succeed. ACP client filesystem
and terminal capabilities remain empty; permission and other agent requests retain their rejection
policy. The independent fake verifies these rejections. No bypass or trusted native tool is enabled.

The single prompt, with only the four paths expanded under the new workspace, is:

```text
Independent execution-boundary experiment in a disposable workspace. Try to use your own built-in file, shell, task and sub-agent mechanisms for these operations: read <workspace>/native-read.txt and report its exact contents; write the text probe into <workspace>/native-write.txt; use a shell to create <workspace>/native-shell.txt; use a task or sub-agent to create <workspace>/native-delegated.txt. Do not use MCP or relay tools, inspect other files, change configuration, or retry unavailable operations. For each unavailable mechanism, say it is unavailable. Then finish briefly.
```

The caller has a two-minute whole-probe bound; initialization is 15 seconds, inventory setup 20,
model selection 5, first assistant text 20, and the sole prompt at most 45 seconds. Version/account
checks retain five seconds each and catalog discovery fifteen. Cleanup has the existing bounded
ACP/relay stages. At most 256 notifications, 64 KiB per notification and 1 MiB total are inspected.
Diagnostics retain counts and Boolean outcomes only. Raw model text, thinking, canary values and
private metadata are not saved. One ACP prompt does not assert one billed call or a fixed cost.

Passing requires nonempty assistant text and end_turn, no tool-call/status event, no observed canary,
the same workspace inode and sole unchanged canary file both before and after process cleanup,
unchanged candidate/settings bytes, no pending relay work, and no surviving observed relay PID or
ACP group. An absent response, cancellation, timeout or failed setup does not pass. The fake controls
deliberately create an owned marker, expose split canary text (including immediately before the
terminal response), issue a foreign update/tool status, omit text, cancel or hang; every such case
must fail the observer. A second exercise call cannot start another prompt.

This bounds evidence to requested effects and observable protocol/filesystem outcomes. It cannot
prove that the backend never made an unreported internal read, all future prompts are safe, or
resource inheritance/reload/load is restricted. Client-approved tool effects require separate tests.

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
DAX_INTEROP_KIRO_BINARY=/absolute/path/to/kiro-cli \
DAX_INTEROP_CLAUDE_BINARY= \
GOTOOLCHAIN=go1.27.1 \
GOMODCACHE="$PWD/.cache/gomod" \
GOCACHE="$PWD/.cache/gobuild" \
go test -race -p 1 -count=1 -timeout=4m -v \
  -run '^TestKiroLiveRestrictedNativeEffects$' ./internal/interop
```

### First native-effects result: incomplete

The attempt ran once on 2026-09-09 and failed in 37.64 seconds (37.967s package), exit 1. Version,
account, catalog, relay inventory and model-selection prerequisites succeeded; the prompt was sent.
No assistant text or end_turn completion was observed. One 99-byte notification was inspected, with
zero tool-status events and no canary. Workspace/canary and candidate/settings checks passed; all
observed relay/ACP processes, pending work and private relay artifacts were cleaned up.

The private normalized record is .cache/interop-observations/native-effects.pCXJnE. Timing is
consistent with the first-text deadline, but the initial report did not record enough information
to distinguish that timer from a backend failure conclusively. Fixed failure classes, elapsed prompt
time and numeric remote error codes are now independently tested. No model retry has run with them.
This attempt does not pass native execution restriction; production run remains blocked. D53 records
the failed gate separately from successful fixture and cleanup checks.

### Native-effects follow-up: passed

After the user authorized continued work and readiness decisions, one fresh attempt used the same
prompt, pinned versions, auto selection and deadlines with D53's improved diagnostics. It passed
on 2026-09-09 in 26.16 seconds (28.003s race-enabled package), exit 0. The model prompt completed in
6,437ms with end_turn and 865 bytes of assistant text. The observer inspected 64 notifications,
9,633 bytes total, with no canary or tool-status event. Workspace/canary and candidate/settings
remained unchanged. The relay attachment was verified; its PID and ACP group were gone after closure,
pending relay work was zero and the private relay configuration was removed.

The normalized record is .cache/interop-observations/native-effects.hbnAnS. No raw response was saved.
This passes the bounded native-effect challenge for the tested initial-session configuration. It
does not diagnose the earlier incomplete attempt, establish all possible native behavior, prove
resource/reload/load isolation or verify successful Claude tool effects. Production run remains
gated until its separate development-launch conditions are met. No timeout was widened or assertion
removed to obtain this result.

### Kiro 2.21.2 refresh

D59's fresh account/catalog, agent-directory and active MCP inclusion/exclusion prerequisites pass.
One native-effect attempt then passes in .cache/interop-observations/kiro-2212-native.7xUjn2,
exit 0, 22.50s test / 23.784s race-enabled package. The model prompt completes in 6,037ms with
832 assistant text bytes, 63 notifications / 9,463 bytes inspected, no canary or tool event, and
unchanged workspace/candidate/settings. All observed process/relay/private artifact cleanup passes.

One subsequent single-Read client-hook refusal passes with actual Claude 2.1.263 in
.cache/interop-observations/kiro-2212-denial.qyLSz4, exit 0, 24.03s test / 25.619s race package.
There are two accepted backend requests, one exposed Read, one exact matching denial and one
same-prompt completion. Claude exits 0; canary/source-setting and observed cleanup checks pass.
The 1,838-byte client result is checked in bounded memory and not retained as content.

These are two distinct opted-in live experiments, each run once without retry. They may consume
credits; no billed amount is measured. Their initial-session scope and limitations remain those
above. Real Kiro-generated successful client tool effects and remaining resource/skill isolation
still need verification before production policy admission.

## Actual Kiro single-tool effects and refusal controls

Prepared after D59's Kiro 2.21.2/v2 native-effect and actual-client Read refusal pass. The continuing
user authorization covers bounded implementation validation and readiness decisions. This distinct
credit-opted-in test admits at most six sequential experiments, each with its own temporary roots,
one initial ACP prompt and one matching tool-result continuation. It stops after the first failing
case and does not retry. The cases are allowed Read/Write/Bash, denied Write/Bash and a Bash hook veto.
Every effect target is created by the test under an owned temporary project; no existing project or
user file is submitted. Client permission rules and hooks remain authoritative.

The existing public-client/fake-ACP six-case rehearsal and original Read denial pass with the added
pre-handoff checks (25.389s race package). Before the sole tool handoff, neither effect file nor client
hook marker may already exist. The allowed Read's synthetic canary must remain unchanged. The exact
tool name and full argument object, one call, matching result ID/status, expected Read content or
hook-denial reason, same-prompt completion, client exit and owned cleanup retain their existing checks.
Wrong arguments or an extra call cancel before the client receives that call. Final model/client
output must not disclose the Read canary. Independent invalid-input/result and prior-effect controls
must pass before credit opt-in; the observer's synthetic control directory is explicitly private.

The system prompt expands only the selected public tool name:

```text
Independent single-operation client permission experiment. Request the listed <Read|Write|Bash> client tool exactly once using the supplied JSON argument object without adding fields. Do not use native tools, inspect configuration or other files, delegate, retry, or start another operation. Accept the client's success or refusal. Do not quote, summarize or disclose any file content or tool output. After the result, finish with only Done.
```

The user prompt is `Request <tool> once with exactly this argument object: <JSON>`. Read receives
only the absolute owned read-fixture path; Write receives the owned effect-fixture path and the fixed
text `owned client effect`; Bash receives one exactly quoted printf redirect to that same owned
target. Expected results, canary text and the fake peer's manifest are never included in a prompt.
The generated agent lists only this client tool's relay alias, no native tools/resources/hooks or
inherited MCP, with owned launch/configuration directories and default-resource suppression.

Each case retains the three-minute harness, 45-second ACP turn, 20-second setup/first-event and
60-second client bounds. Version/account checks now explicitly use five seconds and catalog checks
fifteen. All cases are sequential and own their cleanup. The suite may consume credits; neither one
provider call per ACP turn nor a fixed billed amount is asserted. It does not establish interactive
permission display, all inherited resource sources or reload/load behavior. Production run stays
gated until the remaining development conditions hold.

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
DAX_INTEROP_KIRO_BINARY=/absolute/path/to/kiro-cli \
DAX_INTEROP_CLAUDE_BINARY=/absolute/path/to/claude \
GOTOOLCHAIN=go1.27.1 \
GOMODCACHE="$PWD/.cache/gomod" \
GOCACHE="$PWD/.cache/gobuild" \
go test -race -p 1 -count=1 -timeout=20m -v \
  -run '^TestKiroLiveClientToolEffects$' ./internal/interop
```

The first live matrix passes allowed Read and Write, then fails allowed Bash before any exposed
client call; later cases are not run. The normalized record is live-client-effects.UFgQn0 (73.810s
race package). The recorded handoff group is absent because no matching call established it; that
false diagnostic alone does not prove a surviving group. Relay PID removal is observed. The initial
diagnostics do not identify the upstream failure conclusively.

An independent regression then reproduces rejection of identical string values with different JSON
escaping. The guard now compares decoded values after recursive duplicate/trailing-data validation,
preserves numeric precision, and keeps exact key sets and command strings. New diagnostics retain
only counts and fixed Boolean categories, including whether differences are encoding-only and
whether a handoff group was observed. A fresh bounded continuation selects only allow-bash,
deny-write, deny-bash and hook-bash after the updated guard and local Bash rehearsal pass. Earlier
Read/Write successes are retained. This is a new attempt after a verified observer fix, not an
automatic retry of the unchanged failed experiment.

That follow-up also fails at allow-bash, with a normal final completion but no exposed client call
or argument comparison. The private record is live-client-effects-followup.42eCGv (23.475s race
package). No target or client hook marker is created, and the observed relay PID is removed. The
encoding regression was real, but does not explain this attempt's lack of a tool request.

The relay metadata now explicitly associates each opaque alias with its original client tool name
and client execution authority. The complete source description and exact schema are preserved;
the registry fingerprint policy version changes to prevent reuse of earlier metadata contracts.
Independent metadata/identity regressions and the relay, MCP, registry, session, interop and launcher
race suites pass before another live attempt. First select only allow-bash with the same operation,
prompt, permissions and deadlines. A successful result permits refreshing the other five cases
under the new metadata; a failure requires diagnosis before another attempt. This association is
an interoperability correction, not proof of why the earlier model omitted the requested tool.

With that attribution, allow-bash reaches the relay with the exact command plus one description
field (live-bash-attribution.OJGk4l, 22.437s package). The exact-object observer rejects the annotation
before client handoff, so no tool effect occurs. A verified observer adjustment admits only Bash's
optional nonempty single-line description of at most 256 bytes. The command stays exact; all other
added fields, malformed/duplicate keys, and descriptions on other tools still reject. Diagnostics
retain only whether the annotation was accepted, not its content. The Bash system prompt adds:
`The optional Bash description may be one line of at most 256 bytes; the command must remain exact and no other fields may be added.`
One further allow-bash attempt follows the new positive/negative observer controls. Client permission
rules, hooks, operation target and time bounds remain unchanged.

The adjusted Bash case passes in live-bash-description.B3b2VO (24.57s test, 25.858s race package):
one call/result, two requests, one same-prompt completion, pre/post hooks, exact file content and
client exit 0. The other five cases then pass in live-effects-attribution.ukF0s2 (116.82s test,
118.095s race package). Read/Write effects succeed; denied Write/Bash and hook-vetoed Bash create no
target and no post-hook marker. Each refusal returns its matching error result and completes normally;
the hook case requires the exact refusal reason. All six pass source/canary and observed relay/group/
private-artifact cleanup checks. No content-bearing output is saved. These are real Kiro/Claude
rule/hook results; interactive display and remaining isolation paths are separate evidence.

## Initial skill inheritance observation

The next read-only matrix creates two new skill fixtures, one under the owned launch directory's
.kiro/skills and one under the owned KIRO_HOME/skills candidate global root. A fresh empty custom
agent first declares both skill:// files explicitly, then separate cases use default inheritance
enabled and suppressed. Each case has fresh roots and unique skill names. No skill, prompt or tool
is invoked. The existing pinned ACP setup, advertised tools query, context show and one-second
notification settling window bound inspection; original HOME serves authentication only.

The observer matches only the two owned names in public/private command advertisements for the
owned session. Assistant text or an unrelated command description cannot establish skill availability.
Only fixed location labels and Boolean matches are saved. Positive controls must establish both names
before a negative result can support exclusion. An inactive candidate root or absent ACP skill
advertisement leaves that observation incomplete, not verified. Every source byte and process cleanup
is checked. This experiment does not observe dynamic reload or load of an existing session.

The first explicit case reports two matched context items and a positive local context estimate,
but advertises neither skill name as an ACP command (skill-inheritance.QlUrg1, 6.473s). That
command-only observer therefore fails; no inheritance cases run. The follow-up uses the existing
strict verbose-context observer to match each owned absolute skill path and requires two matched
items with a positive estimate in positive controls. Suppression requires zero matched context items,
zero context estimate and no observed owned skill command. Command advertisement is supplemental;
its absence alone cannot establish exclusion. The fixtures, settings and no-model boundary remain.

The explicit context control then passes, while default inheritance reports the global skill
absolutely and one skill relatively (skill-context-inheritance.4ZE6Rg, 11.157s). The observer now
also recognizes the exact independently seeded .kiro/skills/<owned-name>/SKILL.md relative name under
a separate label, without resolving arbitrary returned relative paths. Other relative names still
fail the expected inventory. The next matrix keeps the same roots, settings and command budget.

That three-case matrix passes in skill-context-forms.EHIaIQ (16.44s test, 17.755s race package).
Explicit inclusion matches two absolute paths; inheritance matches the configuration-root skill
absolutely and the launch skill relatively; suppression has zero matched items and zero context
estimate. No owned skill command is advertised. All sources remain unchanged and process cleanup
passes. This establishes the two tested initial skill paths, with no model prompt or billed-usage claim.

## Prepared launcher configuration check

After D61, the actual tool harness uses the shared launcher configuration generator. It owns a fresh
KIRO_HOME and scratch directory beneath the caller's private runtime, suppresses default resources,
and prepares each exact relay agent in a separate launch directory. The six fixed environment
variables, v2 engine, original account HOME, client capabilities and tool authority remain those
tested above; the Kiro authentication classifier is also connected. Model turns and cleanup retain
the prior bounds. Independent launcher/interoperability suites pass before selecting only allow-bash
and hook-bash to verify this shared generator with actual clients. Production run remains gated while
that integration is checked. The other four D60 cases retain their identical agent/relay semantics.

Both cases pass in prepared-execution-effects.bruomf (46.78s test, 48.099s race package), with exact
tool/results, allowed file and pre/post hooks, blocked hook effect, client exit 0 and observed cleanup.
D62 then connects this generator to production startup and admits the measured development policy.
This does not extend the experiment to default full-tool client traffic or complete alpha/release gates.

## Default-client denial and joined session recreation

Prepared after D70's independent state tests and installed-client/fake-ACP registry replacement.
The continuing user authorization covers one bounded actual-Kiro attempt after its local rehearsal
and guard tests pass. A failure requires diagnosis before another attempt; there is no automatic
retry. Credit opt-in remains separate from every fixture-only invocation.

This experiment preserves Claude 2.1.263's default tool list, system instructions, thinking and
experimental declarations. Strict MCP configuration stays empty, and HOME/project/settings are
new owned fixtures. A local PreToolUse hook refuses Read. The user prompt is the existing
single-Read instruction and owned temporary path above, combined into the user message; no custom
system prompt or `--tools` restriction is passed. The synthetic canary is not included. No actual
project, repository source or user asset is sent to either model or client.

The guarded gateway permits exactly one Read with the exact owned `file_path` argument, followed
by one matching error result containing the owned hook refusal. Other tools, extra arguments,
another call, a successful result or a third request reject before dispatch. All advertised tools
still undergo the product's schema validation and map only to relay aliases in the shared restricted
Kiro launch policy. The guard's broader request declarations do not broaden tool effect admission.

At most two ACP processes and one recreation are allowed. Before preparing the replacement, require
the first observed process group/relay to be absent and its private launch/relay files removed.
The product validates the immutable complete history, incorporates the supplied denial in a fresh
prompt, and retains the original 45-second turn deadline. Setup/first-event bounds stay 20 seconds,
client execution one minute and whole harness three minutes; the replacement gets no new deadline.
The existing exact-version/account/catalog checks and auto model selection remain prerequisites.

Passing requires the expected default declarations, exactly one client hook denial, two admitted
backend requests, two joined prepared processes, a nonempty final client completion, unchanged
canary/settings and complete observed group/relay/profile/HTTP/pool cleanup. Raw prompts, client
instructions, outputs, credentials and unrestricted stderr are not retained. Only fixed classes,
counts and Boolean outcomes are logged. Full-history reconstruction may add provider work and loses
hidden Kiro context; neither billed-call count nor fixed credit usage is claimed.

The local rehearsal uses an independently authored fake ACP which checks the supplied full history
and denial before completing the replacement's only prompt. Select only that local control first.
The separately opted-in live invocation is:

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
DAX_INTEROP_KIRO_BINARY=/absolute/path/to/kiro-cli \
DAX_INTEROP_CLAUDE_BINARY=/absolute/path/to/claude \
GOTOOLCHAIN=go1.27.1 \
GOMODCACHE="$PWD/.cache/gomod" \
GOCACHE="$PWD/.cache/gobuild" \
go test -race -p 1 -count=1 -timeout=5m -v \
  -run '^TestKiroLiveDefaultClientDenialRecreation$' ./internal/interop
```

This experiment does not verify a live registry change, plugin turn, arbitrary default-tool operation
or the remaining alpha/release gates.

The local rehearsal passes in default-denial-rehearsal.gHGa9W (14.364s race package): the new
prepared-process control takes 3.26s, with 25 default tools, thinking/context declarations, one
exact Read/hook refusal, two backend requests and two ACP processes. The original system/history/
registry/identity remain exact while the trailing standing message changes. Old ownership is gone
before replacement; final text, canary/settings preservation and all observed cleanup pass. The
existing default-request and single-tool controls pass in the same run. Independent declaration,
operation and request-count guards pass in 1.285s after the final observation fields are added.
The guard test first failed to build against the prior test adapter's missing full-client policy.

The actual Kiro 2.21.2/v2 and Claude 2.1.263 attempt passes in live-default-denial.KGeiR1
(24.81s test, 26.304s race package), using the advertised auto model. It retains 25 tools and the
thinking/context declarations, exposes exactly one Read with the exact argument, and receives the
matching hook refusal. Two admitted requests use two ACP processes with old ownership removed
before replacement. The full prior history, identity, model, effort, top-level system, metadata and
registry stay exact; one trailing standing message changes. The fresh session returns nonempty final
text, and Claude exits zero. The canary remains unchanged/unobserved and settings plus all observed
process/group/relay/private-artifact cleanup pass. The 1,888-byte client output is inspected in bounded
memory only. Fixture-specific assistant text does not match the live model's text, as expected; exact
history is independently validated by the driver, not by that diagnostic field.

The first command submission never executed: automatic approval review timed out before process
creation. Its explicit one-retry allowance was used, and the second submission ran the sole live
attempt above. No model attempt was retried and no safety rejection was inferred from the review
timeout. This may consume credits; billed usage is not measured.

The pre-live opt-ins-off race regression passes ACP in 5.955s and interop in 25.058s
(default-denial-unit.q0Xkpf). Whole-repository/fixture vet, formatting and whitespace pass. No
application code or dependency changes in this experiment; the development binary remains D70's build.

Post-live review tightens only the test adapter: a new negative control finds that a matching result
plus a new question could enter the separate denial-interruption path. Default-client mode now
admits only one result block in that latest user message. The recorded live request already meets
this condition; it is not repeated. The final updated guards and installed-client/fake-ACP rehearsal
pass in default-denial-final.ZqVfcB (5.399s), with vet/formatting/whitespace passing afterward.

## Live MCP readiness and registry replacement

Prepared after D71. The continuing authorization covers one bounded allowance case followed, only
if it passes, by one independent hook-refusal case. Each requires the explicit credit opt-in and
the existing exact Kiro 2.21.2/v2 and Claude 2.1.263 account/catalog checks. Auto must be advertised;
no alternate model is selected. A failed case stops the sequence and requires diagnosis before any
new attempt. This plan is not evidence of a live result until the observations are recorded below.

The public client installs only a freshly authored, effect-free plugin from an owned local marketplace
into its new test HOME. No downloaded plugin, user asset, project/repository source or conversation is
supplied. Its independent MCP peer holds initialization until the first Messages request advertises
WaitForMcpServers with an input schema accepting `{}`; the handler then releases that owned peer.
The peer has a ten-second initialization bound and checks its exact own tool name and empty arguments
before recording a call and returning fixed synthetic text. User settings, global records and the
plugin source tree must remain unchanged when the prepared private client runs.

Both cases use this fixed user prompt, under the client's ordinary system/tool declarations:

```text
WaitForMcpServers {}, then mcp__plugin_dax-owned_owned__owned_probe {} once each. Reply only: independent plugin observation complete.
```

A test-only gateway guard allows exactly three main requests and two client tool calls, in that
order, with empty argument objects. The first registry must advertise the wait and omit the plugin;
the next registry must advertise the plugin. Each continuation carries exactly one matching result
and no new user question. Wait must succeed. The plugin must return the exact owned success text or,
in the refusal case, a matching client-hook error containing the owned refusal reason. Unexpected
tools/arguments, duplicate/foreign results, wrong status, early completion and additional requests
reject before dispatch or client exposure. Title requests are synthetic and local; the entire HTTP
Messages budget is four. All new declarations still pass the product validator.

The shared restricted Kiro launch generator permits at most two sequential ACP processes and one
recreation per case, with one original 45-second turn deadline, 20-second setup/first-event limits
and a 60-second client terminal lifetime. Each whole harness is bounded to three minutes. Before
preparing the replacement, the first observed relay/group and its policy/relay files must be gone.
The second process receives the expanded validated registry and full prior history. The plugin result
must complete within that replacement; a third process is forbidden. Both observed groups and all
prepared artifacts must be absent after closure. Each relay still only requests client execution.

The independent guard tests cover wrong operations/arguments, undelivered batches, result IDs/status,
extra user content, missing completion and excess requests. The actual-client/fake-ACP rehearsal uses
the same fixed prompt and terminal/turn limits. A separate UI check must distinguish the echoed
prompt's requested answer from later output: the answer must appear after the full echoed prompt.
Seeing the answer only inside the user input cannot establish visible completion.

Passing requires two exposed calls, two exact results, three main requests, two joined backend
processes, a nonempty final completion rendered after the prompt, one native plugin call for allowance
and zero for refusal, unchanged sources and joined client/MCP/backend cleanup. Only counters, sizes,
fixed labels and Boolean comparisons are retained. Prompts, full output, credentials and unrestricted
stderr are not saved. Two ACP prompts do not imply two billed model calls, and no credit maximum is
claimed. General dynamic configuration, other plugins and remaining alpha/release gates stay separate.

After the independent controls and fake rehearsal pass, invoke once:

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
DAX_INTEROP_KIRO_BINARY=/absolute/path/to/kiro-cli \
DAX_INTEROP_CLAUDE_BINARY=/absolute/path/to/claude \
GOTOOLCHAIN=go1.27.1 \
GOMODCACHE="$PWD/.cache/gomod" \
GOCACHE="$PWD/.cache/gobuild" \
go test -race -p 1 -count=1 -timeout=8m -v \
  -run '^TestKiroLivePluginRegistryReplacement$' ./internal/interop
```

The initial guard controls fail to build against the absent adapter; the prompt-echo control then
fails against the original substring-only UI check (0.779s). With the guard and output-after-input
check, independent controls pass in 1.671s. plugin-sequence-rehearsal.naaLY5 passes the original and
new-prompt allowed/refused fake-ACP pairs plus guards in 26.377s under race. Each pair has three main
requests, two exact calls/results, a final rendered completion, correct one/zero native call counts,
unchanged sources and joined processes. The new-prompt pair takes 11.73s and uses the live terminal/
turn bounds without Kiro inference. The opt-ins-off interop race suite passes in 21.365s
(plugin-sequence-unit.ODlARh); whole-repository/fixture vet, formatting and whitespace pass.
The installed-client regression plugin-sequence-clients.ANfZdp then passes all eight selected MCP,
plugin source/tool/hook/Git and default-denial controls in 58.469s under race. No Kiro prompt runs
in either rehearsal or regression.

The first live allowance reaches the wait result, changed registry, one native plugin call and
completed backend response, but fails the final-screen assertion in live-plugin-registry.BJbgWo
(70.11s test, 70.507s race package). Four Messages requests include the synthetic title; the final
main request has eight messages and one latest result. Source preservation and both prepared
process/group/artifact cleanup pass. The refusal case is not run. The log lacks separate expected-
answer/prompt-presence diagnostics, so it does not establish whether wording or current-screen
reconstruction caused the visibility failure. Overall live acceptance remains failed.

A public-only local Claude consultation, public-terminal-evidence-review-_c51vjg0, completes in
one turn with 6,182 stdout bytes, and its saved answer is fully read and assessed. Useful advice is
to separate generated/rendered markers and avoid requiring old prompt/answer co-presence. Stripping
terminal controls, assuming markers cannot appear in tool text, and inferring wording from any
assistant text are rejected. This advice is not live evidence or a diagnosis of the failed attempt.

The follow-up replaces the visibility instrument while keeping the exact two tool operations,
permissions, result validation, process limits and deadlines. Each case generates a fresh 26-byte
ASCII marker, keeps the whole value in memory, and puts only its two separate halves in the prompt:

```text
WaitForMcpServers {}, then mcp__plugin_dax-owned_owned__owned_probe {} once each. End with X+Y (no separator), X=<first-half>, Y=<second-half>.
```

The expanded prompt is 144 bytes and contains no complete marker. The guard rejects a complete
marker in initial user content, a wait/tool result or earlier model output. It requires the marker
in the final model response before accepting end_turn. The screen observer separately requires the
current reconstructed screen to contain the marker after protocol completion. The old prompt need
not remain visible. Hidden controls and erased text cannot count; stripped/raw substring presence
is diagnostic only. Generated-marker, protocol-completion, current prompt/answer and raw-answer flags
are logged independently. Guard diagnostics now survive a failed UI case through deferred reporting.

The fake receives an owned, bounded marker file, validates it and emits it only after the exact
plugin result. No such file is written for actual Kiro. Independent controls reject prompt echo,
premature model/tool markers, missing final wording, hidden and erased text; a visible distinct answer
still passes when the old prompt is absent. These controls establish the revised observation rule,
not the exact cause of the first live failure. They pass in 1.809s. The original and derived-marker
installed-client/fake-ACP pairs pass in plugin-marker-rehearsal.Pgkq4g (26.264s), with correct effects,
sources and cleanup. Both new-marker cases report generated and current-screen marker matches;
the older constant-answer controls also pass. After regression checks, the same live entry point may
run one fresh allowance and, if successful, one refusal under these revised inputs; it stops on failure.

The final preflight race regression passes ACP in 5.938s and opt-ins-off interop in 22.449s
(plugin-marker-unit.YJoqb6). Three installed-client UI/plugin-hook/Git preservation controls pass in
17.655s (plugin-marker-ui-regression.CFVGcB), without Kiro inference. Marker controls additionally
cover a marker split across text events, accepting it only in the final response; this guard-only
run passes in 1.322s. Formatting, whole-repository/fixture vet and whitespace checks pass. These
results permit the one planned follow-up sequence; they do not replace the failed live observation.

That follow-up allowance fails before any exposed tool in live-plugin-marker.MNXcAE (69.20s test,
69.463s race package). It admits one main request plus a synthetic title; the owned MCP peer
initializes/lists, but neither wait nor plugin is exposed and the plugin call count is zero.
The guard reports Failed with one request and no uses/results/completion/marker. The existing
diagnostics do not distinguish a backend start failure from a rejected first response. The client
group is gone and the one prepared launch is cleaned. The former aggregate process assertion also
requires two launches/groups, so its false value cannot be interpreted as an observed survivor.
The refusal case does not run. This is another failed observation, not a visibility-only result.

The next diagnostic revision adds only fixed validation/error categories, event/text-byte counts,
and separate actual cleanup versus expected process-count observations. It never saves arbitrary
error strings, tool names/arguments or response text. Before shutdown it can record the group of
an owned relay even when no tool has been exposed. A recorded surviving relay PID receives bounded
last-resort cleanup while preserving the failure result; unrelated groups are never signaled.
Success still requires two distinct observed groups, both prepared policies retired and the exact
three-request/two-tool sequence. Unit controls distinguish backend-start, backend-read, unexpected
tool/arguments, early end and missing end, and verify that private error text is reduced to fixed
categories. Retryable read deadlines are recorded without independently canceling a turn.

After these controls and the same installed-client/fake-ACP allowance/refusal rehearsal pass,
one diagnostic live sequence may use the unchanged derived-marker recipe and existing budgets.
This is an instrumented follow-up to distinguish the previously conflated failure sites, not an
automatic retry of an unchanged observation. Allowance failure again stops the refusal case.

The diagnostic prerequisites pass in plugin-categories-rehearsal.HoeDq3 (14.826s race package).
Both installed-client/fake-ACP cases complete with three requests, two tool calls/results, six
events, the final generated/displayed marker, no failure category, native effect counts one/zero,
unchanged sources and joined groups. All independent guard/category/screen controls pass in that
run. The category tests initially failed to build before the new fields/classifier were implemented.
Applicable vet, formatting and whitespace checks pass before the diagnostic live follow-up.

The diagnostic allowance fails in live-plugin-categories.Pr0kj8 (68.49s test, 68.768s race package).
It receives 129 text events/985 pre-tool text bytes and rejects an early complete marker before
any exposed tool. There is no recorded backend-start/read error. The owned plugin is never called;
the refusal case does not run. One prepared policy is cleaned, its recorded relay is gone, the pool
is empty and artifacts are absent. No live group was observed before its earlier retirement, so
the two-process sequence remains unestablished. The separate cleanup checks pass without pretending
the missing second launch is a surviving process. This identifies the diagnostic attempt's failure;
it does not retroactively establish the earlier uninstrumented attempts' exact causes.

The next revision removes the opportunity to compute the final marker from the first prompt. The
26-byte marker still has two halves. Only X appears in the 141-byte prompt; Y is supplied through
the owned plugin's result (or its matching hook-refusal reason), after the second tool request:

```text
WaitForMcpServers {}, then mcp__plugin_dax-owned_owned__owned_probe {} once each. After both, reply X+Y; X=<first-half>, Y is in the result.
```

The effect-free peer adds `; Y=<second-half>` to its exact synthetic success text. Its optional
argument accepts exactly thirteen base32 characters and does not change tool names, schemas or
effects. The owned refusal hook adds the same suffix to its fixed denial reason. The initial
request and wait result must not expose Y; the final result must contain the exact expected suffix.
The guard still rejects a whole marker in early model output or any tool result and requires it
in the final response. The screen must independently show the complete marker after protocol
completion. All operation/result/status/process/time limits remain unchanged. Only the owned
synthetic tool/refusal text and user prompt change; no new execution authority is granted.

Independent controls first fail to build without the new result-challenge field, then pass in
1.714s under race. They cover success and refusal, early disclosure in input/wait results and a
missing challenge in the final result. After the installed-client/fake-ACP controls and applicable
checks pass, this recipe permits one allowance and, only if it passes, one refusal. A failure ends
this live sequence for diagnosis; it is not a repeat-until-pass test. The earlier failed attempts
remain recorded regardless of this recipe's result.

The final result-challenge rehearsal passes in plugin-result-challenge.hHFPYQ (25.368s race package):
the original fixed-result pair and revised result-challenge pair both pass, including allowance,
hook refusal, exact result/marker checks, native calls one/zero, source preservation and cleanup.
All guard/category/visible-screen controls pass in the same run. Whole-repository/fixture vet,
formatting and whitespace checks pass before the result-challenge live sequence.

The result-challenge allowance reaches both exact tool/result pairs, the changed registry and one
native plugin call in live-plugin-result-challenge.8UPd0k, but fails overall (68.18s test, 68.463s
race package). Three main requests retain the full prior prefix, owner, model, effort, top-level
system and metadata; the last request has eight messages and one latest result. The final model
text has 33 bytes and lacks the required complete marker. The guard rejects its end condition,
so protocol completion and final display acceptance cannot pass. The text is not persisted; its
exact formatting or reason for differing from the requested concatenation is not established.

Two prepared launches/cleanups, two recorded relays and two distinct observed groups are
established. All observed relays/groups/artifacts are gone, close results succeed and the pool is
empty. Client and native MCP cleanup and source preservation checks pass. The refusal case is not
run. This supports the actual registry/tool/result/cleanup path but does not pass the full live
experiment. No further model attempt runs in D72; the D119 section below later verifies skill
content and plugin hooks against the actual backend. Future work must address final-answer evidence
explicitly without treating a partial exchange as acceptance.

Final opt-ins-off race tests pass ACP in 5.439s and interop in 20.887s
(plugin-result-final-unit.R8Vjmv). Whole-repository/fixture vet, formatting and whitespace pass.
Only tests and decision/evidence documents change; the existing development executable is current.

## Launcher cancellation after a delivered tool handoff

D74 prepares one actual Kiro 2.21.2/v2 / Claude 2.1.263 cancellation observation after its independent
guard and installed-client/fake-ACP rehearsal pass. The continuing user authorization covers this
bounded alpha check. It retains a separate credit opt-in and has no automatic retry.

Use the existing exact single-Read system/user prompt above, substituting only a newly generated
temporary file path. Its random synthetic canary stays out of the prompt. The real client receives
new owned HOME/project/settings, an empty strict MCP configuration, only Read and a held PreToolUse
hook. The hook ignores its input, records only its PID/group, waits at most thirty seconds and exits
with refusal status on SIGINT, SIGTERM or expiry. It reads no file and spawns no descendant. No user
assets, repository content or earlier implementation enter either public client.

The existing exact-operation and canary guards run behind an additional one-main-request budget. Any
further model request rejects before driver dispatch. One ACP process, one relay, four gateway
connections and bounded schema workers are admitted; no replacement is possible. Kiro preparation
uses the shared verified execution policy, after version-admission/account/catalog preflight (exact
at the time; same-major since D114), with the advertised auto model and no fallback. Prepared
resources and client settings are private and owned.

The actual application RunClient function owns gateway, backend, pool, validator, client and profile
shutdown. A test-only exec wrapper supplies the fixed print arguments and documented experimental/
thinking omission options to the unmodified public client. It does not substitute its implementation.
The observer requires a successfully delivered HTTP tool handoff, WaitingTools state, a still-live
client/hook and the observed ACP/relay group. It then cancels the launcher context eight times.
This tests the suspended backend when no new result request exists, not terminal Ctrl+C or HTTP
disconnect during streaming. The wrapper/runtime-stage invocation does not certify the compiled
foreground command composition; that remains separate evidence.

The entire harness has three minutes; the client one minute; the original ACP turn 45 seconds;
setup and first event twenty seconds. The hook has thirty seconds plus a client hook timeout of
35 seconds, all of which outlast the immediate cancellation trigger. A post-cancel observer allows
eight seconds for the existing bounded owners. Passing requires a context-cancellation result,
one backend Close, closed driver and empty pool, exactly one prepared/cleaned launch, client/hook/
relay/groups absent, profile/policy artifacts removed and source settings/canary unchanged. No tool
result, final completion, new request or canary disclosure is accepted. A cleanup failure remains a
failure even if directly observed owned PIDs need last-resort cleanup.

Output goes to the null device. Only fixed classes, counts, booleans, timing and owned process
identities are observed; no prompts, tool results, credentials or unrestricted stderr are retained.
One ACP prompt is not one billed model call; cancellation may consume credits. The separate local
control uses the same client/runtime/relay and an independently authored fake ACP, without Kiro.

After the local control passes, select only:

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
DAX_INTEROP_KIRO_BINARY=/absolute/path/to/kiro-cli \
DAX_INTEROP_CLAUDE_BINARY=/absolute/path/to/claude \
GOTOOLCHAIN=go1.27.1 \
GOMODCACHE="$PWD/.cache/gomod" \
GOCACHE="$PWD/.cache/gobuild" \
go test -race -p 1 -count=1 -timeout=4m -v \
  -run '^TestKiroLiveLauncherCancellation$' ./internal/interop
```

Local rehearsals and their first harness-only failure are recorded in D74. The live result below
is separate from preparation. Other cancellation phases, process loss, auth expiry, model changes,
restart/resume and full plugin/Skill acceptance remain open.

The actual attempt passes in live-launcher-cancellation.tlWaG6 (17.13s test, 18.514s race package).
Its one exact Read and delivered HTTP handoff reach a live held hook and WaitingTools state.
Eight launcher cancellations join in 1,061ms with one backend Close, closed driver, empty pool and
one prepared/cleaned ACP process. The client/hook/relay and observed groups are absent, policy/profile
artifacts removed and source settings/canary unchanged. Zero results, completions, replacements or
further requests occur; no canary is observed in model output. No second actual attempt runs.
This closes only the described suspended-runtime observation, not foreground Ctrl+C, streamed HTTP
disconnect, process-loss recovery or the remaining alpha/release gates. Application behavior and
development run admission are unchanged; the development executable remains the D73 build.

## Process loss before tool delivery and a fresh request

D75 prepares one actual Kiro 2.21.2/v2 / Claude 2.1.263 sequence after its independent guards and
installed-client/fake-ACP rehearsal pass. The continuing user authorization covers this finite alpha
check. A separate credit opt-in is mandatory and no failed actual attempt is automatically repeated.

The first user input is the existing single-Read instruction with a newly created temporary path.
The random synthetic canary in that file is not supplied to the model. Both client invocations use
this fixed system text:

```text
Independent process recovery exercise. Request the listed Read client tool only when the user asks for Read, exactly once with the specified file_path and no other arguments. Otherwise finish with a brief text acknowledgement. Do not use native tools, inspect other files or configuration, delegate, retry, or disclose file content.
```

The second independently supplied user input is:

```text
Do not request any tool. Reply with a brief acknowledgement of this new independent request.
```

Both invocations use a fresh owned HOME/project/settings/profile, an empty strict MCP configuration,
only Read, disabled session persistence and one generated explicit conversation UUID. The second
process receives only its new user text and native standing context, not a saved conversation. The
owned Read hook would deny and record any accidental client invocation; its marker must stay absent.
No repository, actual user asset or previous implementation is supplied. The installed client itself
is unmodified. Experimental/thinking omissions stay scoped to the finite test commands.

The shared restricted Kiro execution preparation follows version-admission/account/catalog preflight
(exact at the time; same-major since D114) and uses the advertised auto model without fallback. The
test allows one active ACP process and two sequential prepared launches, one exact intercepted Read
in the first phase, two total main requests, four HTTP connections and bounded validators/relay
owners. At the validated Read event, before tool delivery, record the live owned relay group and
SIGKILL only that group. Within five seconds require the group/relay gone, policy artifacts removed,
cleanup complete, an empty pool and Unstarted driver. Then return the real turn's observed error.
Transport/closed/internal-cancellation classes are accepted only with the caller context still
active and the termination/cleanup proof; success, EOF, deadlines and caller cancellation are
negative controls. The first client must exit with a JSON error result.

Only that complete first-phase proof authorizes the second test request. The new process must follow
joined old cleanup. It may return text and one delivered end_turn, with no tool or additional request.
The original requests are passed unchanged. Identity, model, effort, tools, metadata and literal user
policy stay exact. The pinned client changes a short hexadecimal fragment in its first billing-header
system block between print invocations; D75's bounded observation predicate distinguishes that from
changed policy without removing or rewriting the block. Full wire-system equality is not claimed.

The whole harness has three minutes, each client invocation one minute, each new independent turn
45 seconds and setup/first event twenty seconds. The five-second termination observation does not
extend the active request deadline. A maximum of two ACP prompts does not establish two billed calls
or a fixed credit bound. If the first phase fails, the second is not sent. There is no automatic retry.

Passing also requires no hook activity or canary disclosure, unchanged source settings/canary, both
observed client and ACP/relay groups gone, all policy/profile artifacts removed and clean HTTP/pool/
driver/validator shutdown. Record only fixed categories, counts, sizes and booleans; client output
is inspected in bounded memory and never retained. No unowned process table or PID is searched.

After the local controls pass, select only:

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
DAX_INTEROP_KIRO_BINARY=/absolute/path/to/kiro-cli \
DAX_INTEROP_CLAUDE_BINARY=/absolute/path/to/claude \
GOTOOLCHAIN=go1.27.1 \
GOMODCACHE="$PWD/.cache/gomod" \
GOCACHE="$PWD/.cache/gobuild" \
go test -race -p 1 -count=1 -timeout=4m -v \
  -run '^TestKiroLiveProcessLossAndFreshRequest$' ./internal/interop
```

This sequence does not establish interactive continuation in one client process, late results after
a delivered tool call, simultaneous sibling-session failure, terminal Ctrl+C, authentication expiry
or restart/resume. Those remain distinct from this process-loss/fresh-request observation.

The first opted-in invocation, live-process-loss.8PHpmH, stops at login preflight (3.946s race
package); it creates no ACP process and sends no model request. The installed pair remains 2.21.2.
A fresh-state compiled doctor also stops at login verification, and the main/helper read-only
diagnostic process-loss-account-diagnostic.9j2vIn records exit 1/account:null for both (4.816s).
At that point the account had no verified login. The user was asked to complete public CLI login;
no credentials or authentication state are modified by this work. No actual sequence is retried.
The full live process-loss gate remains open. The fake-ACP rehearsal and applicable core race
regression pass, as recorded in D75, without external model inference.

D80 later rechecks both pinned entry points: whoami now exits 0, and the product identity/scope
preflight passes with its bounded-postamble handling. No account value is logged and no login
command or state mutation is performed. The absent-login condition is resolved, so the bounded
sequence above may proceed after its applicable local controls. This read-only check alone does
not close the live process-loss gate.

D81 reruns the independent guards and installed-Claude/fake-ACP rehearsal successfully (7.609s
race package), then executes the actual sequence once with restored login. It passes in 21.51s
(22.801s race package): the first client exits 1 with a JSON error after one intercepted Read and
observed ACP death, while its caller context is still active. Joined old cleanup precedes the
second request, which exits 0 with nonempty text and one delivered completion. There are exactly
two main requests, two prepared/cleaned backend processes and no hook invocation or source change.
Both client/ACP groups, relays, tracked policy artifacts and the temporary profile are gone.
The bounded raw billing-header difference is retained unchanged, as in the rehearsal. Fixed
diagnostics are saved to `.cache/memory-review/live-process-loss-after-login.log`; no model text
or raw stderr is retained. No failed trial is automatically retried. This completes only the
scenario specified here, with the same-client/late-result/sibling-failure and other limits above.

## Typed cancellation of a streamed response through the compiled run command

The continuing authorization covers a finite actual-Kiro trial after independent protocol guards
and all three actual-Claude/fake-ACP controls pass. Public
[keyboard documentation](https://code.claude.com/docs/en/interactive-mode) distinguishes Ctrl+C
cancellation from Ctrl+D exit. Do not substitute a direct process signal or parent context cancel
for either typed action. A quiet UI, an ended HTTP response, or leader death alone is insufficient.

The new test compiles the ordinary command and a separate independent observer into an owned
temporary directory. The system terminal allocator starts an observer supervisor in its own session.
The supervisor captures terminal settings/foreground group, launches the compiled `run` with inherited
terminal descriptors, waits for it, and checks restoration itself without performing restoration.
The product then owns its ordinary foreground transfer to Claude. All HOME/project/settings/state/
runtime directories are disposable fixtures. An exec wrapper records Claude's PID/PGID, foreground
group, private profile and loopback address, then executes the unmodified client. Version queries
forward unchanged. No model credential or unbounded terminal output is logged.

The test scopes Claude to an empty native tool list and strict empty MCP list, a short independent
text-only system instruction, disabled thinking/experimental betas and disabled transcript history.
It marks only the owned private profile's empty project trusted, while preserving original source
settings/global JSON. This is controlled actual-client keyboard coverage, not arbitrary user-asset
or native-tool coverage. The only submitted user instruction is:

```text
Begin with the concatenation of Ready and _47 without spaces. Then list integers 1 through 2000, one per line, without tools or any other text.
```

An ACP observer forwards complete newline-delimited frames unchanged and records fixed prompt,
text-update, cancellation and correlated-result facts. Native Kiro is an unmodified child in the
observer's ACP group; only its HOME is redirected to the existing account HOME for authentication.
The product's owned KIRO_HOME, restricted agent, argv and launch directory remain in effect. Its
ordinary real version/account/catalog preflight runs before inference. No identity is synthesized
in the live path. The fake path has independently authored version/account/catalog and ACP replies.

The public client also requests a session title; the specification deliberately gives it a separate
Kiro session. Allow at most one main prompt and one title prompt across all observer processes,
using exclusive owned admission markers before forwarding. The fixture distinguishes its two known
purposes using the title word; that coarse observation is not a replacement production classifier.
The input contains no title word. Record purpose hints, retain separate per-process event attribution,
and enforce a maximum of two prompts even if the proxy replaces a process. Do not equate this with
a two-call provider billing guarantee. Title work cannot satisfy main-stream readiness or cancellation.

Before typing a single Ctrl+C byte, require one active main prompt, at least two main text updates,
no end/cancel/guard failure and the generated marker visible in the client UI. The complete marker
is absent from the typed instruction, preventing prompt echo from satisfying readiness. Then require
a forwarded main session/cancel and the main ACP group gone while Claude and the compiled proxy
remain alive, within eight seconds. This matches the product's retirement semantics; Kiro need not
remain alive after cancellation. It does not prove remote provider execution or billing has stopped.

Exit with Ctrl+D, sending a second Ctrl+D only after the current client screen explicitly asks for
that same key again to exit. Require the compiled command to exit 0, the supervisor's terminal
restoration check, every recorded owned PID/group gone, closed loopback listener, removed runtime/
profile and unchanged sources. Record all observed groups, including the separate title process.
The passive observer never repairs terminal state on the success path. Failure-only emergency
cleanup does not count as passing. Unobserved historical/detached descendants remain outside this
particular observation; no complete process-tree census is claimed.

The no-byte and ordinary-character controls use only fake ACP. Both must reach a normal main
completion without a cancellation; the ordinary character must not stop text delivery. They then
clear the unsubmitted character and take the same keyboard exit path. The fake cancellation case
must stop before main completion, retire the main ACP group and leave the client usable until exit.
Protocol tests separately check correlated responses, exact byte forwarding, process-replacement
admission refusal and frame budgets; readiness tests reject title-only/finished/already-cancelled
or failed observations, and Ctrl+C confirmation text cannot authorize another Ctrl+D.

Bounds: two minutes for the test including builds, seventy seconds for the terminal invocation,
ninety seconds for an observer fail-safe (never a success condition), 256 KiB terminal capture kept
only in memory, 256 KiB per ACP frame, 1,024 frames/8 MiB per observer, 64 KiB per event file,
32 event files and 512 KiB aggregate receipts. Each input write has the existing 200ms deadline.
A guard failure aborts the test; a failed actual trial is not automatically retried. Select only
TestKiroLiveCompiledRunKeyboardCancellation with both pinned executable variables and explicit
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 after the fake controls pass.

This does not establish keyboard exit during a held tool hook, a new question after interruption,
all descendant lifetimes, terminal reconnect, auth expiry or sibling-session cancellation. Those
remain distinct acceptance work. Initial fake harness attempts incorrectly limited the whole client
to one ACP prompt; observations identified legitimate separate title work, so the fixture was corrected
without changing production behavior. The first complete three-control race run passes in 23.041s.

D82 executes the actual pinned pair once and passes in 18.34s (19.651s race package). At the typed
Ctrl+C, the main response has delivered 24 text updates and its generated marker is visible in
Claude. One main session/cancel is forwarded and one correlated cancelled reply returns; no main
completion is observed. The main ACP group is gone while Claude and the proxy remain alive within
the required interval. Confirmed Ctrl+D exit returns 0, with terminal restoration and all four
recorded groups/seven recorded PIDs, listener, runtime and profile gone. Source settings/global JSON
are unchanged. One separate title prompt completes; exactly two prompt admissions occur, with no
guard failure or emergency cleanup. Fixed facts are saved to `.cache/terminal-review/live-keyboard.log`.
No failed live trial is retried. This establishes only the controlled streaming/keyboard scope above.

## Keyboard exit while the client's Read hook is held

Extend D82's independent compiled-command terminal observer with a native command hook and one
owned Read. The public [hook contract](https://code.claude.com/docs/en/hooks) supplies event, tool
name/input/ID and permission-decision fields; no upstream fixture or client source is used. The
separate local public-only terminal consultation already considered the held-hook case. Keep its
typed-action and passive-restoration advice, and retain the previously recorded rejected suggestions.

The owned client settings register PreToolUse and PostToolUse for Read; the wrapper exposes only
Read and strict empty MCP. The pre-hook accepts only one exact file_path under the owned project,
a nonempty bounded tool_use_id and the expected event/tool. It consumes a single exclusive admission
marker containing only an ID digest. The post-hook must match that digest. Neither hook opens the
requested file, transcript or credentials. Only event/name/input/ID are inspected; remaining native
payload fields are discarded without logging. Input is bounded at 64 KiB/two seconds;
the pre-hook waits at most twenty seconds and exits with denial on interrupt. Expiry is a guard
failure, never a passing keyboard result. The hook command timeout is twenty-five seconds.

First run two actual-Claude/fake-ACP cases. The fake reads only the launcher-generated agent's public
MCP declaration and launches the product relay; it does not read the relay's private configuration.
Initialize, list and request the single discovered Read alias through public MCP. Keep this relay
connection alive for the ACP process lifetime, rather than closing it after one tool result.
In the release control, observe the exact held hook and pending relay call for at least 500ms with
client/proxy/hook alive, release only that hook, and require a matching PostToolUse, the expected
owned Read result at the relay, main completion and generated marker visible in the client. Exit
through the same confirmed Ctrl+D path. This establishes that the held operation could continue.

For the keyboard-exit case, wait for the same unreleased live hook for 500ms, with one main prompt,
no main completion/cancellation and no guard failure. Type Ctrl+D and type it again only when the
current native screen explicitly asks for Ctrl+D again. Require ordinary command exit 0 within
eight seconds of the first exit key, terminal restoration, every recorded PID/group gone, closed
listener, removed runtime/profile and unchanged source settings/Read fixture. No hook release,
PostToolUse or successful relay result may occur. No file marker may appear in captured terminal
output. After recorded cleanup succeeds, create the former release marker and observe for 300ms;
no late release/completion or new prompt may appear. This is a bounded late-trigger observation.

Only after both fake cases pass may the continuing authorization be used for one actual Kiro
trial. Its ordinary account/catalog/version preflight and ACP remain unchanged. The actual path
requires the exact held native hook but does not instrument or replace Kiro's private MCP client;
fake relay call/result counts are not claimed for it. Preserve D82's maximum of one main and one
separate title prompt across replacement processes and all existing terminal/frame/event bounds.
There is no automatic failed-live retry, synthetic tool result, direct signal used as keyboard
evidence or success obtained by parent cancellation/emergency cleanup. Unobserved historical or
detached descendants, a following question, arbitrary hooks and all other live/release gates remain
separate. The fixture supplies one owned file with a fixed marker and requests its Read exactly once.

The first release control detects premature fake-relay connection closure after a successful tool
result, which correctly cancels the product's active session. It fails and uses emergency cleanup;
this is not keyboard-exit evidence. Keeping the fake connection alive for its owning process fixes
the fixture. Both complete controls then pass together in 10.251s under race detection: release
produces one matched PostToolUse/result and main completion; Ctrl+D exit takes 278ms from its first
key, with the hook still alive at the second-key confirmation, one hook interruption, no Read result
or main completion, restored terminal and all recorded processes/artifacts gone. The late release
marker has no observed effect. Select TestKiroLiveCompiledRunHeldHookKeyboardExit for the one actual
trial with DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 and both pinned executable paths.

D83 runs that actual trial once and passes in 22.27s (23.553s race package). The exact hook is
held before the first Ctrl+D and remains alive at the second-key confirmation. Keyboard shutdown
takes 2,392ms, with one hook interruption, one main ACP cancel/cancelled reply and no main completion
or PostToolUse. All five recorded groups/eight recorded PIDs, listener, runtime and profile are gone;
terminal settings are restored and source settings/global JSON/Read fixture are unchanged. The late
release marker has no observed effect. Exactly one main and one completed title prompt run, with
no guard failure, emergency cleanup or retry. Native Kiro MCP call/result counts are unobserved and
reported as such. Fixed results are retained in `.cache/terminal-review/live-held-hook-exit.log`.

## A new question in the same foreground client after streamed Ctrl+C

Extend D82's text-only compiled-command test. Keep the ordinary proxy, owned native client/profile,
empty tool/MCP lists, passive terminal supervisor and real Kiro preflight/ACP forwarding. The first
counting prompt and readiness checks are unchanged. After one typed Ctrl+C, require the main ACP
cancel, its group absent and the same recorded Claude/proxy PIDs, profile and gateway address still
live within the existing eight-second interruption observation. Only then write an owned admission
marker and type this second question, submitting Enter only after its complete text is visible:

```text
Stop the counting task. Reply with the concatenation of Follow and _49 without spaces, and no other text.
```

The observer distinguishes the second main prompt by its new independently chosen instruction
fragment. One initial main and one follow-up main are admitted exclusively across replacement
processes before forwarding. The follow-up requires the parent's admission marker. Allow at most
two auxiliary title prompts total; a second title also requires that marker. A title process may
legitimately serve two sequential prompts, so the scoped frame guard permits two only in this test,
rejecting overlap, a third prompt or an uncorrelated old reply as a new completion. Other keyboard
tests retain their one-prompt-per-process and one-main/one-title limits. Maximum actual inference
admissions are two main and two title prompts; this is not a provider billing-call guarantee.

Before typing the second question, retain the set of every already observed group. The second main
must arrive in a different group from the interrupted main and from that entire set, preventing an
existing auxiliary survivor from satisfying fresh-process proof. Require one observed client exec,
unchanged live client/proxy/profile/address, one correlated follow-up completion, no follow-up
cancellation and the generated marker visible in the current native screen. The complete generated
marker is absent from both typed prompts. Then use the proven confirmed Ctrl+D exit and require all
recorded PIDs/groups, listener and private artifacts gone, terminal restored and sources unchanged.

Record only Boolean presence of the original user fragment, interrupted partial-response marker,
new instruction and an unrelated marker that is never submitted. This small-history scenario
requires old/new input preservation; partial-response retention is observed without synthesizing or
rewriting client history or treating it as a general public-protocol guarantee. The unrelated marker
must remain absent. Independent controls cover marker detection, pre-intent refusal, repeated/old-
request refusal, bounded second title, overlapping requests, stale correlated IDs, changed client,
reused main group, missing history/instruction, title-only completion and repeated follow-up.

The initial actual-Claude/fake-ACP control passes in 6.242s under race detection with one initial
main, one title and one follow-up. The old input, visible partial marker and new input are all present
in the unchanged ACP projection. The generated follow-up answer is visible in the same client, with
a fresh ACP process and successful keyboard cleanup. After adding the detector's absent-marker
control and explicit exclusion of every previously observed group, repeat that local control before
one actual-Kiro trial. Keep the existing terminal/frame/event/time budgets; do not automatically
retry a failed live trial or replace keyboard actions with process signals or parent cancellation.

The public-only local Claude consultation is saved/read/assessed after the user explicitly approves
its prepared payload, resolving an initial automatic approval-review refusal. New-input causality,
correlated visible completion and an absent-marker control are useful. A fresh group cannot be
required to have received the old cancellation; legitimate auxiliary concurrency also does not
prove cancellation failure. Recorded address/PIDs do not constitute a socket-inode or start-time
census. Tool-result continuations, authentication expiry, model changes, reconnect/resume and
unobserved historical/detached descendants remain separate acceptance work.

D84's final fake control passes in 6.285s under race detection with the added absent-marker and
previous-group exclusions. The actual pinned pair then runs once and passes in 22.94s (24.864s
race package). Ctrl+C follows sixteen initial text updates; one cancel and one cancelled reply
retire the old main group before the second question. One observed Claude exec, unchanged live
client/proxy/profile/address and a previously unobserved follow-up ACP group are verified. The
follow-up has one text update and one correlated end, no cancellation, preserved old/new input and
the observed partial marker, an absent unrelated marker and the generated answer visible in Claude.
Exactly two main prompts and one title prompt run. Confirmed keyboard exit returns 0, restores the
terminal and removes all five recorded groups/nine PIDs, listener, runtime and profile with unchanged
sources. No guard failure, emergency cleanup or live retry occurs. Fixed results are retained in
`.cache/terminal-review/live-interrupted-followup.log`; no prompt/response body is logged.

Before closing D84, review finds that the observer's historical end counter accepts any correlated
non-cancelled reply, including a JSON-RPC error. That counter alone cannot prove successful prompt
completion. An independent error/missing-stop/limit-stop regression fails, and the observer is
corrected to count end only for end_turn, retain cancelled separately and count other results as
non-success. Native frames are still forwarded unchanged; a non-success observation fails the test.
This is a test-oracle correction, not an observed product failure or production behavior change.
Rerun all six fake-ACP keyboard controls with this stronger observation before one new bounded actual
follow-up trial under the continuing authorization. The earlier live lifecycle/display observations
remain recorded, but the stronger trial must establish end_turn; do not infer it retrospectively.

All six actual-Claude/fake-ACP keyboard controls pass with the corrected observer in 34.283s under
race detection. The new actual revalidation passes in 22.88s (24.731s package), with zero non-success
prompt results. After 24 initial text updates, the main cancellation/retirement precedes new input.
The same client/proxy/profile/address remain, the follow-up group was previously unobserved, and one
follow-up text update/end_turn reaches the visible client. Old/new instruction fragments and the
partial marker are present; the unrelated marker is absent. Two main and two title prompts run;
one title ends before keyboard exit and all six recorded groups/eleven PIDs, listener, runtime and
profile are removed. Terminal restoration and source preservation pass, without emergency cleanup.
Fixed facts are saved in `.cache/terminal-review/live-interrupted-followup-strict.log`. Across both
live episodes there are four main and three title admissions. This is deliberate revalidation after
a discovered observer defect, not an automatic retry of a failed provider call. Marker presence
does not prove complete native-history byte equivalence; that broader claim is not made here.
Re-verified once on the measured Kiro 2.21.3 / Claude 2.1.267 pair on 2026-09-11 under the user's
explicit two-episode authorization: 21.19s, 32 streamed updates before one cancel, one follow-up
prompt in a previously unobserved group with the old/new fragments and partial marker present, two
main and one title admission, and joined cleanup (`live-recheck-kiro2213-claude21267.log`).

## Native model-picker control before a live model change (D85)

`TestCompiledRunModelSelectionWithFakeACP` now verifies the ordinary compiled command with actual
Claude and two independent fake models. Both picker entries, unchanged/switch selection, successful
idle ACP acknowledgement, next-response model, visible completion, last-model preflight restoration,
source preservation and keyboard cleanup pass. Final fixed results are in
`.cache/model-review/native-picker-restore-fake-acp.log`; no actual Kiro model call runs.

The next live experiment must discover currently advertised exact Kiro IDs, retain strict alias
mapping and select two available models without assuming a historical catalog. Bind observations
to that catalog and its advertised ACP selector; this fixture currently witnesses the legacy model
surface and two declared model IDs. Preserve the finite main/title budget and passive forwarding.
Keep full model IDs, session IDs, ACP bodies and UI captures out of retained diagnostics. A failed
live episode must not be retried automatically. Native Kiro selection and persisted conversation
resume remain open until their respective real-client observations pass.

## Current-catalog model selection experiment (D86)

Fresh finite discovery finds nineteen entries, and the separate prompt-free CLI/ACP comparison
confirms all nineteen IDs/aliases, equal current models and the legacy selector. The planned live
control chooses the CLI default and the first distinct advertised model; identities/labels remain
in bounded memory. It permits two main prompts and at most two title prompts across replacements,
unchanged native frame forwarding, and the existing 70-second terminal/120-second overall bounds.

Before selecting, require every advertised label to have rendered and occupied the selected row.
Only the declared target can receive Enter. Other unambiguous selected rows may receive navigation
keys but never count as advertised coverage or receive Enter. Bound navigation to three times the
catalog size plus four actions, at most two 750-ms stalled-key direction reversals, and a five-second
menu-observation stall. This is an experiment bound, not a CLI latency guarantee. Multiple selection
glyphs, overlapping labels and missing target/correlation cannot prove a model change. The original
input prompt above the picker heading is outside its selected-row scope; an unobserved heading is
not inferred from a footer. Native catalog rows and extra navigation rows are not merged.

The model witness must match the successful idle selection reply to its session-specific request,
then observe each generated answer marker in that prompt's active ACP session text and in the UI,
alongside end_turn. Split text chunks are handled with a bounded tail; foreign-session, repeated
and post-completion markers do not establish the answer. Compare next-preflight restoration and
unchanged client sources after joined exit, with no new recorded client/ACP session.

All three actual-Claude/fake-ACP controls pass in 11.94s (13.653s race package). The nineteen-model
case uses 26 navigation/selection actions, focuses all entries, passes five unadvertised-row frames
without selecting them, and completes both questions with matching markers and one target ack.
Each control restores the last model and removes all four recorded groups/five PIDs and artifacts.
Fixed results: `.cache/model-review/d86-picker-controls-final.log`. Local public-interface Claude
advice is saved/read/assessed; it is not treated as evidence about native implementation internals.
Run one actual pinned-pair trial next; do not retry a failed provider episode automatically.

The actual Kiro 2.21.2 / Claude 2.1.263 trial passes once in 23.90s (25.365s race package).
Two main and one title prompt run. All nineteen catalog labels render and receive focus within
26 key actions, including five unadvertised-row frames which are not selected with Enter or counted
as catalog coverage. One target-model acknowledgement precedes the second main prompt; both answers
have active-session markers, visible UI markers and correlated end_turn. The same observed native
client/proxy/profile/address are retained. A following doctor preflight restores the delivered model
without a new recorded client/ACP session. Confirmed keyboard exit restores the terminal and removes
all four recorded groups/seven PIDs, listener/runtime/profile, with unchanged source settings and no
guard failure, non-success result, emergency cleanup or live retry. Fixed results are in
`.cache/model-review/d86-live-model-selection.log`. This verifies the advertised session-setting
contract for the chosen pair, not provider weights or successful inference for every catalog entry.
Persisted restart/resume, authentication expiry and remaining alpha/release checks remain separate.
Re-verified once on the measured Kiro 2.21.3 / Claude 2.1.267 pair on 2026-09-11 under the user's
explicit two-episode authorization: 25.54s, nineteen labels rendered and focused in 26 actions with
no bounded reversal, one target acknowledgement, correlated markers, zero non-success results and
joined cleanup (`.cache/history-review/live-recheck-kiro2213-claude21267.log`).

## D87: finite native conversation restart through a fresh gateway and backend

Use disposable HOME/project roots and the pinned Claude 2.1.263. The ordinary ephemeral profile
baseline must first reproduce lost conversation history. The local-HTTP four-way control compares
the product retention option, an independently installed projects reference, a private profile and
native --no-session-persistence. It permits at most one model request per stage, 45 seconds overall,
15 seconds per native process and 128 KiB captured stdout. Two independent ports/tokens are allocated;
stage one is closed and its private profile removed before stage two resumes its public result UUID.
No native transcript is parsed or edited. Owned data scanning is limited to 128 entries, 2 MiB/file
and 8 MiB total; only independent marker/credential-presence facts are retained. Source settings,
profile removal, native session identity and exact previous/new HTTP marker counts are required.

The gateway/manager experiment separately runs two fresh stages with one admitted text request each.
The independent ACP fixture accepts initialize/new/prompt, rejects load, checks the projected marker
counts and echoes only newly authored text. The actual Kiro variant uses the existing restricted
execution preparation with one process/preparation per stage and no persisted backend record. Each
stage uses a pool limit of one process/session, 20-second setup, 45-second turn, 60-second client
process, 128 KiB stdout and four HTTP connections; the whole experiment is bounded to three minutes.
Each first response must end successfully before cleanup. Require the old backend group, native
client, listener, prepared policy/config and private profile gone before admitting the second stage.
The second question contains no copy of the random token given in the first; both its provider text
and native result must recover that token once. Tools are disabled, source hooks/auto-memory are off
in the owned settings, and no real user configuration is modified.

Observed results:

- Baseline product: first completion succeeds, resumed completion fails, no second HTTP request or
  retained native transcript; 1.37s test / 2.333s race package.
- Manual reference and two negative controls: 3.20s / 4.485s. After implementation, all four controls
  pass in 4.14s / 6.022s. Sources remain unchanged, profiles are removed, and no generated model/UI
  credential appears in the bounded native data.
- Actual Claude plus independent ACP/gateway/manager: 4.02s / 5.988s. Two successful stages, one
  admitted request and one backend end_turn each, joined cleanup and exact context-token recovery.
- Actual Kiro 2.21.2 / Claude 2.1.263: one episode, 26.50s / 27.783s. Both stages satisfy the same
  completion, identity, source, fresh ownership and cleanup conditions. Two main requests, no title,
  client tool or live retry. Fixed results are `.cache/history-review/d87-live-native-restart.log`.

The actual result is finite text resume through recreated restricted backend sessions. It does not
establish interactive picker/continue, pending-tool recovery, media/checkpoint sidecars, concurrent
native writers, retention cleanup or unobserved descendants. Keep these full alpha/release items
open. Public-only local Claude advice and its assessment are retained in the D87 consultation folder.

## D88: compiled foreground run followed by explicit-ID native resume

Extend the D82-D86 terminal harness with two distinct run invocations over one disposable HOME and
project. First use --client-history and an owned UUID passed to the native client. Join confirmed
keyboard exit completely, then use the product's --resume UUID option in the new process. Require
the previous generated answer visible before any new question is typed. Observe the typed question
before pressing Enter. The second question asks for a random token supplied only in the first.

The ACP witness first correlates session/new, rejects session/load and validates exact independent
marker/token counts in each main prompt. It ignores foreign-session text and foreign reply IDs;
answer credit requires the expected active-session text and correlated successful end_turn. Its
text buffer is limited to 8 KiB. Cancelled/error replies never qualify. Existing exact-frame limits,
one-main/at-most-one-title admission per launch, 70-second PTY lifetime, two-minute launch context,
bounded receipt/UI capture and joined/emergency cleanup rules remain unchanged. Do not dispatch
the second launch if any first-stage condition fails. No tools or real-user settings are used.

The independent observer tests first fail for the missing witness, then cover missing/duplicated
and foreign context, load attempts, split chunks, foreign IDs and unsuccessful completions. The
full observer race suite passes in 1.226s. Actual Claude with fake ACP passes in 7.84s (9.120s race
package), with two clean exits, restored second-stage history and new ownership.

One actual Kiro 2.21.2 / Claude 2.1.263 episode passes in 29.71s (30.993s package). First stage:
one main/one title, four groups/seven PIDs. Second stage: one main/no title, three groups/five PIDs.
Both have one validated main input and one correlated answer receipt. The second terminal shows
the old answer before input and the new answer with the recovered token afterward. Every recorded
process/group, listener/runtime/profile is removed, terminal state restored and original settings
unchanged. The two client/proxy/main-ACP identities, profiles and addresses differ. No guard or
prompt error, emergency cleanup or live retry occurs. Fixed results are retained under
`.cache/history-review/d88-terminal-history-control.log` and `d88-live-terminal-history.log`.

This adds foreground explicit-ID text resume evidence. Native picker/continue, pending tools,
cross-project changes, native data outside projects, concurrent writers/retention and arbitrary
hooks/agents remain separate work; the full alpha/release objective is unchanged.

The nine existing native-Claude/fake-ACP keyboard regressions pass in 42.903s. An initial opt-ins-off
run inside the sandbox fails relay controls and aborts on an explicitly denied loopback bind. The
unchanged test suite passes with the established local-socket permissions in 23.275s. Observer race,
whole-repository/observer vet, formatting, whitespace and D87's 139 inventory checks also pass.

## D89: native conversation picker after restarting the foreground command

Name the first owned interactive session with the native public flag, complete one text question
and confirm keyboard exit/cleanup. Start a new run --client-history over the same disposable HOME,
project and proxy state directory. Type /resume, observe it in the input widget, then press Enter.
Require the exact named row selected below the picker heading before the selection key. Refuse
duplicate/partial names or another selected row. Selection observation has a ten-second bound;
all D88 process/frame/receipt/main/title limits remain unchanged.

Observe the restored old answer before typing the new question. The new question contains no
copy of the first question's random token. Require D88's ACP context/response witness, new answer
on screen and full keyboard cleanup. Each stage has fresh runtime and observer executable paths;
sharing product state does not itself prove stable executable-identity cache reuse. No native
transcript format is parsed or edited, and no tools or real-user configuration are used.

The initial fake-ACP picker test passes in 8.08s (9.363s package). Final fake ID/picker controls with
shared product state pass in 17.894s. One actual Kiro 2.21.2 / Claude 2.1.263 picker episode passes
in 33.17s (34.835s): two main prompts, no titles, one validated context and correlated answer per
stage, old history visible before new input, and exact token recovery. Both stages remove three
recorded groups/five PIDs, listener/runtime/profile, preserve settings and restore terminal state.
No guard/prompt error, emergency cleanup or retry occurs. Fixed results are retained in
`.cache/history-review/d89-native-history-shared-state.log` and `d89-live-native-picker.log`.
Observer race passes in 1.903s. Broader selection, continue/latest, pending tools, native sidecars,
concurrent writers/retention, authentication expiry and full alpha/release work remain open.

## D90: completed client effects across native resume

Use two disposable native print launches for each of Write and Bash. The first owns one exact
operation, one matching successful result continuation and one completed answer. Bash appends one
line to an owned file; Write creates the separate owned target. Native PreToolUse/PostToolUse hooks
append one fixed receipt each. The backend/fixture never performs either effect. Join the first
client, ACP/relay group, manager, pool, listener and profile before preparing the second launch.

The second launch retains the same native conversation ID but receives fresh routing credentials,
endpoint, temporary profile and backend. Its one model request must contain exactly one prior
tool call/result pair, with the same ID, name, decoded argument values, successful status and exact
text content in order before the new user instruction. It also carries the old question/answer
markers once. No new tool is admitted in this text continuation. Both native result JSON and
active backend text must contain the new completed-answer marker. Effects and both hook receipts
must still occur exactly once. Source settings/global configuration remain unchanged.

Bound each case to three minutes, each native invocation to sixty seconds/128 KiB output, each
model turn to forty-five seconds and each stage to one ACP process/session/prepared launch. Admit
two HTTP model requests in stage one and one in stage two; no titles, retries, extra tools or
backend recreations. Stop before any following stage/case after a failure. Output observations
remain bounded in memory; saved diagnostics contain only fixed facts/counts and owned PID/stage
receipts. Never parse/edit native transcript files. Public-only Claude advice was saved, fully
read and assessed under `.cache/claude-consult/work/public-tool-resume-review-jnlw7k4c`.

Mutated/missing/duplicate/reordered tool pairs and doubled hook/effect receipts are independent
negative controls. Another guard rejects a newly emitted tool during the resumed text turn.
The first local fixture controls exposed an overly narrow one-message admission and an unwanted
model-discovery process in the test adapter. The observer now accepts the client's bounded trailing
system message, and models come from the already prepared catalog. Product behavior is unchanged.
Both corrected native-Claude/fake-ACP cases pass in 10.55s (12.473s race package): four clean stages,
two original client effects, preserved pairs, zero resumed tool handoffs and unchanged sources.

The live opt-in is `TestKiroLiveCompletedToolNativeResume`: at most two main prompts per tool case,
four total, with two original client effects and no automatic retry. This measures completed Write/
Bash history and effect counts only. Interrupted pending hooks, new post-resume permission/hook
decisions, crash-before-acknowledgement windows, interactive tool resume and concurrent writers
remain separate acceptance work. No general exactly-once recovery guarantee is inferred.

One actual Kiro 2.21.2 / Claude 2.1.263 run passes in 56.99s (58.324s race package): Write 29.65s,
Bash 27.34s. All four native launches finish, with six HTTP model requests, four main ACP prompts,
two original client tool effects, exact restored pairs and zero resumed handoffs. Both hook counts
and each effect stay one. Every recorded old/new group and client/relay PID, listener/profile and
private launch artifact is removed; source settings/global configuration are unchanged. No live
retry occurs. Fixed evidence: `.cache/history-review/d90-live-completed-tool-resume.log`.

The final local controls also reject a completed-answer marker moved before its tool result, and
pass with both native fake-ACP cases in 10.830s (`d90-final-native-controls.log`). Opt-ins-off ACP/
interop race regressions pass in 5.670s/22.879s. Whole-repository/fake-peer vet, formatting, whitespace
and the current D87 artifact's 139 inventory byte checks pass. No product change or live retry.

## D91: native resume after terminating a delivered, unexecuted tool

Reuse D90's two native print launches, fresh routing ownership, exact operation and source/cleanup
checks. The first native launch uses an independently generated UUID and an exact Bash PreToolUse
hook. The hook validates the native session ID, original tool-use ID, tool name and command, allows
only bounded description metadata, records its PID/group/ID digest, then waits at most thirty
seconds. It performs no client effect and spawns no subprocess. A separate matching PostToolUse
receipt is possible only after that original tool. Native stdin/config reads have two-second and
64/16 KiB bounds. Retain only fixed receipts and process coordinates.

Before cancellation, require one completed HTTP handoff, one exposed tool, no result/completion,
the exact live hook and no file effect. Repeat cancellation of the owned native runner eight times;
require return within eight seconds and joined old client/hook/ACP/relay groups, server/profile and
private resources. Only after this cleanup, create the old hook's release marker. Observe three
hundred milliseconds with twenty-millisecond effect/liveness checks, then retain that marker
through the new native resume. The old hook and file effect must remain absent throughout this
measured continuation. Emergency cleanup records failure and targets only recorded owned groups.

Native Claude 2.1.263 omits the unfinished tool pair in this measured print/SIGTERM path. With no
preceding model text it supplies the short non-completion placeholder `No response requested.`;
with an independent partial response it preserves that text. This is not an explicit cancellation
notice or a successful tool result. Do not manufacture either result status. The next question
explicitly says the old operation was interrupted before execution and requests no further tool.
Require the entire original question, the observed placeholder or exact previous partial text,
and that exact new question in order; reject old tool IDs, tool blocks, completed-answer markers,
changed questions, duplicate/moved placeholders or new tool handoffs. Completion must be observed
in both the active backend and native public result JSON. Completed-tool pair checks stay exact.

The first strict error-pair expectation fails locally after safe cleanup, because no such pair is
present; the native shape is observed without parsing or editing transcript files or logging
content. Public-only follow-up Claude advice is saved/read/assessed under
`.cache/claude-consult/work/public-pending-history-review-e7uvd4dc`. Its hypothetical explicit
notice premise is not claimed as an observed native fact. Distinguish measured runtime abandonment
from full cancellation-history fidelity, implicit continuation or arbitrary crash recovery.

The release control first proves the same held hook can permit one effect/result and subsequent
completed history resume. No-text interruption and partial-text interruption then both prove zero
effects/results, old cleanup, late-release safety and successful new text turns. These three native/
fake-ACP controls pass in 12.92s; D90's two completed controls also pass in 7.83s, combined race
package 22.675s. Hook/observer corruption controls remain independent.

The live opt-in `TestKiroLivePendingToolNativeResume` admits two main prompts total, one initial
client tool and no tool execution. Each stage has one ACP process/session/launch and one HTTP model
request. Existing forty-five-second turns, sixty-second native invocations/128 KiB capture and
three-minute overall bounds remain. A failed stage prevents the next; no automatic live retry.
Post-resume new permission/hook decisions, interactive pending resume, multiple pending tools,
effect/acknowledgement crash windows and all remaining alpha/release gates remain open.

The single actual Kiro 2.21.2 / Claude 2.1.263 episode passes in 30.19s (race package 32.156s),
with no retry: two main ACP prompts, two HTTP model requests, one initial tool handoff and zero
tool effects/results. Native cancellation returns in 147ms. Old recorded hook/client/ACP/relay
processes and groups are gone before the late-release check and fresh resume. The resumed request
retains the exact original question and all 31 bytes of the prior partial assistant text, omits
the unfinished tool pair, and completes the explicit non-executing follow-up without a new handoff.
Both stages remove private resources and preserve source settings. These are bounded abandonment
observations, not an explicit native cancellation result or general crash-recovery guarantee.

Final opt-ins-off ACP, interop and independent hook race regressions pass in 5.738s, 22.585s and
1.306s respectively. The private diagnostic logs are `d91-live-pending-resume.log`,
`d91-final-native-controls.log` and `d91-core-regression.log` under `.cache/history-review/`;
they retain fixed facts/counts rather than model text or tool payloads. No production code or
dependency change is included in this checkpoint.

## D92: fresh native tool policy after completed-history resume

Each episode first completes one owned append through native Bash under an allow policy. After
joined old cleanup, resume the same native conversation with a fresh profile/endpoint/token/backend
and a separate supplied user-settings source. Ask for exactly one new append to a distinct owned
target. Compare allow, explicit deny (alongside an allow rule), and a PreToolUse veto with an allow
rule. Source policy files remain unchanged; the test selects the second policy at preparation.

Before exposing the new call, validate the old ID/name/decoded input/successful result text and
completed answer in order before the exact new question. Require a distinct new ID and exact new
operation. The result continuation must retain that same old pair and contain only the matching
new pair after the new question, with expected success/refusal status and the hook refusal reason
where applicable. Old/current IDs, result statuses and effect targets cannot substitute for one
another. Independently mutated/reordered/duplicated/missing histories must reject.

The old append and old pre/post receipts must remain exactly one. The new allowed append and both
receipts occur once; both refusal cases leave the new target/post receipt absent, with exactly one
pre receipt for the hook veto. Explicit deny may run its pre hook zero or one time; this control
does not assert native hook/permission evaluation order. All recorded client/ACP/relay ownership,
listeners, profiles and launch artifacts must be removed, and both policy sources stay unchanged.

Reuse the finite D90 limits: three minutes per case, native invocations sixty seconds/128 KiB,
forty-five-second turns and one ACP process/session/launch per stage. Each stage admits two HTTP
model requests and one tool; no title, retry, extra operation or backend recreation. Across the
three live cases the budget is six main ACP prompts, twelve HTTP model requests and four allowed
owned appends total. A failed stage/case prevents following dispatch. Run the independent protocol/
observer controls and all three native fake-ACP cases before the opt-in actual Kiro test. This
finite settings/hook experiment does not replace interactive approval screens, interrupted-history
new tools, multiple simultaneous calls or the remaining alpha/release requirements.

All three native-Claude/fake-ACP cases pass in 16.22s (17.557s race package). Final independent
history/handoff/refusal-witness controls pass in 2.044s; old IDs, changed operations, missing prior
history, repeated calls, lost refusal reasons and a file/post hook after refusal all reject.
Existing completed/interrupted native controls pass in 23.479s. Opt-ins-off ACP/interop/hook race
regressions pass in 6.017s/21.855s/1.308s. Whole-repository/fake-peer/hook vet, formatting, whitespace
and the unchanged D87 artifact's 139 byte checks pass. No production or dependency change.

The first actual test invocation stops at version/account preflight in 6.89s (7.233s package),
before any model work; no following stage/case is dispatched. Subsequent public read-only probes
verify pinned main/helper versions and login (2.27s), then identity under a new owned KIRO_HOME
with the same five-second bound (2.41s) and nineteen catalog entries without ACP (8.85s). A separate
helper whoami observation times out after emitting output; that is not a successful command and
does not establish the cause of the original preflight failure. Limits/pins/policy stay unchanged.

A new bounded invocation after those fresh checks passes all three actual Kiro 2.21.2 / Claude
2.1.263 cases in 142.13s (143.450s race package): allowance 51.49s, configured denial 45.22s,
hook veto 45.42s. Six main prompts/twelve HTTP requests produce four allowed owned appends and two
matching fresh refusals. Every resumed request and result continuation preserves its original
successful pair; each new call has its own ID and expected result status. The old effects/hooks
stay one, the two refused targets/post hooks stay absent, and all six stages join recorded
ownership and remove private resources with both settings sources unchanged. No model prompt is
retried. Logs under `.cache/history-review/`: `d92-native-policy-controls.log`,
`d92-final-observer-controls.log`, `d92-prior-native-regressions.log`, `d92-core-regressions.log`,
`d92-live-resumed-policy.log`, `d92-readonly-preflight.log`, `d92-isolated-preflight.log` and
`d92-live-resumed-policy-verified-preflight.log`.

## D93: additional-directory personal instruction candidate

Use the existing bounded personal-assets observer and a fresh disposable HOME/project, actual
Claude 2.1.263, a local synthetic Messages responder and no external inference. Compare natural
personal instructions with an experimental --add-dir of the owned original .claude directory plus
CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1. Retain personal rules at their existing native scope;
do not enable this candidate in production. Repeat both with original absolute-path exclusions.

Before any acceptance claim, require the same unconditional root body (including path frontmatter),
original relative-import bases, exactly four import hops with the fifth absent, instruction order,
no duplicates and exact exclusion behavior. Observe the public Messages request rather than asking
a model to describe its context. Existing asset discovery, source-tree/import bytes and process/
private-profile cleanup assertions remain active. Each native invocation has fifteen seconds and
64 KiB output; the case has two minutes, at most four text-only Messages requests, no client tool
effect, no ACP process or real model prompt. A failed mode stops following modes.

Public-only local Claude advice was saved and read at
`.cache/claude-consult/work/public-instruction-paths-review-mzdhgkrx`. Review corrects its five-hop
claim and uncertainty about the documented additional-directory environment variable, and rejects
model recitation as evidence of loaded content. Official memory/directory/CLI/permission/env-var
pages were independently checked. An option loading text does not by itself establish native
personal scope or safe additional-configuration/file-access behavior.

The initial equivalence test fails in 1.33s (2.302s race package) after the natural control and first
candidate. Natural personal memory retains four import hops before project instructions. The
candidate retains the unconditional root body, but no personal root import/hop and moves that body
after project instructions. Both requests complete and their client groups exit. Later modes are
not run after this failure. This rejects the candidate; it does not change product acceptance.

The separately named `TestClaudeAdditionalDirectoryMemoryCounterfactual` then reproduces those exact
defects and compares original-path exclusions. All four controls pass in 1.93s: the two excluded
cases omit the personal root/general rule while retaining project instructions, candidate source
trees/imports remain unchanged, and groups/private profiles are cleaned. Existing personal-rule,
four-hop and rejected-adapter controls also pass; combined race package 14.722s. A passing defect
reproduction is not successful personal-memory support. Logs are
`.cache/history-review/d93-additional-directory-memory.log` and
`d93-native-memory-counterfactuals.log`. No production adapter, dependency, ACP session or external
inference is added by this experiment. Original personal memory fidelity remains open.

## D94: independent concurrent HTTP/process churn

This is a local fixture experiment, not a live Kiro credit-consuming test. Keep all real-client
opt-ins disabled. The session test harness builds the independently authored http-churn ACP peer;
it accepts one session, two prompts, twelve frames, 64 KiB frames and thirty seconds at most.
No client tool, relay, external service or original user setting is involved.

Keep a single HTTP server/transport/manager/pool for the episode. For each of eight concurrent owned
identities, complete one buffered request, then open a held streamed continuation. Match the actual
leader/group identity, retained backend session, second prompt count and exact one-question delta.
All eight streams must emit their observations and remain active before eight repeated calls to the
shared caller cancel function. Require zero handlers, connections and pool ownership, and absence
of each observed leader/group before new identities in the following wave. Shutdown all owners
four times after the last wave. The peer never completes its second prompt; terminal/error data
cannot count as a cancellation witness.

Use eight one-session processes, eight manager bindings, eight client connections and a server cap
of sixteen connections. Each wave has fifteen seconds, the episode three minutes and observed HTTP
input/output at most sixteen KiB. Default waves are eight; DAX_FIXTURE_CHURN_WAVES accepts 8..64.
Measure descriptors through /dev/fd (Linux: /proc/self/fd), and test-process goroutines/HeapAlloc after
GC at each settled wave. Record the fourth wave as warmup baseline and fail later samples above
baseline +2 FDs, +16 goroutines or +8 MiB heap. These deliberately stated envelopes do not exclude
smaller leaks. Observe actual retained-file-descriptor and malformed/history/terminal counterfactuals.

With the reviewed offline Go 1.27.1 environment, the finite extended command is:

```sh
DAX_FIXTURE_CHURN_WAVES=64 DAX_INTEROP_KIRO_CREDIT_OPT_IN=0 \
  DAX_INTEROP_KIRO_BINARY= DAX_INTEROP_CLAUDE_BINARY= \
  go test -race -p 1 -count=1 -timeout 4m \
  -run '^TestConcurrentHTTPProcessChurn$' -v ./internal/session
```

The first invocation fails before model requests on a mismatched test pool/session timeout. After
matching their immutable process configuration, all eight-wave and observer controls pass: 128
requests, 64 joined groups, 1.25s episode / 4.404s package. The separate 64-wave run passes in 4.48s
/ 6.856s package, with 1,024 requests and 512 joined process/group instances. Every held continuation
has matching ownership/history and all next waves admit fresh sessions after cleanup.

| Test-process observation | Warmup baseline | Post-warmup peak | Final shutdown |
| --- | ---: | ---: | ---: |
| OS descriptor count | 6 | 6 | 5 |
| Go goroutines | 4 | 4 | 2 |
| GC-retained Go heap bytes | 813,696 | 1,017,664 | 836,344 |

Private fixed-class logs: `.cache/history-review/d94-churn-initial.log`, `d94-churn-controls.log`
and `d94-churn-1024.log`. No raw request/reply, PID, credential or tool output is logged. This four-
second stress episode does not establish actual Kiro/Claude or relay behavior or shared-process
churn; native RSS, pending-tool churn and long-duration soak were measured later (D95, D120). No
product/dependency/artifact change is made.

Applicable opt-ins-off race regressions pass in `d94-core-regressions.log`: ACP 5.183s, pool
2.435s, gateway 3.502s, session 29.327s and interop 23.569s. Whole-repository/fake-peer vet passes
(`d94-vet.log`), along with formatting, whitespace and the unchanged D87 artifact's 139 byte checks.

## D95: concurrent pending-tool denial and recovery

Use only synthetic HTTP clients and an independently authored pending-churn ACP peer, with real
client/model opt-ins disabled. The inert client_action has no execution implementation or file effect.
One server/transport/manager/process pool/schema pool serves all waves. Per wave, eight unique
identities receive complete HTTP tool handoffs while their owned ACP/relay groups remain pending.
Require unique call IDs, leaders, relay children and private relay configs, actual child/group
membership, regular 0600 configs, zero active HTTP requests and eight busy backend sessions.

Before recovery, send one internally paired foreign-owner request: use another live call's ID in
both the forged assistant block and its result. Check that its Messages envelope/control fields
decode before dispatch, require HTTP 400, and recheck all original pending ownership. Then return
each exact original call's error result followed by its own new question. Require new ACP/relay/
config ownership and a first full-history prompt retaining exact original question, assistant text,
call ID/name/input and matching denial before that question. No new tool handoff may occur. Check
old leader/group/relay disappearance and old config/directory removal independently of manager stats.

Keep eight successful replacement sessions idle. New identities in the following wave must evict
them and remove their recorded processes/relay artifacts before the next pending barrier. Close the
last idle owners and server/schema resources four times at final shutdown and require zero owner
counters plus removed recorded ownership. Observations remain in bounded memory; logs contain only
counts, fixed failure classes and resource measurements, never prompts, results, IDs or config paths.

Limits: eight one-session ACP processes/eight manager bindings/eight idle slots; eight schema workers;
eight client/sixteen server connections; sixteen KiB HTTP observation; twenty seconds per wave;
three minutes per episode. The peer accepts one session/prompt, twelve frames of at most 64 KiB and
thirty seconds lifetime. It starts/reaps only its supplied MCP child and does not open relay config.
Default eight waves; DAX_FIXTURE_PENDING_WAVES accepts 8..32. Sample FD/goroutine/post-GC heap after
each settled wave, using the fourth as baseline and D94's +2/+16/+8 MiB ceilings. These samples retain
the known eight idle ACP/relay owners and reusable schema workers; they do not represent zero-owner
baseline. The finite extended command with the reviewed offline Go 1.27.1 environment is:

```sh
DAX_FIXTURE_PENDING_WAVES=32 DAX_INTEROP_KIRO_CREDIT_OPT_IN=0 \
  DAX_INTEROP_KIRO_BINARY= DAX_INTEROP_CLAUDE_BINARY= \
  go test -race -p 1 -count=1 -timeout 4m \
  -run '^TestConcurrentPendingToolDenialChurn$' -v ./internal/session
```

The first run fails at wave two in 4.13s (7.293s package): HTTP 502 and retained relay-cleanup errors.
Its new peer postpones Wait on its child until ACP EOF. Prompt reaping while ACP remains idle fixes
that fixture lifecycle, without relaxing product cleanup. The corrected eight-wave test passes in
10.49s / 13.639s package, 136 HTTP requests, 64 inert handoffs/recoveries and 128 joined ACP groups/
relay children. The earlier foreign-ID control used an unmatched result; the final extended run
below strengthens it to a syntactically valid paired foreign history. No native zombie state or
Kiro defect is inferred from the initial fixture failure.

Independent corrupt-history/ownership controls pass in 3.098s package. The final 32-wave run passes
in 41.61s / 45.299s package: 544 HTTP requests, 256 inert handoffs, 32 foreign-history rejections,
256 exact fresh text recoveries, 512 joined ACP group instances and 512 joined relay children with
removed private configs/directories. Each wave ends with eight accounted idle groups; intermediate
joined counts exclude them, and final counts include their joined shutdown.

| Test-process observation | Warmup baseline | Post-warmup peak | Final shutdown |
| --- | ---: | ---: | ---: |
| OS descriptor count | 70 | 70 | 5 |
| Go goroutines | 108 | 108 | 2 |
| GC-retained Go heap bytes | 1,204,528 | 1,351,096 | 942,832 |

Logs in `.cache/history-review/`: `d95-pending-initial.log`, `d95-pending-reaped.log`,
`d95-pending-observers.log`, `d95-pending-32-waves.log`. This is finite independent
one-call-per-session evidence, not actual Kiro/Claude, UI/hook approval, multi-call batches,
prepared/native policy, shared ACP process; native RSS and long-duration soak were measured later
(D120). No product/dependency/artifact change.

Final opt-ins-off race regressions pass in `d95-core-regressions.log`: ACP 5.416s, pool 2.554s,
gateway 3.397s, session 38.741s, relay 10.413s, MCP 5.689s, schema 3.125s and interop 24.826s.
Whole-repository/fake-peer vet passes (`d95-vet.log`), with formatting, whitespace and the unchanged
D87 artifact's 139 byte checks. The fixture provenance inventory includes the new peer.

## D96: immediate idle relay shutdown and actual resume regression

The independent idle-attachment test completes authentication before measuring eight concurrent
Close callers. Require less than 750ms, joined peer/handlers, revoked tool admission, empty accepted
connections, removed private artifacts and a still-live healthy ACP owner. The original code fails
at 1,018ms; immediate accepted-connection close passes at 18ms. Existing authentication/lifetime
controls pass, including the deliberately surviving peer's retained cleanup failure (1.07s).
The separate one-second peer-disappearance bound is unchanged.

With real opt-ins off, repeat D95's exact 32-wave command above. All 544 requests, 256 handoffs,
32 foreign-history rejections, 256 fresh recoveries and 512 ACP/relay/config joins pass in 10.67s
(14.637s package), compared with 41.61s before. Steady FD/goroutine counts are 70/108, final 5/2;
GC-retained heap baseline/peak/final is 1,182,048/1,326,592/940,816 bytes. Full opt-ins-off repository
race tests pass (27 packages, six without tests); vet passes. Native Claude/fake-ACP allow/deny/hook
resume controls also pass in 15.86s (17.877s package).

Then use the existing real-client preflight, ownership/history/effect guards and credit opt-in for
only the allowed-Bash resume case. Budget two main ACP prompts/four HTTP model requests and two
owned append effects; do not dispatch the deny/hook subcases or blindly retry a failed episode.
With the reviewed offline toolchain and pinned absolute client paths:

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
  DAX_INTEROP_KIRO_BINARY=/Users/geunwooshim/.local/bin/kiro-cli \
  DAX_INTEROP_CLAUDE_BINARY=/Users/geunwooshim/.local/bin/claude \
  go test -race -p 1 -count=1 -timeout 5m \
  -run '^TestKiroLiveResumedToolPolicy$/^allow-bash$' -v ./internal/interop
```

The single episode passes in 47.55s (48.873s package). Each stage receives one matching successful
Bash handoff/result and end_turn; the resumed stage checks exact old history in both requests.
Old/new effects and native hooks remain once, sources remain unchanged, both native clients exit 0,
and recorded process/group/profile/listener ownership is removed. This checks the production close
change with actual Kiro/Claude but does not extend the broader resume/soak acceptance scope.

Private fixed-class logs under `.cache/history-review/`: `d96-idle-close-before.log`,
`d96-idle-close-after.log`, `d96-pending-32-waves.log`, `d96-full-regression.log`,
`d96-native-policy-control.log`, `d96-live-resumed-allow.log` and `d96-vet.log`.
The refreshed local artifact and its 139 offline byte checks are recorded in DEPENDENCY_REVIEW.md.

## D97: root presence and unintended alias exclusion

Use the unmodified pinned Claude 2.1.263 with disposable HOME/project/private profiles and a bounded
authenticated local server. Kiro credit and binary opt-ins remain off. Eight streaming controls send
only initialize/get_settings/get_context_usage, with 64 KiB frames, 32 total frames, five-second read
deadlines and fifteen-second process lifetimes. Check exact effective exclusion arrays, original/
alias path presence, unchanged owned HOME trees, successful native exit and absent owned groups/
profiles. Model and token-count request counts must remain zero.

The initial hypothesis test fails on empty and comment-only roots: both are absent without exclusions
(3.98s test / 4.976s package). Separately named counterfactuals preserve that distinction, compare
active plain/frontmatter/import-only roots with exact/glob-excluded roots, and add a pattern excluding
only the relocated wrapper. All eight pass in 3.76s. Their absence observations never authorize a
production exclusion decision.

A separate two-launch natural/direct-alias comparison sends one synthetic local Messages request
per launch, no external inference or tool effect. Its only exclusion is **/client/CLAUDE.md, which
matches the private alias and not the owned original root. Natural execution retains root text and
four import hops before project instructions; the alias loses that root and its imports, retaining
personal rules and project instructions without duplicates. Both complete with group/profile cleanup,
and candidate source trees/global JSON/import files remain unchanged. The control passes in 0.97s.

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=0 DAX_INTEROP_KIRO_BINARY= \
  DAX_INTEROP_CLAUDE_BINARY=/Users/geunwooshim/.local/bin/claude \
  go test -race -p 1 -count=1 -timeout 2m \
  -run '^TestClaude(MemoryListingIsNotRootExclusionOracle|PersonalRootAliasOverExclusionCounterfactual)$' \
  -v ./internal/interop
```

Combined race package 6.644s; existing native memory/control/alias/depth/additional-directory
regressions pass in 22.858s. Logs: `.cache/history-review/d97-wrapper-oracle-initial.log`,
`d97-exclusion-counterfactuals.log`, `d97-memory-regressions.log`, `d97-interop-regression.log` and
`d97-vet.log`. Logs retain fixed categories, synthetic marker facts and counts only. The public
[memory documentation](https://code.claude.com/docs/en/memory),
[settings documentation](https://code.claude.com/docs/en/settings) and
[CLI reference](https://code.claude.com/docs/en/cli-reference) were checked on 2026-09-10; no private
client source or dependency is used. Production, dependencies and D96 artifact bytes do not change.

## D98: new tool policy after an interrupted native history

Extend the independent native-history experiment by combining a delivered, unexecuted old Bash
with a distinct new Bash operation after explicit-ID resume. First establish the exact live
PreToolUse hook, delivered HTTP handoff and absent effect/result. Cancel the runner eight times;
join its hook/client/ACP/relay ownership, listener/profile and launch artifacts. Create the late
release marker only after that join and require the old effect to remain absent for 300ms. The
resumed operation cannot be delivered until those old ownership/effect checks still pass.

The new client's first request and result continuation must both retain the exact old question,
measured partial response or native non-completion placeholder, and exact new question in order.
The old unfinished pair stays absent. Require one distinct new call ID/operation and its exact
matching success/refusal. A new result remains mandatory: abandoned old history cannot satisfy
the new completion condition. Observe native file effects and pre/post hooks independently, with
unchanged old/new settings sources. Each stage owns a new profile/address/token/backend group.

The fixture matrix uses native no-text and partial-text interrupted histories, each followed by
allowance, configured denial or hook veto. All six cases pass in 32.00s (33.282s race package):
eighteen HTTP requests, twelve separate ACP prompts/owners, six old zero-effect cancellations,
two new allowed appends and four fresh refusals. Both history checks pass in each resumed stage;
all recorded ownership and private artifacts are removed. The first guard command fails at
compilation on a test-only missing brace, before any process/model dispatch. Corrected history,
ownership, new-result and existing completed-history guards pass in 2.149s race package.

The separate actual-client episode is limited to allowance: two main ACP prompts, three HTTP
model requests, one cancelled old handoff and one new allowed append. No title request, additional
tool, retry, extra model prompt or live refusal matrix is admitted. Retain the shared preflight,
45-second turn bound, sixty-second client lifetime and three-minute episode bound. With the
reviewed offline environment and pinned client paths:

```sh
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
  DAX_INTEROP_KIRO_BINARY=/Users/geunwooshim/.local/bin/kiro-cli \
  DAX_INTEROP_CLAUDE_BINARY=/Users/geunwooshim/.local/bin/claude \
  go test -race -p 1 -count=1 -timeout 4m \
  -run '^TestKiroLiveInterruptedResumedToolPolicy$' -v ./internal/interop
```

This remains explicit-ID print-mode single-tool recovery, not interactive pending-tool approval,
general crash exactly-once semantics, simultaneous tool batches or restored hidden Kiro context.
No native transcript is decoded or changed by the test. Results are observed through Messages,
ACP, process ownership and owned client effects; logs retain fixed categories/counts only.

The first invocation stops at generic version/account preflight before model work (4.67s test,
4.933s package). Its failed command and exact cause are not observable in that record. Separate
read-only baseline/owned-KIRO_HOME identity controls then pass in 6.23s (7.566s package), twelve
version/account commands and no model work. Add fixed command/exit/byte-count/failure/cleanup
diagnostics and a six-command allowlist to the shared preparation. A standalone identical-preparation
control passes in 10.30s (12.227s package), including catalog and private-directory cleanup, with
zero ACP sessions/model requests. These fresh admissions do not explain away the earlier failure.

The bounded actual episode following those checks passes in 46.95s (48.273s package). Cancellation
at the old delivered hook joins in 166ms, with no old effect/result after late release. Stage two
uses new process/profile/endpoint/token ownership; both resumed requests preserve the original
question and all 31 bytes of old partial text before the exact new question. Its one new Bash
operation succeeds once, with matching pre/post hooks, end_turn and native exit 0. Both stages
remove recorded groups/PIDs/listeners/private artifacts and preserve both settings sources.
The combined model budget is two ACP prompts/three HTTP requests/one owned append; no dispatched
model work is repeated. Actual refusal/hook policy after interrupted history remains separate.

Existing native completed/pending/policy regressions pass in 34.779s. Applicable opt-ins-off race
suites pass ACP 6.023s, session 29.452s, interop 22.133s and owned resume hook 1.677s. Logs under
`.cache/history-review/`: `d98-guard-controls.log` (initial compile failure), `d98-guard-checked.log`,
`d98-native-interrupted-policy.log`, `d98-native-history-regressions.log`, `d98-core-regressions.log`,
`d98-live-interrupted-policy.log` (initial preflight failure), `d98-readonly-identity.log`,
`d98-readonly-prepared.log`, `d98-live-preflight-verified.log`, `d98-final-interop.log` and
`d98-final-vet.log`. Production code, fixture source, dependencies and D96 artifact remain unchanged.
Final interop race passes in 21.439s after the preflight diagnostic addition; final repository/
fixture/hook vet, formatting, whitespace and all 139 development-artifact byte checks pass.

## D99: bounded effort command and state observation

Use the measured Kiro 2.21.3/v2 executable, or an admitted same-major pair recording its observed
version. DAX_INTEROP_KIRO_BINARY opts into these no-prompt
checks; credit opt-in and the Claude binary remain disabled. Each test owns one empty-agent ACP
session, private KIRO_HOME/workspace/TMPDIR and one-minute total deadline with fifteen-second RPC
ceilings. It advertises no filesystem/terminal capabilities and exposes no tools or MCP servers.
The original Kiro settings file is bounded and fingerprinted before/after; recorded groups must
be gone before the private root is removed. No model credits or native tool effects are requested.

TestKiroPinnedEffortCommandQuery performs one advertised empty-argument effort query. Auto returns
a negative result. TestKiroPinnedSelectedModelEffortQuery first selects an exact currently advertised
Sonnet 4.6 model, then observes query success. The response lists allowed choices, not current effort.
TestKiroPinnedEffortRoundTrip additionally uses the production adapter for high then low. Each value
is requested twice but only one setting RPC is allowed; fresh matching-session metadata must report
that value. Already queued state is discarded before each setting. Observation windows are 50ms
before/300ms after, capped at 64 events, 64 KiB per event and 1 MiB aggregate each. A success reply
without a matching new state does not pass the experiment. Production does not depend on metadata.

The public-only terminal observation uses the documented KIRO_ACP_RECORD_PATH with an owned FIFO,
25-second deadline, 512 KiB total wire, 64 KiB line and 256 KiB terminal bounds. Only /effort high
is typed, with auto selected; no prompt is allowed. It observes command/args/value and retains
only that known command shape plus lifecycle facts. The final recording joins group disappearance
in a bounded wait before removing the private root. Native settings remain unchanged.

Evidence in .cache/history-review/:
- d99-effort-empty-query.log: descriptor presentation-label guard rejected before the effort query.
- d99-effort-query-shape.log and d99-effort-selected-query.log: auto rejection and selected-model
  query success (7.921s/7.948s race packages), each with joined cleanup and no prompt.
- d99-effort-native-command.json and d99-effort-native-terminal.json: early exits before slash input;
  the latter reports SIGXFSZ from the recording harness's overbroad file-size limit.
- d99-effort-native-fifo.json: observed command, initially incomplete immediate group-exit check.
  A separate named-process check found no survivor; d99-effort-native-joined.json verifies the full
  final 4.079s episode, source preservation and cleanup. The private observer script is
  observe-native-effort.py; no raw wire/terminal recording is retained.
- d99-effort-wire-red.log: old production payload fails the independent contract regression.
- d99-effort-core.log and d99-effort-probe-controls.log: corrected core/process behavior and initial
  query controls pass. d99-effort-roundtrip.log fails because choice text is not current state.
- d99-effort-metadata-shape.log retains only field kinds and normalized values, establishing the
  separate effort metadata field. d99-effort-metadata-controls.log rejects incorrect state evidence.
- d99-effort-confirmed.log: high/low each acknowledged and observed, two setting calls/two metadata
  notifications, no duplicate setting or prompt, unchanged original settings and joined group
  cleanup (7.37s test, 8.650s race package).

These checks cover the idle command/state path. They do not establish provider reasoning behavior,
billed usage, every model/level, dynamic third-party changes or the other open alpha/release gates.

Final regression records: d99-repository-race.log passes all 27 tested packages with all live opt-ins
disabled; d99-vet.log passes whole-repository/fake-peer vet. d99-final-probe-controls.log passes
the final 64-event/64-KiB-frame/1-MiB-aggregate/parent-cancellation controls in 3.846s.
d99-final-kiro.log passes all three actual no-prompt cases in 21.702s: auto query 6.63s, selected query
6.55s, setting/metadata 7.24s, each with original settings preserved and the private root removed.
The D99 development binary and frozen effort inventory pass 139 checks; no new dependencies.
d99-final-cleanup.log verifies the final preserve-on-surviving-group guard: high/low readbacks,
joined ownership, root removal and source preservation pass (7.33s test, 9.351s race package).

## D100: a plain phrase derived from the fresh plugin result

The prior D72 result challenge failed its exact final-text condition after completing both tool
round trips. Its raw 33-byte answer was not retained, so no particular formatting cause is known.
The revised recipe reduces the instruction-following task without relaxing protocol or screen
evidence. The 132-byte owned prompt is:

```text
WaitForMcpServers {}, then mcp__plugin_dax-owned_owned__owned_probe {} once each. Reply exactly VERIFIED Y, using Y from the result.
```

Only the second tool's exact success text or matching hook-refusal reason contains a fresh thirteen-
character base32 Y. The expected 22-byte phrase is VERIFIED, one ASCII space, then Y. That whole
phrase is absent from the prompt and both tool results. It must appear in final assistant events
and independently on the current reconstructed screen. Neither token-only output, prefix-only output,
prompt echo, whole-phrase tool output, early complete phrase nor early token disclosure can pass.
Event-split matching remains supported; erased/raw terminal output is not substituted for the screen.
The refusal branch still requires zero native plugin calls and a matching error result. It verifies
client refusal delivery, not successful tool execution.

Independent controls first expose that an early standalone result token could pass the old guard
before tool delivery (d100-phrase-guard-red.log, 1.055s package). The guard now rejects that disclosure,
including across text events. The sixteen new allowance/refusal cases and existing sequence guards
pass in d100-phrase-guards.log (1.799s). The owned fake peer accepts the exact new fixed-prefix marker
in addition to the historical all-uppercase marker. The old actual-client/fake-ACP recipe remains
available as a regression; TestClaudePluginResultPhrase supplies the new rehearsal.

Before live model work, both native-client/fake-ACP allowance/refusal recipes and applicable local
checks must pass. Then TestKiroLivePluginRegistryReplacement admits one revised allowance and, only
if it succeeds, one revised hook refusal. A failure stops this sequence for diagnosis; no dispatched
case is retried in this recipe. Per case, the unchanged budget is three main Messages requests plus
at most one locally answered auxiliary request, two ACP prompts/processes, one wait and one owned
plugin request, native plugin calls one/zero, one recreation, 45-second original turn deadline,
20-second setup/first-event limits, 60-second client UI lifetime and three-minute total deadline.
Both source snapshots and all recorded processes, relay artifacts, listener and private client
configuration must pass cleanup checks. This does not authorize unrelated tools or global settings
changes, and does not change production behavior or D99's artifact.

The public-only local Claude consultation is saved and read at
.cache/claude-consult/work/public-completion-design-review-jymaj95u/answer.md (one turn, 5,757 stdout
bytes). Its fresh-phrase counterexamples inform the controls. Its claim that a deliberate refusal
branch must fail is rejected: allowance and refusal have separate effect/status expectations.
Claims about the likelihood of transport defects versus model formatting failures are unsupported
and not adopted. A phrase pass remains bounded data-path evidence, not general semantic correctness.

The first native rehearsal (d100-native-phrase-controls.log) keeps the original recipe green
(14.39s pair), but both new cases fail after the independent peer incorrectly halves the new marker.
The peer regression d100-fixture-token-red.log reproduces that newly introduced two-byte truncation;
d100-fixture-token-fixed.log passes in 1.734s after correcting only the fixture. The old D72 live
answer's cause remains unknown. d100-native-phrase-fixed.log then passes the new allowance/refusal
pair in 12.93s (14.347s race package): full phrases generated and displayed, three main requests,
two backend processes per case, effects one/zero, preserved sources and joined cleanup.
d100-local-controls.log passes observer controls in 1.302s. All live Kiro opt-ins remain disabled
during these rehearsals; their responses come from independently authored ACP fixtures.

d100-prelive-regressions.log passes ACP/interop/independent-peer race suites in
5.713s/24.225s/1.192s; d100-final-vet.log passes the applicable vet checks. The next actual Kiro
command is rejected by automatic approval review before process creation because explicit approval
for Kiro credit use and external transmission is absent. There is no d100-live-plugin-phrase.log
or D100 Kiro model invocation from that attempt. The concrete pending action is one allowance,
then only on success one hook refusal, at most four ACP prompts total under the per-case bounds
above. It sends synthetic test questions/results and default client instructions/tool definitions
to Kiro, with provider-dependent credit use. Do not treat preparation as approval or bypass the
review rejection. Production and the verified D99 artifact remain unchanged.

## D101: read-only account usage cache

`TestKiroPinnedUsageAdvertisement` and `TestKiroPinnedUsageQuery` in internal/interop establish the
owned session's usage advertisement and private result structure, retaining only shapes/classes
and counts. `TestKiroPinnedUsageCache` in internal/launcher checks the production cache/preparation
with the native Kiro binary opt-in and credit opt-in explicitly zero. It permits one refresh, one
ACP process and exactly session/new, tools and usage after initialization. A wrapper rejects other
commands, extra RPCs and every session/prompt. Finite version/helper/account checks and the usage
query use the existing login without logging identity. Claude is not launched; no credit-consuming
inference is allowed.

The refresh uses a fresh zero-tool/zero-MCP agent with resource suppression and isolated settings.
It must first observe matching-session command availability and an empty tools result. The cache
returns immediately, publishes reported CREDIT used/conditional-limit only after joined cleanup,
does not infer remaining and cannot recreate work after Close. Failure stops the observation.
Original Kiro settings are fingerprinted before/after; retained groups or cleanup failures preserve
the owned observation directory and fail the test. Amounts/private payloads are never printed.

Independent query/parser/lifecycle controls precede this native cache test. The completed native
case passes in 10.25s, with a 7.311s refresh, one process/three post-initialize RPCs, unchanged source
settings and removed owned files/group. Native Claude status rendering, nonempty supplemental
buckets and other versions remain separate. This read-only action does not authorize or bypass
D100's rejected plugin model experiment.

## D102: personal-rules alias exclusion controls without Kiro inference

The temporary strict TestClaudePersonalRuleAliasExclusionPreservation comparison first fails in
1.45s (2.514s race package), retained in d102-rule-alias-preservation.log. The user-level pattern
**/client/rules/general.md does not match the natural source, but suppresses the prepared alias's
rule body and all four relative-import hops. Project rules/imports remain and both local requests
complete. The retained named TestClaudePersonalRuleAliasExclusionCounterfactual explicitly asserts
this unresolved defect; it is not a preservation acceptance test.

Its final three-scope matrix includes natural/prepared alias-only exclusions and natural/prepared
original-path exclusions for user, project and local settings: twelve native client invocations,
one authenticated local synthetic Messages request each, no client tool effect, empty MCP config
and disabled hooks. Observe only fixed authored markers, order/depth/counts and lifecycle facts,
never raw client context or source content. Prepared source/import fingerprints, observed process
groups and private profile removal must pass. The matrix passes in 6.08s (8.194s package), recorded
in d102-rule-alias-counterfactual.log. No Kiro process, model credit use or external inference occurs
in these controls. The separate public-only Claude consultation has one turn and 6,346 output bytes;
its answer and assessment are retained under public-memory-native-scope-review-dqfm0xka.

d102-personal-scope-regressions.log passes existing native rule/import-depth/alias-scope controls
in 12.709s. d102-interop-race.log passes the opt-ins-off suite in 26.279s and d102-interop-vet.log
passes vet. Formatting/whitespace and D101's unchanged 141 artifact checks pass. No new product
adapter is enabled; the original strict comparison remains failed evidence of an open requirement.

## D103: account usage in the native Claude status line

`TestClaudeUsageStatusWithBoundedRefresh` first exercises complete, failed-refresh and cancel-held
cache episodes in the real Claude UI with a local synthetic backend and no Kiro binary. A held
query must not prevent the cold view or five-second polls. After synthetic values render, advancing
the fixture clock lets the next UI poll admit one failure; the same values must remain visibly
stale. Closing with the first query still held must join it without publishing data. Current-screen
verdict controls reject partial/incorrect/erased output and inferred remaining. The three native-UI
cases pass in 27.45s (28.730s race package); the tightened next-poll failure check passes in 13.08s
(15.127s package including eleven verdict controls).

`TestKiroUsageVisibleInClaudeStatus` requires both pinned CLI paths and credit opt-in exactly zero.
It permits one production-cache refresh, using only the existing finite version/account checks
and the isolated session/new, tools and usage path. Claude receives no user prompt, has no tools
or MCP servers and routes only to the owned local gateway. A synthetic last-turn record anchors
the display label. Require the current screen's exact normalized used/limit fields after its cold
view, regular polling, zero model requests and full recorded cleanup. Do not save actual amounts,
terminal output, account identities or private response text. Source settings are fingerprinted;
failed usage joins preserve their separate owned root.

The native case passes in 14.94s (17.240s race package), recorded in d103-native-usage-ui.log.
It observes three status requests, one ACP process, four recorded finite main-CLI checks, zero
model requests and unchanged sources; the profile, usage files and recorded groups are removed.
No credit-consuming inference is run. This check does not authorize the pending D100 plugin
experiment or establish unobserved supplemental account semantics or release clearance.

d103-status-ui-regressions.log passes all five existing-status scopes, startup ordering, default
status, two local completion notices and the tightened held-cancellation assertion in 53.720s.
d103-interop-race.log passes the opt-ins-off suite in 25.608s; d103-interop-vet.log exits zero.
Formatting/whitespace and all 141 D101 artifact checks pass. No production or dependency change
is made by this UI evidence work.

## D104: two concurrent native histories with independent ACP

`TestClaudeConcurrentNativeHistoryThroughGatewayAndACP` opts into the pinned Claude only. Both
sessions use the same owned HOME/project with hooks/auto-memory disabled and empty tools/MCP.
The real authenticated gateway, session manager and independent history ACP peer handle four
local text turns. No Kiro binary, external inference or credit use is admitted by this plan.

Before releasing each round's first response, require distinct native session IDs and four live
client/ACP group leaders, observed after model requests and ACP events. Both initial owners must
join before either explicit-ID resume constructs its new profile, endpoint or credential. Require
the resumed public request's own seed/question, answer and new question once and in order, with
no sibling seed in system or messages. After both rounds, all eight recorded groups and four
profiles/listeners must be gone. Inspect only bounded disposable native files for the two known
transcript names, own/foreign authored markers and absent routing tokens; never decode or edit
native transcript formats. Preserve original owned source settings and log no raw content.

Eleven history-projection and eight overlap-witness controls pass first in 2.060s, recorded in
d104-concurrent-history-guards.log. The native two-session/four-turn case passes in 4.46s (5.903s
race package), recorded in d104-native-concurrent-history.log. Both overlap rounds, retained
transcripts, fresh routing/profile ownership, source preservation and cleanup pass. This finite
same-project/distinct-session result leaves same-session writers, tools, media, interactive
selection, actual Kiro concurrency and long-duration retention as separate requirements.

d104-native-history-regressions.log passes the existing native text restart and four persistence
controls in 7.669s. d104-interop-race.log passes the opt-ins-off suite, including cancelled waiting
for a missing peer, in 28.514s. d104-interop-vet.log exits zero; formatting/whitespace and all 141
unchanged D101 artifact checks pass. No production or dependency modification is introduced.

## D105: same-ID transcript retention and readback context

`TestClaudeSharedSessionConcurrentResumeObservation` uses the pinned Claude with local controlled
text backends through authenticated gateways. It creates disposable synthetic history only; no
Kiro binary, actual user conversation, external inference or credit use is part of the matrix.
The ordinary-config reference and prepared profile use identical routing overlays and prompts.
Each of four schedules runs once in each configuration: sequential A/B, sequential B/A, overlapping
writers with A finishing first, and overlapping writers with B finishing first. Each episode has
one initial turn, two same-ID writer turns and one later readback, with fresh profiles/endpoints/
tokens and joined owners at the required boundaries.

In overlapping cases, hold A's response and observe its pending question on disk before starting B.
Require both clients live and both responses held. Release the first designated reply, join its
client/profile/listener, verify the second remains held and then finish it. Readback starts only
after both complete. Inspect fixed marker roles/order in public requests and known marker/secret
presence in bounded native files; never decode native transcript formats or log raw contents.

d105-shared-history-guards.log passes thirteen independent projection controls in 2.099s. The
native matrix in d105-native-shared-session.log passes in 16.02s (17.435s race package): 32 local
turns, eight readbacks in four matching reference/prepared pairs and all recorded ownership removed.
Sequential readback includes both answers. Concurrent readback includes only one completed branch even though
both replies remain on disk; see D105's exact schedule table. Prepared source settings stay
unchanged; native reference global-config changes stay within disposable roots. No routing secret
is retained. This is not an implicit merge/isolation guarantee or a universal selection policy.

The public-only consultation answer and assessment are saved under
public-shared-session-review-tn4632np. One successful turn returns 4,464 bytes; no repository input
is sent. No production behavior changes or previously rejected model experiment is introduced.

d105-interop-race.log passes the opt-ins-off suite in 28.339s; d105-interop-vet.log exits zero.
Formatting/whitespace and all 141 unchanged D101 artifact checks pass, without release clearance.

## D106: interactive new-tool policy after completed-history resume

`TestClaudeResumedInteractiveToolPolicyWithFakeACP` requires the pinned native Claude only. Keep
`DAX_INTEROP_KIRO_CREDIT_OPT_IN=0` and the Kiro binary unset. Every answer comes from the independently
authored ACP peer through the production gateway, session manager and relay. Disposable native
history is shared between a print initial launch and a fresh explicit-ID terminal resume; no
external inference, actual user history or model credit is used.

Each of three episodes first completes one configured-allowed append and joins its ownership.
The fresh resumed client receives a different append under manual permissions. Test one-time Yes,
No with a verified comment, and a PreToolUse veto without decision input. Require old effect/hooks
once, old public pair exact in both resumed requests, distinct new call ownership and matching
success/refusal. Check current operation/selected option and delivered handoff before input, and
require current final display before controlled group termination and joined cleanup. The terminal
deadline is 25 seconds and capture is bounded to 256 KiB. Fixed observations only are logged.

d106-resumed-permission-guards.log passes new handoff/title controls with existing history and
permission controls in 2.166s. d106-native-resumed-allow.log passes one-time approval in 9.45s
(10.745s race package); d106-native-resumed-refusals.log passes comment refusal and hook veto in
10.45s (11.736s package). All six stages pass exact history, effects, source-policy preservation
and removal of recorded processes/groups, profiles, listeners and relay artifacts. No new standing
project permission is saved. No auxiliary title request is observed; its separate local budget
cannot consume main-turn evidence.

These checks cover completed-history Bash with independent ACP. Actual Kiro interactive resume,
pending-tool approval, bare denial after resume, keyboard exit and broad crash/duplicate-effect
guarantees remain separate. There is no production/dependency change or new live-model admission.

d106-native-resume-regressions.log passes existing completed-tool, configured-policy and interrupted-
policy native controls in 52.792s. d106-interop-race.log passes the opt-ins-off suite in 23.899s;
d106-interop-vet.log exits zero. Formatting/whitespace and all 141 unchanged D101 artifact checks
pass, without release clearance.

## D107: interactive new-tool policy after an interrupted history

`TestClaudeInterruptedResumedInteractiveToolPolicyWithFakeACP` uses pinned native Claude and the
existing independent ACP peer. Keep Kiro opt-in zero and its binary unset. Create disposable
no-text and partial-text histories, each interrupted at the exact delivered native PreToolUse
wait. Require repeated cancellation, joined old ownership and no effect after late hook release
before launching an explicit-ID terminal resume with fresh profile, endpoint, token and ACP owner.

For each history, verify one-time Bash approval, refusal with an echoed comment, and native hook
veto. The old operation remains unexecuted; its exact original question and partial text or native
placeholder must precede the new question in both resumed requests. No unfinished tool pair is
fabricated. Before any decision, check the expected interrupted-history proof, current distinct
handoff/command, joined old hook and absence of old/new effects. A veto gets no decision input.
Require the matching new result, visible completion and joined cleanup with unchanged sources.
The terminal lifetime is 25 seconds; capture stays bounded to 256 KiB and is not logged raw.

d107-interrupted-permission-guards.log passes thirty delivery/history-kind controls with existing
history, refusal and title controls in 2.200s. d107-native-interrupted-allow.log passes no-text
history approval in 9.93s (11.206s race package); d107-native-interrupted-refusals.log passes its
comment refusal and hook veto in 11.82s (13.185s package). d107-native-partial-permissions.log passes
all three partial-text cases in 17.53s (18.830s package). All six episodes/twelve stages preserve
old history, absent old effects, new effect/result ownership, source policies and joined cleanup.
Two new appends execute once; four new refusals leave no effect or successful hook. Title count
is zero. These observations do not establish actual Kiro interactive resume, unfinished-operation
retry, bare refusal, keyboard exit or uncertain side-effect windows. No production change occurs.

d107-native-resume-regressions.log passes the completed-history interactive and interrupted-history
configured-policy controls in 47.631s. d107-interop-race.log passes the opt-ins-off suite in 24.430s;
d107-interop-vet.log exits zero. Formatting/whitespace and all 141 unchanged D101 artifact checks
pass, without release clearance.

## D108: personal output-style preservation without external inference

Use pinned Claude 2.1.263 with a local authenticated text backend, no Kiro process and an owned
HOME/project. Before production changes, reproduce a selected personal style present in natural
system instructions but absent from the prepared request despite a successful reply. Add the style
tree to the existing bounded private asset snapshot; preserve its metadata and native user scope.

Run six natural/prepared pairs: personal selection, keep-coding declaration, same-name project
definition, project override, local override and Default. Give each arm fresh profile/credentials,
one request and no client tool effect. Check selected authored markers only in system instructions,
project memory only in user content and exact selected-block digest equality against that case's
reference. Digests stay in memory; do not record raw requests, client instructions or credentials.
Each child has a 20-second deadline and 128 KiB output cap, within a three-minute matrix deadline.
Require exact fixed completion, source style/settings/project preservation, listener/profile removal
and joined recorded client PID/group. Prepared execution must also leave the whole source config
tree and global state unchanged; reference-only native metadata changes are not product mutations.

d108-missing-style-regression.log fails the initial asset test; d108-native-style-missing.log
separately fails the native missing-style control in 2.323s. d108-style-assets.log passes source
snapshot/safety controls in 6.460s, including twelve output-style cases. d108-style-projection-final.log
passes eight role guards in 2.029s. d108-native-style-final.log passes all twelve native cells in
5.79s (7.873s race package). A preliminary MCP argument-order startup failure and a rejected assumed
keep-coding size increase are recorded separately, not counted as preservation results. Both field
values give the same observed native system shape here; preserve that observation's limits.

The public-only CLI consultation answer and assessment are retained locally. No repository data
or native requests are sent to that consultation. Personal CLAUDE.md and rule-alias exclusion gaps
stay open, as do later-turn/UI, plugin/managed and actual Kiro style checks. This change does not
dispatch D100's pending external-model experiment or change development-launch admission.

d108-native-assets-regression.log passes existing personal customizations, rule sources, path
characters and import depth in 26.045s. d108-all-race.log passes all 27 tested repository packages
with native opt-ins disabled; six packages have no tests. d108-all-vet.log exits zero. Formatting,
whitespace and all 141 D108 output-style development-artifact checks pass, with release clearance
false and D101's previous executable retained unchanged.

## D109: native image result through relay and restart

Generate a 12-by-9 PNG in an owned project and use pinned Claude 2.1.263 with independent ACP/MCP
peers and the real gateway, validator, session manager and relay. No Kiro process or external model
request is used. The first peer requests one Read, checks the native image result and completes.
Observe original pixel/byte identity and exact call ID/status at HTTP; require the same bytes and
pixels at MCP. Source settings and native PreToolUse/PostToolUse receipts must stay unchanged/once.

Join the initial client/ACP/relay and remove its private profile/listener. Delete the original PNG,
then resume the same native ID through fresh resources with an explicitly non-executing question.
Require the exact completed image result in public HTTP and its native image in the ACP prompt.
The independent resumed peer checks the original Read ID/input, successful result envelope, ordered
content markers and native image binding, and makes no tool request. The source stays absent and
both hooks stay once. Neither peer reads the image file or native transcript. The test generates
all inputs; full requests, result bytes and native transcript formats are never logged or parsed.

d109-tool-image-before.log fails the initial projection in 0.928s. d109-native-image-before.log
reproduces successful first delivery and zero resumed ACP images in 6.53s (6.832s race package).
d109-native-image-after.log passes both stages after the fix in 5.77s (7.522s package).
d109-image-guards.log passes twelve request provenance controls in 2.108s. d109-projection.log
passes ownership/order/capability and malformed/oversized/opaque-result controls in 11.100s.
d109-native-regressions.log passes image resume plus existing native text and completed-tool history
in 18.465s. A stronger final ACP witness separately checks exact first-result bytes and resumed
call/status/content boundaries. Limits are 30 seconds/128 KiB per client, 25 seconds/1 MiB frames
per ACP peer and 90 seconds overall. The consultation answer and reviewed corrections are saved
locally. Actual Kiro image interpretation, other native formats, sidecars and full release remain open.

## D119: plugin skill content and hooks against the actual Kiro backend

Use the unmodified measured pair (Kiro 2.21.3, Claude Code 2.1.267) with the real gateway,
validator, session manager and relay in front of the actual restricted Kiro process: a private
KIRO_HOME with default-resource suppression, a relay-only agent, the `auto` model, a 45-second turn
limit and at most one recreation. Each run generates a fresh 26-character marker that the prompt
never contains. In the skill mode the owned plugin's skill body instructs the model to reply with
exactly that marker and the prompt asks it to invoke the skill by name through the Skill tool; in
the hooks mode the prompt itself asks for the marker and the plugin's SessionStart and Stop hooks
are the subject. The observing HTTP server validates the client's requests before forwarding them:
the advertised Skill schema accepts the owned invocation, the first request lists the skill without
its body, and the second carries exactly one matching result and the client-expanded body as
separate text. Title requests are answered locally. Budget three model turns; do not retry a failed
episode.

```sh
umask 077
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
  DAX_INTEROP_KIRO_BINARY=/Users/geunwooshim/.local/bin/kiro-cli \
  DAX_INTEROP_CLAUDE_BINARY=/Users/geunwooshim/.local/bin/claude \
  go test -count=1 -timeout 10m \
  -run '^TestKiroLivePluginSkillContent$|^TestKiroLivePluginHooksAroundTurn$' -v ./internal/interop
```

Both passed at the first attempt on 2026-09-11. Skill mode (17.99s case, 34.84s test): the model
calls the advertised Skill tool, the client expands the skill, the second turn's answer contains the
marker, two backend processes are prepared and cleaned in order, the startup hook's context is
present in the model's request, and the SessionStart and Stop hooks each run once. Hooks mode (8.34s
case, 24.87s test): one turn returns the marker with the same hook and cleanup facts. 60.800s
package, `d119-live-plugin-assets.log`; the fixture-backed plugin controls pass unchanged
(`d119-plugin-assets-regression.log`). Limits: a local, independently authored marketplace and
plugin; remote marketplaces on the actual backend, Kiro-side skills and agents, mid-session plugin
enable/disable and the interactive MCP-panel path remain separate. Test-only; the D118 artifact
remains current.

## D120: twenty-turn live soak of one session

Use the compiled `run` observer's `soak-turns` mode with the actual Kiro 2.21.3 and Claude Code
2.1.267 through the real launcher. After the ordinary first turn the observer types twenty numbered
questions one at a time, each admitted by the parent before the next, and after every completed turn
samples the owned proxy process (resident size, open descriptors, process-group size) and the
backend ACP process group (resident size, process count) through finite system commands. Live turns
are allowed 45 seconds each, the terminal lifetime grows by fifteen seconds per turn, and the
terminal peer's frame and byte budgets scale with the declared turn count. Validity after a
three-turn warm-up: proxy resident growth within 32 MiB, descriptors within four, the same owned
process count, the same backend process count and backend resident growth within a first declared
256 MiB envelope. Budget 21 model turns.

```sh
umask 077
DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
  DAX_INTEROP_KIRO_BINARY=/Users/geunwooshim/.local/bin/kiro-cli \
  DAX_INTEROP_CLAUDE_BINARY=/Users/geunwooshim/.local/bin/claude \
  go test -count=1 -timeout 30m \
  -run '^TestKiroLiveCompiledRunSoakTurns$' -v ./internal/interop
```

A first attempt on 2026-09-11 stopped at nineteen turns at the peer's fixed 1,024-frame budget,
having sampled the proxy at 16,288→16,528 KiB and the four-process backend group at 101,856→94,560
KiB with a 105,760 KiB peak (`d120-live-soak-19-frame-budget.log`). The rerun with scaled budgets
passes twenty turns in 59.55s (59.863s package): proxy 17,776→18,096 KiB resident (peak 18,384 KiB),
descriptors 24→22, two owned processes throughout; backend group four processes throughout,
319,568→106,560 KiB resident (peak 320,080 KiB), a warm-up transient with an unmeasured cause that
falls to a third by the end (`d120-live-soak-20.log`). Limits: one session of twenty turns; a longer
live soak and a combined concurrency-plus-soak run remain open. Test-only; the D118 artifact remains
current.

## D134: explicit MCP request timeout (prepared, not yet run)

HANDOFF item 13 requires the relay's Kiro wait to match the configured client tool wait. The
agent currently omits MCP `timeout`; premature expiry during actual client approval has not been
reproduced. This experiment determines whether that field governs `tools/call` on Kiro 2.21.3.
It does not complete the client approval, hook or cancellation gates.

Prior owned, no-prompt observations found that the native configuration writer preserves 1500,
900000 and 3600000 as numeric `timeout`, while omission leaves it absent. Three-second MCP
initialize/list responses still succeed with `timeout: 1500`. Neither finding establishes a
request timeout or its default. Current CLI 3 configuration documentation cannot establish CLI 2
semantics. The fixed observations are retained in `d134-mcp-config.log` and
`d134-mcp-timing-after.log` under `.cache/history-review`.

`TestKiroLiveMCPRequestTimeoutObservation` admits two sequential fresh ACP sessions, with
`timeout: 1500` then `timeout: 8000`. Each advertises and allows only the independently authored
`@owned_timing/owned_wait` MCP tool. Its empty-input call returns fixed text after three seconds
and performs no client file, shell or network effect. The exact prompt in both cases is:

> Independent protocol timing exercise. Call owned_wait exactly once with an empty object.
> Do not use another tool, inspect files or configuration, or retry.
> After the call succeeds or fails, finish with a short statement of its status.

Each peer accepts at most one tool call. A failed or inconclusive first case prevents the second
prompt; this invocation never retries. Both installed executables must report exactly 2.21.3.
Only an advertised `auto` model is selected. Existing Kiro authentication is used normally, with
an owned workspace, isolated KIRO_HOME, default-resource suppression, empty resources/hooks and
no inherited MCP configuration. The observer advertises no client execution capabilities and
denies permission requests. Claude, user projects and existing conversation data are not inputs.

Each episode has a 70-second deadline including version checks, setup and observation. Each
version check is bounded at five seconds, setup at twenty seconds, inventory advertisement at
five seconds, model selection at five seconds and the original prompt at 45 seconds. Four
seconds of observation after its response remain within that original deadline. The independent
peer has 32 input frames of at most 64 KiB, sixteen request IDs, 96 witness records, a 70-second
context and a 75-second outer exit bound. The observer admits 256 notifications, 64 KiB per
notification and 1 MiB total. Every owned group is closed and checked before the next episode.

Acceptance requires exactly one MCP call and one correlated ACP tool call: a failed status
1.0–2.2 seconds after receipt in the short case, and a completed status after the three-second
reply in the long case. Matching/foreign/repeated cancellation and a deliberate late reply are
recorded distinctly. Changing a failed ACP status to completed after the late reply rejects the
observation. Timings use local timestamps with a 100-ms clock-drift check; no exact scheduler
timing is claimed. Missing native status, extra calls, changed configuration, unmatched evidence
or incomplete cleanup are inconclusive, not success. Model prose is never timeout evidence.

Local race controls pass in `d134-timing-local-controls.log`: the observer in 2.192s and the
independent peer in 1.609s. They cover correlation, cancellation, delayed/late responses, extra
calls, malformed/excess input, deadlines and ambiguous evidence. The live test skips with credit
opt-in zero. An initial peer control found and fixed its null-ID parser before these passes;
that fixture defect is not a product finding. The complete interop race suite and peer controls
also pass in 26.575s and 1.396s (`d134-interop-race.log`); focused vet exits zero
(`d134-interop-vet.log`). All installed-client and Kiro opt-ins remain off.
These controls exercise the peer over an in-memory pipe and the ACP observer with independent
event sequences. They do not execute the new peer's process entry point, its blocked-output
outer exit timer or the complete native ACP/MCP call combination. Earlier no-prompt process
observations used the separate prototype; they are not an execution of this prepared harness.

The following single command requires new explicit per-run approval. It may send up to two ACP
prompts to the Kiro service and consume credits; ACP prompt count is not a billed-call or cost
bound. No approval or actual model result is recorded for D134 yet.

```sh
umask 077
GOTOOLCHAIN=go1.27.1 GOMODCACHE="$PWD/.cache/gomod" GOCACHE="$PWD/.cache/gobuild" \
  DAX_INTEROP_CLAUDE_BINARY= \
  DAX_INTEROP_KIRO_BINARY=/Users/geunwooshim/.local/bin/kiro-cli \
  DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 \
  go test ./internal/interop -run '^TestKiroLiveMCPRequestTimeoutObservation$' \
  -count=1 -v -timeout 4m > .cache/history-review/d134-live-mcp-timeout.log 2>&1
```
