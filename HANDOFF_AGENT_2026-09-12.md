# Agent handoff: continuing after D124 (2026-09-12)

This brief lets a coding agent with no prior context continue the work of the D121–D124 session
lineage. It is a brief, not a record: `IMPLEMENTATION_DECISIONS.md`, `DEVELOPMENT_STATUS.md`,
`DEPENDENCY_REVIEW.md`, `ACCEPTANCE_SPEC.md` and `LIVE_KIRO_TEST_PLAN.md` remain the authoritative
text and this brief defers to them wherever they differ. `HANDOFF_REVIEW_2026-09-11.md` is the
earlier review brief for D109–D120 written for the previous maintainer; it is not superseded by
this one. `AGENTS.md` still requires reading every document in the `README.md` order before touching
implementation code; do that first.

D125 continuation note (2026-09-12): the original candidate numbering in section 5 is retained for
traceability, but the actual D125 decision addresses items 16 and 20 first: guarded trust publication
and structural test diagnostics. Its reviewed code is committed at `c418f13` on
`d125-trust-concurrency`, merged into main as `07008df`,
based on the D124 handoff commit `095a5ca`. The D125 build is installed (`trust-publication`, SHA-256
`9088ebaf…`), with the independent findings resolved and the stronger native controls passing.
The host client has updated to 2.1.269, admitted unmeasured; its frozen-source batch passes 74/75
with a trust-dialog selection-observer failure before any model prompt. Diagnose that case
standalone before claiming a version migration. 2.1.268 remains the tested pin. The D125 record
and DEVELOPMENT_STATUS.md carry its verification state. The rest of section 1 describes the
inherited D124 checkpoint. The reviewed execution order and corrections appear below; candidate
solutions must not override the acceptance requirements.

D126 continuation: the trust-dialog failure above is resolved by correcting the test observer's
private keyboard query and full-screen margin-reset handling. The final navigator requires unique
option rows and a confirmed yes selection. The full 2.1.269 batch passes 75/75, all 27 race-tested
packages and vet pass, and the source pin moves to 2.1.269. This is not new live Kiro evidence.
The clean `3302837` artifact is installed (`measured-client-269`, SHA-256 `cb5cb96a…`), with
143 byte checks, 18 component checks and doctor passing. Review findings are fixed and reverified;
follow-up review accepts all fixes with no additional finding. D126 verification is complete.
Next: turn/relay robustness, personal memory, request compatibility, diagnostics, then approved live
reverification and combined soak. The original labels below are candidates, not completed decisions.

D127 on `d127-turn-relay`, based on `ea49c87`, addresses two reproduced timeout defects:
validated ACP progress satisfies the first-event wait without becoming answer content, and relay
binding/child attachment use the session setup allowance. All 27 race-tested packages, vet and
the three applicable Claude 2.1.269 core controls pass. The clean `7ce5a58` artifact is frozen
and installed as `progress-setup` (144 byte checks, 18 component checks, doctor verified).
Independent review accepts both record corrections at `220f094`, with no unresolved D127
findings and unchanged production inputs. Other core turn/relay candidates continue next;
no Kiro inference ran. The branch is retained after the required no-fast-forward merge.

D127 merged into `main` as `c8c631c`. D128 on `d128-relay-cancellation` starts from that merge
and addresses the reproduced resolved-call cancellation race in item 14. Its focused regressions,
all 27 race-tested packages with sequential package scheduling, vet and three applicable Claude
core controls pass. The two earlier parallel-package runs had independent process startup failures;
D128 records them separately. The clean `4494910` artifact is installed as
`resolved-cancellation` (144 byte checks, 18 component checks, doctor verified). Independent
review accepts `7e28df7` without an actionable finding or refreeze. Unresolved calls retain
complete prompt retirement. Other core candidates continue separately. No actual Kiro model ran.

D128 merged as `bbc4335`. D129 on `d129-tool-outcomes` addresses the reproduced outcome-window
and failure-reason races in item 15. Candidate history/IDs survive abort before Finish, while
normal result admission remains gated on successful finalization. Retired result-only retries
require history proof too. Focused controls, all 27 race-tested packages with sequential package
scheduling and vet pass. Five applicable Claude 2.1.269 core controls also pass (79.650s),
including original-deadline denial retirement and fresh-question recovery. The clean `dc119e7`
artifact is frozen and installed as `tool-outcomes` (144 byte checks, 18 component checks,
doctor verified). Independent review accepts `3065fd2` without an actionable finding, with
focused race and artifact checks passing. No production fix or refreeze is needed. Other core
candidates continue separately. No actual Kiro model ran.

