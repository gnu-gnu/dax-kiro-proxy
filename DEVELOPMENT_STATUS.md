# Development status and acceptance evidence

Last updated: 2026-09-08. Overall objective: complete the standalone Go Kiro ACP proxy and launcher
contract described in PRODUCT_SPEC.md, including the full acceptance/release gates. This document
does not redefine completion around an intermediate phase.

## Current evidence

- All twelve pre-implementation Markdown documents read in README order.
- Specification-only baseline committed as `7b108dd`.
- Go selected by explicit user instruction; comparative experiments remain unmeasured.
- Local host: macOS 15.4 (24E248), arm64; Go 1.27.1 downloaded into the ignored repository cache.
- Public CLI black-box version/help checks: Kiro 2.21.1 and Claude Code 2.1.263; no model request yet.
- Phase 1 tests first failed because implementation packages were absent, then passed after the new
  transport was implemented. Additional error-envelope/correlation/notification regression tests
  reproduced five failures before their fixes.
- Passed `go test -race ./internal/acp ./internal/kiroauth ./internal/ndjson` (ACP 6.759s on final run).
- Passed `go vet ./...` and `git diff --check`.
- Passed `go test ./internal/ndjson -run '^$' -fuzz FuzzObject -fuzztime=5s -parallel=2`:
  74,534 fuzz executions, no failing input; runtime 6.647s.
- Through Phase 3, `go list -m all` reported only this module; Phase 4 dependencies are recorded below.
- Phase 2 independent HTTP and request-validation tests were written before their adapters. The
  session test suite first failed because no implementation existed. An additional encoded-output
  overflow test reproduced an incomplete SSE sequence before the terminal-error fix.
- Passed `go test -race ./...` after integrating the fake ACP text backend: ACP 5.597s, session/HTTP
  integration 3.472s, gateway and parser suites passed (cached on this final integration run).
- The gateway race suite separately passed in 1.971s with loopback socket permission. It tests exact
  route credentials, malformed/body-size validation, SSE equivalence, early/late auth markers,
  first/total deadlines, output-byte limits, request admission, disconnects and failed writes.
- Phase 3 tests first failed on absent catalog/cache/effort/private-file APIs. Regression tests then
  reproduced contradictory model-selection acceptance, setup shortened by the turn timeout, cached
  effort status blocked by an RPC, and a trailing-separator directory-symlink bypass before fixes.
- Passed uncached `go test -race -count=1 ./...`: ACP 6.075s, gateway 2.110s, session 4.732s,
  catalog 1.878s, effort 2.427s, private files 2.877s; all other packages passed. Use `-count=1` for
  fixture changes because TestMain invokes an external build of the independent fake child.
- Passed `go vet ./...` after the Phase 3 implementation.

Commands use `GOTOOLCHAIN=go1.27.1`, `GOMODCACHE="$PWD/.cache/gomod"` and
`GOCACHE="$PWD/.cache/gobuild"` in this sandbox. No performance comparison with Rust is claimed.

## Phase 1 acceptance mapping

| Acceptance A/G behavior | Executable evidence |
| --- | --- |
| Version negotiation and rejection | TestInitializeAndVersionRejection |
| Concurrent reversed responses and notifications | TestConcurrentOutOfOrderAndNotification |
| Invalid frames/IDs/envelopes, truncated EOF, process exit; all waiters fail | TestMalformedFramesFailEveryWaiter, TestErrorEnvelopeTypesAndCorrelationPrecedeClassification, TestDuplicateResponseRetires |
| 8 MiB boundaries, duplicate keys, UTF-8/depth, fragmented framing | TestFramingBoundaries, TestStrictJSONObjects, TestDefaultFrameCeiling, FuzzObject |
| Oversize output, blocked pipe and explicit deadlines | TestOutboundLimitAndStalledPipe, TestConfiguredDeadlineWithoutCallerDeadline, TestCancellationAndTimeoutsRetire |
| Agent requests rejected; notifications never answered; directional IDs | TestAgentRequestsNeverAuthorizeEffects, TestJSONValueSemanticsAndAgentDirections |
| Credential redaction and bounded stderr, conservative auth errors | TestStderrRedactionAndTruncation, TestFailurePrivacyAndAuth, TestAccountExpiryBoundary |
| Whole group shutdown including surviving descendants and repeated callers | TestWholeProcessGroupShutdown (cooperative, stubborn, leader-exits; 8 concurrent Close calls) |
| Queue/pending bounds and clean child environment | TestPendingCountAndEventByteBudgets, TestQueueOverflowAndNoEnvironmentInheritance |

The independent child is `internal/acp/testdata/fake/main.go`; its provenance manifest records public
source documents. These are fake-process acceptance results, not live Kiro conformance or release
readiness. Load repetition, broader fuzzing and live gates remain in Phase 7.

