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
- Known unmapped stop/sampling/server-execution/container/location/service-tier controls reject
  before HTTP/SSE commitment or backend work with fixed field names and no supplied values. An
  internal caller receives the same rejection before eviction of idle state or consumption of tools.
- Accepted TCP connections are bounded before HTTP parsing; excess connections close, partial headers
  and idle keepalives expire, and oversized headers or incomplete unauthorized bodies cannot retain
  unbounded resources.
- Child environments contain no direct provider credential or provider-routing flag capable of bypass.
- Starting and stopping leaves global and project client settings byte-for-byte unchanged.
- The private client runtime retains standard-HOME user/local MCP declarations and existing
  decisions at native scopes. Installed-client positive/negative controls verify project approval,
  server disable/re-enable, local/project/user name precedence, unchanged source bytes and complete
  observed process cleanup. Copying JSON alone is not interoperability evidence (D64).
- A client-written MCP-named key that declares no server and carries no decision may become a
  reviewed exclusion from that projection (D111). Require an installed-client control that seeds
  the key at the source, keeps every native scope connecting in fresh prepared profiles, leaves
  source bytes unchanged and shows the projected private file without the key. Every other
  unrecognized MCP-related key must still reject.
- The projection also carries the reviewed global flag `hasCompletedOnboarding`, and the client
  environment carries the model token only as the documented Bearer token, so a launch shows no
  theme, security-notes or API-key dialog (D115). Require the projection control to carry the flag
  and not the client's theme or key-approval records, the terminal control to observe zero
  onboarding answers and no dialog or both-variables text, and a malformed flag to reject.
- Plugin source preservation requires an active full-client startup control and disabled/re-enabled
  counterparts, with source tree entries/content/modes unchanged and observed peer cleanup. Finite
  CLI listings and successful peer initialization do not prove later model-visible tool availability
  or a complete plugin tool round trip (D65).
- Plugin tool interoperability requires an advertised declaration, matching result, completed
  continuation and native effect count: one for the owned allowed probe, zero for hook refusal.
  Verify source preservation and joined client/MCP/backend cleanup through the real gateway/relay.
  Distinguish controlled interactive readiness, a warmed private print profile and immediate fresh
  print startup. Passing one does not establish the others or dynamic registry compatibility (D66).
- Native plugin registrations must stay private and bounded while retaining the original read-only
  content source. Verify first-session skill expansion and SessionStart/Stop hooks, plugin disable/
  re-enable and hook suppression against an active native-HOME control. Compare source entries,
  bytes and modes after private uninstall/update/removal. A Git update control must include an
  available owned revision and a native update that actually applies it; a no-op is insufficient.
  Initialization/listing alone cannot establish client readiness: the current controlled interactive
  tool check also observes the client's connected MCP panel. This does not close immediate-input,
  arbitrary dynamic-registry or model-selected skill compatibility (D68). D70 adds the bounded
  wait-to-plugin path below.
- Verify a model-selected owned skill separately from user slash invocation. Validate the advertised
  Skill schema, observe exactly one matching successful result and the expanded body as separate
  client text, and complete through the real gateway/relay with fake ACP. Require preserved sources,
  native hook enable/disable behavior and joined old/replacement process cleanup (D73).
- Personal skills, legacy commands and agent definitions must retain their native user scope in
  the private profile. Compare active natural/prepared runs and a stripped-profile counterfactual;
  verify direct skill/command expansion, personal-over-project skill selection and project-over-
  personal agent selection with equal names. Declaration discovery does not prove agent execution.
  Preserve source bytes/modes and private cleanup. Bound complete snapshots by bytes, entries and
  depth; unsafe links, nonregular sources and exceeded limits reject before partial activation.
  Preserve executable owner bits and exact bytes without parsing frontmatter or running commands;
  exclude unrelated state. Existing profiles keep their snapshot; fresh preparation sees updates (D76).
- Personal output styles must also retain native user scope through the bounded asset snapshot.
  Compare natural/prepared requests for user selection, same-name project definitions, project/local
  setting precedence and Default. Require exact selected style-system-block equality and separate
  project-memory user content, successful replies, unchanged source files and joined cleanup.
  Preserve frontmatter bytes without interpreting them. A keep-coding declaration comparison must
  use observed native behavior, not assumed default coding prose. D108 passes these initial-turn
  controls with actual Claude and a local text backend. UI switching, plugin/managed styles, later
  turns and actual Kiro behavior remain separate checks; this does not satisfy personal memory.