D129 merged as `495a6e5`. D130 on `d130-stop-reason` addresses item 4's public completion gap.
The max_turn_requests reason now maps to pause_turn with exact text and ordinary history commit.
Controlled Claude 2.1.269 observations preserve that answer on an explicit next question, with
one request per input; the real gateway/independent ACP control also reuses the owner with only
the new delta. All 27 race-tested packages, full vet and three existing native core controls pass
(24.019s), including the six tool-policy cases. The clean `3d35c51` artifact is installed as
`prompt-stop` (144 byte checks, 18 component checks). Doctor passes on an unchanged read-only
retry after its first login check fails; both are recorded. Independent review accepts `037ef3a`
without an actionable finding; focused race and artifact checks pass independently. No production
fix or refreeze is needed. Other core candidates continue separately. No actual Kiro model ran.

D130 merged as `14a0478`. D131 on `d131-account-check` addresses item 9's account-command failure
classification and lost cleanup causes. Independent controls reproduce the deadline/login confusion
and dropped cleanup; the focused race checks now pass. Account command execution failures stop
startup without instructing login, while completed nonzero exits and invalid identity keep the
existing login-check failure. The four finite process controls join ownership and preserve source
settings. All 27 race-tested packages, full vet and the three actual-Claude/local-fake core
controls pass (25.363s). The clean `497e0e1` artifact is installed as `account-check`, with 144
byte checks, 18 component checks and doctor passing on its first invocation. Independent review
accepts `ac71b58` without an actionable finding. Focused race and artifact checks pass
independently; no production fix or refreeze is needed. Other preflight stages and the
catalog-postamble candidate are separate. No actual Kiro model ran.

D131 merged as `cf3a162`. D132 on `d132-mcp-schema-dialects` addresses item 2's reproduced
MCP dialect rejection. Claude 2.1.269 forwards Draft 7/2019-09 schemas unchanged and can call
those owned tools, while the former registry rejects them. The bounded input gate now admits
those declared drafts plus 2020-12 without rewriting them. Independent semantic/identity/limit
controls, all 27 race-tested packages and full vet pass. Four native dialect arms, cancellation,
result continuation, default-client refusal and all six tool-policy cases pass sequentially.
The clean cc297b4 artifact is installed as schema-dialects (144 byte checks, 18 component
checks, doctor verified on its first invocation). Independent review accepts 9ee8896 without
an actionable finding; focused race, full vet and artifact checks pass independently. No
production fix or refreeze is needed. No actual Kiro model ran; personal memory and other core
candidates remain separate.

D132 merged as `2566be2`. D133 on `d133-media-history` addresses item 1's reproduced complete
history admission failure. The input gate now uses 256 inline media parts/12 MiB, combining
validated top-level and result media, while the unchanged actual projection keeps 20/6 MiB.
Independent same-owner image/document deltas and excessive full-reconstruction rejection pass.
Actual Claude 2.1.269 with the real gateway and fake ACP retains 20/21/21 historical images at
HTTP and sends only 20/1/0 to the same backend. Focused race tests and this native control pass.
All 27 race-tested packages and full vet pass. Four existing native core controls pass in
27.293s, including all six tool-policy cases, joined cancellation, result continuation and
image-result/native-resume reconstruction. The clean 04eaad2 artifact is installed as
media-history (144 byte checks, 18 component checks, doctor verified on its first invocation).
Independent review accepts 418988d plus the corrected README installed-artifact label; focused
race, full vet, three 144-check binary results and 18 component checks pass independently.
No production fix, refreeze or unresolved finding remains. No Kiro model ran.

D134 is prepared on `d134-mcp-call-timeout` from `4e9bf87` for item 13's unmeasured MCP call
timeout. Independent peer/observer controls and the opt-ins-off interop race suite pass. The
exact two-case live command in LIVE_KIRO_TEST_PLAN.md has no new per-run approval or actual
model result yet. No production code or installed D133 artifact changes; call semantics and
client approval-wait alignment remain open.
Independent review accepts `329558c` plus the explicit local-coverage clarification. Focused
race/vet and the installed D133's 144 byte checks pass. The exact paired command is ready to
request per-run approval; no production fix or refreeze is needed for this preparation.

