# Acceptance specification

## Test strategy

Acceptance is black-box and protocol-driven. Tests may use a purpose-built fake ACP process, a fake
Anthropic client, and live Kiro CLI smoke tests. They must not compare source, fixtures, output text, or
internal identifiers against the previous implementation.

Every test records the public input, observable output, timing class, and resulting lifecycle state.
Live Kiro tests that consume credits must be separately marked and opt-in.

## A. ACP transport

- Negotiates protocol version 1 and rejects a different version.
- Correlates out-of-order JSON-RPC responses to the correct concurrent request.
- Accepts notifications while requests are pending.
- Rejects malformed JSON, non-object JSON, oversized input/output frames, invalid response IDs, and a
  process that exits before completion.
- Redacts credential-shaped stderr and bounds line length and retained line count.
- Recognizes explicit structured and textual 401/login-expiry failures without misclassifying an
  unrelated generic error.
- Rejects every agent permission request and every unsupported agent request.
- Terminates the complete process group after graceful and forceful bounded shutdown stages.

## B. HTTP security and validation

- Binds loopback by default and refuses non-loopback without opt-in.
- Model token authorizes messages/models; UI token does not.
- UI token authorizes only the exact hook/status routes.
- Missing/wrong auth returns 401 without revealing the valid token.
- Body over 16 MiB, non-object JSON, and empty/malformed messages are rejected.
- Accepted TCP connections are bounded before HTTP parsing; excess connections close, partial headers
  and idle keepalives expire, and oversized headers or incomplete unauthorized bodies cannot retain
  unbounded resources.
- Child environments contain no direct provider credential or provider-routing flag capable of bypass.
- Starting and stopping leaves global and project client settings byte-for-byte unchanged.

## C. Anthropic compatibility

- Non-streaming text produces a valid assistant message and end-turn stop reason.
- Streaming text produces the exact ordered event classes and reconstructs the non-streaming text.
- Inline image/document shapes, MIME/header/dimension/count/byte limits and negotiated capabilities
  are enforced; historical images stay native and proven deltas do not resend them. URL/file sources
  cause no fetch. Unsupported citations and media reject before prompt dispatch.
- Tool streaming produces a valid tool-use block and tool-use stop reason.
- Server web search produces compatible use/result blocks and request count when supported.
- Provider token usage remains zero when unreported; estimates appear only in labeled local metadata.
- Recognized auth expiry returns HTTP 200 valid text completion in streaming and non-streaming modes,
  includes an initial fallback header before commitment or a declared terminal trailer after stream
  commitment, instructs `kiro-cli login`, and never attempts another provider. Late expiry finishes
  the existing message without a second message start or an SSE error; visible login text works even
  when the client discards trailers.

## D. Models and effort

- Catalog accepts valid unique Kiro models and rejects empty/duplicate/alias-colliding catalogs.
- Stable client-facing IDs are deterministic and distinct for normalized-name collisions.
- Reverse mapping rejects an ID absent from the current catalog.
- Configured initial model wins the first turn; later client model selection calls set-model while idle.
- Last interactive model is restored without changing the client’s global default.
- Model switch during an active/pending-tool turn is rejected.
- Auto model skips effort.
- Known unsupported effort is skipped; absent `/effort` is unavailable; unknown capability probes at
  most once per model/effort pair; a rejected effort does not fail the turn.
- A model switch clears the effective effort synchronization state.

## E. Tool safety

- Duplicate/empty tool names, invalid object schemas, oversized registries, and unsupported typed server
  tools are rejected before prompting.
- Alias mapping is deterministic, reversible within the session, and collision-safe.
- Kiro sees only session-declared aliases and explicitly supported native tools.
- Relay calls during setup/load or idle time never become client tool-use blocks. Prompt completion
  cannot leave a suspended or validating call to be exposed by another turn.
- Relay child exposes only initialize, ping, tools/list, and tools/call and performs no effect itself.
- Wrong secret, unknown alias, duplicate relay call ID, non-object arguments, invalid schema, queue
  overflow, and wrong-session result are rejected.
- A valid tool call suspends until the exact client result arrives, preserves text/image/error status,
  and completes once.
- Duplicate, orphan, late, partial, extra, or cross-session tool results never reach Kiro.
- Calls arriving after a sealed response stay in a later batch. No result is accepted before its
  successful HTTP delivery, and a rejected set consumes no pending ID.
