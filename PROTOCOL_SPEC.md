# Protocol specification

## 1. External HTTP gateway

### Binding and authentication

The server binds to an ephemeral loopback TCP port by default. Accepted loopback host forms are IPv4
loopback, IPv6 loopback, and localhost. Any other bind requires a deliberate option.

The runtime creates two independent random secrets:

- model token: accepted as `x-api-key` or a Bearer authorization credential on model APIs;
- UI token: accepted only on the exact local hook/status routes configured for it.

The UI token must never authorize a messages or models request. Missing or invalid credentials return
HTTP 401 using an Anthropic-style error envelope.

### Routes

| Method | Path | Purpose | Credential |
| --- | --- | --- | --- |
| GET | `/health` | local liveness | implementation policy; no sensitive data |
| GET | `/v1/models` | Kiro-backed model catalog | model token |
| POST | `/v1/messages` | Anthropic Messages compatibility | model token |
| POST | `/messages` | compatibility alias | model token |
| POST | `/dax-kiro-proxy/hooks/turn-metrics` | drain local turn metrics | UI token |
| POST | `/dax-kiro-proxy/hooks/model-capabilities` | startup model notice | UI token |
| GET | `/dax-kiro-proxy/status/usage` | nonblocking usage and latest turn | UI token |

The new implementation chooses a `dax-kiro-proxy`-owned route prefix rather than retaining a host
project's prefix.

The turn-metrics hook accepts an empty body or empty JSON object, at most 4 KiB, and returns
`{records: [...], dropped: N}`. Unsupported bodies do not drain records. Status returns normalized
account-usage availability/state and optional `latest_turn`, which remains available after draining
the queue or when account usage is unavailable. Neither route accepts a prompt or invokes a model.
Decision D21 defines the cache, record bounds and delivery policy. Before draining, the handler
waits at most 200ms for final-response deliveries already registered when this hook arrived to
finish their bookkeeping. Later deliveries do not extend that wait. Cancellation or expiry returns
a fixed 503 without draining; status and model-capabilities reads do not wait. Decision D50 records
this synchronization, which does not promise acknowledged or exactly-once display.

The model-capabilities POST accepts the same empty body/object and 4 KiB limit. It returns a version-1
object with exactly `version`, `model`, `image_input`, `pdf_input`, `native_web_search`, `effort`,
`client_tools` and `provider_token_usage`. `model` is the immutable prepared launch alias; callers
cannot select another model through this route. Image, PDF and effort states are `unknown` until
negotiated; native web search is `unsupported` by the current adapter, client tools use
`client_permissions`, and provider token usage is `unreported`. These are startup information, not
claims of effective Kiro restrictions or a ready session. The route opens no ACP session, invokes
no discovery/usage refresh, and consumes no turn record. An absent prepared launch model returns a
fixed 503 without discovery. Decision D49 defines this initial payload; future verified capabilities
need a corresponding contract change.

The temporary client settings select a product-owned `statusLine` command with a five-second
`refreshInterval`. Its `statusline --config ABSOLUTE_PATH` helper reads a version-1, owner-only JSON
file containing exactly `version`, `endpoint`, `token` and `model`. The endpoint is an HTTP literal
loopback IP with an explicit port; the token has UI authority only. The helper's sole network request
is the fixed usage-status GET, with a 750ms total HTTP deadline, no redirects/proxies/retries, an
8 KiB response-header limit and a 64 KiB body limit. It reads no client stdin or transcript. Output
is at most 1 KiB of normalized single-line text; unavailable data has a fixed local display. A
two-second deadline started in helper main also bounds blocked inherited output. Source settings
remain unchanged, and a late invocation cannot recreate the removed runtime. Decision D47 records
credential delivery, display semantics and the distinction from live Kiro usage verification.