D134 preparation merged as `e8d64a9`. D135 on `d135-multi-call-relay` verifies concurrent
three-call batches through independent ACP and real relay/HTTP/SSE processes. Eight direct
waves join sixteen owners after mixed results and pending shutdown; eight HTTP waves join
24 owners after exact full-denial reconstruction and sibling continuation. Final focused race
controls and observer negatives pass. The first recovery failure was a single-session fixture
using the default shared-session pool; explicit one-session-per-process configuration fixes it.
No production change or new actual-client/Kiro evidence is involved. Combined resource/long
soak, prepared policy and shared ACP remain separate; D134's model approval is still pending.
The complete related race suites and final vet pass; an existing test-peer cancel closure is
made explicit and its twelve progress controls pass. Installed D133/current inputs pass all
144 byte checks. No rebuild or installation is required for these test-only changes.
Independent review finds one SSE observer issue. Six counterexamples reproduce invalid
lifecycle acceptance; explicit start/terminal ordering fixes it, with all observer controls,
eight HTTP waves (6.813s) and session vet passing. Post-response cleanup evidence is clarified.
Follow-up review accepts `0fdc8bc` with no remaining finding. Independent focused SSE/HTTP
race passes (7.968s), and installed D133/current inputs pass all 144 byte checks after the fix.

D135 merged as `c9f7ebc`. D136 on `d136-prepared-batch-churn` keeps one gateway/manager/pool
across 1,024 prepared three-call waves. The final 216.765-second loop joins all 3,072
ACP/relay/policy owners, preserves exact mixed results/recovery history and verifies old cleanup
before each replacement preparation. Fixed resource bounds and retained-resource/release controls
pass; descriptors/goroutines remain 22/30 until final cleanup yields 5/2. Peak post-GC heap is
1,083,952 bytes. This is independent prepared-lifetime evidence, without native Kiro policy,
shared ACP or backend/installed-proxy RSS measurement. No production change or actual client/
Kiro execution; installed D133/current inputs pass 144 checks. D134 approval remains pending.
The full five related race suites and vet pass. The observer's ledger lock is subsequently
released during OS cleanup; focused resource/prepared and held-cleanup controls pass. The
extended run also passes on that final observer at `9e1e7ca`; the figures above use this run.
Independent review accepts `9e1e7ca` and `b65e9b7` without actionable findings. Focused
session/pool race checks pass independently (10.883s/2.617s); five-package vet and 144 installed/
current-input checks also pass. An initial sandbox listener denial is resolved by the unchanged
socket-enabled rerun. No production fix, refreeze or installation is required.

D136 merged as `d4ed7b8`. D137 on `d137-terminal-delivery` reproduces HTTP 409 for an exact
immediate tool result after terminal JSON/SSE is visible but before server finalization returns.
The driver now allows one Start to wait for that terminal response's Finish or joined abort.
Cancellation of the waiter preserves the preceding owner; active generation/additional starts
stay busy. Direct next-question, result, admission and retirement controls pass. No Kiro model
or D134 run.
Native Claude 2.1.269/fake-ACP core controls pass in 25.828s, including six permission/hook cases
and 152-ms joined cancellation with unchanged sources. The first full race run fails six old
registry-helper busy expectations; the shared check now verifies canceled waiting instead.
No additional production change is involved. Final whole-repository race passes all 27 tested
packages (session 74.754s); final whole-repository vet also passes.
Clean code `3a13775` is rebuilt and installed as `terminal-delivery`, SHA-256
`3d386e521b29bd41bbded9f8255d789ebce8c91b887ec7a6b8174a2775d4cea7`. Candidate and strictly
resolved installed/current inputs pass 144 checks; all 18 component checks pass. The first
doctor verifies measured versions, login/policy and launch availability. Independent review
accepts `4f767aa` without actionable findings. Focused race checks pass in 16.688s/4.674s;
related vet, candidate/installed 144 checks, 18 component checks and independent build/source/
package comparison also pass. No correction or refreeze is needed. No actual Kiro model run
or new credit approval occurs.

D137 merged as `77f7d19`. D138 on `d138-paused-model-preference` reproduces the last-model
preference remaining stale after a delivered `pause_turn`. Both JSON and SSE handler controls
restore the previous model on a fresh launch before the fix; the other three final reasons pass.
The existing foreground-delivery condition now includes `pause_turn`. Focused launcher/gateway
race checks pass (5.021s/1.750s), including cancellation/auth/agent exclusions. No Kiro model
or D134 run is involved.
Whole-repository race passes all 27 tested packages (launcher 36.048s, session 74.223s);
whole-repository vet passes.
Two actual-Claude 2.1.269/fake-local regression controls pass sequentially (16.186s): three
stop/next-question cases and three compiled model-picker cases, with next-preflight restoration,
unchanged sources and joined recorded ownership. Native pause plus preference restoration is
not combined in these controls; the new combined check uses an HTTP recorder.
Clean code `b09bb9e` is rebuilt and installed as `paused-model`, SHA-256
`bd0e3f3def1ad308171f48840db482a53f4754cfdb4e5e5adfa582f1c438e376`. Candidate and strictly
resolved installed/current inputs pass 144 checks; 18 component checks pass. Exactly one
production file changes among the same 105 inputs, 267 packages and four modules. The first
installed doctor verifies measured versions, login/policy and launch availability.
Independent review accepts `295cfb1` without actionable findings. Focused race passes in
4.875s/1.333s, with related vet, 144 candidate/installed checks, 18 component checks and
independent clean-commit source/metadata comparison passing. No correction or refreeze is needed.

