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

Other Phase 0 proposals remain pending until adopted with their relevant implementation and tests.