- Personal rules must retain native user scope, original relative-import bases and path exclusions.
  Compare natural/prepared/stripped startup, full four-hop chains with an absent fifth hop and
  conditionally activated rules before and after exactly one successful client Read. Check original
  source trees/import files, groups and private cleanup, including unusual HOME path characters.
  Validate rules with the shared asset bounds before creating runtime artifacts; a native rules
  reference is not an immutable snapshot and is not a filesystem sandbox (D77).
  Full exclusion fidelity also requires a pattern matching only the private rules alias to leave
  naturally included source rules active. D102 reproduces a current failure for user/project/local
  settings: the prepared client drops the rule and its four import hops. Original-path exclusions
  still work. Passing this counterfactual does not satisfy preservation; the compatibility gap
  remains open alongside personal-root memory.
- Personal CLAUDE.md compatibility additionally requires unconditional root-body loading, original
  path exclusions, all four import hops and native ordering without duplicated content. D77's
  explicit counterfactuals demonstrate rejected adapters, not successful product support. This
  acceptance item remains open; the private profile currently omits personal CLAUDE.md.
  D93 also rejects the measured additional-directory candidate: it omits the original root's
  imports and places its body after project instructions. Its passing counterfactual test records
  this incompatibility and matching exclusions, not successful personal-scope preservation.
  Any replacement must also preserve original settings and plugin registrations through interactive
  startup and native plugin management. A no-history print pass is insufficient (D80). Tests named
  counterfactual deliberately reproduce rejected adapter defects and do not satisfy this gate.
  A replacement must handle both original-path exclusion and unintended exclusion of its relocated
  path. Local context-list absence cannot identify exclusion: empty/comment-only roots are also
  absent without a policy match. D97's controls reproduce these distinctions without enabling an
  adapter; effective initial settings alone do not prove later dynamic-policy preservation.

## C. Anthropic compatibility

- Non-streaming text produces a valid assistant message and end-turn stop reason.
- Validate the declared PRODUCT_SPEC.md/D33/D52 Messages subset against the pinned client. Distinguish
  implemented semantics, accepted non-enforced hints and explicit rejections. No general Anthropic
  parity, provider token cap, reasoning equivalence, context edit or JSON Schema guarantee follows
  from accepting a field. Zero max_tokens rejects before model work. Negative client observations
  are not proof of automatic capability recovery, and visible output is never truncated merely to
  claim enforcement of an unmapped token cap while retaining unseen backend history.
- Streaming text produces the exact ordered event classes and reconstructs the non-streaming text.
- Inline image/document shapes, MIME/header/dimension/count/byte limits and negotiated capabilities
  are enforced; historical images stay native and proven deltas do not resend them. URL/file sources
  cause no fetch. Unsupported citations and media reject before prompt dispatch.
- Tool streaming produces a valid tool-use block and tool-use stop reason.
- Server web search produces compatible use/result blocks and request count when supported.
- Provider token usage remains zero when unreported; estimates appear only in labeled local metadata.
- Recognized auth expiry returns HTTP 200 valid text completion in streaming and non-streaming
  modes, includes an initial fallback header before commitment or a declared terminal trailer after
  stream commitment, instructs `kiro-cli login`, and never attempts another provider. Late expiry
  finishes the existing message without a second message start or an SSE error; visible login text
  works even when the client discards trailers. The measured logged-out boundary of the pinned Kiro
  build is one recognized stderr line, nothing on stdout and exit status 1 at `initialize`; require
  the injected classifier to recognize it from a synthetic logged-out HOME and a compiled-command
  control to show the login completion in the client after an ordinary question, with no API-error
  text, and then to answer one more ordinary question in the same client session once the login is
  restored, both after an ordinary completion and after a login lost while a relayed tool call is
  held (D117).

## D. Models and effort

- Catalog accepts valid unique Kiro models and rejects empty/duplicate/alias-colliding catalogs.
- Stable client-facing IDs are deterministic and distinct for normalized-name collisions.
- Reverse mapping rejects an ID absent from the current catalog.
- The prepared client keeps the measured build's handling of product model IDs, which no client
  catalog describes. On a later build that prints an unknown-model window notice, require a
  text-output natural print control that shows the notice, the same arm with the opt-out and the
  prepared profile without it, and a foreground `run` observation without it. JSON output hides the
  notice and cannot serve as the positive control (D112).
- Configured initial model wins the first turn; later client model selection calls set-model while idle.
- Last interactive model is restored without changing the client’s global default.
- Removing one-launch model/effort overrides preserves an otherwise compatible last-model preference,
  while changing profile/agent/version/capability identity still invalidates it. Unknown explicit
  models never fall back. Title/agent/canceled/auth-fallback/incomplete-tool work cannot overwrite
  the preference; a delivered final foreground turn records its actual model through the catalog.