D138 merged as `9c9adbb`. D139 on `d139-concurrent-native-tools` adds two actual Claude
2.1.269 clients sharing one gateway/manager/pool with independent ACP. The 128-round check
passes under race (21.839s package, 16.092-second active loop): 127 allowed Read results,
128 hook refusals, exact ownership, cancellation before the final Read delivery and a
successful sibling completion. FD/goroutine counts remain 30/50 after warm-up, post-GC heap
peaks at 1,252,936 bytes, and final counts are 5/2. Settings/files are unchanged and recorded
HTTP/process/relay/profile ownership joins. Guard and retained-resource controls pass;
unrelated errors and round-prefix collisions cannot satisfy the final observers. All five
related packages pass race and vet, with final focused race and vet after observer fixes.
Independent review of 93fafeb finds three observer gaps, reproduced by failing controls and
corrected: paired backend liveness, completed server cancellation before sibling release,
and foreign-marker rejection at MCP. Focused race and the final native run pass; both changed
packages pass full race again (36.817s/1.229s) and vet.
Follow-up independent review accepts da54c95 with no remaining finding; focused race
1.426s/1.222s, changed-package vet and 144 installed/current checks pass independently.
Test-only; installed D138/current inputs pass 144 checks, with no rebuild or installation.
This finite control does not complete actual Kiro, interactive permissions, shared ACP or
long-duration combined soak. No new D134 credit approval exists.

D139 merged as 1c6ec65. D140 on d140-sustained-native-tools adds bounded pacing to the same
two native clients and shared runtime. The eight-round two-second-interval control passes
in 20.359s (14.008s active); local schedule/lifetime/observer race passes in 2.901s/2.121s.
Both changed packages pass full race in 37.290s/1.214s and vet.
The extended 128-round fifteen-second-interval race control passes once in 1910.801s, with
31m45.004s active. The same two clients and ACP prompts retain 127 allowed Read results and
128 hook refusals; final cancellation joins before sibling completion. All recorded ownership
and private profiles clean up, and source settings/files stay unchanged. Descriptors/goroutines
stay at 28/48 after warm-up; peak Go heap is 1,242,904 bytes and settled values are 5/2/733,920.
Independent review of 1c47fae has no actionable code finding; focused race 1.526s/1.286s,
vet and 144 installed/current checks pass independently. Follow-up review accepts f9cf712,
including the complete terminal log and final documents, with no actionable finding.
Total episode/client/ACP/peer allowance stays below forty minutes. Actual Kiro, interactive
permissions, shared ACP and RSS remain separate.
Test-only; installed D138 remains unchanged, with 144 current-input checks passing. No new
Kiro model approval or invocation; personal instructions and actual Kiro gates remain open.

D140 merged as f87688a. On d141-mcp-timeout-observation the user authorizes the prepared
D134 command once. It fails before session/new or any prompt, at ACP initialization
(4.171s package); its second case does not run. The original generic error does not classify
the cause or separately report cleanup, so the test now records its existing fixed error
class and cleanup-failure flag. No production code changes.
An existing initialize-only control also stops at account preflight. Later one-shot read-only
observations initialize in short and long owned layouts, with no session/prompt and with
joined cleanup; they use an unused /usr/bin/false MCP entry and do not rerun the live harness.
Neither a path-length cause nor authentication expiry is established. No login/logout occurs.
The first D134 log must be preserved. Its approval is consumed; the proposed D141 follow-up
command in LIVE_KIRO_TEST_PLAN.md needs new per-run approval. Personal instructions and actual
tool timeout/cancellation remain unresolved. Installed D138 is unchanged.
Focused local race passes in 1.893s/1.799s; related vet and 144 installed/current checks pass.
Independent review accepts 0fa7fd2 without an actionable finding. Independent focused race
passes in 1.491s/1.769s; related vet and 144 installed/current checks pass. No refreeze is needed.

## 1. State you inherit