- A successful tool handoff keeps the same ACP prompt alive across HTTP requests and does not reset
  the original total deadline. Timeout/auth expiry while no response is open still cleans up the
  session; a matching later request observes only the scoped terminal outcome, never tool replay.
- A tool-result request may repeat the complete most recent standing system-message sequence without
  starting another ACP prompt. Changed, partial, reordered or older instructions and extra user text
  reject before any pending result is consumed.
- Cancellation and timeout resolve all suspended relay calls and remove owner-only socket/config data.

## F. Session continuity

- Simultaneous title and main requests receive separate Kiro sessions.
- Main turns 1, 2, and 3 reuse one session and send only new deltas.
- Strictly extending persisted history uses `session/load` and sends only the newest delta.
- Load replay notifications are not emitted to the current client response.
- Failed/inconsistent load invalidates persistence and performs one safe fresh full-history attempt.
- Exact duplicate completed ordinary request recreates before sending.
- Exact duplicate tool result is rejected.
- Truncated history with proven overlap continues; idle divergence recreates; active/pending divergence
  rejects.
- Process compatibility prevents sharing when agent, system policy, tool registry, native tools, or
  launch semantics differ.
- A process crash invalidates every attached session, fails all waiters, and never replays pending tools.
- Rejection of a local invalid model or unsupported prompt does not cancel a healthy active sibling.

## G. Cancellation and load

- Client disconnect before first event, during text streaming, and while waiting for tools each cancel
  and discard the session.
- First-event and total-turn timeouts produce distinct diagnostics.
- SSE keepalives maintain a silent stream without satisfying or extending either model deadline;
  recognized authentication expiry after a keepalive still completes one normal assistant message.
- Repeated caller cancellation cannot interrupt final cleanup or leak an ACP/relay child.
- Shutdown can be called repeatedly and remains bounded.
- HTTP server shutdown cancels active request contexts and joins connection/handler cleanup, including
  the corresponding ACP group. A handler that ignores cancellation must produce a bounded cleanup
  failure rather than a successful join report; launcher shutdown separately owns suspended sessions.
- An attached client has explicit descriptors/environment and bounded ownership. Blocked input/output,
  leader exit with descendants and repeated shutdown cannot retain an owned process group. Foreground
  terminal ownership/settings and existing signal handling are restored after success, failure,
  cancellation and failed exec without closing the caller's descriptors.
- Pool limits and idle/session TTLs hold under concurrency and do not evict active or pending-tool state.

## H. Usage and diagnostics

- Status reads return cached data without waiting for a Kiro usage subprocess.
- Refresh requests coalesce within the 60-second TTL and failure preserves last good/model-only status.
- Status-line polling at five seconds does not create model turns.
- Context percentage, duration, metering units, credits, model, multiplier, and effort status are parsed
  when present and degrade independently when absent.
- Multiple metadata notifications in one turn create only one visible completion metric.
- Title/background turns are excluded; pending metrics are bounded.
- Only final delivered responses publish completion metrics; a tool handoff or canceled/undelivered
  final response cannot do so. UI hook authentication/body rejection consumes no queued record, and
  draining records preserves the latest model-only status when account usage is unavailable.
- Token estimates are deterministic, cache their computation within a bounded cache, exclude declared
  media/thinking content, and never claim actual provider cache hits.

## I. Live release gates

On a clean supported macOS machine:

- installation succeeds without the previous project;
- startup finds Kiro and instructs login when logged out;
- a logged-in text prompt streams successfully through Kiro;
- the Kiro model list appears in the client selector and a model change affects the next turn;
- a client file/shell tool round trip is approved and executed only by the client;
- logout during an existing session produces the graceful assistant fallback;
- resume after restart either loads safely or recreates explicitly without duplicate deltas;
- force reinstall and uninstall are documented and leave user settings unchanged;
- no runtime, build, test, or documentation reference to the previous repository is present;
- dependency license report contains no unapproved copyleft dependency for the intended distribution.

## Release-blocking invariants

Any of these is an unconditional release blocker:

- Kiro or the proxy executes a client tool outside client permission control;
- UI credential can authorize a model request;
- direct-provider fallback is possible;
- a tool result can cross sessions or be replayed;
- partial/canceled Kiro state is reused;
- global/project client settings are modified;
- credentials or raw prompt/tool content appear in default logs;
- a copied implementation artifact or unresolved incompatible license remains.
