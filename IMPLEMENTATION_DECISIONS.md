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

The original Phase 2 text driver owned one process/session and admitted one response at a time. Before Phase 5
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

D39 subsequently observes a different envelope for the read-only tools command: its discriminator
is command, and args is an object. That observation does not validate the historical effort payload
above or establish effort's argument members. The current effort fixture remains an unverified
interpretation; no actual effort synchronization is claimed from the tools query. D99 later replaces
that effort payload using fresh unmodified-terminal wire observation and independent ACP controls.

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
not conversation bodies. D17 now defines the implemented exclusive leases and process-crash recovery.

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

The original Phase 4 continuation required the exact previous history plus the delivered
assistant message and a user message containing only that batch's results. Changed system, registry,
requested model/effort or client metadata is incompatible. An ordinary new prompt while waiting is
busy; a completed or orphan tool-result request cannot start a fresh model prompt. Stable family
keys, truncated histories, shared-process routing and persisted resume are now defined by D15-D17.

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

## D14: observed Claude gateway envelope and identity (review R10/R14)

The public [Claude gateway protocol](https://code.claude.com/docs/en/llm-gateway-protocol), checked
2026-09-08, documents session/agent attribution headers and discovery using `data[].id`,
`display_name` and `description`. The launcher must enable gateway discovery explicitly. Header
identifiers are opaque bounded strings, not assumed UUIDs; repeated, empty, whitespace, comma,
non-ASCII and overlong values reject before backend dispatch. Identity participates in pending-tool
compatibility, so a result cannot change its conversation headers. These headers do not authorize
requests; model authentication still precedes their use.

An unmodified installed Claude Code 2.1.263 ran in print mode against an independently written local
fixture gateway. It sent one model request with the expected session header and discovered both
synthetic catalog entries. The initial decoder failed on message roles `[user, system]`; a regression
now preserves text-only system updates after the latest user message. The decoder, gateway and prompt
projection retain this ordering and reject an all-system request, assistant prefill or tool effects
inside a system message. Public Anthropic documentation also describes mid-conversation system
messages on selected models. ACP has no equivalent role field, so this adapter preserves ordered
context without claiming the provider's native instruction-priority or prompt-cache semantics.

The opt-in test retains field names, content types, counts and Boolean assertions only. It uses a
private disposable profile/workspace, synthetic responses, bounded output/deadlines and an owned
process group; global client configuration fingerprints remain unchanged. It declares a client tool
but requests no tool effect. No Kiro/model-provider request is involved. Discovery-cache evidence in
print mode does not establish the interactive selector, multi-turn tool behavior, launch policy or
R06 restrictions. Those acceptance gates remain open.

## D15: request keys and committed history (review R10/R14)

An explicit profile-scoped client session/agent identity has separate main and title lookup families.
Tool follow-ups, retries and resume retain the main binding. The first-user anchor is verified during
history reconciliation instead of changing the explicit lookup key after client truncation. Fallback
keys include a random launcher instance, stable system and first-user digests; they never persist.
Title keys additionally include their input anchor. The title classifier requires all of: no tools,
disabled thinking (or omission as subsequently verified by D50), a JSON object schema with only a
required string title, and title/conversation intent in the system. Classification examines bounded
system/configuration inputs, not one loose substring. Broader actual-client title templates remain
an interoperability gate.

History uses domain-separated HMAC-SHA-256 with a private 32-byte scope key. Records contain ordered
assistant/user digest pairs and message-role anchors, never text. Text shorthand and text blocks are
normalized; object key order and content-block cache hints do not change meaning. Argument numbers
retain their exact spelling, and cache-like keys inside tool arguments are not stripped. Other
content fields and per-message system updates participate in continuity. At most 8,192 committed
message anchors are retained; exceeding the next-turn bound causes safe fresh history.

Strict extensions send only the uncommitted suffix, without resending the delivered assistant or
stable system context. The longest proven truncated overlap must contain user and assistant content;
an assistant-only match is insufficient. Completed duplicates and idle divergence create fresh
backend state. Active/pending divergence rejects. Pending tool results additionally require exactly
the sealed IDs and no new user/system content; successful resolution continues the original prompt.
D25 permits a strict repetition of the last complete standing system-message sequence.
Only final successful HTTP completion commits history. Ordinary model/effort changes remain idle-only.

The manager defaults to eight bindings and one-hour lazy idle expiration, with per-key admission and
short map locks. Idle bindings may be evicted at capacity; active and pending bindings cannot. Family
classification enums also reserve local-command, resume and retry outcomes for launcher/diagnostics
integration. This does not yet claim actual-client continuation with changing runtime system updates.

## D16: bounded shared processes and response barriers (review R07/R12)

The default pool has four processes, two lifetime session allocations per process, two retained idle
processes and a five-minute lazy idle TTL. Starting/retiring groups count toward admission until
cleanup completes. Each process owns an immutable launch configuration; a borrowed pool must match
the requested executable, arguments, environment, working directory, classifier and transport limits.
Behavior compatibility includes system context, the effective registry/native choices and policy
scope. Title work has a separate process policy family. Models may differ between independent sessions.

A single router owns each process's notification stream. `session/new`/load has a serialized temporary
owner; an established ID always keeps its existing owner. One unknown early ID may bind to creation,
but conflicting IDs or an inconsistent response retire the group. Per-lease queues are 64 events/
16 MiB by default. Every successful RPC crosses a router barrier before returning, so preceding
notifications cannot be overtaken by the final response. Load replay is discarded through that barrier.
Process failure wakes all attached leases and pending calls. Driver watchers discard idle state and
close relay artifacts; shutdown joins routing and owned watchers.

An idle lease can be disposed without stopping an active sibling. Its backend allocation and an ID
tombstone remain counted because public session deletion is optional and is not negotiated here.
Cancellation, ambiguous load, malformed state and transport corruption conservatively retire the
whole process. A full pool can retire its oldest wholly idle group for one bounded admission retry.
No path evicts an active sibling merely to reuse its slot.

The public [ACP session setup](https://agentclientprotocol.com/protocol/v1/session-setup), checked
2026-09-08, requires `loadSession` negotiation, historical replay before load completion, and permits
a null load result. The adapter also accepts object results used for model state. It validates any
returned ID, checks restored model/selector metadata, and confirms the selected model before a new
prompt. Unknown optional resume/close methods are not assumed from CLI version alone.

## D17: durable idle ownership (review R11/R12)

Persistence requires an explicit client session ID, a profile fingerprint, a pinned backend version
and compatible launch/system/tool/model metadata. A 0700 cache holds a random 0600 history key.
Version-1 idle records hold only key/profile/compatibility/launch digests, backend session ID,
validated catalog/current model/selector, history anchors and timestamps. The initial idle TTL is one
hour. No title or fallback-identity record is saved.

The cache has 256 fixed digest slots, each with one retained 0600 advisory-lock inode and at most one
2 MiB record. Full keys are always compared. A live slot collision returns busy; an inactive collision
loses cache locality and recreates safely. This bounds retained record data at 512 MiB; atomic writes
temporarily need another record per concurrent writer. File/link/owner checks apply to records, key
and lock files. Lock checks reject a replaced inode. Lock files are never unlinked to "unlock" them.

A nonblocking exclusive lease is acquired before reading and held throughout live ownership. Before
session load, model/effort mutation or any new prompt, the old idle record is removed and the directory
fsynced. Failure stops dispatch. Successful final delivery writes a new idle record by private atomic
rename and fsync. A failed cache save retains safe in-memory state and records persistence failure;
it cannot restore an old snapshot. Closing an idle driver releases the file lease only after its
backend/relay ownership is disposed. Abrupt-process tests verify OS lease release and that invalidated
data cannot reappear; they do not simulate a host power failure.

Resume requires a full strict extension of the stored anchors. A fresh relay accompanies load; replay
never appears in the current HTTP response. Failed, unsupported or inconsistent load retires its
process and performs one fresh full-history attempt. Authentication failure stops with the account
fallback, and overload does not trigger additional process attempts. If a valid load omits model
state, the compatible stored catalog is used with an explicit selected-model acknowledgement before
dispatch. Process/request crash and private-file adversarial coverage remains part of Phase 7.

## D18: instruction semantics and relay admission (review R07/R15/R16)

The public [mid-conversation system message contract](https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages),
checked 2026-09-08, includes standing and turn-scoped instructions plus optional per-message effort.
The adapter currently accepts only text and standing `clear_at: "never"` (or its default absence).
It rejects other lifetime values and per-message `output_config` before normalization, because
discarding them would change meaning. Tool-addition/removal content is also unsupported. The
installed-client shape probe did not observe either message-level field; that single sample does not
establish that the client never sends them. Full lifetime/effort support requires a separate mapping.

A local model/projection rejection before prompt dispatch discards only the affected pooled lease.
It restores that lease's idle accounting and releases it without signaling its healthy siblings.
Transport failure, cancellation and ambiguous backend state retain whole-process retirement.

Every relay starts with call admission closed. The driver opens it immediately before dispatch and
closes it atomically on the ACP prompt reply, before publishing completion to HTTP. Successful tool
handoffs keep admission open for the same owned prompt. Pending or validating calls at final
completion close the broker permanently and fail the turn; they cannot spill into another prompt.
Repeated shutdown still joins the existing cleanup. No client tool effect is performed by these tests.

## D19: negotiated inline media (review R12/R15/R16)

Prompt media accepts only inline base64 PNG/JPEG/GIF/WebP images, inline `text/plain` documents and
base64 `application/pdf` documents. URL, file-ID, audio, custom document content and enabled citation
requests reject before dispatch. Image/document source objects must have the exact supported fields.
Document title/context are optional strings bounded at 8/32 KiB. Citation settings may explicitly
disable citations; no structured citation support is claimed.

Limits are 20 media parts per request, 4 MiB decoded bytes per part, 6 MiB aggregate decoded media,
and image width/height between 1 and 8,000. Strict base64 rejects embedded newlines. Image MIME must
match its recognized header; header decoding obtains dimensions without allocating pixel buffers.
PDF MIME is checked by signature. These checks do not fully decode/render a file or prove a provider
can process every accepted payload. Kiro failures retain ordinary failure/cleanup semantics.

Images require negotiated `promptCapabilities.image`; PDFs require `embeddedContext`. Known
capability fields require exact names and Boolean types, with absent capabilities disabled. Unknown
future keys cannot enable a recognized feature by case-insensitive matching. Plain text documents
remain JSON-wrapped text with title/context even when embedding is absent. Binary document resources
use content-derived product URNs and inline blobs; no source URL or local filename is fetched.

Historical and current images stay native content blocks, with JSON role markers preserving message
and content order. Proven deltas retain only new content. The text-only projection remains unchanged.
The final encoded prompt envelope, including the largest permitted request ID, must fit the configured
ACP line ceiling before model/effort/prompt dispatch; rejection disposes only the affected lease.

Sources checked 2026-09-08: [ACP content](https://agentclientprotocol.com/protocol/v1/content),
[Anthropic images](https://platform.claude.com/docs/en/build-with-claude/vision),
[PDF source shape](https://platform.claude.com/docs/en/build-with-claude/pdf-support) and
[plain-text document shape](https://platform.claude.com/docs/en/build-with-claude/citations).
Exact x/image version, checksum and BSD-3-Clause/PATENTS review are in DEPENDENCY_REVIEW.md.

## D20: silent-stream keepalive (review R01/R04/R14)

The gateway uses public Anthropic ping events during silent streaming waits, with a 15-second default
and a configurable positive interval at most one minute. A ping can create the one message start and
commit headers before text; the auth marker is therefore predeclared as a trailer. No heartbeat is
generated for buffered responses. Pings have a reserved output budget and bounded write deadline.

Only each Next wait receives the shorter heartbeat context. Its expiration consumes no model event
and does not cancel the owned ACP prompt; another wait continues. The original first-usable-event and
total deadlines remain unchanged. No extra reader goroutine is needed. First/total expiry after a
ping emits the defined stream error, while recognized auth expiry appends login text and ends the
same message normally. Synthetic scheduled turns and the independent ACP child test these cases.

Source: [Anthropic stream event types](https://platform.claude.com/docs/en/build-with-claude/streaming),
checked 2026-09-08. Actual client streaming behavior remains a separate interoperability gate.

## D21: bounded status and foreground completion records (review R12/R14/R15)

Usage reads return a copied in-memory snapshot immediately and admit at most one asynchronous fetch.
The default TTL and failure backoff are 60 seconds, with a five-second fetch deadline. Failure keeps
last good numeric data and records only a safe failure class. Close cancels and joins the fetch; a
version-specific subprocess adapter must honor that context and own bounded group cleanup. No such
adapter is enabled for installed Kiro 2.21.1 because its read-only help does not establish a non-model
usage command. The cache's used/limit/remaining credit fields are normalized internal data, not a
claimed Kiro JSON wire schema. Missing fields are never inferred from other amounts.

The provisional private metadata adapter interprets `contextUsagePercentage`, `turnDurationMs` and
`meteringUsage: [{unit, value}]` on `_kiro.dev/metadata` params. This is an explicit interpretation of
the repository contract, not an observed live payload. Input is bounded at 64 KiB and 16 metering
entries. Only finite nonnegative context (at most 100), duration (at most one hour in milliseconds),
and credit/token values (at most 1e12) survive. Singular/plural unit labels normalize; unknown labels
and fields are discarded. Repeated values replace the turn's previous snapshot without summing.
Invalid optional fields do not erase independently valid diagnostics or fail ordinary text delivery.

Only final successful delivery publishes a completion. A tool handoff retains the same owned turn;
Finish is idempotent, and canceled, failed or undelivered responses publish nothing. Manager title
families and requests with an agent or parent-agent identity are excluded. Other main-family requests are the
initial foreground classification, pending broader actual-client observations. Records contain only
the keyed binding digest, namespaced selected model, optional multiplier, bounded local elapsed time
(including setup/tool wait/delivery; accepted ceiling two hours), created/reused/loaded state, safe
effort status and filtered private numeric metadata. Backend error prose and raw identity are absent.

The shared queue retains at most 32 records plus one latest snapshot, with oldest-first eviction and
a cumulative dropped counter. Sequence numbers increase without wrap through 2^53-1. The exact
UI-authenticated turn-metrics POST accepts only an empty body/object within 4 KiB and drains once;
it is best-effort diagnostics, so a failed response after draining may lose records. The latest
snapshot survives draining and remains in usage status when account usage is unavailable. UI request
admission and read/write deadlines are separate from model admission. No provider usage is changed.
D50 adds the launcher/client hook adapter and bounded finalization wait; D22 defines local estimates.

The public [Claude gateway attribution contract](https://code.claude.com/docs/en/llm-gateway-protocol),
checked 2026-09-08, identifies first-level subagents through the agent header; the parent-agent header
is only present for nested agents. A regression therefore excludes both, including an agent with no
parent header. A parent-only check would incorrectly publish first-level agent work as foreground.

## D22: labeled local token heuristic (review R12/R16)

`local_estimate` explicitly identifies an approximation using `utf8-bytes/4-v1`: round up the selected
UTF-8 byte total divided by four. It includes system/message text, tool names/descriptions and
canonical schemas, historical tool names/arguments and text tool results. It excludes every declared
image/document and thinking/redacted-thinking block, including nested media in tool results. It is
not a model tokenizer, and does not account for hidden backend context, framing overhead or media
costs. Provider usage remains zero when no compatible accounting is reported.

Visible output includes text and canonical tool arguments across all successfully delivered HTTP
handoffs belonging to the same ACP prompt, with one rounding step at the end. Only integer counts
are accumulated; no additional output transcript is retained. Final input context describes the
latest complete client request in that turn, including any matched tool results. A canceled or
undelivered final response produces no completion record or committed estimate.

Each foreground binding has a 128-entry FIFO computation cache holding only keyed digests and byte
counts. Exact repeated components avoid reparsing/canonicalization. Inputs are bounded at 4,096
messages, 128 tools, 65,536 blocks and 32 MiB of selected source representations. Larger/unsupported
estimation inputs omit this optional diagnostic rather than failing an otherwise valid turn. The
previous successful input retains at most 4,098 component digests/counts; output count overflow above
1 TiB similarly disables that estimate.

`logical_prefix_tokens` counts the common ordered component prefix against that binding's previous
delivered input. It describes local reuse opportunity even if backend state was recreated. It is
neither a provider cache hit nor proof of ACP session reuse; no estimate enters an Anthropic usage
field. Cache formatting/hint/media changes may conservatively reduce prefix recognition.

## D23: finite launcher subprocess checks (review R06/R12/R14)

Version/login/other noninteractive checks use a separate runner with two process slots by default.
Starting and retiring processes count until cleanup finishes. Commands require absolute executable
and working-directory paths, at most 512 arguments and 128 explicitly supplied unique environment
entries, with a 64 KiB combined command/environment budget. No environment or stdin is inherited.
The caller remains responsible for choosing its version-specific environment allowlist.

Stdout retains at most 64 KiB by default (configurable to 4 MiB); overflow fails the command and
retires its group. Stderr is drained without retention. Errors expose fixed classes and exit status,
never command arguments, output, credentials or subprocess error prose. Callers must parse bounded
stdout without logging raw account data. The default command timeout is five seconds; cleanup has
separate 100 ms graceful, 250 ms TERM and one-second KILL stages plus a bounded pipe-drain window.
The direct process is reaped and its group checked even when its leader exits first. Repeated Close
cancels and joins admitted work, and closed/overloaded runners cannot launch more processes.

This runner covers finite preflight checks, not interactive terminal ownership or live Kiro tool
restriction. Its independent child emits synthetic output/environment flags and creates its own
TERM-ignoring descendant. No Kiro model or client tool effect is used by these tests.

## D24: temporary client settings and owned routing (review R14)

The initial client adapter is pinned to observed Claude Code 2.1.263. Unsupported versions reject
until their contract is checked; a caller must verify the executable's reported version before using
the adapter. The client keeps the user's project as cwd. A newly created 0700 runtime contains a
private client configuration directory, scratch directory and 0600 host settings overlay. Cleanup
joins repeated callers, refuses a replaced runtime root and does not traverse links into user data.

Only the named user settings file is read, as an owned regular file without writable group/other
permissions or a final symlink/hardlink, at most 2 MiB. Missing settings mean an empty user layer;
malformed/duplicate JSON and invalid env values fail preparation. Its temporary snapshot preserves
permissions, hooks and other compatible settings at user scope. Old model selection, credential
helpers and provider/routing environment entries are removed from the snapshot. The source is never
written. Project/local settings remain loaded by the client. The initial host overlay omits
permissions, hooks and availableModels; D49 subsequently adds a startup notice hook with observed
user/project hook preservation. No safe/restricted/empty-settings-source/strict-MCP flags are added.

The initial environment permits only named OS/terminal/tool-socket variables, with HOME, TMPDIR and
CLAUDE_CONFIG_DIR supplied explicitly. The host sets the literal loopback HTTP URL, one ephemeral
model token in both supported auth variables (D115 later keeps only the Bearer token), provider-host
guard, discovery and no automatic retry or nonstreaming fallback. HTTP proxy variables are cleared;
loopback bypass, telemetry opt-out and automatic-update opt-out are explicit. D112 later adds the
client's unknown-model window opt-out. No remote endpoint, URL credential or unknown model alias is
accepted by this initial adapter. A separate UI credential is not part of the model connection.

The public [settings precedence](https://code.claude.com/docs/en/settings) and
[CLI reference](https://code.claude.com/docs/en/cli-reference) distinguish a temporary overlay from
disabling user/project sources. The [environment reference](https://code.claude.com/docs/en/env-vars)
documents the host provider guard and explicit telemetry opt-out; checked 2026-09-08. The unmodified
client probe verifies user/project hooks, local env precedence, user Read denial despite project
allowance, ignored conflicting project provider routes and byte-for-byte settings preservation.

This is the settings/environment component, not complete launcher acceptance. User plugin/skill/MCP
assets and global config preservation, managed deployments, interactive readiness/terminal ownership,
status-line integration and actual Kiro restrictions remain separate gates. The adapter must not be
advertised as preserving untested assets or as a bypass of organizational permission/model policy.

## D25: repeated client instruction suffix at tool handoff (review R10/R14/R16)

An unmodified Claude 2.1.263 tool-result request contained user, system, assistant, user, system roles.
The trailing system text repeated the preceding system message. Accepting arbitrary new system content
would change the meaning of an already-running ACP prompt; embedding it into tool output would also
alter that output's contract. Neither is adopted.

A valid pending-history extension must still start at its one next user message containing exactly
the matching results. It may end with the same complete, contiguous system-message sequence that
immediately preceded the delivered assistant tool handoff. Each message must match its existing
HMAC anchor, including order and compatible content normalization. A partial/reordered sequence,
an older instruction, new text or extra user/assistant content rejects before consuming a result.

These identical standing instructions are already in the owned prompt. Only their new client-history
anchors are retained; no second ACP prompt, extra MCP result content or tool effect is generated.
Ordinary new instructions remain supported on a subsequent idle turn. Changed runtime instructions
during a pending tool remain an explicit unsupported case, not silently discarded information.

The installed-client test uses the actual gateway, session driver, schema worker and MCP relay with
an independently authored ACP peer. Only the catalog listing is a static fixture. A client-owned
PreToolUse denial follows the public [hook contract](https://code.claude.com/docs/en/hooks), checked
2026-09-08. The peer requires an actual MCP result containing the synthetic hook denial and only one
ACP prompt; the test requires exactly two HTTP model requests and final idle state. It does not treat
a JSON-RPC error as proof of client permission enforcement or consume Kiro model credits.

## D26: pinned Kiro identity preflight (review R06/R11/R14)

Kiro 2.21.1 is the initial observed preflight version. Check both the configured `kiro-cli` and its
sibling `kiro-cli-chat` executable with `--version`, requiring the same exact version before identity
lookup. The allowlisted PATH contains that installation directory followed by standard system
directories; HOME is the intended Kiro account scope, while cwd/TMPDIR are product-owned. Each finite
command uses D23's bounds, and a canceled caller remains canceled instead of becoming a login error.

The installed `whoami --help` documents `whoami --format json`. Both binaries in this installation
returned one compact JSON object followed by a short non-JSON postamble. The version-specific adapter
accepts one leading object with duplicate-key/UTF-8/depth validation: at most 16 KiB, 32 fields and
64 KiB total command output. A postamble is bounded to 4 KiB/16 newline separators, never interpreted
or logged, and cannot contain another complete or object/array/string-looking JSON line. This
exception belongs to finite CLI output parsing, never the strict ACP transport.

The observed identity fields are accountType, email, region and startUrl. Account type and email must
be nonempty strings; optional region/startUrl, when present, must also be bounded strings without
control characters. Only their domain-separated HMAC with the private scope key survives preflight;
account values and output prose are not returned. Version mismatch, nonzero exit, timeout, malformed
or ambiguous identity fails startup. An unknown login result instructs `kiro-cli login` without
asserting that a timeout proves the account is logged out. No authentication-changing command runs.

Successful identity preflight establishes a current CLI identity/cache scope, not future token
validity or Kiro execution restrictions. A separate isolated-HOME probe used a newly authored empty
agent. Its finite validation command exited 0 with the existing HOME; ACP itself, started with the
empty HOME, failed initialization with a recognized authentication error and was cleaned up. No
`session/prompt` was submitted. Logged-in ACP/tool restrictions and clean-host release gates remain.

## D27: reviewable restricted-agent candidate (review R06/R14)

The candidate builder takes a validated registry and the owned relay executable/configuration paths.
It enumerates only `@dax_session/<alias>` entries in tools and allowedTools, with no wildcard or native
tool exception. The sole MCP server runs the execution-free relay. Resources and hooks are empty,
and includeMcpJson is false. No client tool names, descriptions, schemas or conversation text are
copied into the agent's prompt; the exact registry fingerprint participates in its policy digest.

The compact JSON is limited to 64 KiB and written atomically with mode 0600 under a private
`.kiro/agents` directory in the supplied product-owned workspace. The policy digest includes the
Kiro version, registry and relay binding; its name is deterministic for those inputs. Existing
different content and directory links reject instead of overwriting or traversing outside data.

This function produces an unverified candidate and starts no process. Its current evidence is the
independent alias/file tests only. Literal MCP-reference validation, effective built-in/hook/MCP
suppression, launch-profile lifetime and pooling/load behavior still require interoperability work.
The installed ACP help describes --agent as selecting the first session's agent; later sessions must
not be assumed to inherit the same restriction. Successful syntax validation is never an execution
restriction proof or permission to enable real model traffic.

The later 2.21.1 negative control confirms that `agent validate --path` exit status cannot establish
even that a tool-list type check succeeded: the candidate and the same file with tools replaced by
the number 42 both exited 0. The observation uses `/usr/bin/false` as its declared MCP executable, so
unexpected validation-time execution cannot perform a client tool. This is evidence of an unreliable
validation exit status, not evidence that malformed tools were effectively enabled or rejected.
Neither this utility nor its success code may be used as the R06 startup gate.

The follow-up probe checks both pinned binaries, `kiro-cli` and `kiro-cli-chat`. Their public agent
help advertises create/validate/list/edit. Both still return zero and empty stdout for both candidates.
For this synthetic validation probe only, a fixed shell wrapper uses quoted positional arguments to
combine stderr into D23's 64 KiB stdout capture. Only fixed markers, byte counts and line kinds are
reported; raw diagnostics are never logged. The well-shaped candidate produces no diagnostics. The
numeric tools control produces 214 bytes with `invalid type`, `expected a sequence` and `error`
markers. This establishes where the parse failure is reported; it does not establish native-tool,
hook or inherited-MCP restrictions. Production preflight stderr remains discarded.

The [official 2.x reference](https://kiro.dev/docs/cli/2x-reference/), rechecked 2026-09-08, lists
`includeMcpJson` among 3.0 additions and does not document `allowedTools`. Those fields in the candidate
therefore remain unproven for the pinned v2 engine even though the candidate produces no parse error.
Installed 2.21.1 advertises v2/v3 engine selection; no engine upgrade or 3.0 permission semantics are
adopted. The read-only `/tools` surface documented for 2.x is a possible effective-policy observation,
not a model prompt or an already-passed restriction gate.

## D28: attached client lifetime and terminal ownership (review R07/R12/R14)

An attached client has one owner and one active process slot, an explicit validated command/environment,
and three caller-owned file descriptors. Descriptors go directly to the child, so blocked terminal,
pipe or file I/O creates no parent copy goroutine or retained transcript. This is the client UI path;
finite Kiro checks still use D23's bounded capture/discard policy. The owner never closes the caller's
descriptors. The default client lifetime is 24 hours, with a positive configurable ceiling of seven
days. Cancellation and owner shutdown stop admission and join owned cleanup. Normal leader exit
also triggers descendant cleanup. Grace/TERM/KILL use the same implementation and default stages as
D23: 100 ms, 250 ms and one second; each configured stage is at most five seconds.

Foreground mode requires the input descriptor to identify the caller's current foreground controlling
terminal. One terminal owner is permitted across the process; invalid/nonforeground terminals fail
before client execution. A private close-on-exec duplicate retains the terminal handle for cleanup.
Go's `SysProcAttr.Foreground` gives the child a new foreground process group, with no terminal data
proxy or emulation. The original group and termios settings are restored after process cleanup and
after failed exec. Window size is shared directly with the client; the owner does not consume input.

Restoring the foreground group from a background parent must not alter process-wide SIGTTOU handlers.
The executable handles the exact internal `internal-terminal-reclaim` argument by immediately exiting
successfully, before settings or application work. During cleanup the parent starts that same binary
with Go foreground process attributes and the original group, then restores termios from the now
foreground parent. The helper receives no account/provider environment or terminal input/output. Its
only environment entry disables the race runtime's exit sleep for instrumented tests. A one-second
deadline and one-second WaitDelay bound it. Since it joins the parent's group, timeout targets only
the helper PID, never the shared group. A failed helper is reported; when the original group was
nevertheless restored, termios restoration is still attempted. No group IDs or tty descriptors come
from command-line text, and direct invocation of the helper command has no authority or effect.

The macOS fixture uses the system `script` utility with `/dev/null` as its transcript destination and
an independently authored client inside a disposable pseudo-terminal. It verifies normal/nonzero/
forced exit, exec failure, foreground exclusivity, restored settings, and preservation of both an
existing SIGTTOU handler and an ignored disposition. macOS's PENDIN flag is pending-input state;
comparisons exclude only that state bit while checking all persistent settings. No user terminal or
live model is exercised. The Linux adapter uses platform-specific termios constants; runtime
portability, shell job suspension/resumption, actual interactive Claude initialization and launcher-wide
gateway/session cleanup remain separate checks. This component does not open the R06 live-model gate.

Sources checked 2026-09-08: [Go process attributes](https://pkg.go.dev/syscall#SysProcAttr),
[Go signals](https://pkg.go.dev/os/signal),
[Apple foreground-group contract](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man3/tcsetpgrp.3.html),
the installed macOS 15.4 `script(1)` and `termios(4)` manuals, and the reviewed x/sys API documentation.

## D29: owned HTTP server and connection admission (review R07/R12/R14)

`StartServer` owns its listener, HTTP serving lifetime and shutdown. It validates the gateway and
limits before listening, defaults to an ephemeral loopback address, and retains the explicit
unsafe-network requirement for any other bind. Listening/name resolution has a five-second setup
context. The listener admits at most 64 accepted sockets by default (configurable 1–1024) before
HTTP parsing; excess sockets close immediately. Header reads default to five seconds, request reads
to the gateway's 15 seconds, and keepalive idle time to 30 seconds. Header memory is limited through
Go's 64 KiB MaxHeaderBytes setting, with the standard parser's small read allowance. The gateway's
16 MiB body and separate active model/UI request bounds remain in force. HTTP/1 is selected explicitly;
no TLS, cleartext HTTP/2, upgrade or hijack route is added.

Per-write/flush deadlines cover health, authentication, catalog, UI and model output. They use the
gateway's five-second default and refresh at the actual write; neither model deadline is extended.
The server's default error logger discards raw parser/panic diagnostics instead of sending request
or internal values to unrestricted stderr. Public server errors contain only fixed failure classes.
Structured application diagnostics remain a separate launcher feature.

Parent cancellation, explicit Close or a serving failure stops admission and cancels the base request
context before HTTP draining. A shielded five-second Shutdown window is followed by forced closure
of all remaining sockets, then a one-second join window. The configurable maxima are ten and five
seconds respectively. Tracking includes accepted sockets, in-flight handlers and Go's connection
state lifecycle; closing a descriptor alone is not treated as joined request handling. Close/Wait
callers share the same recorded result. A backend that ignores cancellation produces a bounded
cleanup error, never a successful cleanup claim; Go code cannot forcibly terminate an arbitrary
handler. Production adapters must retain their own cancellation and bounded cleanup contracts. A
successful tool handoff may have no open HTTP request, so the launcher must also close its session
manager, relay/schema owners and profile; the HTTP server does not take ownership of those components.

Independent TCP tests cover the auth/SSE contract, pre-header socket capacity and slot reuse, partial
headers, oversized headers, unauthorized incomplete bodies, keepalive expiry, forced closure of new
connections, canceled streaming and repeated Close. A deliberately non-cooperative catalog tests
the bounded error path and is explicitly released by the fixture afterward. A real loopback request
through the session driver and independent fake ACP emits only its synthetic owned PID before
suspending. Server Close must return with no handler, no socket, an unstarted session and no surviving
ACP process group. This is local lifecycle evidence, not live Kiro or complete launcher acceptance.

Primary API source checked 2026-09-08: [Go HTTP server contract](https://pkg.go.dev/net/http#Server),
including Shutdown, BaseContext, ConnState, Protocols and ResponseController.

## D30: finite public CLI catalog decoding (review R11/R14)

The launcher can read the pinned 2.21.1 catalog with `chat --list-models --format json`, after checking
both main/helper versions with the same allowlisted command constructor as D26. This operation sends
no prompt and creates no ACP session. Identity/login checking is still a separate mandatory startup
step. A canceled caller runs no listing command; command/output failure yields a fixed catalog error.

The independently observed shape is one complete JSON object with default_model and a models array.
The decoder accepts at most 64 KiB, 16 fields per relevant object and 256 advertised entries. It
rejects duplicate keys/IDs, malformed or additional JSON/prose, empty catalogs, invalid names and
descriptions, and a default absent from the advertised list. In contrast to public ACP's explicit
current selection, this CLI default cannot synthesize a missing model entry. model_id/model_name/
description map into the existing catalog and its exact reversible client aliases.

Optional rate_multiplier and rate_unit are type/bounds checked, but do not populate credit multipliers
until the rate-unit meaning is independently established. The first fixed unit-enum probe recognized
none of the 19 observed values; it is not evidence that the CLI lacks rate metadata. Context-window
metadata is not used to invent a tokenizer, billing, or ACP capability. Compatible unused fields stay
irrelevant to routing. No provider usage response changes.

The pinned live read-only adapter verified 19 advertised models, membership of the default and exact
alias round trips. This is an available discovery source, not proof that every ID has the same live
ACP representation or that a client model selector is fully integrated. Cache use must retain D11's
complete identity/TTL rules and compare actual session model support before dispatch; no approximate
ID mapping or fallback is introduced. The public CLI help/black-box JSON observation is the wire
source; the synthetic tests use newly invented names, descriptions and metadata rather than copying
the installed catalog as a fixture.

## D31: prepared process and session policy ownership (review R06/R07/R12)

An explicit prepared acquisition reserves a pool process slot before invoking its preparation
callback. Overload, invalid input, prior cleanup failure and closed/canceled admission do not invoke
the callback. The callback shares the existing bounded setup context and transfers any partial
artifacts even when it returns an error. Ordinary acquisitions retain the immutable launch template
and existing shared-session behavior. Prepared processes have exactly one lifetime session allocation:
they never share another prepared or ordinary acquisition, including one with the same compatibility
scope. Their one session can still serve successive compatible turns while idle. The global process,
idle and TTL limits apply to both paths together.

Retirement joins the ACP process and notification router, then invokes owned-artifact cleanup once
before freeing its process slot. Cleanup does not receive the canceled caller/setup context. Internal
preparers must honor their setup context and implement finite cleanup; this is not an interface for
untrusted extension code that can ignore those contracts. Partial preparation/start failure also
joins cleanup. A repeated idle release waits for the same retirement and returns the same error.
The pool retains its first cleanup failure after removing the retired group, refuses subsequent
admission, and reports that failure on Close. It cannot silently accumulate further failed artifacts.

The session extension passes only the validated current registry, relay executable and owned relay
config path to a preparer. Its relay admission remains closed during setup. The extension can append
launch arguments and choose a separate process cwd; it cannot replace the transport executable,
environment, authentication classifier, client identity or limits. Configuration slices are copied.
The public ACP session cwd remains the original project. An internal adapter may explicitly declare
that its independently verified launch profile binds this exact relay, in which case session/new
receives an empty MCP list rather than a duplicate server declaration. Otherwise the existing public
session MCP declaration is retained. No HTTP authority, conversation text or client request identity
is passed into process preparation.

Policy changes, including tool_choice none, retire the old prepared session and its launch artifacts
before preparing the next one. Unrelated sessions remain owned independently. Successful HTTP tool
handoff retains the same launch artifacts, relay and ACP prompt until final delivery or cancellation.
A manager supplies one common process-capacity owner to all prepared bindings. A standalone prepared
driver must be given an explicit pool. Prepared session persistence is rejected at configuration time;
restoring a launch profile or relay binding requires separate proof before enabling session/load on
this path. The already-tested public shared-pool/load path is unchanged.

This closes the internal lifetime wiring gap, not the Kiro execution-restriction gate. No production
Kiro preparer, user-controlled verification bit, trust flag or unverified-model bypass is added.
D27's candidate remains unverified. The independent fake has its own invented launch manifest and
MCP binding; it is not a claim about Kiro's config precedence, hooks, allowedTools, inherited MCP or
session/load behavior. The pinned CLI's first-session agent selection motivates the conservative
single-session launch path, while actual restricted-policy evidence remains required under R06.

## D32: local client runtime ownership (review R04/R07/R12/R14)

The internal RunClient stage takes exclusive ownership of its already-constructed backend, schema
pool and optional gateway usage cache on entry, including invalid configuration and startup failure.
Binary/version, account, model and effective Kiro-policy preflight belong before this stage; passing
an adapter does not establish it as verified. No live CLI entry point or R06 bypass is enabled.

This local-client stage generates new model/UI credentials, starts its bounded loopback HTTP server,
prepares D24's isolated client profile using the actual listener URL, and starts the D28 attached
client with explicit caller-owned descriptors. Conflicting caller-supplied profile routing/credentials
or HTTP backend/tokens reject. This stage requires loopback operation. The underlying standalone
server's explicit unsafe-network contract remains separate. No credential or client transcript is
returned in its result. Results contain only the child PID/exit status and elapsed gateway, profile,
process-launch and cleanup times. Process launch is not reported as actual client initialization.

Client exit, parent cancellation or premature server termination ends the runtime. Parent cancellation
keeps its cancellation cause even when the child finishes concurrently. Cleanup cancels server request
contexts, stops the attached client, closes the backend and cancels usage refresh concurrently. Backend
closure is unconditional: a completed HTTP tool handoff may still own an ACP prompt and suspended
relay calls without an open HTTP response. All these owners join before schema shutdown and removal
of the private client profile. Caller descriptors remain open. Existing component limits bound this
internal stage; adapters must honor their own finite cancellation and Close contracts.

Cleanup attempts continue when an owner reports a failure. The runtime returns a fixed cleanup error
instead of exposing an adapter's arbitrary error text. Client startup/exit and parent cancellation
retain their existing fixed error classes. Cleanup must not be reported successful merely because a
profile was removed or an HTTP connection closed.

The independent HTTP client fixture uses only standard packages and its explicitly supplied loopback
gateway. It submits new synthetic requests and, in the completion case, returns a synthetic tool
result without executing anything. Tests compose the real gateway, session manager, schema worker,
independent ACP and execution-free MCP child. They cover normal text, final tool completion, client
exit/cancellation while awaiting a tool result, repeated caller cancellation, startup failures,
usage cancellation and sanitized cleanup failure. Owned profiles remain present through backend
shutdown, disappear afterward, and source settings/descriptors are preserved. The fixture's readiness
line is an invented test control, not a claim about Claude's interactive UI or initialization.

Catalog/cache/last-model selection, effective restricted Kiro launch preparation, complete client
asset preservation, status-line credentials/hooks, user-facing timing output and final CLI wiring
remain separate work. No live model request or installed-client run is implied by these tests.

## D33: unsupported request controls and observed client rejection (review R16)

Known request controls without an implemented mapping must not silently disappear. The decoder,
session manager and standalone driver reject the controls below before backend dispatch, binding
eviction, process preparation, history mutation or consumption of a pending tool result. HTTP returns
400 with the normal invalid_request_error envelope, including for a streaming request before SSE
starts. The diagnostic names only a fixed protocol field; it never includes the supplied stop text,
server URL/token, container identifier or location. Execution directives are checked first in a fixed
order. Compatible metadata and unknown compatible extensions retain their existing handling.

This is the current request-field inventory, not a declaration that R16 is complete:

| Field | Current implemented behavior | Remaining boundary |
| --- | --- | --- |
| model | Validate syntax, resolve exact catalog alias, synchronize while idle | Live catalog/ACP identity and client selector gates remain |
| messages, system | Validate bounded ordered roles/content and project the supported text/media/tool subset | D18/D19 limitations apply; assistant prefill, signed thinking and unsupported blocks reject |
| stream | Boolean; equivalent supported buffered/SSE message content | No extra generation control is implied |
| tools, tool_choice | Validated custom registry; auto/none only; schema validation before exposure | Required/specific/disabled-parallel choices and unimplemented typed tools reject under D13 |
| stop_sequences | Only absent or an empty JSON array is accepted | A nonempty list is rejected; the adapter does not enforce custom stops |
| temperature, top_p, top_k | Reject whenever present, including null or a nominal default | No sampling synchronization is implemented |
| mcp_servers | Only absent or an empty JSON array is accepted | A client body cannot request remote server-side tool execution |
| container | Absent/null accepted; every supplied identifier or object rejected | No provider container, skill or execution environment is mapped |
| inference_geo | Absent/null accepted; every supplied location rejected | The adapter cannot attest to a requested inference location |
| service_tier | Reject whenever present, including auto | No requested Anthropic service tier can be asserted for Kiro |
| metadata | Retained but not projected, used as identity, or logged | Authenticated headers remain the identity source |
| cache_control | No provider caching action or billing attribution | D22 offers only separately labeled local estimates |
| output_config.effort | Normalize recognized effort; malformed effort warns and is ignored; optional Kiro synchronization follows D09 | Actual private extension behavior remains a live gate |
| max_tokens | Required integer in 1..1,048,576, shape checked | Provider token maximum is not enforced; gateway byte/deadline limits are different bounds. R16 remains open |
| thinking | Retained; disabled participates in narrow title classification | No provider reasoning/budget control is mapped. Rejection recovery is not established |
| context_management | Retained, not mapped | No requested context-edit semantics are implemented. R16 remains open |
| output_config.format and other output controls | Retained; the narrow title shape participates in classification | No structured-output constraint or task budget is enforced. Title isolation tests are not schema-conformance tests |
| Other compatible fields | Retain in Extra without backend actions | Future fields affecting execution, routing or output semantics need an explicit decision |

This inventory exposes existing gaps rather than legitimizing them as fully compatible behavior.
Reasoning, structured output, context management and a truthful max_tokens contract must be resolved
before claiming complete client compatibility. No production retry setting or client feature is
disabled by this decision, and no generation control is simulated by rewriting tool permissions.

Sources checked 2026-09-08: the public
[Messages request reference](https://platform.claude.com/docs/en/api/messages/create) describes
sampling, stop, container, location, service tier and output declarations;
[MCP connector documentation](https://platform.claude.com/docs/en/agents-and-tools/mcp-connector)
defines server declarations. The documented
[ACP prompt turn](https://agentclientprotocol.com/protocol/v1/prompt-turn) does not establish matching
controls for the installed Kiro adapter. The rejection policy is this project's conservative subset,
not an assertion that every public ACP agent or future extension lacks those features.

The public [Claude gateway protocol](https://code.claude.com/docs/en/llm-gateway-protocol) describes
wording-sensitive capability recovery and adaptive thinking on unfamiliar gateway aliases. Six local
black-box cases on unmodified Claude Code 2.1.263 establish only the following narrower evidence:
the same owned profile completes a synthetic response when no error is returned; a generic thinking
rejection, an extra-input rejection, an enum rejection and a newly invented thinking token each yield
one adaptive request and exit 1 without recovery. Increasing the generic case's retry setting from
zero to two, in both environment and owned settings, does not change that result. The thinking token
is an experimental fixture value, not a documented token or an adopted production error contract.

These are negative observations, not proof that this client can never recover from a correctly
recognized rejection. The initial recovery assertions failed; the named observation test now requires
those failures and a successful positive control. A passing observation suite does not pass the
recovery acceptance gate. Each case has an empty owned HOME/project, disabled client tools, a finite
process deadline and bounded output, and a local synthetic HTTP responder. It records only fixed
field/kind enums, counts, exit status and output length. No client source, Kiro prompt, live inference,
real tool effect or private user profile is used.

## D34: prepared catalog and completed-model preference (review R11/R12/R14)

The internal launcher prepares its catalog before opening the client gateway. The existing cache
retains its full identity and 24-hour TTL, bounded/coalesced asynchronous refresh, failure backoff
and last-good behavior. Startup selects an exact backend ID in this order: an explicit initial model,
a compatible last-used preference for an interactive launch, then the catalog's advertised current
model. An unavailable explicit choice fails; it is never replaced by a saved or approximate model.
An absent/removed saved choice can use the current model. A catalog with no selectable current model
requires an explicit valid choice or saved preference. Selection returns the exact client alias,
source, initial stale flag and measured catalog-loading duration, without modifying client defaults.

Last-model record schema 2 keeps executable/version, profile, agent and capability identity but omits
the one-launch model/effort overrides from the preference digest. Those overrides still participate in
catalog discovery identity. This corrects the reproduced case where removing a previous launch's
explicit options made its last-used preference unreachable. Version 1 preferences are ignored rather
than migrated. Existing private atomic file limits and exact current-catalog membership checks apply.
No prompt, client conversation identifier, tool value or credential enters the preference record.

The optional prepared-model owner is transferred to RunClient on entry, even on invalid startup.
Its selected alias supplies the client profile; a second caller-supplied profile model rejects as
conflicting authority. The gateway's model-list method uses this prepared cache, without creating an
ACP discovery session. Inference still goes to the configured backend, which must check actual ACP
model support before prompting. A stale advertised alias cannot authorize fallback or override a
backend rejection. Callers still construct the session model/effort configuration from the explicit
launch contract; the model cache does not mutate an existing manager's configuration.

A response wrapper saves only after a final end_turn/max_tokens/refusal event and successful response
delivery via Finish. It resolves the actual backend-reported client model through the compatible
catalog. It does not assume the original requested model was used. Tool handoff alone, cancellation,
early Finish, authentication fallback, title generation, identified child/parent-agent work and
noninteractive launches do not update the preference. A completed foreground tool continuation can
update it. Repeated Finish/Cancel has one winner. No new discovery occurs on the save path. A missing
actual catalog model or local write failure sets a sticky Boolean diagnostic and does not invalidate
an already-delivered response. The failure's arbitrary text is not retained or logged.

Runtime shutdown joins HTTP handlers, the client and backend before closing the prepared catalog;
those handlers can still record a delivered final response during shutdown. Catalog Close then
cancels and joins its bounded refresh before private client profile removal. Repeated closure joins
the same cleanup. The internal model owner uses caller-provided stable policy/profile/capability
digests, not D27's volatile per-session candidate file digest. Constructing the real stable Kiro
identity remains part of verified launcher preflight; this API does not confer execution authority.

Independent tests cover selection precedence, exact aliases, removed preferences, unknown explicit
choices, the one-launch identity regression, title/agent/tool/cancellation/error delivery cases,
unadvertised actual models, atomic-write failure, stale refresh coalescing and shutdown. The independent
HTTP client now optionally requests the cached model list and verifies its selected alias before its
synthetic message. Full runtime tests retain model state through backend closure, save final text/tool
completion, discard abandoned tool handoffs, close the cache on startup failure and preserve source
settings. No installed Kiro/model request or client selector UI is exercised by this composition.

## D35: public startup composition with a mandatory policy gate (review R06/R11/R14)

The executable now exposes doctor, models and run, plus help/version. Exact relay, schema-worker and
terminal-reclaim invocations retain their separate dispatch. Public launch options cannot select a
backend, supply a policy callback, assert execution verification or bypass the gate. The built-in
Kiro 2.21.1 adapter always returns ErrPolicyUnverified until R06 supplies effective restriction proof.
Thus a successful doctor/model command is a diagnostic result; it does not authorize model traffic.
Run returns exit 3 after successful preflight while this gate is unresolved. This is an intermediate
product state, not a completed launcher acceptance gate.

Startup validates bounded options and the source settings snapshot, resolves executables from an
explicit PATH or absolute override, creates one private runtime, checks the pinned client version,
then performs the existing pinned Kiro version/account checks. A 60-second setup context bounds
startup. Version/identity commands retain five seconds each; only the exact public catalog-listing
command receives fifteen seconds. An installed observation produced complete output but did not exit
within five seconds, then exited normally at 8.559 seconds under the larger observation budget.
The catalog still requires a successful exit: output from a timed-out process is discarded. Caller
deadlines remain authoritative, and the existing cache refresh remains bounded at thirty seconds.
Startup prepares the exact catalog selection
before checking the policy gate. No listener, client, ACP session or schema worker starts before that
gate. Diagnostic commands may retain the private catalog and scope key; they do not save last-model
preferences or modify source client settings. Current account-usage refresh remains unavailable.

A private 32-byte random scope key survives startup under a retained file lock and atomic write.
Malformed, linked, insecure or changed state rejects without regeneration. Concurrent initialization
either returns the same key or a bounded state/lock failure. The profile digest includes the keyed
normalized home and verified account scope. Catalog identity also records the executable/version,
explicit model/effort, and versioned labels for the still-unverified policy and unknown ACP capability
scope. Neither label asserts negotiated support. Policy/capability identity must change when evidence
and an actual adapter are adopted; D27's ephemeral relay paths never enter this stable identity.

The internal composition creates schema and session owners only after an available policy plan.
The session manager owns its pool, uses the stable history key and the original ACP project cwd,
and receives the session-scoped preparer. Prepared persistence stays disabled under D31. One metrics
queue is shared by the manager and HTTP gateway. RunClient receives backend/schema/models ownership
on entry, including failure; it retains the source profile and D34 delivered-model behavior. The
outer startup owner joins catalog refresh before closing its finite runner or removing runtime files.
An observed root replacement causes an explicit cleanup failure and is never recursively removed.

Tests can inject a package-private synthetic policy/service; this path has no production flag,
configuration record or environment selector. Independent executable fixtures exercise the same
startup/manager/HTTP/profile/runtime path for text, completed tools, abandoned tools and cancellation.
The public command is separately executed against fake version/account/catalog CLIs to verify JSON
diagnostics, model listing and the mandatory rejection. These are not Kiro restriction observations.

Diagnostics contain fixed error classes, validated model aliases and bounded named phase durations.
Usage/configuration errors return 2, unverified policy 3, cancellation 130 and ordinary preflight
failures 1; actual client exit status is preserved. Cleanup failure remains visible alongside another
failure. Parent cancellation arriving during final startup cleanup remains visible to API callers.
Timing output is emitted when the command returns; client process launch is measured, while
client session initialization remains explicitly unverified. Interactive readiness, live policy/load
proof, R16 generation semantics, client assets/status bridge and release gates remain outstanding.

## D36: configuration and capability observations without policy adoption (review R06/R14/R16)

The public [Kiro 2.3 changelog](https://kiro.dev/changelog/cli/2-3/) establishes KIRO_HOME as a 2.x
configuration-root option, including global agents and settings. It does not establish authentication
continuity. The [2.10 changelog](https://kiro.dev/changelog/cli/2-10/) documents
chat.disableInheritingDefaultResources and live agent/MCP configuration reload. Empty resource lists
and a one-time inventory therefore cannot by themselves establish exclusion throughout a session.
Exact search/merge precedence, effective hooks/native tools and attempted-dispatch restrictions
remain observations to obtain on the pinned 2.21.1 installation. These sources were checked
2026-09-09; newer 3.x fields are not substituted for 2.x proof.

An owned-HOME inventory probe stops on authentication failure instead of substituting the real HOME
or copying account files. A separately identified read-only identity experiment may retain the
existing HOME and vary owned KIRO_HOME roots, comparing only ephemeral keyed identity scopes in
memory. Even equal identities would not establish agent selection or execution restrictions. Neither
experiment enables the production policy adapter or starts a model turn. The probes do not copy or
directly modify credentials or invoke login/logout; installed CLIs may maintain account/cache state.

The pinned main binary's read-only JSON getter distinguishes independently written flat Boolean
values at KIRO_HOME/settings/cli.json while the existing HOME supplies its normal environment. Use
the explicitly advertised --format json for this observation; the default is Markdown, and interpreting
it as a bare Boolean was a test mistake. Account-backed agent inventories and settings getters did
not exit within the initial five-second bounds; only these opt-in observations allow fifteen seconds
for those exact commands. Their version/help checks remain five seconds, with bounded whole-probe
deadlines. No production subprocess budget or setting is changed. Empty-HOME settings commands still
fail with missing-file diagnostics, and setting readback does not prove effective resource exclusion.

The installed ACP help also advertises v3 and --auth-method cli while retaining v2 as default.
An engine-specific v3 investigation is possible without assuming that 3.x configuration fields apply
to the current v2 adapter. No engine switch, v3 startup or restriction approval follows from help.

The public [Claude environment-variable reference](https://code.claude.com/docs/en/env-vars)
documents thinking omission and experimental-beta suppression. Local synthetic-response observations
confirm their request-shape effects for Claude 2.1.263; beta suppression still leaves four header
values in the tested profile. These are test inputs, not launcher defaults. Thinking omission does
not prove a model's internal reasoning stops, and beta suppression is not a universal capability
negotiation mechanism. No structured-output request was exercised. The existing title classifier
initially required an explicit disabled-thinking declaration; D50 separately checks actual title and
foreground paths before accepting omission with every other signal. Tool, permission, compaction and output-constraint
semantics remain separate gates.

Advisory Claude output is checked against the specifications and observations before use. A
correlated relay/client-denial/sentinel experiment is a useful restriction test design, but passing
one path cannot discharge R06. No silent constraint deletion, fabricated provider token limit,
unrestricted payload recording or public verification bypass is authorized by that advice. The
existing unsupported-control policy and all live startup gates remain in force.

## D37: configured MCP inventory is not effective session inventory (review R06/R14)

Pinned Kiro 2.21.1 exposes mcp add and mcp list through its public main/helper executables. The
installed add help distinguishes --scope for a standalone configuration from --agent for a named
agent. Under a completely synthetic HOME, the main executable's add help failed with a nonzero exit;
the adjacent, version-checked helper completed the same help and writer commands. This observation
does not change the production executable or establish the reason for the main/helper difference.

The helper writes a workspace-scoped server into work/.kiro/settings/mcp.json. For a separately
authored local agent, --agent writes its mcpServers entry into that agent's existing JSON file.
Both disabled and enabled cases use only /usr/bin/false as their server command. The disabled field
is true for --disabled and omitted without it; the public
[MCP configuration reference](https://kiro.dev/docs/mcp/configuration/) documents the omitted default
as false. Only newly produced files inside the owned probe root are inspected, with rooted reads,
no followed links, and explicit entry/depth/file/byte bounds. No real HOME setter is used.

The same helper's mcp list workspace displays the named agent and its server in both states, but
does not display the server written into the standalone workspace MCP file in either state. These
positive and negative controls separate the observed inventory distinction from disabled filtering.
A separate main-binary observation with the account HOME also omits all six independently seeded
standalone-file markers across two owned KIRO_HOME roots and two documented filename candidates.
That absence cannot establish that these files are unused or excluded during session creation.

The tests record the pinned observation, including its negative controls; they do not turn the
initial failed full-inventory assertion into a passing restriction gate. Named-agent listing is not
agent activation, MCP initialization, effective tool enumeration or execution denial. In particular,
this list cannot discharge inherited-MCP exclusion. No session/new, session/prompt, server status or
connect command is sent, and no real client tool effect is performed. Any unexpectedly invoked
declared server can only run false. Production startup still returns ErrPolicyUnverified.

The public [ACP extension reference](https://kiro.dev/docs/cli/acp/) describes command availability
after session creation and a separate commands/execute request. A future bounded /tools inventory
observation must therefore first establish the owned session's setup inputs and inspect advertised
commands, without converting a slash command into a model prompt. Its exact payload/result, inherited
configuration behavior and attempted native-tool denial still need independent evidence. Neither
the newer documentation nor these finite CLI results establish v3 engine or 3.x permission support.
Public documentation was rechecked 2026-09-09; no previous implementation was consulted.

## D38: main/helper identity failure under synthetic HOME (review R06/R14)

D37's helper-only configuration commands work without establishing an authenticated HOME. A
separate identity comparison now verifies both pinned binary versions, a baseline using the account
HOME and an owned KIRO_HOME, synthetic-HOME main and helper identity invocations, then the original
baseline again. Only exact --version and whoami --format json commands are admitted. At most twelve
commands run, each with five-second timeout, 64 KiB output capture and bounded group cleanup, under
a seventy-second whole-probe deadline. Nothing starts an agent, ACP session or model prompt.

One ephemeral HMAC key compares normalized identities without logging values or digests. The account
baseline succeeded before and after with equal identities. Both synthetic-HOME identities returned
exit 1 and failed verification, including the direct helper invocation. Thus changing to the helper
did not repair this observed identity-continuity failure. It is not evidence that the user's actual
account is logged out, that configuration listing requires login, or where credentials are stored.

The installed opt-in identity test remains a failed continuity gate for the two synthetic HOME
cases, rather than converting their failed verification into successful authentication evidence.
The command wrapper redirects only the test's explicit helper whoami call; production preflight
still uses its original main executable and HOME contract. No credentials are copied or directly
modified, no login/logout runs, and no production policy restriction is relaxed. Installed CLIs may
maintain their own account/cache state. R06 still requires effective configuration/tool proof with
an authenticated setup; D36's account-HOME plus owned KIRO_HOME observations remain separate evidence.

The account-HOME initialize-only probe now supplies one owned KIRO_HOME to both its preflight and
ACP child. It successfully negotiates ACP version 1 with pinned Kiro 2.21.1/v2, advertising session
loading, images and HTTP MCP, while audio, embedded context and SSE MCP remain false. Cleanup joins
the process group. No session/new or session/prompt is sent. This establishes that initialization
works in that specific environment, not that the selected agent is activated or inherited effects
are excluded. Production policy/capability cache identities are not upgraded from this observation.

## D39: observed read-only tools command and native-name projection (review R06/R14)

An independent fake ACP peer precedes the installed observation. It admits only initialize,
session/new with an empty MCP list, and one exact argument-free tools query. A valid advertisement
must belong to the newly returned session. Missing advertisement times out; an advertised list
without tools sends no query. Foreign sessions, malformed or duplicate command names, malformed
success values and bounded diagnostic overflow reject. Pre-response notifications are drained before
reporting completion. Every case closes and checks its process group; no prompt method is supported.

The pinned Kiro 2.21.1/v2 observation uses the account HOME for existing authentication, an owned
KIRO_HOME, an empty owned workspace, a newly authored agent and explicit environment. Agent resources
and hooks are empty, allowedTools is empty, MCP servers are empty and includeMcpJson is false. The
owned cli.json sets chat.disableInheritingDefaultResources. These inputs do not, by themselves,
establish the effectiveness of every field or inherited-configuration exclusion. Both binary versions
and the current account identity must pass before ACP starts. No credential is copied or directly
modified, no login/logout runs, and no model prompt is submitted. Kiro may maintain its own state.

Session creation advertises 25 commands, including tools. An initial D09-style name/arguments object
fails with -32700. Fixed parse markers successively identify command and args as required members,
and an array args value fails the expected-struct check. The following exact request succeeds:

```json
{
  "sessionId": "<owned-session-id>",
  "command": {"command": "tools", "args": {}}
}
```

The method is _kiro.dev/commands/execute. Only this read-only command and empty arguments are admitted
by the test helper. It never converts a slash command to session/prompt or accepts arbitrary command
arguments. The public [ACP extension reference](https://kiro.dev/docs/cli/acp/) identifies the method
and advertisement lifecycle; the [2.x reference](https://kiro.dev/docs/cli/2x-reference/) identifies
bare /tools as the permission inventory. Exact request/response shapes here are independent black-box
observations on 2026-09-09, not inferred 3.x behavior or copied implementation artifacts.

Successful responses have Boolean success, string message and object data. data contains a tools
array and a message string. The empty agent returns zero tool entries. A separately owned positive
control with tools [fs_read] returns one entry, whose name is read; its description, source and status
are strings. Thus configuration and listed tool names are not assumed identical. The first equality
assertion fails before the explicit fs_read-to-read observation is recorded. No description, status
value, account value, session ID or unrestricted upstream error text is logged. Diagnostics retain
bounded field names/kinds, container sizes, fixed native-name matches and fixed parse markers.

The observation bounds session/new results to 1 MiB, command results and individual notification
params to 64 KiB, notifications to 64/1 MiB, advertisements and tool arrays to 128 entries, and
field names to 64 ASCII bytes. Version/account commands retain five seconds; each ACP request has
fifteen seconds, advertisement wait three seconds and each case a one-minute caller deadline.
Shielded process cleanup remains separately bounded by the existing transport. No production limit
or policy adapter changes, and no public bypass is introduced.

This establishes session creation, a usable read-only command envelope, and the observed empty/one
native-tool inventory distinction. It is not proof of attempted native-tool denial, inherited MCP
or hooks exclusion, relay initialization, later configuration reloads, loaded sessions, model turns
or full R06 acceptance. Actual effort arguments and private metadata semantics remain separate work.
Production startup continues to return ErrPolicyUnverified.

## D40: live relay enumeration and a separate process-group observation (review R06/R07/R09/R12)

The read-only observation can additionally require _kiro.dev/mcp/server_initialized for the exact
owned session and serverName dax_session before its sole tools query. Missing MCP readiness times
out; a foreign session rejects. A server with another name cannot satisfy readiness by including
dax_session in an unrelated description field. The latter synthetic regression first failed before
the helper switched to exact serverName matching. Repeated valid readiness/advertisement events are
bounded and do not multiply the expected tool inventory.

The live probe builds the actual effect-free relay command from this repository. A fresh random
synthetic tool name produces a new alias for every run, avoiding reuse of an earlier tool catalog.
The generated D27 candidate contains only @dax_session/<alias>, its owned MCP command/configuration,
empty resources/hooks and includeMcpJson false. The parent broker never opens tool-call admission.
The schema fixture is sufficient only for this independently written empty object schema and
enumeration; the real schema-worker suites remain the argument-validation evidence. No model
prompt or client tool call is submitted.

Pinned Kiro 2.21.1/v2 returns two initialization notifications for serverName dax_session and a
successful tools response containing exactly the fresh bare alias, with no listed native tool.
The actual relay server offers MCP 2025-06-18 and exposes tools/list only after its initialization
lifecycle; this establishes the exercised enumeration's interoperability with that implementation.
No complete MCP conformance suite, client tool round trip, inherited-MCP exclusion or live policy
clearance is inferred. The two notifications are not evidence that two relay processes started.

A separately built, standard-library-only observation wrapper now records its own PID and group
before exec of the adjacent actual relay binary. It forwards descriptors/arguments unchanged and
records no MCP data. The private record is limited to 32 entries/1 KiB, refuses symlinks and uses
a nonblocking exclusive append lock. The independent version control verifies that exec retains
the owned PID/group and that ordinary runner cleanup removes it. No global process table is searched.

The installed wrapper observation records one relay process whose group differs from the ACP
group. Normal ACP Close still leaves that relay PID gone, with no pending relay work and its private
config removed. The same-group assertion fails. This disproves the current test's assumption that
the ACP group's signal/reap path directly covers this child; it is not an observed orphan or proof
that ordinary shutdown fails. The uninstrumented inventory successes do not close this ownership gap.

Before enabling live startup, the relay lifecycle needs explicit evidence and ownership for its
actual group: either a verified association with the ACP-owned group or separate authenticated child
registration and bounded joining/termination. Forced ACP loss and cleanup during pending tool work
must exercise the chosen mechanism. Tests may then validate that mechanism instead of requiring
group equality by assumption. The current installed ownership probe remains failed; ordinary fixture
success is not reported as real descendant-cleanup proof. No production gate, process policy,
dependency or timeout is changed by these observations.

## D41: cooperative relay group-join feasibility (review R06/R07/R12)

An independent fake ACP mode starts its MCP child in a deliberately separate process group. The
explicitly named joining-relay observation variant then calls setpgid for itself, targeting its
parent's group, before exec of the adjacent actual relay. It records one fixed outcome marker and
its resulting PID/group. The ordinary observer and every version control preserve their group.
The experiment's parent identity is a controlled test assumption, not a production authorization
mechanism. Its marker is exclusive, owner-only, bounded and refuses symlinks.

The initial fake normal-close test failed because no group movement occurred. After implementing
the experiment, normal close, forced ACP-group loss and close with a suspended synthetic tool all
pass, including eight concurrent Close callers and checks for the relay PID, ACP group, pending
work and private configuration. No fixture performs a client tool effect.

The installed Kiro 2.21.1/v2 experiment also moved one relay into its ACP parent's group. The fresh
alias was enumerated, no native tool was listed, and both the relay PID and ACP group were absent
after normal Close. The first attempt stopped at the unchanged five-second account-check deadline,
before the relay observation; an identical bounded retry passed. Neither attempt sent a model
prompt or copied credentials. This is macOS feasibility evidence for cooperative group membership,
not proof of authenticated production membership or all live cancellation paths. D40's original
uninstrumented production ownership gap remains open.

The selected next mechanism is a private authenticated attachment exchange: the process owner binds
its known ACP group, the relay receives that group over the owner-only control socket, and the
owner checks the socket peer's kernel-reported PID and actual group before MCP becomes available.
A persistent connection must couple relay lifetime to its owner. Setup, duplicate attachment,
failed joins, owner loss and repeated shutdown require bounded tests; cleanup errors must reach the
process owner. No client-supplied PID or the experiment's parent assumption may authorize a signal.
This record selects the implementation direction; the authenticated mechanism is not implemented
by the observation wrapper and does not open the execution-policy gate.

Public operating-system reference:
https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/setpgid.2.html

## D42: authenticated relay membership and bounded lifetime (review R06/R07/R12)

The product relay now requires attachment before starting MCP. Child configuration is version 2
with seven exact fields: version, supervisorPid, socket, owner, secret, timeoutMillis and tools.
The supervisor PID is written by the listener into its existing owner-only configuration. The
child checks the socket server's kernel peer PID and effective UID before sending credentials.
Version 1 child configurations are rejected; private tool-call envelopes remain version 1.

The session driver binds its actual ACP leader after process initialization and before session
creation/loading. This local one-time binding rejects invalid, current, shared-parent and absent
groups. A peer request cannot supply a PID or select a target group. The private attachment request
has exactly version=1, operation=attach, owner and secret. The supervisor answers with version,
operation=attach and its bound group. The child joins that group and acknowledges version and
operation=joined; the supervisor independently checks the kernel peer's actual group before sending
version and operation=ready. Unexpected fields, wrong credentials, invented acknowledgments and
false group claims reject. Darwin uses LOCAL_PEERPID and LOCAL_PEERCRED; Linux uses SO_PEERCRED.
Unsupported platforms reject this mechanism.

One authenticated child can claim each socket's lifetime slot. An invalid credential or extra
untrusted field cannot consume that slot; a duplicate cannot retire the valid attachment. Once
claimed, failed setup or connection loss closes the broker and prevents tool replay. The child has
a five-second setup bound. Parent reads/acknowledgments retain bounded deadlines, and waiting for
the local process binding is also bounded. After binding, tool-call connections must come from the
same kernel PID in the verified group. The default/maximum socket connection count is now 65,
allowing one lifetime connection alongside at most 64 tool calls; broker and MCP call limits remain
64. No new dependency is added.

The lifetime connection stays open while MCP runs. Its loss cancels the reader and all suspended
calls. Inherited blocking stdio initially caused the actual-child closure tests to fail: closing a
blocking os.File did not interrupt the pending read. MCP now owns nonblocking close-on-exec copies
registered through os.NewFile, so cancellation interrupts pipe reads and writes. The tests cover
idle input, pending tools and a full unread output pipe. All original and duplicated descriptors are
closed by the relay when Run returns; this does not change the launcher's client descriptors.

Socket Close joins connection handlers with a one-second shutdown deadline, then allows up to one
second for its recorded peer to disappear. Handlers cannot extend a deadline after closure begins.
Repeated callers join the same result. A surviving child or failed private-directory removal is a
retained ErrCleanup, not a success report. The socket never signals a wire PID or a separate peer
group. The existing ACP owner performs bounded group retirement when a session cleanup fails.
Driver cleanup errors survive idle release and final Close; concurrent idle closes join one result,
and a failed driver cannot start again. A deliberately stopped owned relay exercises this path.
Manager eviction and TTL pruning retain that failure, cancel the manager's admitted work and reject
new starts/discovery. Pruning participates in the shutdown join so final Close cannot miss an
in-progress retired driver's outcome. Both eviction and pruning initially failed this regression.

The plain observation wrapper now records the relay's pre-attachment group. An inventory probe may
accept that initial group difference only when the authenticated socket verifies that exact
recorded PID's later membership, and the PID is absent after shutdown. The earlier group-equality
assumption is not silently treated as evidence. Independent fake ACP tests exercise normal, forced
and pending-tool cleanup both with the experimental join wrapper and with the unmodified product
attachment path. Live execution restrictions, native tool effects and credit-consuming prompt gates
remain separate.

Public API references:

- https://pkg.go.dev/os#NewFile
- https://pkg.go.dev/golang.org/x/sys@v0.47.0/unix
- https://man7.org/linux/man-pages/man7/unix.7.html

## D43: independent MCP configuration-source controls (review R06)

The read-only inventory observer now tracks up to eight explicitly supplied server/alias pairs.
Names are bounded ASCII identifiers and aliases must be unique. A positive control can require
readiness for every exact server name in the owned session before dispatching its one tools query.
Only matching bare or server-qualified aliases count. Independent protocol cases cover two servers,
a missing server and a notification arriving after the query response. An optional observation
window is fixed at no more than one second; notifications retain the existing count/byte bounds and
cannot extend it. This does not establish absence of events after the window.

The installed test seeds four independently named, effect-free MCP servers in an owned KIRO_HOME's
mcp.json and settings/mcp.json, and the owned workspace's .kiro/mcp.json and .kiro/settings/mcp.json.
Each has its own fresh alias, closed-admission broker, authenticated socket and independent process
observer. The primary session relay is separate. All five sockets bind to the test's owned ACP group;
no tool call, model prompt, credential copy or real-HOME configuration write is performed.

The explicit positive control also registers all four servers in the selected agent's mcpServers
and lists all five exact tool references. With pinned Kiro 2.21.1/v2, every relay starts, attaches,
appears in the inventory and disappears after ACP Close. This verifies the commands and server
fixtures. The next control omits includeMcpJson, retains all five tool references, and registers only
the primary relay in the agent. None of the four standalone-file servers starts or appears during
that observation; only the primary alias is listed.

Because the default case does not establish an active inherited source, the test does not run its
flag-false and final-candidate comparisons or claim that includeMcpJson false caused exclusion. The
public [2.x reference](https://kiro.dev/docs/cli/2x-reference/) identifies that field as new in 3.0;
the [current configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/)
also explicitly targets CLI 3.0. Both were rechecked on 2026-09-09. Unknown fields being accepted by
the installed parser is not evidence of their effect on v2.

This narrows the observations from configured CLI inventory (D37) to actual session startup and
listing for these owned inputs. It does not prove every global/workspace search path, hooks,
resource inheritance, configuration reload, session loading or native execution denial. Production
startup continues to reject the unverified policy. No production adapter, dependency or timeout
changes are made by these test controls.

## D44: launch and session directory selection controls (review R06/R14)

An independent inventory fixture first verifies that session/new receives the exact supplied
workspace and that its directory inode differs from the process working directory. It admits only
the existing initialization, one session creation and one advertised tools query; no prompt or
additional command is supported.

The installed Kiro 2.21.1/v2 comparison uses a fresh owned KIRO_HOME and launch workspace per case.
The named launch agent has either tools [fs_read] or an empty tools list, with no MCP servers,
resources or hooks. A separate session workspace can omit that agent or contain a newly authored
agent with the same name and the opposite tool list. All source agent files are read through the
bounded private-file adapter and compared byte-for-byte after normal ACP shutdown.

The same-directory positive control lists read. A separate session directory with no agent also
lists read from the launch agent. When the launch agent has fs_read and the session agent is empty,
read remains listed. With the lists reversed, the inventory stays empty. Thus the selected launch
agent determines the first session's observed inventory in these controls; the session directory's
same-named agent does not replace it. Every case completes the tools query, leaves its source
configuration unchanged and joins its ACP group.

This supplies direct evidence for D31's separation of process cwd and the original session cwd in
this initial selection path. It does not establish the same precedence for inherited resources,
other hooks or MCP sources, later configuration reload, a loaded session, or native tool effects.
No production configuration or policy gate is changed and no model prompt or tool call is sent.

## D45: bounded client-denial experiment before credit opt-in (review R04/R06/R14)

LIVE_KIRO_TEST_PLAN.md specifies one reviewable live interoperability experiment. Its Kiro variant
requires DAX_INTEROP_KIRO_CREDIT_OPT_IN=1 and both pinned executable paths. It was unrun at this
checkpoint; D51 records the later explicit approval and successful live result.
It is confined to test code and does not change the production run gate or declare the candidate
execution-verified. A successful single path would not discharge native/inherited/reload/load proof.

The test-only backend admits one initial request and one matching error-result continuation.
Another ordinary request, an incorrect result ID, a successful result, missing hook-denial evidence
or a third request rejects before the driver. Concurrent initial requests can admit only one.
Continuation must contain a result, so a changed driver state cannot turn it into a fresh prompt.
The output guard permits only one Read call for the owned synthetic file. A different tool/path,
multiple calls, or a canary in inspected text, arguments or client-result text cancels/rejects.
The text check keeps only a copied boundary fragment and detects a canary split across chunks.

The public client runs with an owned HOME/project/settings and a newly written denying PreToolUse
hook. Its local gateway uses the actual bounded server with four connections. The same prepared
process/relay path is used by the fake control and Kiro experiment; the fake consumes an independent
launch manifest, while the live branch writes the D27 candidate. Only the latter needs account
preflight, pinned Kiro/v2 and the exact advertised auto model. The documented client declaration-
omission options are experiment inputs, not product defaults or a resolution of R16.

The probe appends those two options to its copied child command after ordinary profile environment
filtering. The initial attempt passed them into the platform-only constructor and they were dropped;
a new assertion reproduced thinking/context declarations still reaching the test backend. The guard
now requires both declarations to be absent before dispatch. An explicitly empty, owned MCP file is
also supplied with the client's strict-MCP option. The product environment allowlist is unchanged.

The existing finite runner, 20-second setup/first-event bounds, 45-second ACP turn, 60-second client
invocation and three-minute harness bound apply, with separately shielded cleanup. One initial ACP
turn can contain multiple internal provider calls, including generation after the denied tool;
these limits do not establish a fixed credit or token budget. There is no automatic live retry.

The independent fake requires the synthetic hook denial before completing its sole ACP prompt.
Verification also requires one exposed call, its exact returned refusal, final completion, client
exit 0, unchanged canary/source settings, no observed canary disclosure, zero gateway/pool owners,
removed private launch/relay artifacts, and absence of the independently recorded relay PID and its
kernel-observed group after cleanup. Reports retain only fixed names, counts, sizes and Boolean
outcomes. Native effect attempts and actual successful client file/shell execution remain separate
tests; this denying control cannot stand in for either requirement.

## D46: CLI and ACP catalog agreement without a model turn (review R14/R16)

The read-only inventory can compare its session/new result against the separately validated public
CLI catalog. It uses the production session-model decoder, requires exact backend-ID set equality
and checks every client alias's reverse mapping. The report retains only counts, equality flags,
the fixed legacy/config-option selector kind and presence of the exact auto model. It retains no
model descriptions, arbitrary model/selector IDs or session ID. A different current selection is
reported separately because the CLI default is not an instruction to change an existing session.
Catalog counts include the public adapter's selected-but-unlisted current model when supplied.

Independent cases cover legacy and public select controls, reordering, different current selections,
an omitted current entry, normalized-name collisions, missing/additional IDs, duplicate or absent
model state and the one-MiB response ceiling. The fake ACP permits only initialize, session/new and
one advertised tools query; catalog disagreement or malformed state must stop before that query.
It cannot switch a model or submit a prompt. An invalid expected catalog rejects before session work.

The installed Kiro 2.21.1/v2 test uses an empty owned agent and configuration/workspace. Its initial
attempt stopped at the unchanged five-second account-check deadline before ACP. One identical
bounded retry passed: both validated catalogs contain 19 models, all 19 IDs and aliases agree,
both include auto, and their current selections match. ACP uses the legacy model-state shape.
The tools query reports an empty inventory and cleanup joins the process group. Only the exact
public catalog-listing command receives fifteen seconds; ordinary version/account checks retain
five seconds and the entire observation has a one-minute context with separately bounded cleanup.

This establishes catalog decoding and identity agreement for this session/version. It does not
establish live model selection, generation constraints, execution restrictions or complete client
selector behavior. No prompt, model change, client tool, credential copy or login/logout runs.
The production policy gate and the separately pending D45 credit-consuming experiment are unchanged.

## D47: bounded client status display with UI-only authority (review R12/R14/R15)

The internal launcher passes its already-generated UI token to a private version-1 statusline.json
inside the temporary client runtime. The four exact keys are version, endpoint, token and model;
the file is at most 4 KiB, owner-only, regular and neither a symlink nor a hardlink. Model and UI
tokens must differ. The helper accepts only a literal loopback IP, HTTP and an explicit valid port,
with no path, query, user information or fragment. No user-supplied token reaches runtime startup.

The temporary host-settings overlay selects a shell command that runs the absolute proxy executable
through `/usr/bin/env -i PATH=/usr/bin:/bin`, with `statusline --config` and the quoted private path.
Paths with quotes, backticks and shell substitution syntax remain literal. The UI credential is in
neither argv nor the helper environment. The source user's status command remains in the temporary
user snapshot, while the host overlay selects this product display for the launched session. Source
settings are never rewritten, and original status commands are not chained. Permission and hook
settings retain their existing precedence. The documented refreshInterval is five seconds. The
client's workspace-trust and disableAllHooks gates still apply; the launcher does not override them.

The helper reads no stdin, transcript or ambient credentials and launches no child process. It makes
one GET to the exact cached usage route with its UI key. There are no redirects, environment proxies,
DNS hostnames or retries. The HTTP context is 750ms, connect/header deadlines are 250/300ms, headers
are bounded at 8 KiB and the body at 64 KiB. Ordinary failures render a fixed unavailable line; caller
cancellation and malformed private configuration return a silent failure. A two-second timer in
main exits even if inherited stdout or a filesystem call stalls. This begins after Go initialization,
not at OS process creation. The helper owns no durable write or child that this exit could abandon.

The display uses the selected startup model until a valid last completed foreground record exists.
Thereafter `Kiro last` labels that record, including its effort state, local elapsed time and supplied
context, multiplier and credit values. Missing values are not inferred. Local estimates carry the
`~tokens` marker and remain distinct from billed usage; stale account amounts are marked. Labels
strip the product prefix and terminal routing digest only for display, never for model selection.
No binding digest, arbitrary diagnostic, account identity or provider token estimate is printed.
The normalized output is one line of at most 1 KiB.

A regression reproduced a late status invocation recreating the removed runtime through the existing
privatefs.New constructor. The helper now uses privatefs.Open, which requires an existing private
directory and never creates it. Runtime shutdown retains its server/backend/profile ordering. This
feature adds no dependency or account-usage command and cannot enable the unverified Kiro policy.

The broad run under umask 077 also exposed old negative-fixture assumptions: requesting mode 0644
at file creation produced mode 0600, so private-file and scope-key tests mislabeled safe files as
public. The three affected test sources now chmod their synthetic public records explicitly.
Production file validation and permissions are unchanged by that correction.

The installed UI test uses a fresh owned HOME, empty workspace, strict empty MCP config, no built-in
tools and a local backend that cannot infer. Its dedicated client config seeds only the documented
projects[exact owned path].hasTrustDialogAccepted key. This is a test prerequisite, not a launcher
policy or proof of the complete interactive onboarding flow. The terminal observer supplies only
recognized theme, synthetic local-key and introductory-note inputs. It waits for a separate selected
Yes render before confirming that key. Unknown/login/directory/tool dialogs receive no input.
Capture stays within 256 KiB in memory; retained reports contain only fixed markers/counts/outcomes.
The test verifies actual visible output, a four-to-eight-second refresh interval, zero HTTP message
requests and zero backend starts, then joins the independently recorded client's group. Earlier
unseeded controls did not complete the trust flow and remain negative onboarding evidence.

Public client contracts checked on 2026-09-09:

- https://code.claude.com/docs/en/statusline
- https://code.claude.com/docs/en/settings
- https://code.claude.com/docs/en/cli-reference
- https://code.claude.com/docs/en/permissions#project-allow-rules-and-workspace-trust

## D48: startup hooks and visible status are separate observations (review R14)

The public [hook reference](https://code.claude.com/docs/en/hooks) specifies parallel matching
handlers. SessionStart identifies a session lifecycle event, not a documented completion barrier
for the interactive terminal. The [status-line contract](https://code.claude.com/docs/en/statusline)
has independent update triggers and disabling/trust gates. A missing optional callback therefore
cannot be treated as a hung client, nor can its arrival prove every startup task finished. These
documents were checked on 2026-09-09; no interactive readiness interface was established in this
review. Absence of an established interface is not proof that none exists.

An explicitly authorized local Claude CLI consultation answered only a new public startup-lifecycle
question. Its complete saved answer was reviewed against those primary sources. The first stripped
environment returned a login error; the normal CLI environment then returned a successful one-turn
result. No authentication change was needed and the cause of that difference remains unknown.
The advice to distinguish observations and use a bounded terminal experiment was adopted. No source
inspection, print-mode readiness substitution or unverified flag was adopted. This public-only
question did not transmit the separately pending internal MCP observation payload.

The independent startup-hook executable imports only standard packages, ignores stdin and never
reads client context/transcripts. Each fresh owned hook reads a private synthetic configuration,
records its own PID once, and sends two authenticated loopback callbacks: entered and returned.
The parent holds one entered response while the other hook completes. Its kernel-observed PID
must disappear before the held response is released after a further three seconds. Callback bodies
are exactly empty objects; supplied identifiers cannot choose a process or falsify a stage. Duplicate,
out-of-order, malformed and unauthenticated callbacks cannot advance recorded observations.

Each helper has an eight-second HTTP budget and twelve-second deadline in main, with no redirects,
proxies, retry, child process or model request. It emits one synthetic JSON systemMessage after its
callbacks. No additionalContext or initialUserMessage is emitted. The fixture build disables Go
telemetry only within its disposable build HOME using the documented go telemetry off command.
GOTELEMETRY is a read-only Go environment value; assigning an environment variable does not set it.

The installed Claude 2.1.263 experiment reuses D47's prepared empty project, documented test trust
record, isolated source settings and bounded terminal capture. It records first callback, callback
return, observed helper exit, held-response release, status request, visible status and visible
notice times separately. Every timing is relative to the first hook callback, not process spawn.
Raw terminal data remains bounded at 256 KiB in memory and never enters the retained report.

In the verified run, the fast hook exited at 3ms. Status was requested at 514ms and visible at 520ms,
while the other hook remained held until 3,005ms. Both notices first appeared at 3,023ms, after that
release. There were zero HTTP message requests and zero backend starts. Client/group and both hook
PIDs were gone after cleanup. This proves visible status can precede completion of the known startup
hooks in this environment. The later notices do not establish a universal barrier for all other
hooks, asynchronous work or input usability; no latency distribution or full onboarding is claimed.

Production still reports process launch separately from the unverified client-initialization phase.
Neither SessionStart nor status rendering becomes a readiness flag. This decision supplied positive
synthetic systemMessage display evidence; D49 subsequently implements the product payload, UI route
and launcher hook. Full readiness, R06 and release gates remain open. D48 itself changed no production
transport, dependency or execution authority.

## D49: bounded startup model notice without a model turn (review R14/R15)

The launcher supplies its prepared client model alias to the gateway. A caller cannot supply a
second startup-model authority through the internal runtime configuration. The exact UI-authenticated
POST /dax-kiro-proxy/hooks/model-capabilities accepts only an empty body/object within 4 KiB. It
shares existing UI admission and I/O deadlines. Model-route credentials cannot authorize it, and
the UI credential remains unable to authorize either model route.

The version-1 response has exactly these fields:

| Field | Startup value |
| --- | --- |
| version | 1 |
| model | Prepared launch alias |
| image_input, pdf_input, effort | unknown |
| native_web_search | unsupported |
| client_tools | client_permissions |
| provider_token_usage | unreported |

No session is opened or queried on this path, so media and effort support cannot be inferred from
the selected model's name. Native web search is unavailable in this adapter; this is not a claim
that Kiro lacks that feature. The tool field describes client ownership, not verified Kiro execution
restrictions. Provider usage has not been reported at startup and is not estimated as billed usage.
The route neither invokes model discovery/usage refresh nor drains completion metrics. A missing
prepared model returns a fixed 503; it never attempts to acquire one. The payload is immutable launch
information, not the current model after a later switch. Future capability support needs an explicit
payload/formatter update and independent evidence.

The existing private version-1 statusline.json file also supplies the model-notice helper. Its
four-field schema, owner-only mode and runtime lifetime are unchanged. Shared internal uiclient code
restricts both helpers to enumerated operations: status uses its existing GET, and model-notice sends
one POST with {}. Neither helper reads client stdin, transcripts or ambient credentials. There are
no child processes, DNS names, proxies, redirects or retries. The existing 750ms HTTP, 250/300ms
connect/header, 8 KiB header and 64 KiB body limits apply. Both exact command forms have a two-second
deadline beginning in main, including blocked inherited stdout and filesystem calls. Late invocation
cannot recreate a removed runtime or fetch with removed credentials.

The host settings overlay adds a synchronous SessionStart command with matcher startup and timeout
three seconds. It uses a cleared environment and literal shell-quoted executable/config paths; the
UI token is never in that command or its environment. Resume, clear and compact do not trigger it.
No permission or disableAllHooks setting is added. User/project hooks retain the client-controlled
merge and policy behavior verified below. This updates D24's initial omission of product hooks;
the D47 status-line contract is unchanged.

The helper validates the exact response shape, constants and launch alias before formatting. It
emits a single JSON object containing only systemMessage, within 1 KiB. Labels are normalized solely
for display, with no routing-prefix/digest reversal. Network and malformed-response failures produce
a fixed unavailable notice; invalid private configuration or caller cancellation return a silent
failure. There is no additionalContext, initialUserMessage, permission decision or control field.

The public [hook contract](https://code.claude.com/docs/en/hooks), checked on 2026-09-09, distinguishes
synchronous systemMessage display from asynchronous delivery into a later model turn. It also defines
parallel handlers and effective disableAllHooks precedence. This helper therefore stays synchronous;
its completion or absence is never an initialization barrier. D48's ordering evidence still applies.

Installed Claude 2.1.263 rendered the product notice with exactly one UI request in both idle-terminal
and held-peer-hook controls. Each made zero model requests/backend starts and retained the existing
status refresh and process cleanup checks. Separate local synthetic-conversation controls retained
user/project hooks, local environment precedence and client Read denial. With hooks disabled, the
same conversation and denial completed with zero startup-notice requests. Neither of the two model
request bodies in either control contained the notice. These checks use independently authored local
responders, not Kiro prompting or external inference; source settings remain unchanged.

No dependency or execution-policy authority changes. This completes the initial startup-notice
contract only. D50 subsequently adds turn-metrics client hooks. Full interactive readiness, real
feature/usage adapters, R06/R16 and release gates remain open.

## D50: foreground completion display and final-delivery synchronization (review R14/R15)

The launcher adds a synchronous Stop command invoking turn-metrics with the existing private UI
configuration. Its cleared environment, quoted paths, three-second client timeout and two-second
helper-main deadline match D49. The shared UI reader enumerates the exact metrics POST with {},
retaining its 750ms HTTP, 250/300ms connect/header, 8 KiB header and 64 KiB response bounds. No
client stdin, transcript, model token, provider credential, subprocess, proxy or redirect is used.
Source Stop hooks and the effective disableAllHooks setting remain under client-controlled merging.

The formatter validates exact field names, record shapes, ascending sequence numbers, model labels,
effort states and bounded numeric metadata/estimates. Unknown text, null required values, duplicate
keys, unrecognized units, invalid estimates and identity digests cannot enter display. Each retained
completion is shown with its sequence, model, effort, session state and local elapsed time; available
model multiplier, context, Kiro duration/metering and labeled local estimates are optional. Labels
longer than 40 ASCII bytes are shortened for display only. All 32 retained records fit within the
9 KiB JSON limit, including maximal numeric widths. Eviction counts are cumulative runtime totals,
shown only with a nonempty page. Empty/unavailable/malformed pages emit {}. Output contains only
systemMessage, never additionalContext, a new prompt, decision/reason or continuation control.

The public [hook contract](https://code.claude.com/docs/en/hooks), checked 2026-09-09, makes Stop a
main-response lifecycle event and distinguishes synchronous display from asynchronous later-context
delivery. A Stop callback does not establish that another process has completed bookkeeping after
its final HTTP flush. The gateway therefore registers a pending delivery before terminal JSON/SSE
output and releases it only after Finish or Cancel returns. Registration remains bounded by model
admission. A metrics request snapshots existing registrations and waits at most 200ms outside the
lock; new deliveries cannot extend it. Failure returns a fixed 503 without draining old records.
Authentication/body rejection still precedes waiting, and status/model notice never wait. Successful
final delivery remains the only publication point; tool handoff, cancellation and failed writes do
not become completions. No request identity, polling loop or wait goroutine is added.

This remains best-effort diagnostic delivery. Drain has no acknowledgement: a later HTTP/helper/UI
failure may lose a record, and concurrent Stop readers can divide records differently. A timeout
leaves records for a later caller but does not initiate a retry or model turn. No exactly-once user
visibility, client readiness barrier or provider-billed accounting is claimed.

An installed-client observation found an independent compatibility gap: with the documented
CLAUDE_CODE_DISABLE_THINKING test option, both title and main requests omit thinking. The earlier
classifier treated both as main. The corrected classifier permits omission only with every existing
tool/schema/system-purpose condition; explicit enabled/adaptive, null or malformed declarations
remain main. The observation uses a title-only string JSON Schema and separate fixed foreground
system input, not copied client prompt text. The synthetic title responder returns independently
authored valid JSON and publishes no foreground metric. This does not implement general structured
output enforcement, disable Kiro reasoning or change production client options.

A generic public Stop/HTTP timing question was submitted through the explicitly authorized local
Claude CLI; answer.md and result.json were saved privately and reviewed. Exit 0, one successful
turn and no tools were recorded under .cache/claude-consult/work/turn-metrics-review-9wf8y8gr.
The accepted advice preserves display-only synchronous output and tests the final-flush race.
The chosen snapshot wait and no-drain-on-timeout policy are recorded here rather than adopting
the answer's optional partial drain. No repository source, previous implementation or separately
blocked MCP observation payload was sent. Public documents and independent tests remain authority.

No dependency or live-policy authority is added. Real Kiro metadata/usage, full interactive/client
compatibility, R06/R16 and release work remain separate unfinished gates.

## D51: first live Kiro client-denial continuation (review R04/R06/R14)

The user explicitly approved the bounded one-attempt experiment in LIVE_KIRO_TEST_PLAN.md on
2026-09-09. TestKiroLiveOnePromptClientDenial then ran once with its separate credit opt-in,
Kiro 2.21.1/v2, Claude Code 2.1.263 and the exact advertised auto model. The existing prepared
agent/relay path, isolated client profile and guard were used without a production-policy override.

The test passed: one Read call was exposed, the client's PreToolUse hook refused it, its exact
error result returned through the relay and the same ACP turn completed. Initial request plus
continuation accounted for two accepted backend requests. Client exit was 0; canary preservation,
absence from checked output, idle-before-shutdown state, source-setting checks, private artifact
removal, joined HTTP/pool ownership and disappearance of the relay PID/observed group all passed.
The test took 40.91s (42.402s package under race instrumentation). Only the normalized counters,
Boolean outcomes, sizes and exit status are retained under
.cache/interop-observations/live-kiro-denial.de4u7z. No automatic repeat ran.

The result is real inference evidence for this particular client-denial continuation and cleanup
path. It does not measure provider call count, billed token/credit usage or a fixed cost, and it
cannot prove that Kiro never performed an unobserved internal read. Native filesystem/shell/task/
subagent denial, inherited configuration exclusion, reload/load restrictions and approved client
tool execution require their own evidence. R16 reasoning/output constraints and other models/media/
web behavior are unchanged. Therefore the candidate remains execution-unverified and production
run still fails closed with ErrPolicyUnverified. No dependency or implementation policy changed.

## D52: development admission, declared subset and active MCP inclusion control (review R06/R16)

The user's revised priority is effective Kiro restriction, real client tool approval/denial/hook
round trips, client-environment preservation, then request compatibility; optional metadata/web and
release soak follow. ACCEPTANCE_SPEC.md separates development run, internal alpha and release.
Ordinary launch already rejects solely because the built-in execution policy is unverified. No
development gate is added for full Anthropic parity or optional features, and no trust override is
introduced. Unverified reachable execution/load paths still require restriction or disabling.

The first product contract is the pinned Claude Code Messages subset. PRODUCT_SPEC.md documents
positive max_tokens, top-level thinking and context_management as accepted but non-enforced hints,
effort as best effort, and auxiliary title classification separately from general schema enforcement.
The current parser and its explicit safety/control rejections are unchanged. In particular, zero
max_tokens continues to reject: the public
[Messages reference](https://platform.claude.com/docs/en/api/messages/create) defines it as a
no-generation cache operation. Positive values are documented absolute output limits in that API;
accepting them here does not enforce that limit. Do not truncate only the client-visible result while
retaining unseen backend history. D33 remains an implementation inventory, not full API parity.

Prefer an ephemeral client overlay, but preserve mandatory routing and existing settings/permissions.
The public [settings precedence](https://code.claude.com/docs/en/settings) allows per-session settings
while preserving omitted lower-scope values; managed settings and individual environment/setting
pairs have their own precedence. This is insufficient to replace the current private profile without
installed-client tests. Optional product UI integration must yield to incompatible user choices.

The earlier D43 default control omitted includeMcpJson and activated no standalone MCP source.
A new comparison sets true, then false while retaining references to all seeded servers, then uses the
relay-only candidate with false. It requires an activated standalone source in the true arm before
either exclusion arm can count. Started sources must attach, initialize and list their exact alias;
false arms reject startup or initialization as well as listed tools. All input fixtures are newly
generated from this repository, not taken from the user-supplied earlier-implementation description.

On Kiro 2.21.1/v2, explicit true starts the owned global settings/mcp.json and workspace
.kiro/settings/mcp.json servers. Both initialize twice, list their fresh aliases and join the owned
group; the primary relay makes three total listed tools. Explicit false starts/lists neither even
while their tool references remain allowed. The final candidate also lists only its primary relay.
The two flat-path controls remain inactive and cannot establish exclusion for those paths. Every
observed relay PID and ACP group is gone after Close; pending work and private relay configuration
are removed. The three cases pass in 43.20s (44.495s package under race), exit 0, retained privately in
.cache/interop-observations/mcp-inclusion.Nmcz83. No prompt or model credits were used.

This establishes initial-session exclusion for the two activated paths and supersedes D43's missing
positive control for them. It does not establish resource inheritance, later reload/load, other search
paths or native-effect denial. The [2.x reference](https://kiro.dev/docs/cli/2x-reference/) and
[current agent reference](https://kiro.dev/docs/cli/custom-agents/configuration-reference/) describe
includeMcpJson as new in 3.0, but this installed v2 comparison directly shows a true/false effect.
Neither the newer documentation nor the previous omission observation defines the binary's default.

A separately authorized local Claude CLI consultation received only a public-document test-design
question, with tools/MCP/hooks disabled and a 60-second deadline. It exited 0 and saved answer.md and
normalized result.json in .cache/claude-consult/work/inclusion-review-ky5ve6n5; both were read. The
review supports an explicit true/false comparison, separate startup/initialize/list observations and
not treating an inactive positive control as exclusion proof. Its repeated randomized campaigns and
adaptive quiescence suggestion were not adopted for this bounded first observation. There is one
fresh setup per arm with the existing fixed readiness/window bounds. The earlier blocked MCP payload
was not sent. Public references and independent observations remain implementation authority.

## D53: bounded native-effect observer and incomplete live attempt (review R06)

The independent native-control ACP fixture precedes the live test. It requires empty advertised
client capabilities, one session/inventory/model selection/prompt, rejects a second prompt, and
checks the proxy's negative permission response and disabled filesystem/terminal methods. Separate
contamination controls create one owned marker or emit the owned canary across chunks. A terminal
response racing the final canary must not hide it. Foreign-session updates, tool status, absent text,
cancelled completion, remote error and a silent prompt cannot pass the observer. Fixtures use only
newly authored temporary data and public protocol fields; no client tool is executed by the fake.

The test-only inventory report now retains its session ID in memory so the separately marked live
experiment can use the same observed session. Existing read-only modes still send no prompt. The
live branch first requires exactly its fresh effect-free relay alias, no other listed tool, matching
CLI/ACP catalogs and exact auto selection. It then sends the single prompt in LIVE_KIRO_TEST_PLAN.md.
The broker never opens client tool admission. Native effects are requested only in a separate owned
workspace. The canary, workspace identity, directory entries, candidate/settings bytes and process
cleanup are checked, including a second file check after ACP Close. Directory enumeration reads at
most two entries; notification counts/bytes and retained text boundaries are bounded.

One live attempt ran after the user's instruction to continue the execution-gate work, using the
separate credit opt-in. Version/account/catalog checks, relay attachment/inventory and model selection
succeeded. A prompt was sent, but no assistant text or successful completion was observed. The run
failed in 37.64s (37.967s package), exit 1, retained privately under
.cache/interop-observations/native-effects.pCXJnE. It observed one 99-byte notification, zero text,
zero tool-status events, no canary, unchanged workspace/canary and candidate/settings, no pending
relay work and no surviving observed relay PID or ACP group. No automatic repeat ran.

Elapsed timing is consistent with the 20-second first-text bound, but that first report did not
retain its timer/error classification; a specific underlying backend cause cannot be recovered from
it. Absence of an effect during an incomplete turn is not a passing execution-restriction test.
The observer now retains only fixed failure classes, prompt elapsed milliseconds and a numeric
remote error code. Independent silent-prompt and remote-error controls verify the distinction without
another Kiro call or storing diagnostic prose. An atomic attempt claim prevents another exercise
from spending a second prompt, including after a failed first attempt.

Initialization stays at 15 seconds and live inventory setup is bounded at 20; model selection is 5,
first assistant text 20 and the sole model prompt at most 45 seconds within a two-minute caller.
Existing read-only cases retain their prior request/whole-probe bounds. No production timeout or
policy changed. D52's MCP exclusion result remains valid; native restriction, resources/reload/load
and client-approved effects remain unfinished. Production run still returns ErrPolicyUnverified.

## D54: completed native-effect challenge (review R06)

The user's instruction to continue and exercise readiness decisions authorized one fresh D53 probe.
No automatic retry loop, timeout increase, prompt change, alternate model or provider was introduced.
The same Kiro 2.21.1/v2 candidate, exact auto catalog/selection, isolated workspace and closed relay
admission passed the inventory prerequisites and completed the prompt in 6,437ms. It returned
end_turn and 865 assistant-text bytes; 64 notifications totaled 9,633 bytes. No tool-status event or
canary was observed, all sentinel/policy files remained unchanged, the relay was authenticated and
its observed PID and ACP group disappeared after closure. Pending work and private relay artifacts
were removed. The test passed in 26.16s (28.003s race-enabled package), exit 0; the normalized record
is .cache/interop-observations/native-effects.hbnAnS. Raw model content is not retained.

This is positive evidence for completion without the requested native effects in the tested fresh
session. It does not retroactively turn D53's incomplete attempt into a pass or prove its underlying
cause. The limitation of a black-box sentinel challenge remains: unreported internal reads, future
prompts, inherited resources and reload/load are not exhaustively proven. Development run still
requires the other applicable isolation and client permission/hook checks. Production policy remains
unverified; no production implementation or dependency changed for this milestone.

## D55: actual client permission rules and tool effects through fake ACP (review R06)

The existing single-prompt guard now also accepts a test-only exact tool/input/result expectation.
The original live Read-denial experiment retains its hook-specific refusal requirement. New state
controls initially failed on absent expectation/result APIs; they then passed with the previous
single-turn guards in 1.728s under race instrumentation. Wrong paths, changed write content, extra
arguments, wrong result status and a successful Read without its expected canary are rejected before
client exposure or backend continuation. A valid Read may return the canary solely as its tool
result; generated inputs and model output still cannot expose it. No raw result is retained.

The new fake ACP launch mode consumes only an owned request/expectation manifest and the relay launch
manifest. It never performs the requested file or shell effect. The sole relay call must return the
expected error status and required synthetic content before its one ACP prompt completes. The first
actual-client Read control failed before that fake mode existed, with no exposed tool or completion.
This negative result was not considered a permission or cleanup pass.

The unmodified Claude Code 2.1.263 then passed six owned scenarios: allow Read, allow Write, allow Bash,
deny Write, deny Bash and a PreToolUse Bash veto despite the allow rule. Each accepts exactly two
backend requests, exposes one tool, accepts its one matching result and completes the same ACP turn.
Allowed Read returns the exact synthetic file content; allowed Write and Bash create the exact owned
file content and trigger PreToolUse/PostToolUse hooks. The two rule denials and hook veto return errors
without creating the target or firing PostToolUse. The Read canary and source settings are unchanged.
Every observed relay PID/group and prepared launch/profile artifact is cleaned up. All client exits
are zero. The six scenarios passed in 19.92s (21.621s race-enabled package), exit 0, recorded privately
in .cache/interop-observations/client-effects.GbT2Ej. No Kiro prompt or external model call ran.

The [public permission reference](https://code.claude.com/docs/en/permissions), checked 2026-09-09,
specifies Edit rules for Write paths and double-leading-slash absolute paths. The synthetic settings
use that contract, exact owned Edit/Read paths, a printf-scoped Bash rule and manual mode. They retain
normal permission enforcement. The [hook reference](https://code.claude.com/docs/en/hooks) supplies the
PreToolUse deny envelope; a silent/empty decision leaves the ordinary permission rules in control.
These passing rule-based cases do not establish interactive approval/diff/denial UI, sequential
different tools within one turn or the corresponding real Kiro-generated requests. Those remain
separate development/alpha evidence, and the production gate is unchanged.

Final uncached ACP and interop race suites passed in 5.718s and 21.814s with installed opt-ins off.
The original actual-Claude/fake-ACP single Read-denial control passed after the shared refactoring
in 3.52s (4.809s package), still requiring its exact client hook denial. No existing live Kiro test
was rerun for that refactoring.

## D56: interactive permission evidence with an independent model peer (review R06)

The permission observer uses the current reconstructed 160x40 terminal screen, exact owned operation,
the selected one-time Yes/No option and the single pending tool identity. A previous screen cannot
authorize a second input: each key records a screen digest and further decision steps require a
changed current screen with the expected selection or echoed comment. Ordinary status tests retain
their previous observer; only the permission variant polls its pipe every 100ms to examine a quiet
screen. Output remains bounded at 256 KiB and the terminal lifetime at 25 seconds, followed by bounded
process cleanup. No raw screen is saved. Model requests use an independent fake ACP, never Kiro.

The initial screen-control build failed on its absent observer, then its independent controls passed.
They reject wrong filenames/content, missing questions, persistent-grant selections, unselected or
erased menus and repeated pre-key frames. Additional controls require the no-input window and each
new selected denial/comment frame. The installed UI initially failed before tool exposure. Its
test-only one-prompt budget had no separate allowance for title work; a new state regression verifies
that a preceding title cannot consume the sole tool turn. Interactive probes now route only the
existing narrow Title classification to at most two local synthetic responses. Titles never enter
ACP or contribute to tool/continuation/completion counts. Legacy print/live-denial guards retain
their previous behavior. An installed UI observation then passed with one separately counted title,
one exact tool handoff and its matching continuation.

The first combined five-case run passed in 21.08s (22.338s race-enabled package), recorded privately
in .cache/interop-observations/permission-ui.OLh7Rl. It exercises a Write prompt held for one second
without input, one-time Write/Bash approval, and Write/Bash refusal with an entered reason. The held
case remains WaitingTools with no result, completion, file or PostToolUse. Each approved case displays
the requested file content or exact command, waits at least 500ms without an effect, accepts one Yes,
creates the exact file and fires both hooks. Each refusal selects No, opens the comment, verifies
the entered reason and submits it. The exact correlated error reason is required independently by
the guard and fake ACP before the same prompt can complete; no target or PostToolUse is produced.
All observed client/relay/ACP groups and private artifacts are removed. The interactive client is
intentionally terminated after the observed result or held state (exit 143 here); this is not evidence
of a natural client exit. Source settings remain unchanged.

The [public permission documentation](https://code.claude.com/docs/en/permissions), checked
2026-09-09, distinguishes refusal with a comment from bare No, which ends the main turn. This test
does not equate those paths: bare refusal and its pending-backend cleanup still require observation.
It also does not prove repeated same-file reapproval, every permission UI, or real Kiro-generated
requests. Production run stays gated on the remaining effective-policy/development requirements.

A separately authorized local Claude CLI review received only a generic public-document question,
with tools/MCP/hooks disabled and a 60-second/64-KiB bound. It returned exit 0, one turn, and saved
answer.md/result.json in .cache/claude-consult/work/permission-ui-review-h319ixcv. Both were read and
review.md records the assessment. Adopted points include observing absence before input, exact
current selection, no-input controls, auxiliary separation and joined cleanup. Its claim that bare
denial necessarily sends a model-facing tool result is not assumed; the public docs do not specify
that wire behavior. No repository or earlier-implementation content was submitted.

After adding continued pre-effect checks during denial and checking that no project
.claude/settings.local.json was saved, the final installed-client regression passed in 52.612s
under race instrumentation. It includes the five interactive cases (21.23s), six existing permission
rule/hook cases (18.98s), original Read-hook denial (3.61s), and status-only terminal display (7.02s).
The status case still starts zero model requests and restores/cleans its observed process owners.
All five interactive cases leave the project permission-settings file absent. The normalized final
record is .cache/interop-observations/permission-ui-final.xSZ4qu, exit 0.

The final uncached interop and requestfamily race suites pass with installed opt-ins disabled in
21.854s and 2.216s. Whole-repository go vet, formatting and whitespace checks pass. These changes are
test/observation infrastructure and records; no production execution gate or dependency changed.

## D57: new questions after a silent client permission refusal (review R06)

The pinned interactive client stops its main turn on bare No. In the owned experiment, no model
request, Stop or PostToolBatch hook appeared during the three-second post-denial window; ACP remained
WaitingTools, with no file effect. The initial test's immediate-cancellation expectation failed.
The [public permissions](https://code.claude.com/docs/en/permissions) and
[hooks reference](https://code.claude.com/docs/en/hooks), checked 2026-09-09, distinguish bare No from
refusal with a comment and exclude user interruption from Stop. A completed HTTP tool handoff has no
remaining response to cancel. Silence alone cannot distinguish refusal from a user still deciding.
No new hook, terminal parser, transcript reader, inferred tool result or shorter product timeout is
used to make that distinction. Existing absolute tool/turn limits and launcher shutdown remain owners.

One subsequent ordinary question was observed before dispatch. Its five-message request contains one
matching error result followed by two text blocks in user message index 3, and a trailing standing
instruction. It contains the new question; the error result need not contain the word interrupted.
The fixed-label observer retains only counts, at most eight allowlisted block types and Booleans.
It rejects the observed request before driver dispatch in observation-only mode. The successful
record is .cache/interop-observations/bare-next.YVuAOj (8.733s package). Earlier attempts failed to
send the new text because their screen readiness predicates did not match the installed UI; those
attempts supplied no request-shape evidence. Input now requires the observed interruption/rejection,
then the new text's echo before a single Enter. Raw screens and tool-result content are not saved.

The adopted transition applies only to a complete set of matching is_error=true results, followed
by at least one nonblank text block. The authenticated identity, model/effort, registry, metadata and
system compatibility must match; the history must prove extension at that user message. A trailing
system sequence must exactly repeat the most recent standing sequence. Missing, duplicate, partial,
successful, reordered, cross-owner or divergent result sets and changed instructions reject before
consuming ownership. Result-only requests retain the existing same-prompt continuation behavior.

For a valid new question, join the old relay/process and policy-artifact retirement before creating
a fresh full-history prompt. No result is delivered to the old prompt, no tool is replayed, and no
canceled backend state is reused. Client denial data stays in a JSON boundary separate from new user
text. The existing five-minute terminal outcome additionally retains bounded keyed history nodes,
never raw content, so an already expired owner can establish the same proof. A new admitted prompt
clears that outcome; replay of the mixed request cannot restart it. Failed cleanup prevents admission.

Independent fake-process regressions first failed for both pending and expired owners. They also
found that fresh projection rejected the latest user tool_result; projection now preserves that
validated result as JSON before the new text. Pending/expired recovery, invalid model/identity/history,
altered system suffix, wrong/duplicate/successful results and blank new text pass together with the
existing continuation and original-deadline tests (6.158s race-enabled session package). New-process
identity, one prompt, full original context, no replay and disappearance of the old PID are checked.

The actual Claude/fake-ACP pair passes in
.cache/interop-observations/bare-recovery.EXrUf9, exit 0, 57.245s package. Without further input, the
existing 45-second turn deadline retires ACP while the client remains open (48.13s test, zero results
or completions). The test terminal lifetime alone increases from 25 to 55 seconds to observe this
unchanged limit. With a new question, the old ACP group/relay and private artifacts are verified gone
before a second prepared launch; one new completion is displayed (7.37s test). Both cases retain the
unchanged canary/settings, absent target and project permission-settings file, and joined cleanup.
The interactive client is intentionally terminated afterward (exit 143), not observed exiting normally.
No Kiro inference ran, and real-Kiro interruption/recovery and other isolation gates remain open.

The authorized isolated local Claude consultation received only a generic protocol question. The
first question timed out without output; the narrowed question returned exit 0 and one answer in
.cache/claude-consult/work/bare-signal-review-5vxj8s4b. answer.md/result.json were read, and review.md
records the assessment. Accepted points are the missing signal, bounded ownership and no fabricated
results/replay. Its suggested latency-based idle cutoff and permissive history retirement were not
adopted. No repository or previous-implementation content was transmitted. Dependencies and the
production execution-policy gate are unchanged.

Final relevant uncached race suites pass with installed opt-ins disabled: session 17.193s,
projection 1.911s, interop 23.582s, gateway 3.759s, Anthropic 8.240s and ACP 5.066s. Whole-repository
go vet, formatting and whitespace checks pass. The final installed-client regression in
.cache/interop-observations/bare-final.C81OMn passes in 58.111s: original Read-hook denial, six
permission-rule/hook cases, five interactive approval/comment-denial/no-input cases, the bounded
next-request observation and actual recovery. Recovery again verifies old-owner retirement before
replacement and no denied effect (7.27s). The separate unchanged-deadline result above remains the
deadline evidence; it was not rerun merely to repeat a passing test.

## D58: initial file-resource inheritance on installed Kiro 2.21.2 (review R06)

Both installed Kiro executables now report 2.21.2. The cause of this change was not observed.
The first new observation failed the production 2.21.1 version check before ACP startup; it did
not establish a resource failure. Production preflight and execution-policy admission remain
unchanged. A separate test-only exact 2.21.2 check verifies both executables and admits only owned
empty-agent read-only observations. It cannot enable relay, catalog, native-effect or model variants.
Existing 2.21.1 live evidence does not automatically verify this version.

The public [context documentation](https://kiro.dev/docs/cli/chat/context/),
[agent configuration reference](https://kiro.dev/docs/cli/custom-agents/configuration-reference/)
and [slash-command reference](https://kiro.dev/docs/cli/reference/slash-commands/), checked 2026-09-09,
describe default resources, custom-agent resources and context inspection. The default-inheritance
switch is a CLI setting, not an agent field. An explicit file-resource positive control alone cannot
prove default-resource exclusion: the defaults themselves must first be observed active.

The owned session advertises context with name, description and meta, including the show subcommand.
It does not advertise a JSON argument schema. An independently tested private wire request is:

```json
{"sessionId":"<owned session>","command":{"command":"context","args":{"subcommand":"show","verbose":true}}}
```

This is sent to _kiro.dev/commands/execute once, only after the matching session's advertisement
and successful empty tools inventory. It is not a general command dispatcher. Without verbose,
the successful reply lacked per-file items; that earlier shape observation was not inclusion proof.
The observer bounds the response to 64 KiB, objects/metadata traversal, item count, names and numeric
estimates. It retains field kinds, counts, fixed labels and Boolean path matches; descriptions,
contents, raw paths and session identity are not diagnostics. Null token/item values cannot count
as an empty context, a missing show advertisement cannot dispatch, and another query is refused.
An independent fake checks the exact request, rejects prompts/extra commands and supplies these
malformed counterfactuals. No model prompt or billable-token claim is made by context inspection.

An explicitly declared owned file is observed as one matched item with 1,450 context tokens in
.cache/interop-observations/context-resource.GhEpvZ, exit 0, 6.181s race-enabled package. This is Kiro's
local context estimate, not measured provider-billed usage. The agent and file bytes stay unchanged.

The default-resource matrix seeds only independently authored AGENTS.md and steering files. P is
the owned process launch directory, W the session/new cwd, and H the owned KIRO_HOME. Each custom
agent retains resources=[], hooks={}, empty tool lists and includeMcpJson=false. The setting below
is chat.disableInheritingDefaultResources in H/settings/cli.json; overrides use .kiro/settings/cli.json.

| Control | Files / directories | Setting | Observed matched files / context estimate |
| --- | --- | --- | --- |
| inherit | P=W, both files | false | 2 / 3,900 |
| suppress | P=W, both files | true | 0 / 0 |
| split-session-only | P empty, W has both | false | 0 / 0 |
| split-inherit | P and W each have both | false | 2 / 3,900, absolute steering belongs to P |
| split-suppress | P and W each have both | true | 0 / 0 |
| workspace-override | P and W each have both | true, W overrides false | 0 / 0 |
| launch-override | P and W each have both | true, P overrides false | 2 / 3,900, absolute steering belongs to P |

Kiro reports the matched AGENTS.md name relatively and steering absolutely. The observer retains
that distinction; it does not invent an absolute base for AGENTS.md. The positive same-directory
case and split-session-only negative control establish additional source evidence. Counts alone
do not identify arbitrary unseen resources or establish skill metadata exclusion.

The final seven cases pass in .cache/interop-observations/resource-inheritance.pFFtbL, exit 0,
36.13s test / 37.905s race-enabled package. Each establishes empty tool inventory, verbose successful
context inspection, unchanged owned sources and joined ACP-group cleanup. One earlier matrix
incorrectly expected W-only files to load and failed; another assumed all matched names were absolute
and failed. Those observations corrected the experiment's assumptions rather than proving a backend
restriction failure. They remain recorded in resource-inheritance.2nSgTx and resource-inheritance.MG4Dog.

The practical consequence is to retain an owned launch directory: a setting there can override
the owned global suppression. The tested W setting does not do so during initial session creation.
This does not prove other global roots, skills, reload/load, hidden internal reads or native effects.
Neither production run nor any execution-policy verification flag changes in this decision.

The authorized local Claude consultation used only a generic public-protocol question, with tools,
MCP and hooks disabled and a 60-second/64-KiB bound. It exited 0 with one answer saved in
.cache/claude-consult/work/resource-context-review-otqlrrqa. answer.md/result.json were read and
review.md records the assessment. Runtime advertisement and active inclusion/exclusion controls
were adopted; advice placing the CLI setting in the agent and assuming advertised schemas was
corrected against public documentation and the independent observations. No repository or earlier
implementation material was sent. Dependencies and production behavior are unchanged.

The final uncached race suites pass with all installed CLI opt-ins disabled: interop 24.291s,
ACP 5.689s and launcher 21.469s. They include malformed-context and query-budget counterfactuals
and rejection of 2.21.2 at the unchanged production preflight. Whole-repository go vet, formatting
and whitespace checks pass.

## D59: explicit preflight migration to Kiro 2.21.2 (review R06/R14)

After D58 identified the installed version change, preflight moves from exact 2.21.1 to exact
2.21.2. This is a finite command compatibility decision, not execution-policy verification. Main
and adjacent helper must both report 2.21.2; old, future and mismatched versions fail before identity
lookup. Tests first fail with the new independent version fixture against the old pin, then pass
after migration. Independent startup/compiled-command fixtures now report the selected version;
the existing run barrier still rejects before client launch. Historical identity tests retain
their 2.21.1-to-2.21.2 pair to verify that version changes cannot share cache identity. Generated
candidate policy digests already incorporate the pinned version, so no earlier digest is reused.

Fresh installed account and CLI/ACP catalog tests pass in
.cache/interop-observations/kiro-2212-preflight.5RLmJs, exit 0, 19.363s race-enabled package. The
owned-session comparison validates 19 model identities and all 19 client aliases, matching auto
and current selection. It also observes an empty tool inventory and joins process cleanup.
Catalog/owned-account commands finish within their existing bounds. A separate normal-HOME account
check also passes with the existing bounded non-JSON postamble handling. No model selection or
prompt is sent by these tests, and account values are not retained in diagnostics.

Final uncached race suites pass with installed opt-ins off: launcher 22.353s, interop 24.294s,
catalog 1.489s and command 1.787s. These include main/helper mismatch rejection, version-scoped
identity, startup cancellation/cleanup and the compiled run policy barrier. No dependency changes.

Fresh 2.21.2/v2 isolation controls also pass in
.cache/interop-observations/kiro-2212-isolation.xPRqtT, exit 0, 74.661s race-enabled package. Four
agent-directory cases (26.64s) preserve the selected launch profile despite a distinct session cwd
and a conflicting same-named session agent; the owned agent bytes stay unchanged. The active MCP
comparison (46.75s) starts and lists the owned global/project settings/mcp.json relays with explicit
inclusion, excludes both with false, and lists only the generated candidate's own relay. Flat file
locations remain inactive controls. Every observed relay/group is removed, pending work is zero and
private relay configurations are removed. No model prompt or client-tool effect occurs in these
read-only tests. These results refresh the initial paths; they do not establish reload/load safety.

The native-effect follow-up is separately opted in after these prerequisites, using the unchanged
owned-workspace prompt and limits from LIVE_KIRO_TEST_PLAN.md. It challenges one initial 2.21.2/v2
session only; successful real-client tool effects and complete resource exclusion remain separate.

That single native-effect attempt passes in
.cache/interop-observations/kiro-2212-native.7xUjn2, exit 0, 22.50s test / 23.784s race package.
The prompt completes in 6,037ms with 832 assistant text bytes, 63 notifications / 9,463 inspected
bytes, no canary and no tool event. Workspace/canary and candidate/settings bytes remain unchanged;
the observed relay/group and private configuration are removed with no pending work. This is fresh
initial-session evidence for 2.21.2, not an assertion about hidden reads, every future prompt or
remaining resource/reload/load paths. No raw response or provider-billed usage is retained.

Afterward, one separately opted-in actual Claude 2.1.263 / Kiro 2.21.2 single-Read denial also passes:
.cache/interop-observations/kiro-2212-denial.qyLSz4, exit 0, 24.03s test / 25.619s race package.
Exactly two backend requests carry one exposed Read and its matching client-hook denial, followed
by one final completion on the same ACP prompt. The client exits 0. Canary/source-setting checks,
relay/group disappearance and owned runtime cleanup pass; no canary is observed in checked model
output. The 1,838-byte client result is checked without saving its contents. Each of the two live
experiments ran once, with no retry; they may consume Kiro credits, whose billed amount was not
measured. The native and client-refusal results refresh distinct paths and do not enable run.

Whole-repository go vet, formatting, whitespace checks and the local development build pass.
Successful actual Kiro-generated Read/Write/Bash with client permission controls, remaining inherited
resource/skill sources and the prepared production policy remain the next development work. Unused
reload/load paths must stay disabled unless separately verified; optional web/usage and release soak
remain outside the development-launch prerequisite set.

## D60: original client names in relay metadata and actual tool effects (review R06/R14)

Opaque relay names previously lacked an explicit association with original client names. Each MCP
description now prefixes that association and the client's execution authority, followed by the
complete original description. Wire aliases and input schemas do not change. The private child
description limit is 8,192 + 256 bytes; source descriptions retain their full existing allowance.
Registry identity moves to version 2 so existing sessions cannot reuse the earlier metadata contract.
Independent tests cover empty and maximum descriptions, maximum names, unchanged schemas, compiled
MCP tools/list output and fingerprint invalidation. They fail before the corresponding changes.

The public [MCP tool definition](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
separates wire identity, optional display title and functional description. A description prefix keeps
the association attached to each immutable tool; optional title alone cannot establish what a client
passes to a model. This does not guarantee tool selection or diagnose earlier model behavior.

The actual Kiro 2.21.2/v2 and Claude 2.1.263 test permits one exact owned operation and its matching
result continuation per case. The initial six-case matrix passes allowed Read and Write, then stops
at Bash without a client call (live-client-effects.UFgQn0, 73.810s package). An independent guard
regression exposes rejection of equivalent JSON string escaping. Comparison now decodes values after
strict recursive duplicate validation, preserving command strings and numeric precision. A follow-up
still ends without a tool call (live-client-effects-followup.42eCGv, 23.475s), so encoding is not an
established explanation of the live failure.

With the original-name attribution, Bash reaches the relay with the exact command and one additional
description (live-bash-attribution.OJGk4l, 22.437s). The observer refuses it before client execution.
New positive/negative controls then admit only the optional Bash description as a nonempty single-line
string of at most 256 bytes. Changed commands, other added fields including timeout/background, bad
types and duplicate fields remain rejected. Other tool inputs still require exact key sets. This is
an experiment admission correction; production schema validation and client permissions are unchanged.
Only fixed field categories and counts are retained; actual descriptions and arguments are not logged.

The next single Bash case passes in live-bash-description.B3b2VO: 24.57s test / 25.858s race package.
It has one tool call, matching successful result, two requests and one same-prompt completion. The
client's pre/post hooks run, the owned target has the exact content, source/canary checks pass and the
relay/group/artifacts are removed. Claude exits 0. The description is admitted but not retained.
LIVE_KIRO_TEST_PLAN.md records the bounds and successive changes; no failed case is silently retried.

Two authorized local Claude consultations used generic public JSON/MCP questions only, with tools,
MCP and hooks disabled and 60-second/64-KiB bounds. Both exited 0 with one answer. Full answers and
reviews are saved under .cache/claude-consult/work/json-value-review-v81chywo and
.cache/claude-consult/work/tool-alias-context-review-0v0bdn_p. Adopted decoded-value comparison and
complete tool-name attribution; rejected mapping elision and unneeded hashes/arbitrary-key logging.
No earlier implementation or actual client inputs were supplied. No dependency changes.

The other five cases pass freshly with the new metadata in live-effects-attribution.ukF0s2:
116.82s test / 118.095s race package. Read takes 25.23s, Write 23.68s, denied Write 21.57s,
denied Bash 22.26s and hook-vetoed Bash 24.08s. Each has one matching call/result, two requests,
one same-prompt completion and client exit 0. Allowed effects have pre/post hooks; refused effects
are absent and have no post hook. The Bash hook refusal returns the exact expected reason.
All source/canary, relay/group and private artifact cleanup checks pass. Together with the separate
Bash case this establishes all six rule/hook paths on the current metadata contract. Interactive
screen evidence remains the independent fake-ACP result, and skill/source isolation and prepared
production policy remain separate development work. These model tests may consume account credits;
no provider-billed usage is measured. Raw model/client output is checked without retaining content.

Applicable uncached race suites pass: relay 10.436s, MCP 7.071s, registry 1.889s, session 17.319s,
launcher 21.850s and final interop 21.929s, with installed opt-ins disabled. Targeted malformed
annotation/command/JSON controls pass in 1.595s. Whole-repository go vet, formatting, whitespace
checks and the development build pass. Production run remains unavailable until the remaining
initial-session isolation evidence and built-in launch policy are connected.

## D61: active skill inheritance controls on Kiro 2.21.2 (review R06/R14)

The owned empty-agent observer now tests independently authored skill metadata at the launch
directory's .kiro/skills and the selected KIRO_HOME/skills. It first declares both absolute skill://
files explicitly, then compares resources=[] with default inheritance enabled and disabled in fresh
roots. Each fixture has a unique name, no actions and preserved source bytes. No model, skill or tool
is invoked; only the existing tools/context inspection runs, with a one-second notification window.

The [Kiro inheritance changelog](https://kiro.dev/changelog/cli/2-10/) describes suppression of default
skills alongside steering and AGENTS.md. [Context documentation](https://kiro.dev/docs/cli/chat/context/)
distinguishes startup skill metadata from on-demand content. These supply hypotheses for the pinned
black-box observation, not an assumption that the current documentation proves older CLI behavior.
The public [ACP command advertisement](https://agentclientprotocol.com/protocol/v1/slash-commands)
is optional, and its absence alone cannot establish resource exclusion.

The first explicit control reports two matched context files but no skill command advertisement
(skill-inheritance.QlUrg1, 6.473s); its command-only observer fails. After adding exact owned context
path matches, the explicit control passes but inheritance reports the launch skill relatively
(skill-context-inheritance.4ZE6Rg, 11.157s). The final observer recognizes that independently seeded
relative name under a separate label without inventing a base for arbitrary relative names.

All three cases then pass in skill-context-forms.EHIaIQ: 16.44s test / 17.755s race package.
Explicit resources match both absolute paths, with context estimate 98. Default inheritance matches
the selected configuration root absolutely and the launch skill relatively, with estimate 86.
Suppression reports no matched context items and estimate zero. These are local estimates, not billed
usage. No skill names are observed in command advertisements in any case. Source bytes and joined
process cleanup pass throughout. The result establishes initial suppression for these two active
skill sources, not dynamic reload, persisted-session load or every possible undocumented source.

Independent tests reject foreign-session and unrelated-description/text matches. Existing malformed
context/query-budget controls remain active. One interim fake initial initialize call times out before
the changed observer runs; an unchanged focused follow-up passes in 3.067s, and the final focused
suite passes in 3.266s. Its cause is not established or attributed to the observer. The final uncached
interop race suite passes in 24.082s, with installed opt-ins disabled. Whole-repository go vet passes.
No production policy or dependency changes; this closes the measured skill-source gap before the
prepared development launch policy is connected.

## D62: admit the measured initial-session development run (review R06/R14)

D59-D61 establish the development gate's initial native-effect challenge, active configuration/MCP/
resource exclusion, actual client-approved Read/Write/Bash, matching denials and hook vetoes, source
preservation and observed cleanup. On that evidence, the built-in launcher policy now admits Kiro
2.21.2/v2 on macOS arm64 with the separately pinned Claude Code 2.1.263. The execution version has
its own literal pin: changing finite preflight support alone cannot inherit this verification.
There is no user-provided trust assertion or override. This is development admission, not alpha or
release completion. Persisted-session loading stays rejected for prepared launches.

PrepareKiroExecution is shared by startup and the actual tool harness. The caller owns its private
runtime until all backend cleanup joins. The generator creates a fresh KIRO_HOME, suppression settings,
scratch and agent parent; each prepared process gets a separate agent directory and the exact supplied
registry/relay. Its fixed environment carries only HOME, KIRO_HOME, PATH, TMPDIR, TERM and LANG. Existing
HOME serves Kiro authentication; no credentials or user Kiro configuration are copied. Public ACP keeps
empty client capabilities and request refusal, and the Kiro authentication classifier is connected.
The original project remains session cwd while process cwd stays owned. Preparation checks the runtime
inode and suppression bytes; callback cleanup checks its original directory and is repeatable.

Startup passes its own temporary root to policy preparation and removes it after joined owners, even
for doctor or a failed setup. Configuration errors retain their own classification instead of being
misreported as unverified policy. Catalog identity now includes the development policy identifier;
ACP capabilities remain unknown until negotiated. No dependency changes.

Independent generator tests cover exact empty/Read/Bash relay policies, immutable wire bindings,
private settings/environment, repeated cleanup, cancellation, changed settings and unknown versions.
The stub first fails the positive preparation test. Initial fixture-name compilation errors are fixed
before that behavioral regression. Pre-wiring launcher/interop race suites pass in 22.462s/23.687s.

The shared generator then passes actual Bash approval and hook refusal in
prepared-execution-effects.bruomf: 46.78s test / 48.099s race package. Each has one call/result, two
requests and one same-prompt completion. The allowed file and pre/post hooks match; the hook refusal
creates no target/post marker and returns the exact denial. Both client exits are zero, source/canary
checks pass and all observed process/relay/artifact cleanup joins. Prompts, results, arguments and
canaries are not retained. These bounded model checks may consume credits; billed usage is unmeasured.

The compiled command test now uses an owned PTY because run requires a foreground terminal. Its fake
Kiro validates the generated agent/environment and execs the independent ACP peer, which requests a
synthetic client tool and receives its result. Initial pipe-only execution fails before ACP; the PTY
control exposes a fixture assumption about /var versus /private/var, corrected by comparing the actual
owned directories. The observer also separates the terminal's leading EOF echo from the fixture JSON.
The final doctor/models/run command suite passes in 7.965s, with client/launcher groups gone and runtime
and source settings checked. Earlier failures are not claimed as passing execution evidence.

The rebuilt executable's actual doctor passes in development-doctor.bcBw5G with temporary state and
settings: exact installed versions, login verified, 19 catalog models, fresh auto selection, policy
verified and launch_available true. Login takes 2,554ms, catalog 8,432ms and policy preparation 15ms.
No ACP session, client launch or model prompt is sent by doctor; client_initialization remains
unverified. Full default-client traffic, environment overlay preservation and live alpha/release
lifecycle gates remain subsequent work.

Final applicable uncached race suites pass with installed opt-ins disabled: launcher 21.902s,
interop 23.100s, session 17.055s, catalog 1.416s and command 1.326s. Whole-repository go vet,
formatting, whitespace checks and the local build pass. The actual doctor's temporary runtime is
empty after return and its owned source settings remain unchanged.

## D63: default client tools and bounded instruction recovery (review R10/R14/R16)

The installed Claude Code 2.1.263 now runs an ordinary print request with its default system prompt
and tool list through the actual decoder, schema worker, session driver and HTTP/SSE gateway. Its
25 custom tools include Read/Write/Bash; the maximum description is 3,502 bytes, with no deferred
tools, typed server tools or unknown declaration fields in this observation. Thinking,
context_management and output_config are present. The text case passes without a production
compatibility change; accepting these hints still makes no provider-enforcement claim.

The default-tool Read-hook-denial case exposes a real continuation gap. Both requests retain the
same identity, model, effort, top-level system, metadata, declarations and prior-history prefix.
The assistant's tool input and result ID also match. But the trailing single per-message system
text changes from 7,231 bytes to 49 bytes. No meaning is inferred from this size change: the observer
retains fixed counts/Booleans only, and no client instructions, descriptions, schemas or results are
saved. This fails D25's exact-repeat rule and leaves the prompt waiting until test cleanup.

D25's same-prompt behavior remains. A separate recovery path now supports a single changed text-only
system message following a user message containing only matching tool results, when exactly one
standing system message immediately preceded the handoff. It requires every prior message anchor
as a complete prefix, unchanged compatibility, and all sealed, successfully delivered result IDs.
Truncated overlap, changed earlier history, multiple standing messages, wrong owner/model/effort/
registry/metadata, missing/extra/duplicate results and replay cannot authorize it.

Before mutation, the driver validates the complete fresh projection against negotiated capabilities
and the ACP frame bound, reserving worst-case JSON escaping for the maximum accepted session ID.
The relay's Abandon operation shares Resolve's result validation and encoding limits. Under the
broker lock it verifies the delivered batch and revokes every old call, including queued calls.
Actual result content is never supplied to the old prompt. The driver joins old relay/process cleanup
and preserves any confirmed authentication/cleanup failure before preparing a fresh session. The
new prompt includes all supplied history, the already-executed tool request/result and new instruction.
No tool is automatically replayed. Failed replacement consumes no reusable result ownership.

One instruction recovery is allowed per logical turn. Its original start time and absolute deadline
carry through replacement setup and any subsequent tool handoff; a new HTTP request cannot reset
them. Exhaustion rejects further changed suffixes without consuming pending calls. An exact repeat
can still resume that prompt. An expired owner cannot recover through this path. D57's separate
new-question-after-denial behavior is unchanged. There is no persistence-format or dependency change.

This is bounded context reconstruction, not equivalence to the retired backend's hidden state.
It can add a provider request and latency and loses any unreported backend context. ACP describes
continuing after a completed turn, not an arbitrary mid-prompt system-instruction replacement:
[prompt-turn lifecycle](https://agentclientprotocol.com/protocol/v1/prompt-turn) and
[session creation](https://agentclientprotocol.com/protocol/v1/session-setup), checked 2026-09-09.
The choice to retire and reconstruct is this project's interoperability policy, not an ACP guarantee.

An authorized local Claude consultation receives only this abstract protocol question, no repository
or client payload. Its one answer is saved and reviewed in default-continuation-review-gi8o1ix9.
It supports strict-prefix validation, joined retirement, shared deadlines, bounded retries and explicit
hidden-context/cost limitations. Its suggested production opt-in does not substitute for evidence;
development admission follows independent checks, while actual Kiro recovery remains an alpha gate.
Its description of all truncated overlap as a forgery is not adopted: normal proven overlap remains
supported elsewhere; this new recovery deliberately requires a stronger proof.

Independent broker tests first fail on the missing Abandon API. They then verify undelivered/wrong/
duplicate/oversized result rejection without mutation, terminal revocation of sealed and later calls,
and absence of supplied result content in canceled calls. Session tests first reject valid recovery,
then pass success/error results, complete-history reconstruction, old-group disappearance, failed
replacement, preserved deadline, replay rejection and the one-restart allowance. A valid truncated-
overlap control establishes that this path rejects an overlap the normal planner would otherwise
accept. Existing repeated multi-system instructions and D57 denial recovery are covered together.

Actual-client observations, all using independent fake ACP and no Kiro model request:

- default-client.dnatym: text passes in 4.44s test / 6.248s race package.
- default-client-relay.3OTIJz: text passes; initial denial continuation fails (7.153s package).
- default-continuation.tGQo2h, default-history.yw7LuU and default-standing.2pHYVL: progressively
  bounded comparisons isolate the unchanged owner/history and changed standing text. These remain
  failed observations (3.297s, 4.979s and 4.799s), not passing evidence.
- default-client-recovery.mON5iG: both pass in 5.75s test / 7.585s race package. Text has one request;
  denial has two HTTP requests and one fresh ACP replacement. The fake verifies the historical Read,
  exact returned denial, old and new instructions, and no old process group before replacement.
  Both observed groups are gone after cleanup, source settings are unchanged, and client exits are 0.

The initial session-focused recovery/repetition/D57 suite passes in 7.132s. Full applicable regression
results are recorded in DEVELOPMENT_STATUS.md. This establishes default-client/fake-ACP compatibility,
not live Kiro default-tool recovery, complete client asset preservation or release readiness.

Final uncached race suites pass with installed opt-ins off: relay 9.972s, session 19.542s,
Anthropic validation 8.089s, gateway 3.362s, interop 21.739s, launcher 21.774s, command 1.554s
and ACP 4.851s. Whole-repository go vet, formatting, whitespace checks and the rebuilt development
executable pass. All added fixtures and requests are independently authored from this repository's
contracts, the public protocols and unmodified-client observations; no earlier implementation is used.

## D64: preserve native client MCP scopes in the private runtime

The temporary Claude configuration root hid user and local MCP declarations. Independent public
`mcp add --scope` and `mcp list` controls establish that all three native scopes connect with the
ordinary owned HOME, but only project MCP connects with the old prepared profile, even when that
profile is created after registration. The natural list command also changes the owned global
configuration file. Simply removing CLAUDE_CONFIG_DIR therefore fails the existing byte-preservation
requirement; that requirement is retained.

PrepareClient now reads the standard HOME/.claude.json through the same bounded, owner-checked,
regular-file, no-follow reader as source settings. A missing file remains absent in the source.
Before creating runtime artifacts, strict JSON validation projects only MCP state into the private
client root's .claude.json. Public client writer commands independently establish this destination
and the native top-level mcpServers / projects[original path].mcpServers shape. Server declarations
are not combined into a command-line MCP scope: validation, connection, precedence and permissions
remain the client's responsibilities. The proxy does not run these servers or their tools.

The projection keeps mcpServers objects; enabledMcpjsonServers, disabledMcpjsonServers,
enabledMcpServers, disabledMcpServers and mcpContextUris string arrays; enableAllProjectMcpServers;
and per-project hasTrustDialogAccepted Booleans (D118 later writes a yes answer to that dialog back
to the source, the launcher's only write to a source client file). It preserves original project
keys and declaration values, including tool-specific MCP authentication, in owner-only temporary
files. It excludes unrelated provider sign-in, model defaults, conversation and other global state.
D111 records the reviewed exclusion of the client's `claudeAiMcpEverConnected` breadcrumb. Other
unrecognized MCP-related keys reject rather than silently dropping a possibly restrictive policy.
Malformed objects, duplicate keys, null array elements, unsafe source modes/links and files over 2
MiB reject before runtime creation. This is a pinned mapping, not a general client-state migration
facility.

The public [MCP reference](https://code.claude.com/docs/en/mcp) documents native scopes and the
distinct per-project server toggles versus .mcp.json approvals. The public
[settings reference](https://code.claude.com/docs/en/settings) distinguishes user settings from
mutable global state. Both were checked on 2026-09-09. The installed-client tests use only owned
temporary inputs and an independently authored standard-library MCP peer. The peer returns an
effect-free tool and records only its own PID and fixed lifecycle events. No tool is called in
these configuration controls, and no Kiro model request is made.

Recorded observations, preserving failures and their narrower conclusions:

- client-mcp-sources.YPZNUt fails because an unapproved project server does not initialize.
  This is not exclusion proof; the later positive control explicitly approves that owned server.
- client-mcp-approved.n3PXAE activates all three scopes but fails source byte equality after the
  natural client changes its global file. It does not satisfy profile preservation.
- client-mcp-profile.Vs1DCc (3.412s) and client-mcp-writer.SLzlqW (3.944s) confirm missing user/local
  connections in the old profile; the latter also proves the private writer location/scope shape.
- client-mcp-preserved.F5bngO passes after projection: all three scopes connect in both natural
  and prepared runs, while the prepared run and private writer leave all source bytes unchanged
  (2.43s test / 4.221s race package).
- client-mcp-policy.PJfmri passes fourteen natural/prepared controls (6.33s test / 8.137s race
  package). A disabled user/project server does not initialize, re-enabling restores both, and a
  project refusal defeats a matching approval. For equal server names, only the local declaration
  starts; removing it activates project, and removing project activates user. Fresh prepared
  profiles reproduce every result. All observed peer processes are gone, no tools were called,
  original sources remain unchanged by prepared commands, and temporary profile cleanup succeeds.

Unit regressions cover provider-state exclusion, native scope/decision retention, temporary secret
file permissions, repeated cleanup and unsafe/ambiguous inputs. Added tests first expose rejection
of documented enabledMcpServers and incorrect acceptance of null string-array elements; both are
corrected. These tests verify retention of default-off opt-ins, not actual activation of built-in
servers. Broader installed-client regression results are recorded in DEVELOPMENT_STATUS.md.

The authorized local Claude consultation is saved and independently assessed in
client-assets-review-xyum00fk. Its active preservation controls are useful. Its suggestion to relax
byte preservation is not adopted; symlinks do not themselves prevent writes. Its claim that no
read-only asset path is documented is too broad: the public
[plugin seed contract](https://code.claude.com/docs/en/plugin-marketplaces#pre-populate-plugins-for-containers)
provides one. That contract needs its own installed-client controls before integration.

This closes the measured standard-HOME MCP declaration/decision gap only. Plugin/skill/agent assets,
existing status commands, custom configuration roots, remote MCP OAuth and environment-dependent
credentials remain separate preservation work. General asset compatibility and release readiness
are not claimed. Development run admission and provider routing are unchanged; no dependency is added.

The final installed regression client-mcp-regression.qMLLwo passes in 15.642s with the final mapping:
MCP controls, ordinary default-tool text/Read denial, user/project permission and hook preservation,
and disabled hooks. Applicable uncached race suites pass with installed opt-ins off: launcher
22.466s, interop 22.239s and command 1.775s. Whole-repository go vet, formatting, whitespace checks,
the local build and executable help pass. The development executable includes this MCP correction.

## D65: read-only plugin seed during full client startup

The native private client profile also hid an installed user plugin. An independently authored local
marketplace installs one plugin through the public client CLI; the plugin supplies the existing
effect-free MCP fixture. Natural mcp list initializes that peer and fetches tools/list. An otherwise
identical profile without a plugin seed does neither. A list of configured names alone is not the
positive control.

PrepareClient now supplies standard HOME/.claude/plugins through CLAUDE_CODE_PLUGIN_SEED_DIR when
that source exists. Both .claude and plugins must be owned directories without group/other write
permission or a symlink at either entry. A path-list separator in an existing seed path rejects
rather than naming extra roots. A missing source remains missing. These checks occur before runtime
creation. The proxy neither copies plugin content nor invokes install/update commands. Client
enablement comes from the existing settings snapshot; mutable runtime state stays under the private
configuration root. Custom asset/configuration roots remain separate compatibility work.

The public [plugin seed contract](https://code.claude.com/docs/en/plugin-marketplaces#pre-populate-plugins-for-containers)
describes reading a source cache without writing it, including full interactive and print startup.
The [plugin reference](https://code.claude.com/docs/en/plugins-reference) supplies the manifest,
installation and enable/disable contracts. The [CLI reference](https://code.claude.com/docs/en/cli-reference)
defines init-only separately from a conversation. These sources were checked 2026-09-09; installed
observations here cover Claude 2.1.263 full print startup, not interactive plugin behavior.

Finite commands were insufficient in this experiment. mcp list, plugin list and init-only with a
fresh seed profile did not activate or list this plugin. Full print startup did start its peer, but
the initial synthetic response finished before the MCP initialization completed. The observer now
holds its local response for at most two seconds while the owned peer completes initialization and
tools/list. The disabled case retains that entire observation window. This changes no product
timeout and introduces no external model request or tool call.

The first print request still has zero advertisements of this plugin's tool. The successful source
and enablement checks must not be reported as a completed model-driven plugin tool round trip.
Later tool availability and any registry change during a pending tool turn need independent tests.
Plugin hooks/skills/agents, remote marketplaces, custom roots and interactive plugin startup remain
open. The launch policy and development/release gates are not broadened by these observations.

Recorded experiments:

- client-plugin-seed.O8mYzZ fails (2.838s package): natural activation succeeds; finite seed lookup
  does not. client-plugin-init.jTmJbL (3.043s) also fails after init-only.
- client-plugin-layout.G8QZrc fails (3.299s). The local installer references its own directory and
  does not create marketplaces/<name>. Copying the independently authored marketplace into that
  layout did not fix finite commands. Later full startup succeeds without that copy.
- client-plugin-print.4xwpjT fails before the synthetic request (3.019s): the fixture placed its
  prompt after the variadic mcp-config option. Argument ordering was corrected, and the final
  observer removes that unnecessary MCP option entirely.
- client-plugin-startup.VDX08c fails (3.536s): one full-startup peer starts but does not finish
  initialization before the immediate synthetic completion. This is not a passing readiness check.
- client-plugin-ready.knvqsC passes initialization controls (5.801s). The stronger discovery
  controls in client-plugin-discovery.jfCeEd pass in 8.138s, adding tools/list and a full two-second
  disabled observation window.
- client-plugin-prepared.eHhkoq passes with the production seed generator (8.203s). Natural,
  explicitly unseeded, finite seeded, enabled print, disabled print and re-enabled print controls
  have the expected connections, initialization and discovery counts; all tool-call counts are 0.
- client-plugin-regression.G7EBDR passes the final combined installed regression in 22.414s. This
  removes the extra MCP flag and checks source tree entries, file/link content and modes. Plugin
  controls take 6.08s, MCP controls 6.33s, default-tool text/Read denial 4.95s, source policy/hooks
  1.94s and disabled hooks 1.53s. All observed peer processes and temporary profiles are cleaned up;
  source settings and plugin files are unchanged by prepared runs.

The authorized local Claude answer and independent assessment are saved in
plugin-seed-review-tbg5c_sh. Its suggestion to compare finite and full startup was useful. Its claim
that the initial add/install baseline proves reconstruction into another profile is not adopted;
that baseline only created the source fixture. Arbitrary plugin reinstallation is not this product's
preservation mechanism. No previous implementation, user plugin content or client payload was sent.

Unit tests first expose the absent seed reference and acceptance of unsafe/ambiguous roots, then
pass after integration. Applicable uncached race suites with installed opt-ins off pass: launcher
22.836s, interop 24.018s and command 1.571s. Whole-repository go vet, formatting, whitespace checks,
the local build and executable help pass. The development executable includes the seed reference.
No dependency is added.

## D66: client plugin tool effects, refusal and bounded continuation

D65 established source activation only. Independent black-box tests now distinguish immediate
fresh print startup, a second print launch of the same owned private profile, and interactive startup
whose input is held until the owned MCP peer has initialized/listed and the project prompt is visible.
There is no Messages request before interactive input. The observer checks the echoed prompt before
Enter and completion on the reconstructed terminal screen; no raw terminal data is saved.

The fresh immediate print observations fail to find either the plugin declaration or a client wait
tool: 25 default tools, zero MCP declarations, valid Messages decoding. They do not prove later
unavailability. One local synthetic text warmup followed by a second launch of the same private
profile advertises and invokes the owned effect-free plugin tool. This warmup is fixture-only; the
launcher does not send a hidden model prompt or invent an undeclared client tool. The synthetic
observer can request one advertised WaitForMcpServers only after isolated schema validation, but no
passing case needed that fallback. Availability of that tool varies among these observations.

The controlled cold interactive case also advertises the plugin before its tool turn. It completes
with one exact fixed tool result. This is evidence for input after observed readiness, not arbitrary
immediate input or general dynamic MCP registry changes. Its optional title request is narrowly
classified and answered synthetically, separately from the main request budget.

The stronger interactive pair uses the product's authenticated gateway, isolated schema worker,
relay and session driver with independent fake ACP. Client allowance calls the owned MCP probe
exactly once. A source user PreToolUse hook refuses the same advertised tool: zero native calls and
one matching error result. The fake validates the exact result ID, error flag and owned result text,
then completes. User settings, global state and plugin source tree entries/content/modes stay
unchanged by prepared runs. Client/MCP processes and both observed backend groups are gone.

In both cases the client replaces one standing system suffix (8,323 bytes to 49) after returning
the result. Identity, model, effort, top-level system, metadata, tools and full prior history match.
D63 retires and joins the old prompt/process before reconstructing once with the complete supplied
history, result and new instruction. There are exactly two main requests and two backend processes,
with no native tool replay. This validates the existing recovery against another real-client shape;
it does not validate that recovery with Kiro or retain hidden backend context. No product behavior
or launch gate changes in this decision.

Recorded private normalized observations retain fixed labels, counts, equality flags and owned PIDs
only; request content, tool payloads, credentials, terminal data and unrestricted stderr are not saved:

- plugin-tool-shape.3mw6oI and plugin-tool-envelope.vyLy7s fail the immediate-print tool selection
  (4.543s and 3.968s). The latter records valid decoding and absent plugin/wait declarations.
- plugin-tool-warm.qP6Sbh passes the second-print exchange (6.323s race package), including one
  native call, exact result, source preservation and cleanup.
- plugin-tool-interactive.jSH3rO fails an observer's raw ANSI text check (5.340s) despite the
  reconstructed screen and tool exchange completing. The corrected rendered-screen assertion
  passes in plugin-tool-ui-shape.2cqGFU (5.900s). The failed attempt is not acceptance evidence.
- plugin-tool-proxy.92MCxg passes the real gateway/relay/session allowed case (7.828s).
- plugin-tool-permissions.bcnkOA passes allowed and hook-denied cases (14.143s), with native
  call counts one and zero. Its zero tool-count summary was an unpopulated observer field, not an
  absent registry; that summary is populated from the client envelope in the final regression.
- plugin-tool-regression.3BY4Qd passes in 30.723s under race instrumentation: six D65 source
  controls, warmed print, controlled interactive shape, both real-gateway plugin outcomes and D63
  ordinary default-tool text/Read-hook refusal. Observed tool counts are 26 for warmed print,
  29 for the synthetic interactive shape and 30 for the real-gateway pair. Both plugin main
  continuations retain the same tool registry and complete after exactly one reconstruction.

A detailed local-Claude consultation was rejected by automatic approval review before execution:
the review did not establish authorization for the specific nonpublic payload and destination.
That payload was not sent. A separately approved public-only question, containing public reference
URLs and general startup questions, completed and was read and assessed in
public-mcp-startup-review-3uuwo12e. Its suggestions about mode/origin controls were useful; its
uncertainty about init-only and overbroad ordering claims were corrected using the public
[CLI reference](https://code.claude.com/docs/en/cli-reference) and
[MCP reference](https://code.claude.com/docs/en/mcp), checked 2026-09-09. Advice is not acceptance
evidence. No previous implementation or user assets were accessed or transmitted.

Applicable uncached opt-ins-off race suites pass: ACP 5.697s and interop 21.071s. Whole-repository
go vet and formatting/whitespace checks pass. No dependency changes. Plugin-provided hooks, skills
and agents, existing status commands, remote assets/OAuth, custom roots, dynamic registry changes,
immediate fresh print bootstrap and actual Kiro plugin/reconstruction remain separate work.

## D67: preserve native status commands and their refresh behavior

The prior profile preserved source bytes but unconditionally set the product statusLine in the
command-line overlay. An installed-client positive control confirms that the source user command
never runs while the product display polls twice. Byte preservation alone did not preserve behavior.

The product display is now an optional user-scope default. An existing user statusLine value is
kept exactly, including an empty/null choice; the proxy does not repair or add fields to it. The
client decides whether such a value is valid. The command-line layer carries no statusLine. Native
client precedence therefore selects project/local settings. Routing/authentication and the separate
additive startup/metrics hooks retain their existing layers and credential separation.

Moving the default to user scope was insufficient on its own. With a project command and no
refreshInterval, the product's lower-scope five-second timer still caused a later invocation during
an otherwise quiet seven-second observation. The final implementation suppresses the entire default
when a project source contains a statusLine key or cannot be checked safely. It examines only the
known settings.json/settings.local.json names under .claude, conservatively walking ancestors until
a normal owned repository root or filesystem root. Worktree/git metadata files are not opened or
resolved to another checkout; that uncertain case suppresses the default. Unsafe/link entries,
malformed settings, read errors, the 64-directory limit or the aggregate 2 MiB allowance also omit
the optional default. Each individual read retains the existing bounded file checks. A default
that would push the private user snapshot over its 2 MiB bound is omitted as well.

This check is not an independent settings resolver. Conservative suppression can omit the product
display even when the client would ignore an ancestor source. It neither changes original settings
nor blocks preparation on optional-display uncertainty. The remaining source settings and separate
UI hooks still belong to the client; the product status helper still receives only its private UI
credential through the existing isolated command. Managed policy sources, custom configuration
roots and status changes made after startup are not covered by these new controls.

The public [settings reference](https://code.claude.com/docs/en/settings) documents native scopes,
local-file placement when starting below a repository root and worktree exceptions. The public
[status-line reference](https://code.claude.com/docs/en/statusline) documents command execution and
event-driven versus configured periodic updates. Both were checked on 2026-09-09. Field merging and
the resulting invocation counts here come from the pinned unmodified client, not an assumption
that a higher-scope object replaces every lower-scope member.

Independent installed tests create owned user/project/local settings and fixed status commands.
Each command acknowledges execution only to a bounded loopback observer and prints its fixed scope
label. It neither saves client stdin nor requests a model. Tests assert actual display, expected
refresh behavior, zero calls to lower-priority/product commands, source byte equality and joined
client process cleanup. They retain normalized fixed flags/counts only; no terminal payload or
credentials are saved. The default product display and its isolated helper retain prior coverage.

Recorded observations:

- status-existing-before.TFn7aI fails in 7.774s: the owned user command has zero calls and no
  display; the product polls twice. No model turn occurs and client cleanup still succeeds.
- status-native-scopes.8zRrCu passes four command-precedence cases and the default five-second
  product display after the first change (21.231s), but does not establish timer preservation.
- status-event-only.IlJ2Yr then fails in 9.600s: the project command renders, yet runs twice over
  7.144s, with one late call caused by the remaining periodic default. This is not acceptance evidence.
- status-existing-preserved.su5wci passes the final five-case scope/timer matrix, default display,
  held startup-hook ordering, two synthetic completion turns and disabled hooks in 47.718s. The
  event-only project command runs exactly once over 7.139s with zero late calls. Existing status
  cases make zero product status requests and zero model requests; all source bytes and observed
  cleanup pass. Independent startup notices remain visible, including while product status is absent.
- status-policy-regression.WhdXnq separately passes enabled user/project hooks, permission refusal
  and gateway routing in 2.985s. This test's exact name was absent from the prior selection; it is
  not claimed as part of that combined run.

Unit controls first reproduce the unwanted host override and merged default, then pass after the
correction. They cover unchanged user values, project/local/ancestor choices, linked worktrees and
unsafe/ambiguous settings while retaining independent hooks/routing. Applicable uncached race
suites with installed opt-ins off pass: launcher 23.325s, interop 21.563s and command 1.535s.
Whole-repository go vet, formatting, whitespace, build and executable help pass. No dependencies
change. The development executable includes this fix; alpha/release gates remain open.

## D68: preserve first-session plugin registrations, skills and hooks

A read-only content seed alone does not preserve the pinned client's first skill/hook lookup in a
fresh private profile. An active native-HOME control advertises an independently authored skill,
runs its SessionStart and Stop hooks once, includes its startup context and completes a synthetic
text turn. The same seed without native registrations omits both assets; direct namespaced skill
invocation then makes no model request. Canonical seed placement, init-only warmup and waiting for
an initial hook do not establish activation. Retaining only installed_plugins.json is also
insufficient. Both native registration records are required in the measured configuration.

Preparation now reads exactly installed_plugins.json and known_marketplaces.json from the validated
standard-HOME plugin root before creating runtime state. Each existing record must pass the existing
owned regular-file, permission/link and 2 MiB read checks plus strict JSON object validation. Missing
records stay absent; malformed or unsafe records fail preparation. At most two records and 4 MiB are
copied into the private client/plugins directory, with directory mode 0700 and file mode 0600.
Raw record bytes, scopes and values are retained for native client interpretation. Plugin content
continues to use the read-only seed. The product performs no installation, refresh or source mutation.
Private state is removed by the existing runtime owner; source enable/disable settings stay native.

The public [marketplace reference](https://code.claude.com/docs/en/plugin-marketplaces) describes
the read-only seed, primary mutable configuration, native cache lookup and blocked seed management.
The [plugin reference](https://code.claude.com/docs/en/plugins-reference) documents skill/hook layout
and the plugin-root substitution; the [skills reference](https://code.claude.com/docs/en/skills)
documents user-invoked namespaced commands. These were checked on 2026-09-09. The need for both
registration snapshots is a pinned-client black-box result, not a general protocol requirement.

Eleven installed-client controls cover active native HOME, first prepared text/skill startup,
interactive skill invocation, plugin disable/re-enable, hook suppression/re-enable and three private
management operations. The skill's fixed body reaches the request only on explicit invocation. An
enabled plugin contributes startup context and one start/stop hook; disabling it removes all three,
while disabling hooks retains skill expansion but removes hook context/effects. The built-in Skill
declaration is observed but not model-invoked. All model responses are bounded local fixtures; these
asset checks do not exercise Kiro or a model-selected skill through the session driver.

The management controls verify original registration/settings bytes and complete bounded plugin/
marketplace trees, including entry types, content, permissions and symlink targets. Private uninstall
finishes without changing the original source. Directory-source update/removal also return success
without source changes; this is not evidence that those commands were refused. A separate canonical
Git source rejects update/removal with a seed-related diagnostic. The fixture has a second local
revision available before those attempts, and a later natural-HOME update actually applies it.
Thus preservation is tested against a real possible update, not only an unchanged remote.

All Git repositories here are newly authored local fixtures. An owned-HOME URL rewrite maps one
reserved .invalid HTTPS URL exactly to the local .git directory; ls-remote verifies the mapping
before client installation. No external repository is fetched. The public client rejects the two
attempted file-URL forms, which remain failed setup attempts. The existing system Git is only an
external test tool, recorded separately in DEPENDENCY_REVIEW.md, with no added application module.

The combined regression also invalidated D66's weakest readiness assumption. Server initialization
and tools/list can finish before the client includes that tool in its first Messages registry. One
synthetic turn instead advertises WaitForMcpServers, changes its registry afterwards and coalesces
the prior wait result with a later plugin result. Its bounded observer now checks the one exact
plugin result and, if repeated, the exact prior wait result using an in-memory digest; unknown or
duplicate results still fail. This observation changes no product result/history validation.

The controlled interactive MCP test now also opens the client's public `/mcp` panel, observes its
owned connected server, closes the panel and verifies the ordinary prompt before submitting input.
There is no preceding model request. This is a stronger measured readiness control, not a launcher
automation or an immediate-input compatibility fix. The real gateway/fake-ACP allowance and hook
refusal cases retain unchanged registries, exactly two main requests, one D63 reconstruction and
native call counts one/zero. All client, hook, MCP and backend processes are joined. Dynamic registry
changes, coalesced history through the driver, arbitrary cold input and actual Kiro remain open.

Private normalized evidence includes:

- plugin-hook-skill.5nbd9i and plugin-skill-direct.ca6xPk fail first lookup; the active native
  control plugin-hook-natural.GtjEdj passes in 2.754s. Canonical/init-only/interactive-wait and
  single-registration controls fail; plugin-assets-registries.CkskaD passes both-record activation
  and private uninstall in 3.885s. These diagnostic copies contain only independently authored data.
- plugin-assets-preserved.oCGmnm passes all eleven controls through the production constructor
  in 9.495s. Unit tests first fail without registration preservation, then pass in 4.303s.
- plugin-registration-mcp.fShyWo fails in 45.372s on the synthetic observer's coalesced wait result,
  while the actual gateway pair passes. plugin-git-source.CYZ4qO and
  plugin-git-classification.XQlnkx reject file-URL setup; neither is accepted evidence.
- plugin-git-rewritten.SGDABw passes all three Git mutation controls and the active native update
  in 5.684s. Final controls additionally classify seed refusal and fingerprint the owned Git config.
- plugin-registration-regression.YrMMSZ fails in 69.991s: the denied real-gateway turn starts before
  the plugin is advertised despite server initialization/listing. Its eleven hook/skill and three
  Git controls pass, but this combined run is not reported as a pass.
- plugin-panel-readiness.0MmKIC passes the denied gateway turn after the panel control (7.697s).
  plugin-registration-final.rLXV0p passes the final ten-test installed regression in 51.515s under
  race instrumentation: native MCP sources, plugin source controls, warmed tool, interactive tool,
  real-gateway allowance/refusal, default tools, hook/skill and Git controls, enabled policies and
  disabled hooks. The source/cleanup checks pass; no external model call occurs.

The approved public-only local Claude consultation is saved and assessed in
public-plugin-readiness-review-kps_w578. Its generic active/disabled controls were useful; its
outdated uncertainty about the seed/init-only interfaces and model-only skill claim were rejected
against the primary references. No repository payload, user asset or previous implementation was
sent. Advice is not acceptance evidence. Observations retain fixed labels/counts/Booleans/owned PIDs
only; raw prompts, tool outputs, terminal content and command diagnostics are not persisted.

Applicable uncached opt-ins-off race suites pass: launcher 21.915s, interop 21.068s and command
1.355s (plugin-registration-unit.dgoLxd). Whole-repository go vet, formatting, whitespace, build and
executable help pass. The development binary includes this correction. Broader assets, plugin-owned
permission hooks, standalone user skills/agents, custom roots and alpha/release checks remain work.

## D69: reproducible client wait and cumulative tool history

Correction: D70 isolates the cumulative grouping to this fixture's repeated message ID. Fresh IDs
produce ordinary appended history. This section preserves the historical observation and proposed
investigation; its grouping is not an ordinary-client requirement and no regrouping exception was
adopted in the product.

D68's startup race now has a controlled black-box reproduction. An optional mode of the independently
authored MCP peer holds initialize for at most ten seconds. The interactive client receives input
only after that hold is observed. The local synthetic responder requires an advertised
WaitForMcpServers with a schema that accepts its empty object before creating an owned release marker
and returning the tool request. No earlier model request, extra warmup, private API or arbitrary sleep
establishes readiness. The client then advertises the owned plugin, calls it once and completes after
returning the exact result. All requests, output, process lifetimes and observations retain bounds.

There are three main requests plus a separately classified synthetic title. The initial request has
no plugin declaration and does advertise the wait tool. Later requests have a changed registry. A
history observer uses the product's existing keyed canonical block comparison, saving only block
kinds and equality ordinals. Ignoring already-defined cache hints, the history evolves as follows:

| Request | Supplied messages after the unchanged initial user/system prefix |
| --- | --- |
| Initial | None |
| Wait result | assistant(wait call), user(wait result), system(first updated standing message) |
| Plugin result | assistant(wait call, plugin call), user(wait result, plugin result), system(second updated standing message) |

Both earlier wait blocks remain exactly equal. The client's last request groups all calls and all
results into cumulative messages, replacing the preceding trailing standing message. The exact
original prefix, owner, model, effort, top-level system and metadata remain equal. This differs from
ordinary append-only history. An initial raw-block observer also saw a cache-hint difference in the
original system block; canonical comparison correctly removes that false content-change signal.

This test observes the unmodified client against a synthetic Messages endpoint. It does not pass the
three-request sequence through the product session driver or actual Kiro. D68's separate real-gateway
allow/refusal pair still passes with ready tools and an unchanged registry. Existing driver checks
require compatible registry, exact pending history and exactly the currently delivered result set;
they have not been relaxed. Successful synthetic completion is not evidence of product support.

The public [Messages reference](https://platform.claude.com/docs/en/api/messages/create), checked
2026-09-09, documents a stateless supplied conversation and combination of adjacent same-role turns.
That rule does not establish equivalence for this measured regrouping across a call/result boundary.
The pinned-client behavior therefore requires its own bounded ownership/history proof. Before any
driver change, independent fake-ACP tests must require:

- the complete immutable original prefix and exact previously emitted assistant blocks, including
  tool IDs/names/arguments; truncated, changed, reordered or cross-owner history rejects;
- exact recorded previous results and one complete new delivered batch; duplicates, mutations,
  orphan IDs, extra user text and oversized projections reject before consuming any pending call;
- changed registry/schema validation and a fresh restricted backend after joined old cleanup when
  the old prompt cannot represent the new policy/instructions; old results never enter a broker twice;
- one original absolute deadline, an explicit bounded reconstruction allowance, preserved cleanup
  errors and no automatic tool replay. Any accepted transcript rewrite needs a stated narrow rule;
  plain adjacent-message normalization or a tool-name exception is insufficient.

Those are implementation prerequisites, not a newly adopted broad acceptance rule. The tradeoff of
full-history recreation remains lost hidden backend context and possible additional provider work.

Private normalized observations: plugin-held-wait.VPEupu passes the first held-peer reproduction in
5.637s; plugin-wait-canonical.fvlQOm passes the canonical comparison in 5.263s. The final installed
six-test regression plugin-wait-regression.AmrHdk passes in 36.175s under race instrumentation,
including source scopes, plugin sources, warmed and connected-panel tool turns, the forced wait and
real-gateway allowance/refusal. The wait control has one native call, complete visible response,
unchanged source files/tree and joined client/MCP cleanup. No Kiro/external model call is part of
these tests. Only tests and evidence change; no application dependency or binary behavior changes.

A separately approved public-only local Claude consultation completed in one turn and was read and
assessed in public-history-invariants-review-y8s3lgez. The prompt contains only general protocol
questions and public URLs. Ownership, complete batches, immutable history and original deadlines
are useful proposed invariants. Silently dropping known result IDs and treating ACP itself as proof
of Anthropic-client execution authority are rejected. No repository observations, user assets or
previous implementation were sent; this advice is not acceptance evidence.

The uncached opt-ins-off interop race suite passes in 20.774s (plugin-wait-unit.dztEf5).
Whole-repository go vet, explicit fixture vet, formatting and whitespace checks pass.

## D70: fresh message identities and validated registry replacement

The first real-gateway connection of D69's held-MCP experiment did not reproduce cumulative history.
Inspection found that writeObservedMessage reused one message ID for every synthetic response,
whereas the product already generates a fresh ID. A paired installed-client control varies only
this response identity policy: unique IDs produce the normal appended sequence; duplicate IDs
combine the earlier calls/results and replace an intermediate standing message. Both complete, but
only the unique-ID control represents the product's response behavior. D69's grouping inference is
therefore withdrawn. Ordinary synthetic responses now receive distinct IDs; one explicit duplicate
counterfactual remains. The existing observer still requires exact earlier-result equality there.

An uncommitted prototype for cumulative history was tested while investigating the initial shape.
The message-ID control invalidated its need, and the helper, stored digest state and driver exception
were removed. The final product continues to reject regrouped history and prior results mixed with
the pending batch. Passing a fixture caused by repeated IDs cannot justify relaxing those checks.

The real required extension is a registry change at an exact delivered tool-result boundary. The
session now compares owner, model, effort, top-level system, metadata and tool-choice policy
independently of the mutable registry fingerprint. The complete prior message prefix must still
match; truncated overlap cannot authorize a replacement. All new tool declarations go through the
existing validator first, and every supplied result must match the currently delivered batch.
Changed single standing messages retain D63's cardinality/type requirements; unchanged or repeated
standing instructions can accompany a registry-only replacement. Extra/new user content stays on
the separate existing denial-interruption policy and cannot enter this path.

Result encoding and the full replacement projection must fit their existing bounds before broker
revocation. The old relay receives closure, not the supplied result. Old process/relay/prepared-policy
cleanup joins before the replacement is prepared with the new registry and complete supplied history.
The same original absolute deadline and start time carry across all replacements. MaxRecreations is
an internal bound, default 16 and accepted range 1..64; one counter covers both instruction and
registry replacements. This extends D63's original single-replacement limit to bounded repeated
registry changes. Exhaustion rejects before mutation, while an unchanged continuation can still
resume the owned prompt. No automatic tool replay or alternate provider is introduced. Hidden backend
context is not transferred, and reconstructing full history may add provider work.

The installed-client test holds an owned MCP initialization until the first request advertises a
valid WaitForMcpServers schema. Independent fake ACP asks for that tool through the real relay.
After the client returns its result and expanded registry, the driver reconstructs once. That
replacement asks for the now-advertised plugin. Its matching allowed or hook-denied result resumes
the same replacement prompt; there is no third process. Both cases have three main requests plus
a separate synthetic title. The client supplies ordinary appended history (eight messages at the
last request), one new result at a time, and unchanged later standing instructions. Native call
counts are one for allowance and zero for the source PreToolUse refusal. Client settings/global
state/plugin trees remain unchanged and all observed client/MCP/backend groups are gone.

The fake checks the exact tool names, empty arguments, result IDs/error flags and owned plugin
success/refusal text. It also accepts both public string and text-block result representations.
The client uses a string for its wait result; insisting on an array caused the first failed
gateway attempt. The fake never persists that text. Its process ledger requires the previous group
to be gone before the next process starts and allows exactly two processes. Public ACP, relay and
gateway semantics are exercised; no actual Kiro model turn or live recovery is claimed.

Recorded private normalized observations:

- The initial independently authored registry/cumulative state test fails against the prior driver
  (3.927s package). Prototype cumulative tests pass but are not final acceptance evidence; that
  code was subsequently removed after the controlled message-ID finding.
- plugin-wait-proxy.UyKxmc fails in 25.011s before plugin execution because the fake rejects the
  string-form wait result. plugin-wait-result-shape.FdWXN0 then reaches one plugin call but fails
  completion in 24.633s: the real client appends messages and resumes the replacement prompt,
  contrary to the fake's expectation of cumulative history and a third process.
- plugin-message-identity.SefCLa passes the two identity controls in 8.966s. Unique IDs produce
  eight messages with one latest result; the repeated-ID counterfactual produces five messages
  with two latest results. Both preserve exact earlier blocks. No unrelated variable changes.
- plugin-wait-real-continuation.HuZKSP passes the real-gateway allowance/refusal pair in 13.770s
  under race instrumentation, with three main requests, two backend groups, effect counts one/zero,
  expected result representation, visible completion, source preservation and joined cleanup.

The D69 public-only local Claude advice remains available and assessed; no additional external
consultation or transmission of project observations was needed. D70 resolves the misleading
observation through an independent variable control, not advice or client-source inspection.
No dependency is added. Actual Kiro replacement, broader live lifecycle and release gates remain.

Final D70 validation: registry-core-regression.NPTavW passes the uncached whole-repository race
suite with installed/model opt-ins off (session 25.882s, interop 22.255s, launcher 22.678s). The
independent repeated-policy and strict-result matrix also passes in 10.632s. Whole-repository and
fixture go vet, formatting, whitespace, build and executable help pass. The unused cumulative
branch is removed from the wait fake before the final installed regression.
registry-claude-regression.zpMDys passes all 27 existing TestClaude top-level controls in 223.639s
under race instrumentation, including the fresh/duplicate identity pair, held wait allowance/refusal,
default tools, native permission UI, bare refusal/deadline recovery, hooks, assets and status.
Every model response in this regression comes from an owned synthetic server or fake ACP.

## D71: actual default-client denial reconstruction

The next live check retains the pinned client's default tools, system prompt, thinking and
experimental declarations. A separate test-only mode extends the existing single-operation guard
to accept bounded default declarations containing Read/Write/Bash, with unique names. It still
exposes only one Read with the exact owned path/argument object and accepts only its matching
hook refusal. Any other operation, extra argument, successful result or third request rejects.
The original single-tool guard retains its narrower declaration policy. Independent tests first
fail against the missing mode, then establish declaration, argument, result and request bounds.

The shared prepared-process harness allows exactly two launches and one recreation under the
original 45-second deadline. It proves old relay/group/artifact removal before the next preparation,
checks the final live group before shutdown and requires both groups gone afterward. The client's
ordinary changed standing message triggers the existing full-history path; no product exception is
added. The fake's launch-manifest variant checks the full projection, exact historical Read and
owned denial before completing its replacement prompt. Raw request/output data is not persisted.

default-denial-rehearsal.gHGa9W passes the prepared default-client control plus earlier default and
single-tool controls in 14.364s under race. The new case takes 3.26s, with 25 tools, unchanged policy/
history, one changed standing message, one exact refusal, two requests, two processes, nonempty
completion, source/canary preservation and joined cleanup. Pre-live guard checks pass in 1.285s.

The separately opted-in actual Kiro 2.21.2/v2, Claude 2.1.263 and auto-model attempt passes in
live-default-denial.KGeiR1 (24.81s test, 26.304s race package). It observes the same counts and
standing-message change, keeps thinking/context declarations, and completes after the refusal in
a fresh session. All source/canary and observed process/group/relay/artifact checks pass. Only fixed
flags/counts are retained; its 1,888-byte client completion is inspected in bounded memory. The
synthetic assistant-text diagnostic is false for live model text and is not a history invariant;
the driver's canonical full-prefix validation establishes the actual immutable history.

Automatic approval review timed out before the first submission could create a process. The tool
explicitly permitted one resubmission; it ran the sole live attempt. There was no automatic retry of
a failed model attempt, and no safety finding is inferred from the review timeout. The existing
authorization covers this bounded experiment; LIVE_KIRO_TEST_PLAN.md records its inputs and limits.

Pre-live opt-ins-off race tests pass ACP 5.955s and interop 25.058s in default-denial-unit.q0Xkpf.
Whole-repository/fixture vet, formatting and whitespace pass. No application code/dependency changes;
the existing D70 development binary is current. This verifies one actual changed-standing-message
recovery. Live registry changes, plugin turns, other alpha lifecycle and release checks remain.

Post-live review adds a negative guard control for a result mixed with a new question. It fails in
0.842s because the shared adapter also serves the separate new-question recovery experiment. The
default-client mode now requires one result-only latest user message before dispatch, preserving
the intended single logical turn. The recorded live request already has exactly that shape. Final
default-denial-final.ZqVfcB passes the updated guard suite and installed-client/fake-ACP rehearsal in
5.399s, followed by whole-repository/fixture vet and formatting/whitespace checks. No extra model
attempt runs; the separate bare-denial/new-question experiment retains its original behavior.

## D72: bounded live plugin registry and visible completion evidence

D70's held-MCP readiness experiment now has a separately opted-in actual-Kiro harness. It retains
the pinned client's default declarations, the real gateway/validator/relay/session driver and the
shared restricted Kiro launch configuration. A test-only guard exposes only WaitForMcpServers and
the owned effect-free plugin, in that order, with exact empty arguments and unique IDs. It admits
three main requests, one matching result per continuation and no additional user question. Local
synthetic title handling brings the maximum Messages count to four. Unexpected operations, result
status/content, early end, missing completion and excess requests fail before further dispatch.

The held MCP peer releases only after the initial advertised wait schema accepts its empty input.
The replacement registry must advertise the plugin. One recreation permits two sequential prepared
ACP processes, with old relay/group/policy cleanup joined before the replacement. The original
45-second turn deadline, 20-second setup/first-event limits and 60-second terminal lifetime remain
fixed; the whole case has a three-minute limit. The plugin result must complete the replacement
prompt without a third process. Sources stay unchanged and all observed resources must be gone.
Allowance requires one native plugin call; hook refusal requires zero and a matching error result.

Initial live observations exposed weaknesses in the test's completion evidence, not a basis for
relaxing product history or execution rules. live-plugin-registry.BJbgWo reaches both results and
a completed backend response but fails the screen assertion (70.507s race package). Its observer
cannot distinguish final wording from old-prompt disappearance. The first derived-marker follow-up,
live-plugin-marker.MNXcAE, fails before exposing a tool (69.463s); its diagnostics do not establish
the failure site. Instrumented live-plugin-categories.Pr0kj8 identifies an early complete marker
in the first model response, before any exposed tool (129 events/985 text bytes, 68.768s package).
None of these attempts passes overall, and no refusal case follows a failed allowance.

Failure diagnostics now retain fixed validation/error categories and bounded counts only. They
separate expected two-process completion from actual cleanup: an absent second launch is not a
surviving process. A still-running owned relay can be observed before shutdown even without a tool
callback. A recorded surviving relay PID receives bounded last-resort cleanup while preserving the
failure result. No unrelated process group is signaled and arbitrary error text is not logged.

The final observation recipe splits a fresh 26-byte ASCII marker between the prompt and the second
tool's result. The owned peer's exact success text or owned hook-refusal reason supplies the second
half; the initial request/wait result must not expose it. The complete marker must occur in final
model text, never in a tool result or earlier model text. Independently, the current reconstructed
terminal screen must show it after protocol completion. The earlier prompt need not remain visible.
The observer does not strip terminal escapes to resurrect erased text, use prompt echo as an answer
or infer a particular answer from an arbitrary text delta. Matching across text-event boundaries is
covered. This changes only owned synthetic content and observation, with no new execution authority.

The authorized public-only local Claude consultation is saved, fully read and assessed in
public-terminal-evidence-review-_c51vjg0 (one turn, 6,182 stdout bytes). Separating generated and
visible output evidence is useful; stripped terminal text, assumed marker provenance and claims
that any assistant delta proves final wording are rejected. No repository content, client output,
user asset or previous implementation is sent. Advice is not acceptance evidence.

Independent controls precede the guard, category and result-challenge changes and initially fail
against missing functionality. The final fixed-result and result-challenge actual-client/fake-ACP
pairs pass in plugin-result-challenge.hHFPYQ (25.368s race package), with exact three-request/two-tool
sequences, generated/displayed markers, native effects one/zero, source preservation and cleanup.
Earlier eight-client regression plugin-sequence-clients.ANfZdp passes in 58.469s; the shared final
screen condition's three UI/hook/Git controls pass in plugin-marker-ui-regression.CFVGcB (17.655s).
Both use independent synthetic responses only. LIVE_KIRO_TEST_PLAN.md records all recipes, bounds
and the fuller normalized observation history. No application code or dependency changes in D72;
the development executable remains the current D70 build.

The final actual allowance, live-plugin-result-challenge.8UPd0k, establishes three main requests,
two exact tool/result pairs, the validated expanded registry, one native plugin call and two joined
prepared processes. Its 33-byte final text lacks the required complete marker; end-condition
validation rejects and the overall experiment fails (68.463s race package). The raw wording is
not retained, and no formatting diagnosis is inferred from its length. Client/MCP cleanup, unchanged
sources, both observed relay/groups/artifacts, successful close and an empty pool are established.
The refusal case does not run. The full actual plugin acceptance gate remains open; partial protocol
and cleanup evidence is not labeled a successful end-to-end test. No further model attempt runs in
this decision. Development run admission is unchanged.

Final opt-ins-off race tests pass ACP 5.439s and interop 20.887s in
plugin-result-final-unit.R8Vjmv. Whole-repository/fixture vet, formatting and whitespace pass.
Broader plugin assets, model-selected skills, live lifecycle and release gates remain outstanding.

## D73: client text after successful tool results

An independent synthetic model asks the pinned Claude 2.1.263 client to invoke only the freshly
authored plugin skill, after validating the exact input against its advertised Skill schema. Native
HOME, prepared private profile and prepared hooks-disabled controls all complete in
plugin-model-skill.HRgSI8 (4.84s test, 6.105s race package), without Kiro or external inference.
Each has two main requests, one matching successful result, expanded skill text and a final answer.
Enabled hooks run once at startup/stop; disabling them preserves the skill but suppresses both.
Prepared sources and observed hook/client cleanup remain intact.

The second request has the shape `user[text,text] system[text] assistant[tool_use]
user[tool_result,text] system[text]`. Its complete prior prefix, owner, model, effort, top-level
system, metadata and registry remain equal; the single trailing standing message changes. The
extra user text is the client-expanded skill body, outside the successful tool result. The previous
driver rejects it: ordinary continuation/recreation requires result-only content, while the separate
new-question recovery accepts only denials. Synthetic endpoint success alone is not driver acceptance.

The public [client tool-result contract](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls),
checked 2026-09-09, permits user text after the tool-result blocks for client-only calls. It does
not permit placing text before the results or interposing a message before the result message.
Its pending server-tool exception does not establish any new support here.

The adopted extension is a full-history recreation for a complete delivered client-result batch
containing at least one success, followed only by nonempty client text in that same user message.
It must preserve every previous block, owner and policy, validate the registry/results/projection
before revocation, join old cleanup, retain the original absolute deadline and share the existing
recreation bound. It must not inject the new text into a tool result or replay effects. The rule is
based on validated message structure, not a Skill-name exception. Expired successful work cannot
be revived by appending text. The existing all-denial/new-question recovery remains separate.

The initial observation/helper tests pass in 1.847s; they first fail to build before the new harness
entry point exists. Independent state/process tests then fail against the old driver in
result-text-red.NTjIRc (4.162s), before implementing the new path. They require exact history and
ownership, result order/identity, nonempty text-only additions, projection bounds, joined old cleanup,
no replay, one original deadline, failed-replacement cleanup and the existing recreation budget.

The first candidate run fails in result-text-candidate.HG16AW (17.327s). A focused fixed-index
diagnostic (4.972s) identifies the test's ninth mutation: it incorrectly treats the 4 MiB relay
result limit as the ACP prompt limit, whose default is 8 MiB. The test now selects a 64 KiB ACP
frame limit and exceeds that exact bound; no application limit is changed to satisfy the test.
result-text-boundary.BiDhzi passes the new and existing instruction/registry/denial recovery matrix
in 14.390s. The former active-success rejection control moves to this new continuation policy;
the expired-success negative control remains in the all-denial recovery test.

plugin-skill-gateway.AoBM3j passes the installed-client source/skill controls and real-gateway/fake-ACP
skill round trip in 17.743s under race. The gateway case takes 4.45s: two main requests, one exact
Skill call, matching success, the skill body in a separate user text block, one changed standing
instruction, two joined backend groups and final client output. Its complete old prefix, owner,
model, effort, top-level system, metadata and registry remain equal. The independent replacement
fake verifies the full projected history, exact Skill input/result ID, separate skill body and
standing update before responding. It accepts at most two processes and requires the old group gone
before replacement; the original relay may receive only retirement, never the successful result.

Three synthetic native/prepared/hooks-disabled controls independently verify the same client shape,
including explicit skill-body presence in the added user text. Eleven existing plugin skill/hook/
source controls also pass. All preserve the expected enabled/disabled hooks and prepared source
bytes/tree/modes, with observed client/hook/ACP cleanup. No actual Kiro or external model inference
runs in D73. No dependency is added; actual Kiro skills and broader plugin/lifecycle/release checks
remain open.

The first complete opt-ins-off regression, skill-result-core.hTaccS, fails only an existing registry
deadline assertion. The ACP request reports `acp.ErrTimeout` while the test requires only
`context.DeadlineExceeded`; either owner can observe the same original deadline first. The test now
accepts either sentinel while retaining the original timing, cleanup, ownership and replay checks.
No application timeout behavior changes. The complete session suite then passes in
skill-result-session-final.AKWBsX (28.513s).

The first all-client regression, skill-result-claude.6suCOR, passes 30 of 31 top-level controls
but fails the omit-betas completion aggregate (253.515s). It records exit zero, one accepted request
and the expected declarations; its aggregate does not distinguish a runner error from missing
answer evidence. The exact cause remains unclassified. The observer now adds only fixed runner-error
categories and answer/JSON-result booleans; the acceptance condition is unchanged and raw client
output is not retained. Five finite repetitions of the four documented-option controls all pass in
capability-completion-diagnostic.wgmpkq (20 cases, 11.116s), with no runner errors and matching answers.
Those passes do not erase or explain the original failure.

The final complete opt-ins-off race regression passes in skill-final-core.QZ6DoB (session 27.252s,
interop 22.639s, launcher 23.546s). Whole-repository/fixture vet, formatting, whitespace, build and
executable help pass. The development binary is rebuilt with D73; run admission is unchanged.
All 31 installed-Claude top-level controls pass together in skill-final-claude.VE8lEz (252.391s
under race), including the instrumented option controls, native permissions, bare refusal/deadline
recovery, default-tool denial, plugin wait allowance/refusal, preserved assets/status and new Skill
cases. All use local synthetic responses or fake ACP, with Kiro credit opt-in disabled.

## D74: launcher cancellation after a delivered tool handoff

The next alpha observation isolates runtime ownership after the tool HTTP response has completed.
The pinned public client invokes a newly authored effect-free Read hook that records its own PID
and group, waits at most thirty seconds and exits with refusal status on either signal or expiry.
The hook never reads stdin, tool input or files and has no descendants. The client has only Read,
an empty strict MCP configuration, an owned source HOME/project/settings and no persistent session.
The existing exact-Read/canary/output guard is wrapped by a one-main-request budget: no result,
retry, title or new question can dispatch another ACP request. The test observes successful HTTP
handoff separately from tool exposure before cancellation.

The application RunClient owner, real gateway, validator, relay, process pool and session driver
remain in the path. A test-only executable wrapper appends finite print-mode arguments and execs
the unmodified public client. It does not invoke the public foreground run command or exercise
terminal Ctrl+C handling. After observing the delivered handoff, live held hook, client and relay
groups, and WaitingTools state, the parent calls the same cancellation function eight times.
Passing requires a context-cancellation outcome, one backend Close, closed driver/empty pool,
joined process/hook/relay/group cleanup, removed profile/policy artifacts, unchanged source settings
and canary, and zero tool results or final completions. The whole harness is three minutes, client
one minute, original turn 45 seconds and setup/first event twenty seconds; the post-cancel observer
allows eight seconds for the existing bounded owners. No hidden provider work or billed usage bound
is inferred from one ACP request.

The budget/control test first fails to build in cancellation-guard-red.1DyOVN, then passes in
1.950s. The first installed-client/fake-ACP rehearsal, cancellation-rehearsal.VfTLGf, fails before
any ACP preparation or tool (4.103s package). Its test-only client omission flags were put into the
product's allowlisted platform environment and correctly omitted. The flags now belong to the
test wrapper; the application environment policy is unchanged. The corrected rehearsal passes in
cancellation-rehearsal-scoped.jrmixY (3.97s test, 5.668s race package), with one held tool, no results,
joined cancellation in 177ms, source preservation and all observed cleanup. Unobserved process
flags in the earlier failed run do not establish surviving processes.

This is not streamed HTTP disconnect, foreground Ctrl+C, process-loss/recovery, authentication
expiry or restart/resume evidence. Those remain separate alpha checks. No application or dependency
change is proposed. LIVE_KIRO_TEST_PLAN.md specifies the separately opted-in actual-Kiro attempt.

The final delivered-handoff guard and fake-ACP rehearsal pass in
cancellation-delivery-rehearsal.7CXxdE (4.45s test, 6.038s race package): exactly one HTTP Finish,
one still-pending client tool/hook, no results/completions and one launcher-owned backend Close.
Joined cancellation takes 169ms, with every observed group/process/artifact gone and sources intact.
Interop/held-hook vet and whitespace checks pass before actual model work.

The sole actual Kiro 2.21.2/v2 / Claude 2.1.263 attempt passes in
live-launcher-cancellation.tlWaG6 (17.13s test, 18.514s race package). One main request exposes one
exact Read and completes its HTTP handoff. The held client hook and WaitingTools state are observed
before eight calls to launcher cancellation. RunClient returns context cancellation after a 1,061ms
join, with one backend Close, one prepared/cleaned ACP launch, closed driver and empty pool. Client,
held hook, relay and observed groups are gone; policy/profile artifacts are removed; source settings
and canary stay unchanged and no canary is observed in model events. There is no result, final
completion, replacement, subsequent request or model retry. The measured result establishes this
specific suspended-runtime cancellation path; it does not close all cancellation or alpha gates.

The final opt-ins-off complete interop race suite passes in cancellation-final-unit.vM02NG
(22.082s). The applicable launcher runtime race regression passes in
cancellation-runtime-regression.Wha8bs (5.588s). Whole-repository/fixture vet, formatting and
whitespace pass. Only independent fixtures, tests and evidence documents change; the D73
development executable and run admission remain current.

## D75: process loss before client tool delivery and a fresh request

The next alpha observation kills only an independently observed owned ACP process group after the
driver produces one exact Read request, before that request reaches the client. The test guard
validates its name/arguments, records the live relay group, sends one SIGKILL and waits at most five
seconds for joined process/relay/policy cleanup, Unstarted state and an empty pool. It then calls the
real turn again and returns its actual error. No tool event or invented failure is sent to the client.
The client must exit with a JSON error result; an owned denying-hook marker must remain absent.

Only after that failure and cleanup pass may a second independently supplied text request start.
Two finite print invocations use the same prepared profile, explicit fresh UUID (supported by the
pinned public client's help), model, tools and fixed user-supplied system instructions. Persistence
is disabled. The second request contains only its new user text and native standing context, no
assistant/tool/result history; it is a fresh request, not continuation/resume of the failed turn.
The replacement must be a different prepared process with old cleanup joined before its creation.
Only text and a delivered end_turn may complete the second request. Further requests, results,
effects or replacements fail. The production driver receives the original client requests unchanged.

Independent guard controls precede the implementation and initially fail to build in
process-loss-guard-red.jSqpNR. They then pass in 4.136s and 1.516s. They cover actual versus absent
failure, caller cancellation versus internal retirement, recovery before cleanup, owner/model/tools/
fixed-policy changes, unexpected questions/results, further tool effects and a third request.

The first installed-client/fake-ACP rehearsal, process-loss-rehearsal.eyLND8 (5.198s package), proves
the initial failure and cleanup but rejects the second request's system equality. Its first error
is a transport/closed class. Diagnostic process-loss-shape.ibkGSk instead stops at the first error
(3.367s), and process-loss-error-shape.AfAExd identifies internal context.Canceled with no signal/
cleanup-wait failure or deadline (3.192s). The application can cancel the owned turn during retirement
before transport EOF wins. A new negative control first fails in 0.716s, then separates an active
caller context from actual caller cancellation. The accepted observed set is transport failure,
closed process, or internal context cancellation only after the confirmed signal/cleanup and with
the caller context still live. EOF, success and deadline outcomes do not establish this case.
No application error handling changes.

The authorized local Claude public-only consultation is saved, fully read and assessed in
public-process-failure-review-y58ob59a (one turn, 6,494 stdout bytes). It supports separating causal,
client-error, cleanup and recovery evidence. Do not infer a signal exit status or raw transport EOF
that the public owner does not expose, signal unrelated processes, silently normalize metadata,
or infer history continuity merely from a reused UUID. No source/client payload is supplied and the
advice is not acceptance evidence.

process-loss-causal-shape.lUf0yZ passes the guards and initial loss but still rejects system equality
(4.088s). process-loss-system-shape.I1YD2Q (3.726s) identifies three top-level system blocks: the
fixed user policy and other block stay exact, while the first x-anthropic-billing-header block
changes a short fragment. Identity/model/effort/tools/metadata and the trailing standing message
remain equal. The changed request prefix is its intended new question, with no prior history.

The test observation now allows only that first single-line billing block's equal-length variation
within one hexadecimal span of at most 32 bytes, under a 256-byte block bound. All non-text block
fields, other system blocks and the literal owned policy remain exact. Unknown headers, non-hex or
multiline changes and changed block metadata reject. Independent controls precede this predicate
and fail to build before it exists. This is not a production normalization or pending-continuation
exception: the original billing block is passed to the fresh session. Full wire-system equality
remains false and is logged as such; no claim of identical backend context is made.

process-loss-policy-rehearsal.wG3Mle passes the independent controls and installed-client/fake-ACP
round trip in 5.080s (3.36s test). One intercepted Read becomes a real client-visible error, no client
hook executes, and a new request under the same explicit owner reaches a fresh process and final
text. Two requests/processes, one joined replacement, one final completion, original sources and
all observed client/relay/ACP/artifact cleanup pass. The independent replacement fake requires one
full prompt with an empty prior history, the new exact question, native standing context and no old
target path. Earlier unobserved second-process flags do not establish cleanup leaks.

Only test fixtures, observers and evidence change. The separately opted-in actual-Kiro recipe is in
LIVE_KIRO_TEST_PLAN.md. This is not interactive same-process continuation, delivered pending-result
replay, simultaneous sibling failure, foreground Ctrl+C or complete alpha/release evidence.

Final pre-live guard controls include caller deadlines and nil error as negatives and pass in
1.843s. Interop/independent-ACP vet and whitespace pass before the actual attempt. The added labels
explicitly distinguish preserved owned policy from unequal native billing-header text.

The actual-Kiro invocation live-process-loss.8PHpmH stops in version/account preflight, before
any ACP process or model request (3.60s test, 3.946s race package). This is not an executed
process-loss experiment. Both installed version commands still report 2.21.2. The compiled
doctor with fresh owned settings/state also stops at login_check (2,396ms). The separate read-only
main/helper diagnostic, process-loss-account-diagnostic.9j2vIn (4.816s race package), finds both
whoami commands exit 1 with an object containing account:null and no verified identity. Its test
passes as a shape observation, not as a successful login check. No credentials, login state or
client configuration are modified. Login renewal has been requested from the user; no further
actual model attempt runs while identity is absent.

The complete applicable opt-ins-off race regression passes in process-loss-core-regression.Cjvd9l:
ACP 5.882s, session 27.869s and interop 21.593s. Whole-repository/fixture vet, formatting and whitespace
pass. No application or dependency change; the D73 executable remains current. Development policy
admission stays enabled for the measured installation, but current login preflight prevents launch.
Live process-loss/recovery acceptance remains open until the prepared sequence actually runs and
passes; local fake-ACP recovery and read-only diagnostics do not substitute for that evidence.

## D76: retain personal customization scope in the temporary client profile

Independent Claude 2.1.263 controls reproduce a missing personal layer. Natural owned-HOME startup
advertises personal/project skills and agent definitions and expands each scope's user-invoked
skill/legacy command. The old prepared profile advertises only project assets. This is distinct
from plugin registration and from model-selected Skill continuation. No actual user assets, Kiro
model response or previous implementation input participates in these controls.

Preparation now snapshots only standard HOME/.claude/skills, commands and agents into those same
relative locations under the private client configuration root. No frontmatter, script or prompt
is interpreted by the proxy. Full bytes and hierarchy remain intact; files are 0600, or 0700 when
the source has the owner execution bit, and directories are 0700. Group/special permission bits are
not propagated. Source files are never linked into the private tree. Native client scope resolution,
permissions and execution remain with Claude; no additional-directory or command-line agent scope
is introduced. Unrelated state, credentials and other customization roots are not migrated.

Each aggregate snapshot is bounded to 1,024 entries including selected root directories, depth
sixteen with each selected root at depth one, 2 MiB per regular file and 32 MiB total file content.
Read directory names in bounded batches and descend through checked os.Root handles. Reject unsafe
owner/mode/type checks, symbolic/hard links, changed opened-file identity/size/mtime, read failures
and exceeded limits. Directory handle identity is checked against its original entry. No partial
snapshot is activated: all source validation precedes creation of runtime artifacts. A private-write
failure uses the existing profile cleanup owner. These checks do not promise a transactional snapshot
of a concurrently edited source tree. Existing profiles retain their initial bytes; a fresh launch
reads subsequent source edits. Absolute references inside an asset remain unchanged and may still
name the original tree when the client executes them; this is not filesystem isolation of client tools.

The public [skills documentation](https://code.claude.com/docs/en/skills) describes personal/project
locations, legacy commands and personal skill precedence. The public
[subagent documentation](https://code.claude.com/docs/en/sub-agents) describes the distinct agent
scope order. The [settings documentation](https://code.claude.com/docs/en/settings) documents the
private configuration-root override. Reviewed 2026-09-09; the snapshot is our own mechanism, while
the need for it and the measured fidelity are pinned-client observations. Additional-directory
loading is not assumed equivalent to keeping the original personal scope.

The test's complete matrix has thirteen finite text-only invocations: six natural, one private
profile with all three snapshots deliberately removed, and six prepared. All configured assets
are independently authored. Exact body markers appear only on explicit slash invocation. With
equal names, both natural and prepared runs select the personal skill and project agent; the
stripped control loses personal advertisements while retaining project ones. Responses come only
from a bounded loopback fixture and never request a tool or dispatch an agent. Each response must
complete once, client groups must be gone and private profiles removed. Prepared/stripped runs
preserve owned source trees, settings and global configuration. Natural global mutable state is
not used as source-preservation evidence.

Recorded failures and progression:

- Initial test compilation exposes incorrect assumptions about the existing runner result API;
  the harness is corrected to inspect its actual PID and owned process group.
- personal-assets-red.pfShzA fails before a model request (1.689s package). A variadic MCP option
  consumed the final prompt. Moving the following option boundary fixes only the test invocation.
- personal-assets-args.AUOGX7 passes all five natural controls, then fails on missing personal
  advertisements in the old prepared profile (3.513s package). This establishes the product defect.
- Independent snapshot/unsafe-source tests first fail on the absent APIs; after implementation
  the initial race suite passes in 5.544s. Controls cover links, FIFO, modes, individual/aggregate
  bytes, entry/depth limits, private mutation, exact non-JSON/CRLF/Unicode bytes and source refresh.
- personal-assets-snapshot.GMkyjb passes ten natural/prepared controls (6.948s package).
- personal-assets-precedence.4SkKhX passes the final thirteen controls, including the active
  missing-assets counterfactual and two distinct scope orders (6.79s test, 8.481s package).

Public-only local Claude advice is saved, read and assessed in
public-personal-assets-review-s5qj_xo8. Bounded traversal, source protection and collision controls
are useful; universal claims about absent documented seeds are not adopted as facts. Advice to
skip unsafe subtrees is rejected because silently dropping a policy-bearing asset changes behavior.
No raw client request/response, credentials or user source is retained in observation logs. The
consultation answer is advisory and does not count as acceptance evidence.

No dependency is added. Actual Kiro personal skills/agents, relative helper execution, custom roots,
linked asset compatibility and dynamic source synchronization remain unverified. Development
execution-policy admission stays enabled. D75's unresolved login state still prevents live Kiro
work; these local controls neither restore authentication nor close the alpha/release gates.

The final applicable opt-ins-off race regression personal-assets-core.ImGOBO passes: launcher
24.245s, interop 24.644s and command 1.597s. The six installed-client regression controls in
personal-assets-client-regression.RJdYdB pass together in 47.157s, covering native MCP scopes,
plugin skills/hooks, existing status precedence, ordinary default-tool requests, permission hooks
and disabled hooks. Whole-repository go vet, formatting and whitespace checks pass. No whole-suite
installed-client or actual-Kiro success is inferred from this targeted regression.
The development executable is rebuilt with D76 and its public help command passes. Development run
admission remains enabled for the pinned combination; login is still required before live work.

## D77: preserve native personal rules; reject incomplete root-memory adapters

Independent Claude 2.1.263 controls extend D76 to personal instruction sources. The old private
profile drops both user CLAUDE.md and rules, while natural owned-HOME startup loads them. Preserve
only the verified rules path in this change. Personal CLAUDE.md remains a documented compatibility
gap; no tested root-memory adapter is enabled. This is progress on environment preservation, not
completion of that gate or a new condition for development execution-policy admission.

Preparation validates HOME/.claude/rules using the same checked bounded walker as the existing
skill/command/agent snapshots. All four trees share 1,024 entries, depth sixteen, 2 MiB per file
and 32 MiB total. Rule bytes are validated but not retained as a relocated snapshot. The private
client/rules entry instead references the original owned directory. Native Claude resolves relative
imports and exclusions, loads conditional rules and retains user/project order. No Markdown,
frontmatter, glob or command parser is introduced. Unsafe source links, modes, nonregular files
and exceeded bounds reject before runtime artifacts exist. Repeated cleanup removes the private
reference without traversing it. The launcher does not write original rules or imported files.

This is not an immutable/read-only filesystem view: source edits after validation may affect
what the client reads. Imported files outside the selected rules tree remain native client reads,
not part of the launcher's bounded tree snapshot. Client tools retain their own permission/hook
authority. Dynamic mutation, custom roots and arbitrary linked-source compatibility remain open.

Public [memory documentation](https://code.claude.com/docs/en/memory) documents relative imports,
four import hops, personal rules, symbolic links, path conditions and claudeMdExcludes. The
[configuration directory documentation](https://code.claude.com/docs/en/claude-directory) describes
CLAUDE_CONFIG_DIR relocation. Reviewed 2026-09-09. Public help and the documented environment
surface provide no verified separate personal-memory root in the mechanisms checked; this does
not establish the universal absence of any future/native solution. The source-reference mechanism
and its limitations below are our own pinned-client black-box observations.

The independent responder uses an authenticated bounded loopback server, owned HOME/project,
fixed synthetic markers, finite print clients and strict empty MCP configuration. It never calls
Kiro, dispatches an agent or requests a shell effect. Exactly two conditional controls ask the
client to Read one owned file under an explicit permission; each must show the condition absent
before Read and present only with the matching successful result in the second request. Every
invocation requires one final fixed answer, an absent owned client process group and removed
private profile. Source entries/bytes/modes and import fingerprints outside the selected .claude
tree are checked.
Only fixed fields/markers, counts and booleans are logged, never raw client requests/responses.

Root-memory experiments expose three independent defects:

- A direct private CLAUDE.md symlink retains ordinary content, but an exclusion naming the original
  absolute CLAUDE.md path does not suppress its body/imports (personal-instructions-exclusions.2SmJdL).
- A wrapper importing the original path honors that exclusion and ordinary relative imports.
  Native tilde syntax also handles six tested HOME-character classes and conditional rules
  (personal-instructions-native-import.wIYOkM, 22.381s). The later depth control disproves full
  fidelity: personal imports stop at hop three while native/project controls reach four
  (personal-instructions-depth.nDZzxs, failed 2.065s).
- A rules entry pointing at the original CLAUDE.md retains four hops and exclusions, but changes
  root semantics: paths frontmatter suppresses its body although natural global-root loading
  includes it (personal-instructions-frontmatter.POVAf7, failed 2.296s). Combining a root wrapper
  and rules entry still loses the fourth hop (personal-instructions-combined.bemAgf, failed
  3.697s). Two rules entries retain depth but still omit the root body
  (personal-instructions-two-rules.HWHXSL, failed 4.024s).

Earlier quoted/aliased import candidates either omitted content or bypassed exclusions. An initial
path-character control varied runtime/project paths as well as HOME and failed even in natural
execution; it is not proxy-defect evidence. The corrected matrix varies only HOME. No broader
runtime/project path-character support is claimed. The incomplete production root wrapper is
withdrawn after a unit control fails on its accidental activation (3.226s), rather than reducing
the required native import depth or accepting missing body text. Three retained counterfactual
tests explicitly assert these rejected behaviors; passing them never means root-memory acceptance.

Two bounded public-only local Claude consultations are saved, fully read and assessed in
public-memory-scope-review-ps8dh1zx and public-memory-fidelity-review-f8z1eben. Their emphasis on
separate source identity, depth, conditionality and source immutability is useful. Their broad
absence claims are not adopted as facts. No additional dependency, private-client inspection,
previous implementation input or custom instruction parser is used.

Final scoped installed-client evidence personal-rules-scoped.43hozu passes in 23.858s: seven source
controls (3.97s), twenty-four HOME-character controls (11.83s), four depth/exclusion controls
(1.94s) and eight rejected-root-adapter counterfactual controls (4.16s). The thirty-five product
rules invocations require unchanged sources/imports and observed process/artifact cleanup. The
eight memory counterfactual invocations reproduce defects only. Expanded personal unit race tests
pass in 6.051s, including the full unsafe/oversized source matrix for both snapshots and rules.

Review strengthens the rule-body markers so an imported marker cannot accidentally stand in for
a missing general-rule body. Both body and import now participate in order/duplication checks.
The final scoped matrix personal-rules-final-markers.pAsngv passes in 24.203s (3.96s source,
12.41s HOME paths, 1.88s depth, 4.11s counterfactuals), without changing the required native depth.
The applicable opt-ins-off race regression personal-rules-core.XhZBkj passes launcher 26.458s,
interop 24.853s and command 1.833s. Seven installed-client regressions in
personal-rules-client-regression.8XCT7t pass in 54.956s, covering D76 personal assets, MCP scopes,
plugin skills/hooks, existing status precedence, permission/disabled-hook behavior and default
tool traffic through the gateway/relay. Whole-repository and independent-ACP fixture vet,
formatting and whitespace pass. The development executable is rebuilt with D77 and its public
help command passes. This is not a release or actual-Kiro verification; D75's login blocker and
remaining live alpha/release work are unchanged.
The final personal unit race suite passes in 5.372s after consolidating duplicate rules negatives
into the shared snapshot/rules matrix; all twelve unsafe/bound cases still run for each tree.

## D78: identify development artifact dependencies and retain scoped notice evidence

Phase 7 requires an artifact-based license report as well as future clean-host installation. Add
an offline-verifiable snapshot for the clean D77 command binary and retain already reviewed notice
texts. The report covers the four embedded Go module records, 265 selected command packages,
fifteen standard-library vendor packages, selected native filenames and nineteen embedded resources.
It does not claim full linked-symbol, build/test/toolchain, advisory or distribution clearance.
The exact binary, cache inputs, source manifest/checksum files and retained notices have recorded
SHA-256 and byte counts. No runtime code, dependency version, client configuration or execution
policy changes. The local executable is rebuilt from clean D77 and its public help succeeds.

All nineteen standard metaschemas are compared with current official JSON Schema resources and
fixed public specification commits. Three match current published JSON, fifteen match a selected
tag, and sixteen match at least one source structurally; none is byte-identical to the current
publication. Exact JSON-pointer differences remain for every unequal pair. The three resources
that match neither source remain unresolved variants. Differences from a current URL alone must
not be labeled validator-authored modifications: most modern vocabulary files match an earlier
official version. Sorted-key comparison is not schema-behavior equivalence or ownership evidence.
All 57 JSON inputs independently reject duplicate keys and nonfinite numbers.

Retain exact Go BSD/PATENTS, validator Apache and currently referenced Unicode-v3 texts, with the
Unicode-17 release README attribution. The existing CLDR-32 and test-only LLVM texts remain separate.
Retain the current JSON Schema dual-license text only as a reference; do not invent historical
copyright years, choose this project's license or treat an unsuccessful root LICENSE fetch as
proof of absent licensing. DEPENDENCY_REVIEW.md and the JSON inventories carry exact sources,
hashes, observations and outstanding scope. Only public protocol resources and dependency licensing/
metadata are inspected. No previous implementation, upstream test corpus or dependency code is
adopted as implementation input.

The new Python verifier is a read-only development tool using the already reviewed standard library.
It checks only frozen snapshot bytes, optionally including the identified binary, and never claims
release clearance or performs a network request. Forty file-record checks pass with the artifact;
nine authored negative controls reject missing input, mutation, bounds/type/path violations and a false clearance
claim. Cache `go mod verify` passes through the explicitly installed toolchain; an earlier automatic
selector invocation stops at its checksum-database requirement, before module verification.

The bounded local Claude consultation public-metaschema-review-review-i89nisso is saved/read/assessed.
Its assumptions about unchanged data and historical provenance are not adopted. No Kiro model call,
account mutation, installer execution or release publication occurs. This closes missing retained
top-level notice/artifact-correlation work and establishes precise resource differences, while full
historical licensing, component attribution, advisory scanning, installer/clean-host testing and
owner rights remain open. D75's absent login and D77's personal-root-memory gap are unchanged.

## D79: publish per-user executable generations and protect active helper paths

Phase 7's local installation contract now has explicit `install`, `install --force` and `uninstall`
commands. They resolve their own executable and a per-user destination without launcher settings,
Kiro/client probes, credentials, model work, downloads, shell-profile edits or services. The default
destination is HOME/.local/bin; `--bin-dir` accepts an absolute clean owned directory. A custom
destination must also be specified on later operations. The source must be an owned regular
executable, not a symlink/hardlink/special file, with no group/other write permission. The CLI resolves
its own symlink before handing that source to the installer; there is no arbitrary `--source` option.

The public `dax-kiro-proxy` link points to `.dax-kiro-proxy-install/current/dax-kiro-proxy`.
The private manager has a fixed format marker, a stable zero-byte lock and `current` pointing to
one `generation-` directory with a random 128-bit lowercase hexadecimal identifier. Each generation
contains the executable, a strict manifest and retained notice/reference files. Owned directories
use 0700, executable files 0700 and metadata/notices 0600. Existing parent directories retain their
modes. User/client settings, credentials, source executable, unrelated bin entries and product state
are outside the installation contents and remain intact after uninstall. Empty destination parents
are retained. Repeated uninstall of an absent installation succeeds.

Bounds are 128 MiB per source executable, 16 KiB per manifest, sixteen payload files per generation,
64 KiB per notice, 1 MiB total notices and four retained generation/stage directories. The command's
ordinary argument bounds also apply. Paths and exact file types/modes/owner/link counts are checked
through os.Root and no-follow bounded file opens; manifests reject duplicate/unknown members,
unexpected paths, invalid sizes/hashes and mismatching content. Unknown or changed entries refuse
both force replacement and uninstall, before their planned destructive work. These checks are not
a signed provenance guarantee or a transaction against arbitrary concurrent edits by the same user.
Filesystem call latency still depends on the host filesystem; byte/count bounds are not a hard
wall-clock bound for an unresponsive kernel/filesystem operation.

Every installed normal command and internal helper acquires a shared nonblocking flock before
dispatch and holds it through its process lifetime/owned child cleanup. Installer commands instead
take an exclusive nonblocking lock. Busy operations reject without changing the active generation.
The existing manager never recreates a missing lock; root and lock inode identities are rechecked
after acquisition and before mutation. This preserves paths a parent still needs to spawn helpers.
A startup racing installation can fail explicitly; uninterrupted startup during replacement is not
promised. Self reinstall/uninstall operates from the already mapped executable and does not spawn a
helper from its removed path.

Installation writes a `staging-` directory, syncs files/directories, writes the manifest last and
validates the complete result before renaming it into a generation. A temporary pointer is renamed
atomically over `current`; the initial public link is created exclusively. Before publication,
ordinary failure/cancellation cleans only this invocation's recorded creation identities after
rechecking the entire tree. Failure to prove ownership preserves the stage and reports incomplete
cleanup. A later invocation reclaims only fully validated inactive artifacts; invalid or incomplete
stages are never deleted by name. A low-level bootstrap failure may retain an incomplete manager
for inspection. Process exit/power loss can occur outside these ordinary-error cleanup paths.

After changing `current`, failures explicitly report partial publication and retain the new
generation. They never silently restore the old executable. Cancellation arriving after publication
does not interrupt the remaining bounded cleanup. Uninstall validates everything before removing
the public link, then removes known pointers/payloads, marker and finally lock/manager. A filesystem
failure after removal starts reports incomplete cleanup and leaves remaining artifacts for inspection.
Neither installation nor uninstall claims an all-or-nothing transaction across arbitrary failures.

The seven runtime/reference texts reviewed in D78 are embedded unchanged into the single executable
and installed beside each generation. The test-only LLVM notice is excluded from this ordinary
executable. The JSON Schema historical-license reference remains explicitly a reference, not a
retroactive rights determination. No external module is added or upgraded. The new D79 artifact
snapshot separately records 267 selected packages, the same four external module versions/sums,
ninety production Go source records, seven embedded texts and go.mod/go.sum. The earlier D78 report
is retained unchanged. The new snapshot records a dirty build based on D78 plus exact selected
production inputs; it is not falsely attributed to a clean commit. See DEPENDENCY_REVIEW.md.

Independent installation tests are written before their APIs and first fail because implementation
is absent; CLI dispatch tests likewise fail on missing service fields. Passing tests then cover
normal/forced/repeated operations, eighteen unsafe/changed-content cases, invalid source/cancellation,
twelve pre-publication failure/cancellation cases, shared leases and retired-generation rejection.
Real child process exits at prepared/published checkpoints separately verify retained current state,
kernel lock release and bounded recovery; ordinary injected failures are not substituted for these
process controls. Changed-lock identity and ambiguous-partial-stage preservation also pass.
These are process exits on the current filesystem, not a sudden power-loss/durability experiment.

The actual compiled command is copied into an isolated HOME outside the repository and initialized
as a schema helper. Its seven notice files are byte-exact. While the helper is held, force install
and uninstall both reject; after joined helper exit, self reinstall publishes a different executable
path and self uninstall removes installation artifacts. A second uninstall succeeds from the source.
Six user/project/product/unrelated-file sentinels, their modes, source bytes and observed process
group cleanup pass. The focused compiled race run passes in 5.629s (3.67s test). Final installation
and command race suites pass in 7.667s and 4.963s after adding committed-cancellation coverage and
ensuring bootstrap-cleanup errors retain the explicit cleanup status. Applicable launcher/childproc/ACP/schema regressions
pass in 26.376s, 5.840s, 5.010s and 3.075s. Whole-repository vet, formatting, whitespace, build and
executable help pass. This development-host test does not substitute for clean supported macOS
release installation, full live alpha lifecycle, soak tests or owner/dependency license clearance.

The bounded public-only local Claude consultation `public-install-review-review-sssx6nt0` is saved,
read fully and assessed. Stable locking, partial-publication reporting and independent crash checks
are useful advice; deleting invalid stages by name and a mistaken extra path segment are rejected.
The response supplies no validation or implementation-source authority. No earlier implementation
is consulted, no real client/user configuration changes and no Kiro model request occurs. Development
run policy remains enabled; D75 login renewal and D77 personal-root-memory fidelity remain open.

## D80: distinguish native memory observations from a source-preserving adapter

Personal CLAUDE.md remains an open compatibility requirement. Do not enable either a renamed
rules entry or a settings-stage configuration redirect. Do not infer effective exclusion from an
absent context entry, and do not treat explicit fixture alias exclusions as a general policy mapper.
No production code, dependency, development executable or frozen artifact changes in this decision.

Twelve installed Claude 2.1.263 controls independently put an exact original-root exclusion in user,
project or local settings. Supplying the exact private alias as an additional flag-level exclusion
preserves the expected root body, ordering, four import hops and original exclusion in these fixtures.
The alias is deliberately supplied by the test; no effective-settings discovery or glob translation
has been implemented. Managed policy, glob edge cases and later settings changes are not established
by this result. Naming a rules entry CLAUDE.md still loses the path-frontmatter root body and also
changes the observed order of its imports relative to general rules. The existing acceptance bar
is retained, rather than reducing import depth or dropping the root body.

Five bounded streaming-control sessions independently test initialize, get_settings and
get_context_usage requests against the unmodified pinned client. Effective exclusions match the
owned input; context inspection makes zero model and zero token-count requests. A plain rules link
is listed by original path; a direct user-root alias is listed by alias, even when the original
path is excluded. Both a conditional rules entry and an excluded one are absent. Thus an absent
entry is not an exclusion oracle. Exact path identities, original files, private cleanup and joined
process groups are checked. Control frames are limited to 64 KiB each, 32 total, with a five-second
read deadline and fifteen-second process lifetime. No SDK is installed or executed.

Public [memory documentation](https://code.claude.com/docs/en/memory) supplies the native import,
path and exclusion concepts. The [Agent SDK reference](https://code.claude.com/docs/en/agent-sdk/typescript)
describes local context inspection and alpha resolveSettings; that snapshot API does not run a
policy helper or fetch server-managed settings and has trust-related differences. It is distinct from the
live get_settings control observed here. The public function documentation does not specify every
CLI envelope: wire probes are authored independently and validated only for the pinned executable.
The SDK package metadata refers to its README license, whose commercial-terms statement is not
assumed to be an MIT grant. No new dependency or license clearance follows from reading these docs.

A separate candidate keeps the process-level configuration path private, but puts the original
owned HOME/.claude path in the temporary flag overlay's env.CLAUDE_CONFIG_DIR. It initially passes
finite print memory controls. With normal persistence enabled it adds one completed transcript in
the original projects tree; positive controls check the synthetic answer inside the new bounded
file without printing its content. Existing originals, global JSON and project settings remain
unchanged in that finite candidate run. The native comparison also positively saves a transcript.

The public [environment reference](https://code.claude.com/docs/en/env-vars) documents
CLAUDE_CODE_SKIP_PROMPT_HISTORY, and the [CLI reference](https://code.claude.com/docs/en/cli-reference)
distinguishes it from print-only --no-session-persistence. Setting the environment option suppresses
the candidate's source transcript write. Six natural/candidate print sessions then match root body,
ordering, four import hops, original exclusion and conditional-rule activation after one owned Read.
This is a scoped observation, not approval to use the candidate in normal run.

The independent plugin fixture establishes the missing distinction. Finite plugin skill expansion
passes, but interactive startup changes the original settings.json. A separate native uninstall
changes original enabledPlugins and removes the original installed_plugins.json registration.
The original marketplace, global JSON and Git configuration remain unchanged in the candidate
controls. Disabling transcript persistence therefore does not isolate settings/plugin writes.
Initial acceptance attempts fail; the retained counterfactual explicitly requires these rejected
effects and never weakens the ordinary product-profile preservation assertions. All changed source
files belong to disposable fixtures, never the user's actual HOME. Settings diagnostics contain
fixed categories and field-presence/change facts; no prompts, tool outputs, credentials or raw
client stderr are printed or persisted as diagnostic logs.

The public-only local Claude consultation public-root-exclusions-review-4jid6djz is saved, fully read
and assessed. Its cautions about all settings scopes, glob anchoring, ordering and false inclusion/
exclusion are useful. Its suggestion to reduce the import limit is rejected. Its claim that no
effective-settings inspection exists is corrected by the public API and live-control observations;
neither proves a complete adapter. No earlier implementation or private client implementation is
consulted, no Kiro model request or login mutation occurs, and no feature is published.

The six observation/counterfactual controls pass together under race detection in 22.428s, covering
39 client control/conversation sessions plus finite version/plugin-management commands. Passing
counterfactuals mean the rejected defects remain reproducible, not that personal-root support is
complete. Required product preservation and memory semantics remain unchanged. Development run
policy stays enabled. Full client-environment preservation, live lifecycle, soak, clean-host release and
rights/license gates remain open.

After closing parent pipe ends and registering cleanup immediately in the new control fixture,
all seven applicable installed-client regression controls pass under race detection in 40.373s,
including native memory identity, personal assets/rules and the ordinary plugin preservation gates.
Opt-ins-off race suites pass interop 24.864s and launcher 28.615s. Whole-repository vet, formatting
and whitespace checks pass. The D79 development executable and its reviewed selected inputs are
unchanged; no rebuild or inventory refresh is required for these test/documentation changes.
The unchanged installation snapshot again verifies all 138 file records, including that executable,
without granting release clearance.

At the end of this work, a fresh read-only identity check observes whoami exit 0 for both pinned
Kiro entry points (5.140s package). The correctly named product login-preflight test then passes in
1.686s, verifying the identity, private scope and bounded trailing notice. An earlier misnamed test
selection ran no tests and is not counted as validation. No account values or credentials are retained.
This supersedes D75's absent-login observation: no further login action is currently required, and
bounded live testing can resume. It does not establish the still-unrun live acceptance cases.

## D81: verify actual process loss followed by an independently supplied request

After D80 verifies the restored login, rerun the independent process-loss guards and the installed
Claude/fake-ACP rehearsal. They pass together under race detection in 7.609s, including the actual
client error, exact intercepted Read, absent hook activity, joined old cleanup and successful new
request. The existing bounded LIVE_KIRO_TEST_PLAN.md sequence can therefore proceed with the
already authorized actual Kiro pair; neither request or acceptance condition is changed.

The actual Kiro 2.21.2/v2 / Claude 2.1.263 test passes in 21.51s (22.801s race package). Before any
client tool delivery, the first owned ACP group is terminated. The actual turn reports internal
cancellation while its caller remains active; the existing guard requires the independent process
death/cleanup proof and distinguishes this from user cancellation or timeout. Claude receives a
JSON error and exits 1. One exact Read is intercepted, its denial hook never runs and the source
canary is neither read by the client nor disclosed. The old ACP/relay group, policy artifacts and
pool entry are gone before the new request is admitted.

The separately supplied second request preserves owner/model/effort/tools/metadata/user policy,
returns nonempty text and one delivered end_turn, and exits 0. The short native billing-header
variation is observed by the existing bounded predicate; requests are passed through unchanged.
There are exactly two main requests, two prepared/cleaned backend processes, one intercepted tool
and one successful fresh completion. Both client/ACP groups and relays are gone; all four tracked
policy/relay artifacts and the profile are removed. Source settings/canary, empty HTTP/pool state
and shutdown checks pass. No trial is repeated and no production, fixture or dependency code changes.

This closes the measured process-loss-before-delivery/fresh-request alpha scenario. It does not
establish interactive continuation in the same client, late tool results, sibling-session failure,
foreground Ctrl+C, authentication expiry, model changes, or resume. Personal-root memory, complete
live lifecycle, soak, clean-host release and rights/license requirements remain open. Only fixed
categories/counts/booleans are retained from the live test; no model text, tool output, credentials,
actual-user assets or earlier implementation is used as a fixture or implementation input.

The unchanged D79 executable's doctor command also exits 0 with a separate owned state directory:
login verified, policy verified, launch_available true and a non-stale catalog for the pinned pair.
Its client_initialization remains unverified by design; doctor does not launch the client or prove
all acceptance gates. This confirms current development admission without an interactive run or
another model request. Documentation/whitespace checks pass; no binary rebuild is needed.

## D82: observe typed streaming cancellation and keyboard exit in the compiled command

Add a separate test-only CLI/ACP observer and drive the ordinary compiled `run` from an owned
terminal with the unmodified pinned client. Independently authored protocol and state guards first
fail on the absent observer API, then verify active correlated prompts, exact frame forwarding,
bounded receipts and admission consumed across replacement processes. The observer records only
fixed events, counts, Boolean classifications and owned lifecycle coordinates. It never executes
client tools or logs ACP bodies, captured terminal text, credentials or native stderr.

The supervisor captures its terminal settings and foreground group, launches the compiled command
with inherited descriptors and checks restoration after the command exits. It does not restore
state itself. The client exec wrapper records its actual foreground ownership and private profile;
only the owned empty project is pretrusted. Native tools/MCP are empty, the test supplies a short
text-only system instruction and disables thinking, experimental betas and transcript history.
The live Kiro wrapper forwards ordinary version/account/catalog commands and ACP bytes unchanged,
preserving restricted KIRO_HOME, agent argv and launch directory. Only the native Kiro child's HOME
uses the existing account for authentication. No production setting or actual-user asset is changed.

The first fake attempts mistakenly allow one ACP prompt for the entire client. Observation shows
the legitimate separate title request, required by ACCEPTANCE_SPEC.md and FLOWS_AND_STATE.md. The
fixture now permits one main and one title prompt, with separate exclusive admission files checked
before forwarding, and attributes events by that process's known purpose. Its coarse title-word
observation is scoped to these two test instructions; it is not a production classifier. A title
completion cannot satisfy main readiness or main cancellation, and every observed group is retained.
These were test-harness failures; production request-family behavior is unchanged.

Public [keyboard documentation](https://code.claude.com/docs/en/interactive-mode) and observed native
screens distinguish a typed Ctrl+C from kernel SIGINT and Ctrl+D exit. Require an active main
prompt, at least two text updates, no main completion/cancel and a generated marker visible in the
client before typing one Ctrl+C. The marker's complete form is absent from the prompt, so input
echo cannot establish generation. Then require a forwarded main ACP cancellation and its group
gone while client/proxy remain alive within eight seconds. This checks the product's retirement
contract; it does not measure provider-side execution or billing cancellation.

The test exits through Ctrl+D and sends a second Ctrl+D only after the client explicitly requests
the same key again to exit. Require command exit 0, passive terminal restoration, all recorded
PIDs/groups absent, listener closed, runtime/profile removed and original owned settings unchanged.
Known groups remain available to failure cleanup even after a later receipt-read error. Emergency
group termination and its three-second bounded join always fail the test and cannot satisfy a
passing result. This is a census of observed owned groups/PIDs, not every historical or detached
descendant. The separate held-hook keyboard-exit and following-question cases remain open.

All three actual-Claude/fake-ACP controls pass together under race detection in 23.041s: natural
completion and ordinary-character input each deliver forty main text updates and one end without
cancellation; typed Ctrl+C after two updates yields one cancel and no main end. Every case reaches
confirmed keyboard exit, restored terminal state, removed observed processes/artifacts and unchanged
sources. The independent fixture/readiness guards pass again in 1.298s/1.897s before actual inference.

The actual Kiro 2.21.2 / Claude 2.1.263 trial runs once and passes in 18.34s (19.651s race package).
Ctrl+C follows 24 main text updates and visible generated text. One cancel and one cancelled reply
occur, with no main end. The main ACP group disappears while client/proxy remain alive, followed
by confirmed keyboard exit and terminal restoration. All four recorded groups and seven recorded
PIDs, listener, runtime and profile are gone; source settings/global JSON are unchanged. There is
exactly one main prompt and one completed title prompt, with no guard failure, emergency cleanup
or live retry. The detailed bounds and fixed diagnostic log location are in LIVE_KIRO_TEST_PLAN.md.

The public-only local Claude consultation public-terminal-cancellation-review-v49xatpe is saved,
fully read and assessed. Typed-byte controls and passive restoration advice are useful. Requiring
all Kiro processes to remain alive after cancellation conflicts with this project's retirement
contract and is rejected. Raw capture dumps and unconditional stale-PID kills are also rejected.
No libproc API, client SDK, dependency, prior implementation or private client source is adopted.
These are test/documentation changes; the D79 development artifact and its frozen inputs remain
unchanged. Development run remains enabled. Other live lifecycle, personal-root memory, soak,
clean-host release and rights/license gates remain open.

Applicable opt-ins-off race regressions pass interop 23.720s, launcher 28.446s, childproc 6.106s,
ACP 5.031s and command 3.790s. Whole-repository/observer vet, formatting and whitespace checks pass.

## D83: verify keyboard exit while an exact native Read hook remains held

Extend the independent D82 observer with two native command-hook roles and a fake ACP MCP client.
The product command and installed clients remain unchanged. A pre-hook accepts only the expected
PreToolUse/Read event with one exact owned file_path and a bounded nonempty tool-use ID. It consumes
one exclusive admission marker and records only the ID digest plus fixed process/lifecycle facts.
The post-hook must match the same ID and input. Input/readiness tests first fail on absent APIs,
then cover wrong event/tool/path, extra input, missing ID, oversized input, absent/repeated/released/
interrupted hook, main completion/cancellation and title-only observations.

The public [hook contract](https://code.claude.com/docs/en/hooks) supplies the native input and
permission-decision fields. Only event, tool name/input and ID are inspected; remaining bounded
payload is discarded without logging. Neither hook opens the requested file or transcript. The
two-second/64 KiB stdin bound and twenty-second held-hook bound fail explicitly. SIGINT/SIGTERM
return denial, while only an exact owned release marker permits the positive Read control. The
settings command timeout is twenty-five seconds. Expiry cannot satisfy keyboard-exit acceptance.

The fake ACP reads only the launcher-created agent's public MCP command declaration, executes the
product relay and uses initialize, tools/list and one tools/call for the discovered Read alias. It
never opens the relay's private configuration or requested file. A matching result is checked in
memory for the independently authored file marker, without logging it. The first control completes
Read but incorrectly closes its relay connection before ending the ACP prompt; the product treats
that lifetime loss as cancellation. The test fails after its terminal deadline and needs emergency
cleanup. The corrected fixture retains its one connection for the owning process lifetime; no
production cancellation semantics or deadline is weakened.

Both complete actual-Claude/fake-ACP controls pass together in 10.251s under race detection. In the
release control, the exact hook and pending relay call stay live for at least 500ms before release.
One matching PostToolUse/Read result, main end and generated text visible in Claude follow. In the
exit control, the same unreleased live hook precedes the first Ctrl+D and remains alive when the
current native screen requests the second Ctrl+D. Shutdown takes 278ms: hook interrupted, no tool
result/main end, ordinary command exit 0, restored terminal, all recorded processes/artifacts gone
and unchanged source settings/global JSON/Read file. A release marker created after cleanup has no
observed effect during a further 300ms. Existing terminal/frame/event budgets still apply.

The hook/protocol/readiness guard selection passes again in 2.160s/1.866s before live inference.
The actual pinned Kiro 2.21.2 / Claude 2.1.263 trial runs once and passes in 22.27s (23.553s race
package). Exactly one native Read hook is held and remains alive at exit confirmation. Shutdown
takes 2,392ms, with one hook interruption, one main ACP cancel and one cancelled reply, no main
completion or PostToolUse and no file marker in captured terminal text. All five recorded groups
and eight recorded PIDs, listener, runtime and private profile are gone; the passive supervisor
confirms terminal restoration and the source fixtures remain unchanged. One main prompt and one
completed title prompt run, with no guard failure, emergency cleanup or live retry. The late release
marker produces no observed event. Native Kiro's private MCP client is neither replaced nor
instrumented; fake relay call/result counts are not claimed as live observations.

The saved public-only D82 Claude consultation also covers the held-hook keyboard scenario. It is
read and applied with the same recorded exclusions; no prior implementation, private client source,
new SDK or dependency is used. This closes the controlled held-hook keyboard-exit scenario, not
arbitrary hooks, unobserved historical/detached descendants, a following question or all cancellation
paths. These test/documentation changes leave production and the D79 frozen development artifact
unchanged. Development run remains enabled. Personal-root memory, other live alpha, soak, clean-host
release and rights/license gates remain open.

The shared terminal fixture's existing natural-completion, ordinary-character and Ctrl+C controls
all pass again with actual Claude/fake ACP in 22.676s. Final opt-ins-off race suites pass interop
25.344s and the complete independent observer package 1.225s. Whole-repository/observer vet,
formatting and whitespace checks pass. No binary rebuild or dependency inventory refresh is needed
for these test/documentation-only changes.

## D84: verify a new foreground question after streamed keyboard interruption

Extend the independent compiled-command observer to type one new text-only question after D82's
proven Ctrl+C path. The interrupted main ACP group must disappear while the original recorded
Claude/proxy PIDs, profile and gateway address remain live. Only then does the parent authorize a
new question and type it; Enter requires its complete echo in the current native screen. The second
main prompt must contain its new instruction and arrive in a group absent from every previously
observed group, excluding reuse of the old main or an auxiliary survivor. Require one client exec,
unchanged live ownership, correlated end_turn and a generated marker visible in the same UI before
the existing confirmed keyboard exit, terminal restoration and private cleanup checks.

Admission tests first fail on missing APIs, then verify one initial main, one explicitly admitted
follow-up and at most two titles across replacement processes. The second title also requires the
parent's intent marker. This scope permits two sequential prompts per observer process so a reused
title session remains legal, while independent guards reject overlap, a third prompt and an old
reply completing the next request. Other keyboard scenarios retain their prior one-prompt limits.
The live maximum is four inference admissions per episode, not a provider billing-call guarantee.

Only Boolean marker-presence facts are recorded from unchanged ACP frames: old/new instruction
fragments, the interrupted partial marker and a never-submitted null marker. Independent controls
check both presence and absence, pre-intent refusal, repeated old/follow-up requests, title-only
completion, changed client, reused group, missing instruction and non-success results. Native
partial-response retention is observed, never synthesized or assumed to be a public-protocol rule.
These markers do not establish full native-history byte equivalence. No tool/MCP is exposed to the
native client, and no prompt, response body, credential or unrestricted stderr is logged.

The initial actual-Claude/fake-ACP control passes in 6.242s; the final absent-marker/group-exclusion
control passes in 6.285s. The first actual Kiro 2.21.2 / Claude 2.1.263 observation passes in 22.94s
(24.864s race package), with sixteen initial text updates, two main prompts and one title, a visible
new response in the same client and joined cleanup. Review then finds that the historical observer
end counter also accepts correlated JSON-RPC errors. Its count alone cannot establish successful
prompt completion. A new error/missing-stop/limit-stop test fails, and only end_turn now increments
end; cancelled remains separate and other correlated results increment a non-success counter that
fails the scenario. Frames are still forwarded unchanged. This is an observer defect, not evidence
of a product failure, and the earlier completion count is not retroactively treated as end_turn.

After the correction, all six actual-Claude/fake-ACP keyboard controls pass in 34.283s under race
detection, including the prior streaming and held-hook cases. A new bounded actual revalidation
then passes in 22.88s (24.731s package). The original main streams 24 updates before one cancel and
cancelled reply retire its group. The same live client/proxy/profile/address remain; the follow-up
uses a previously unobserved ACP group, yields one text update and one end_turn, and appears in the
current UI. Old/new instruction fragments and the partial marker are present, the null marker is
absent, and no non-success result or follow-up cancellation occurs. Two main and two title prompts
run within the declared bound; one title ends before exit. All six recorded groups/eleven PIDs,
listener, runtime and profile are gone, with terminal restoration and unchanged sources. Neither
episode needs emergency cleanup. The two episodes total four main and three title admissions;
the second is deliberate revalidation after an observer correction, not an automatic failed-call
retry. Detailed bounds and fixed log locations remain in LIVE_KIRO_TEST_PLAN.md.

The prepared local-Claude consultation was initially rejected by automatic approval review because
it included project-specific implementation/runtime facts without approval for that exact external
payload. The user explicitly approved it; the same command then completed with one turn and a saved
answer, which was fully read and assessed. New-input causality and the null detector are adopted.
Requiring a fresh group to have received the previous cancellation, or treating legitimate auxiliary
concurrency as cancellation failure, is rejected. Address/PID observations are not a socket-inode
or process-start-time census. No alternate destination or rejection bypass, prior implementation,
private client source, SDK or new dependency is used.

These are test/documentation changes; production and the D79 frozen development artifact remain
unchanged. Development run stays enabled. Pending-tool recovery, auth expiry, model changes,
persisted restart/resume, complete client-environment preservation, unobserved descendants, soak,
clean-host release and rights/license requirements remain open.

Final opt-ins-off race suites pass interop 25.113s and the complete observer package 1.242s.
Whole-repository/observer vet, formatting and whitespace checks pass. The frozen production inputs
are unchanged, so no executable rebuild or inventory refresh is required.

## D85: observe native model selection before the following foreground question

The public [Claude model configuration documentation](https://code.claude.com/docs/en/model-config)
describes both typed selection and the native picker, including persistence to user settings. Add
an ordinary compiled-command control with actual Claude 2.1.263 and two independent fake-ACP models.
The temporary profile must absorb native selection writes while the original owned settings remain
unchanged. No actual Kiro model request is required for this control.

After one visible end_turn completion on the initial model, open the picker and require both catalog
entries to be visible. Navigate from an observed selected row, with at most eight key actions; select
either the unchanged model or the second model. Only after the native selection confirmation may
the parent admit and type the independently supplied second question. Distinct generated markers
are absent from the typed prompts. Both completions must reach the same observed client, proxy,
profile and address. The model witness follows correlated session/new and idle session/set_model
replies, retaining only model slots in receipts. Unknown/session-mismatched selections, outstanding
prompts, missing acknowledgements and error/null/non-object results cannot establish a change.
The existing frame/admission guards bound two main and at most two auxiliary prompts across process
replacement. Non-success prompt results invalidate the observation.

An initial typed model-ID control produces an additional prompt attempt that the declared budget
rejects; that failed control requires emergency owned-group cleanup. Its cause is not established
as native model validation. The final controls use the observed picker, without increasing the
request budget. Both pass in 6.74s (8.548s race package): the first keeps model slot one; the second
has one successful target acknowledgement and its next prompt uses slot two. Each has two main
completions and one title, restored terminal, unchanged sources, and all four recorded groups/five
PIDs, listener/runtime/profile gone. A following ordinary doctor preflight restores the selected
model without another recorded client/ACP identity or prompt. This establishes startup selection,
not a resumed conversation or the behavior of a second interactive client.

Saved fixed results are in `.cache/model-review/native-picker-restore-fake-acp.log`. Independent
negative controls precede integration. The six existing native-Claude/fake-ACP keyboard controls
pass in 32.959s. Opt-ins-off race suites pass interop 24.745s and observer 1.251s; whole-repository
and observer vet, formatting and whitespace checks pass. No production, dependency or frozen
artifact changes. Actual Kiro model selection, arbitrary catalogs, effort changes, persisted
resume and the remaining alpha/release gates stay open.

## D86: verify current-catalog model selection with actual Kiro

Select the CLI default and first distinct currently advertised Kiro model without embedding a
historical model name. Finite version/catalog discovery has an exact three-command allowance,
owned configuration, bounded output/deadline and joined process groups. The separate prompt-free
CLI/ACP check observes nineteen matching IDs/aliases, equal current models and a legacy selector.
Ambiguous/overlapping display labels cannot supply a picker target.

Extend the independent terminal controls to cover all rendered and focused catalog labels, with
two- and nineteen-model fake catalogs before the actual experiment. The input prompt above the
picker heading is outside its selected-row scope. An initial heading-clipping hypothesis does not
explain the long-list stop and is removed: fixed diagnostics show a visible heading/footer and one
selected row with no exact catalog-label match. Such rows may receive bounded navigation keys, but
never count as an advertised model or receive Enter. Their row identity remains only in memory.
Multiple selected glyphs and overlapping labels remain ambiguous and receive no input. Require all
advertised labels to render and receive focus before confirming the exact target. This proves
focus coverage; it does not establish successful inference on every entry.

The finite navigation bound is three times catalog size plus four actions, with at most two
direction reversals after 750 ms without a changed row and a five-second observation stall limit.
These are test limits rather than a claimed native latency guarantee. Response evidence now also
requires each answer marker in its active ACP session's text, including split chunks, plus the
visible UI marker and correlated end_turn. Foreign-session, repeated and post-completion markers
cannot establish the answer. Selection acknowledgement must correlate to the session-specific idle
request; JSON-RPC replies need not invent an additional session field. All frames remain unchanged
when forwarded within the declared guards. No title or unrecognized navigation row may substitute
for a foreground selected-model response. Count ACP process receipts explicitly when checking that
the later preflight creates no new recorded client/ACP session.

After the rejected local controls and observer corrections, all three native-Claude/fake-ACP
controls pass in 11.94s (13.653s race package). The nineteen-entry case uses 26 actions and passes
five unadvertised-row frames without confirming them. The actual Kiro 2.21.2 / Claude 2.1.263 trial
then passes once in 23.90s (25.365s package), with the same catalog coverage/action counts. Two main
and one title prompt run. One successful target acknowledgement precedes the next question; both
answers have active-session and UI markers and end_turn. The same observed client/proxy/profile/
address remain. A following doctor restores the delivered model. Keyboard exit restores terminal
settings and removes all four recorded groups/seven PIDs, listener/runtime/profile, with unchanged
sources and no non-success result, guard failure, emergency cleanup or live retry. This establishes
the advertised session-setting contract for the chosen pair, not provider weights or hidden routing.

The public-interface local Claude consultation is saved/read/assessed under
`.cache/claude-consult/work/public-model-picker-review-bw6hj0j5/`. Its hypothetical question contains
no project code, runtime capture, credential or model identity. Distinguishing visibility/focus,
ambiguous glyphs, response causality and public acknowledgement from backend internals is adopted;
native validation behavior is not inferred. Fixed experiment results and limits are recorded in
LIVE_KIRO_TEST_PLAN.md. The six existing native-Claude/fake-ACP keyboard controls pass in 33.153s;
opt-ins-off race suites pass interop 24.109s and observer 1.428s. Whole-repository/observer vet,
formatting and whitespace checks pass. Production, dependencies and the D79 frozen artifact do not
change. Authentication expiry, pending-tool recovery, persisted restart/resume, complete environment
preservation, other model/effort paths and the remaining full release gates remain open.

## D87: retain optional native conversation data across private profiles

The temporary client profile previously lost its transcripts when removed. The installed-client
baseline reproduces this: the first conversation succeeds, then explicit-ID resume makes no model
request and fails after a fresh profile is prepared. A data-directory reference succeeds without
redirecting the settings root. This supplies a native source of client history for restart, while
the proxy's persistent backend records continue to contain no conversation text.

`run --client-history` opts into a reference from the private profile's projects directory to
`HOME/.claude/projects`. `run --resume UUID` implies retention and appends the native resume argument
alongside freshly generated routing settings and the selected model. Explicit retention=false plus
resume is a usage error; diagnostic commands do not accept either option. Malformed UUIDs reject
before finite preflight commands. The native client reports a valid but absent session itself.

The ordinary profile remains ephemeral without either option. Retention includes native data under
projects, not just transcript text: this is shared application data that ordinary Claude can access.
It is not a private proxy log, an immutable snapshot or a promise of all native history features.
Native settings continue to govern native retention. The proxy does not scan, parse, copy, merge,
prune or fabricate transcript content. It creates missing directory roots with mode 0700, requires
same-account directories without group/other write permissions, rejects links and file substitutes,
and compares held directory identities during reference preparation. Existing modes are not changed.
The check does not lock against later same-account changes or validate every descendant. Cleanup
removes the temporary link and configuration, preserving the explicitly retained native directory.

The public [session interface](https://code.claude.com/docs/en/sessions) documents explicit-ID resume
for print-created conversations and requires launch-specific configuration to be supplied again.
The [native directory reference](https://code.claude.com/docs/en/claude-directory) distinguishes
projects data from file-history, media and other native locations. These sources describe a public
storage/interface contract; no native transcript schema is used as an implementation input.

The following independent checks establish the current scope:

- Profile safety and CLI tests first fail on absent fields/flags, then check retention, cleanup,
  wrong source shapes/modes, UUIDs, diagnostic rejection and startup-to-client forwarding.
- Four installed-Claude local-HTTP controls pass: explicit retention, the manual reference control,
  an ordinary private profile and native persistence suppression. Both successful runs use new
  profiles, tokens and ports, with exactly one previous user/assistant and one new user marker at
  HTTP. Selected source files remain byte-identical; bounded owned data contains no generated
  model/UI credentials. No native file is decoded or edited.
- Actual gateway/session-manager restart with an independent ACP process passes. Each process
  accepts only initialize/new/prompt and verifies the projected old/new marker counts; session/load
  rejects even though load support is advertised. A random token appears only in the initial user
  question, and the second client result recovers it from restored context.
- One actual Kiro/Claude two-turn episode passes the same text scenario. Each stage has one admitted
  request and one successful end_turn, new backend/profile/address/token, and joined cleanup before
  the next stage. The second answer recovers the random initial token. The first stage also leaves
  no listener, observed process group or temporary profile. No live retry or tool effect occurs.

Actual prepared Kiro loading remains disabled: a fresh manager has no old backend record and creates
a new restricted session from the native client's supplied history. Hidden backend context can be
lost and full-history reconstruction adds provider work. This text-only finite check does not prove
interactive picker/continue behavior, pending-tool recovery, file checkpoints, external media
sidecars, concurrent writers or retention cleanup. Those remain work toward full alpha/release.

Local public-interface Claude advice is saved/read/assessed in
`.cache/claude-consult/work/public-native-history-review-t309qo98/`. The hypothetical question contains
no project implementation or runtime data. Its fresh endpoint/token, source preservation, sidecar
and concurrency concerns inform the controls. A blanket claim that narrow references make source
mutation impossible is rejected; only measured preservation is claimed. Its proposed source-format
assumptions and advice to waive unresolved concurrency/retention gates are not adopted.

The complete opt-ins-off race suite and whole-repository/new-peer vet pass. Formatting and whitespace
checks pass. The rebuilt D87 development executable has an independently collected 267-package graph,
four unchanged external modules, 100 repository input records and seven retained embedded notices.
Its named native-history inventory passes 139 offline byte checks. This is not release clearance.

## D88: verify explicit native resume through ordinary foreground commands

D87's finite client exercise is extended through the compiled public run command and ordinary
terminal input. The first launch uses --client-history; its native client receives an independently
generated UUID through the public --session-id flag. After confirmed keyboard exit and joined old
cleanup, a separate compiled run uses --resume UUID. The two runs share only the owned HOME/project
for native history. Their runtime roots, gateway addresses, processes and private profiles differ;
the second setup does not rewrite the original fixture settings.

The restored prior answer must be visible in the new terminal before typing the new question. Both
questions are first observed in the input widget before Enter is sent. The new question contains no
copy of the random token supplied in the first question. The ACP observer requires exactly one old
question marker, old answer marker, new question marker and token in the resumed main prompt, and no
unsubmitted control marker. First-stage expectations instead require only the initial question/token.

A separate bounded witness records the correlated session/new reply and rejects session/load. It
collects at most 8 KiB of active-session text in memory and emits an answer receipt only after the
matching successful end_turn, with the expected generated marker/token counts. Foreign sessions,
foreign reply IDs, missing/duplicated context, cancellation, errors and split text chunks have
independent tests. Native frames are forwarded unchanged after admission. Only fixed receipts and
owned lifecycle coordinates are persisted; native transcript bytes are not decoded or edited.

Existing process-wide frame, event, PTY, main/title admission and cleanup limits remain in force.
Each launch admits one main prompt and at most one title; it has its own 70-second attached-process
limit and two-minute setup/test context. Any failed first stage prevents the second from starting.
Confirmed keyboard exit must restore terminal state and remove every recorded PID/group, listener
and temporary runtime/profile. Emergency cleanup, source changes and non-success prompt results
fail the observation. Retained native conversation data is intentionally separate from those
ephemeral resources and is removed with the disposable test HOME after both stages.

The installed-Claude/independent-ACP control passes in 7.84s (9.120s race package). One actual pinned
Kiro/Claude episode then passes in 29.71s (30.993s): two main prompts and one title, restored history
visible before new input, both correlated answers and both clean keyboard exits. The first stage
records four groups/seven PIDs, the second three/five, all removed. No live retry, guard failure,
prompt failure or emergency cleanup occurs. The independent observer suite passes in 1.226s.
D87's saved/read/assessed public-interface Claude advice covers the native continuation control;
no new project-data transmission is needed. No production, dependency or frozen-artifact change.

This verifies explicit-ID text resume in a foreground terminal. It does not establish native
session-picker/continue behavior, pending-tool restoration, cross-project changes, media/checkpoint
sidecars, arbitrary hooks/agents, concurrent native writers, retention cleanup or unobserved
descendants. Authentication expiry and the remaining full alpha/release requirements remain open.

The nine prior installed-Claude/fake-ACP keyboard cases pass in 42.903s after the shared harness
extension. The first opt-ins-off run in the filesystem/network sandbox fails existing relay-group
controls and aborts at a loopback bind denied by the sandbox. The unchanged suite with the required
local-socket/process permissions passes in 23.275s; this is an execution-environment correction,
not a source fix or a change to test deadlines. Whole-repository/observer vet, formatting and
whitespace checks pass. The unchanged D87 artifact still passes all 139 inventory byte checks.

## D89: verify native selection of retained conversation history

The [public session interface](https://code.claude.com/docs/en/sessions) documents naming a session
and opening the interactive picker with /resume. The owned first client uses a public name flag;
the second compiled invocation starts with --client-history, then opens the picker through ordinary
terminal input. Enter is sent only after the exact named row appears selected below the native
heading. Missing headings, another selected row, duplicate names and partial-name matches reject.
The selector is bounded to ten seconds within the existing terminal lifetime.

After selection, require the old answer visible before the new question. D88's unchanged ACP
witness checks the initial/resumed marker counts, recovers an independent token supplied only in
the first question, and requires a correlated successful response from the owning session. Each
run still admits one main prompt and at most one title. Both stages share a disposable HOME,
project and product state directory; temporary runtimes and observer executable paths differ.
This does not establish stable executable-identity cache reuse merely from the shared state path.
Original source settings are not rewritten for the second stage, and each keyboard exit must join
every observed process/group and remove its listener/runtime/profile with restored terminal state.

The first local picker control passes in 8.08s (9.363s race package). After sharing product state,
the fake ID and picker controls pass in 17.894s. One actual Kiro 2.21.2 / Claude 2.1.263 picker
episode passes in 33.17s (34.835s): two main requests, no titles, restored old answer before input,
one validated context and correlated answer per stage, exact token recovery and clean keyboard
exit. Each stage records three groups/five PIDs, all removed. No live retry, guard failure, prompt
error or emergency cleanup. Observer race passes in 1.903s. D87's public-only saved/read/assessed
Claude consultation already covers this native history control; no new project-data query is sent.

No production or dependency change is needed for this named current-project text flow. Continue/
latest, broader/cross-project selection, pending tools, arbitrary hooks/agents, checkpoints/media,
concurrent writers/retention, authentication expiry and full alpha/release requirements remain open.

The opt-ins-off interop race suite passes in 25.513s. Whole-repository/observer vet, formatting and
whitespace checks pass, as do the unchanged D87 artifact's 139 inventory byte checks.

## D90: observe completed client effects across native history resume

The [public native session contract](https://code.claude.com/docs/en/sessions) restores conversation
tool calls/results and rereads launch configuration. This establishes a test surface, not an
exactly-once execution guarantee. Observe public Messages requests and independently owned native
effects without decoding/editing transcript files. The saved public-only Claude consultation at
`.cache/claude-consult/work/public-tool-resume-review-jnlw7k4c` was read completely and assessed.
Adopt non-idempotent effects, exact ID/result checks and joined old ownership. Reject unrestricted
request logging, treating native resume as an ACP operation, and blanket claims about unmeasured
crash/acknowledgement windows or arbitrary repetition counts.

Each owned Write/Bash case performs one operation through the client, receives its successful
result and finishes. The Bash command appends a line; Write creates its own separate target.
Exact PreToolUse/PostToolUse hooks append fixed receipts. A test-only gateway adapter admits two
model requests in that first native launch and one in a second explicit-ID resume. The resumed
request contains one original assistant tool use and one matching successful user result, ordered
before the new user instruction. IDs/names, decoded argument objects and the result's text blocks
must match the first observed pair. Old question/answer markers occur once; the new answer is
observed in both the active backend text and native public result JSON. No newly emitted tool may
reach the client during this text continuation, and any such attempt fails the observation.

Each stage owns one prepared restricted ACP process/session, one manager/pool, bounded schema
worker and local server, and a fresh temporary native profile and credential. The first native
client and observed ACP/relay group must be gone, its listener closed and its private artifacts
removed before the second stage. Original settings/global configuration stay unchanged. Native
history survives only in the explicitly retained disposable projects directory. The second stage
must use the same native conversation ID but a different backend group/profile/address/token.
Effects and both hook receipts remain exactly one after each stage. Source contents, model text
and tool results are not written to diagnostic logs; only fixed counts/facts are retained.

Negative controls change IDs/arguments/result text/status, remove/duplicate/reorder blocks, move
the completed answer before its result, emit an unexpected resumed tool or double an effect/hook
receipt. The independent ACP peer reads only an invented operation expectation and public relay
launch declaration; it never reads native history or effect files. It has a sixty-second lifetime,
one initialization/new session/prompt, a 1 MiB incoming frame limit and bounded owned PID/stage
receipts. Its initial prompt relays the exact one operation. Its resumed prompt requires prior
context markers and tool blocks, then emits text without another tool call. Backend load rejects.

Initial local controls found test-only defects: a counter directory lacked required private
permissions; native first requests include a trailing system message; delegating the test model
list to the manager triggered an unwanted discovery process. The corrected observer admits one
user message followed by at most one system message, uses the prepared catalog and retains all
other bounds. Corrected fake-ACP/native-client cases pass in 10.55s (12.473s race package), with
both operations and all four stages clean. No live inference ran before those controls passed.

One actual Kiro 2.21.2 / Claude 2.1.263 run passes both cases in 56.99s (58.324s race package),
Write 29.65s and Bash 27.34s. Across four native launches: four main ACP prompts, six HTTP model
requests, two original client tool effects, two restored matching historical pairs, two resumed
text completions, zero resumed tool handoffs and joined old/new cleanup. No live retry occurs.
The log is `.cache/history-review/d90-live-completed-tool-resume.log`; corrected fake evidence is
`d90-native-tool-resume-standing-control.log`. No production code/dependency/artifact change.

This establishes finite single-tool completed-effect resume for these two operations. It does not
establish interrupted pending recovery, a new permission/hook decision after resume, interactive
tool resume, the entire default registry, concurrent writers, native file checkpoints/media or
arbitrary effect/acknowledgement crash windows. Those and all other alpha/release gates remain open.

Final native-Claude/fake-ACP and observer controls pass in 10.830s after adding a counterfactual
completed answer placed before its tool result. The opt-ins-off race regressions pass: ACP 5.670s,
interop 22.879s. Whole-repository/fake-peer vet, formatting and whitespace pass. The unchanged D87
development artifact still passes all 139 frozen dependency/input byte checks; no release clearance
is inferred. Final logs are `d90-final-native-controls.log`, `d90-core-regression.log` and
`d90-final-vet.log` under the owned history-review cache.

## D91: observe abandonment of an unexecuted native tool across resume

The independent native hook validates one owned session, Bash command and public tool-use ID,
stores only fixed receipts and process coordinates/ID digest, and waits at most thirty seconds.
Require a completed HTTP handoff and the exact live hook before repeated runner cancellation.
Join old hook/client/ACP/relay groups and private resources before releasing the old wait marker
and preparing a fresh native profile, endpoint, credential and backend for the retained session.
Neither the proxy nor the hook performs the tool effect. Native transcript files are not decoded,
edited or used as fixture input.

Claude 2.1.263 omits the unfinished pair in this measured print/SIGTERM path. With no preceding
model text it inserts `No response requested.`; otherwise it preserves the partial response.
Accept this only as the measured non-completion context alongside independent proof of no effect
and joined cleanup. Do not invent an error/success result or describe it as an explicit cancellation
notice. The new question explicitly prohibits execution; require the entire original question,
exact prior partial text or placeholder and exact new question in order, with no old ID/tool pair,
completed-answer marker or new handoff. Existing completed-result checks remain exact.

The initial local strict error-pair assumption fails after safe cleanup, prompting a bounded
public-request observation. Public-only Claude advice is saved/read/assessed in the D91 location
listed by LIVE_KIRO_TEST_PLAN.md; its hypothetical explicit-notice premise is not an observed fact.
Release/no-text/partial-text controls pass in 12.92s, and completed Write/Bash regressions in 7.83s.
One actual Kiro 2.21.2 / Claude 2.1.263 episode passes in 30.19s (32.156s race package), without
retry: two main prompts, two HTTP requests, one initial handoff, zero tool effects/results and a
resumed text completion. Cancellation returns in 147ms; all 31 bytes of prior partial text remain
exact. Old recorded ownership is gone before the late-release check and fresh resume; source
settings are unchanged and private resources are removed. Final ACP/interop/hook race regressions
pass in 5.738s/22.585s/1.306s; vet, formatting, whitespace and the unchanged artifact's 139 byte
checks pass before checkpoint beae010. No production or dependency change.

This does not establish implicit/interactive pending recovery, new post-resume permissions/hooks,
multiple pending operations, uncertain effect/acknowledgement crash windows or full alpha/release
completion. Those remain separate requirements.

## D92: re-evaluate client tool policy after completed-history resume

After one completed owned Bash append and joined old ownership, resume the same native conversation
with fresh routing/profile/backend ownership and a separately supplied current user-settings file.
Request a distinct new append. Compare an allow rule, an explicit deny alongside that rule, and
a PreToolUse veto alongside allowance. Neither source settings file is edited by the runtime.

The first resumed model request must preserve the old completed tool pair and answer before the
exact new question. The next result continuation must preserve that pair again while supplying
one new call/result after the new question. Require the new ID to differ from the old one, exact
decoded operation arguments, expected success/refusal status and the hook refusal reason where
applicable. A test-only backend checks this independently of the fake peer's MCP result validation.
Require one old append/pre/post receipt throughout, one new effect/pre/post receipt for allowance,
and no new effect/post receipt for either refusal. Hook veto requires one pre receipt; the explicit
deny control accepts zero or one and makes no claim about native evaluation order.

Each of six native stages owns one ACP process/session/launch and admits two HTTP requests and one
tool. Preserve the D90 native/turn/overall limits, reject another operation or model prompt, and
join all observed client/ACP/relay ownership, listener/profile and launch artifacts. Independent
controls reject changed old/new history, reused IDs, wrong result status/reason, reordered/missing/
duplicate blocks, unproven/repeated handoffs and effect/success-hook evidence after refusal.

All three native fake-ACP cases pass in 16.22s (17.557s race package); observer controls pass in
2.044s. Existing completed/interrupted native cases pass in 23.479s; ACP/interop/hook race suites
pass in 6.017s/21.855s/1.308s. The first actual invocation stops before model work at preflight.
After fresh read-only version/login, isolated identity and catalog checks pass, a new bounded
invocation passes allowance, configured denial and hook veto in 142.13s (143.450s package).
Across that episode: six main prompts, twelve HTTP requests, four owned append effects, two
matching new refusals, exact retained old pairs and six clean stages with unchanged sources.
No model prompt is retried and no limit or execution policy is relaxed. LIVE_KIRO_TEST_PLAN.md
retains both invocation results and their diagnostic scope; the first preflight cause is unknown.

No production/dependency/artifact change is required. Whole-repository/fake-peer/hook vet,
formatting, whitespace and 139 artifact byte checks pass. This proves finite settings/hook policy
after completed Bash history; interactive approval screens, new tools after interrupted history,
arbitrary/default registries, simultaneous calls, authentication expiry and full alpha/release
requirements remain open.

## D93: reject additional-directory loading as a personal memory adapter

The [public memory interface](https://code.claude.com/docs/en/memory) and
[environment reference](https://code.claude.com/docs/en/env-vars) document loading additional-directory
memory, but do not establish equivalence with personal user scope. A public-only local Claude
consultation is saved/read/assessed at the D93 location in LIVE_KIRO_TEST_PLAN.md. Correct its
five-hop assertion and uncertainty about the environment variable using current official docs;
do not use model recitation or absence from CLI help as conclusive evidence.

An owned native-Claude/local-Messages experiment first requires natural equivalence, including
unconditional root text with path frontmatter, four-hop relative imports, ordering and original
absolute-path exclusions. The candidate adds only the original owned .claude directory through
--add-dir and enables its documented memory variable; personal rules keep their existing native
source reference. It is not installed in the product. No external inference or client tool is used
in this candidate experiment, and the public request is reduced to bounded synthetic marker facts.

The initial comparison fails: the natural root and four import hops precede project instructions,
while the candidate's root loads after the project instructions and its root imports/hops are
absent. This establishes two observable incompatibilities, not their internal native cause or a
claim about every additional-directory import. The client completes and its group exits in both
cases. Keep that failed equivalence evidence rather than calling the adapter supported.

A separately named counterfactual reproduces those defects and checks original-path exclusions;
all four cases pass in 1.93s, including root/rule suppression with retained project instructions,
candidate source preservation and joined group/profile cleanup. Existing rule/depth/adapter
controls pass too, combined race package 14.722s. This is a passing rejection regression, not a
passing personal-memory acceptance gate. No production/dependency/artifact change. A solution must
still preserve the required original personal semantics and source settings; neither plain text
loading nor this additional-directory candidate is enabled as a substitute.

## D94: bounded concurrent HTTP and ACP process churn

Use one loopback HTTP server, client transport, session manager and process pool across the complete
episode. Eight simultaneous client identities each complete one buffered response, then send the
exact retained assistant text and a distinct second question. Each continuation must reach the same
observed ACP PID/session with prompt count two and only that new question. Wait until all eight have
emitted text and all eight handlers/backend turns remain active before repeated caller-context
cancellation. Reject terminal/error events as evidence of a cancelled held response. Require zero
pool ownership, connections and handlers, plus ESRCH for each recorded PID and process group, before
admitting the next wave with new client identities. The next wave exercises bounded binding eviction
and fresh admission; four final shutdown calls must join the same outcome.

The independently authored peer has one session, at most two prompts/twelve frames, a 64 KiB frame
bound and thirty-second lifetime. Its first response completes and its second emits an observation
but no final reply; it accepts public cancellation and remains subject to transport retirement.
It imports no proxy code, executes no tool, starts no child and accesses no files or network. This
experiment configures eight processes with one session per process and eight retained idle slots;
it does not exercise shared-process sibling cancellation or the prepared relay policy.

Default execution uses eight waves (128 requests); DAX_FIXTURE_CHURN_WAVES accepts only 8 through 64.
Each wave has fifteen seconds, the episode three minutes, HTTP input/output observation sixteen KiB,
eight client connections and a sixteen-connection server cap. After four warmup waves, every settled
wave records OS descriptor count, Go goroutines and GC-retained HeapAlloc for the test process hosting
the gateway/manager. Fixed failure bounds are baseline plus two FDs, sixteen goroutines and eight MiB
of heap. These are a finite regression envelope, not proof against smaller leaks, native RSS growth
or accumulation over arbitrary duration. The descriptor observer is independently challenged with
eight retained handles; response controls reject changed ownership/history and premature completion.

The first invocation fails before requests because the test's external pool configuration does not
match the session's normalized request timeout. Matching the test configurations fixes that setup
error without changing production code. The eight-wave run and observer controls pass in 1.25s /
4.404s race package. A separate 64-wave episode passes in 4.48s / 6.856s race package: 1,024 requests,
512 completed responses, 512 observed active-stream cancellations and 512 joined process/group
instances. After warmup FD/goroutine counts stay 6/4; final shutdown yields 5/2. Heap baseline is
813,696 bytes, measured peak 1,017,664, and final 836,344. No real client, external inference, native
tool, dependency or production change is involved. Actual-client/relay soak, pending-tool churn,
shared-process stress, native RSS and long-duration release evidence remain separate requirements.

Applicable opt-ins-off race regressions pass: ACP 5.183s, pool 2.435s, gateway 3.502s, session
29.327s and interop 23.569s. Whole-repository/fake-peer vet, formatting, whitespace and the frozen
D87 artifact's 139 byte checks pass. The fixture provenance inventory includes the new peer.

## D95: concurrent pending-tool denial, recovery and idle eviction

Extend finite load evidence to eight independent one-call tool batches, using one HTTP server,
transport, manager, process pool and schema pool throughout. Each synthetic client receives one
inert client_action handoff and completes its HTTP response while its ACP/relay remain live. All
eight IDs, processes, relay children and config paths are distinct. A foreign-owner control replaces
both the assistant call ID and matching result ID with another live session's ID, so the Messages
envelope is internally paired and passes request/control decoding. It must receive HTTP 400 while
all original pending processes/relays remain live and their valid recoveries remain possible.

Then supply each exact original batch's error result followed by a distinct new question. Require a
fresh observed ACP PID/relay/config, first prompt count one, exact old question/assistant text/call
ID/name/input, matching error result and new question in a full-history projection. The response
must end normally without another tool handoff. Independently require old leader/group/relay absence
and removed old config/directory. Keep the eight completed replacements idle; new identities in
the next wave evict them, and all their recorded ownership/artifacts must be gone before that wave's
pending barrier. Four final closes join the last idle owners, server, manager and schema resources.

The new independent ACP peer has one session/prompt, twelve 64 KiB-bounded frames and a thirty-second
lifetime. It launches only its supplied effect-free MCP relay, exposes bounded synthetic identity/
prompt/config-path observations, never reads the config and never implements the requested action.
An initial fixture delayed child Wait until ACP EOF; the second wave receives 502 and retained relay
cleanup errors in 4.13s. Promptly reaping that owned child while ACP stays idle makes the eight-wave
test pass in 10.49s / 13.639s race package. No product cleanup rule is relaxed. OS zombie state was
not separately sampled; the child-wait scheduling diagnosis comes from the peer's lifecycle code
and the corrected control, not an observed native Kiro defect.

Defaults are eight waves; DAX_FIXTURE_PENDING_WAVES accepts 8..32. Limits are eight manager bindings,
eight one-session ACP processes/eight idle slots, at most eight schema workers, eight client
connections/sixteen server connections, sixteen KiB observed HTTP bodies, twenty seconds per wave
and three minutes per episode. After four warmup waves, use D94's fixed +2 FD/+16 goroutine/+8 MiB
GC-retained test-process heap envelope. Eight idle ACP/relay owners and the reusable schema pool
remain deliberately present at these sampling points; final shutdown must clear all owner counters.

The final 32-wave race episode, including the strengthened internally paired foreign-owner control,
passes in 41.61s / 45.299s package: 544 HTTP requests, 256 inert handoffs, 32 rejected foreign histories,
256 fresh text recoveries and 512 joined ACP groups plus 512 joined relay children/config directories.
Steady FD/goroutine baseline and peak are 70/108; final counts are 5/2. Heap baseline/peak/final are
1,204,528/1,351,096/942,832 bytes. Independent history/ownership observers pass in 3.098s package.
No actual client, external inference, native tool effect, production or dependency change. The finite
run does not establish multi-call batches, real client UI/hooks, prepared/native policy, shared-process
semantics, native RSS or long-duration release soak.

Final opt-ins-off race regressions pass: ACP 5.416s, pool 2.554s, gateway 3.397s, session 38.741s,
relay 10.413s, MCP 5.689s, schema 3.125s and interop 24.826s. Whole-repository/fake-peer vet,
formatting, whitespace and the unchanged D87 artifact's 139 byte checks pass.

## D96: revoke idle relay connections immediately during shutdown

D95's repeated idle eviction exposes a fixed one-second delay in the relay socket's Close path.
D39 set each accepted connection's read/write deadline to one second in the future, then joined
its handler. An idle authenticated lifetime reader therefore waited for that artificial deadline
before notifying its child of lifetime loss. Close now closes accepted Unix connections while it
owns the connection registry, waking lifetime/request readers immediately, then closes the broker
and joins handlers. The existing lock still prevents a handler from changing deadlines after closure.

This supersedes only D39's connection-deadline mechanism. Preserve the separate one-second check
for actual recorded peer disappearance, private-directory removal and retained ErrCleanup on failure.
Concurrent/repeated callers still join the same sync.Once outcome. Socket Close does not signal a
wire-supplied PID or terminate a healthy ACP owner. No wire contract, resource limit or dependency
changes. Existing cancellation-safe relay stdio remains responsible for waking its own blocked I/O.

First add an independent authenticated idle-peer test, measuring only after the full handshake.
Eight simultaneous Close callers must finish below 750ms, close broker admission, leave no accepted
connection or pending job, observe the peer gone and remove private artifacts while its healthy
group owner remains alive. Before the change it fails at 1,018ms; afterward it passes at 18ms.
Existing attachment authentication/lifetime cases and socket cleanup pass (5.800s race package).
The deliberately lingering peer still fails cleanup after its separate bound (1.07s case).

Repeat the same D95 32-wave fixture with unchanged limits/assertions: 544 HTTP requests, 256 inert
handoffs, 32 foreign-history rejections, 256 fresh recoveries and 512 joined ACP/relay owners pass
in 10.67s (14.637s race package), compared with 41.61s before. Settled FD/goroutine counts stay
70/108 and finish at 5/2. GC-retained heap baseline/peak/final is 1,182,048/1,326,592/940,816 bytes.
This finite same-fixture comparison does not establish general throughput or long-duration soak.

The full opt-ins-off repository race suite passes (27 packages, six without tests), as do repository/
fake-peer vet, formatting and whitespace checks. Installed Claude with fake ACP passes all current
allow/deny/hook resume cases (15.86s test, 17.877s package). One separately bounded actual Kiro/Claude
allow-Bash case passes in 47.55s (48.873s package): two main ACP prompts/four HTTP requests, exact
old history in both resumed requests, two distinct successful effects, unchanged sources and joined
recorded process/group/profile/listener cleanup. No live deny/hook repeat or model retry is needed.
Logs are recorded in LIVE_KIRO_TEST_PLAN.md; none contains raw model/tool payloads or credentials.

Rebuild the local development command and freeze macos-arm64-relay-close.json. Its 100 selected
repository input paths match D87, with only internal/relay/socket.go changed. The fresh collection
contains 267 packages; all four external module versions/sums/package sets and selected native
filenames match D87, and notice bytes remain unchanged. Record the full current import/file list
without claiming a full prior Go-file-list comparison that the earlier snapshot cannot supply.
The 13,589,266-byte artifact records parent c121224 and vcs.modified=true, identifying the captured
uncommitted source bytes rather than claiming a later commit. Its 139 byte checks pass; D87 remains
an unchanged historical snapshot with its old executable retained privately. Release clearance and
the remaining interoperability/soak gates remain open.

## D97: distinguish native memory presence from exclusion policy

Continue the personal-root compatibility investigation without installing a candidate. A private
root wrapper imports the original personal CLAUDE.md only for local native context/settings
inspection. Hypothesis: original path presence can decide whether the direct root alias should be
excluded. Require initialized, correlated, bounded control replies, exact effective exclusions,
zero model/token-count requests, unchanged owned source trees and joined process/profile cleanup.
The public memory documentation describes absolute-path glob exclusions and imports; it does not
define an absent context entry as proof of exclusion. No custom glob/instruction parser is added.

The initial seven-case hypothesis test fails for empty and comment-only roots (3.98s test,
4.976s package). Both lack an original context entry without any exclusion. Plain, frontmatter and
import-only roots are present; exact and glob-excluded original roots are absent. Retain that red
evidence. A separately named eight-case counterfactual additionally shows that excluding only the
private wrapper hides both its own entry and the imported original. It passes in 3.76s with all
eight zero-inference observations and clean ownership. Absence is not a native exclusion oracle.

Also compare natural versus linked execution with the same exclusion **/client/CLAUDE.md. It matches
only the owned private alias, not the original HOME/.claude/CLAUDE.md. The natural local Messages
request includes unconditional root text and all four relative-import hops before project text;
the linked candidate loses that root and every hop while retaining personal rules/project text.
Both synthetic responses complete; the candidate leaves sources unchanged and both client groups/
profiles are gone. This 0.97s counterfactual establishes the opposite direction from D77's original-
path exclusion bypass. Merely adding exclusions for an alias cannot fix both directions.

The combined counterfactual race package passes in 6.644s, and the existing memory/control/alias/
depth/additional-directory regressions pass in 22.858s. These observations reject decision rules;
they do not grant personal-memory acceptance or prove every native solution impossible. The
production private profile, dependency selections and D96 artifact stay unchanged. Exact evidence
and command bounds are recorded in LIVE_KIRO_TEST_PLAN.md.

The final opt-ins-off interop race suite passes in 24.230s. Interop vet, formatting, whitespace
and all 139 unchanged D96 artifact byte checks pass.

## D98: distinguish new tool policy from abandoned work after native resume

D91 observes native omission of an unfinished tool pair; D92 checks new tools only after completed
history. Combine those independent cases without changing production history semantics. First
deliver one owned Bash handoff and establish its exact live PreToolUse wait with no effect/result.
Cancel the runner repeatedly, join recorded hook/client/backend/relay ownership and private state,
then check a late release marker cannot execute the old command. Only then launch a fresh native
explicit-ID resume requesting a distinct new operation under current client settings.

Factor the current-pair observer from the prior-history observer. Completed-history cases retain
their exact successful pair requirement. Interrupted-history cases require the exact old question,
measured partial response or non-completion placeholder, absent unfinished pair and exact new
question. Validate that prefix in both the new request and its result continuation. The new call
must have a fresh ID and exact separate arguments, and its result must match ID, status and any
required refusal reason. An abandoned old pair cannot satisfy the new result prerequisite.
Before delivering the new call, recheck old hook disappearance/late-release proof/no effect and
new target absence. Both current permission policy and native effects/hooks are independently checked.

Use the existing independently authored peer's interrupted/follow-up modes; no fixture expression
or production code changes. The first guard command fails to compile on a new test's missing brace;
correct it before native dispatch. New corrupt-history/order/identity/operation/result/completion
controls and existing completed/interrupted guards pass in 2.149s race package. No-text and partial-
text native histories each pass allowance, configured denial and hook veto: six cases, twelve
stages, eighteen HTTP requests, twelve ACP prompts/owners, six unexecuted old operations, two new
allowed appends and four refusals. All history/effect/cleanup/source checks pass (32.00s test,
33.282s package). Existing native completed/interrupted/policy regressions pass in 34.779s.

A separately opted-in actual Kiro/Claude case admits only two main ACP prompts, three HTTP model
requests and one new append. Its initial invocation fails during version/account preflight before
any model work (4.67s test, 4.933s package); the generic observation cannot identify the failed
command. Do not infer logout, a version change or a timeout. Subsequent read-only baseline/owned-
KIRO_HOME identity checks all pass (twelve public commands, 6.23s test / 7.566s package). Add fixed
command/exit/output-length/failure/cleanup diagnostics and a six-command allowlist to the shared
preflight. A standalone check of that exact preparation also passes, including the catalog and
private configuration cleanup, with no ACP/session/model work (10.30s / 12.227s).

Those observations establish fresh admission, not the cause of the earlier failure. They precede
the bounded model episode using the still-unused two-prompt/three-request budget. No dispatched
model work is retried, no deadline or product isolation rule is relaxed, and no authentication
state is changed. Results and the remaining evidence limits are recorded below and in the live plan.

The actual episode passes in 46.95s (48.273s race package). The first handoff reaches the exact
native hook, cancellation joins in 166ms, and late release produces no old effect. After fresh
preflight/profile/address/token/backend ownership, both resumed requests retain the original
question and all 31 bytes of measured partial text before the exact new instruction. One distinct
new call returns a matching success and completes; its append and pre/post hooks occur once.
All recorded process/group/listener/profile/policy ownership is gone and both settings sources are
unchanged. Total work stays two ACP prompts/three HTTP requests/one new effect, with no old result
invented. This establishes the allowed new operation after this interrupted history, not live
refusals, interactive approval, arbitrary crashes/effect windows, simultaneous calls or hidden
backend context restoration.

Applicable opt-ins-off race suites pass: ACP 6.023s, session 29.452s, interop 22.133s and the owned
resume hook 1.677s. No production/dependency/artifact change is required; the D96 inventory remains
current. Whole-repository/fake-peer/hook vet and formatting/whitespace checks pass.
The final interop race suite after preflight diagnostics passes in 21.439s; all 139 unchanged
development-artifact byte checks pass with release clearance still false.

## D99: observe and correct the optional effort command

The old D09 name/arguments envelope prevented the optional setting from reaching the measured
Kiro command handler. The public Kiro ACP documentation names commands/execute but does not specify
effort argument members. The public settings reference documents KIRO_ACP_RECORD_PATH. An owned
empty-tool Kiro 2.21.2/v2 terminal was run with a private KIRO_HOME, launch directory and temporary
directory. Its only input was the public slash command /effort high, with auto selected. Recording
through a private FIFO revealed `{command: "effort", args: {value: "high"}}`. The raw traffic was
bounded in memory and discarded; retained diagnostics contain only protocol names, the known input
value and cleanup facts. No model prompt was sent. The final 4.079s observation joins the group,
removes its private root and preserves original settings. This observes a request; auto's rejection
does not establish applied effort.

Sources checked 2026-09-10: https://kiro.dev/docs/cli/acp/ and
https://kiro.dev/docs/cli/reference/settings/ . No previous implementation or external source code
supplies this wire contract. The independent regression first fails against the old implementation
(d99-effort-wire-red.log, 0.864s package). Only the private effort payload changes in production;
availability, automatic-model skip, bounded rejection ledger, effective-state reset and transport
failure policies remain those of D09. Independent process fixtures verify model selection before
effort before prompt, nonfatal rejection and fatal transport corruption (1.904s/4.914s packages).

Fresh empty-agent ACP experiments independently observe an advertised effort command and one empty
argument query. Auto returns success=false; an exact advertised Sonnet 4.6 model selected through
session/set_model returns success=true. Its message lists low/medium/high/max choices. Treating that
message as current state fails the first readback experiment and is explicitly rejected.

The final control uses the production adapter to set high then low in one owned idle session, with
no prompt. Before each setting it discards already queued metadata in a bounded window. A successful
setting acknowledgement must be followed by matching-session `_kiro.dev/metadata.effort` with the
requested normalized value. Both settings pass, with two setting RPCs, two matching notifications
and no duplicate RPC for repeated identical Sync calls (7.37s test, 8.650s race package). Source
settings bytes are unchanged and process cleanup joins. Independent controls reject stale or
ambiguous values, refusal, silence, foreign-session metadata and an already queued matching value;
query/model admission, bounds and privacy controls pass in 4.016s. Metadata readback belongs only
to this test: production still requires the command's success acknowledgement and does not wait
for optional metadata. These observations do not measure provider reasoning, billed usage or
every advertised model/effort pair.

Initial terminal-recording attempts exited before any slash command under an overbroad file-size
limit. A later successful FIFO observation checked group existence too soon after signalling;
the named process check subsequently found none. The final bounded join above replaces that
incomplete cleanup claim. No raw terminal output, account values or native stderr is retained.

A separate public-only local Claude consultation was saved and read at
.cache/claude-consult/work/public-root-design-review-srsnqqpe/answer.md. It offered no newly supported
personal-memory root mechanism. Its suggested model recitation/tool-read denial checks cannot prove
memory exclusion and are not adopted. The personal CLAUDE.md compatibility gap and full remaining
alpha/release scope stay open.

Whole-repository race verification passes in all 27 tested packages (six packages have no tests),
including ACP 5.023s, session 27.307s, interop 25.991s and launcher 25.139s. Whole-repository/fake-peer
vet passes; additional observation bounds/cancellation controls pass in 3.846s. Final actual Kiro
auto-query, selected-query and setting cases pass in 21.702s, with original settings preserved and
private roots removed. The D99 executable is rebuilt and its 139 byte checks pass. Its 267 selected
packages, four external module versions/sums and selected filenames match D96. Only effort.go
changes among the same 100 repository input paths; D96's snapshot and executable are retained.
See DEPENDENCY_REVIEW.md for the artifact identity and remaining license/release limitations.
The final cleanup control preserves its private root if the recorded process group survives;
the fresh setting/metadata run passes after that guard (7.33s test, 9.351s race package), with
group disappearance established before root removal. This test-only change does not alter the build.

## D100: simplify the live plugin completion challenge without weakening its source check

D72's tool/registry/cleanup observations remain valid partial evidence, but its final answer did not
contain the required random-string concatenation. The answer was not retained; its exact cause is
still unknown. The revised test asks for VERIFIED followed by the fresh thirteen-character base32
token delivered only in the second tool's success result or matching hook-refusal reason. The full
22-byte phrase is absent from the prompt and tool results. It must occur in the final assistant
events and independently in the reconstructed current terminal screen. The 132-byte prompt avoids
asking the model to concatenate two unrelated random strings. It does not relax the exact phrase,
tool/result identities, prefix history, execution ownership, error status or lifecycle checks.

Independent allowance/refusal controls cover complete and split final output, token-only and prefix-
only responses, prompt echo, early token/phrase disclosure and a whole phrase supplied by a tool.
The first run exposes that an early bare token could precede tool delivery under the old guard
(1.055s failing package). The guard now rejects that case before exposing a tool, including when
the token spans events. All sixteen new cases and existing sequence guards pass in 1.799s.

The first native-client/fake-ACP new-phrase rehearsal fails because the independent peer still
extracts the token by halving the final marker. That rule is valid for the historical equal-length
halves but discards two token bytes for the new fixed prefix. A separate fixture regression first
fails (0.865s), then passes (1.734s) after distinguishing the two owned marker formats. This fixture
defect was introduced by the new recipe and does not explain the older D72 live answer. The original
native recipe passes its allowance/refusal pair in 14.39s; the combined initial run remains failed
(143.180s package). After the fixture fix, the new actual-Claude/fake-ACP pair passes in 12.93s
(14.347s package), with three main requests/two backend processes per case, generated/displayed
phrases, effects one/zero, unchanged sources and cleanup. Local observer controls pass in 1.302s.

The public-only local Claude consultation is saved and assessed in
.cache/claude-consult/work/public-completion-design-review-jymaj95u/answer.md. Its provenance
counterexamples are useful; its claim that deliberate refusal delivery must fail is inapplicable.
The refusal branch verifies zero execution and its matching error response. Its unsupported claims
about likely transport-versus-formatting causes are not adopted. The source boundary is unchanged.

LIVE_KIRO_TEST_PLAN.md records the finite live recipe and prerequisites. One allowance, then only
on success one hook refusal, may run after the local gates. Each retains the original three-main-
request/two-prompt/two-process bound and stops on failure. No production source or dependency changes;
the D99 artifact remains current. A pass will cover this owned plugin's data path, not arbitrary
plugin assets, semantic correctness, actual model-selected skills or the remaining release gates.

Pre-live race regressions pass ACP 5.713s, interop 24.225s and the independent peer 1.192s, with all
real opt-ins disabled. Applicable vet and formatting/whitespace checks pass. Automatic approval
review rejects the actual Kiro/Claude test command before process creation, citing missing explicit
user approval for enabling Kiro credit use and transmitting the test to an external service.
No D100 Kiro prompt or native live episode occurs. Approval is required for the prepared maximum
of one allowance plus one hook refusal, two ACP prompts per case (four total), with synthetic
questions/results and the client's default instructions/tool definitions sent to Kiro. Credit use
is provider-dependent and is not estimated here. The action remains pending; this record does not
override the rejection or claim a live pass. The remaining independent product work is unaffected.

## D101: connect account usage through an isolated non-model command

The public Kiro slash-command reference describes `/usage`; installed 2.21.2 finite help still has
no top-level usage command. An owned empty-agent ACP session advertises usage and tools. The
independently verified command envelope accepts `command: "usage", args: {}` without a model turn.
Read-only observations retain only shape, success, enum classes and counts. The 446-byte native
result contains a CREDIT entry labeled Credits, numeric used/limit, hasLimit, and empty bonus/add-on
arrays. No actual amounts, plan labels or response prose enter logs or fixtures. Sources are the
unmodified local CLI, https://kiro.dev/docs/cli/reference/slash-commands/ and
https://kiro.dev/docs/cli/acp/.

The private adapter maps only the unique CREDIT entry's reported used and conditional limit. It
ignores supplemental buckets and never calculates a total remaining balance. No aggregate meaning
is inferred for accounts with add-ons, bonuses or enterprise overages, whose nonempty private
payloads have not been observed. Independent fixtures cover unknown units, duplicate entries/keys,
invalid numbers, over-limit usage, absent limits and supplemental values without summation. Replies
are bounded at 64 KiB, breakdowns at sixteen and amounts at 1e12. Account usage stays separate from
turn metadata; no credit amount enters Anthropic provider token accounting.

Each refresh creates a fresh private profile, scratch and empty workspace. It reuses the measured
resource-suppression preparation but supplies its own zero-tool/zero-MCP agent, empty resources/hooks,
no MCP JSON inheritance and no client filesystem/terminal capabilities. Before ACP, three finite
version/helper/account checks must match the launch's HMAC account scope. The fresh session receives
no MCP declarations. At most 64 notifications/1 MiB are considered; a foreign session, MCP
initialization, malformed/duplicate command advertisement or nonempty tools result stops before
usage dispatch. Only session/new, tools and usage are sent after initialize.

The cache remains lazy, coalesces readers for 60 seconds and returns immediately. A refresh has a
15-second total deadline, including identity checks; each finite CLI check has a five-second limit.
ACP has a 128 KiB frame limit, four pending/writer slots, 64 notification slots/1 MiB and existing
finite cleanup stages. Every refresh joins its ACP process and command runner before publishing
data or removing its root. Cleanup failure preserves the root and stops this cache's further
refresh admission. Roots are direct children of the launch runtime parent, outside the model/client
startup root, so outer cleanup cannot remove a preserved usage root. Cache Close cancels and joins
once, reports owner failure to RunClient and cannot admit late readers. ACP initialization now
preserves a distinct cleanup-failure marker instead of hiding a failed join behind its initial error.

`run` owns this cache through early exits and normal shutdown. `doctor` and `models` return before
cache construction and retain their no-ACP behavior. Query/identity failure preserves the existing
model-only or last-good view; optional usage does not change development admission. Scope checks
precede the command; they do not claim an atomic snapshot across external login changes.

The independent process test initially fails its session path assertion because macOS can name the
same temporary directory through /var and /private/var. The fixture now requires directory identity
instead of string equality, preserving the requested workspace and empty-MCP checks. Normal query
and held-response cancellation then pass with all recorded groups removed. Launcher/usage/startup
race controls pass in 16.467s, including cleanup-failure latching and early exit propagation. The
prior sandboxed runtime attempt fails at loopback binding; the authorized local-process run passes.

One production-cache refresh against unmodified Kiro passes in 10.25s (13.195s race package). The
refresh itself takes 7.311s, uses one ACP process/three post-initialize RPCs and zero model prompts,
and returns normalized used/limit fields. Source settings are unchanged; the recorded ACP group
and owned files are removed. This verifies the adapter/cache path, not new native Claude status
rendering, add-on/enterprise accounts, actual billing precision or release clearance. D100's
separately rejected credit-consuming plugin experiment remains pending.

Whole-repository race verification passes in all 27 tested packages (six without tests), including
ACP 5.504s, interop 26.839s, launcher 25.982s, session 28.581s and status 1.446s. Whole-repository
and new independent-peer vet passes; formatting and whitespace checks pass. The D101 development
binary replaces dist after retaining D99. Its 141 byte checks pass; the same 267 packages and four
external dependencies remain selected. Two production files are added and five changed, with
102 repository input records. DEPENDENCY_REVIEW.md records the exact artifact and open release work.

## D102: expose unintended exclusions of the prepared personal-rules alias

Revisit the unresolved personal-memory requirement using current public documentation and owned
native-client controls. The public configuration-directory reference still relocates personal memory
and mutable settings/state together. No independently verified separate read-root interface has been
found in the examined surface; this is not a proof that every native solution is impossible. Do not
repeat the rejected D77/D80/D93/D97 adapters or weaken root semantics to call them complete.

The current memory reference documents that rules exclusions match either a symlink alias or its
target, for directory links as well as file links. D77 had verified original-target exclusions;
that does not establish fidelity for a pattern matching only the newly introduced private alias.
A strict two-run experiment sets **/client/rules/general.md in an owned user settings file. The
natural client loads the personal rule body, its relative import and four hops before project
rules. The prepared production profile drops that personal rule and all its imports, while project
rules and their four hops remain. Both local Messages requests decode, complete with the fixed
synthetic response and join the observed client group. The initial preservation test fails
(1.45s test, 2.514s race package); its log is retained, not described as a feature pass.

A separately named counterfactual repeats the alias-only case and original-target exclusion
controls in user, project and local settings. All twelve invocations match the observed defect
(6.08s test, 8.194s package). Prepared sources/imports remain unchanged and private groups/profiles
are removed. This narrows the fault to inclusion through the relocated name; it does not justify
removing or reinterpreting user exclusions. Personal rules remain partially supported, with this
additional open requirement recorded in ACCEPTANCE_SPEC.md and the user-facing README. Renaming
the private directory to evade a particular test pattern would not restore general fidelity.

The public-only local Claude consultation is saved in
.cache/claude-consult/work/public-memory-native-scope-review-dqfm0xka/answer.md and assessed in
review.md. Its both-path hypothesis matches the native observation and its narrow lack-of-known-
interface statement is useful. Its proposed approximate byte/token deltas are not used as proof;
the observer instead checks fixed synthetic marker presence, ordering and depth. No model recitation,
private-client inspection, original implementation, custom instruction/glob parser or global
configuration mutation is used. Official sources reviewed on 2026-09-10:
https://code.claude.com/docs/en/memory, https://code.claude.com/docs/en/claude-directory and
https://code.claude.com/docs/en/env-vars.

This is a newly verified compatibility defect, not a production fix or completion of personal-memory
support. Development admission is unchanged; D101's executable/dependencies remain unchanged.
The still-pending D100 credit-consuming experiment is not attempted or implicitly authorized.

Existing native rule sources, four-hop rules and original-root alias-scope controls pass together
in 12.709s. The opt-ins-off interop race suite passes in 26.279s; interop vet, formatting/whitespace
and all 141 D101 artifact checks pass. The shared helper now places a supplied test exclusion at
its requested native settings scope, rather than only moving original-target exclusions. No
production setting merge, exclusion interpretation or artifact is changed by these tests.

Re-checked on 2026-09-11 against the current public memory and settings references with the measured
2.1.267 client: personal rules and the user CLAUDE.md still live only under the configuration
directory, `autoMemoryDirectory` relocates auto memory alone, and `claudeMdExcludes` still matches
either a rule's path under `.claude/rules/` or its link target (since v2.1.239), so a pattern
matching only the private alias still excludes a rule that natural startup loads.
`CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` with `--add-dir` would load `~/.claude/rules` from
its native path, but only by granting the client file access to the whole home directory as a
working directory and loading the rules with additional-directory rather than user semantics; it is
not adopted. No product change; the gap stays recorded here, in ACCEPTANCE_SPEC.md and in README.md.

## D103: verify account usage in the native status line without a model turn

Extend the owned-terminal status observer with an optional usage cache. Existing startup, completion
and user-status scenarios keep their previous inputs. Usage episodes use the production gateway,
status helper and prepared Claude profile, with an empty tool/MCP configuration and no user prompt.
A fixed synthetic last-turn record supplies the model/effort/duration label; it is not evidence of
a newly completed model turn. Every episode retains the existing five-second polling check and
also waits for its specific current-screen condition before joining the owned terminal.

The first independent refresh blocks until the current screen actually displays the unavailable
view. Its completion supplies synthetic reported values. In the failure case, the clock advances
61 seconds only after those values render; the next client status poll admits a failing refresh.
Observer reads cannot start the initial or expired refresh ahead of that poll. The current screen
must retain both numbers with a stale marker after failure. The cancellation case holds the first
query through repeated status polls, asserts it is still refreshing before Close, then requires
joined cancellation with no invented data. Fetch counts are one/two/one respectively. Eleven
small controls reject false positives from incomplete amounts, substring matches, wrong models,
erased screens, unsupported/refreshing views, inferred remaining or missing stale labels.

All three synthetic native-UI cases pass in 27.45s (28.730s race package). The tightened next-poll
failure control and verdict controls pass in 15.127s, with the failure case taking 13.08s. The
observer's use of the current reconstructed screen prevents erased historical output from
satisfying the account-value condition. Terminal bytes stay within the existing 256 KiB memory
bound and are not written to disk; logs contain only fixed classes, booleans, counts and timings.

The separate native case uses D101's lazy production cache with its 60-second TTL, 15-second
deadline, isolated zero-tool agent and non-model command path. A bounded version/account preflight
establishes scope before the UI starts. An owned shell observer records only a main-CLI command
class and PID, then execs the unmodified pinned binary; ACP bytes are untouched. Exactly two main
version checks, two identity checks and one ACP group are recorded across preflight and refresh.
The adjacent helper is checked directly by the existing runner. Kiro source-settings existence,
mode and bounded content fingerprints must stay unchanged. The usage root is separate from the
UI root, so failed joins preserve it independently; normal completion leaves no refresh artifacts.

Kiro 2.21.2 and Claude 2.1.263 pass this composition in 14.94s (17.240s race package). Three status
requests include a 4.960-second interval. The cold model view and then the normalized used/limit
values appear on the current screen, with zero model requests or backend starts. All recorded
groups, the prepared profile and usage artifacts are removed, and source settings are unchanged.
Actual values, identities and raw private payloads are never logged. No remaining balance is
inferred. This verifies this account's observed fields, not nonempty add-on/bonus/enterprise data,
provider billing precision, every terminal layout or complete release readiness. D100's separate
credit-consuming plugin experiment remains pending and is not invoked by these tests.

Only tests and evidence change. D101's production binary, inventory, dependencies and development
admission stay unchanged. Native regressions cover all five existing-status scopes, startup-hook
ordering, the default status view, two local completion notices and the tightened held-cancellation
case; they pass together in 53.720s. The opt-ins-off interop race suite passes in 25.608s. Interop
vet, formatting/whitespace and all 141 unchanged D101 artifact checks pass with release clearance
still false. These results do not resolve the separate D102 personal-memory compatibility gaps.

## D104: overlap distinct native histories and resume each through fresh owners

Extend the existing text-restart observer with an optional two-session plan sharing an owned HOME
and project. Each session keeps its own private profile, gateway token/address, session manager and
independent ACP process. No actual Kiro binary is accepted by this plan. The existing independently
authored history peer is reused unchanged; no native transcript fixture or parser is introduced.

Each initial question contains a different generated seed. A first-response barrier records the
native session ID plus the client and ACP group leaders after a public model request and ACP event
arrive. An owned shell writes only the client PID and then execs the unmodified client. Both pairs
must be distinct and alive at release; missing, repeated, stale or cross-paired ownership cannot
establish overlap. A context cancellation releases its waiting caller, and an episode failure
releases its sibling's barriers. These are observed concurrent sessions, not an inference from
starting two test goroutines.

After each initial turn completes, the existing observer joins its listener, client, backend,
schema owner and profile and removes the stage root. A second barrier requires both initial client
and ACP groups gone before either session creates resume ownership. Explicit-ID resume then uses
fresh profiles/endpoints/tokens and fresh ACP processes; its first responses must overlap again.
Native session IDs stay distinct between sessions and unchanged across each session's restart.

Public normalized request checks require the old question/seed once, followed by the old assistant
answer and then the new question once. Any sibling seed in system or message text rejects before
backend dispatch. The generic restart observer now also checks marker ordering. Eleven controls
cover valid fresh/resumed context, foreign user/answer/system data, wrong seeds, duplicate/truncated/
reordered histories, invented markers and non-text blocks. Eight overlap controls cover the valid
case and missing/repeated/reused/dead/aborted ownership. All nineteen pass in 2.060s under race.

The actual Claude 2.1.263 concurrent test passes in 4.46s (5.903s race package): two initial and two
resumed turns, two established overlap rounds, four distinct profile/address/token sets and eight
joined recorded client/ACP groups. The shared native data has two matching transcript files among
four entries/11,970 bytes. Each transcript contains its own seed and resumed answer, no sibling
seed and no routing token. The observer bounds files to 2 MiB, the tree to 128 entries/8 MiB, and
checks only known marker/secret presence; native formats are never decoded, merged or edited.
Source user/global settings are unchanged. Raw requests, transcripts, client output and tokens are
not logged; fixed classes, counts, timings and booleans provide the evidence.

This is a finite distinct-session text check with actual clients and fake ACP. It does not prove
same-session concurrent-writer behavior, tool/checkpoint/media retention, interactive selection,
actual Kiro parallel operation or release soak. Those acceptance items remain open. No product,
dependency or frozen artifact changes; D100's pending model experiment is not attempted.

Existing native text restart and persistence/reference/ephemeral/suppression controls pass in
7.669s, including source preservation and cleanup. The opt-ins-off interop race suite passes in
28.514s, with the missing-peer control additionally exercising cancelled barrier waiting. Interop
vet, formatting/whitespace and all 141 D101 artifact checks pass; release clearance remains false.

## D105: distinguish a shared native transcript from later selected conversation context

The public sessions reference says concurrent resumes without an explicit fork share transcript
writes. That fact does not define how every retained reply enters the next model request. Consult
https://code.claude.com/docs/en/sessions#branch-a-session, reviewed 2026-09-10. Do not infer a merge
algorithm or introduce an implicit fork from this statement.

The bounded public-only Claude CLI consultation is retained at
.cache/claude-consult/work/public-shared-session-review-tn4632np/answer.md with an assessment in
review.md. It completes in one turn with 4,464 output bytes and no tool/MCP access. Its useful
distinction is retention versus selected request context. The suggested raw-request logging is
rejected: only authored marker counts/order and lifecycle facts are retained. Its private-lineage
explanation stays an unverified hypothesis and supplies no implementation input. The first matrix
has one episode per configuration/schedule; it is not a statistical stability claim.

Each episode creates one synthetic native conversation, resumes its same ID twice, and then uses
a fourth fresh client to inspect the public request's context. Every response is supplied by a
controlled text backend through the real authenticated gateway, with no ACP or external inference.
Both configurations use identical routing overlays, tool/MCP suppression and disposable HOME/project.
The reference removes only the process-level CLAUDE_CONFIG_DIR override so the unmodified CLI reads
and writes its ordinary owned configuration. The prepared arm keeps the production profile path.
The ordinary reference is an observation control, not a replacement launcher implementation.

Sequential controls fully finish and join the first writer before starting the second. Concurrent
controls hold the first reply, confirm the first pending question is retained on disk, and then
start the second writer. Both public requests must reach held response consumption with distinct
live client groups. Release the selected reply, join that client/profile/listener, confirm the other
client is still held, and release it. Only after both are joined does the readback client start.
No fixed sleep substitutes for request readiness or completion. The transcript observer checks
known marker/credential presence only, bounded to 64 entries and 2 MiB per file; native JSONL formats
are never decoded or edited. Each episode has a 60-second lifetime, 20-second client/turn bounds,
four gateway requests and at most two concurrent clients; captured outputs are bounded at 128 KiB.

Thirteen independent projection controls distinguish complete selected branches from unanswered
questions and reject missing base context, orphan/reversed pairs, missing readback input, duplicates,
wrong roles and system-marker contamination. They pass under race in 2.099s. The installed Claude
2.1.263 matrix passes all eight episodes/32 local turns in 16.02s (17.435s race package). Reference
and prepared readback marker roles/order match exactly for every schedule:

| Schedule | Later selected context after the common initial turn |
| --- | --- |
| Sequential A then B | A question/answer, then B question/answer |
| Sequential B then A | B question/answer, then A question/answer |
| Overlap; A completes before B | A question without its answer, then B question/answer |
| Overlap; B completes before A | A question/answer; B question/answer absent |

Both completed replies remain in the shared transcript in all cases. The table describes this
controlled start/completion ordering and this version; it does not establish a general last-writer
selection policy, atomicity or safe merging under other timings. In particular, an omitted reply
in the next public request is not evidence that its transcript bytes were deleted. The ordinary
reference changes its disposable global config; prepared runs preserve both source settings files.
All four recorded client groups/profiles/listeners per episode are joined, and no routing token
appears in the owned transcript. Raw native requests, outputs, transcripts and credentials are
not logged. No original user data or settings is used in either arm.

Document the concrete same-ID limitation and retain native behavior. Do not add a hidden transcript
merger, identity change or serialization based solely on these observations. Distinct native
sessions remain the verified way to keep parallel conversations independent. This is text/print
evidence, not interactive UI, tools/checkpoints, actual Kiro, long-duration retention or complete
release clearance. No production, dependency or artifact change is made; D100 stays pending.

The opt-ins-off interop race suite passes in 28.339s. Interop vet, formatting/whitespace and all
141 unchanged D101 artifact checks pass, with release clearance false. The new observer is separate
from existing restart paths; no native transcript interpretation is added to production code.

## D106: current interactive permissions after completed-tool native resume

Extend D92's independent-ACP observer with a terminal second launch. The first native print launch
completes one owned Bash append and records its exact public tool pair and native session ID.
Join its client/backend/listener/profile before creating fresh ownership. The second launch resumes
that ID with current manual permissions and a distinct owned append request. Both source policies
are fingerprinted; native history remains client-managed, without a transcript parser or fabricated
resume/result record. Initial allowance is a configured rule; only the new operation is interactive.

Before input, require the new request to retain the old successful pair, one delivered current
handoff, a distinct matching call ID/command, unchanged old effect/hook counts and no new effect.
This is public handoff evidence, not direct inspection of the session manager's private state.
The existing screen controller requires the exact current command and selected one-time Yes. It
waits 500 ms, then approves once or navigates to No, opens the reason field, verifies the authored
reason's echo and submits it. Each subsequent key requires a changed selected frame. A hook veto
receives no decision keys and must complete without an observed operation permission menu.

The result request must preserve the old pair again and contain one exact new matching result,
including the refusal flag/reason when expected. The old append and pre/post hooks remain once.
New approval appends once with both hooks once; both refusal cases leave the target and success
hook absent and require the current pre-hook once. Independent controls reject missing, repeated,
changed-session, old-ID, wrong-operation and already-returned handoffs. Auxiliary title requests
have a separate limit of two and controlled local replies; they cannot enter ACP or consume main
history evidence. No title request occurs in the three measured episodes.

The new terminal helper has a 25-second lifetime, 256 KiB capture limit and bounded current-screen
inspection. It records an owned exec wrapper's native PID/group, observes the final response marker
both in the backend and current UI, then signals that group and joins the attached terminal owner.
This is controlled process shutdown after visible completion, not keyboard-exit evidence. Source
settings remain unchanged and no project permission rule is saved. Existing outer checks remove
each profile, relay config, agent directory and listener and join recorded ACP/relay ownership.
Raw terminal, request/history contents and credentials are not logged; only fixed markers/counts/
booleans are recorded. Tests use disposable HOME/project data and never dispatch actual Kiro.

Fourteen handoff controls and a title-isolation control pass alongside existing history/permission
guards in 2.166s under race. Actual Claude 2.1.263 one-time approval passes in 9.45s (10.745s package).
Comment refusal and hook veto pass in 10.45s (11.736s package). All six initial/resumed stages retain
exact history, effect witnesses, source preservation and cleanup; new handoffs/results/completions
occur once per resumed stage. Refusal-witness controls additionally cover interactive policies.

This closes the finite fake-ACP completed-history interactive Bash check. Actual Kiro interactive
resume, unfinished tool approval, uncertain effect/acknowledgement windows, arbitrary tools and
keyboard exit remain separate gates. No production, dependency or frozen artifact changes are
made, and D100's pending external-model experiment is not attempted.

Existing actual-Claude/fake-ACP completed-tool, current-policy and interrupted-policy resume
regressions pass in 52.792s. The opt-ins-off interop race suite passes in 23.899s, including the
extended interactive refusal-witness controls. Interop vet, formatting/whitespace and all 141
unchanged D101 artifact checks pass. Release clearance remains false.

## D107: interactive new work after a delivered operation was interrupted

Combine the existing independent interrupted-history observer with D106's native terminal second
launch. Keep the first operation under a real owned PreToolUse wait, correlate its receipt with
the delivered call, cancel repeatedly and join the recorded hook/client/backend owners. Create
the late release marker and prove no original effect or successful hook occurs before resuming.
Do not retry that operation or manufacture a result when native history omits its unfinished pair.

Two initial-history forms are required: no assistant text, and an independently authored partial
response before the handoff. Both use the unchanged independent ACP peer and native hook fixture.
On fresh explicit-ID terminal resume, request a distinct Bash append with current manual policy.
The public request guard requires the exact original question and measured partial text or native
non-completion placeholder before the exact new question. Validate that prefix again in the new
result request, with a distinct call ID, matching decoded input and expected result/status/reason.

The permission observer now takes the expected history kind explicitly. Its public-delivery proof
must match that kind and the guard's successfully established abandoned-history state. Factor the
old-effect check so both pre-delivery validation and every current-screen callback require the
appropriate witness: once-only completed effects, or absent interrupted effects with joined old
hook ownership and a successful late-release check. Any failed witness latches the terminal check
unsuccessful and prevents decision input. The completed-history path retains its original meaning.
Thirty independent controls cover both expected history kinds and invalid ownership, operation,
history proof, missing delivery, repeated requests and already-returned results; with existing
history/refusal/title controls they pass in 2.200s under race.

The current operation screen still requires a selected one-time Yes and a 500 ms no-effect window.
For refusal, navigate to No, verify the authored comment in its field and submit once. A native
PreToolUse veto must complete without an operation permission menu or decision keys. Every new
result requires current final display before controlled terminal shutdown. The same 25-second
terminal and 256 KiB capture bounds apply; record only fixed marker/shape/count/Boolean observations.
No actual Kiro process, external model request, original user history or credits are involved.

With native Claude 2.1.263, no-text history approval passes in 9.93s (11.206s race package); its
comment refusal and hook veto pass in 11.82s (13.185s package). Partial-text history passes all
three in 17.53s (18.830s package). Six episodes/twelve stages produce eighteen main HTTP requests
and twelve independent ACP prompts/owners. All six original operations stay unexecuted, while two
new appends execute once and four new refusals have no target or success hook. Both resumed
requests retain the required old history. Source policies and project permission-rule absence
remain intact. Recorded original hook/client, resumed client/terminal and ACP/relay ownership is
joined; profiles, listeners and private launch artifacts are removed. New hook receipts are checked
separately. No auxiliary title request is observed.

This adds interactive permission evidence for a new operation after an abandoned one. It does
not establish retry of unfinished work, arbitrary effect/acknowledgement crash windows, actual
Kiro interactive resume, bare refusal or keyboard exit. Production, dependencies, launch admission
and D101's artifact stay unchanged. D100's separately pending model experiment is not dispatched.

Existing completed-history interactive and interrupted-history configured-policy regressions pass
in 47.631s. The opt-ins-off interop race suite passes in 24.430s. Interop vet, formatting/whitespace
and all 141 unchanged D101 artifact checks pass; release clearance remains false.

## D108: preserve personal output styles at native user scope

The private client configuration copied a user's outputStyle setting but omitted the corresponding
personal output-styles directory. An initial failing snapshot test establishes the missing source
asset. A separate installed-client comparison reproduces the effect: ordinary Claude includes the
selected authored style marker in its system instructions, while the prepared client completes the
same local text turn successfully without that marker. Successful completion alone therefore does
not demonstrate environment preservation.

Add output-styles to the existing personal asset snapshot. The complete asset set shares the existing
entry, depth, per-file and total-byte limits and source validation. Copy owned regular files privately
with exact bytes and executable owner bits; reject unsafe links, special files and writable sources.
Do not parse style frontmatter or translate settings. Claude resolves the selected style and its
scope. Existing private profiles retain their copy; a later launch sees source changes. This adds no
dependency and does not change tool execution, Kiro isolation or development admission.

The [public output-style documentation](https://code.claude.com/docs/en/output-styles) identifies
personal/project files and the outputStyle and keep-coding-instructions fields. A public-only
bounded Claude CLI consultation is saved, read and
assessed locally. Adopt its role-marker and native-reference comparisons, but reject saving raw
model requests or unrestricted metadata. The observation uses fresh single-turn histories: it does
not assume that user-message instructions disappear from subsequent full-history requests. Neither
the documentation nor the consultation substitutes for an observed native result.

Six scenarios each run natural/prepared controls with Claude 2.1.263 and a local authenticated text
backend: personal selection, the same style with a keep-coding declaration, same-name project
precedence, a project selection, a local selection and a local Default override. The reference
removes only the process configuration-directory override from the same routing overlay. Each arm
uses an owned HOME/project, fresh credentials/profile and one request; no Kiro process or tool effect
occurs. Two native client tool declarations are admitted only to observe the coding-style context.
The MCP argument is followed by an option before the positional question, because that argument is
variadic in the measured client. A preliminary startup failure is distinct from the reproduced
missing-style defect.

Retain only authored marker counts, system sizes/counts, in-memory selected-block digests and fixed
lifecycle facts. Require one selected style marker in one system block (none for Default), exact
same-case native block equality and project memory once in user content. Eight independent controls
reject misplaced or duplicated instructions. Require successful fixed completion, unchanged source
style/settings/project inputs, removed profile/listener and joined recorded client PID/group. The
ordinary native reference can modify disposable native cache/global metadata; prepared execution
must additionally preserve the entire source configuration tree and global state. Do not promote
the reference path to a production launcher workaround.

The keep-coding declaration does not change the observed system size or selected block in this
native reference, with either no tools or the two admitted declarations. An initial assertion that
it must increase coding-instruction size is rejected. Preserve the field bytes and compare actual
reference behavior without defining native coding prose or claiming a general client defect. This
test establishes initial selected instructions, not the model's coding behavior or later turns.

After the production fix, twelve native cells pass in 5.79s (7.873s race package). The pre-fix
missing-style comparison fails in 2.323s. Eight role guards pass in 2.029s, and the asset controls
including twelve new source-safety cases pass in 6.460s. Personal-root memory and private-alias rule
exclusion fidelity remain incomplete. Interactive switching, managed/plugin styles, actual Kiro
style behavior and broader live/release gates remain separate work.

Existing native personal-asset/rule source, path-character and import-depth regressions pass in
26.045s. All 27 tested packages pass whole-repository offline race tests; six packages have no tests.
Whole-repository vet, formatting/whitespace and all 141 rebuilt D108 artifact checks pass. The new
output-styles inventory records unchanged dependency selections and only one changed production
input. D101's executable is retained before atomic development-artifact replacement. Release
clearance remains false.

## D109: restore tool-result images as native ACP content

Fresh-session projection previously recognized only top-level images. It serialized a historical
tool_result containing an image into JSON text, so a restarted conversation could retain its image
in the client but omit native media at the backend. The initial independent projection regression
fails in 0.928s. An actual Claude 2.1.263 comparison then proves both halves: its initial Read returns
an image successfully through the real gateway/relay, while a fresh explicit-ID resume still sends
that result over HTTP but the independent ACP peer receives zero image parts. The source PNG is
removed between stages, and Read hooks remain once. This failing experiment takes 6.53s (6.832s
race package); source settings and recorded cleanup already pass.

Validate inline base64 images within result content before fresh projection, using D19's existing
MIME/header/dimension and per-part limits. Apply the image capability, media count and total-byte
limits to the combined top-level and result media. Keep the request and its history hashing input
unchanged. Plain text projection rejects supported result images rather than silently flattening
them. Other result content retains the existing opaque JSON fallback without fetching URLs; nested
documents gain no new native support. A proven delta excludes committed images, as before.

For a result containing native images, emit a JSON tool_result marker with the original tool_use_id,
is_error and content_blocks count. Each tool_result_content marker retains the same ID and an
ordered content_index. Text/opaque blocks stay nested JSON content; an image placeholder identifies
the immediately following ACP image. Finish with a matching tool_result_end marker. These are prompt
conventions inside public ACP text/image parts, not new protocol methods. Preserve result provenance
and untrusted content boundaries; do not turn result text into standalone user instructions. The
final encoded ACP frame limit still applies before dispatch. Production only validates image
headers; full pixel decoding occurs in the independent fixture, not the proxy.

The [public Anthropic result contract](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)
permits image content in a matching client result; the [public MCP image format](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
defines image data and MIME fields. A public-only bounded Claude CLI consultation is saved, read
and assessed. Adopt exact call/status, decoded-pixel and encoded-byte checks and source removal
before resume. Reject its suggestion of a different native ID for resume and its text-length
heuristics; neither proves this conversation's media preservation. No private client source or
previous implementation is consulted, and no repository data is sent for that advice.

The native test generates its own 12-by-9 PNG and permission/hook policy in a disposable HOME/project.
An independent ACP peer requests exactly one Read through MCP; the unmodified client performs it.
Check one matching successful result with the original pixels and bytes at HTTP and MCP, then a
fixed completion. Join the first client/backend/relay and remove its temporary profile/listener
before deleting the source image. Resume the same native ID with new profile/credentials/backend.
Require the exact prior call/result and image bytes in public HTTP history, followed by the new
question. The new ACP peer separately verifies call ID, error state, indexed result boundaries and
one native image, without requesting a tool. Both native hook receipts remain exactly one. The
client owns transcript writing; the test never parses or edits its transcript formats.

The client runner has a 30-second timeout and 128 KiB output cap; each ACP peer has a 25-second
lifetime and 1 MiB input-frame cap. The test has a 90-second overall deadline. Observations retain
fixed counts and in-memory digests, never raw requests, tool outputs or image bytes in logs. Source
settings/global state stay unchanged; both recorded native client and ACP groups plus relay PIDs
are gone, and profiles/listeners are removed. The image remains absent after resume.

After the fix, the native episode passes in 5.77s (7.522s package). Twelve independent request
guards pass in 2.108s, rejecting absent/text-only/changed images, wrong calls/status/roles, duplicates,
wrong order/session and system-marker contamination. Projection result order/ownership, missing
capability, malformed base64/MIME/header/dimensions, combined count/bytes and opaque fallback pass
in 11.100s. Existing native text and completed Write/Bash history controls pass with the final image
case in 18.465s. These are finite native PNG transport/retention observations. Actual Kiro image
interpretation, other native image formats, media sidecars and broader alpha/release gates remain
open. Development admission and dependencies are unchanged.

## D110: admit compatible client builds by major version and report measurement state

The installed Claude Code client updated itself from the measured 2.1.263 to 2.1.267 on this host,
observed 2026-09-10. The exact-equality check in the `client_version` phase then rejected every
launch, and both `doctor` and `run` reported an unsupported client. ACCEPTANCE_SPEC.md section I,
D24 and D62 pin the client exactly and reject other versions without a trust override; D59 is the
precedent for moving a pin explicitly after fresh evidence. The user directed a different policy
for now: admit any client build whose major version equals the measured pin's. This decision
records that deviation, its bounds and its risk, rather than treating the admitted build as verified.

`SupportedClientVersion` remains the measured build behind every recorded installed-client result.
Startup parses the `--version` output as a bounded dotted decimal followed by the client's name,
admits it only when its major component equals the measured pin's, and records the detected version.
`StartupReport.ClientVersion` carries the detected build; the new `client_version_measured` Boolean
is true only for the exact measured build, and `doctor` prints
`Claude Code: <version> (unmeasured; measured 2.1.263)` for any other admitted build. The client
profile accepts the detected version under the same rule. Malformed, oversized, prerelease-suffixed
or different-major outputs still reject before any finite Kiro command runs.

The installed-client evidence gates in internal/interop used the same exact-equality check with
`t.Fatal`, so an admitted but unmeasured client made every opt-in control fail rather than skip.
Those nineteen gates now use the shared admission parser, and the four controls that logged the
pin log the observed version instead. Evidence collected on an unmeasured build must be recorded
with that observed version; it does not retroactively become 2.1.263 evidence and does not make
the new build measured. No test bound, deadline, request budget or product isolation rule changes.

Risk and limits: a later 2.x client may change request shapes, onboarding, hooks or assets in ways
the recorded evidence does not cover, and this policy admits it without new measurement. The
measured combination in README.md and ACCEPTANCE_SPEC.md remains Kiro 2.21.2/v2 with Claude 2.1.263.
A D59-style explicit migration of the measured pin, with a fresh regression pass on the new build,
remains the way to make that build measured (D113 later did this for 2.1.267). Kiro preflight keeps
its exact main/helper pin. No dependency changes.

The independent parser/admission cases pass with the launcher suite (31.490s race package) and the
command suite, including the new unmeasured-render case (4.726s). With the installed 2.1.267 client,
TestClaudeClientGatewayContract passes in 0.67s and D111's MCP scope control passes in 7.55s
(10.370s race package), so the opt-in pipeline is open again on the admitted build; both log the
observed version. The whole-repository opt-ins-off race suite passes every tested package (interop
28.069s, launcher 27.673s, session 28.468s); whole-repository vet and whitespace checks pass. Logs
under `.cache/history-review/`: `d110-all-race.log`, `d110-all-vet.log` and
`d111-native-mcp-breadcrumb.log`. The rebuilt development artifact and its `client-version`
inventory are recorded in DEPENDENCY_REVIEW.md.

## D111: exclude the client's connector breadcrumb from the MCP projection

With the client admitted under D110, the same host's launch stopped at client profile preparation.
D64's projection of `HOME/.claude.json` rejects any unrecognized key whose name contains "mcp" so
that a possibly restrictive policy is never silently dropped. The installed 2.1.267 client writes a
top-level `claudeAiMcpEverConnected` key: a JSON array of strings naming claude.ai connectors the
account has connected at some point (observed 2026-09-10; the values are not recorded). It declares
no server, contains no enable/disable decision and grants no execution authority, so omitting it
cannot broaden what the private client can do. Copying it would move unrelated account state into
the private root, which D64 excludes.

The key is now a reviewed exclusion in the projection for both the global and per-project objects.
Every other unrecognized MCP-related key still rejects; the pinned-mapping rule is unchanged. The
public MCP and settings references cited by D64 do not document this key, so the exclusion rests on
the observed shape and on the absence of any declaration or decision in it. Remote claude.ai
connectors and their OAuth state remain separate preservation work, as D64 records; this decision
does not claim that the private profile reproduces a connected claude.ai connector.

The unit regression seeds the key beside real declarations and requires the projection to keep
mcpServers and disabledMcpServers while omitting it; the existing `futureMcpPolicy` rejection
control stays. The installed-client MCP scope control now seeds the key into the owned global file
before its fourteen natural/prepared cases. All fourteen pass with Claude 2.1.267 (7.55s test,
10.370s race package): the native user/local/project scopes still connect in fresh prepared
profiles, disable/re-enable and precedence behave as before, prepared runs leave the source bytes
unchanged, the natural client retains the seeded key, and a profile prepared after seeding keeps
`mcpServers` while omitting the breadcrumb (`projection_excludes_breadcrumb=true`). No tool is
called and no Kiro model request is made. This verifies exclusion with preserved scopes; it does
not observe any client UI that may consult the key.

## D112: keep the measured client's unknown-model behavior through the prepared environment

The installed 2.1.267 client, launched through `run` on 2026-09-10, printed a notice that the launch
alias "isn't described by this version's model catalog", offered `behavesAs` on a `modelPicker` row,
`modelOverrides` or a `[1m]` suffix, and said auto-compact would keep the session within an assumed
200k-token window until then. No recorded observation of the measured 2.1.263 client shows such a
notice. Product model IDs are deliberately absent from the client's built-in catalog (D08), so no
client version can describe them. This is the first launcher-visible difference of an admitted
unmeasured build under D110.

The notice's own text names `CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1` as restoring the
previous wait-for-the-API behavior. The public environment-variable reference does not document it
(checked 2026-09-10); the public model-configuration reference documents `modelPicker`,
`modelOverrides` and `[1m]` but not `behavesAs`, and says to append `[1m]` only when the underlying
model supports 1M context. Mapping a Kiro model, in particular `auto`, to a client-known Claude model
or to a 1M window would assert an equivalence this project has not measured, and a suffix would also
change the alias that the catalog reverses (D08). The prepared client environment therefore sets the
opt-out through the existing host environment and settings overlay (D24). It restores the measured
build's behavior only; it does not describe the model, change routing, use the CLI catalog's
context-window field (D30) or claim any window size.

Black-box observations, all with the unmodified 2.1.267 client and no Kiro model request:

- In print mode the notice goes to stderr beside a separate `[claude-code:unrecognized_model]`
  diagnostic. With `--output-format json` the client omits the human notice and keeps only that
  diagnostic; the D110 controls use JSON output, which is why they did not see it. With the opt-out
  the notice disappears and the diagnostic remains. Gateway model discovery does not remove it.
- `TestClaudeUnknownModelWindowNotice` runs four print arms with text output against a synthetic
  local gateway that lists the alias. The unmodified client without the opt-out emits the notice
  (positive control); the same arm with JSON output hides it; the same arm with the opt-out and the
  prepared profile emit none. All four complete the fixture and the source settings stay unchanged
  (1.75s test, 3.744s race package). On the measured build an absent positive control is logged and
  the suppression assertions are not exercised.
- An expect-driven terminal observation of the compiled `run` reproduces the interactive notice
  with the D110/D111 artifact, which lacks the opt-out, and shows none with a development build
  carrying it. Both runs pass the client's onboarding dialogs and exit through Ctrl+D with no
  surviving proxy group. The frozen D110–D112 artifact repeats the opt-out observation with no
  notice, a clean exit and no surviving proxy group. The procedure is retained as
  `d112-pty-observe.exp` with fixed facts only; no terminal capture is kept.

The launcher unit test requires the opt-out in the prepared environment. Existing D110 controls are
unchanged. This does not make 2.1.267 measured (D113 later did) and does not change compaction
behavior on the measured build. Logs under `.cache/history-review/`: `d112-native-unknown-model.log`,
`d112-pty-observation.log`, `d112-all-race.log` and `d112-all-vet.log`.

## D113: measure Claude Code 2.1.267 and move the measured client pin

D110 admitted the self-updated 2.1.267 client by major version as a directed exception and named a
D59-style explicit migration, with a fresh regression pass on the new build, as the way to make a
build measured. This decision performs that migration on 2026-09-11: `SupportedClientVersion` is
2.1.267, `doctor` reports the installed client as measured, and a 2.1.263 client would now be
admitted as unmeasured under D110's rule. The regression is the complete installed-client control set
in internal/interop (68 `TestClaude*`/`TestCompiledRun*` functions) with the unmodified 2.1.267
client, local fixtures, independent ACP peers and the Kiro credit opt-in off; the finite Kiro
commands those controls use start no ACP session. The credit-consuming live episodes recorded for
2.1.263 were not rerun and remain historical evidence for that build. The only production change is
the constant; the major-only admission rule, Kiro's exact main/helper pin and the dependency set are
unchanged.

Three behavioral differences from 2.1.263 were found and measured with independent witnesses before
the pin moved:

- Interrupted native history. When a held tool is interrupted, 2.1.263 omitted the unfinished tool
  pair from the resumed request (the abandoned form of D57/D98). 2.1.267 may instead retain the
  pair: the `tool_use` block stays, its `tool_result` carries `is_error` with the fixed text
  `[Request interrupted by user for tool use]`, and a fixed user text `Continue from where you left
  off.` follows in the same message before the same non-completion placeholder (the retained form).
  The HTTP-side guard and the independent ACP peer now each classify the measured form, must agree,
  and reject any other: a successful result, foreign ids, other notice text, a missing or moved
  continuation, a missing placeholder, a repeated call or a split pair. Neither form is a successful
  result; the effect, hook and cleanup witnesses of D57/D98 are unchanged, and the controls log the
  form beside their existing facts. ACCEPTANCE_SPEC.md section F records the second form.
- Unknown-model notice. The D112 control's positive arm is now the measured build itself, so its
  suppression assertions are exercised on the measured build rather than logged as absent.
- `/model` picker. 2.1.267 renders a scrolling window with `↑ N.`/`… +N models` indicators, an
  effort row and the footer `Enter to set as default · s to use this session only · Esc to cancel`,
  and warns that both `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_API_KEY` are set (D24 sets both; the
  warning is recorded here, not changed). Two observer defects surfaced under this picker, both in
  test code. The client emits the charset designation `ESC ( B` while re-asserting mouse reporting
  mid-session; the bounded screen reconstructor did not parse two-byte intermediate escapes and
  painted a literal `B` over the selection-glyph cell, which the client's differential renderer
  never repaints, so the observer lost the selection and stalled. The reconstructor now treats
  intermediate-byte escapes and DCS/APC/PM/SOS strings as non-text. Separately, a picker frame
  arrives across several terminal writes; acting on a partially painted row once double-moved the
  selection and confirmed `Opus 5 (1M context)` instead of the target. The observer now acts only on
  a frame unchanged for one idle observation (100ms), fails immediately when a confirmation names a
  different row, and bounds the confirmation wait at 15s with a bounded screen excerpt. The D85
  requirements are unchanged; Enter now also sets the client's default for new sessions, which the
  control still requires to leave the source settings unchanged and which the disposed private
  profile does not carry forward.

A test-host requirement also surfaced: private readers reject group/other-readable files, so a
control that writes an owned fixture with default permissions fails under a permissive umask for a
reason unrelated to the client. The regression and its reruns use `umask 077`, as the whole-repository
suites already did; LIVE_KIRO_TEST_PLAN.md records the requirement.

With the observer fixes, `TestCompiledRunModelSelectionWithFakeACP` passes four consecutive
whole-test repetitions (12.27–13.16s; 3 actions unchanged, 2 for the switch, 26 for the wide switch
with all 19 catalog rows focused and no reversal). The complete batch of 68 installed-client
controls passes in 551.945s package time with no Kiro model request; the interrupted-history
controls log `interrupted_history_form` abandoned in seven cases and retained in seven, each matched
by the independent peer's classification. The whole-repository opt-ins-off race suite (interop
29.730s, launcher 34.289s, session 30.885s) and vet pass on the final code. Logs under
`.cache/history-review/`: `d113-claude-regression-clean.log`, `d113-model-selection-settled.log`,
`d113-picker-glyph-diagnostic.log`, `d113-all-race-2.log` and `d113-all-vet-2.log`; the earlier
`d113-*` logs record the diagnosis, including the batch that failed only on the picker observer. The
rebuilt development artifact and its `measured-client` inventory are recorded in
DEPENDENCY_REVIEW.md and installed. Its `client_version` phase admits the installed 2.1.267 in 18ms,
but the doctor summary line was not observed on this host: the Kiro main/helper updated themselves
to 2.21.3 on 2026-09-11, and D59's exact 2.21.2 pin rejects them at `login_check` for the D112 and
D113 artifacts alike. The unit test covers the measured rendering; moving the Kiro pin is a separate
D59-style decision.

Risk and limits: this measures the controls in this repository on 2.1.267. It does not rerun the
credit-consuming live Kiro episodes, does not observe what the client persists inside the private
profile after Enter in the picker, and does not remove the both-auth-variables warning or the
per-launch onboarding dialogs, which remain open. A later 2.x build is still admitted unmeasured under
D110 until it is measured the same way.

## D114: admit Kiro builds by major version and measure 2.21.3

The installed Kiro main and helper updated themselves from the measured 2.21.2 to 2.21.3 on
2026-09-11 (application bundle timestamp 03:17), and D59's exact pin then rejected every `doctor`
and `run` at `login_check`, for the D112 and D113 artifacts alike. The user directed the same policy
D110 applies to the client: admit a Kiro installation whose major version equals the measured pin's,
and move the measured pin to the installed build. This decision records that deviation from
ACCEPTANCE_SPEC.md section I, its bounds and its risk, rather than treating an admitted build as
verified.

Preflight parses each `--version` output as `<name> <dotted decimal>` with the client's bounded
parser and admits the pair only when both report the same build and that build's major component
equals `SupportedKiroVersion`'s. `KiroInfo.Version` carries the detected build, the startup report
adds `kiro_version_measured`, and `doctor` prints `Kiro: <version> (unmeasured; measured 2.21.3)`
for an admitted build that is not the measured one. The development execution policy and the private
usage command mapping (D59–D61, D101) accept the same-major build and run unchanged; their digest
string names the measured 2.21.3 build, and the catalog cache identity still carries the detected
version, so builds never share cached state. Mismatched main/helper pairs, other majors, prerelease
suffixes and malformed output reject before identity lookup, as before.

The measured pin moves from 2.21.2 to 2.21.3 in the D59 manner. With the installed 2.21.3 pair and
the Kiro credit opt-in off, the finite read-only controls pass: 39 controls with 18 live controls
skipped, 307.128s package time. They cover the preflight surface, login and catalog adapters, agent
validation, ACP initialization without a prompt, owned-HOME identity and settings discovery, MCP
writer and file inventories, agent-directory selection, the pinned read-only tool inventory with
context, resource and skill variants, and the effort command queries. The live credit-consuming
episodes recorded for 2.21.2 were not rerun and remain historical evidence for that build. The
compiled-command controls with the independent Kiro fixture, which now reports 2.21.3, pass: six
controls, 58.483s package time. The launcher and command suites gain same-major admission,
mismatched-pair rejection, unmeasured startup reporting and unmeasured doctor rendering cases
(launcher 34.559s, command 4.407s); the whole-repository opt-ins-off race suite (interop 29.939s,
launcher 33.494s, session 29.155s) and vet pass.

The finite checks measured one behavioral difference from 2.21.2: 2.21.3 keeps login under the
account HOME (`Library/Application Support/kiro-cli/data.sqlite3`). From a synthetic HOME, `whoami
--format json` prints `{"account":null}` and exits 1, the main entry point's `agent list` prints a
login diagnostic, and `settings` reads and writes fail with a missing-file diagnostic even though
the owned `KIRO_HOME/settings/cli.json` is created; the helper still lists owned agents with the
selected search root. The product's execution configuration, the account HOME with a private
KIRO_HOME, keeps working: whoami, agent listing (global root = the private KIRO_HOME) and settings
set/get succeed, and the account-HOME controls pass unchanged. The five synthetic-HOME controls now
require the measured login-bound diagnostics instead of the 2.21.2 continuity, and D26's choice of
HOME as the account scope is confirmed rather than weakened. One identity-stage deadline (5s)
occurred once in a first batch while the user's own Kiro session was active; it did not recur in two
standalone reruns or in the recorded batch, whose identity stages finish in about 2s.

The Kiro interop gates that compared `--version` output with the exact pin now use the shared
admission parser and log the observed version, so an admitted unmeasured build is recorded with its
own version instead of failing or being mislabeled; the read-only catalog control logs the measured
pin as `measured_version`. The inventory variants identify the measured build through the pin
constant.

Risk and limits: a later 2.x Kiro may change ACP negotiation, tool inventory, resource handling or
CLI output in ways the recorded evidence does not cover, and this policy admits it without new
measurement and runs the measured execution policy on it. Evidence gathered on an admitted build
must be recorded with its observed version and does not make that build measured; a D114-style
migration with fresh finite checks remains the way to do that. No dependency changes. Logs under
`.cache/history-review/`: `d114-kiro-finite.log`, `d114-compiled-run.log`, `d114-all-race.log` and
`d114-all-vet.log`. The rebuilt development artifact and its `measured-kiro` inventory are recorded
in DEPENDENCY_REVIEW.md and installed; its doctor on this host reports login and execution policy
verified, launch available, and Kiro 2.21.3 and Claude Code 2.1.267 both measured (login_check
2187ms, model_catalog 2157ms), which closes the D113 note about the unobserved doctor summary.

## D115: launch without the client's per-launch onboarding dialogs

Every interactive `run` showed the client's first-run dialogs: the theme picker, then the approval
prompt for the ephemeral API key with "No (recommended)" selected, then the security notes. The
private profile is fresh on every launch and the model token is fresh on every launch, so the client
never saw completed onboarding or an approved key; the user's own global file records four rejected
keys from earlier launches and no approved one. The measured 2.1.267 build also warned that both
`ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_API_KEY` were set (D113). This decision removes the dialogs at
their causes rather than answering them.

The public environment reference (checked 2026-09-11) documents `ANTHROPIC_API_KEY` as the
`X-Api-Key` value that, in interactive mode, is approved once before it overrides a subscription,
and `ANTHROPIC_AUTH_TOKEN` as the `Authorization: Bearer` value with no approval step. D24 set both
"supported auth variables"; the gateway accepts either header and requires them to agree when both
are present. The prepared client environment now sets only the Bearer token, which the client's own
warning names as the way to use it. Public client documentation describes `~/.claude.json` as app
state holding theme, per-project trust and UI toggles; it does not name onboarding keys. Bounded
terminal observations of the unmodified client with synthetic tokens and a synthetic HOME measured
them: with `hasCompletedOnboarding: true` in the config file the theme picker and security notes do
not appear, `theme` alone does not suppress them, and with the Bearer token alone the approval
prompt does not appear; only the documented folder-trust dialog remained, which the projection
already carries per project (D64). The projection therefore adds one reviewed global key, the
Boolean `hasCompletedOnboarding`, still rejects a malformed value and drops a per-project
occurrence. A `theme` record seeded in the global file was measured as removed by the natural
client's next write; the public settings reference places the theme in user settings, which the D24
overlay already carries, so the global `theme` record and the client's key-approval record are not
copied.

Witnesses, all with the unmodified 2.1.267 client and no Kiro model request: the launcher unit tests
require the Bearer token without an API key in the prepared environment and the projected onboarding
flag beside the native MCP scopes, with a malformed flag rejected and a global or per-project theme
record not copied; the independent client fixture requires the same environment. The
installed-client MCP scope control (D64/D111) seeds the flag and a theme record at the source and
requires the fresh projection to carry the flag while excluding the breadcrumb and the theme. The
status-line terminal control now supplies trust and completed onboarding through the owned global
file instead of overwriting the private file, and requires zero onboarding answers, no theme or
API-key screen text and no both-variables warning before the status line is visible (status control
7.13s, projection control 7.77s). The shared onboarding helper of the terminal controls now proceeds
from the theme screen to the security notes when no key approval appears, so controls that seed only
project trust still complete; five plugin controls stalled at that step before the change. The
complete installed-client batch passes (68 controls, 580.273s package time, 11 terminal observations
with zero onboarding answers); launcher and command suites (launcher 28.329s, command 4.008s), the
whole-repository opt-ins-off race suite (interop 30.144s, launcher 35.328s, session 30.404s) and vet
pass.

Risk and limits: a launch from a HOME whose global file lacks `hasCompletedOnboarding` still shows
the theme picker once per launch, and the client may still show its documented trust dialog for an
untrusted project; neither is answered by the product (D118 later records a yes answer to the trust
dialog so it is asked once per project). The Bearer-only environment is measured on the 2.1.267
client; an admitted unmeasured build could route or approve differently. Logs under
`.cache/history-review/`: `d115-onboarding-arms.log` and `d115-onboarding-keys.log` (the bounded
terminal measurements), `d115-controls-first.log` (the first control run, whose projection kept the
flag but not the seeded theme), `d115-plugin-stall.log`, `d115-plugin-after-helper.log`,
`d115-claude-regression.log`, `d115-all-race.log` and `d115-all-vet.log`. The rebuilt development
artifact and its `onboarding` inventory are recorded in DEPENDENCY_REVIEW.md and installed. An
expect-driven foreground `run` of the installed artifact from a fresh project directory with the
user's own HOME shows the client banner and the launch alias with no theme, key-approval,
security-notes, trust, both-variables or unknown-model text, and exits through Ctrl+D with no
surviving proxy or Kiro process; the procedure is retained as `d115-pty-observe.exp` with fixed
facts only, and no terminal capture is kept.


## D116: verify a new question after keyboard interruption of a held tool

D57 verified one new question after a silent permission refusal, whose request carries matching
`is_error` results before the new text, and D84 verified one new question after streamed Ctrl+C with
no tool in flight. Ctrl+C while the client holds a tool through its PreToolUse hook, followed by a
new question in the same client session, had no control; D113 deferred it while measuring the two
restart-path representations of an interrupted pair. This decision adds that control to the
compiled-command terminal observer with the independent Kiro fixture and records what the measured
2.1.267 client sends. It changes no production code; the D115 artifact remains current.

The new `held-hook-followup` mode reuses the held-hook prompt and hook fixtures: once the owned
PreToolUse hook has held the Read call for 500ms with one relay call and no result, the observer
sends Ctrl+C. The completed HTTP tool handoff leaves nothing for the client to cancel, so the
observer waits only for the interrupted hook and the returned prompt, then authorizes and types the
D84 follow-up question. The independent peer now records fixed counts of the historical tool blocks
each follow-up prompt carries (`tool_use`, `tool_result`, `is_error`, the fixed interruption text,
the fixed continuation line and the non-completion placeholder) beside its existing old/new
instruction facts.

Measured with the unmodified 2.1.267 client: the client interrupts the held hook, no result reaches
the relay and no PostToolUse hook runs; the follow-up prompt's projection carries the old `tool_use`
block and the fixed text `[Request interrupted by user for tool use]` with no `tool_result`, no
continuation line and no placeholder (`tu=1 tr=0 ef=0 ir=1 ct=0 ph=0`), a third representation
distinct from the two restart forms of D113. The product retires the old prompt with one ACP cancel,
answers the new question from a fresh ACP group absent from every previously observed group,
delivers no manufactured tool result, keeps the original client, proxy, profile and gateway address
in use, never shows the Read fixture content, and joins keyboard exit and cleanup. The control
requires each of those facts and rejects any historical `tool_result` without `is_error`. It passes
three consecutive runs of 4.04–4.63s (13.008s package); the neighbouring keyboard, held-hook,
interrupted-follow-up and native-history compiled-command controls pass unchanged (37.082s package);
the interop race package (28.270s) and vet pass. Logs under `.cache/history-review/`:
`d116-held-followup.log` and `d116-compiled-run.log`.

Limits: this observes the fake ACP peer, not actual Kiro; it does not establish tool-result
recovery, history equivalence or persisted restart/resume, and the same-session representation is
recorded for 2.1.267 only. An admitted unmeasured client build may represent the interruption
differently, which the recorded counts would show.

## D117: measure the logged-out Kiro boundary and verify the graceful login fallback

The internal-alpha stage lists authentication expiry, and the clean-machine checklist requires a
startup login instruction when logged out and a graceful assistant fallback when the login is lost
during a session. D04's injected classifier and the gateway's login text completion implement the
fallback, but no control had observed the real CLI's logged-out behavior or the fallback in the
foreground client. The user asked for a synthetic ("pseudo") reproduction rather than logging the
account out: the measured 2.21.3 build keeps its login under the account HOME (D114), so a synthetic
HOME reproduces the logged-out state without changing the account, copying a credential or consuming
credits. It does not reproduce a token that expires while a process is running; that boundary
remains unmeasured and is emulated from the measured one.

Two probes run the unmodified 2.21.3 executable from a synthetic HOME after `whoami` reports no
account. Started through the product's ACP client with the injected classifier, the logged-out
process fails at `initialize` and the failure is classified as authentication expiry. Started raw
with one initialize request, it writes nothing on stdout, prints one stderr line that the classifier
recognizes (it names the login command), and exits with status 1 within about two seconds; no
JSON-RPC error is produced. Both probes pass (two runs each, 21.805s package). The startup path
already reports `Kiro login could not be verified; run kiro-cli login and retry` from the same
`login_check` phase (D26, D114).

The compiled-command terminal observer gains an `auth-expiry-followup` mode. One ordinary question
completes through the independent Kiro fixture; the observer then authorizes and types the follow-up
question, and the fixture reproduces the measured boundary on it: one stderr line carrying the login
instruction, nothing on stdout, exit status 1, recorded as `acp-auth-exit`. With the unmodified
2.1.267 client the gateway's `Kiro authentication expired. Run \`kiro-cli login\` and retry this
request.` text appears in the client as a normal completion, no API-error or 502 text appears, the
original client and proxy stay live until the confirmed keyboard exit, the follow-up prompt has no
text update or end, and the first turn's single end_turn is unchanged. The observer then marks the
login as restored, and the same client session carries one more ordinary question: the fixture
answers it from a fresh ACP group absent from every previously observed group, the answer is visible
in the client, the original client, proxy, profile and gateway address remain, one title prompt is
used in all, and cleanup joins. The session is therefore usable again after the login is restored,
the situation the user reported as failing in an earlier build. A second mode,
`held-hook-auth-expiry`, loses the login while the client holds a relayed Read call through its
PreToolUse hook: the fixture exits at the measured boundary on the parent's auth-cut marker while
its relay call is waiting, the observer releases the hook, the client's tool result finds no relay
and its request receives the same login completion (one login message, no API-error text), and the
recovery question is answered from a fresh ACP group at the first attempt; the pending outcome did
not report the expiry a second time (three consecutive runs of 3.69–4.36s (12.563s package)). It
passes three consecutive runs of 9.07–9.26s (27.719s package); the neighbouring keyboard,
interrupted-follow-up, held-hook keyboard and held-hook follow-up controls pass unchanged (21.028s
package for the held-hook group) (36.580s package); the interop race package (27.858s) and vet pass.
Test-only change; the D115 artifact remains current.

Limits: the fake ACP peer, not actual Kiro, delivers the mid-session boundary, and the reproduced
line is a synthetic line carrying the recognized instruction, not the CLI's retained text. A prompt
that fails after streaming has begun (the late-expiry path) is covered only by the gateway unit
controls. Logs under `.cache/history-review/`: `d117-logged-out.log`, `d117-auth-expiry.log` and
`d117-compiled-run.log`.

## D118: keep the user's answer to the client's workspace-trust dialog

The product is meant to give the experience of Claude Code in a directory with only the backend
replaced by Kiro. Native Claude Code asks its workspace-trust question once per project and records
the answer in `~/.claude.json` under `projects[<path>].hasTrustDialogAccepted`; the proxy asked on
every launch of a project the user had not opened natively, because the client wrote the answer into
the private profile, which is discarded at exit, and the launcher never writes a source client file
(D64). With the installed pair the dialog reads "Accessing workspace … Quick safety check … ❯ No,
exit / Yes, I trust this folder", its default exits the client, and it appears for a directory with
files or a repository but not for an empty one. Projects the user trusted natively were already
projected (49 of the user's 58 recorded projects), so only proxy-only projects repeated.

The user directed native parity with the narrow write-back: the dialog stays the client's own
security gate, but a yes answer is recorded where the client records it. Auto-trusting the launch
directory was rejected because it would bypass that gate for project hooks and settings, and a
product-private trust store was rejected because native Claude Code would not see trust granted
through the proxy. This decision is a reviewed exception to D64's byte-for-byte rule and is the
launcher's only write to a source client file.

Safeguards, all in the launcher: the write happens only after the client exits, only when the
private profile of this launch records `hasTrustDialogAccepted: true` for the launch project under
the client's own key (the launch path or its resolved form), only when the source file existed at
launch and lacks the value, and only when the source bytes are identical to what this launch read (a
digest taken at preparation); a native client rewriting the file in the meantime makes the launch
skip the write, so the dialog is asked once more. The document is spliced, not re-encoded: the one
key is inserted into the existing project object, or the project object into `projects`, or a
`projects` object at the top, and an explicit false becomes true in place; every other byte is
preserved. The result must parse as a strict object, must be accepted by the next launch's
projection and must carry exactly that one new value; it is written beside the source with the
source's permission bits and renamed into place, and a link or an unsafe mode aborts. An absent
source is never created, "No, exit" records nothing, and every doubt skips silently. The client run
result reports `ProjectTrustPersisted`.

Witnesses: launcher unit tests cover the splice for an empty document, a missing `projects` object,
an existing project with nested braces inside strings and arrays, an empty project object and a
false value, reject malformed or unexpected documents, and require the written source to differ from
the seed by exactly the inserted fragment with its mode kept and no temporary file left; they also
require silence for no answer, a source changed since launch, an absent source, an already projected
trust and a closed profile (launcher 37.717s and command 8.216s race packages). The compiled-command
control launches the unmodified 2.1.267 client twice in one owned HOME whose global file lacks the
project: the first launch shows the dialog, moves to "Yes, I trust this folder" only after a fresh
render shows the selection there, completes an ordinary question, and leaves the global file changed
by that fragment alone; the second launch starts without the dialog and leaves the file unchanged
(four consecutive two-launch runs of 18.55–20.71s, three of them in a 57.068s package). The other
compiled-command scenarios now seed native trust for their project in the owned global file, which
the projection carries, instead of the fixture writing acceptance into the private profile: that
fixture write was indistinguishable from a user's answer and would have been persisted. Neighbouring
compiled-command controls pass (68.541s package); the complete installed-client batch passes (72
controls: 69 pass in the 637.174s batch and the three load-sensitive controls, existing-statusline
precedence and the two personal-asset controls, pass standalone in 35.929s); the whole-repository
opt-ins-off race suite (interop 29.769s, launcher 37.717s, session 30.417s) and vet pass. The
rebuilt development artifact and its `project-trust` inventory are recorded in DEPENDENCY_REVIEW.md
and installed.

Limits: the compiled control exercises the same client-run path with the independent Kiro fixture;
no observation was made against the user's own global file, which would have left a temporary
project entry in it. The client's own key form on a symlinked launch path is accepted by resolved
comparison but was measured only with a plain path. The dialog's default and wording remain the
client's.

## D119: verify plugin skill content and hooks against the actual Kiro backend

D72 observed one plugin tool call through the actual restricted Kiro process, and D65–D73 verified
plugin sources, hooks, skill expansion and a model-selected skill with the independent fixture
backend. Whether a plugin skill's own instruction reaches the actual model, and whether plugin hooks
surround a real turn, remained unverified, recorded as the open actual-Kiro plugin acceptance. The
user authorized the credit-consuming episodes on 2026-09-11.

The plugin-assets control gains two live modes that put the real gateway in front of the actual
restricted Kiro process with the same launch configuration as the live plugin control: a private
KIRO_HOME with default-resource suppression, a relay-only agent, the `auto` model, a 45-second turn
limit and at most one recreation. Each run generates a fresh 26-character marker that the prompt
never contains. In the skill mode the owned plugin's skill body carries the instruction to reply
with exactly that marker, and the prompt asks the model to invoke the skill by name through the
Skill tool; in the hooks mode the prompt itself asks for the marker and the plugin's SessionStart
and Stop hooks are the subject. The observing HTTP server still validates the client's requests
before forwarding them: the advertised Skill schema accepts the owned invocation, the first request
lists the skill without its body, and the second carries exactly one matching result and the
client-expanded body as separate text. Title requests are answered locally.

Measured with the unmodified 2.1.267 client and the measured Kiro 2.21.3: in the skill mode the
model calls the advertised Skill tool, the client expands the skill, the second turn's answer
contains the marker, two backend processes are prepared and cleaned in order, the startup hook's
context is present in the model's request and the SessionStart and Stop hooks each run once (17.99s
case, 34.84s test). In the hooks mode one turn returns the marker with the same hook and cleanup
facts (8.34s case, 24.87s test); 60.800s package, `d119-live-plugin-assets.log`. Both passed at the
first attempt with no retry. The fixture-backed plugin controls pass unchanged after the observer's
forwarding was generalized ({REGRESSION}). Three model turns were consumed.

Limits: this measures a local, independently authored marketplace and plugin; remote marketplaces on
the actual backend, Kiro-side skills and agents (suppressed by the launch configuration),
mid-session plugin enable/disable and the interactive MCP-panel path remain separate work, as D70
records. Test-only change; the D118 artifact remains current.