- `main` is at `e07e591` (merge of D124). The working tree is clean. There is no remote; never push
  or add one without being asked. Feature branches `d122-diagnostics-lifetimes`,
  `d123-deferred-standing`, `d124-measure-client-2-1-268`, the probe branch
  `client-2-1-268-measure` and the historical `codex/kiro-isolation-checks` are kept as history;
  nothing on them is unmerged work.
- Measured pins: Claude Code 2.1.268 (`launcher.SupportedClientVersion`, D124) and Kiro CLI 2.21.3
  (`launcher.SupportedKiroVersion`, D114). D110/D114 admit same-major builds as "unmeasured". The
  retained 2.1.267 client at `~/.local/share/claude/versions/2.1.267` is now the admitted one; use
  it only to cross-check build-form differences.
- Installed executable: the D124 build (`~/.local/bin/dax-kiro-proxy`, SHA-256 `282b8556…`,
  revision `e08ce8e`, snapshot `third_party/inventory/macos-arm64-measured-client-268.json`).
  `doctor` reports login and execution policy verified, launch available, Kiro 2.21.3 and Claude
  Code 2.1.268 without the "unmeasured" suffix. Every earlier build is retained under the ignored
  `.cache/history-review/dax-kiro-proxy-d<NN>` on this host.
- Decisions D121–D124 in one line each: D121 completed the dependency inventory and moved the
  owner's rights checklist outside the repository; D122 added run diagnostics and lifetimes (7-day
  client lifetime, SIGHUP cleanup, named failure messages, `--tool-timeout`/`--turn-timeout`/
  `--first-event-timeout`, schema-worker 429, retained tool-wait outcome); D123 defers a rotated
  standing instruction on result-only continuations instead of recreating the Kiro session; D124
  measured 2.1.268 and moved the client pin.
- The 2.1.268 request form, measured by `internal/interop/client_shape_probe_test.go`: the first
  user message is a plain string; the first request's trailing system-role message carries an
  environment block (about 395 bytes, run-varying) plus the selected output style; later requests
  move that message into history and send the 49-byte token-budget line as the trailing message.
  D123 absorbs the rotation, D124's output-style control records where the style body sits.

## 2. Standing instructions from the user

These were given during the sessions and are not all written elsewhere.

- Work in batches. Each batch: branch from `main` → implement → verify → documents → commit →
  (production change only) clean rebuild, inventory freeze, `install --force`, inventory commit →
  brief an independent reviewer agent that has no session context → apply its findings → refreeze
  only if a production input changed → `git merge --no-ff` into `main`. Keep the branch.
- The user reads terse Korean, wants options with a recommendation, and then usually says "권장대로
  진행" (proceed as recommended) or "승인" (approved). "일단" means a pragmatic fix with the
  deviation recorded. Report outcomes with fixed facts; do not pad.
- Latest scope instruction: strengthen the existing core product. Prioritize basic/continued
  conversations, client tool approval/refusal/hooks, cancellation/recovery/cleanup and preservation
  of existing user instructions. HANDOFF candidates require a demonstrated core failure or an
  explicit mandatory specification gap before implementation. Defer optional features, cosmetic
  work and broad refactoring; a client update alone is not a reason to start another migration.
- Never run `kiro-cli login` or `kiro-cli logout`. Never kill the user's own `claude` or
  `kiro-cli --resume` processes (they may be running on this host). Synthetic ("pseudo")
  reproduction of a logged-out backend is preferred over touching the real login.
- Credit-consuming tests (`DAX_INTEROP_KIRO_CREDIT_OPT_IN=1`, the `TestKiroLive*` controls) run
  only with explicit per-run approval from the user. Approval for one run does not carry over.
- Never run two tests that launch the installed client or Kiro at the same time (also not two
  agents doing so); run every installed-client control under `umask 077`.
- Tests and logs record fixed-shape facts only: counts, roles, block kinds, digests, byte lengths,
  booleans. No prompt text, tool output or file paths.
- The owner's rights checklist (licensing, distribution, employer policy, service terms) is outside
  the repository by the user's decision (D121). Do not re-add it as a gate. The three unattributed
  metaschema resources are recorded and are not a task; do not list them again.
- A clean macOS install/uninstall check will be done by someone else; the remote/push decision is
  the user's. Do not schedule either.

## 3. Environment and commands

Go toolchain and caches (every `go` command):

```sh
cd /Users/geunwooshim/gnu-gnu-personal/dax-kiro-proxy
umask 077
export GOTOOLCHAIN=go1.27.1 GOMODCACHE=$PWD/.cache/gomod GOCACHE=$PWD/.cache/gobuild
```

Installed-client and Kiro witnesses (owned fake ACP backend, no model credits unless opted in):