- Prepared cached model discovery avoids an ACP session. Runtime shutdown preserves the model owner
  through response/backend cleanup, then cancels and joins refresh, including startup failure.
- Model switch during an active/pending-tool turn is rejected.
- Auto model skips effort.
- Known unsupported effort is skipped; absent `/effort` is unavailable; unknown capability probes at
  most once per model/effort pair; a rejected effort does not fail the turn.
- A model switch clears the effective effort synchronization state.
- On the pinned Kiro version, the optional effort adapter uses the command shape observed from the
  unmodified terminal. A bounded, no-prompt control selects an advertised model, applies high/low,
  confirms each successful acknowledgement with new matching-session effort metadata, and avoids
  duplicate setting calls. Already queued, foreign, absent or inconsistent metadata cannot establish
  a new setting. Empty-argument choice text is not evidence of current effort. Original Kiro settings
  and owned process/configuration cleanup are checked separately (D99); provider reasoning behavior
  and every model/level combination remain outside this state observation.
- A compiled-command native picker control must display both independently advertised test models,
  distinguish unchanged selection from a successful idle ACP model change, and complete the next
  visible foreground response on the selected model. Failed/unmatched selection acknowledgements
  and auxiliary-only observations cannot prove a switch. After exit, the next diagnostic preflight
  must restore the delivered model without another client/ACP session or source-settings change.
  This fake-ACP control does not replace actual Kiro selection or conversation-resume gates (D85).
  The picker observer acts only on a frame that has settled, parses the client's charset and string
  escapes as non-text, and fails when a confirmation names a different row (D113).
- The actual model-selection experiment uses a fresh advertised catalog and distinguishes rendered
  labels, focused catalog rows and successful inference on the chosen pair. Extra selected rows
  may receive bounded navigation but cannot count as a catalog model or receive Enter. A matching
  idle ACP acknowledgement and active-session answer marker, correlated end_turn and visible client
  response must establish the chosen transition; a later diagnostic must restore the delivered
  model with preserved sources and joined cleanup (D86).

## E. Tool safety

- Duplicate/empty tool names, invalid object schemas, oversized registries, and unsupported typed server
  tools are rejected before prompting.
- Alias mapping is deterministic, reversible within the session, and collision-safe.
- Kiro sees only session-declared aliases and explicitly supported native tools.
- Every relay description identifies its original client tool name and client execution authority,
  preserves the complete source description and exact input schema, and retains the opaque wire name.
  Metadata policy changes invalidate the registry fingerprint used for session compatibility.
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
  reject on that same-prompt path before any pending result is consumed, except the separately
  validated recreation cases below.
- A changed text-only standing system message may recreate after a result-only user message when
  the preceding standing sequence also has exactly one message. Require an exact complete prior-
  history prefix and unchanged owner/model/effort/top-level system/metadata/tool-choice policy.
  A validated changed registry may also recreate, including without a changed system suffix. Validate
  the complete delivered batch, encoded result bounds and full projection before revocation. Wrong,
  missing, duplicate, undelivered or oversized results and truncated overlap reject without mutation.
  Join the old group and relay before creating a replacement with the full request history. Neither
  the supplied results nor new instructions enter the old prompt; no tool is automatically replayed.
  Bound recreations per logical turn (default sixteen), preserve the absolute deadline through setup
  and further tool handoffs, and reject replay after completion or failed replacement. Exact repeated
  suffixes continue normally, including after the restart allowance is exhausted. Multi-message
  changed sequences remain unsupported. Distinguish actual-client/fake-ACP evidence from live Kiro.
- A complete delivered result batch containing at least one success may be followed by nonempty
  client text in the same user message. Require all result blocks first and only text blocks after
  them. Recreate from the complete immutable prefix and full supplied content, including when the
  registry/standing instructions repeat. Preserve policy, original deadline and shared recreation
  budget; validate the projection/result limits before joined revocation. Malformed, reordered,
  empty-only, non-text, truncated or oversized candidates consume no pending ownership. Do not
  revive expired successful work, replay tools or put client text inside a tool result (D73).
- Use distinct message IDs for independent model responses. Compare fresh IDs with a duplicate-ID
  counterfactual before attributing client history regrouping to ordinary tool use. Keep regrouped
  history, prior results mixed into the new delivered batch and truncated replacement prefixes
  rejected. Verify held MCP initialization followed by an advertised wait, a validated registry
  replacement, exact allowed/refused plugin result and completion in the same replacement prompt.
  Require effect counts one/zero, joined old/new groups and unchanged client sources (D70).
  Keep actual backend progress, final model content and client-visible completion separate. A prompt
  echo, early text, hidden control or erased terminal content cannot establish final display. If a
  marker is used, independently prove its final-response provenance and current visibility. Partial
  tool/cleanup evidence does not pass a failed end-to-end live experiment (D72).
