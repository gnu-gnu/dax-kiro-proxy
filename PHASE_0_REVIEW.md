# Phase 0 specification review

Review date: 2026-09-08. Status: review and experiment design complete; Phase 0 exit gate open.

The recommendation is Go, subject to the experiments and final record described in
[LANGUAGE_DECISION_RECORD.md](LANGUAGE_DECISION_RECORD.md). No production code, spike code, executable
fixtures, dependency installation, Kiro invocation, or live model test was performed in this review.
The user's Phase 1 instructions describe a subsequent session, after language selection.

README.md and AGENTS.md were read first. CLEAN_ROOM_BOUNDARY.md, PRODUCT_SPEC.md, PROTOCOL_SPEC.md,
FLOWS_AND_STATE.md, IMPLEMENTATION_REQUIREMENTS.md, ACCEPTANCE_SPEC.md, and LANGUAGE_DECISION.md were
then read in full, in README order. Those were all repository Markdown documents at review start.
The previous implementation, its repository, history, and artifacts were not searched or consulted.
External evidence consists of public protocol documentation and candidate dependency documentation,
license files, and package manifests. No external implementation was used as a design template.

## Gate status

| Requirement | Current evidence | Remaining work |
| --- | --- | --- |
| Specification review | Findings and proposed dispositions below | Ratify changes and update the affected specifications and acceptance criteria together |
| Go/Rust experiments | Identical experiment contract and measurement plan recorded | Execute both slices and macOS CI; retain actual results |
| Language decision | Go recommended; user reports Python familiarity and prioritizes technical fit | Complete measured scores and change the decision record from proposed to final |
| Rights checklist | Recorded in DEPENDENCY_REVIEW.md | Owner facts remain unconfirmed |
| Dependencies and licenses | Candidate-level review with primary sources | Resolve exact build/test graphs and review notices and target-specific components |
| Protocol fixtures | Cases identified below | Author, validate, and freeze independently created fixtures after resolving ambiguities |
| Specification baseline | All nine starting Markdown files appeared as untracked in `git status --short` | Create the specification-only baseline commit required by CLEAN_ROOM_BOUNDARY.md before implementation; no commit was made in this review |

The following proposals are decision records for review, not silent amendments to the seven original
specifications. A proposal that changes an acceptance requirement must be incorporated into that
requirement before claiming conformance. Finding IDs below are review identifiers, not existing tests.

## Contradictions requiring a specification change

| ID | Conflicting requirements | Proposed disposition and verification |
| --- | --- | --- |
| R01 | PROTOCOL_SPEC.md sections 1 and 3 require an auth-fallback response header and ordinarily HTTP 502 for backend failure, while PRODUCT_SPEC.md requires incremental streaming. Either failure can occur after HTTP headers and text are sent. | Before headers: retain the specified status/header behavior. After headers: ordinary failure uses a defined Anthropic SSE error and discards state; recognized auth expiry closes the existing message with login text and `end_turn`, without an SSE error. Require the ordinary fallback header only when known before headers. Specify a separate terminal status marker for late expiry, optionally a declared trailer, and verify actual client handling. Extend acceptance C/G for both timings. |
| R02 | PROTOCOL_SPEC.md section 6 says absent `/effort` means no synchronization. FLOWS_AND_STATE.md section 9 can be read to probe when no command notification has arrived. Acceptance D distinguishes absence from unknown pairs. | Separate command advertisement from model/effort support. No advertisement by a bounded deadline means unknown and no command dispatch; an advertised list without `/effort` means unavailable. Probe an unknown pair only after `/effort` is advertised. Track attempted pairs separately from successful effective state so rejection does not cause another probe next turn. Model switches clear effective state, not the version-scoped attempt ledger. |
| R03 | PROTOCOL_SPEC.md section 6 calls any effort transport error nonfatal, but section 4 requires malformed frames, failed writes, and process termination to fail every waiter and retire the process. | A valid method-not-found/rejection/application error degrades effort only. A broken transport or ambiguous timed-out command remains fatal to that process. Optional features must not override transport integrity. Test both application rejection and broken transport during effort synchronization under A/D/F. |