```sh
export DAX_INTEROP_CLAUDE_BINARY=/Users/geunwooshim/.local/share/claude/versions/2.1.268
export DAX_INTEROP_KIRO_BINARY=/Users/geunwooshim/.local/bin/kiro-cli
export DAX_INTEROP_KIRO_CREDIT_OPT_IN=0
```

| Purpose | Command | Expectation |
| --- | --- | --- |
| Unit and fixture suites with the race detector | `go test -race ./... -count=1 -timeout 30m` | every package `ok` |
| Full installed-client batch | `go test ./internal/interop -run '^Test(Claude\|CompiledRun)' -count=1 -v -timeout 40m` | 74 of 74 PASS, about 600 s |
| Vet | `go vet ./...` | no output |
| Artifact snapshot | `python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot measured-client-268 --binary dist/dax-kiro-proxy` | 142 checks, release clearance false |
| Component record | `python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --components` | 18 records |

Two controls are load-sensitive inside a full batch and pass standalone:
`TestClaudeInterruptedResumedToolPolicyWithFakeACP` and `internal/acp/edge_test.go` (50 ms request
deadline). System memory pressure has killed long batches before; rerun the whole batch rather than
splitting it silently. Keep batch logs under `.cache/history-review/<dNNN>-*.log` and cite them from
the decision record. Remove `tools/__pycache__/` before committing (the freeze script imports the
verifier).

## 4. Production-change procedure (what D122–D124 did, step by step)

1. Commit the code and documents first. The rebuild must come from a clean committed revision
   (`vcs.modified=false`); the inventory commit follows it.
2. Rebuild exactly like the prior snapshots, without `-trimpath` and without extra `GOFLAGS`:
   `go build -o dist/dax-kiro-proxy ./cmd/dax-kiro-proxy`, then
   `go version -m -json dist/dax-kiro-proxy > .cache/history-review/dNNN-build-info.json`,
   `go list -deps -json ./cmd/dax-kiro-proxy > .cache/history-review/dNNN-command-packages.json`,
   `git rev-parse HEAD > .cache/history-review/dNNN-head.txt`, and copy the executable to
   `.cache/history-review/dax-kiro-proxy-dNNN`.
3. Freeze with a script modelled on `.cache/history-review/freeze-d124.py` (host-local, ignored):
   load the prior snapshot (`macos-arm64-measured-client-268.json` is the current predecessor),
   assert toolchain go1.27.1, darwin/arm64, CGO_ENABLED=1, `vcs.revision` equal to the head file,
   267 packages, the four external modules' versions/sums/package sets unchanged, native selected
   files and stdlib vendor packages unchanged, the `repository_inputs` diff equal to exactly the
   production files the decision changed (103 records today), and the import-path list identical
   (D122 relaxed this to set equality once because it reordered imports; D123 and D124 assert
   equality again). Rename the comparison keys to `*_since_D124` / `*_match_D124`, write
   `third_party/inventory/macos-arm64-<name>.json` with `'xb'`, and run the verifier on it.
4. Add the snapshot name to the `--snapshot` choices and help text in
   `tools/verify_dependency_inventory.py`; add a dated section to `DEPENDENCY_REVIEW.md` in the
   shape of the D124 section (bytes, SHA-256, revision, changed production files, verifier
   command, "all 142 byte checks pass", the scope caveats).
5. `dist/dax-kiro-proxy install --force`, then confirm `~/.local/bin/dax-kiro-proxy` has the frozen
   SHA and `doctor` shows both versions measured. Commit the inventory.
6. Brief the reviewer (section 7). Test-only or document-only fixes need no refreeze; prove it by
   rerunning the verifier with `--binary` after the fixes (the snapshot hashes every production
   input). A production fix means a new commit, rebuild and refreeze (D123 did this).
7. Every production change also needs: a `## D<NNN>: …` record appended to
   `IMPLEMENTATION_DECISIONS.md` with a verification paragraph citing the logs, a bullet at the top
   of the "Current evidence" list in `DEVELOPMENT_STATUS.md`, and amendments wherever
   `ACCEPTANCE_SPEC.md`, `README.md`, `PRODUCT_SPEC.md` or `PROTOCOL_SPEC.md` state the behavior
   in the present tense. Documents wrap at 100 columns; when editing with a script, rewrap only
   the paragraph or bullet touched (a blank-line-bounded rewrap once collapsed a bulleted section).

## 5. Remaining planned batches

The numbering below continues from D124; each batch is one branch, one review, one merge. The
file positions were checked against `main` at `e07e591`. Items marked LIVE need the user's approval
for a credit-consuming run.

