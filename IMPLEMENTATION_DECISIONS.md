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
disabled thinking, a JSON object schema with only a required string title, and title/conversation
intent in the system. Classification examines bounded system/configuration inputs, not one loose
substring. Actual-client title templates remain an interoperability gate.

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
The launcher/client hook adapter remains separate implementation work; D22 defines local estimates.

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
written. Project/local settings remain loaded by the client, and the host overlay omits permissions,
hooks and availableModels. No safe/restricted/empty-settings-source/strict-MCP flags are added.

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
requires an explicit disabled-thinking declaration, so omission cannot be adopted without separately
checking actual title and foreground paths. Tool, permission, compaction and output-constraint
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
