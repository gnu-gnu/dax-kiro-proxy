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
profile with empty tools, empty MCP servers/resources, no hooks and `includeMcpJson: false` passed
`agent validate` with exit 0. The default agent creation behavior advertised all tools (`*`) and
included inherited MCP configuration. The create command opened an editor; the owned editor/process
was terminated, and later probes suppressed editor invocation and used bounded process groups.
Syntax acceptance does not prove effective tool suppression. No model request or client tool effect
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

### Phase 6 media evidence

Media validation and projection tests first failed on missing APIs. The implemented inline subset,
limits and capability contract are recorded in D19. Independent PNG/JPEG/GIF encoders generate the
test inputs; no upstream image fixture is copied. WebP uses the reviewed x/image header decoder.
Malformed WebP/MIME mismatches are covered; a valid independently generated WebP and live media
interoperability remain additional verification work. Synthetic PDF-shaped bytes test only the
header/transport contract, not document rendering.

Passed `go test -race -count=1 ./internal/anthropic ./internal/projection ./internal/session
./internal/acp ./internal/gateway`: 8.845s, 1.879s, 8.470s, 6.405s and 2.589s respectively. The fake
ACP sees native image bytes, the next turn sends only its text delta, and missing media capabilities
or an escaped prompt above the line limit do not cancel another active pooled session. Provider
token counts remain unchanged at zero. Actual Kiro media/embedding support is not inferred from this.

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
