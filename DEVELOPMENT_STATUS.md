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
fresh state for subsequent full-history requests until reconciliation is implemented in Phase 5.

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

## Remaining work

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
| 5 | Request families/history/pool/persistence/resume and crash tests | Pending |
| 6 | Media/web capabilities, cached usage/metrics, isolated launcher/profile and client interoperability | Pending |
| 7 | Full acceptance, fuzz/race/load, license inventory, macOS packaging/install/uninstall and opt-in live gates | Pending |

Owner rights, Kiro/private-extension usage permission, distribution intent, and project license remain
unconfirmed. Do not publish a release or declare legal clearance from the user's language selection.
Live tests that consume credits remain separately marked and opt-in under ACCEPTANCE_SPEC.md.
