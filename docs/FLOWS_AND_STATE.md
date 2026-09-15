# Flows and state machines

## 1. Runtime topology

The runtime consists of a launcher, a loopback HTTP gateway, a session manager, a bounded pool of Kiro
ACP processes, and one execution-free relay child per Kiro session.

The client talks only to the loopback gateway. The gateway owns conversation reconciliation and maps a
client conversation to a Kiro session. Kiro sends model text and tool-call intentions through ACP. The
relay converts a Kiro tool request into a normal client tool-use response, then waits for the client to
execute and return the result.

## 2. Startup flow

1. Resolve the Kiro CLI and client executables.
2. Load product settings and create an isolated runtime directory.
3. Run a bounded, lightweight `kiro-cli whoami --format json` login check.
4. Resolve the cached model catalog. A compatible cache can be served immediately; stale data triggers
   refresh without blocking when safe.
5. Generate independent model and UI tokens.
6. Bind the gateway to an ephemeral loopback port.
7. Create a temporary client profile/plugin configuration without changing global or project
   settings; the launcher's only later source-file write is the D118 workspace-trust answer in step
   10.
8. Sanitize the client environment, then inject only the local base URL, ephemeral model token, local
   hook/status data, and required profile paths.
9. Launch the client and wait for initial session readiness.
10. On shutdown, stop accepting requests, cancel active sessions, close relay children and ACP
    process groups, remove ephemeral runtime files, and leave persistent caches intact. After the
    client exits, and only when this launch's private profile records a yes answer to the client's
    workspace-trust dialog for the launch project while the user's `~/.claude.json` still matches
    the launch-time read and lacks that value, splice exactly
    `projects[<key>].hasTrustDialogAccepted: true` into it (D118); every doubt skips the write.
    D125 stages that splice before acquiring the client lock path, then revalidates source bytes,
    file and directory identities and publication age before rename. Existing locks are left
    untouched. This coordinates cooperating writers but is not atomic against unlocked writes.

Optional startup timing reports each phase separately. The login check has its own timeout and cannot
hang the launcher indefinitely. D131 retains that account command's deadline, cancellation and
cleanup causes. Partial identity output cannot establish a successful check. A deadline gets an
account-check timeout diagnostic; other execution failures get a failed-check diagnostic. A
completed nonzero exit or malformed identity keeps the existing unverified-login result. Cleanup
failures remain visible alongside the primary failure, and no failed check reaches execution policy.

Individual SessionStart callbacks, hook completion and visible status are distinct observations.
Neither callback arrival nor status rendering establishes step 9. Optional hooks may be disabled
without making startup fail. D48 records the observed ordering; full interactive readiness remains
unverified, and process launch must not be reported as session initialization.

## 3. Conversation identity

A session binding key has three layers:

- profile scope: stable identity for the selected Kiro home/profile and authenticated context;
- explicit client session/agent identifiers when present;
- request family discriminator so session-title work cannot capture the main agent session.

For explicit client identities, main/tool/resume/retry requests use one stable lookup key; first-user
continuity is checked through the stored history anchors. Rehashing the first visible user on every
request would split a safely truncated conversation. Title keys include first-user and system digests
in their separate family. Decisions D15-D17 define the exact normalization, pool and persistence rules.

When the client provides no stable session identifier, use a deterministic fallback derived from the
profile scope, launcher instance, stable system context, and first user content. The key is an internal digest in logs; raw
prompt text is never used as a visible identifier.

Request families include at least main agent, session title, tool follow-up, local command, resume, and
retry. Session-title work is isolated and not persisted as the main conversation.

## 4. Session lifecycle

Session states are: unstarted, starting, idle, prompting, waiting-for-tools, canceling, unhealthy, and
closed.

Allowed transitions:

- unstarted to starting to idle after ACP session creation or loading;
- idle to prompting after model/effort synchronization and prompt dispatch;
- prompting to waiting-for-tools when Kiro requests client tools;
- waiting-for-tools to prompting when the complete matching result set arrives;
- waiting-for-tools to joined retirement and a fresh session when D57 proves a complete matching
  error-result batch followed by a new question; a bounded retired outcome can establish the same proof;