### Original D125 candidate — protocol compatibility

1. Media limits apply to the whole request, and Claude Code resends the whole history each turn:
   `internal/anthropic/request.go:121` rejects once accumulated images exceed
   `MaxMediaParts` (20) / `MaxMediaTotalBytes` (6 MiB) from `internal/anthropic/media.go:19-21`,
   and `internal/projection/media.go:85` applies the same totals. Separate bounded HTTP input
   validation from the media actually dispatched in a proven delta. A fresh-session reconstruction
   must still preserve historical native images under D109; replacing them with size markers is
   not an accepted fix. Keep history hash inputs stable and explicitly handle full projections
   that exceed the negotiated dispatch bounds.
2. `internal/schemawire/input.go:35` rejects any MCP tool schema whose `$schema` is not the
   2020-12 URI, with an error that does not name the tool. Measure draft-07 and 2019-09 declarations
   before selecting dialect-aware support or an explicit unsupported-dialect diagnostic. Stripping
   `$schema` alone changes validation semantics and does not establish compatibility. Any tool
   identification in diagnostics must remain bounded and safe. Whether Claude Code forwards
   MCP servers' `$schema` verbatim is unmeasured (a fixture MCP server in the interop suite can
   measure it without credits).
3. `internal/session/turn.go:183-208` treats only `agent_message_chunk` as visible output, so a
   backend that emits only thought chunks or `tool_call` updates for 90 s trips the first-event
   deadline (`internal/gateway/http.go:294`, default at `:86`). Define validated progress from the
   owned prompt separately from visible text. Arbitrary metadata, unknown updates and SSE pings
   must not bypass the first-event bound or extend the original turn deadline.
4. `internal/session/turn.go:136-148`: `stopReason` values other than `end_turn`, `max_tokens`,
   `refusal` and the tool path fail the turn and discard the process; ACP's `max_turn_requests`
   should have an explicit supported completion mapping with streamed text preserved. Define a
   non-text output policy from the public protocol; silently discarding answer content is not
   automatically a compatible alternative to the current rejection.
5. Tool results with `document` or URL-sourced content are mapped as raw JSON text in
   `internal/session/continuation.go:150-175`; review the bounded conversion without discarding
   supported result content or fetching a URL. Unify nested image validation with the top-level
   rule (`internal/relay/broker.go:447-452` decodes and checks the nested form).
6. Expose a "Diverged" counter (session key replaced by a diverging history) in the turn metrics and
   the status line so context replays are visible; `internal/requestfamily/classify.go:24-44`
   classifies tool-less requests as Main unless they match the title shape exactly.
   Risk: medium (history hashing and projection). Verify 1–3 with unit and fixture controls; 3 and 4
   would benefit from one LIVE observation.

### D126 — operational and diagnostic

7. Startup sweeper for stale owner-only runtime residue: `dax-startup-*`
   (`internal/launcher/startup.go:226`), `dax-account-usage-*`
   (`internal/launcher/kiro_usage.go:96`) and the relay's `/private/tmp/dax-r-*`; remove only
   directories whose socket/flock owner is gone.