- A new user question following all matching client error results may abandon the old prompt only
  with an exact compatible owner, proven history extension, results preceding text, and unchanged
  repeated standing instructions. Join old process/relay cleanup before a fresh full-history prompt;
  never resolve those results into the abandoned prompt, replay a tool or reuse canceled state.
  The same proof may use the bounded five-minute retired outcome. Missing, duplicate, partial,
  successful, cross-owner or divergent results reject on this all-denial/retired-outcome path without
  consuming recovery ownership; active D73 continuations follow the separate rule above.
- Cancellation and timeout resolve all suspended relay calls and remove owner-only socket/config data.
- MCP starts only after authenticated supervisor/child PID checks and verified ACP group membership.
  A peer cannot supply its own PID/group, forge a join, replay an attachment or replace a valid child.
  After binding, a different process cannot submit a tool call using the child's control credentials.

## F. Session continuity

- Completed image tool results must stay native images when recreating ACP context. Preserve the
  original call ID, error status and ordered result content, with separate result boundaries;
  base64 embedded only in text is a failure. Enforce negotiated capability and combined media
  limits before dispatch, without changing stored history or resending committed images in a delta.
  With actual Claude and independent ACP, require one native Read and its matching image result,
  remove the owned source image after joined cleanup, then resume the same native ID using fresh
  resources. Require the same result/image bytes in the request and ACP prompt, no repeated Read
  hooks, successful completion and preserved settings. D109 passes this finite PNG case; actual
  Kiro image interpretation, other formats, sidecars and broader media retention remain separate.
- A new tool after an interrupted native history must be checked separately from resuming completed
  work. Join the original delivered-but-unexecuted hook/client/backend before new admission, and
  require the old effect to remain absent after late hook release and throughout the new turn.
  Retain the exact old question and measured partial text/native non-completion placeholder before
  the exact new question in both resumed requests; do not manufacture the omitted old tool pair.
  A later build may instead retain the unfinished pair itself with a fixed error result and a fixed
  continuation line before the same placeholder (D113 measured this for 2.1.267 without partial
  text). Both the HTTP witness and the independent ACP peer must classify the same measured
  representation and reject any other; neither form is a successful result. A same-session new
  question after keyboard interruption of a held tool carries the old tool_use and the fixed
  interruption text without any tool_result (D116).
  Require a distinct new call and matching success/refusal under current client policy, with
  independent effect/hook witnesses and joined new cleanup. Old history cannot substitute for
  the new result. D98 supplies no-text/partial-text native-client controls with independent ACP
  for allowance, configured denial and hook veto, plus one actual Kiro/Claude allowed-operation
  episode. D107 adds native-terminal one-time Bash approval, refusal with a comment and hook veto
  for both forms using independent ACP. Require the expected interrupted-history proof and old
  effect/hook cleanup after late release before every permission decision; completed-history
  evidence cannot substitute. The new result must complete visibly, with joined ownership and
  unchanged source policies. Actual Kiro interactive prompts/refusals after interrupted history,
  uncertain effect windows and concurrent/multiple tools remain separate gates.
- A distinct new tool after completed-history resume must obey current client settings and hooks.
  Preserve the old successful pair before the exact new question and again in the result request;
  require a new ID, exact new operation and matching success/refusal, including the hook reason.
  Old effects/hooks stay one. A new allowed append and its hooks occur once; refused targets and
  success hooks stay absent. Join fresh and old ownership and preserve both source policies. D92
  covers finite Bash allowance, configured denial and hook veto with actual Kiro/Claude. D106 adds
  actual Claude/independent-ACP interactive one-time approval, comment refusal and hook veto after
  completed-history resume. Before typing, require the current exact operation and selected choice,
  a delivered fresh handoff with proven old history, and no new effect. A previous call ID, missing
  handoff, changed session or already-returned result cannot authorize input. Require current final
  display, matching result and joined recorded terminal/client/backend ownership. Hook veto must
  complete without a permission decision. This does not establish actual Kiro interactive resume,
  keyboard exit or unfinished-operation approval; D98 separately covers interrupted history.
- A finite native restart after cancellation at a delivered PreToolUse wait must join the original
  hook/client/backend ownership before a fresh resume. Require no original effect/result, including
  after a late release marker, and retain the exact original question and observed partial text or
  native non-completion placeholder before the explicit non-executing follow-up. Do not manufacture
  a tool result when the native client omits the unfinished pair. D91 covers one actual Bash wait
  and independent release/no-text/partial-text controls. This does not establish an explicit native
  cancellation notice, implicit/interactive continuation, new tool permissions or arbitrary crashes.
