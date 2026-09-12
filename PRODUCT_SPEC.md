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

No user or project-level client settings may be modified, with one reviewed exception (D118): after
the client exits, a yes answer to the client's workspace-trust dialog is spliced into the user's
`~/.claude.json` as that one value, and every doubt skips the write. The launcher must use a
temporary, product-owned settings/profile location. It must prevent accidental fallback to a direct
Anthropic credential by removing incompatible provider and credential variables from the child
environment.

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

The initial compatibility target is the Messages subset exercised by the measured Claude Code client
(2.1.268 since D124, previously 2.1.267 from D113; D110 admits same-major builds without new
measurement), not full Anthropic API parity. D33 inventories implemented behavior; D52 records the
delivery policy.

| Area | Initial contract |
| --- | --- |
| Conversation, streaming, exact model selection, client tools/results and cancellation | Preserve the supported semantics and client execution authority; unsupported content/tool constraints still reject |
| Effort | Best effort after model selection; skip auto and degrade on optional capability absence or application rejection |
| Images and documents | Support only validated shapes and negotiated capabilities |
| Positive max_tokens, top-level thinking and context_management | Accepted compatibility hints with no provider token cap, reasoning-budget/block conversion or context-edit guarantee |
| JSON Schema output declarations | Narrow auxiliary-title classification only; no general output-schema enforcement |

The accepted max_tokens range is 1..1,048,576. Zero is outside this subset and rejects before model
work. Do not cut off visible output to simulate a token budget while retaining a longer committed
backend conversation. Gateway byte/deadline limits are resource bounds, not provider token limits.
Acceptance of a top-level reasoning hint does not permit fabricated reasoning signatures or override
the validation of history blocks. Context hints do not authorize altering the client's tool results.
These limitations must remain visible in product documentation; acceptance alone is not semantic
support. Unknown models, invalid tool schemas, unsupported server tools, mismatched results and
active/pending history divergence fail before changing their associated state. Proven idle divergence
can still recreate a session under the existing history policy. D33's other explicit rejections remain.

For the pinned client's tool traffic, a changed tool registry or one changed standing system message
after a tool result can require a fresh backend session (D63, extended by D70). D73 also supports
successful tool results followed by client text, such as an expanded skill body, through this path.
The complete prior history, owner, model, effort, top-level system, metadata and tool-choice policy
must still match. Validate the new registry and delivered results before joined cleanup and a
full-history replacement. Reconstruction is bounded to sixteen times per logical turn by default and
retains its original absolute deadline. It loses hidden backend context and may add provider work.
An unchanged registry and repeated standing instructions keep the same prompt for result-only
continuations. Regrouped/altered history and broader changed-message sequences remain unsupported.
Installed-client/fake-ACP tests verify MCP readiness followed by allowed or hook-denied plugin
execution. Actual Kiro passes one default-tool Read refusal and changed-standing-message recreation
(D71). D72 observes the actual wait/registry/plugin/result path and cleanup, but its final-answer
marker check fails; D119 later verifies plugin skill content and SessionStart/Stop hooks against the
actual Kiro backend, leaving remote marketplaces, Kiro-side skills and mid-session plugin changes
open. Broader recovery remains an alpha gate.

The pinned client's model-selected plugin skill completes through the real gateway/relay with fake
ACP: one Skill request, its matching success and separate skill text, one joined recreation and
final client output. D119 supplies the actual Kiro skill evidence. The successful-result/text
extension preserves the original deadline and complete history; the proxy neither executes the skill
nor absorbs its client instructions into a tool result. Remote marketplaces and mid-session plugin
changes remain.

Public ACP and Anthropic behavior form the stable core. Kiro methods beginning with a private namespace
are optional, version-sensitive capabilities. Their absence must reduce metadata or effort features,
not break ordinary text/tool turns. The implementation records the detected Kiro CLI executable,
version, and capabilities in caches so incompatible cache entries are not reused.

Prefer a temporary overlay for product-owned client integration. Preserve the client's existing
permissions, hooks and assets without modifying source settings, apart from the D118 workspace-trust
answer. Optional product hooks/status must yield when explicit client settings, safe mode or an
existing status command prevent a compatible overlay. Mandatory local routing and credential
separation must still hold. Replacing the current private profile requires independent
installed-client precedence and preservation tests first.

Native client conversation retention is optional and distinct from the text-free proxy session
records (D87). `run --client-history` permits Claude to use its ordinary `~/.claude/projects` data
through a reference in the temporary profile. `run --resume <UUID>` implies retention and passes the
validated ID to the native client. The option covers native conversation text and any other data
Claude keeps under that directory; it does not promise retention of native data stored elsewhere.
The proxy does not parse, copy, merge or edit transcript formats. Source settings and routing remain
in the existing isolated configuration path. Without either option, client history stays ephemeral.
Prepared Kiro sessions still do not load persisted backend state; restart creates a new restricted
session from client-supplied history and can add provider work or lose hidden backend context.

Concurrent resumes of one native session retain the client's own shared-transcript behavior.
Stored replies do not guarantee their inclusion in a later model request (D105). The proxy does
not merge those histories, silently fork the session or promise isolated branches for the same ID.
Use distinct native sessions for independent parallel conversations.
