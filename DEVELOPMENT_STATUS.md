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
- `go list -m all` reports only this module. No external Go module is currently required.

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

## Remaining work

| Phase | Completion evidence required | State |
| --- | --- | --- |
| 1 | All acceptance A plus applicable G; independent fake child, framing, negotiation, correlation, notifications, stderr, deadlines, process-group cleanup | Passed on local macOS with fake ACP |
| 2 | Authenticated HTTP text path, exact SSE/non-streaming responses, authentication fallback, disconnect tests | Next |
| 3 | Model catalog/mapping/cache/selection and optional effort state | Pending |
| 4 | Restricted Kiro agent proof, MCP relay, schema validation, client-only tool effects and result ownership | Pending |
| 5 | Request families/history/pool/persistence/resume and crash tests | Pending |
| 6 | Media/web capabilities, cached usage/metrics, isolated launcher/profile and client interoperability | Pending |
| 7 | Full acceptance, fuzz/race/load, license inventory, macOS packaging/install/uninstall and opt-in live gates | Pending |

Owner rights, Kiro/private-extension usage permission, distribution intent, and project license remain
unconfirmed. Do not publish a release or declare legal clearance from the user's language selection.
Live tests that consume credits remain separately marked and opt-in under ACCEPTANCE_SPEC.md.
