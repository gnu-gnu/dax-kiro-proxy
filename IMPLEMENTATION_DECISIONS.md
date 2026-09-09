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
interpretation; no actual effort synchronization is claimed from the tools query.

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
model token in both supported auth variables, provider-host guard, discovery and no automatic retry
or nonstreaming fallback. HTTP proxy variables are cleared; loopback bypass, telemetry opt-out and
automatic-update opt-out are explicit. No remote endpoint, URL credential or unknown model alias is
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
and per-project hasTrustDialogAccepted Booleans. It preserves original project keys and declaration
values, including tool-specific MCP authentication, in owner-only temporary files. It excludes
unrelated provider sign-in, model defaults, conversation and other global state. Unrecognized
MCP-related keys reject rather than silently dropping a possibly restrictive policy. Malformed
objects, duplicate keys, null array elements, unsafe source modes/links and files over 2 MiB reject
before runtime creation. This is a pinned mapping, not a general client-state migration facility.

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