The same private configuration also serves `model-notice --config ABSOLUTE_PATH`. Its only request
is the exact model-capabilities POST with `{}` and the UI token, under the same HTTP and process
bounds. The launcher adds a synchronous command hook for `SessionStart` with matcher `startup` and
a three-second client hook timeout. It preserves source hooks, permission policy and the client's
effective hook-disable setting. Other session lifecycle events do not repeat the launch notice.
The helper emits at most 1 KiB of JSON containing only a fixed-format `systemMessage`; it never emits
model context, a new user prompt or permission/control output. Unavailable/malformed responses produce
a fixed unavailable notice; invalid private configuration and caller cancellation fail silently.
Neither a notice nor an optional hook's absence changes the separate initialization/policy gates.

`turn-metrics --config ABSOLUTE_PATH` uses that same private configuration and HTTP/process bounds.
The launcher adds a synchronous `Stop` command hook with a three-second timeout and the existing
cleared environment. Its sole request is the exact UI-authenticated turn-metrics POST with `{}`.
It validates all retained records and emits at most 9 KiB of JSON containing only `systemMessage`.
Each line identifies one completed foreground turn with normalized model, effort, session state,
local duration and available numeric metadata/labeled estimates. Identity digests and arbitrary
upstream text are excluded. Up to 32 retained records are shown; a nonempty page also reports its
cumulative eviction count. Empty, unavailable or malformed pages emit `{}` with no notice. Invalid
configuration or caller cancellation fails silently. It emits no model context, user prompt,
permission decision or continuation control; source hooks and effective hook-disable policy remain
under client control. A tool handoff alone is not a completion.

### Message request validation

The request body limit is 16 MiB. The body must be a JSON object with a nonempty `messages` array.
The gateway accepts the Anthropic request fields required by current Claude Code traffic and ignores
unknown compatible fields unless they conflict with safety invariants.

System content may be a string or an ordered array of text blocks. Message order and content-block
order are preserved. The latest user message provides the current prompt and any tool results. Earlier
messages provide reconciliation history rather than being blindly replayed on every turn.

Claude Code may also send text-only per-message `system` entries, including after the latest user
message. Preserve those role boundaries and ordering; they do not replace the latest user input.
Assistant prefill remains unsupported. Authenticated `x-claude-code-session-id`,
`x-claude-code-agent-id` and `x-claude-code-parent-agent-id` headers supply opaque conversation
identity. Each optional value is 1-128 visible ASCII bytes excluding comma; duplicates are rejected.
The body cannot override this identity. See decision D14 for source and observation evidence.

Per-message `clear_at` is accepted only as `"never"` on a system message. Turn-scoped instruction
expiry and per-message `output_config` are currently rejected before backend dispatch; their meaning
must not disappear during normalization. Decision D18 records this supported subset.

Malformed model selection is a 400. Malformed effort should be ignored with a warning unless future
public API requirements say otherwise. Backend failures are normally 502. Recognized Kiro auth expiry
is the special successful fallback described below.

Nonempty `stop_sequences` and `mcp_servers`, non-null `container` and `inference_geo`, and any
`temperature`, `top_p`, `top_k` or `service_tier` declaration are rejected with a safe field-specific
400 before backend work. Empty arrays are accepted for the two list fields. These controls have no
implemented mapping and must not be silently ignored. Decision D33 inventories the supported fields
and explicitly records the still-unresolved token, reasoning, context-management and structured-output
semantics; syntactic acceptance of those fields is not proof that their constraints are enforced.

### Local command interception

Kiro usage and proxy diagnostic commands are recognized by strict request shape, completed locally,
and removed from the model conversation history. They return normal Anthropic message responses with
zero provider token usage. They must not create a Kiro model turn.

## 2. Model discovery

The model-list response uses the OpenAI-compatible object/list shape expected by the client. Each Kiro
model is represented by:

- a stable namespaced identifier beginning with a client-accepted Claude-family prefix;
- a human-readable name;
- a description marked as supplied by Kiro;
- an optional credit multiplier in display text, not in the reversible backend identifier.