8. `doctor` returns before `PrepareClient` (`cmd/dax-kiro-proxy/main.go:330` prints "Client
   initialization: unverified"); include the read-only profile checks so `run` cannot fail on a
   settings cause that `doctor` called available. Attach the offending path or rule to
   `launcher.ErrSettings` (`internal/launcher/client_customizations.go:30-70`).
9. `internal/launcher/kiro.go:100-105` reports a `whoami` timeout as "not logged in"; separate the
   timeout from a non-zero exit, and tolerate a trailing postamble on `chat --list-models`
   (`internal/launcher/catalog.go:21`) the way `whoami` already does.
10. `models` should print the backend model id beside the client id; the cleanup-failure message
    should name the directory left behind.
11. README: state the bounds (8 sessions / 4 processes, 90 s first event, 30 min run turn default,
    tool wait 15 m, 16 MiB body, 20 media parts / 6 MiB) and `MAX_RETRIES=0`.
    Risk: low.

### D127 — relay and session robustness

12. `internal/relay/attachment.go:17-66`: the relay attach wait is `ReadTimeout`; align it with the
    session `SetupTimeout` and fail closed on expiry.
13. The Kiro agent definition (`internal/launcher/agent.go:44-45`) sets no MCP `timeout` for the
    `dax_session` relay server; make it match `ToolTimeout`. LIVE: Kiro's default MCP timeout and
    cancel behavior are unmeasured.
14. `internal/relay/broker.go:268` fails the whole relay on one cancelled call. Test queued, sealed
    and delivered batches before deciding where cancellation can stay local to one call. Preserve
    the complete delivered-result set and session retirement when ownership becomes ambiguous.
15. `internal/session/turn.go:311-331`: an abort in the Seal–Finish window records no outcome, the
    deadline reason depends on which error wins the race (`:271-279`), and
    `internal/relay/group_unix.go:20` treats a zombie as alive; make these deterministic.
16. D118 write-back (`internal/launcher/client_trust.go:232-256`) renames without taking the
    client's `.claude.json.lock`; either take it or re-compare the source bytes immediately before
    the rename.
    Risk: medium. 13 needs approval.

### D128 candidate — quality and later refactoring

Test-only changes need no artifact freeze. Removing production dead code, changing errors or
refactoring production helpers changes inventory inputs and requires a clean rebuild and freeze.

17. `internal/acp/edge_test.go:63` couples a 50 ms `RequestTimeout` to the initialize handshake;
    separate a startup timeout or raise the bound.
18. Dead code: `Request.TextOnly` (`internal/anthropic/request.go:233`), `projection.Delta`
    (`internal/projection/text.go:20`), `clientMCPState` (`internal/launcher/client_mcp.go:15`);
    capitalized error strings (`internal/launcher/startup.go:23`, `kiro.go:20-21`,
    `internal/ndjson/frame.go:14`, `internal/childproc/run.go:18-22`).
19. Coverage gaps: stop_reason validation and the streaming output cap in
    `internal/gateway/http.go`, late drain in `internal/session/turn.go:108-126`, `acp.Call` against
    an exhausted client, identical tool_result resubmission.
20. Log hygiene: `internal/interop/tool_restart_test.go:382` logs up to 200 bytes of the issued tool
    input (the fixture's Bash command text and temp paths appear in batch logs through the
    interrupted-follow-up controls). Replace with a digest and length.
21. Split `Handler.messages`, `Driver.Start`, `Driver.prepare`; unify the three process-group
    helpers and the raw-JSON string helpers. Last, because of regression risk.

Reviewed order: finish the actual D125 (items 16 and 20, including permission-bit preservation),
then turn/relay robustness (3, 4 and 12–15 with the relevant tests from 17 and 19), personal memory
and rule-exclusion preservation (the still-open D77/D93/D97/D102 requirements), request compatibility
(1, 2, 5 and D123's one-time standing-message deferral limitation), operational diagnostics (6–11),
then the latest-pair live rerun and longer combined concurrent soak with refreshed artifact scans.
Keep items 18 and 21 and optional native web work after the correctness changes. Assign final
decision numbers when each bounded batch starts; the old candidate labels above are not commits.

## 6. Decisions waiting on the user

- Rerunning the 21 `TestKiroLive*` controls on the 2.1.268 pair (about 15 minutes of model
  turns). Until approved, `README.md` and D124 state that the live evidence stands on the 2.1.267
  pair; the owned-witness batch is the 2.1.268 evidence.
- Whether the D110 same-major admission rule should be narrowed after the 2.1.268 incident (13
  controls failed on an admitted build before D123/D124). It is listed in the review brief; no
  change was requested.
- Remote/push, clean-host install check, owner's rights items: outside this brief.

## 7. Briefing the independent reviewer

Give the reviewer: repository path, branch and base commit, the diff range, `AGENTS.md`
conventions, the decision's claims in plain words, the evidence logs it may read, exact questions
per area (correctness, content safety of test logging, stale statements, artifact consistency,
document wording, anything else), the allowed commands with the environment prefix, the sequential
rule for client-launching tests, and the prohibitions (no login/logout, no credit tests, no killing
processes, no edits, no git writes). Ask for findings ordered by severity with file:line, a
"checked and OK" list per area, and the commands run with results. The D122–D124 reviews each
returned one medium and several low findings that were all applied before merging; expect the same.

## 8. Commit map of this lineage

| Commit | Content |
| --- | --- |
| `d00ec30` | D109–D120 review brief and corrected document statements |
| `5de746a` | D121 component inventory; owner's rights checklist scoped out |
| `4665909` | Merge D122 (branch `d122-diagnostics-lifetimes`; snapshot `run-diagnostics`, SHA `59e8eb4b…`) |
| `a756dde` | Merge D123 (branch `d123-deferred-standing`; snapshot `deferred-standing`, SHA `20a81468…`) |
| `e08ce8e`, `257514e`, `4f242dc` | D124 code and documents, inventory freeze, review fixes |
| `e07e591` | Merge D124 (snapshot `measured-client-268`, SHA `282b8556…`) |
