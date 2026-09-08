# Product specification

## Product outcome

`dax-kiro-proxy` lets an Anthropic Messages client use the models available through a locally
authenticated Kiro CLI while preserving the client’s normal conversation, tool, permission, hook,
and streaming experience.

The product has three principal roles:

- expose a short-lived, authenticated loopback Anthropic-compatible HTTP gateway;
- translate each client conversation into a stateful Kiro ACP session;
- relay Kiro tool requests back to the client without granting Kiro direct execution authority.

## Primary use case

A user starts a launcher command in a project directory. The launcher verifies that Kiro CLI exists
and is logged in, creates an isolated runtime profile for the Anthropic client, starts the loopback
gateway, and launches the client with only the local gateway URL and ephemeral credential. The user
selects a model discovered from Kiro, converses normally, approves tools through the client, and can
see Kiro usage and turn metadata without altering global client settings.

## In scope

- macOS-first local execution, with portability considered for later releases;
- one local Kiro CLI installation and its existing authenticated account;
- Anthropic Messages requests, both streaming and non-streaming;
- Kiro ACP process initialization, session creation, session loading, prompting, cancellation,
  model switching, and observed Kiro private metadata/effort extensions;
- Kiro model discovery exposed through an OpenAI-compatible model listing;
- conversation continuity across turns and optional process restarts;
- text, supported document/image prompt parts, client tools, tool results, and supported Kiro web
  tools;
- graceful authentication-expiry completion;
- usage/status output and explicitly labeled local token estimates;
- isolated temporary client configuration and optional handoff metadata for future work.

## Out of scope

- general TLS interception or transparent network proxying;
- routing to providers other than Kiro;
- reuse of the previous project’s hook DAG, traffic capture, request shaping, auth sources, model IR,
  or MCP management stack;
- direct filesystem, shell, editor, task, or subagent execution by Kiro;
- bypass of client permissions or hooks;
- claiming provider-accurate token accounting when ACP does not report it;
- storing conversation text as part of persistent session records;
- a Claude plugin UI for handoff in the first implementation;
- NeMo Relay integration in the core release.

## User-visible requirements

### Startup

The launcher must fail early with a concise instruction to run `kiro-cli login` when authentication is
unavailable. Startup must report elapsed time for binary/settings loading, login checking, runtime
preparation, gateway startup, model-catalog loading, client-profile preparation, process launch, and
client session initialization when startup timing output is enabled.

No user or project-level client settings may be modified. The launcher must use a temporary,
product-owned settings/profile location. It must prevent accidental fallback to a direct Anthropic
credential by removing incompatible provider and credential variables from the child environment.

### Models

The client model selector must show all valid models currently advertised by Kiro. Each model gets a
stable client-facing identifier that cannot collide with built-in model names. Display metadata should
include Kiro’s description and, when known, its credit multiplier.

The last model actually used by an interactive proxy session may be restored on the next launch. This
state must be separate from the client’s global default model. A configured launch model takes
precedence on the first turn.

### Effort

Client effort is synchronized only after the effective Kiro model is selected and before its prompt is
sent. Automatic Kiro model selection must not receive an explicit effort. Known unsupported
model/effort pairs are skipped. Unknown pairs may be probed once. Rejection is logged and the turn
continues using Kiro’s current/default effort.

### Tools

The client remains the sole execution authority. Kiro can request only tools advertised in the current
client request. A requested call appears to the client as a normal tool-use block. Kiro waits until a
matching client tool-result returns. No tool result is replayed, crossed between sessions, or accepted
twice.

### Streaming

Text must appear incrementally. Tool requests must be expressed as Anthropic tool-use streaming
events. Client disconnect, cancellation, upstream failure, and authentication expiry must each have a
deterministic result and cleanup behavior.

### Authentication expiry

If Kiro authentication expires during a live session, the gateway returns a normal assistant text
completion explaining that the user must run `kiro-cli login` and retry. It must not emit an opaque 502
or an SSE error event for this recognized case, and it must not route elsewhere.

### Usage and status

Usage refresh occurs independently of model turns and must never block status-line rendering. Cached
usage can be polled frequently; Kiro’s usage command is refreshed on a longer TTL. Turn metadata may
show model, effort, multiplier, duration, context percentage, credits, and clearly approximate input
context/visible output tokens.

## Security posture

- Loopback-only binding is the default; non-loopback use requires an explicit unsafe-network option.
- Model endpoints require a random high-entropy bearer/API token.
- A distinct UI token authorizes only enumerated status and hook routes.
- Every subprocess environment is allowlisted.
- Relay sockets, secrets, cache files, and session records are owner-only.
- Input sizes, line frames, queues, pending calls, stderr, logs, and shutdown waits are bounded.
- Prompt bodies are not logged by default, and credential-like text is redacted.
- The gateway never silently falls back to a different provider.

## Compatibility posture

Public ACP and Anthropic behavior form the stable core. Kiro methods beginning with a private namespace
are optional, version-sensitive capabilities. Their absence must reduce metadata or effort features,
not break ordinary text/tool turns. The implementation records the detected Kiro CLI executable,
version, and capabilities in caches so incompatible cache entries are not reused.