Client-facing IDs are generated from a normalized Kiro model ID plus a short cryptographic digest of
the exact backend ID. This prevents collisions between punctuation variants and avoids deduplication
against built-in client models. Reverse mapping accepts only entries in the current validated catalog.

The catalog is invalid if it is empty, repeats a backend model ID, or produces duplicate client-facing
IDs. The current backend model is included if Kiro identifies it but omits it from the available list.

Use `display_name` for the human-readable model name consumed by Claude Code gateway discovery.
Discovery requires the client's explicit gateway discovery configuration, as recorded in D14.

## 3. Anthropic response and SSE mapping

### Text completion

A streaming text response follows this order:

1. message start;
2. text content-block start;
3. zero or more text deltas;
4. content-block stop;
5. message delta with `end_turn` stop reason;
6. message stop.

A non-streaming response contains the equivalent single assistant message and text block.

Streaming responses send a public Anthropic `ping` during a silent wait, initially every 15 seconds.
The first ping can commit the message headers before any model text. Pings do not satisfy the first
usable-event deadline, reset total-turn time, count as model output, or change provider usage.
Failures after that commitment follow the ordinary late-failure/authentication rules below.

### Tool request

A tool request follows this order:

1. message start;
2. tool-use content-block start carrying the client-visible tool call ID and original tool name;
3. one or more JSON input deltas;
4. content-block stop;
5. message delta with `tool_use` stop reason;
6. message stop.

The next client request must return all and only the tool-result IDs in that response's sealed,
successfully delivered batch. Later relay calls remain queued for a subsequent response. Validate
the complete result set and final encoded result sizes before completing any suspended call.

A result-only request with an exact repeated standing system suffix and unchanged registry continues
the same ACP prompt. So does a result-only request whose suffix is one nonempty text-only system
message replacing a one-message standing sequence: the pending prompt keeps the instruction it
received, the rotated message is recorded in the history digest so later requests still match, and
the backend next receives whatever standing message accompanies the next prompt; the rotated text
itself is not delivered unless a later full projection replays it (D123). D70 extends D63's
replacement path to a changed validated registry, with or without changed standing instructions. On
the replacement paths a changed suffix must be one text-only system message after the eligible
result/text message below, and the last standing sequence must also have exactly one message. This
requires the entire prior history as an exact prefix, unchanged owner/model/effort/top-level
system/metadata/tool-choice policy, and the complete delivered result set. Regrouped history and
repeated prior results still reject. Validate the full replacement projection and result encoding
before revoking the old relay. Join old ACP/relay cleanup, then create a fresh session with all
supplied history, including the tool request, its result and the new instruction. Actual results
never resolve into the retired prompt. Recreation is bounded to sixteen times per logical turn by
default and retains the original absolute deadline, including replacement setup. Expired work,
truncated history, multiple changed messages or an exhausted restart allowance reject. Full-history
recreation loses unreported backend context and may add provider work; it is not equivalent to
keeping the original backend conversation.

D73 also recreates when the complete delivered client-result batch contains at least one success
and is followed by text in the same user message. All results must precede all text; the additional
blocks must be text-only and include nonempty content. The same full-prefix, policy, validation,
joined-cleanup, original-deadline and recreation-count rules apply even if the standing instructions
and registry are unchanged. Preserve the supplied text as client text in the full projection, never
as tool output. This covers client-expanded skill instructions without a tool-name exception.
Expired successful work cannot use the separate all-denial/new-question recovery window.

Each HTTP completion uses a fresh message ID, including tool handoffs. D70's installed-client
counterfactual shows that reusing one ID across separate responses can regroup earlier calls/results;
that fixture behavior is not an accepted history transformation.

### Server web search