- prompting to idle on successful end-turn;
- waiting-for-tools to joined retirement and a fresh session for a changed standing message,
  validated registry, or D73's successful results followed by client text; require the complete
  prior-history prefix and delivered batch under the original deadline and shared recreation limit;
- any live state to canceling on client disconnect, timeout, or explicit cancellation;
- transport/protocol/auth failures to unhealthy;
- canceling or unhealthy to closed after cleanup.

Model switching and ordinary prompts on a retained session are allowed only in idle. Tool-result
continuation is allowed only in waiting-for-tools. D57 recreation requires matching ownership, proven
history extension and unchanged standing instructions. It never supplies results to the old prompt
or reuses its partial state. A bare UI refusal with no new HTTP or hook event remains pending until
an existing deadline or launcher exit. A session is never persisted while prompting or waiting for tools.

Idle sessions expire after a configurable TTL, initially one hour. Expiration runs lazily and only when
there is no active turn or pending tool. Each session key has its own lock; unrelated keys remain
concurrent.

## 5. Process pool

An initialized Kiro ACP process can host multiple compatible sessions. Compatibility includes the
restricted agent definition, system context/behavioral policy, client tool registry fingerprint, native
tool selection, and launch configuration that changes Kiro behavior.

Default pool policy:

- at most two sessions per process;
- retain at most two idle processes;
- expire an idle process after five minutes;
- serialize `session/new` briefly on a shared process so notifications emitted before its response can
  be attributed correctly;
- allow established sessions on the same process to issue concurrent JSON-RPC requests.

The initial global process cap is four, including starting and retiring groups. These limits are
configurable and should be benchmarked rather than treated as protocol constants. Without negotiated
session deletion, a disposed idle session's backend allocation is not recycled within that process.
Releasing one idle binding preserves active siblings; cancellation or corrupt shared state retires
the whole group. Idle capacity may be retired to admit a different compatible process configuration.

Retire the process on ambiguous session creation, malformed model/session response, transport failure,
or a failed load that can leave routing ambiguous. A crash makes all sessions attached to that process
unhealthy. A later independent request may create a fresh process and session; pending tool calls are
never replayed.

## 6. Request classification

Session-title requests are detected from a combination of output JSON schema shape, title-specific
system instruction, absence of tools, and either an explicit disabled-thinking declaration or an
omitted thinking field. Omission alone is not a title signal; every schema and purpose condition is
still required. Explicit enabled/adaptive, null or malformed thinking does not match. D50 records
the installed-client observation behind accepting omission. Detection must not rely on a single
loose substring or infer a backend's reasoning behavior from classification.

Local usage/diagnostic commands require exact recognized request shapes. They bypass Kiro and are
removed from later history. All other requests are main agent or tool follow-up based on the newest user
content and current pending-tool set.

## 7. History reconciliation

The gateway does not resend the complete client transcript on every turn.

For each user turn, it records a digest pair representing the immediately preceding assistant content
and that user content. The ordered digest sequence is compared with the in-memory or persisted session
history.

Rules:

- strict extension: send only the newest uncommitted delta;
- truncated client history: accept when a longest safe suffix/prefix overlap proves continuity;
- exact duplicate ordinary request after completion: create a fresh session before sending, preventing
  the same committed delta from being applied twice;
- exact duplicate tool result: reject;
- divergence while idle and without pending tools: recreate once and send full current history;
- divergence during an active turn or with pending tools: reject;
- a same-prompt tool-result continuation must match the current registry/system compatibility and
  contain all and only the IDs in the latest sealed, successfully delivered batch. D70's separate
  replacement path accepts a validated registry change only after complete prior-history proof.

History is committed only after a successful completed Kiro turn. A request that begins a new turn first
invalidates the previously persisted idle snapshot so two processes cannot claim the same backend
session.

The public `max_turn_requests` completion is eligible for the same successful finalization (D130).
It maps to `pause_turn`, retains the exact emitted text and returns to idle without issuing another
ACP prompt. An explicit next user question uses the normal continuity proof and sends only its
uncommitted delta. A failed HTTP delivery still retires the affected turn; a pending tool batch
cannot be completed by this stop reason.
Its successfully delivered foreground response also records the actual model for the next
interactive launch under the existing catalog and preference rules (D138).