## Phase 2 acceptance mapping

| Acceptance B/C/G behavior | Executable evidence |
| --- | --- |
| Exact model/UI route scopes, bearer/API key handling, duplicate/conflicting credentials | TestRouteAuthentication, TestDuplicateCredentialHeadersAndStartFailure |
| Loopback policy and request shape/body limit | TestLoopbackBindPolicy, TestHTTPValidationBeforeBackend, TestRequestValidationAndOrderedContent |
| Equivalent buffered/SSE text and zero provider usage | TestTextResponseAndExactSSE, TestHTTPThroughIndependentACPProcess |
| Early/late authentication fallback and safe ordinary errors | TestEarlyAndLateAuthenticationFallback, TestBackendFailureTimingAndPrivacy |
| Disconnect before first event/during stream, request/write/output limits | TestActiveRequestLimitAndDisconnectBeforeFirstEvent, TestDisconnectCancelsStreamingTurn, TestResponseWriteFailureCancels, TestEncodedOutputBudgetAndTerminalStreamError |
| Distinct first/total deadlines through a subprocess | TestFirstAndTotalDeadlineDiagnostics, TestHTTPThroughIndependentACPProcess |
| Prompt order and response barrier, wrong-session/canceled state disposal | TestOrderedPromptProjection, TestPublicTextPathAndOrderedCompletion, TestUnsafeStateIsDiscarded, TestCancellationAndSingleSessionAdmission |

These pass against independent fake processes. Model discovery/selection, actual session reuse,
relay/media/native web, subprocess environment/profile integration, and the launcher remain later
phase requirements. There is no executable live Kiro entry point yet. The initial text driver creates
fresh state for subsequent full-history requests in its original Phase 2 version. Phase 5 now adds
the reconciliation and sharing evidence below.

## Phase 3 acceptance mapping

| Acceptance D and cache/security behavior | Executable evidence |
| --- | --- |
| Unique, deterministic model IDs; strict reverse lookup; include omitted current model | TestStableCatalogMappingAndValidation, TestDerivedAliasCollisionIsRejected |
| Display-only multiplier and validated legacy/public model selectors | TestCreditMultiplierIsOnlyDisplayMetadata, TestSessionCatalogWireShapes |
| Selection before effort before prompt; inconsistent/unavailable selection rejects | TestModelSelectionThenOptionalEffortThenPrompt, TestUnknownModelAndInconsistentSelectionAreRejected |
| Initial configuration applies only through first completed turn | TestConfiguredInitialModelWinsOnlyFirstCompletedTurn |
| Auto/unsupported/unavailable/unknown effort behavior and rejected-probe ledger | TestEffortAvailabilityAndAutomaticModel, TestRejectedProbeIsNotRepeatedAndTransportRemainsFatal, TestAutoAndRepeatedRejectedEffortInProcessPath |
| Model-switch effective-state reset; optional rejection versus broken transport | TestEffortSuccessAndSwitchOrdering, TestBrokenTransportDuringEffortRemainsFatal |
| Independent setup/turn time and immediate status reads | TestSetupAndOwnedTurnHaveIndependentDeadlines, TestEffortStatusDoesNotWaitForPrivateCommand |
| Fresh/stale catalog identities, single-flight refresh, failure/backoff, last model scope | TestCatalogCacheCoalescesAndUsesCompatibleDiskState, TestStaleCatalogReturnsImmediatelyAndFailedRefreshPreservesData, TestFailedCatalogRefreshRetainsLastGoodDataAndBacksOff, TestLastInteractiveModelIsSeparateAndStrictlyValidated |
| Atomic owner-only files, bounded reads and link/special-file rejection | TestOwnerOnlyAtomicFiles, TestSymlinkPermissionsAndSpecialFilesAreRejected |

The last-model/cache policy modules pass independently; final launcher integration remains Phase 6.
Private effort wire shapes are explicit specification interpretations pending real Kiro validation
(decision D09). The unmodified client model-selector UI and all live release behavior remain unverified.

## Phase 4 implementation evidence

Schema-worker, registry, broker, socket, MCP child, mixed HTTP block and session-continuation tests
were written before their respective implementation and initially failed on absent APIs/behavior.
An additional regression reproduced a completed call's timer incorrectly canceling a newer batch;
the timer now checks its still-pending ownership before retiring a relay. Correlated auth expiry wins
over a racing relay disconnect, and notifications are drained again at the prompt completion barrier.

- Passed `go test -race -count=1 ./internal/toolregistry ./internal/schemacheck`: deterministic
  collision extension and registry ownership; real worker Draft 2020-12/numeric/retrieval/deadline cases.