Supported versioned Anthropic web-search tool declarations map to Kiro’s native web-search capability.
The result maps back to an Anthropic server-tool-use block, a web-search-result block, optional text,
and final usage metadata including the search request count. Duplicate Kiro update events are
suppressed. Web-fetch structured conversion is optional until independently specified; text fallback
is acceptable.

### Usage fields

Anthropic input/output/cache token fields remain zero when Kiro ACP provides no compatible accounting.
Locally estimated tokens are exposed only through diagnostic/status metadata and carry an approximation
marker. Estimates must never be inserted into provider usage fields.

### Authentication fallback

Recognized Kiro login/token expiry returns HTTP 200 and a normal assistant text completion with
`end_turn`. Before response headers are sent (including buffered responses),
`X-Dax-Kiro-Proxy-Auth-Fallback: 1` marks the fallback. If a stream has already started, append login
text to the existing message, finish it with `end_turn`, and send the same marker as a predeclared
HTTP trailer. Never start a second message or emit an SSE error for recognized auth expiry. Trailers
are supplemental because a client may discard them; the visible text always tells the user to run
`kiro-cli login` and retry. No alternate provider is attempted. Ordinary failures before headers use
HTTP 502; after stream commitment they use an Anthropic SSE error and discard the affected session.

## 4. ACP process transport

### Launch

Start the configured `kiro-cli` executable with the `acp` subcommand and an execution-restricted agent.
An initial model and effort may be passed when explicitly configured. The process runs in a new process
group, in an isolated working directory, with an allowlisted environment.

### Framing

ACP messages use JSON-RPC 2.0 over stdin/stdout. Each message is one compact JSON object terminated by
a single line feed. Content-Length framing is not used. The default maximum inbound and outbound line
is 8 MiB.

Client request IDs are monotonically increasing integers within a process. Multiple requests may be
pending and responses may arrive out of order. Writes are serialized; response completion is matched
by ID. Malformed JSON, non-object frames, oversize frames, unexpected process termination, or an
unrecoverable write failure fail every pending request and retire the process.

### Initialization

The first request is `initialize` with ACP protocol version 1, empty client capabilities, and
implementation name/version. The result must negotiate protocol version 1. Any other version is fatal.

Observed Kiro capabilities include session loading, prompt images, HTTP MCP servers, and no prompt
audio, embedded context, or SSE MCP. Capabilities must be detected, not assumed where ACP exposes them.

### Agent-to-client requests

The process may send JSON-RPC requests to the proxy. `session/request_permission` is answered by
selecting a reject option when present, otherwise by cancellation. Every other agent request receives a
method-not-found or disabled response. This rule prevents Kiro from acquiring direct execution rights.

### Stderr and authentication recognition

Stderr is drained continuously into a bounded tail. Each line is length-limited and credential-shaped
substrings are redacted. Explicit 401, login-required, expired-token, invalid-token, invalid-grant, or
reauthentication messages are classified as authentication expiry. Ambiguous transport errors remain
ordinary backend errors.

### Shutdown

For an active session, send `session/cancel` when possible. Close stdin, wait for graceful exit, then
terminate the process group and finally kill it after bounded waits. All pending futures are completed
with an error. Cleanup must be shielded from caller cancellation and safely retryable.

## 5. ACP session methods

### New session

Call `session/new` with the isolated workspace path and a list containing the session’s relay MCP server
definition. The result must contain a string session ID. It may contain model state with a current model
ID and an available-model list. Each available model has a backend ID and optional name/description.

### Load session

Call `session/load` with the persisted Kiro session ID, isolated workspace path, and a newly created
relay MCP server definition. Replay notifications received while loading are discarded. If the result
returns a session ID, it must equal the requested ID. A failed or inconsistent load invalidates the
persistent record and falls back to a new session with full safe history.

Attempt load only when `loadSession` was negotiated. Accept the public `null` result and an object
result; a returned ID must match. If model state is omitted, use compatible stored catalog metadata
and explicitly confirm its selected model before dispatch. Replay is drained before the load response
becomes visible to the session driver. Authentication failure ends recovery with the login fallback.