- A completed client Write or Bash must remain historical context after native explicit-ID resume.
  Require one matching original tool ID/name/decoded input and successful result with exact text
  content, ordered before the new user instruction. Observe native effect counts and PreToolUse/
  PostToolUse receipts independently; an append effect and both receipts remain exactly one after
  the resumed text turn, with no new tool handoff. Each stage uses a fresh joined backend/profile/
  endpoint/token while retaining the native session ID. D90 covers these two finite single-tool
  cases with actual Kiro/Claude, plus corrupted-history and repeated-effect controls. Interrupted
  pending work, new policy decisions after resume, interactive/default-tool variants, concurrent
  writers and uncertain effect/acknowledgement windows remain separate checks.
- Optional native client history survives removal of its temporary configuration. Explicit-ID resume
  uses a fresh endpoint, credential and backend owner; previous completed text appears once in the
  subsequent request. The native client remains the only transcript writer. Default ephemeral
  operation and native persistence suppression remain negative controls. Reject malformed resume IDs
  before finite startup commands, and reject unsafe source directory roots without following them.
  Verify source settings and joined runtime cleanup separately from intentionally retained native
  data. D87 covers finite text resume and D88 covers compiled foreground run with explicit-ID resume,
  a restored answer visible before new input, correlated ACP context/completion and keyboard cleanup.
  D89 adds selection of a named conversation through the native /resume screen with shared owned
  HOME/project/proxy state, confirmed selected row, restored old answer and a successful new turn.
  Continue/latest, cross-project selection, pending tools, media/checkpoints, concurrent writers and
  retention behavior remain broader gates.
- Concurrent native histories sharing one HOME/project must retain each distinct session without
  importing another session's context. Require both native clients and their independent ACP owners
  live before releasing the first response in each initial/resumed round. Join every initial owner
  before new resume profiles/endpoints/credentials are created. Each resumed request must contain
  its own question, answer and new input once and in order; reject foreign markers in system or
  messages. Verify both native transcripts, unchanged source settings, absent routing secrets and
  joined resources. D104 covers two distinct text sessions/four turns using actual Claude and fake
  ACP. Same-session writers, tools, interactive selection, real Kiro and long-duration retention
  remain separate checks; the proxy still never parses or edits native transcript formats.
- Same-ID native resume observations must separate retained transcript markers from the conversation
  actually sent in a later public request. Compare sequential controls and both controlled completion
  orders for overlapping writers against an ordinary-config reference using the same routing setup.
  Require live pending writers before releasing replies, joined old owners before readback, exact
  marker roles/order, preserved prepared-profile sources and removed routing/process resources.
  D105 matches the native reference in eight finite text episodes: both replies remain stored, but
  overlapping writers need not contribute both completed replies to later context. This is not a
  successful merge guarantee, same-ID isolation or evidence about tools, interactive UI or actual Kiro.
- Simultaneous title and main requests receive separate Kiro sessions.
- The combined title classifier accepts explicit disabled thinking or omission with all other
  title signals present. Ordinary structured output, loose title mentions, tools and explicit
  enabled/adaptive, null or malformed thinking cannot capture the main family.
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
- A prepared launch reserves capacity before creating its policy artifacts and serves only one
  lifetime session. Compatible turns retain that session; a changed tool policy replaces the owned
  launch without interrupting an unrelated binding. Partial preparation failure revokes its relay.
- A process crash invalidates every attached session, fails all waiters, and never replays pending tools.
- Observe process loss before a client tool handoff separately from ordinary cancellation. Require a
  verified owned-group termination, joined old cleanup, an actual backend error with the caller
  context still live, and a client-visible failure before admitting a new independent request.
  Verify a new process completes that request without tool effects. A repeated client UUID alone
  does not prove historical continuation or identical native system context (D75).
- Rejection of a local invalid model or unsupported prompt does not cancel a healthy active sibling.

## G. Cancellation and load

- Client disconnect before first event, during text streaming, and while waiting for tools each cancel
  and discard the session.
- A bare permission refusal after a completed HTTP tool handoff may emit no immediate model request
  or hook. Silence alone is not observable cancellation. Existing absolute tool/turn deadlines and
  launcher exit still retire the suspended owner; no shorter heuristic deadline or invented result
  substitutes for a signal. A later request follows the exact new-turn proof in section E.