- Passed `go test -race -count=1 ./internal/relay/...`: sealed batch atomicity, cross-owner/repeated/
  partial/late results, byte/admission limits, timeout/cancel, private file modes and cleanup; real
  MCP child lifecycle, suspended call plus ping, exact whitelist and cancellation.
- Passed `go test -race -count=1 ./internal/gateway ./internal/anthropic`: mixed text/tool event order,
  buffered/SSE content, exact large-number arguments, no partial/undeclared tool block and explicit
  rejection of unsupported tool-choice restrictions, plus the existing auth/deadline suites.
- Passed `go test -race -count=1 ./internal/session` in 6.188s: actual gateway → independent fake ACP
  → MCP child → private socket → matching client result, in both response modes; only one ACP prompt
  spans the round trip. Includes preserved tool error/output, ignored duplicate diagnostic tool
  updates, HTTP completion versus cancellation, no-consumer tool expiry, model/result replay refusal
  and graceful auth expiry while no HTTP response is open.
- Schema runtime dependencies are jsonschema v6.0.3 (Apache-2.0) and x/text v0.41.0 (BSD-3-Clause).
  `go mod tidy` and the compiled application/test graph are recorded in DEPENDENCY_REVIEW.md.
- Passed full uncached `go test -race -count=1 ./...`: ACP 6.689s, gateway 3.251s, broker 3.007s,
  MCP child 5.690s, schema worker 6.010s and session 8.122s; all other packages passed. After adding
  the original-HTTP-deadline regression, the session race suite passed again in 6.434s.
- Passed `go vet ./...` and `git diff --check`. Control-frame fuzzing passed 265,734 executions
  in 6.442s (`-fuzztime=5s -parallel=2`), with no failing input.

Black-box Kiro 2.21.1 configuration evidence remains limited: an independently authored temporary
profile with empty tools, empty MCP servers/resources, no hooks and `includeMcpJson: false` returned
exit 0 from `agent validate`. The default agent creation behavior advertised all tools (`*`) and
included inherited MCP configuration. The create command opened an editor; the owned editor/process
was terminated, and later probes suppressed editor invocation and used bounded process groups.
The later D27 negative control shows this exit status cannot prove syntax acceptance either. No
model request or client tool effect
has been run. Real Kiro restricted-profile/negotiation proof (R06), actual Claude model UI (R14), and
credit-consuming live opt-in gates remain open.

## Phase 5 implementation evidence

History, multi-session fake-process, manager and persistence tests were written before implementation
and first failed on missing behavior/APIs. A later regression reproduced capacity rejection despite
an available wholly idle process; admission now retires that idle group and retries once. Shared
deadline timers also reproduced an incorrect generic HTTP failure; diagnostics check the original
absolute deadline as well as context cancellation.

| Acceptance F/G behavior | Executable evidence |
| --- | --- |
| Main turns 1-3 reuse and only new deltas; safe truncation; duplicate/divergent recreation | TestThreeTurnsAndProvenTruncationSendOnlyNewContent, TestThreeTurnReuseTruncationAndDuplicateRecreation |
| Key order/cache hints, exact large numbers, changed system/assistant anchors | TestCanonicalizationPreservesArgumentNumbersAndSystemContext, TestDuplicateDivergenceAndUnprovenAssistantOnlyOverlap |
| Combined title classifier and concurrent title/main sessions | TestTitleRequiresAllSignals, TestManagerIsolatesTitleAndMainAndPreservesThreeTurns |
| Independent keys share compatible processes; active key/capacity restrictions | TestIndependentClientIDsShareCompatibleProcessAndActiveKeysStayBounded |
| Temporary creation owner, established routing, concurrent RPC/event ordering | TestEarlyCreationRoutingAndConcurrentResponseBarriers |
| Ambiguous creation and shared crash fail all attached waiters | TestAmbiguousCreationAndCrashInvalidateAllAttachedSessions |
| Compatibility, pool limits, idle TTL and repeated shutdown | TestCapacityCompatibilityAndIdleExpiry, TestIdleCapacityCanBeRecycledWithoutEvictingActiveOwners |
| Load replay suppressed and foreign session requests rejected | TestLoadReplayAndWrongSessionRequestsAreRejected |
| Strict persistent extension, null response, failed/mismatched/unsupported load and fresh fallback | TestPersistentLoadDiscardReplayAndInvalidateBeforePrompt |
| Private fixed-slot records, stable keyed digests, exclusive ownership, no raw conversation data | TestMetadataOnlyRecordsStableKeyAndHeldOwnership, TestFixedSlotsBoundStorageAndCollisionCannotCrossOwnership |
| Durable invalidation, replaced inode/link/mode refusal, abrupt owner exit | TestExclusiveLeaseAndDurableInvalidation, TestLeaseRefusesLinksAndUnsafeFileModes, TestAbruptOwnerExitReleasesLockAndKeepsOnlyIdleData |
| No title or unstable-identity persistence | TestTitleAndFallbackIdentityNeverPersist |