### Prompt

Call `session/prompt` with the Kiro session ID and an ordered ACP prompt content array. Content supports
text and the media/document forms advertised by negotiated capabilities. A normal completed result and
its streamed notifications jointly determine the Anthropic response. First-event and total-turn
timeouts are independent.

D127 distinguishes validated progress from answer content. An owned active prompt's nonempty text
thought, valid nonempty plan, or validated tool activity can satisfy the first-event wait. Progress
is internal and carries no answer, tool execution request or usage. Unknown/empty updates, private
metadata and transport keepalives do not qualify. The original total-turn deadline remains fixed.
The bounded admission fields and deliberately ignored informational forms are recorded in D127.

The inline media subset and limits are defined in D19: base64 PNG/JPEG/GIF/WebP require image
capability, base64 PDFs require embedded context, and plain-text documents can use text projection.
URL/file-ID sources and enabled citation conversion are unsupported. Historical images remain native
blocks. The final encoded prompt must fit the ACP frame before it can be dispatched.

Fresh context also preserves inline images nested in a historical client tool result (D109).
Each such result uses JSON text markers for its original tool_use_id, is_error and content count,
then indexed content markers and a matching end marker. An image content marker is followed by
the native ACP image; other result content remains JSON data within its result boundary. These
markers are proxy prompt conventions, not new ACP methods or Anthropic wire blocks. Images in
results share D19's negotiated image capability, header/dimension and total media bounds with
top-level prompt media. Proven deltas do not resend committed result images. Existing opaque
fallback for unsupported tool-result sources remains effect-free and performs no URL fetch.

### Model selection

Call `session/set_model` with `sessionId` and exact backend `modelId`. Selection occurs only while the
session is idle. Successful selection updates current model state and clears cached effort state.
When the session instead advertises a public `configOptions` select control in the `model` category,
use its exact ID with `session/set_config_option` and validate the returned complete configuration
state. Only that model selector may be changed. The proxy retains the idle-only transition policy
even though the public generic configuration API permits some changes during generation. An
inconsistent selected value fails the turn; there is no approximate-model fallback.

### Cancellation

Send the `session/cancel` notification with `sessionId`. A disconnected client or canceled gateway task
must discard the affected session so partially committed Kiro state is never reused.

## 6. Kiro private extension methods

Private methods are optional and isolated behind a capability adapter.

### Command availability

`_kiro.dev/commands/available` is a notification containing a session ID and command descriptors. The
presence of `/effort` allows an effort attempt. An advertised list without `/effort` means unavailable.
No valid advertisement before prompt dispatch means unknown and no attempt. A previously rejected
unknown model/effort pair is not probed again within the same version/configuration scope. Model and
process changes clear effective state; confirmed supported effort may be reapplied to new state.

### Effort execution

Use `_kiro.dev/commands/execute` with `{sessionId, command: {command: "effort", args: {value}}}`,
where `value` is the normalized requested level. This private shape is observed with the unmodified
Kiro 2.21.2 terminal and verified independently through ACP (D99; the measured pin has since moved
to 2.21.3, D114). An empty args object lists choices on the measured supported model; it is not a
current-value query. Success requires an object with `success` equal to true. A missing command,
well-formed negative result, or command rejection is nonfatal and recorded as the effective sync
status. Transport corruption, failed writes, process termination, and ambiguous timeouts retain the
process-retirement semantics of section 4, even during an optional command.

### Account usage

On the measured Kiro 2.21.3/v2 combination (2.21.2 when D101 was recorded; a same-major pair
admitted under D114 runs this path unmeasured), a separate empty-agent ACP session may execute the
advertised `tools` and `usage` commands with `{sessionId, command: {command, args: {}}}` (D101). The
tools result must first confirm an empty list. This path never issues `session/prompt`, borrows a
model session, enables native tools or copies user Kiro configuration. Missing advertisements,
malformed replies and query failures leave usage unavailable or preserve its last good cache entry;
they do not fail a model request. Other major versions require new evidence.

