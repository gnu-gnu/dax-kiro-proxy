# Adopted implementation decisions

Date: 2026-09-08. Authority: the user's Go selection and instruction to implement the whole product;
AGENTS.md requires decisions or independent tests where the specifications are ambiguous.

## D01: language and provenance

Go is selected by the user, with no claim of measured superiority over Rust. The specification-only
baseline is `7b108dd`; LANGUAGE_DECISION_RECORD.md records the selection. The first production commit
`fd1df50` references that record. Owner rights and the project license remain unresolved release gates. Initial
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

## D08: model identities and selectors (review R08/R14/R15)

Client IDs use `claude-dax-`, at most 48 normalized ASCII name characters, and the first 16 hexadecimal
digits of SHA-256 of the exact backend ID. Only the validated current catalog reverses them. Duplicate
backend IDs, derived client collisions, empty catalogs and malformed/oversized entries fail. A valid
omitted current backend is added. Limits are 256 models and 1 MiB encoded catalog data; individual
IDs/names are at most 256 bytes and descriptions at most 8 KiB. Multipliers are display metadata and
do not affect IDs. Returned catalog views cannot mutate retained data.

The repository's legacy `models`/`session/set_model` contract is supported. A session advertising a
public `configOptions` model select control instead uses `session/set_config_option`; inconsistent
confirmation fails, and mode/permission selectors are never changed. Multiple competing model
selectors are rejected as ambiguous. The public contract is documented at
https://agentclientprotocol.com/protocol/v1/session-config-options (checked 2026-09-08).
The configured launch model/effort wins only until the first successfully delivered turn. No later
model selection can overlap prompt dispatch. Actual Claude Code selector population remains a live
client interoperability gate, independent of serving a syntactically correct model catalog.

## D09: optional effort and setup timing (review R02/R03)

Only a valid command advertisement seen before dispatch authorizes the optional effort attempt.
No advertisement is unknown; an advertisement without effort is unavailable. Auto and known
unsupported pairs skip. Rejected unknown pairs are retained in a bounded 1,280-entry ledger across
model/process recreation in the driver's fixed version/configuration scope. Effective state resets
on model/process changes; confirmed support can be applied again. Default diagnostics store only
status/reason classes, and reading the last status never waits for a private command's RPC.

The independent private fixture interprets a command descriptor as `{name: "/effort"}` and execution
as `{sessionId, command: {name: "effort", arguments: [value]}}`. These shapes are an explicit reading
of this repository's incomplete private contract, not a claim of observed live Kiro interoperability.
Live validation must confirm or amend them before claiming private feature support. A missing or
unrecognized advertisement leaves ordinary text working without the feature.

Setup has its own bounded context. The transport's RPC ceiling must not accidentally shorten setup
to a smaller owned-turn timeout; each prompt still receives the explicit owned-turn deadline. A
delayed-initialize fixture reproduces and verifies this separation. The gateway's overall request
deadline also includes setup.

## D10: catalog persistence and local file authority (review R11/R12/R14)

Catalog cache schema 1 records a digest of executable path/version, profile/agent/capability digests,
and initial model/effort configuration. Compatible fresh data is used for 24 hours; stale data returns
immediately during a single bounded refresh. Failures preserve the last good catalog and back off
for one minute. Cold or incompatible caches require discovery. Shutdown cancels and joins refresh.
Last-model records are separate, saved only for interactive use, and restored only through the
current compatible catalog. Launcher integration will choose explicit launch configuration before
that saved preference and will never write global client model settings.

Product record directories are 0700; files are atomically replaced at 0600 after fsync. Reads are
bounded, refuse links, special files, foreign owners and group/other access, and stay within an open
directory root. Directory path normalization precedes the symlink check. Individual records cannot
exceed 4 MiB; catalog and last-model readers impose smaller limits. Files contain metadata/digests,
not conversation bodies. Exclusive session leases and crash recovery remain Phase 5 work.

## D11: bounded schema validation (review R12/R13)

Custom tool schemas require root `type: "object"`, use Draft 2020-12, and may resolve only resources
within the submitted document. An explicit loader rejects all network and file retrieval. Go's default
RE2 regex subset is used; unsupported lookaround/backreferences reject the schema. Formats and content
keywords retain the draft's annotation behavior. Numbers use exact decimal representations, with a
128-character lexical limit and exponents between -1000 and 1000. Schema/argument limits are 64 KiB/
1 MiB, depth 64, and 8,192/65,536 value nodes. These are an explicit accepted subset, not a claim that
all Draft 2020-12 schemas or ECMAScript regular expressions work.

The reviewed validator runs in a separate helper process, with no inherited home/credentials and a
denied schema resource loader. At most two helpers run by default, each caching 16 compiled schemas.
The parent enforces 500 ms per operation, 5 seconds for startup and 5 minutes of lazy idle retention;
cancellation or deadline expiration retires and reaps the helper's group. The internal version-1
handshake reuses the tested ACP lifecycle carrier; `schema/*` RPCs are never sent to Kiro. A 64 MiB Go
memory target is soft, not an OS memory sandbox or a hard RSS guarantee. Stack/thread caps and input,
node, concurrency and time limits are independent; adversarial RSS measurements remain hardening work.