Passed uncached `go test -race -count=1 ./...`: ACP 6.758s, pool 4.429s, gateway 2.110s, history 2.604s,
schema worker 3.747s, session 7.780s and store 2.556s; all packages passed. The opt-in installed-client
test is skipped in the ordinary suite. Subsequent idle-admission, abrupt-exit and title-persistence
regressions passed in targeted race suites. `go vet ./...` passed before these final regressions;
final checkpoint checks are recorded with the implementation report.

The new fake `fake/pool.go` imports only standard packages. Its prompt guard exits if an idle JSON
record still exists when dispatch arrives, proving the actual invalidation-before-prompt ordering.
It captures only synthetic test input. Live Kiro loading, actual-client title/tool history shapes,
longer load/fuzz and clean-host release gates remain unverified. No new external dependency was added.

## Remaining work

### Restricted-agent candidate preparation

The new candidate-profile test first failed on the missing builder, then passed for empty/two-tool
registries. It checks literal relay-only aliases, no wildcard/native authority, the sole owned MCP
command, empty resources/hooks, no inherited MCP flag, private file modes and deterministic policy
identity. D27 keeps the result explicitly unverified; it is not wired into live model startup.
The initial filesystem fixture did not provide an owner-only runtime root; the test now explicitly
creates one with MkdirTemp instead of relaxing the production mode check. The launcher race suite,
including directory-link refusal and conflicting-file preservation, passed in 2.444s. `go vet ./...`
and `git diff --check` passed for this checkpoint.

The installed `kiro-cli acp --help` lists --agent as applying to the first session, --model/--effort,
and an explicit --agent-engine selector with v2 as the current default. The first-session wording
requires a separate effective-profile check before enabling shared or loaded live sessions. No
trust-all-tools flag was used. Candidate validation, process/profile lifecycle and R06 remain open.

An opt-in validation probe initially failed its assumption that an invalid tool-list type produces
a nonzero exit. Repeating the control at the exact same file path confirmed that both the candidate
and a tools value of 42 return exit 0. `TestKiroAgentValidationExitStatus` records this observation
(6.078s suite), with execution verification remaining false. Its declared MCP executable is the
fixed system `/usr/bin/false`; no Kiro prompt is submitted. This is not a passing restriction gate,
nor proof that Kiro effectively accepts malformed tools: only the exit-status test is inconclusive.
Earlier descriptions of successful syntax validation must be read as a zero command exit only.

### Phase 6 Kiro identity preflight and unauthenticated startup

Initial probes without the Kiro installation directory in PATH timed out. The installed public
`kiro-cli-chat` helper was then located and independently reported 2.21.1, matching `kiro-cli`.
With that directory in the explicit PATH, `whoami --format json` completed with exit 0. The complete
stdout was not JSON: a compact first object contained accountType/email/region/startUrl string fields,
followed by a short non-JSON postamble. The helper returned the same format. No field values or
postamble prose were logged. The earlier whole-stdout-object assumption was therefore incorrect.

New preflight tests first failed on missing APIs. D26's version-specific parser now bounds and checks
the leading identity object, rejects ambiguous JSON continuations and stores only a keyed account
scope. Independent tests verify version-check ordering, environment, stable/different account scopes,
malformed/duplicate/oversize output, command timeout and caller cancellation. The final launcher race
suite passed in 2.149s. The installed read-only `TestKiroPinnedLoginPreflight` passed in 4.388s
(2.50s test), with a verified CLI identity and a private scope digest. This supersedes the earlier
unknown login result; it does not claim that the next model request's credentials cannot expire.

The empty-HOME Kiro probe's syntax validator first timed out, then exited unsuccessfully after PATH
was corrected. The same validation command with the existing HOME exited 0. ACP was still
launched with the empty temporary HOME and no copied account files; it returned a recognized
authentication failure before initialization completed. `TestKiroIsolatedACPHandshake` passed as that
bounded observation in 6.766s, with no session prompt, model credits or client tool effect. This proves
local unauthenticated failure classification, not logged-in ACP conformance or effective restrictions.
No login/logout command was invoked, and no claim is made about the CLI's internal account-cache reads
or updates. `go vet ./...` and `git diff --check` passed at this checkpoint.

### Phase 6 temporary client settings

The profile tests first failed on the missing launcher component, then passed in 2.180s after its
implementation. They cover user-scope permissions/hooks preservation, explicit environment routing,
bounded JSON snapshots, invalid versions/endpoints/credentials, owner-only files, defensive copies,
missing settings and concurrent cleanup. D24 defines the pinned settings/environment contract and
remaining asset/interactive integration work.