A successful result has `success: true` and a `data.usageBreakdowns` array. Only a unique
`resourceType: "CREDIT"` entry supplies `used_credits` from `used` and, when `hasLimit: true`,
`limit_credits` from `limit`. Amounts must be finite numbers from zero through 1e12. The proxy does
not derive remaining credits, combine bonus/add-on buckets, expose account labels or convert these
credit amounts into provider token usage. D101 records the bounds and version-specific evidence.

### Metadata

`_kiro.dev/metadata` is a notification associated with a session. Recognized fields are context usage
percentage, turn duration in milliseconds, and metering-usage entries containing numeric value and
unit labels. Unknown fields are ignored. Metadata may arrive more than once; the turn-level accumulator
deduplicates or replaces values without producing duplicate user-facing turn completions.

### Diagnostic updates

Private session/tool chunk notifications and MCP-server/subagent status notifications are diagnostic.
They may enrich logs but must not be required for correct text or tool-result delivery.

## 7. Relay protocol

Each Kiro session gets a child MCP server plus a private parent-control channel.

The child MCP server speaks newline-delimited JSON-RPC over stdio, implements the 2025-06-18
initialization lifecycle, and supports only initialize, ping, tools/list, and tools/call request
methods. Lifecycle/cancellation notifications receive no replies. Real Kiro version negotiation must
be verified before live enablement. It performs no tool effect. A tools/call is forwarded over an owner-only
Unix-domain socket to the parent, authenticated by a random per-session secret.

Before MCP starts, the child authenticates and joins the supervisor-bound ACP process group (D42).
The private on-disk child configuration is version 3 with eight exact fields (D127): `version`,
`supervisorPid`, `socket`, `owner`, `secret`, `timeoutMillis`, `attachTimeoutMillis` and `tools`.
The new attachment limit is 1..60000 milliseconds and follows the configured session setup limit.
The supervisor's original setup context also bounds waiting for its one-time local process binding;
a canceled or expired setup rejects a new binding. A binding already completed successfully remains
valid after setup finishes. Initial-frame and acknowledgement reads retain their separate limits.
Legacy child configurations reject; private control frames and public MCP versions stay unchanged.

The version-1 private control channel uses a four-byte unsigned big-endian payload length followed
by a strict UTF-8 JSON object. Its call fields are `version`, `owner`, `secret`, `callId`, `alias` and
object `arguments`. The opaque relay owner is allocated before the Kiro session ID and bound only to
that session. Replies contain version, matching callId, and either result or a safe error. A relay
call ID never becomes reusable after completion; a bounded lifetime ledger retires on exhaustion.

The control request identifies the relay call, opaque tool alias, and object arguments. The parent
validates the secret, unique call ID, alias membership, argument schema, queue capacity, and session
ownership. It then creates a new client-visible tool call ID and suspends the relay call until the next
matching client tool result. Results preserve text, base64 image content, and error status; unsupported
content becomes text.

The MCP wire name remains an opaque alias. Its description prefixes the original client name and
client permission/hook authority, then preserves the entire original description. Client descriptions
retain their 8 KiB allowance; attribution has a separate 256-byte allowance in the private child
configuration. The registry identity version changes with this metadata policy (D60).

Tool-call admission is open only for an owned ACP prompt, including its successful HTTP tool handoffs.
It is closed during session creation/load, idle time and after the prompt reply. Prompt completion
with any pending or validating relay call is inconsistent and retires the session.

Default limits are 4 MiB per parent-control frame, 8 MiB per MCP stdio frame, 64 pending relay calls,
and a configurable pending-tool timeout. Socket, secret/config file, and containing directory are
owner-only.
