# Implementation requirements and delivery plan

## 1. Architectural boundaries

Implement the product as independently testable components:

| Component | Responsibility | Must not own |
| --- | --- | --- |
| launcher | discovery, login preflight, isolated client profile, lifecycle | protocol translation |
| HTTP gateway | auth, validation, local routes, Anthropic encoding | subprocess internals |
| request translator | request classification and ACP prompt projection | session ownership |
| response translator | ACP events to buffered/streaming Anthropic output | tool execution |
| ACP transport | JSON-RPC framing, correlation, stderr, process lifecycle | conversation semantics |
| process pool | process compatibility and bounded reuse | message history |
| session manager | keys, state, reconciliation, persistence, cancellation | HTTP presentation |
| model catalog | discovery IDs, reverse mapping, cache, capability metadata | turn execution |
| relay parent/child | execution-free MCP bridge and pending calls | performing tools |
| usage/status | asynchronous usage refresh, local metrics, token estimates | provider token claims |

Public ACP adapters and Kiro-private extensions must be separate modules or interfaces. The core turn
path must work when every private extension is disabled.

## 2. Data and API requirements

- Use explicit typed representations for public request/response structures and internal state.
- Preserve unknown compatible Anthropic fields when possible, but validate fields that influence
  security, routing, model selection, tool execution, or resource limits.
- Keep raw prompts and tool output out of persistent session records.
- Use cryptographic random generation for tokens, relay secrets, and client tool-call IDs.
- Use cryptographic digests for model aliases, session keys, compatibility fingerprints, and history
  reconciliation.
- Use JSON Schema Draft 2020-12 validation for tool arguments.
- Reject duplicate tool names, duplicate model IDs, ambiguous aliases, malformed IDs, and oversized
  descriptions/schemas before launching a turn.
- Write persistent files atomically and with owner-only permissions.

## 3. Concurrency requirements

- Support concurrent HTTP requests for unrelated sessions.
- Serialize writes to one ACP stdin stream while allowing multiple outstanding request IDs.
- Route notifications by backend session ID; provide an explicit temporary owner for early
  `session/new` notifications.
- Use per-session-key locking, not a global request lock.
- Make model/effort transitions atomic relative to prompt dispatch.
- Bound ACP event queues, relay pending calls, process count, sessions per process, logs, and caches.
- Ensure a slow status/usage refresh cannot block a model request or status-line read.
- Ensure process failure completes all waiters and cannot leave futures pending indefinitely.

## 4. Security requirements

- Loopback bind by default with an explicit non-loopback override.
- Separate model and UI credentials and maintain an endpoint-level authorization table.
- Sanitize inherited environment variables for both Kiro and the client.
- Refuse to pass direct provider credentials into the launched client.
- Use an execution-restricted Kiro agent whose exact allowed tools are generated from the current
  registry plus explicitly supported native web tools.
- Reject all Kiro permission requests and unsupported agent-to-client methods.
- Authenticate relay control frames and validate session ownership for every call/result.
- Redact token-like strings before storing stderr/log records.
- Never include raw authentication data, prompts, tool results, or relay secrets in diagnostic output.
- Apply maximum-size checks before JSON parsing where feasible.

## 5. Reliability requirements

- Treat incomplete or ambiguous backend state as non-reusable.
- Distinguish authentication expiry from generic transport failure conservatively.
- Produce a valid Anthropic completion for recognized authentication expiry in both streaming and
  non-streaming modes.
- Separate first-event and total-turn deadlines.
- On disconnect/cancel, discard the session rather than risk a committed unseen turn.
- Make shutdown bounded, process-group aware, idempotent, and cancellation-shielded.
- Treat Kiro private feature absence as feature degradation, not a core turn failure.
- Never silently change requested model to an unavailable or approximate alternative.

## 6. Catalog and cache requirements

The catalog cache identity includes Kiro executable path/version, initial agent/model configuration,
and a digest of the selected Kiro profile scope. The initial TTL is 24 hours. A compatible stale cache
may be returned immediately and refreshed asynchronously; a hard identity mismatch requires synchronous
discovery.

The last-used model record is separate, atomic, owner-only, and applied only to interactive launches.
It never modifies the client’s global setting.
Decision D34 excludes one-launch model/effort overrides from preference identity while preserving
them in catalog identity. It records the actual model only after final foreground response delivery;
title/agent work, canceled turns and incomplete tool handoffs do not replace the preference.

Session persistence uses a separate cache. Version every record schema and invalidate rather than
migrate when correctness cannot be proven.

## 7. Observability requirements

Default logs contain request correlation, request kind, selected/effective model and effort status,
session created/reused/loaded status, bounded timing, outcome, and recognized failure class. They do not
contain full bodies.

Provide at least off, basic, debug, and trace request-log levels. Trace body capture, if implemented,
must be explicitly enabled, bounded, redacted, owner-only, and rotating. Initial rotation targets are
5 MiB and three backups.

Startup timing and turn metrics are user-facing diagnostics, not mandatory telemetry. No external
telemetry is sent by default.

## 8. Delivery phases

### Phase 0 — language and provenance gate

- complete `CLEAN_ROOM_BOUNDARY.md` checklist;
- run the Go/Rust spikes in `LANGUAGE_DECISION.md`;
- record the language, dependency, and license decision;
- freeze protocol fixtures written from these documents and public observations.

### Phase 1 — ACP transport

- implement newline-delimited JSON-RPC transport;
- initialize version negotiation, request correlation, notification routing, stderr handling, and
  bounded process shutdown;
- use a fake ACP child for deterministic malformed-frame, out-of-order-response, timeout, crash, and
  cancellation tests.

### Phase 2 — basic Anthropic gateway

- implement loopback auth, messages validation, basic text prompt conversion, non-streaming response,
  and exact SSE text sequence;
- support one process and one in-memory session;
- add authentication-expiry fallback tests.

### Phase 3 — models and effort

- discover and validate Kiro models;
- implement stable client IDs, reverse lookup, model switching, initial/last-used model policy, and
  catalog cache;
- isolate optional private effort synchronization and capability states.

### Phase 4 — client tool relay

- validate tool registry and aliases;
- implement execution-free MCP child and authenticated Unix control channel;
- map Kiro calls to Anthropic tool-use and exact tool-result continuation;
- add adversarial cross-session, duplicate, malformed-schema, timeout, and cancellation tests.

### Phase 5 — session continuity

- add request-family keys, history digests, delta reconciliation, duplicate/divergence policy, process
  pooling, and persistent `session/load` resume;
- verify simultaneous title/main requests and process crash blast radius.

### Phase 6 — media, web, usage, and launcher UX

- add negotiated media/document prompt support and supported native web mapping;
- add asynchronous Kiro usage, status-line data, bounded turn metrics, and labeled token estimates;
- finish isolated client profile, startup timing, and graceful cleanup.

### Phase 7 — hardening and release

- run all acceptance suites, fuzz framing/parsing boundaries, inspect file modes and child environments,
  generate a dependency license report, and perform a logged-out/expired-token live test;
- package and install on a clean supported macOS host;
- verify uninstall leaves user/client settings untouched.

## 9. Optional later work

NeMo Relay may be evaluated after the standalone core passes acceptance. It is suitable only if it
replaces a clearly bounded transport/session concern without changing client-visible behavior, pulling
in incompatible licensing, or granting remote execution authority. It is not a prerequisite.

Client-to-plain-Claude handoff and richer plugin controls are separate milestones. The first handoff
implementation should be an explicit CLI operation over selected session data, never an automatic copy
of every session.