Ordered assistant/user digest pairs are accompanied by message-role anchors, including per-message
system updates. This detects changes that a pair alone would miss. A truncated overlap must include
both user and assistant content and end at the last delivered assistant. Assistant-only overlap does
not establish continuity. Pending tool continuation permits the next matching result message, optionally
followed by an exact repetition of the complete system-message sequence immediately before the last
delivered assistant handoff. The repeated keyed anchors are retained for reconciliation without
resending those already-present instructions to ACP. Older, partial, reordered or changed sequences
reject on this same-prompt path. New system or user text cannot be injected into the already-running
ACP prompt through that path.

D70 extends D63's bounded fresh-session path to changed tool registries. The latest user message
must contain the exact delivered tool results, with only D73's eligible trailing text permitted;
owner/model/effort/top-level system/metadata and tool-choice policy stay unchanged. Validate the new
registry before any state mutation. For changed
standing instructions, the preceding standing sequence must have one system message, and the new
suffix must be one text-only system
message. All prior message anchors must match as a complete prefix; ordinary truncated-overlap
acceptance does not authorize this transition. Result encoding and the complete new projection are
validated before revocation. Old relay calls receive cancellation, never the actual supplied result,
and old process cleanup joins before replacement setup. The replacement preserves all supplied
history and instructions but not hidden backend context. It inherits the original deadline and the
same per-turn reconstruction counter, bounded to sixteen by default. A failed replacement cannot
replay consumed ownership, and budget exhaustion does not prevent an otherwise compatible same-prompt
continuation. Existing multi-message standing-sequence rules remain unchanged. Cumulative/regrouped
call/result histories still reject: D70 proves D69's grouping was caused by reused fixture message IDs.

D73's trailing-text case requires at least one successful result and all result blocks before one
or more text-only blocks, including nonempty text. It forces full-history recreation even with
unchanged registry/standing instructions. The new client content stays separate from tool output.
The original turn deadline and shared recreation budget still apply; success plus text cannot revive
a retired turn. All-denial/new-question recovery retains its separate rules and retired-outcome bound.

## 8. Persistent resume flow

The persistent record contains no prompt or response text. It contains the Kiro session ID, profile and
compatibility fingerprints, selected/current model metadata, ordered history digests, timestamps, and
enough version information to reject incompatible data.

Save atomically with owner-only permissions only when idle. Use an exclusive ownership lock when
loading or invalidating the record.

Resume flow:

1. Require an explicit stable client session ID and matching profile scope.
2. Require compatible agent/tool/system/model metadata.
3. Require the incoming history to strictly extend the persisted digest sequence.
4. Start a compatible ACP process and call `session/load` with a fresh relay.
5. Discard replayed historical notifications during load.
6. If load succeeds consistently, send only the newest delta.
7. If load fails, invalidate the record, create a new session, and send one full safe history.

## 9. Model and effort flow

On first session creation, an explicitly configured launch model/effort wins over client defaults. For
later turns, a client model selection is resolved strictly through the current catalog. If it differs
from the Kiro current model, switch while idle. A switch clears the remembered effort state.

Effort synchronization then runs:

1. normalize the requested effort;
2. skip empty or already successfully applied pairs;
3. skip Kiro `auto` model selection;
4. skip known unsupported model/effort pairs;
5. if private commands were advertised without `/effort`, record unavailable; without any valid
   advertisement before dispatch, record unknown and skip;
6. after `/effort` is advertised, execute the private effort command once for an unknown pair;
7. cache only a confirmed result or known capability decision;
8. on application rejection, record the attempted pair, warn and continue with current Kiro effort;
   transport corruption/failure still retires the process. Model switches clear effective state,
   while the version/configuration-scoped rejected-probe ledger remains bounded and intact.

The status line distinguishes supported/current, unsupported, unavailable, unknown, and configured
initial states.

## 10. Tool relay flow

1. Validate the request’s tool declarations and build a deterministic registry.
2. Use opaque aliases for Kiro's tool wire names, retaining the original client name in metadata.
3. Launch a session-specific relay MCP child whose tools/list exposes those aliases.
4. Kiro calls an alias through the relay.
5. The parent authenticates the control request, validates the schema, and creates a unique client
   tool-use ID.