The installed unmodified Claude 2.1.263 probe uses only new temporary settings, synthetic local HTTP
servers and owned harmless SessionStart hooks. It observed two model-envelope requests, one catalog
request, both user/project hooks, local env precedence and zero requests to the conflicting provider
endpoint. A user-level full Read deny remained effective despite project-level Read allowance; the
matching tool result was an error, then the client finished normally. All four source settings files
were unchanged and the runtime removed. No Kiro or external model request was made.

The first probe's path-specific Read pattern did not deny the tool; that independently authored test
was corrected to the unambiguous whole-tool Read rule without changing product permission behavior.
The corrected probe passed in 2.526s. Its actual continuation roles were user, system, assistant,
user, system. The later D25 work below handles an exact repetition of the last standing system
sequence; arbitrary new instructions during a suspended tool remain unsupported.
The completed profile race suite, including link/replaced-root cleanup, passed in 1.983s.
`go vet ./...` and `git diff --check` passed at this checkpoint.

### Phase 6 actual-client tool continuation

The local profile probe confirmed that the trailing system text repeats the prior standing update,
and that the decoder accepts the envelope. The session regression covers a two-message system
sequence: changed, older, partial, duplicated and reordered sequences, extra user/assistant content,
then the valid complete repetition and a rejected result replay. Rejections leave pending ownership
available. The first assertion incorrectly required Request for a new ordinary user request; it was
corrected to accept the existing Busy rejection as well. The focused race suite passed in 3.931s.

The first actual-client/ACP integration failed because its whole-tool deny left zero advertised
tools; the independent ACP peer requires one declared relay alias. The separate profile observation
now records that zero count explicitly. A new client-owned PreToolUse hook instead denies the declared
Read call when requested. The peer distinguishes a real MCP tool result from JSON-RPC failure and
requires the synthetic hook denial reason before completing its one ACP prompt.

All three opt-in unmodified Claude 2.1.263 tests passed together in 5.135s. The actual gateway →
independent ACP → MCP relay → client hook denial → matching result path passed in 2.11s, with exactly
two HTTP model requests, final idle state, unchanged source settings and joined driver cleanup. The
other probes verified envelope/header/discovery behavior and settings/environment precedence. No
Kiro, external inference or client file/shell tool effect was used. An approved real tool effect,
interactive UI and live Kiro execution restrictions remain separate release gates.

The full uncached `go test -race -p 2 -count=1 ./...` suite then passed: ACP 4.990s, pool 2.595s,
Anthropic 8.204s, gateway 2.262s, launcher 1.570s, session 8.550s and status 1.378s. All remaining
packages passed; installed-client tests are separately opt-in and were verified above. `go vet ./...`
and `git diff --check` passed after the continuation changes.

### Follow-up public Kiro configuration and catalog observations

The main and helper CLI validation observation passed in 10.224s (8.30s test). Both 2.21.1 binaries
advertise create/validate/list/edit and return zero for the generated candidate and a numeric tools
negative control. A bounded, test-only combined stdout/stderr capture distinguishes the control:
214 bytes containing fixed parse-error markers; the candidate has no diagnostic output. No diagnostic
prose, account data or unrestricted stderr was retained. D27 records that the syntax failure is
reported on stderr, while effective execution restrictions remain unverified.

The public help-only configuration probe passed in 6.686s (4.80s test). Chat advertises model/session
listing, model/agent/effort, legacy/TUI and engine choices; ACP advertises agent/model/effort/engine and
auth-method options. The probe records only flag names. No trust flag, alternate engine, cloud mode,
model prompt, login or configuration mutation command was invoked. The current 2.x reference's
version claims for candidate includeMcpJson/allowedTools fields still require black-box resolution.

The public `chat --list-models --format json` observation passed in 4.924s (3.08s test), without a chat
prompt or ACP session. It returned a JSON object with `default_model` (string) and `models` (array),
with 19 entries at observation time. The first item's field types were model_id/model_name/description/
rate_unit strings and context_window_tokens/rate_multiplier numbers. Only names, types and counts
were recorded, not account values or model output. This identifies a possible finite startup catalog
source; strict decoding, unit semantics and cache integration remain implementation work.

### Phase 6 owned HTTP server

New server tests first failed on absent APIs. D29 adds connection admission before HTTP parsing,
header/idle/body/write bounds, base-context cancellation and bounded drain/forced-close/join stages.
The first idle-connection test failed because its inner error declaration hid the socket read result;
the corrected test passed without changing product timeouts. Connection lifecycle tracking also
checks Go's final connection states, not just closed descriptors.