- First-event and total-turn timeouts produce distinct diagnostics.
- SSE keepalives maintain a silent stream without satisfying or extending either model deadline;
  recognized authentication expiry after a keepalive still completes one normal assistant message.
- Repeated caller cancellation cannot interrupt final cleanup or leak an ACP/relay child.
- Shutdown can be called repeatedly and remains bounded.
- Relay lifetime loss cancels blocked stdio and pending tools. A surviving relay produces a retained
  cleanup error; idle release retires the owned ACP group and repeated idle/final shutdown joins that
  result. A driver with failed cleanup cannot be reused.
  Closing the relay socket must immediately revoke accepted connections and wake idle lifetime
  readers, without waiting for an artificial connection deadline. Measure after authenticated
  readiness; concurrent Close callers must join handlers/peer disappearance and remove private
  artifacts while leaving a healthy ACP owner alive. Retain the separate bounded peer-exit check
  and failure for a surviving peer (D96).
  Manager eviction/pruning retains the failure, prevents further admission/discovery and joins it
  during final shutdown even after the failed binding has been removed from its map.
- HTTP server shutdown cancels active request contexts and joins connection/handler cleanup, including
  the corresponding ACP group. A handler that ignores cancellation must produce a bounded cleanup
  failure rather than a successful join report; launcher shutdown separately owns suspended sessions.
- An attached client has explicit descriptors/environment and bounded ownership. Blocked input/output,
  leader exit with descendants and repeated shutdown cannot retain an owned process group. Foreground
  terminal ownership/settings and existing signal handling are restored after success, failure,
  cancellation and failed exec without closing the caller's descriptors.
- Pool limits and idle/session TTLs hold under concurrency and do not evict active or pending-tool state.
- Exercise repeated concurrent HTTP completion, same-session streaming, cancellation and fresh-session
  admission through independent ACP processes. Require live response readiness before cancellation,
  exact continuation ownership/deltas, joined recorded groups and zero remaining handlers/connections
  before each next wave. Observe OS descriptors and post-GC Go heap/goroutines with a declared finite
  budget, and reject observer controls with retained descriptors, wrong history or terminal output
  substituted for cancellation. D94 covers 64 waves/1,024 requests with eight concurrent sessions;
  its short text-only fixture run does not complete actual-client, pending-tool or long-duration soak.
- Also exercise concurrent delivered tool batches after their HTTP responses finish. A syntactically
  valid request pairing another session's call ID must reject while all original pending owners
  remain available. Matching denials plus new questions must join old ACP/relay ownership and preserve
  exact original history in fresh responses without another handoff. Bound idle eviction and final
  cleanup across repeated waves; observe relay PID/group membership and config/directory removal
  independently of pool counters. D95 covers eight single-call sessions for 32 waves, including
  steady descriptor/Go-goroutine counts and a declared heap envelope. Multi-call batches, real clients,
  prepared policy, shared ACP processes, native RSS and long-duration soak remain separate checks.
- Prepared policy cleanup occurs once after ACP/router shutdown and before releasing capacity.
  Repeated idle release joins the same cleanup result. A retired cleanup failure remains visible to
  pool shutdown and prevents admission of further launch artifacts.
- Client runtime exit/cancellation joins HTTP, backend and usage owners, including an ACP turn waiting
  for tool results after its HTTP response ended. Schema shutdown and profile removal follow these
  joins. Startup failure also closes transferred owners, preserves caller descriptors/source settings,
  and reports cleanup failure without disclosing arbitrary adapter error text.
- Separate completed HTTP handoff evidence from tool exposure when observing suspended runtime
  cancellation. Require a live held client hook and WaitingTools state before repeated parent
  cancellation, then verify the launcher joins its client, hook, backend, relay, pool and private
  artifacts without another model request (D74). A print-wrapper runtime observation does not
  establish foreground terminal Ctrl+C or disconnect during an active HTTP response.
- For the compiled foreground command, type Ctrl+C only after an active main ACP response and
  generated text visible in the actual client are both observed. Require a forwarded ACP cancel,
  retirement of its owned group and a live client/proxy before keyboard exit. Normal completion
  and ordinary-character fake-ACP controls must not cancel. Observe keyboard exit confirmation,
  terminal restoration, recorded process/group disappearance, closed listener, removed private
  artifacts and unchanged sources (D82). Separate title work cannot satisfy main-response readiness.
  This does not establish a following question, exit during a held hook or unobserved descendants.
