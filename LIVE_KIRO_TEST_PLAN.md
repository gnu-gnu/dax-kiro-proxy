# One-turn Kiro/client denial experiment

Prepared: 2026-09-09. Status: actual Kiro execution awaits explicit opt-in; no Kiro model prompt has
been sent. This is an interoperability experiment, not a release or an override of the product's
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

The experiment verifies Kiro CLI 2.21.1 and its adjacent helper, and Claude Code 2.1.263. Kiro uses its
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
This rehearsal used fake ACP and Kiro credit opt-in 0. Actual Kiro authorization remains pending.