`go test -race -count=1 ./internal/gateway` passed in 3.347s, including connection capacity/reuse,
auth/SSE behavior, incomplete headers and unauthorized bodies, oversized headers, idle expiry,
canceled streaming, eight Close callers and bounded reporting for an intentionally uncooperative
catalog handler. That fixture is explicitly released and joined after verifying the failure report.

The new HTTP → session → independent ACP shutdown test initially omitted mandatory ClientInfo in its
fixture configuration and failed before starting ACP. After supplying the fixture identity it passed
in 3.863s overall (0.28s test). It observes the fake's owned PID through SSE, closes the actual loopback
server mid-turn, and verifies the handler/socket counts are zero, the driver is unstarted and the
process group no longer exists. No live Kiro/model or client tool effect was used. `go vet ./...` and
`git diff --check` passed. Full launcher sequencing and suspended-session cleanup remain separate work.

The complete uncached `go test -race -p 2 -count=1 ./...` then passed with the terminal and HTTP server
changes together: ACP 4.924s, pool 2.487s, Anthropic 8.453s, childproc 5.427s, gateway 2.963s,
launcher 2.011s and session 8.227s. All remaining packages passed; installed-client/Kiro tests remain
separately opt-in. No live model prompt is implied by this suite.

### Phase 6 attached client and terminal lifecycle

The previous checkpoint's full uncached `go test -race -p 2 -count=1 ./...` passed: ACP 5.071s,
pool 2.539s, Anthropic 8.009s, gateway 2.202s, launcher 1.544s, session 8.486s and status 1.241s;
all remaining packages passed, with live tests still opt-in.

New attached-client tests first failed on absent APIs. The descriptor/environment, output-stall,
input-stall, admission, repeated Close and whole-group cleanup implementation then passed its race
suite in 5.543s. The disposable macOS terminal fixture exposed two distinct issues: exact termios
comparison included the documented pending-input PENDIN state, and Ignore/Reset did not preserve the
parent's prior signal state. The test now excludes only PENDIN, and the implementation restores
foreground ownership through a bounded effect-free self invocation without changing signal handlers.
The helper initially hit its one-second deadline under the race runtime's default exit delay;
the helper's explicit environment now disables only that test-runtime delay, leaving its deadline
unchanged. D28 records the final behavior and limits.

Final `go test -race -count=1 ./internal/childproc ./internal/launcher` passed in 5.931s and 2.148s.
The actual disposable tty test covers normal/nonzero/forced termination, failed exec, competing tty
ownership, original termios/group restoration, existing SIGTTOU handler delivery and ignored-signal
preservation. No user terminal, real client, Kiro model or client tool effect was used in these tests.
`go vet ./...` and `git diff --check` passed. x/sys's compiled use is recorded in DEPENDENCY_REVIEW.md.
Gateway/session orchestration, actual interactive readiness and shell job control remain unfinished.

### Phase 6 finite launcher subprocess runner

Tests first failed because the CLI runner did not exist. `go test -race -count=1 ./internal/childproc`
then passed in 4.048s. Cases cover bounded stdout overflow, discarded 16 MiB stderr, exact exit code
without raw error disclosure, an explicit environment without inherited sentinels, process admission,
eight concurrent Close callers, and complete group removal for a TERM-ignoring descendant including
when its leader exits first. D23 records the limits; interactive client ownership remains separate.
The additional invalid-command regression passed in 2.241s: duplicate/invalid environment keys, NULs,
oversized inputs and relative executable paths are rejected before admission. `go vet ./...` and
`git diff --check` passed at this checkpoint.

The opt-in read-only Kiro probe verified `kiro-cli 2.21.1`. Its `whoami --format json` did not complete
within the five-second command deadline and was terminated through the owned cleanup path. The
observation test passed as a bounded probe (6.897s suite), but this is **not** evidence of successful
authentication or a verified whoami JSON schema. No account values, stderr, prompts or model calls
were retained. At that checkpoint login status remained unknown; a launcher must not infer logged-in
or logged-out from this timeout. The later D26 preflight above verifies the observed CLI identity. No
login, logout or settings mutation command was invoked.