- Observe compiled-command keyboard exit during an exact live native PreToolUse wait separately
  (D83). First release a matching fake-ACP control and require the native tool completion, matching
  PostToolUse/result and displayed final response. In the exit case, keep the hook unreleased,
  type the documented exit/confirmation keys and require bounded ordinary exit, removed observed
  hook/client/ACP groups and private artifacts, restored terminal and unchanged sources. Hook expiry,
  direct signals and emergency cleanup cannot count as keyboard success. A late release marker
  must not produce an observed result or new prompt during the bounded post-exit check.
- For same-client recovery after streamed keyboard interruption, admit a new distinct question
  only after the old main group is gone and the original client/proxy remain live (D84). Require
  unchanged recorded owner/profile/address, a follow-up ACP group absent from the prior observed
  group set, observed old/new instruction fragments, correlated end_turn and a generated answer visible
  in the current client screen. Titles and automatic repeats cannot satisfy the new main turn.
  Observe partial-response retention without inventing history. Verify ordinary keyboard exit and
  cleanup afterward; this does not establish tool-result recovery or persisted restart/resume.
- For the same recovery after keyboard interruption of a held tool, require the interrupted
  PreToolUse hook with no delivered relay result or PostToolUse hook, the old prompt's ACP group gone
  after the new question, a fresh follow-up group, the recorded interrupted-history counts with no
  `tool_result` lacking `is_error`, and the fixture content absent from the screen (D116). The
  measured 2.1.267 same-session shape carries the old tool_use and the fixed interruption text only.
- Public startup rejects invalid binaries/settings, failed login, unknown configured models and an
  unverified execution policy before launching a model-facing runtime. There is no CLI/config trust
  override; a Claude Code build sharing the measured major version is admitted but reported as
  unmeasured (D110). Diagnostic success reports launch availability separately. Startup
  cancellation joins
  catalog refresh before closing its command runner or removing private runtime files; replaced roots
  are preserved with a cleanup failure. Stable scope keys never regenerate from malformed state.

## H. Usage and diagnostics

- Status reads return cached data without waiting for a Kiro usage subprocess.
- Refresh requests coalesce within the 60-second TTL and failure preserves last good/model-only status.
- Status-line polling at five seconds does not create model turns.
- Native status rendering must show a usable cold view while a refresh is held, then the reported
  used and optional limit values after completion. A failed later refresh retains those values
  with a stale marker; cancellation joins the held refresh without inventing data. Check the current
  terminal screen, not erased history, and distinguish synthetic cache controls from actual account data.
- The temporary client status command reads only its private UI credential, never client stdin or
  model/provider credentials, and cannot redirect that credential or select a different HTTP route.
  Invalid configuration, remote/hostname endpoints, proxy variables, oversized responses and stalled
  input/output remain bounded. Display text contains no arbitrary upstream diagnostic or identity.
- The status command preserves source settings and is removed with the client runtime. A completed
  foreground turn remains visible without account usage; a late helper cannot recreate removed
  runtime files. Installed-client UI evidence must be distinguished from a direct helper invocation.
- Existing user/project/local status commands must win over optional product display defaults.
  Verify the selected command executes and renders, lower-priority/product commands stay inactive,
  and an event-only command does not inherit a product refresh timer. Suppress the entire optional
  default on uncertain source resolution while retaining mandatory routing and separate hooks (D67).
- Startup diagnostics distinguish process launch, individual hook execution and visible UI from
  session initialization. A held-hook control must not let an earlier callback or status render
  pass a full-readiness assertion. Disabled optional hooks must not make healthy startup fail.
- The startup model-notice hook uses only the exact UI-authenticated model-capabilities POST and the
  prepared launch model. It cannot discover/select a model, refresh usage or consume completion
  metrics. Unsupported request bodies and model credentials reject before exposing the notice.
- Its synchronous `startup` hook emits only bounded `systemMessage` JSON. Unverified model support
  and unreported provider usage stay explicit; malformed responses cannot inject text or context.
  Existing user/project hooks and permission rules remain effective. An installed-client control
  verifies the notice is absent from model input, and disabling optional hooks preserves conversation
  completion and tool denial. Timeout, blocked output and late invocation obey the UI-helper bounds.
- Context percentage, duration, metering units, credits, model, multiplier, and effort status are parsed
  when present and degrade independently when absent.
- Multiple metadata notifications in one turn create only one visible completion metric.
- Title/background turns are excluded; pending metrics are bounded.
- Only final delivered responses publish completion metrics; a tool handoff or canceled/undelivered
  final response cannot do so. UI hook authentication/body rejection consumes no queued record, and
  draining records preserves the latest model-only status when account usage is unavailable.
- A completion hook racing terminal-delivery bookkeeping waits at most 200ms for its initial
  pending-delivery snapshot. Overlapping finalizations remain independent; timeout/cancellation
  consumes no queued record, failed writes publish nothing and release their wait registration.
  Status/model-capabilities reads remain independent, and later model arrivals cannot extend the wait.