Dependency pins and licenses are in DEPENDENCY_REVIEW.md. Independently written worker tests cover
local/dynamic refs, unevaluated properties, exact large integers and decimals, denied retrieval,
unsupported regex, invalid/oversized input and process-enforced cancellation. No upstream fixture
corpus is incorporated.

## D12: effect-free relay and sealed batches (review R04/R05/R09/R12)

The MCP child implements the public 2025-06-18 initialization lifecycle, including
`notifications/initialized` and `notifications/cancelled`. If another version is requested, it offers
2025-06-18; the client must accept that version or disconnect. This pins the independently tested
contract, not a claim of real Kiro negotiation. Only initialize, ping, tools/list and tools/call are
request methods. No reply is sent to notifications. Tool waits do not block ping or the stdin reader.
Input/output MCP frames are bounded at 8 MiB, active requests at 64 and writes at 5 seconds.

The parent control protocol is version 1: a four-byte unsigned big-endian byte length followed by one
strict UTF-8 JSON object, at most 4 MiB. A call carries `version`, `owner`, `secret`, `callId`, `alias`
and object `arguments`. Replies carry version, matching callId and exactly a result or a safe error.
Each session has an opaque random owner independent of its eventual Kiro ID, a separate 256-bit
secret, a fresh 0700 directory, and 0600 socket/config. Defaults are 64 socket connections, 5-second
frame reads/writes, 64 combined pending/validating calls, 16 MiB pending use data and 5-minute tool
waits. A 4,096-entry call-ID ledger never evicts a tombstone to permit reuse; exhaustion retires the
relay. Config and socket paths are private and never supplied in logs.

Tool aliases are `relay_` plus a SHA-256 prefix of a domain-separated original name. Colliding
prefixes extend symmetrically, independent of declaration order; a full digest collision rejects.
Names follow the public 1-64 ASCII letter/digit/underscore/hyphen contract, descriptions are at most
8 KiB, and registries have at most 128 tools/1 MiB. Fingerprints include sorted original names,
aliases, descriptions, canonical schemas and native choices. Canonicalization sorts keys and removes
whitespace while preserving number spellings; a numeric spelling change conservatively changes the
fingerprint. No native tool adapter is enabled yet.

The broker seals only calls included in one response, up to the encoded batch budget. Calls arriving
later stay queued. Results cannot be accepted before successful HTTP handoff. The complete result set
is validated and encoded before any future completes; duplicate/partial/extra/cross-owner/late sets
consume no IDs. Results preserve text, supported base64 images and error status. Other client result
content is serialized as text without fetching or executing it. Cancellation/timeout completes pending
calls with safe tool errors, makes the relay unusable, and removes its private artifacts. Timer
callbacks verify that their own call is still pending, including callbacks racing a successful result.

Sources checked 2026-09-08: https://modelcontextprotocol.io/specification/2025-06-18/basic/lifecycle,
https://modelcontextprotocol.io/specification/2025-06-18/server/tools and
https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools.
MCP is explicitly added to AGENTS.md's permitted public protocol documentation under the user's
existing public-protocol authorization. No previous implementation is an input.

## D13: HTTP tool handoff and continuation ownership (review R04/R05/R10/R16)

Each ACP prompt owns its context across one or more HTTP responses. A successful tool-use response
only delivers a sealed batch; its Finish does not cancel the prompt. The following exact client
results resolve the relay and continue that same prompt without another `session/prompt`. A session
supervisor enforces tool/turn deadlines and process cleanup while no HTTP request is open. A scoped
terminal outcome retains only compatibility digest, pending IDs and safe failure class, for at most
five minutes. A correlated auth-expiry class takes precedence over the secondary relay disconnect.

Until Phase 5 reconciliation, continuation requires the exact previous history plus the delivered
assistant message and a user message containing only that batch's results. Changed system, registry,
requested model/effort or client metadata is incompatible. An ordinary new prompt while waiting is
busy; a completed or orphan tool-result request cannot start a fresh model prompt. Stable family
keys, truncated histories, shared-process routing and persisted resume remain Phase 5 work.

The HTTP adapter preserves text before tools and emits complete validated JSON argument deltas. It
buffers a tool batch until the backend confirms its tool-use stop, then writes blocks with sequential
indices and checks the encoded budget before tool emission. ACP tool status notifications do not
create tool-use blocks. Streaming and buffered responses describe the same ordered content, and
provider token counts remain zero when unreported.

Custom tools and `tool_choice` auto/none are implemented; none exposes an empty effective registry.
Required/specific tool choices, disabling parallel use, deferred tool loading, non-direct callers and
unsupported versioned typed tools are rejected before dispatch. Strict custom arguments are validated
before exposure, with no claim of backend constrained decoding. Security-affecting unsupported choices
are never silently ignored. The complete request-field table, live client shapes, media/native web
and exact enforcement of other provider-specific generation controls remain R14/R16/Phase 6 work.

The current executable has only internal `schema-worker` and `relay --config` commands. It is not a
finished launcher or an approved live Kiro profile. R06 execution restriction and all live release
gates remain open. Other Phase 0 proposals remain pending until implemented and tested.
