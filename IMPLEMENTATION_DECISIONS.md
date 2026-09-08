# Adopted implementation decisions

Date: 2026-09-08. Authority: the user's Go selection and instruction to implement the whole product;
AGENTS.md requires decisions or independent tests where the specifications are ambiguous.

## D01: language and provenance

Go is selected by the user, with no claim of measured superiority over Rust. The specification-only
baseline is `7b108dd`; LANGUAGE_DECISION_RECORD.md records the selection. The first production commit
will reference it. Owner rights and the project license remain unresolved release gates. Initial
production and test code use the Go standard library only; the fake ACP child is independently
written in Go and does not import production protocol packages.

## D02: ACP framing and envelopes (review R08/R12)

Frames are UTF-8 JSON objects with one terminating LF. The 8 MiB limit counts bytes before LF; a CR
before LF is JSON whitespace and counts toward the limit. Blank lines, invalid UTF-8, incomplete EOF,
duplicate JSON object keys, excessive nesting (64), and batches are protocol failures. Protocol
envelope keys use exact spelling. JSON numbers are preserved, never rounded through float64 for IDs.
Outgoing IDs increase from 1 to 2^53-1; no reuse or wrap is permitted. Responses must match an
outstanding numeric ID. Unknown/duplicate responses are fatal. Valid inbound agent request IDs retain
their numeric or string representation. Notifications do not receive replies.

All malformed/oversized envelopes and unrecoverable read/write failures retire the process and
settle all pending calls. Local parameter encoding errors detected before admission need not retire
it, but an oversized transport frame does. Optional unknown notifications are allowed through a
bounded queue; they never authorize effects.

## D03: lifecycle and bounds (review R03/R07/R12)

A transport owns its process independently of an individual caller context. A caller cancellation
after request admission retires the initial transport: send cancel for known prompting sessions when
possible, close stdin, allow graceful exit, signal the entire owned group with TERM and then KILL.
All direct children are reaped. The group is checked even if its leader has already exited. Cleanup
runs once in its own lifetime; repeated Close calls join it, even if their caller is canceled.
A future narrower session-local cancellation policy must preserve routing and pass shared-process
tests. Closing a shared transport always invalidates all attached sessions.

Defaults: 64 pending RPCs; 64 notification entries and 16 MiB queued notification bytes; 64 outbound
entries and 16 MiB queued output bytes; 30-second RPC timeout; 5-second write timeout; 500-millisecond
best-effort cancellation-write stage, then 1 second each for graceful/TERM/KILL stages. These are
configurable positive finite limits, with finite upper bounds. There is no background goroutine per
unaccepted request or per notification. Overflow fails explicitly rather than losing protocol data.
Transport initialization must complete before ordinary calls can be admitted.

## D04: failure privacy and classification (review R13/R15)

Process stderr is continuously drained with a 4 KiB line limit, a 64-line/64 KiB retained budget, and
credential redaction before retained diagnostics. Default public diagnostics contain only safe
failure classes, counts and timings, never raw stderr, remote error messages, params, prompts or tool
outputs. Truncated stderr fragments must not expose a suffix of a credential.

Kiro account-expiry classification is an injected backend adapter, separate from public ACP framing.
Known structured/textual authentication failures at the backend error/stderr boundary classify as
auth expiry; model notifications and tool output never feed that classifier. Plain numeric 401 in
arbitrary prose and generic transport errors are insufficient. Raw remote error data is used only
for immediate classification and is not included in an error's printable representation.

## D05: feature failures

A well-formed private-command rejection or method-not-found response can degrade an optional feature.
A broken transport cannot. This resolves R03 in favor of the transport integrity invariant. No private
method names or Kiro-specific payloads are required by the public ACP transport.

## D06: HTTP commitment and turn ownership (review R01/R04/R12)

Authentication failure before HTTP commitment uses status 200, a normal completion and initial
`X-Dax-Kiro-Proxy-Auth-Fallback: 1`. After stream commitment it appends login text to the current
message, ends normally, and emits that marker in a predeclared trailer. Trailer handling by a live
client remains an interoperability observation; visible login text never depends on it. Ordinary
failures before commitment use 502; ordinary failures after commitment emit a safe SSE error.
Every failed backend is discarded even when the client receives a successful auth explanation.

The gateway calls Finish only after delivering a complete HTTP response. Cancel covers disconnect,
timeout, malformed output and partial delivery, and discards backend state. The session owner remains
responsible for the longer ACP-turn lifetime across a successfully delivered future tool handoff.
First-event timing starts after prompt dispatch; total-turn timing also bounds setup and will continue
across tool handoffs. Diagnostics distinguish the two deadlines; empty text is not a usable event.

HTTP routes match exact method/path pairs without path-cleaning redirects. `/health` returns only
liveness without a token. `/dax-kiro-proxy/status/usage` requires the independent UI token; all model
routes require the model token. Both secrets are independently generated from 32 random bytes.
Conflicting or duplicate credential headers are rejected. Query parameters never supply credentials.

Initial limits: 16 active model HTTP requests; 16 MiB request bodies and encoded responses; 15-second
body read timeout, 5-second write timeout, 90-second first-event and 10-minute total-turn timeouts.
No provider-billed token estimates are synthesized. Unsupported tool/media adapters are rejected
explicitly until their respective delivery phases, rather than silently losing those request fields.

## D07: initial text projection and delivery barrier (review R08/R10/R16)

The public ACP adapter uses ordered text parts. A fresh conversation places system text and prior
role/content arrays in a JSON context block, preceded by an explanatory text block. A separate marker
introduces the newest user blocks, which retain their order. This is a local projection policy, not
a claimed ACP system-message field or a substitute for the launcher's execution restrictions.

One stdout consumer drains all notifications already received before exposing a prompt's RPC
completion. Unknown optional updates do not become visible model text. A wrong/missing session ID on
a public session update, invalid content/stop reason, or a canceled result makes the state unusable.
Only `end_turn`, `max_tokens` and `refusal` map directly to terminal client stop reasons. An agent's
`max_turn_requests` or unknown reason currently fails explicitly rather than fabricating success.

The initial text driver owns one process/session and admits one response at a time. Until Phase 5
reconciliation is installed, subsequent independent requests retire the previous process and create
fresh state with full history; they cannot accumulate unrelated histories. It is an intermediate
internal adapter, with catalog/selection and executable launcher entry points still to come. Its
owned turn timeout survives HTTP context disposal; successful delivery uses Finish, and Cancel/Close
join process cleanup independently of caller cancellation. The test driver configures empty MCP
servers for the text-only fake; no live Kiro launch is authorized by that fixture configuration.

Other Phase 0 proposals remain pending until adopted with their relevant implementation and tests.