- The synchronous `Stop` helper reads only the private UI configuration and exact metrics route,
  and emits at most 9 KiB of `systemMessage` JSON for all retained records. An empty/unavailable or
  malformed page emits no notice, model context, prompt or continuation/permission decision.
  The installed client must show two distinct foreground completions without title metrics or
  notice text in the next model input. Existing user/project Stop hooks and Read denial remain
  effective; disabling hooks preserves conversation and tool refusal. Blocked output, unread stdin,
  late invocation and source-setting cleanup retain the UI-helper bounds.
- Token estimates are deterministic, cache their computation within a bounded cache, exclude declared
  media/thinking content, and never claim actual provider cache hits.

## I. Staged live gates

| Stage | Required evidence |
| --- | --- |
| Development run | Applicable independent transport/security/tool/cleanup checks; pinned Kiro restricted inventory and attempted native-effect denial; effective exclusion of inherited configuration; client-approved file/shell effects, client denials and hook vetoes; source settings preserved and owned processes/artifacts removed |
| Internal alpha | Development evidence plus live cancellation, process-loss recovery, authentication expiry, model selection and safe restart/resume for each enabled path |
| Release candidate | Full acceptance for the supported product, parallel soak and FD/process/memory checks, dependency/rights review, and clean macOS install/uninstall |

An inactive inheritance positive control cannot establish effective exclusion. A client-hook denial
alone does not establish native-tool restrictions or approved execution. Bind evidence to the tested
CLI/engine and effective execution policy; keep unverified paths disabled. Unknown/new versions must
not inherit verification automatically. No trust override may stand in for this evidence. D110
records one directed exception: a Claude Code build sharing the measured build's major version is
admitted without new measurement, reported as unmeasured, and evidence gathered on it is recorded
with its observed version. Making a build measured requires the full installed-client regression on
that build with the same witnesses (D113 did this for 2.1.267). D114 records the same directed
exception for Kiro: a main/helper pair reporting one build whose major version equals the measured
pin's is admitted, reported as unmeasured, and runs the measured development policy; other majors and
mismatched pairs reject. Making a Kiro build measured requires fresh finite account/catalog and
read-only isolation checks on that build (D114 did this for 2.21.3).

Optional private metadata, account usage, native web support and full Anthropic option parity are
not conditions for development run. Their absence must not interrupt supported ordinary turns.
Required core security/cancellation/cleanup behavior is not postponed to soak testing. These stages
do not mark an unfinished product complete, and credit-consuming tests remain separately opt-in.

### Live release checks

On a clean supported macOS machine:

- installation succeeds without the previous project;
- startup finds Kiro and instructs login when logged out;
- a logged-in text prompt streams successfully through Kiro;
- the Kiro model list appears in the client selector and a model change affects the next turn;
- a client file/shell tool round trip is approved and executed only by the client;
- logout during an existing session produces the graceful assistant fallback (D117 verifies the
  fallback and the session's recovery after a restored login with the measured logged-out boundary
  reproduced by the fixture; an actual logout during a live session remains a manual check);
- resume after restart either loads safely or recreates explicitly without duplicate deltas;
- force reinstall and uninstall are documented and leave user settings unchanged;
- no runtime, build, test, or documentation reference to the previous repository is present;
- dependency license report contains no unapproved copyleft dependency for the intended distribution.

Dependency evidence must identify the exact binary and separate its selected package/module graph,
embedded/generated data, native components and build/test tools. A frozen byte-inventory verifier
must reject changed/missing inputs, exceeded bounds, unsafe paths/file types and fabricated release
clearance. Passing a snapshot/cache comparison does not establish new-source reachability, complete
attribution, absence of vulnerabilities or distribution rights (D78).

The per-user development installer must copy only its own executable and retained notices into an
owned private generation. Independent tests must cover normal install, explicit force replacement,
self reinstall/uninstall, repeated uninstall, source/settings preservation and exact notice bytes.
Unknown or changed files, unsafe ownership/modes/links, missing markers/locks, exceeded bounds and
invalid sources must reject without overwriting those contents. Active executable/helper leases
must refuse replacement/removal; interrupted startup may fail explicitly during mutation. Ordinary
pre-publication failure/cancellation must clean this invocation's provably owned staging files and
keep the previous executable. Process-exit tests must separately demonstrate recovery before and
after publication, with no rollback of an already published generation. An ambiguous partial stage
is preserved and reported. Passing these checks in a temporary HOME on the development host is not
the clean-host release check above (D79).

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