R01 is a wire-level limitation, not a language limitation. HTTP trailers are distinct from initial
headers and may be discarded; they cannot be the sole user-visible signal. The Anthropic stream
supports multiple blocks and terminal error events. The proposed late-auth behavior must finish the
already-started message rather than emit another `message_start`. Buffer complete, validated client
tool arguments before starting a tool block, so an upstream failure cannot leave a partial JSON tool
block requiring an invented completion. These are proposed interoperability choices, not claims about
unmodified clients. See [HTTP trailer semantics](https://www.rfc-editor.org/rfc/rfc9110.html#section-6.5),
[response header commitment](https://pkg.go.dev/net/http#ResponseWriter), and
[Anthropic streaming](https://platform.claude.com/docs/en/build-with-claude/streaming).

## Missing contracts and associated risks

| ID | Gap and specification location | Proposed decision or evidence needed | Needed before |
| --- | --- | --- | --- |
| R04 | HTTP request lifetime versus ACP turn lifetime: FLOWS_AND_STATE.md sections 4, 10, 11 and acceptance G do not distinguish a successful `tool_use` response from an interrupted response. | A session-owned turn supervisor survives successful HTTP tool handoff. Only a disconnect before completing that response cancels it. Tool continuation resolves relay futures; it does not send a second `session/prompt`. Retain a bounded tool-wait deadline while no HTTP request exists. A/G/E tests must exercise each boundary. | Phase 2 lifecycle design; mandatory in Phase 4 |
| R05 | PROTOCOL_SPEC.md section 3 says all pending IDs, while section 7 permits 64 pending calls and gives no batch-closing rule. Calls can arrive after the HTTP response closes. | Seal the advertised result batch atomically. Distinguish advertised pending calls from bounded, not-yet-advertised calls; only the sealed batch determines the next accepted result set. Queue later calls for the next response. Validate the complete set before releasing any relay result. Preserve text preceding tools and emit each tool only from an authenticated relay request, not again from its ACP status updates. | Phase 4; acceptance C/E |
| R06 | Restricted-agent behavior, allowed native tools, Kiro configuration precedence, and inherited MCP/tools/hooks are not specified. Empty ACP client capabilities and permission rejection alone do not prove Kiro's built-in tools are disabled. | Pin an independently observed Kiro version and effective restricted profile. In a disposable workspace, attempt filesystem/shell/task/subagent effects and inherited configuration overrides; verify no effects and inspect the effective advertised tool set through public behavior. If restrictions cannot be established, fail startup. Native web access needs a separately enumerated exception. | Any live Kiro integration; acceptance E/I |
| R07 | Process shutdown in PROTOCOL_SPEC.md section 4 closes stdin, but FLOWS_AND_STATE.md section 5 allows two sessions on one process. Early creation notifications, late cancellation responses, and session-local disposal lack complete routing rules. | Separate disposal of one session from retirement of a process. Closing shared stdin or signaling its group retires every attached session. Preserve request ownership while draining cancellation responses; never assign an existing session's events to the temporary creation owner. Unknown/ambiguous creation or load routing retires the process. | Phase 1 transport policy, then Phase 5 pool tests A/F/G |
| R08 | Public ACP notifications, permission outcomes, result/error envelopes, invalid response IDs, stop reasons, and unsupported content cases are not frozen as wire fixtures. | Use the versioned public ACP v1 contract, with Kiro extras in separate fixtures. Specify duplicate/unknown/late IDs, numeric precision, invalid JSON-RPC envelopes, unsupported notifications, and no responses to notifications. Add the Phase 1 fixture cases below. | Phase 1; acceptance A |
| R09 | Relay MCP has no pinned protocol version, lifecycle notification handling, control-frame delimiter/envelope, or cancellation contract. AGENTS.md's public-source list does not explicitly name MCP. | The user's public-protocol authorization covers official MCP docs for this review; add MCP explicitly to the repository allowlist. Use a versioned initialize-based MCP baseline only after Kiro negotiation confirms it. Permit `notifications/initialized` and applicable cancellation notifications without expanding the four effect-free request methods. Define control version, framing, IDs, session binding, errors, and result encoding. | Phase 4; acceptance E |
| R10 | Request/system/history serialization and digest canonicalization are unspecified; request-family keys could split tool follow-ups from their main session. Identical first-user text cannot uniquely identify two independent conversations. | Define ordered projection and canonicalization with domain-separated digests, explicit numeric handling, and compatibility inputs. Bind follow-ups to the originating main session; isolate titles. Scope fallback identity to a launcher instance as well as profile; reject ambiguous ownership. No persistent resume without a stable client ID. Freeze examples for system arrays, mixed content, duplicates, truncation, and divergent histories. | Basic projection in Phase 2; full reconciliation in Phase 5 |
| R11 | FLOWS_AND_STATE.md section 8 requires exclusive ownership but does not give the lock lifetime or crash protocol. | Lock before checking/loading/invalidating; invalidate the old reusable snapshot before backend mutation; retain an exclusive live lease so another gateway cannot claim the same backend session. Specify crash points, atomic rename, durability policy, symlink refusal, owner-only lock files, and bounded lock acquisition. Consider keyed history digests to reduce offline guessing of short prompts. | Phase 5; acceptance F/G |
| R12 | Many resources are called bounded without sizes, byte budgets, deadlines, or overload behavior. A 16 MiB HTTP body can exceed the 8 MiB ACP or 4 MiB relay frame after encoding. | Define global active-process/session/request caps, pending RPC and event byte/count caps, buffered non-streaming output, schema depth/complexity, socket clients, caches, stderr, and write deadlines. Check the final encoded size; reject oversized tool results before consuming any pending ID. Define queue overflow as a terminal failure when dropping would corrupt a turn. Metrics can use a separately specified drop policy. | Transport limits in Phase 1; HTTP in Phase 2; relay in Phase 4 |
| R13 | Draft 2020-12 alone does not choose external `$ref`, regex, format, numeric precision, or computational limits. Token-like redaction alone cannot prevent arbitrary prompt/tool text in stderr from appearing in logs. | Schema retrieval is restricted to supplied in-memory resources, with no HTTP or file access; test regex dialect and validation cost. Classify bounded stderr privately, and log only allowlisted classes/metadata by default. Do not dump the raw tail or schema validation values into diagnostics. Add synthetic credential and prompt/tool-output sentinels. | Stderr in Phase 1; schema in Phase 4; acceptance A/E/H |
| R14 | The launcher-facing contract has no exact flags/environment/profile isolation, hook/status paths and payloads, client readiness signal, local-command shapes, or title classifier fixtures. Model discovery assumes a custom catalog shape and model selector behavior without a pinned client version. | Freeze executable/version discovery, model/UI auth table, command/status contracts, and sanitized child environment. Use an unmodified client to prove custom catalog consumption, model-ID acceptance, title/main separation, local command detection, readiness, and byte-for-byte settings preservation. Unsupported client behavior needs a specification decision; do not assume `/v1/models` alone populates its UI. | Validate feasibility before broad implementation; phases 2/3/6, acceptance B/D/H/I |
| R15 | Media, native search tool versions, search constraints, citation/result fields, and Kiro private payload fields are incomplete. Authentication patterns also need source attribution to distinguish Kiro account expiry from a tool's unrelated HTTP 401. | Pin supported media/search declarations and independently observed capability payloads; reject unsupported typed tools before prompting. Define base64/result budgets, optional document support, search limits, duplicate event identity, and replay/citation behavior. Classify account expiry only from known Kiro failure sources; a 401 quoted in model/tool text is not enough. | Auth in Phase 1/2; remaining mapping in Phase 3/6 |
| R16 | The accepted Anthropic field subset and unsupported-field behavior are not enumerated. `messages` being nonempty does not establish valid roles/content, or enforcement of `tool_choice`, token/output constraints and structured-output declarations. | Freeze a field support table for the pinned client: validate, faithfully map, explicitly ignore when harmless, or reject. Never ignore a field that restricts tool execution or routing. Define required response fields, error envelopes, multi-block/non-streaming equivalence and stop-reason mapping, including limits and refused/cancelled turns. | Phase 2, with tool constraints in Phase 4; acceptance B/C/E |

For R04/R12, a proposed deadline policy starts first-event timing at prompt dispatch and satisfies it
only with usable text/tool/native-result activity, not a diagnostic notification or keepalive. The
total wall-clock ACP-turn deadline continues across HTTP tool handoffs and includes tool waiting;
the pending-tool deadline is an additional bound and never extends it. Defaults must allow the
intended client approval interval. On tool timeout, settle the relay with a tool error and cancel and
discard the session. If expiry/failure happens while no HTTP response is open, retain only a bounded,
session-scoped terminal outcome for the next request/status read, never resume late tool results or
reuse that backend state. Define this next-request behavior alongside R01 and test it explicitly.

Public ACP's baseline includes `session/new`, `session/prompt`, `session/cancel`, and `session/update`;
other capabilities must be negotiated. Cancellation can produce updates before the original prompt
returns its cancelled stop reason. Empty client capabilities are appropriate but do not describe an
operating-system sandbox. See [ACP initialization](https://agentclientprotocol.com/protocol/v1/initialization)
and [ACP prompt cancellation](https://agentclientprotocol.com/protocol/v1/prompt-turn#cancellation).

For R09, [MCP lifecycle 2025-06-18](https://modelcontextprotocol.io/specification/2025-06-18/basic/lifecycle)
is a candidate for the initialize-based contract, not a claim that Kiro negotiates that version. It
includes `notifications/initialized`; [MCP stdio](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#stdio)
defines newline framing. Never substitute an unversioned latest MCP schema without rechecking Kiro.
The prohibition on replying to notifications also follows from
[JSON-RPC 2.0](https://www.jsonrpc.org/specification#notification).

For R15, Anthropic web-search result/citation fields have specific continuation semantics. Do not
invent provider-encrypted search payloads or claim compliance from a text-only approximation. Define
and test the supported subset, including declared search limits, before advertising it. See
[Anthropic web search](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool).

## Proposed Phase 1 fixture contract

Author inputs from public ACP v1 and these documents, with synthetic names/content. Store source URL,
version/date, case ID, expected public events, deadline class, and final process state beside each
case. These cases are designed, not implemented or frozen in this review.

| Case group | Required observable behavior | Acceptance |
| --- | --- | --- |
| Initialization | First request is version-1 initialize with empty client capabilities; reject a different/missing/malformed negotiated version; no session work before success | A |
| Correlation | Two outstanding numeric IDs receive reversed results with a notification between them; neither result completes the other waiter | A |
| Envelope and framing | Exactly one UTF-8 JSON object per LF-delimited frame; validate `jsonrpc`, method/ID and mutually exclusive result/error shapes; reject blank/non-object/batch frames, malformed JSON, invalid UTF-8, truncated EOF and oversize in either direction. Proposed strict policy rejects duplicate envelope members and requires exact protocol-key spelling. | A |
| ID policy | Proposed emitted range is 1 through 2^53-1 with retirement before wrap; parse without floating-point rounding. Reject fractional/null/string substitutes for emitted numeric response IDs and unknown/duplicate responses. Echo valid agent-request IDs in their original type. | A |
| Boundary sizes | Measure payload bytes excluding the final LF; test 8 MiB exactly and one byte over, split UTF-8/escaped newlines, fragmented reads and multiple frames per read. Bound accumulation before parsing. | A |
| Notifications | Route known `session/update` content while RPCs are pending; unknown optional notifications do not execute effects or receive JSON-RPC replies; a full queue must not deadlock the stdout reader | A/G |
| Agent requests | Select a valid rejection option for permission requests, otherwise return cancellation; every other request receives a disabled/method-not-found response; notifications never receive replies | A |
| Timeout/write/crash | Initialization, RPC and blocked-write deadlines settle every waiter once. In the initial transport phase, an ambiguous timeout retires the process. Later session-local recovery requires a separate routing decision and pool tests. | A/G |
| Cancellation/shutdown | Send session cancel when available, close stdin, wait, TERM the process group, then KILL within fixed bounds. Test repeated cancellation, leader exit with a surviving descendant, ignored TERM, full stderr, blocked stdin, and inherited open pipes. All direct children are reaped and the owned group is gone. | A/G |
| Stderr/auth | Bound retained bytes/line/count; redact split credential sentinels before retained diagnostics; emit no raw prompt/tool sentinels in default logs. Positive Kiro-expiry cases and unrelated numeric/textual 401 cases remain distinct. | A |

Keep the stdout reader independent from potentially blocked HTTP writers, agent-request handlers, and
stderr processing. Draining protocol output and joining shutdown must not depend on a canceled caller.
For a full process retirement all attached sessions become unusable; Phase 1 must not pretend to offer
session-local recovery before its routing semantics are implemented.

## Proposed implementation sequence after the gate

1. Resolve Phase 1 decisions R03/R07/R08/R12/R13/R15 and the specification-baseline/rights record;
   execute both language experiments, fill the decision record, and freeze the shared protocol fixtures.
   Resolve R01 and the R04 ownership model before building the HTTP turn path. Check R06/R14 feasibility
   before investing in broad Kiro/client integration.
2. Phase 1: write an independent fake ACP process and failing protocol tests first. Implement framing,
   negotiation, correlation, notification dispatch, redacted stderr classification, deadlines, and
   process-group shutdown. Run every acceptance A item and applicable cancellation tests in G until
   they pass. Retain evidence for malformed/out-of-order/crash/cancel scenarios. Use no live Kiro here.
3. Phase 2: one-process, one-session authenticated HTTP text path and both response modes; acceptance
   B/C and disconnect tests in G, including early/late auth expiry under the revised R01 contract.
4. Phase 3: model catalog, strict mapping, model/effort ordering and caches; acceptance D, including
   absent private features and transport failure during a private command.
5. Phase 4: effect-free relay and tool continuation under R04/R05/R06/R09/R13; acceptance C/E/G and
   adversarial ownership/duplicate/result-set tests before opt-in client tool round trips.
6. Phase 5: family identity, reconciliation, pooling and persistent ownership; acceptance F/G with
   crash-point and simultaneous title/main coverage.
7. Phase 6: only the independently specified media/web subset, cached usage, metrics, isolated launcher
   profile and readiness; applicable B/C/H and macOS interoperability checks.
8. Phase 7: all applicable A-I, fuzzing, dependency/notice report, clean-host packaging, opt-in live
   logout/expiry and tool tests, reinstall/uninstall/settings-preservation evidence. No release while
   a release-blocking invariant or rights/license gate remains unresolved.

## Validation of this review

Validation is document-level: full mandatory reading, cross-document traceability, primary-source
checks, and local Markdown/link/whitespace checks. There is no benchmark, ACP acceptance result,
verified Kiro/client capability, dependency lock graph, macOS artifact, or completed license clearance
to report yet. Those omissions are explicit Phase 0 follow-up work, not passing test results.