6. The gateway seals the calls included in the response and completes the current Anthropic response.
   Successful HTTP delivery leaves the owned ACP prompt alive. Calls arriving later remain queued.
7. The client applies its normal permission/hooks, executes the tool, and sends a tool result.
8. The gateway validates the entire sealed result set and exact session ownership before resuming any
   suspended relay call. Kiro continues the same prompt; no second `session/prompt` is dispatched.

Alias generation is deterministic from the original tool name, uses a cryptographic digest prefix, and
extends the prefix on collision. The registry fingerprint includes original name, alias, description,
input schema, and native-tool choices.

Each relay description identifies the original client tool name and states that execution is decided
by client permissions and hooks. The complete original description follows this attribution. The
opaque wire alias and input schema stay unchanged. Registry identity includes the metadata policy
version, so sessions prepared under an earlier metadata contract cannot share its fingerprint.

## 11. Cancellation and failure flow

Client disconnect before turn completion cancels the Kiro session and discards it. Timeout before the
first event and timeout for the total turn are distinct diagnostics. A tool timeout completes the relay
call with a tool error and prevents late result reuse.

Relay cancellation checks the exact pending call under the same lock as result resolution
(D128). Once its result is committed, late cancellation cannot change that result or retire newer
work. Cancellation before resolution retains complete prompt retirement, including queued,
sealed and delivered calls; it does not selectively remove a delivered tool ID.

D129 records the sealed batch's candidate history digests and IDs under the retirement lock before
exposing it. This does not commit idle history or enter waiting-for-tools; successful response
finalization still authorizes normal result delivery. An intervening abort retains the candidate's
terminal error. Result-only retries must prove the same compatible owner, complete IDs and history
extension/standing suffix as active continuation. The existing all-denial/new-question proof can
start fresh work after joined retirement; a late old Finish cannot replace that new owner.

D137 distinguishes an active response from a terminal response awaiting delivery bookkeeping.
After exposing End, the driver permits one following Start to wait under its existing start gate.
The wait ends on caller cancellation or after Finish/joined abort updates the preceding owner.
It neither resolves tools early nor commits history; normal compatibility and result-set checks
still follow successful delivery. An additional overlapping Start remains busy. Canceling only
the waiter leaves the preceding response intact, and no new deadline extends its owned turn.

Settlement captures known failure state before cleanup changes it: confirmed authentication wins,
then an already-recorded tool deadline, then an expired owned-turn deadline, then the supplied
failure. An authentication class confirmed during joined backend cleanup retains its precedence.
Once recorded, matching retries see the same outcome until the existing five-minute expiry or
a new turn. An ordinary cancellation with no prior deadline stays cancellation.

Validated progress from the active owned prompt satisfies only the first-event wait (D127). It
does not advance conversation history, publish tools, count as visible output or extend the original
turn deadline. Streaming heartbeat scheduling remains independent of silent progress notifications.

Recognized Kiro authentication expiry produces the successful assistant fallback, marks the process and
session unhealthy, and instructs login. Other ACP/backend failures return a bounded 502 error. No path
can use a direct Anthropic credential or alternate provider.

Cleanup is single-flight per session/process, cancellation-shielded, and idempotent. Repeated stop calls
join or retry the same cleanup rather than leaking a child process.

## 12. Usage and local metrics

Kiro usage is fetched by running its usage command in an isolated temporary directory without creating
a model turn. Parsed results are cached for 60 seconds. The client status line may poll the local cache
every five seconds and must return immediately even if refresh is in progress or failing.

Completed foreground turns enqueue bounded metrics records; title/background turns do not. A bounded
queue, initially 32 records, prevents unbounded memory growth. Multiple Kiro metadata notifications for
one turn update the same record rather than create duplicate “turn complete” messages.

The approximate token estimator uses only local request and visible response structure. It estimates
system, tools, and messages as input context; visible response text and serialized tool arguments as
output. Media and hidden thinking are explicitly excluded. Repeated prefix estimates describe logical
reuse opportunity, not actual provider cache hits.
