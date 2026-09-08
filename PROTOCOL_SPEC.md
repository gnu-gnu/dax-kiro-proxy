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
| POST | product hook namespace: turn metrics | client hook bridge | UI token |
| POST | product hook namespace: model capabilities | startup model notice | UI token |
| GET | product status namespace: Kiro usage | nonblocking status line | UI token |

The new implementation chooses a `dax-kiro-proxy`-owned route prefix rather than retaining a host
project’s prefix.

### Message request validation

The request body limit is 16 MiB. The body must be a JSON object with a nonempty `messages` array.
The gateway accepts the Anthropic request fields required by current Claude Code traffic and ignores
unknown compatible fields unless they conflict with safety invariants.

System content may be a string or an ordered array of text blocks. Message order and content-block
order are preserved. The latest user message provides the current prompt and any tool results. Earlier
messages provide reconciliation history rather than being blindly replayed on every turn.

Malformed model selection is a 400. Malformed effort should be ignored with a warning unless future
public API requirements say otherwise. Backend failures are normally 502. Recognized Kiro auth expiry
is the special successful fallback described below.

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

### Tool request

A tool request follows this order:

1. message start;
2. tool-use content-block start carrying the client-visible tool call ID and original tool name;
3. one or more JSON input deltas;
4. content-block stop;
5. message delta with `tool_use` stop reason;
6. message stop.

The next client request must return all and only the pending tool-result IDs for that session.

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
`end_turn`. Streaming requests receive the complete valid SSE sequence. A product-specific response
header marks the completion as an authentication fallback. The text tells the user to run
`kiro-cli login` and retry. No alternate provider is attempted.

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

### Prompt

Call `session/prompt` with the Kiro session ID and an ordered ACP prompt content array. Content supports
text and the media/document forms advertised by negotiated capabilities. A normal completed result and
its streamed notifications jointly determine the Anthropic response. First-event and total-turn
timeouts are independent.

### Model selection

Call `session/set_model` with `sessionId` and exact backend `modelId`. Selection occurs only while the
session is idle. Successful selection updates current model state and clears cached effort state.

### Cancellation

Send the `session/cancel` notification with `sessionId`. A disconnected client or canceled gateway task
must discard the affected session so partially committed Kiro state is never reused.

## 6. Kiro private extension methods

Private methods are optional and isolated behind a capability adapter.

### Command availability

`_kiro.dev/commands/available` is a notification containing a session ID and command descriptors. The
presence of `/effort` allows an effort attempt. Its absence means the gateway continues without effort
synchronization.

### Effort execution

Use `_kiro.dev/commands/execute` with the session ID and a command object whose command name is `effort`
and whose arguments contain the requested value. Success requires an object with `success` equal to
true. A missing command, well-formed negative result, or command rejection is nonfatal and recorded as
the effective sync status. Transport corruption, failed writes, process termination, and ambiguous
timeouts retain the process-retirement semantics of section 4, even during an optional command.

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

The child MCP server speaks newline-delimited JSON-RPC over stdio and supports only initialize, ping,
tools/list, and tools/call. It performs no tool effect. A tools/call is forwarded over an owner-only
Unix-domain socket to the parent, authenticated by a random per-session secret.

The control request identifies the relay call, opaque tool alias, and object arguments. The parent
validates the secret, unique call ID, alias membership, argument schema, queue capacity, and session
ownership. It then creates a new client-visible tool call ID and suspends the relay call until the next
matching client tool result. Results preserve text, base64 image content, and error status; unsupported
content becomes text.

Default limits are 4 MiB per parent-control frame, 8 MiB per MCP stdio frame, 64 pending relay calls,
and a configurable pending-tool timeout. Socket, secret/config file, and containing directory are
owner-only.