Current official Kiro links redirect to CLI 3.0/IDE 1.0 documentation. The explicit
[CLI 2.x reference](https://kiro.dev/docs/cli/2x-reference/) describes different tool/permission/hook
configuration, and the [current configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/)
labels its version scope. Checked 2026-09-08. Installed 2.21.1 must not be configured by assuming 3.0
permission semantics or automatically upgraded. R06 still requires effective black-box restriction
proof; syntax validation alone does not establish supported fields or disabled inherited behavior.

### Phase 6 media evidence

Media validation and projection tests first failed on missing APIs. The implemented inline subset,
limits and capability contract are recorded in D19. Independent PNG/JPEG/GIF encoders generate the
test inputs; no upstream image fixture is copied. WebP uses the reviewed x/image header decoder.
Malformed WebP/MIME mismatches are covered. A later positive test below adds independently generated
lossless WebP; live media interoperability remains unverified. Synthetic PDF-shaped bytes test only the
header/transport contract, not document rendering.

Passed `go test -race -count=1 ./internal/anthropic ./internal/projection ./internal/session
./internal/acp ./internal/gateway`: 8.845s, 1.879s, 8.470s, 6.405s and 2.589s respectively. The fake
ACP sees native image bytes, the next turn sends only its text delta, and missing media capabilities
or an escaped prompt above the line limit do not cancel another active pooled session. Provider
token counts remain unchanged at zero. Actual Kiro media/embedding support is not inferred from this.

### Phase 6 streaming evidence

Keepalive tests first failed on the missing configuration/API, then passed with scheduled text,
first-event expiry, total expiry and authentication failure. The gateway/session race suites passed
in 2.959s/7.851s. The independent ACP HTTP suite also runs short ping intervals and verifies that
failure still discards the process and normal completion becomes idle. D20 records header commitment
and the unchanged owned deadlines. These are local fixture results, not live Kiro/client proof.

The focused independent ACP HTTP regression subsequently passed in 5.093s. Its old 150 ms total
budget sometimes expired during ordinary process startup under race instrumentation; the test now
uses separate bounded startup, first-event and deliberately slow-turn scenarios. Product deadlines
were not relaxed by that correction.

### Phase 6 usage cache and turn metrics

Cache/metadata/queue tests initially failed on missing APIs, followed by new integration failures on
absent manager and gateway metrics configuration. The independent fake emits three identical private
metadata notifications; two delivered main turns produce exactly two records in created/reused
states. Title, parent-agent, canceled and undelivered cases produce none. A real fake ACP/MCP relay
round trip publishes once only after its final response, never on its successful tool handoff.

The UI hook tests reject wrong model/UI authority, wrong methods/paths and unsupported/oversize bodies
without draining queued records. Missing account usage preserves the latest model-only status.
Usage tests cover 100 coalesced readers, TTL/backoff, failure retaining prior values, copied snapshots,
shutdown joining a canceled fetch and invalid numeric values. Private unknown strings never enter
numeric diagnostics. D21 records the provisional field mapping and best-effort bounded queue policy.

The public Claude gateway contract later exposed a gap in the background classifier: the agent
header identifies a first-level subagent without any parent header. A new regression first produced
one incorrect foreground record; the manager now excludes either agent header as well as title work.
The focused foreground/tool-handoff race suite passed in 4.038s after this correction.

Passed `go test -race -count=1 ./internal/session ./internal/gateway ./internal/status
./internal/kirofeature`: 9.554s, 3.025s, 2.152s and 2.344s respectively. Earlier focused metadata/cache
tests passed in 1.924s/1.946s; the main lifecycle regression passed in 3.471s. These are synthetic
protocol and module results; a real Kiro account-usage command, private payload mapping and actual
client hook bridge have not been verified. No model credits or client tool effects were consumed.

### Phase 6 local estimates

The estimator's tests first failed on absent APIs, then passed for deterministic UTF-8 byte estimates,
128-entry computation caching, ordered logical prefix accounting, JSON formatting, large integer
arguments and exclusion of declared media/thinking. New session assertions initially failed on the
missing completion estimate. The integration now verifies that text before tools, serialized tool
arguments and final text all contribute once across HTTP handoffs; consecutive main turns retain
logical prefix opportunity. D22 documents that this is not a provider tokenizer or cache-hit claim.

Passed `go test -race -count=1 ./internal/session ./internal/status ./internal/gateway` in 9.380s,
2.423s and 3.065s respectively. The earlier standalone status suite passed in 1.955s. No provider
usage fields changed, and the cache/records retain only keyed digests and numeric diagnostics.

The WebP positive fixture is independently generated from the public
[lossless bitstream format](https://developers.google.com/speed/webp/docs/webp_lossless_bitstream_specification)
and [RIFF container contract](https://developers.google.com/speed/webp/docs/riff_container), checked
2026-09-08. It uses newly chosen constant colors and single-symbol code alphabets, with no imported
image or upstream encoder/test code. Both opaque and transparent 2×3 images must fully decode to
their exact pixels before the gateway's header-only validation is tested. This adds local format
evidence without claiming Kiro image interoperability or full production pixel validation.

The focused WebP race test passed in 2.061s. `go vet ./...` and `git diff --check` also passed after
the estimator and positive media fixture were integrated.

The first full race run had one fake ACP initialization exceed its existing two-second test RPC
limit; other packages passed. The unchanged negotiation test then passed ten consecutive repetitions
(2.514s suite; first case 0.33s, later cases 0.03–0.05s). The complete uncached suite with package
parallelism bounded at two, `go test -race -p 2 -count=1 ./...`, passed: ACP 5.014s, pool 2.306s,
Anthropic 7.896s, gateway 2.230s, session 8.273s and status 1.338s. Product and test deadlines remain
unchanged. Higher build/test process concurrency and startup sensitivity remain part of Phase 7 load
verification; a single passing rerun is not a diagnosis of the initial timeout.

### Installed Kiro usage command surface

Read-only help probes on unmodified Kiro 2.21.1 returned exit 2 for `usage --help` (unrecognized
subcommand); `--help-all` and `user --help` advertise no account-usage subcommand. `chat --help`
documents list-model/list-session JSON formats, not a usage JSON interface. No chat prompt or slash
command was submitted. The public Kiro slash-command reference describes interactive `/usage`, but
does not establish a safe noninteractive adapter for this installed version. Account usage must remain
unavailable/model-only until a version-specific, non-model command path is independently verified;
do not guess a CLI command or turn a refresh into inference. Cache/metrics infrastructure can proceed.

### Post-Phase 5 input and relay hardening

Two failing regressions reproduced an invalid client model canceling an active pooled sibling and
unsupported system-message lifetime/effort fields disappearing during normalization. Invalid local
model/projection requests now dispose only their lease. Per-message effort and turn-scoped expiry
reject explicitly; standing `clear_at: "never"` is accepted. D18 records the public-source correction
and supported subset. This does not claim complete native Anthropic system-priority semantics.

Relay admission now follows the owned ACP prompt lifetime. It is closed during creation/load/idle
and atomically checked at prompt completion; pending or validating calls cannot cross into another
turn. The new `chat-tools-idle` fake attempts a call during session creation, requires its error,
and then exercises the normal client handoff. The relay API regression initially failed on absent
lifecycle methods before their implementation.

Passed `go test -race -count=1 ./internal/anthropic ./internal/session ./internal/relay/...`:
anthropic 1.736s, session 6.800s, broker 2.052s and MCP 3.714s. The added fake-process premature-call,
active-sibling and continuation checks then passed together in 3.869s. `go vet ./...` passed.
The installed-client shape observation also records message-level field names; its latest synthetic
sample contained only role/content and no clear_at field (1.454s overall). No real model/tool effect.

### Actual client envelope observation

The opt-in `TestClaudeClientGatewayContract` passed against installed unmodified Claude Code 2.1.263
using a synthetic local gateway (1.363s overall, 0.63s test). One message and one catalog request were
observed; the session header matched and both synthetic models appeared in the discovery cache.
Only field names/types/counts/Boolean outcomes were recorded. Global client settings were unchanged,
the private runtime was removed, and no model credits or client tool effects were used.

Two earlier attempts reproduced rejection of a trailing per-message system update. The decoder now
accepts only text in that role, finds the latest user correctly and preserves update order in ACP
projection. The parser/projection/catalog/session race suites then passed. Gateway header validation
and pending-tool cross-header rejection have independent regression tests. Decision D14 records
the exact scope: interactive UI and actual-client tool continuation remain unverified.

| Phase | Completion evidence required | State |
| --- | --- | --- |
| 1 | All acceptance A plus applicable G; independent fake child, framing, negotiation, correlation, notifications, stderr, deadlines, process-group cleanup | Passed on local macOS with fake ACP |
| 2 | Authenticated HTTP text path, exact SSE/non-streaming responses, authentication fallback, disconnect tests | Passed with independent fake ACP; broader B/C requirements tracked below |
| 3 | Model catalog/mapping/cache/selection and optional effort state | Passed independent module/process tests; launcher and live interoperability remain below |
| 4 | Restricted Kiro agent proof, MCP relay, schema validation, client-only tool effects and result ownership | Fake-process/HTTP implementation passes; R06 live restriction proof and additional hardening remain |
| 5 | Request families/history/pool/persistence/resume and crash tests | Independent implementation tests pass; live client/Kiro and extended hardening remain |
| 6 | Media/web capabilities, cached usage/metrics, isolated launcher/profile and client interoperability | Pending |
| 7 | Full acceptance, fuzz/race/load, license inventory, macOS packaging/install/uninstall and opt-in live gates | Pending |

Owner rights, Kiro/private-extension usage permission, distribution intent, and project license remain
unconfirmed. Do not publish a release or declare legal clearance from the user's language selection.
Live tests that consume credits remain separately marked and opt-in under ACCEPTANCE_SPEC.md.
