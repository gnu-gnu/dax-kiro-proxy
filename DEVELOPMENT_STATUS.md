# Development status and acceptance evidence

Last updated: 2026-09-13. Overall objective: complete the standalone Go Kiro ACP proxy and launcher
contract described in PRODUCT_SPEC.md, including the full acceptance/release gates. This document
does not redefine completion around an intermediate phase.

## Current evidence

- D128 on d128-relay-cancellation (base c8c631c) fixes a reproduced race where a
  resolved tool call's late cancellation retires queued, sealed, delivered or next-turn work.
  The exact pending-call check shares the resolution lock; committed results stay exact and
  unresolved cancellation still retires the whole prompt. All four regressions fail before the
  fix and focused race controls pass in 2.051s. All 27 packages pass the full race suite with
  sequential package scheduling, and vet passes. Two earlier parallel-package runs failed on
  independent process startup failures, recorded separately in D128. The three applicable actual
  Claude 2.1.269/fake-ACP controls pass sequentially (28.447s), including all six tool-policy
  cases, continuation and joined cancellation. No actual Kiro model runs. The clean 4494910
  artifact is frozen and installed as resolved-cancellation (144 byte checks, 18 component
  checks); doctor reports the measured pair and verified login/policy with launch available.
  Independent review remains pending.

- D127 on d127-turn-relay (base ea49c87) fixes two reproduced timeout defects. Validated owned
  ACP progress satisfies only the first-event wait, preserves answer/history/usage boundaries
  and does not starve streaming pings or extend the original total deadline. Relay binding and
  the actual child handshake use the configured session setup allowance; initial-frame and
  acknowledgement reads remain separate. The original setup context rejects a late binding,
  while a completed binding survives setup completion. Private child configuration v3 carries
  the bounded attachment limit.
  Initial regressions fail before the fixes. The final full repository race suite passes all 27
  tested packages, and full vet passes. Three applicable Claude 2.1.269/fake-ACP controls pass
  sequentially (26.026s): cancellation, tool-result continuation and all six client tool
  allow/deny/hook cases. Sources remain unchanged and owned resources are joined. No actual Kiro
  model was invoked.
  The clean 7ce5a58 build is frozen and installed as progress-setup (144 byte checks, 18
  component checks). Doctor reports both versions measured, login/policy verified and launch
  available. Independent review found no introduced production defect. Its two record findings
  are fixed at 220f094 and accepted on follow-up; focused race controls and artifact checks pass.
  No production input changed during review. Other core turn/relay candidates continue next.

- D126 fixes two terminal-observer defects exposed by Claude Code 2.1.269: a keyboard-flags query
  was interpreted as cursor restore, and full-screen margin reset did not reset the cursor. The
  trust navigator now requires exact, unique option rows and an observed yes selection before
  confirmation. Independent cursor/choice regressions pass; the two-launch trust control passes
  on 2.1.269 and 2.1.268. The full 2.1.269 installed-client batch passes 75/75 (579.147s),
  including the final trust control and measured output-style placement. All 27 race-tested
  packages and vet pass. The measured client pin moves to 2.1.269; Kiro stays 2.21.3. No actual
  Kiro model test ran, and live evidence remains on the 2.21.3/2.1.267 pair. The clean `3302837`
  build is frozen as `measured-client-269` and installed (143 byte checks; 18 component checks).
  Doctor reports both versions measured, login/policy verified and launch available. Review fixes
  reject oversized margin parameters and correct stale prose; the interop race package, vet and
  native trust control pass again, with unchanged artifact bytes. Follow-up review accepts all fixes
  with no further finding; no artifact refreeze is needed.

- D125 on `d125-trust-concurrency`: trust write-back now stages before acquiring the
  client lock path and revalidates source/staged bytes, identities and publication age before rename.
  Three existing-lock regressions fail before the fix; the focused race controls pass, including
  eight simultaneous candidates with one unchanged winner. The actual-client two-launch trust
  control passes (20.30s). A new black-box control observes unchanged source bytes at a one-second
  directory-lock release, but eventual writes with a persistently held lock; arbitrary unlocked
  writers therefore remain outside the publication guarantee. Tool-restart failure diagnostics now
  retain only counts, lengths, digests and status. The full installed-client batch passes 75/75
  (602.114s). A subsequent permission regression shows umask narrowing and accepts a changed
  staged mode; both are fixed, with the focused race controls and two-launch trust test passing
  again (4.718s and 19.700s package time). The final whole-repository race suite passes 27 tested
  packages and vet passes. The clean D125 build is installed and frozen as `trust-publication`
  (143 byte checks; unchanged four-module dependency set); all three refreshed advisory scans
  report no vulnerabilities. Independent review found a missing final HOME check and an incomplete
  lock-identity witness. Both are fixed, with the HOME regressions and Trust race controls passing
  (4.854s), followed by all 27 race-tested packages and vet. The stronger native lock and two-launch
  trust controls pass together (28.665s), and the reviewed `c418f13` artifact is refrozen and
  installed (143 checks and three advisory modes pass). A separate frozen-source full batch on
  the host's new Claude 2.1.269 passes 74/75: the trust-dialog observer sees the dialog but cannot
  confirm the yes selection, with no model prompt. Standalone diagnosis remains open; doctor
  reports 2.1.269 unmeasured and the tested pin remains 2.1.268.
- D124 measures the self-updated Claude Code 2.1.268 and moves the measured client pin from 2.1.267
  to it: the first user message is a plain string, the first request's standing message carries the
  environment block and the selected output style (D123 defers the rotation), and the output-style
  control now records the style's location and passes on both builds. Production change (the
  constant only); the artifact snapshot is recorded in DEPENDENCY_REVIEW.md.
- D123 defers a rotated standing instruction on result-only continuations instead of recreating the
  session: a suffix of one nonempty text-only system message replacing a one-message standing
  sequence resolves the results into the pending relay call, the same process answers, and the
  rotated message is recorded so the next prompt carries it; the all-denial recovery accepts the
  same shape. Measured cause: the self-updated Claude Code 2.1.268 (and 2.1.263 in D63's default
  configuration) sends an environment block and any selected output style as the first request's
  standing message and the token-budget line afterwards, which cost every conversation one
  recreation at its first tool result and failed fourteen controls on 2.1.268. A structural
  request-shape probe records roles, block kinds and digests only. Verified with the measured
  2.1.267 (74 of 74 installed-client controls) and, after adapting the three recreation-expecting
  controls, with 2.1.268 (the output-style form remains for its own decision); the live D71 variant
  passes on the actual Kiro with one launch (18.05s). Production change; the artifact snapshot is
  recorded in DEPENDENCY_REVIEW.md.
- D122 raises the interactive client lifetime to the seven-day ceiling and classifies its expiry,
  handles SIGHUP like an interrupt, separates "run needs a foreground terminal" (exit 2, nothing
  started) from a failed terminal restore, names absent or rejected executables with their found
  version, gives model, runtime, bind, start and gateway-stopped failures their own fixed lines,
  queues schema checks for a bounded time and maps schema capacity to 429/502 instead of 400, keeps
  a retired tool-wait outcome for identical resubmissions, and exposes `--tool-timeout` (15m),
  `--turn-timeout` (30m) and `--first-event-timeout` (90s) with validated ranges. Production change;
  the artifact snapshot is recorded in DEPENDENCY_REVIEW.md. Verified with the measured Claude Code
  2.1.267: eleven compiled-command controls (102.665s) and the complete installed-client batch (72
  of 73, the one load-sensitive failure passing three standalone reruns). The host client
  self-updated to 2.1.268 on 2026-09-12; on it this branch passes 60 of 73 and the thirteen failing
  controls (twelve tool-result paths, one output-style) fail identically on `main` (13 of 13);
  measuring 2.1.268 is a separate decision.
- D121 completes the dependency inventory for the darwin/arm64 development artifact and, at the
  owner's direction, moves the owner's rights checklist (ownership, provenance, employer policy,
  Kiro/client service terms, distribution, project license) outside this repository's gates; nothing
  is asserted about those items here. `third_party/inventory/components.json` records every linked,
  resolved-only, toolchain, generated-data, embedded, native, test-utility and external-executable
  input with digests, SPDX and status; the verifier's `--components` mode checks 18 file records
  offline and rejects two negative controls. govulncheck v1.8.0 (scratch install, database
  2026-09-10T14:48:42Z) reports no vulnerabilities in source, source-with-tests and binary modes
  over the installed D118 executable. BSD-3-Clause is recorded as the chosen branch for the sixteen
  matched metaschemas; the draft-04 root and the 2019-09 applicator/core resources stay unattributed
  (draft-04 tag probe 404 again). The installed generation's seven notices match the repository byte
  for byte. No production change; the D118 artifact and its 142 byte checks remain current.
- D120 records the long fixture soak and a many-turn actual-client soak. The churn controls at their
  maximum waves (64 waves/1,024 requests of eight concurrent sessions; 32 waves/512 joined groups
  and relays of concurrent pending-tool denials) end at the descriptor/goroutine/heap floor within
  their envelopes. The new `soak-turns` compiled-command control drives the unmodified 2.1.267
  client through numbered turns of one session with the independent Kiro fixture, sampling the
  proxy's resident size, descriptors and process-group size after every turn: 20 turns in 14.59s
  (16,464→18,240 KiB, 22→22 descriptors, 2→2 processes after warm-up) and 100 turns pass in 27.52s
  with 17,776 KiB, 22 descriptors and two processes after warm-up and 20,736 KiB, 22 and two at the
  end. The same soak against the actual Kiro 2.21.3 process, authorized by the user, also samples
  the backend process group: twenty turns pass in 59.55s with the proxy at 17,776→18,096 KiB, 24→22
  descriptors and 2→2 processes after warm-up and the four-process backend group at 319,568→106,560
  KiB (peak 320,080 KiB; a transient of the actual backend, since a first attempt that the terminal
  peer's fixed frame budget stopped at nineteen turns sampled 101,856→94,560 KiB), and the soak mode
  now also scales the peer's frame and byte budgets with the turn count (`d120-live-soak-20.log`).
  Test-only; the D118 artifact remains current.
- D119 verifies plugin acceptance against the actual restricted Kiro process through the real
  gateway: the model calls the owned plugin's advertised Skill tool, the client expands the skill,
  and a fresh marker that only the skill's own instruction supplies appears in the final answer
  (17.99s case); plugin SessionStart and Stop hooks each run once around a real turn with the
  startup context in the model's request (8.34s case). Both pass at the first attempt with joined
  process, relay and source cleanup (60.800s package); the fixture-backed plugin controls pass
  unchanged (hooks/skill sources 9.91s, Git source 4.18s, redirected-profile counterfactual 3.54s
  and gateway skill 4.95s in a 22.890s package, plus the model-selected skill in 4.08s;
  `d119-plugin-assets-regression.log`, `d119-model-selected-skill.log`). Remote marketplaces on the
  actual backend, Kiro-side skills and mid-session plugin changes remain open. Test-only; the D118
  artifact remains current.
- Live re-verification completed on the measured Kiro 2.21.3 / Claude 2.1.267 pair: with the user's
  authorization the sixteen remaining credit-consuming controls ran once in one package invocation
  (877.124s package): launcher cancellation 24.39s, default-client denial recreation 31.69s,
  one-prompt client denial 23.65s, resumed tool policy 140.08s, interrupted resumed tool policy
  50.62s, client tool effects 171.32s, plugin registry replacement 77.63s, restricted native effects
  22.62s, native restart through gateway and ACP 38.44s, pending tool native resume 42.94s, process
  loss and fresh request 28.48s, native history picker 44.77s, native history 44.45s, keyboard
  cancellation 23.43s, held-hook keyboard exit 24.47s and completed tool native resume 87.20s, all
  passing with their recorded facts and joined cleanup. Together with the two episodes re-verified
  earlier the same day, every live control now has evidence on the measured pair; the 2.21.2/2.1.263
  records remain historical (`live-recheck-2-kiro2213-claude21267.log`).
- D102 re-check on 2026-09-11 with the measured 2.1.267 client and the current public memory and
  settings references: personal rules and the user CLAUDE.md remain bound to the configuration
  directory, `claudeMdExcludes` still matches either the alias or its target, and the only native
  way to load `~/.claude/rules` from its own path (`CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD`
  with `--add-dir`) would widen the client's file access to the home directory, so it is not
  adopted. The alias-only exclusion gap stays open; no product change.
- D118 keeps the user's yes answer to the client's workspace-trust dialog as native Claude Code
  does: after the client exits, the launcher splices exactly `projects[<the client's
  key>].hasTrustDialogAccepted = true` into the user's `~/.claude.json`, only when this launch's
  private profile recorded the answer, the source lacks the value and the source bytes still equal
  what the launch read; every other byte is preserved, the result is validated for the next launch
  and renamed into place, and any doubt skips so the dialog repeats. This is a reviewed exception to
  D64 and the launcher's only write to a source client file; auto-trust and a product-private store
  were rejected. Launcher unit tests cover the splice and the guards (launcher 37.717s and command
  8.216s race packages); the compiled-command control answers the dialog in one owned HOME and
  requires the file changed by that fragment alone, then a second launch without the dialog (four
  consecutive two-launch runs of 18.55–20.71s, three of them in a 57.068s package); neighbouring
  controls (68.541s package), the complete installed-client batch (72 controls: 69 pass in the
  637.174s batch and the three load-sensitive controls, existing-statusline precedence and the two
  personal-asset controls, pass standalone in 35.929s), race suite (interop 29.769s, launcher
  37.717s, session 30.417s) and vet pass. The artifact is rebuilt, frozen as the `project-trust`
  inventory and installed.
- D117 measures the logged-out Kiro boundary synthetically and verifies the graceful login fallback
  in the foreground client. With the measured 2.21.3 build a synthetic HOME is logged out (D114):
  `kiro-cli acp` then fails at `initialize` with one stderr line naming the login command, nothing
  on stdout and exit status 1, and the product's classifier recognizes it (two runs each, 21.805s
  package). The new compiled-command control completes one question, reproduces that boundary on the
  next one through the independent fixture, and observes the gateway's `Kiro authentication expired.
  Run kiro-cli login` completion in the client with no API-error text, then, once the login is
  marked restored, answers one more ordinary question in the same client session from a fresh ACP
  group, with live client/proxy until keyboard exit and joined cleanup; a second mode loses the
  login while a relayed tool call is held and shows one login completion for the tool result before
  answering the recovery question at the first attempt (three consecutive runs of 3.69–4.36s
  (12.563s package)) (three consecutive runs of 9.07–9.26s (27.719s package)); neighbouring
  compiled-command controls (36.580s package), the interop race package (27.858s) and vet pass. No
  account was logged out and no credit was consumed. Test-only change; the D115 artifact remains
  current. A token expiring inside a running process remains unmeasured.
- Live re-verification on the measured Kiro 2.21.3 / Claude 2.1.267 pair, authorized by the user for
  two credit-consuming episodes on 2026-09-11: the actual model-selection episode (D86) passes in
  25.54s with all 19 catalog labels rendered and focused in 26 actions, no bounded reversal, one
  target acknowledgement, correlated first/next markers and zero non-success results; the actual
  interrupted follow-up episode (D84) passes in 21.19s with 32 streamed updates before one cancel,
  one follow-up prompt in a fresh ACP group, old/new instruction fragments and the partial marker
  present, and two main plus one title admission. Both keep the same client/proxy/profile/address,
  exit through the confirmed keyboard path and remove every recorded group, PID, listener, runtime
  and profile with unchanged sources (47.248s package, `live-recheck-kiro2213-claude21267.log`). The
  other sixteen live controls were re-verified later the same day (see the live re-verification
  bullet above).
- D116 adds the compiled-command control for Ctrl+C during a held tool followed by a new question in
  the same client session, the case D113 deferred. With the unmodified 2.1.267 client the held
  PreToolUse hook is interrupted, no result reaches the relay, the old prompt is retired with one
  ACP cancel, a fresh ACP group answers the new question, and the follow-up projection carries the
  old tool_use and the fixed interruption text with no tool_result (`tu=1 tr=0 ef=0 ir=1 ct=0
  ph=0`), a third representation beside the two restart forms. The peer records those fixed counts;
  the control rejects any manufactured successful result and requires the fixture content to stay
  off the screen. It passes three consecutive runs of 4.04–4.63s (13.008s package); neighbouring
  compiled-command controls (37.082s package), the interop race package (28.270s) and vet pass.
  Test-only change; the D115 artifact remains current.
- D115 removes the per-launch onboarding dialogs at their causes: the prepared client environment
  carries the ephemeral model token only as `ANTHROPIC_AUTH_TOKEN` (the documented Bearer header,
  which needs no interactive approval and ends the both-variables warning), and the private
  `.claude.json` projection carries the reviewed global flag `hasCompletedOnboarding` from the
  user's file (the theme lives in user settings, which the D24 overlay already carries; a stale
  global theme record is measured as dropped by the client). Bounded terminal measurements of the
  unmodified 2.1.267 client showed `hasCompletedOnboarding` alone suppresses the theme picker and
  security notes and the Bearer token alone suppresses the API-key approval; only the documented
  folder-trust dialog remains, already projected per project. Launcher unit tests, the client
  fixture, the D64/D111 projection control and the status-line terminal control (zero onboarding
  answers, no dialog text) verify it (status control 7.13s, projection control 7.77s); the complete
  installed-client batch (68 controls, 580.273s package time, 11 terminal observations with zero
  onboarding answers), launcher/command suites (launcher 28.329s, command 4.008s), race suite
  (interop 30.144s, launcher 35.328s, session 30.404s) and vet pass. The artifact is rebuilt, frozen
  as the `onboarding` inventory and installed; an expect-driven foreground `run` from a fresh
  project directory with the user's own HOME shows the client banner and the launch alias with no
  theme, key-approval, security-notes, trust, both-variables or unknown-model text, and exits
  through Ctrl+D with no surviving proxy or Kiro process.
- D114 admits a Kiro main/helper pair by major version and moves the measured Kiro pin from 2.21.2
  to 2.21.3 after the installed pair updated itself and the exact pin rejected every doctor/run at
  login_check. Preflight requires the same build on both executables and the measured major; startup
  reports `kiro_version_measured`, doctor labels other admitted builds unmeasured, and the
  development execution policy runs unchanged on them. Finite read-only Kiro controls pass on the
  installed 2.21.3 with the credit opt-in off (39 controls with 18 live controls skipped, 307.128s
  package time); 2.21.3 keeps login under the account HOME, so the synthetic-HOME controls now
  require its login-bound diagnostics while the product's account-HOME plus private-KIRO_HOME
  configuration keeps working; compiled-command controls with the 2.21.3 fixture pass (six controls,
  58.483s package time); launcher/command suites (launcher 34.559s, command 4.407s), race suite
  (interop 29.939s, launcher 33.494s, session 29.155s) and vet pass. The Kiro interop gates use the
  shared parser and log observed versions. Live credit-consuming episodes were not rerun. This is
  the same directed deviation from ACCEPTANCE_SPEC.md section I as D110, recorded as such. The
  artifact is rebuilt, frozen as the `measured-kiro` inventory and installed; its doctor on this
  host reports login and execution policy verified, launch available, and Kiro 2.21.3 and Claude
  Code 2.1.267 both measured (login_check 2187ms, model_catalog 2157ms).
- D113 moves the measured Claude Code pin from 2.1.263 to 2.1.267 after the full installed-client
  regression on the unmodified 2.1.267 client: 68 controls pass in 551.945s package time with the
  Kiro credit opt-in off under `umask 077`. Measured on the way: 2.1.267 may retain an interrupted
  tool pair with a fixed error result and continuation line before the placeholder, which the HTTP
  guard and the independent ACP peer now each classify and must agree on (`interrupted_history_form`
  abandoned in seven cases and retained in seven, each matched by the independent peer's
  classification); the D112 notice control's positive arm is the measured build; the `/model` picker
  scrolls, adds an effort row and an Enter/`s` footer, warns that both auth variables are set, and
  emits `ESC ( B` mid-session, which the bounded screen reconstructor now parses instead of painting
  a `B` over the selection glyph; the picker observer acts only on settled frames and fails fast on
  a wrong confirmation (four consecutive whole-test passes, 12.27–13.16s). The whole-repository
  opt-ins-off race suite (interop 29.730s, launcher 34.289s, session 30.885s) and vet pass. The
  artifact is rebuilt and frozen as the `measured-client` inventory in DEPENDENCY_REVIEW.md and
  installed; the D112 executable is retained. Its `client_version` phase admits the installed
  2.1.267 in 18ms, but the doctor summary was not observed: the host's Kiro main/helper updated
  themselves to 2.21.3 on 2026-09-11 and D59's exact 2.21.2 pin rejects them at `login_check` for
  the D112 and D113 artifacts alike. Moving the Kiro pin is a separate decision. Live
  credit-consuming episodes were not rerun at that point (all were re-verified on the measured pair
  later that day); D115 later closed the both-auth warning and the per-launch onboarding dialogs.
- D112 opts the prepared client out of a later build's unknown-model context-window enforcement.
  Installed Claude 2.1.267 printed that notice for the launch alias through `run`; the measured
  2.1.263 build never did, and product IDs are absent from every client catalog by design. The
  opt-out is the client's own switch named in that message, absent from the public env-var
  reference; the documented alternatives would assert model or window equivalences this project
  has not measured. A four-arm text-output print control passes in 1.75s (3.744s race package):
  natural shows the notice, JSON output hides it, opt-out and prepared show none, all complete
  with unchanged sources. An expect-driven foreground `run` shows the notice with the D110/D111
  artifact and none with the opt-out build, repeated with the frozen artifact. No Kiro model
  request occurs. The whole-repository
  opt-ins-off race suite and vet pass on the final code.
- D111 reviews the client's `claudeAiMcpEverConnected` breadcrumb as an exclusion from the MCP
  projection instead of a rejection. Installed Claude 2.1.267 writes it as a string array; it
  declares no server and carries no decision. The unit regression keeps declarations while
  omitting it, and the D64 installed-client scope control now seeds it at the source: all fourteen
  natural/prepared cases pass in 7.55s (10.370s race package), fresh prepared profiles connect
  every native scope, source bytes stay unchanged, and a profile prepared after seeding keeps
  mcpServers without the breadcrumb. No tool call or Kiro model request occurs. Other unrecognized
  MCP-related keys still reject; remote claude.ai connector preservation remains open.
- D110 admits a Claude Code build by major version after the installed client updated itself to
  2.1.267 and the exact 2.1.263 check rejected every launch. The measured pin is unchanged; startup
  records the detected build and `client_version_measured`, and doctor labels other admitted builds
  unmeasured. The nineteen installed-client evidence gates use the same admission parser and four
  controls log the observed version. Parser/admission cases pass with the launcher suite (31.490s
  race package) and the command suite (4.726s); with the installed 2.1.267 client the gateway
  contract passes in 0.67s. The whole-repository opt-ins-off race suite passes all tested packages
  (interop 28.069s, launcher 27.673s, session 28.468s); vet and whitespace pass. The development
  artifact is rebuilt and frozen as the `client-version` inventory recorded in DEPENDENCY_REVIEW.md;
  D109's executable is retained. This is a directed deviation from ACCEPTANCE_SPEC.md
  section I, recorded as such: evidence on 2.1.267 is labeled with that version and does not make
  it measured. Actual Kiro work and the remaining alpha/release gates are unchanged.
- D109 fixes completed tool-result images being serialized as base64 text during fresh ACP context
  reconstruction. The initial projection regression fails in 0.928s. An actual Claude/independent-ACP
  episode reproduces the gap: initial Read/MCP image delivery succeeds, but after source PNG removal,
  the native resumed request still contains its image while ACP receives no native image (6.832s
  failing package). Fresh projection now validates nested inline images, preserves result call/error
  ownership and content order, and applies the existing combined media limits. Committed deltas do
  not resend those images; unsupported result sources keep the existing opaque no-fetch fallback.
  After the fix, both stages pass in 5.77s (7.522s package). The final native regression run passes
  image resume in 5.11s and existing text/completed-tool history controls in 18.465s total. Encoded
  bytes and decoded pixels match, with one Read and one receipt from each native hook across both
  stages; source settings stay unchanged and all recorded client/ACP/relay ownership is removed.
  Twelve request-observation guards pass in 2.108s. Projection order/ownership/capability, seven
  malformed-or-excessive-media controls and opaque fallback pass in 11.100s. The public-only Claude
  consultation is saved/read/assessed; its suggestion of a different resume ID and length-only image
  heuristics are rejected. No actual Kiro inference runs. Actual image interpretation, other native
  formats/sidecars and full alpha/release requirements remain open.
- D108 fixes omitted personal output-style files by adding `~/.claude/output-styles` to the existing
  bounded native-scope snapshot. The new byte-preservation test fails before the change, and an
  actual Claude control confirms that a successful prepared reply omitted the selected style while
  natural execution included it (2.323s race package). After the fix, six natural/prepared pairs
  pass in 5.79s (7.873s package): personal selection, keep-coding declaration, same-name project
  precedence, project/local setting overrides and Default. Selected system-block digests, marker
  roles and system sizes match each reference; original style/settings files stay unchanged and
  prepared runs also preserve the full source config tree/global state. Natural controls mutate
  disposable native metadata/cache state, so they are not an alternate production launch path.
  Both keep-coding declaration values produce the same observed reference system shape here; a
  size-increase assumption is rejected, without inferring general native behavior. Eight role guards
  pass in 2.029s; snapshot/source-safety controls including twelve output-style cases pass in 6.460s.
  The public-only Claude consultation was saved/read/assessed; raw-request logging advice is rejected.
  No Kiro inference or client tool effect occurs. Personal-root and rule-exclusion fidelity gaps,
  UI switching, plugin/managed styles and broader live/release gates remain open.
  Existing native personal assets, rule sources/path characters and import-depth controls pass in
  26.045s. Whole-repository offline race tests pass all 27 tested packages (six have no tests), and
  whole-repository vet exits zero. Formatting/whitespace and the rebuilt D108 output-style artifact's
  141 byte checks pass, with release clearance false; D101's executable is retained unchanged.
- D107 extends native interactive new-tool permissions to interrupted histories with independent
  ACP. No-text history passes one-time Bash approval in 9.93s (11.206s race package), then comment
  refusal/hook veto in 11.82s (13.185s package). Partial-text history passes all three in 17.53s
  (18.830s package). Across six episodes/twelve stages, every old delivered operation remains
  unexecuted after repeated cancellation, joined old ownership and late hook release. Both resumed
  requests retain the exact old question and native placeholder or measured partial text; no old
  tool result is invented. Two new appends occur once and four new refusals leave no target/success
  hook. All final responses are currently visible before joined terminal cleanup, with unchanged
  source policies. Thirty handoff controls now separate completed/interrupted histories and reject
  mismatched proof, identity, operation and delivery/result phases (2.200s with existing guards).
  This verifies new work after an abandoned operation, not execution retry, actual Kiro interactive
  resume, bare denial, keyboard exit or uncertain side-effect windows. No production, dependency
  or D101 artifact change occurs; full-product acceptance remains open.
  Existing completed-history interactive and interrupted-history configured-policy regressions
  pass in 47.631s. The opt-ins-off interop race suite passes in 24.430s; interop vet, formatting/
  whitespace and all 141 unchanged D101 artifact checks pass, with release clearance false.
- D106 verifies new interactive Bash permissions after native completed-tool resume using actual
  Claude 2.1.263 and independent ACP. One-time approval passes in 9.45s (10.745s race package);
  comment refusal and PreToolUse veto pass in 10.45s (11.736s package). All six stages preserve the
  original successful pair and once-only old effects/hooks. The new append occurs once after
  approval or stays absent with its matching refusal; both resumed requests preserve old history.
  Current final display precedes controlled terminal shutdown, with all recorded ownership joined
  and both source policies unchanged. Fourteen fresh-handoff controls and bounded title isolation
  pass with existing history/permission guards in 2.166s; refusal witnesses also reject missing or
  repeated interactive hooks. No Kiro model prompt or credit use occurs. These are completed-history
  Bash observations with a controlled backend, not actual Kiro interactive resume, interrupted-work
  approval, keyboard exit or general exactly-once execution. Production/dependencies and D101's
  binary remain unchanged; remaining full-product gates stay open.
  Existing completed/interrupted native-resume policy regressions pass in 52.792s. The opt-ins-off
  interop race suite passes in 23.899s, including the added interactive refusal witnesses. Interop
  vet, formatting/whitespace and all 141 unchanged D101 artifact checks pass; release clearance
  remains false.
- D105 distinguishes same-ID transcript retention from resumed model context. Eight native Claude
  episodes compare ordinary and prepared configuration paths, sequential writes and both completion
  orders for overlapping writers. All 32 local synthetic turns pass in 16.02s (17.435s race package).
  Both completed replies remain on disk. Sequential readback contains both; concurrent readback
  contains only one completed branch in these cases, identically in reference/prepared runs. One
  schedule also retains the other writer's unanswered question in active context. No automatic
  merged-conversation guarantee or universal selection rule is inferred. Prepared source settings,
  all recorded client groups/profiles/listeners and routing-secret absence pass. The disposable
  reference's global config changes through native behavior; it is not adopted as production policy.
  Thirteen projection controls pass in 2.099s. A public-only Claude consultation is saved/read/assessed;
  its full-request logging suggestion and private-lineage hypothesis are not used. Product behavior,
  dependencies and D101's binary remain unchanged; same-ID limitations are now explicit in the docs.
  The opt-ins-off interop race suite passes in 28.339s. Interop vet, formatting/whitespace and all
  141 unchanged D101 artifact checks pass. These checks do not complete the remaining live/release gates.
- D104 verifies two concurrent native Claude histories in one owned HOME/project through the real
  gateway/session manager and independent ACP processes. Each of two rounds holds first responses
  until both distinct client/ACP pairs are live. Initial owners are all joined before either resume
  starts. Four turns pass with exact own-history marker counts/order, no sibling markers, two native
  transcripts and four fresh profiles/endpoints/credentials; all eight recorded groups disappear
  and sources remain unchanged (4.46s test, 5.903s race package). Nineteen independent guard controls
  pass in 2.060s, covering mixed/reordered/truncated context and invalid overlap witnesses. Native
  transcript formats are not decoded or edited; bounded authored-marker/secret-presence checks
  examine only disposable data. This is distinct-session text evidence with fake ACP, not actual
  Kiro concurrency, same-session writers, interactive selection or long-duration retention.
  Production, dependencies and D101's artifact remain unchanged; the full goal remains open.
  Existing native restart/persistence/ephemeral controls pass in 7.669s. The opt-ins-off interop
  race suite, including the strengthened missing-peer cancellation control, passes in 28.514s.
  Interop vet, formatting/whitespace and all 141 unchanged D101 artifact checks pass.
- D103 verifies the production usage cache, gateway, status helper and prepared profile together
  in the installed Claude UI. One actual Kiro refresh shows the cold view followed by the exact
  normalized used/limit values on the current screen (14.94s test, 17.240s race package), with three
  status polls, zero model requests, unchanged source settings and joined process/profile cleanup.
  Actual amounts and raw terminal contents remain unlogged. Synthetic native-UI controls pass
  completion, retained stale values after a failed refresh and cancellation of a held query
  (27.45s test, 28.730s package). Eleven verdict controls reject incomplete/incorrect/erased displays,
  refreshing or unsupported snapshots, inferred remaining and unmarked stale values. A tightened
  failure control leaves refresh admission to the next UI poll and passes in 13.08s (15.127s package).
  This is test/evidence work; production, D101's binary/dependencies and development admission are
  unchanged. Nonempty supplemental account payloads, billing precision and broader release work
  remain open; D100's separate model experiment is still pending.
  Native status precedence/startup/completion and tightened held-cancellation regressions pass
  in 53.720s. The opt-ins-off interop race suite passes in 25.608s; interop vet, formatting,
  whitespace and all 141 unchanged D101 artifact checks pass.
- D102 identifies an additional personal-rules compatibility gap. An exclusion matching only the
  private rules alias drops an otherwise naturally included rule and all four imports. The first
  strict natural/prepared comparison fails in 1.45s (2.514s package). Twelve separately named
  counterfactual controls reproduce this behavior in user/project/local settings while confirming
  original-path exclusions, retained project rules, source preservation and process/profile
  cleanup (6.08s test, 8.194s race package). These passes verify the defect, not successful rules
  preservation. A bounded public-only local Claude consultation is saved/read/assessed; it supplies
  no separate documented personal-memory read root. Neither the personal-root nor rules-alias
  gap is resolved. Production, D101's artifact and development admission remain unchanged.
  Existing native rule/import-depth/alias-scope regressions pass in 12.709s; opt-ins-off interop
  race tests pass in 26.279s. Interop vet, formatting/whitespace and all 141 D101 artifact checks
  pass. These regression results do not change the newly identified preservation failure.
- D101 connects lazy account usage to `run` using a separate empty-agent Kiro 2.21.2/v2 session.
  Independent parser/lifecycle tests cover malformed and ambiguous amounts, session/command/tool
  admission, cache coalescing, identity mismatch, timeout, repeated close and cleanup-failure
  latching. Read-only discovery verifies CREDIT used/limit fields without retaining amounts. The
  production cache passes one actual refresh in 10.25s (13.195s race package), with one ACP process,
  three post-initialize RPCs, zero model prompts, unchanged source settings and removed owned
  resources. It reports server-used/conditional-limit credits, never inferred remaining or summed
  supplemental credits. The 60-second cache and 15-second refresh deadline keep status reads and
  model requests independent. Launcher/startup/usage race controls pass in 16.467s after fixing the
  independent peer's macOS path-alias assertion. D103 adds native status rendering with account data;
  nonempty supplemental payloads and full release gates remain separate. D100's actual plugin
  experiment still awaits its required explicit approval.
  Whole-repository race verification passes in 27 packages (six without tests), with interop
  26.839s, launcher 25.982s and session 28.581s. Whole-repository/new-peer vet, formatting and
  whitespace checks pass. D101's rebuilt development executable and new usage inventory pass all
  141 byte checks with release clearance false. D99 is retained; no dependency is added/upgraded.
- D100 prepares a simpler fresh-result phrase for the unresolved actual plugin completion gate.
  Sixteen independent allowance/refusal counterexamples first expose early-token disclosure, then
  pass with the corrected guard and existing sequence tests (1.799s). A new fixture token-extraction
  regression catches and fixes a two-byte truncation introduced by the changed marker format.
  The revised actual-Claude/fake-ACP allowance/refusal pair passes in 12.93s (14.347s package), with
  exact generated/displayed phrases, three main requests/two backend processes per case, effects
  one/zero, preserved sources and cleanup. ACP/interop/fake-peer race regressions pass in
  5.713s/24.225s/1.192s; applicable vet and formatting checks pass. The attempted actual Kiro command
  was rejected before process creation by automatic approval review, citing absent explicit user
  approval for Kiro credits and external transmission. No D100 Kiro model work occurred; the live
  allowance/refusal sequence awaits that approval. Production, dependencies and D99 artifact stay
  unchanged. This is completed preparation, not completed actual-plugin acceptance.
- D99 corrects the private effort envelope to command/args/value using an independently recorded
  unmodified Kiro 2.21.2 terminal request. A fresh empty-agent ACP control applies high/low through
  the production adapter on an advertised Sonnet 4.6 model and confirms matching-session metadata
  (7.37s test, 8.650s race package), with zero prompts, unchanged source settings and joined cleanup.
  Repeated identical Sync calls send no additional setting RPC. Query text was shown to enumerate
  choices rather than report the current setting; it is excluded as a state oracle. Independent
  controls reject stale/ambiguous/missing/foreign/already-queued state and optional refusal (4.016s).
  Core effort/model-order/rejection/transport tests pass in 1.904s/4.914s after a failing old-wire
  regression. The final native recording also verifies joined cleanup and source preservation.
  No new dependency is introduced. Actual provider reasoning/billing and other model/level pairs
  remain outside this no-prompt observation; the full personal-memory/alpha/release scope stays open.
  Whole-repository race tests pass (27 packages, six without tests), including ACP, session and
  interop; whole-repository/fake-peer vet passes. Final probe bounds/cancellation controls pass in
  3.846s. The three final actual Kiro cases pass in 21.702s, checking removed private roots and
  unchanged original settings. The rebuilt D99 development executable replaces dist after retaining
  D96; all 139 artifact checks pass with release clearance false. It selects the same 267 packages
  and four external dependencies; only effort.go changes among production inputs.
  A final test-only cleanup guard preserves the root on a surviving group; the fresh actual
  setting/metadata run passes in 7.33s (9.351s package), with group exit before root removal.
- D98 verifies new work after an interrupted native history. Six native-client/fake-ACP cases pass
  in 32.00s (33.282s package): no-text/partial-text histories followed by allowance, configured
  denial or hook veto, with six old operations staying unexecuted, two new appends and four refusals.
  Exact old history precedes the new question in both resumed requests; fresh IDs/results and all
  ownership/source checks pass. One actual Kiro/Claude allowed-operation episode passes in 46.95s
  (48.273s package), two ACP prompts/three HTTP requests: old cancellation joins in 166ms, all
  31 bytes of old partial text remain, and only the new operation executes once. Initial admission
  failed at generic version/account preflight before model work; separate identity/identical-
  preparation read-only controls pass before the successful episode. Its first failure cause
  remains unknown; fixed command diagnostics now distinguish later failures. Guards pass in 2.149s,
  existing native history/policy regressions in 34.779s, and ACP/session/interop/owned-hook race
  suites in 6.023s/29.452s/22.133s/1.677s. Production, dependencies and D96 artifact remain unchanged.
  Final interop race passes in 21.439s after adding preflight diagnostics; whole-repository/fixture/
  hook vet, formatting, whitespace and all 139 artifact byte checks pass.
  Interactive pending approval, actual refusals after interrupted history and remaining alpha/
  release requirements stay open.
- D97 rules out a context-list absence oracle for personal root exclusions. The initial hypothesis
  fails for empty/comment-only roots without an exclusion (3.98s test, 4.976s race package).
  Eight separate counterfactual controls pass in 3.76s: active plain/frontmatter/import-only roots,
  original exact/glob exclusions, empty/comment-only roots and an excluded private wrapper. All
  use zero model/token-count requests and preserve sources/cleanup. A two-launch native/local-response
  control passes in 0.97s: a private-path-only glob hides linked root text and four import hops that
  remain active naturally. Combined race package 6.644s; existing memory regressions pass in 22.858s.
  These are rejected-adapter observations, not completed personal-memory compatibility. Production,
  dependencies and the D96 development artifact remain unchanged.
- D96 removes the fixed one-second wait when closing an idle authenticated relay connection.
  An independent ready-peer regression fails at 1,018ms before the change and passes at 18ms after
  it; eight concurrent Close callers join cleanup without terminating the healthy ACP owner.
  Surviving-peer failure and the separate one-second peer-exit bound remain intact. The same D95
  32-wave/544-request episode passes in 10.67s (14.637s package), down from 41.61s, with all 512 ACP
  groups and 512 relay/config owners removed. Settled FD/goroutine counts stay 70/108, final 5/2;
  GC-retained heap baseline/peak/final is 1,182,048/1,326,592/940,816 bytes. Full opt-ins-off race tests
  pass (27 packages; six without tests), as do vet and native-client allow/deny/hook resume controls
  (17.877s package). One actual Kiro/Claude allowed-Bash resume episode passes in 47.55s (48.873s
  package): two ACP prompts/four HTTP requests, two effects, exact old history, unchanged sources
  and joined observed cleanup. The rebuilt development artifact has a new relay-close inventory;
  139 byte checks pass with no dependency/notice change. Remaining alpha/release gates stay open.
- D95 extends repeated HTTP load to eight concurrent delivered tool batches and denial/new-question
  recovery. A final 32-wave episode passes under race detection in 41.61s (45.299s package): 544
  requests, 256 inert handoffs, 32 rejected internally paired foreign-owner histories and 256 fresh
  full-history text recoveries without another handoff. All 512 recorded ACP groups and 512 relay
  children/config directories are joined/removed across recovery, next-wave idle eviction and final
  shutdown. With eight idle replacements and the schema pool retained between waves, FD/goroutine
  counts remain 70/108; final counts are 5/2. GC-retained Go heap baseline/peak/final is
  1,204,528/1,351,096/942,832 bytes. History/ownership counterfactuals pass in 3.098s package. The
  initial peer delayed child reaping and produced a wave-two cleanup failure; correcting only the
  peer makes the eight-wave control pass in 10.49s (13.639s package). Product cleanup remains
  strict. Actual-client, native-RSS and long-duration soak were measured later by D120; multi-call
  and prepared/shared-process soak remain open. Final opt-ins-off race regressions pass: ACP 5.416s,
  pool 2.554s, gateway 3.397s, session 38.741s, relay 10.413s, MCP 5.689s, schema 3.125s and interop
  24.826s. Whole-repository/fake-peer vet, formatting, whitespace and the unchanged D87 artifact's
  139 byte checks pass.
- D94 adds a bounded HTTP/process churn regression using one gateway/manager/pool for 64 waves of
  eight concurrent client identities. All 1,024 requests succeed through their expected phase: 512
  buffered completions and 512 same-backend active streams followed by caller cancellation. Each
  wave joins eight recorded PID/groups and clears handlers/connections/pool ownership before fresh
  session admission. The 4.48s episode passes under race detection (6.856s package): 512 joined
  process/group instances, post-warmup FD/goroutine counts fixed at 6/4 and final 5/2; GC-retained
  Go heap baseline/peak/final 813,696/1,017,664/836,344 bytes. Eight-wave and
  history/terminal/descriptor counterfactual controls pass too (4.404s package). Initial test-only
  timeout mismatch fails before requests and is corrected without a production change. Opt-ins-off
  race regressions pass: ACP 5.183s, pool 2.435s, gateway 3.502s, session 29.327s and interop
  23.569s. Whole-repository/fake-peer vet, formatting, whitespace and 139 unchanged-artifact byte
  checks pass. This is short, text-only fixture evidence; real-client/relay, pending-tool,
  native-RSS and long-duration soak were measured later (D95, D120); shared-process soak remains
  open.
- D93 rejects the public additional-directory memory option as an equivalent personal CLAUDE.md
  adapter. The initial natural-equivalence comparison fails in 1.33s: root text remains, but its
  four relative-import hops disappear and its ordering moves after project instructions. Both
  local Messages requests complete with joined client groups. A separately named counterfactual
  reproduces those exact defects and matching original-path exclusions in 1.93s, with candidate
  sources unchanged and private cleanup. Existing native rule/depth/adapter controls also pass,
  combined race package 14.722s. Public-only Claude advice is saved/read/assessed and corrected
  against current official docs. No production adapter or external inference is added; personal
  root-memory fidelity remains an unmet requirement, not a passed gate.
- D92 verifies a fresh Bash operation after completed-history resume under current allowance,
  configured denial and PreToolUse veto with actual Kiro/Claude. The old successful pair remains
  exact before the new question and in the new result continuation; the new call has a distinct ID
  and matching success/refusal. Old effects/hooks stay one, the allowed new effect/hooks occur once,
  and both refused targets/post hooks stay absent. Six stages remove observed processes/groups and
  private resources with both settings sources unchanged. The completed live episode passes in
  142.13s (143.450s race package), six main prompts/twelve HTTP requests, four allowed appends and
  two fresh refusals. An earlier invocation stopped at preflight before model work; fresh public
  version/login/isolated-identity/catalog checks precede the successful invocation. Its precise
  initial failure cause is unknown; no limits change or model prompt retry occurs. Native fake-ACP
  cases pass in 16.22s (17.557s package), observers in 2.044s and existing native resume cases in
  23.479s. ACP/interop/hook race regressions pass in 6.017s/21.855s/1.308s; vet, formatting, whitespace
  and 139 artifact byte checks pass. No production/dependency/artifact change. Interactive approval,
  new tools after interrupted history, authentication expiry and remaining alpha/release work stay open.
- D91 verifies native resume after cancellation before a delivered Bash operation executes. The
  single actual Kiro/Claude episode passes in 30.19s (32.156s race package): two main prompts/two
  HTTP requests, one original handoff, zero tool effects/results and one resumed text completion.
  Cancellation returns in 147ms; old recorded groups/PIDs and private artifacts are gone before
  a late-release check and new ownership. The resumed request preserves the original question and
  all 31 bytes of prior partial text, omits the unfinished pair and carries the exact non-executing
  follow-up. This is bounded runtime abandonment, not an explicit native cancellation result.
  Release/no-text/partial-text native controls pass in 12.92s; completed Write/Bash regressions pass
  in 7.83s. Final opt-ins-off race suites pass ACP 5.738s, interop 22.585s, hook 1.306s. Public-only
  Claude advice was saved/read/assessed. Vet, formatting, whitespace and 139 unchanged-artifact byte
  checks pass before checkpoint beae010. No production or dependency change. New tool policies
  after resume, interactive pending recovery and the remaining alpha/release gates stay open.
- D90 verifies completed Write/Bash history after native explicit-ID resume with the actual pinned
  Kiro/Claude pair. Each case has one original client effect, two native launches, two ACP main
  prompts and three HTTP model requests. Stage two preserves the exact original tool ID/name,
  decoded arguments, successful status and result text, ordered before its new question, with
  no new tool handoff. Native pre/post hook counts and the file effect stay exactly one. Four
  stages complete with distinct old/new groups/profiles/endpoints/tokens, joined cleanup and
  unchanged sources. The single live run passes in 56.99s (58.324s race package): Write 29.65s,
  Bash 27.34s, no live retry. Corrected fake-ACP controls pass in 10.55s (12.473s package).
  Initial local failures exposed test-only directory permissions, overly narrow initial-message
  admission and a model-discovery path; the product needed no change. Public-only local Claude
  advice is saved/read/assessed. Independent controls reject modified/missing/duplicate/reordered
  pairs, a newly emitted resumed tool and doubled effect/hook receipts. Interrupted pending work,
  post-resume policy decisions, arbitrary/default tools, interactive tool resume, authentication
  expiry and all remaining alpha/release gates stay open. No dependency or artifact change.
  Final fake/native and observer controls pass in 10.830s after strengthening completed-answer
  ordering. Opt-ins-off race regressions pass: ACP 5.670s, interop 22.879s. Whole-repository/fake-peer
  vet, formatting, whitespace and the unchanged D87 artifact's 139 inventory byte checks pass.
- D89 verifies the native /resume picker after a new compiled run --client-history. A public native
  flag names the owned first conversation; the second invocation types /resume, confirms that exact
  selected row, then waits for the restored answer before entering its question. Both stages share
  owned HOME/project/proxy state, while runtime and observer executable paths are fresh. One actual
  Kiro 2.21.2 / Claude 2.1.263 episode passes in 33.17s (34.835s race package): two main requests,
  no titles, one validated context and correlated end_turn answer per stage, restored history and
  exact random-token recovery. Each stage removes three recorded groups/five PIDs, listener/runtime/
  profile and restores terminal state with unchanged settings. No guard failure, prompt error,
  emergency cleanup or live retry. The initial fake picker control passes in 8.08s (9.363s package).
  Final fake ID/picker controls with shared proxy state pass in 17.894s; observer race passes 1.903s.
  Picker guards reject missing headings, wrong rows, duplicate names and partial-name matches.
  Public session documentation supplies the naming/navigation contract; D87's saved public-only
  Claude consultation remains relevant. No production/dependency/artifact change. Continue/latest,
  broader selection, pending tools, authentication expiry and full alpha/release gates remain open.
  The opt-ins-off interop race suite passes in 25.513s. Whole-repository/observer vet, formatting,
  whitespace and the unchanged D87 artifact's 139 inventory checks pass.
- D88 verifies ordinary compiled terminal run, confirmed keyboard exit and a new run --resume UUID
  using actual Kiro 2.21.2 / Claude 2.1.263. The earlier answer is visible before the second question
  is entered. The fresh ACP prompt contains one old question, one old answer, one new question and
  the independent random token once; the absent-control marker is absent. The active owning session's
  text returns the token, and a correlated successful end precedes the answer receipt/UI completion.
  Both keyboard exits restore terminal settings and remove every recorded group/PID, listener and
  private runtime/profile with unchanged source settings. The first stage uses four groups/seven
  PIDs; the second uses three/five. Client/proxy/main-ACP identities, profiles and addresses differ.
  One live episode passes in 29.71s (30.993s race package), with two main prompts and one title, no
  guard failure, prompt error, emergency cleanup or retry. The independent fake-ACP control first
  passes in 7.84s (9.120s package), and observer negatives cover lost/duplicated/foreign context,
  foreign response IDs, split chunks, cancellation and errors. The full observer race suite passes
  in 1.226s. D87's saved public-only Claude advice covers this additional native-interface control.
  No production/dependency/artifact change. Session picker/continue, pending tools, authentication
  expiry, complete native asset/data preservation and full alpha/release gates remain open.
  The nine existing installed-Claude/fake-ACP keyboard controls pass in 42.903s. An initially
  sandboxed opt-ins-off suite fails relay controls and aborts at an explicitly denied loopback bind;
  the unchanged suite with local sockets permitted passes in 23.275s. Whole-repository/observer vet,
  formatting, whitespace and the existing D87 artifact's 139 byte checks pass.
- D87 adds optional native conversation retention and explicit-ID resume. `run --client-history`
  connects only the native projects data directory to each temporary profile; `run --resume UUID`
  enables retention and forwards the validated ID. Default profiles remain ephemeral. Owned roots,
  non-writable-by-others permissions and preparation-time directory identities are checked; links,
  regular files in place of directories and malformed IDs reject. Native transcripts are not copied,
  parsed or edited, and source settings stay private. A baseline first reproduces lost history.
  Four installed-Claude controls then pass in 4.14s (6.022s race package), including fresh token/port,
  exact prior marker counts, native suppression, unchanged sources, no credential bytes in bounded
  owned data and removed profiles. The actual gateway/manager plus independent ACP restart passes
  in 4.02s (5.988s package); the peer rejects session/load and verifies the projected old/new markers.
  One actual Kiro episode passes in 26.50s (27.783s package): two main requests, two successful ends,
  distinct cleaned process groups/profiles/endpoints/tokens, and exact recovery of a random token
  supplied only in the first question. Each old stage is joined before the next is dispatched. This
  is finite text resume with recreated backend state, not interactive picker or pending-tool proof.
  Public-only local Claude advice is saved/read/assessed. Full alpha/release gates stay open.
  The complete opt-ins-off race suite passes, including interop 23.625s, launcher 26.580s and session
  28.264s. A rebuilt development executable and D87 inventory retain 267 selected packages, four
  unchanged external modules and 100 repository input records; 139 offline byte checks pass.
  Whole-repository/new-peer vet, formatting and whitespace checks pass. The retained earlier D79
  artifact remains historical; the current executable uses the named native-history snapshot.
- D86 completes one actual Kiro 2.21.2 / Claude 2.1.263 model-selection episode: 23.90s test,
  25.365s race package, two main prompts and one title. Fresh finite discovery and prompt-free
  CLI/ACP comparison first establish nineteen equal IDs/aliases and a legacy selector. All nineteen
  labels render and receive focus in the native picker; a distinct target receives one successful
  idle ACP acknowledgement before the next question. Each generated marker is observed in its
  active ACP session text and in the same native client, with correlated end_turn. A following
  doctor restores the delivered model without another recorded client/ACP session. All four recorded
  groups/seven PIDs, listener/runtime/profile disappear, terminal settings are restored and sources
  remain unchanged. No live retry, guard failure or emergency cleanup occurs. The three strengthened
  fake-ACP controls first pass in 11.94s (13.653s race package), including a nineteen-entry list and
  unchanged selection. Earlier failed local controls expose an input-prompt glyph outside the menu
  and unadvertised navigation rows; the heading-clipping hypothesis is rejected. Those rows receive
  only bounded navigation, never Enter or catalog coverage. Local public-interface Claude advice
  is saved/read/assessed. No production/dependency/artifact change. Other model pairs, effort changes,
  authentication expiry, pending-tool recovery, persisted resume, personal-root memory and full
  release acceptance remain open.
  The six existing native-Claude/fake-ACP keyboard controls pass in 33.153s; opt-ins-off race suites
  pass interop 24.109s and observer 1.428s. Whole-repository/observer vet, formatting and whitespace
  checks pass.
- D85 adds native Claude model-picker controls through the ordinary compiled command and two
  independent fake-ACP models. Observer and readiness negatives first fail on missing APIs, then
  pass. Both catalog entries appear before selection. Unchanged selection and switching each produce
  two visible, correlated end_turn completions in the same observed client/proxy/profile/address;
  the latter has one successful target-model acknowledgement before the next prompt. A following
  doctor preflight restores the last model without another recorded client/ACP session. The final
  two controls pass in 6.74s (8.548s race package); each removes four recorded groups/five PIDs,
  listener/runtime/profile, restores the terminal and preserves source settings. An earlier typed
  model-ID attempt produces an additional prompt attempt which the declared budget rejects; it is
  retained as a failed control, not attributed to a particular native validation mechanism.
  No production/dependency/artifact change or actual Kiro model call. Actual Kiro selection,
  authentication, pending-tool recovery, persisted resume, personal-root memory and release gates
  remain open.
  The six existing native-Claude/fake-ACP keyboard controls pass in 32.959s; opt-ins-off race suites
  pass interop 24.745s and observer 1.251s. Whole-repository/observer vet, formatting and whitespace
  checks pass.
- D84 verifies a new question after streamed Ctrl+C in the same foreground Claude 2.1.263 / Kiro
  2.21.2 run. Admission/correlation/readiness tests first fail on missing APIs, then pass. An initial
  actual observation passes, but review finds that its end counter also accepts correlated errors.
  A failing negative test leads to an observer correction: only end_turn counts as successful end.
  All six actual-Claude/fake-ACP keyboard controls then pass in 34.283s under race detection. The
  stronger actual revalidation passes in 22.88s (24.731s package): after 24 initial text updates,
  the old main group retires on cancel/cancelled reply before the new question. The same observed
  Claude exec and live client/proxy/profile/address remain; a previously unobserved ACP group
  delivers one text/end_turn and its answer appears in the current UI. Old/new instruction fragments
  and the partial marker are present; an unrelated marker is absent. Two main and two title prompts
  run within the declared budget; one title ends before exit. All six recorded groups/eleven PIDs,
  listener, runtime and profile disappear, with restored terminal, unchanged sources and no guard
  failure or non-success prompt result. Two live episodes total distinguish initial observation
  from corrected revalidation; no failed provider call is automatically retried. The local
  Claude consultation is saved/read/assessed after explicit payload approval resolves an initial
  automatic approval refusal. No production/dependency/artifact change. Pending-tool recovery,
  authentication expiry, model changes, persisted restart/resume, personal-root memory, unobserved
  descendants and the remaining alpha/release gates stay open.
  Final opt-ins-off race suites pass interop 25.113s and the complete observer package 1.242s.
  Whole-repository/observer vet, formatting and whitespace checks pass.
- D83 verifies compiled-command keyboard exit during an exact native Read hook wait with actual
  Kiro 2.21.2 / Claude 2.1.263. Hook input/readiness tests first fail on missing APIs, then pass.
  Both actual-Claude/fake-ACP controls pass in 10.251s under race detection: explicit hook release
  produces a matching native PostToolUse/Read result and displayed completion; keyboard exit while
  held finishes in 278ms without tool completion. The actual trial runs once and passes in 22.27s
  (23.553s package): the hook remains alive at exit confirmation, and shutdown takes 2,392ms with
  one hook interruption and ACP cancel/cancelled reply. All five recorded groups/eight PIDs, listener,
  runtime and profile disappear; terminal state is restored and sources remain unchanged. One main
  and one title prompt run; no late release effect, guard failure, emergency cleanup or live retry.
  Native Kiro MCP counts are not instrumented. The initial fake release attempt closed its relay
  too early and correctly triggered session cancellation; only the fixture's connection lifetime
  changes. D82's public-only saved Claude consultation also covers this case. No production,
  dependency or artifact change. Arbitrary hooks, following questions, unobserved descendants,
  other live alpha, personal-root memory and full release gates remain open.
  The existing three actual-Claude/fake-ACP streaming controls pass again in 22.676s. Final
  opt-ins-off race suites pass interop 25.344s and the observer 1.225s; whole-repository/observer
  vet, formatting and whitespace checks pass.
- D82 verifies typed Ctrl+C during main-response streaming through the ordinary compiled foreground
  command with actual Kiro 2.21.2 / Claude 2.1.263. Independent observer/readiness controls pass first,
  followed by all three actual-Claude/fake-ACP cases: normal completion, ordinary-character input
  and cancellation (23.041s race package). The actual trial passes once in 18.34s (19.651s package):
  after 24 main text updates and a generated marker visible in the client, Ctrl+C produces one
  forwarded ACP cancel and one cancelled reply, with no main end. The main group disappears while
  Claude/proxy remain alive; confirmed Ctrl+D exit restores terminal state and removes all four
  recorded groups, seven recorded PIDs, listener, runtime and profile. Source settings remain intact.
  Exactly one main and one separate title prompt run; no guard failure or emergency cleanup occurs.
  Initial fake harness failures exposed an incorrect one-total-prompt assumption; correcting title
  attribution changes only the independent fixture. Local public-only Claude advice is saved/read/
  assessed. No production/dependency/artifact change or live retry. Held-hook keyboard exit, a new
  question after interruption, unobserved descendants, other live alpha and release gates remain open.
  Applicable opt-ins-off race suites pass interop 23.720s, launcher 28.446s, childproc 6.106s,
  ACP 5.031s and command 3.790s. Whole-repository/observer vet, formatting and whitespace pass.
- D81 completes the actual Kiro 2.21.2 / Claude 2.1.263 process-loss-before-Read-delivery and fresh-
  request scenario after D80 restores verified login. Independent guards and the fake-ACP rehearsal
  first pass in 7.609s; the actual test passes in 21.51s (22.801s race package). The first client
  receives the real backend error, old cleanup joins, and a second independently supplied request
  completes with unchanged owner/policy. Exactly two requests and two cleaned backend processes,
  one intercepted Read, no client hook/effect, one new completion, unchanged sources and all observed
  groups/artifacts gone. No retry or code/dependency/artifact change. Same-client continuation,
  late results, sibling-session failure, remaining live alpha and release gates stay open.
  The current D79 executable's fresh-state doctor exits 0 with login/policy verified and
  launch_available true; client_initialization remains unverified because doctor does not launch it.
- D80 adds bounded personal-memory observations without enabling a production adapter. Explicit
  alias exclusions pass twelve user/project/local controls, but do not implement general policy
  mapping. Five native read-only control sessions verify effective exclusions and exact context
  path identities with zero model/token-count calls; an absent entry is ambiguous between an
  excluded and conditional rule. A renamed rules entry still loses root body/order. Redirecting
  the settings-stage configuration path preserves finite print memory behavior, but normal
  persistence writes an original transcript; suppressing history still allows interactive startup
  to change original settings and uninstall to remove original plugin registration. These rejected
  effects are retained as explicit counterfactuals, not accepted product behavior. Six top-level
  controls, comprising 39 sessions, pass under race detection in 22.428s. Local public-only Claude
  advice is saved/read/assessed; its import-depth compromise and introspection absence claim are
  not adopted. No production/dependency/artifact change, actual-user-source write or Kiro model request.
  Seven applicable installed-client regressions pass in 40.373s; opt-ins-off race suites pass
  interop 24.864s and launcher 28.615s. Whole-repository vet, formatting and whitespace pass.
  The unchanged D79 artifact still passes 138 inventory checks. A final read-only Kiro recheck now
  returns whoami exit 0 for both entry points (5.140s); product identity/scope preflight passes in
  1.686s. This supersedes D75's absent login and permits live tests to resume. Personal CLAUDE.md
  remains absent; run policy is enabled, while live alpha and release acceptance remain incomplete.
- D79 adds per-user `install`, `install --force` and `uninstall`, copying only this executable and
  seven retained notice/reference files. Shared process/helper leases prevent replacement/removal
  while in use; exclusive publication and validated cleanup preserve user/client state and foreign
  or modified entries. Tests first fail on missing APIs/dispatch, then pass normal/forced/repeated
  operations, unsafe contents, cancellation, changed lock identity, real process exit before/after
  publication and bounded recovery. An ambiguous partial stage is preserved. The compiled binary
  passes an isolated-HOME exercise outside the repository, including worker initialization, busy
  refusal, self reinstall/uninstall, exact notices, settings/source preservation and joined groups
  (5.629s race package). Final focused race suites pass installation 7.667s and command 4.963s;
  applicable regressions pass launcher 26.376s, childproc 5.840s, ACP 5.010s and schema 3.075s.
  Whole-repository vet, formatting, whitespace, build and executable help pass. Public-only local
  Claude advice is saved/read/assessed; advice to delete invalid stages by name is rejected.
  The new frozen artifact has 267 selected packages, four unchanged external modules and seven
  newly embedded reviewed notices. Its 99 repository-input records include 90 production Go files;
  138 offline file-record checks pass; seven owned negative controls reject changed/missing/unsafe
  sources, a changed prior report, exceeded record count, escaping paths and false clearance.
  The original D78 snapshot/artifact still passes forty checks.
  This is local development installation evidence, not clean-host release or license clearance.
  Login renewal, personal CLAUDE.md preservation, live alpha and remaining release gates stay open.
- D78 adds an artifact-linked dependency inventory, eight scoped notice/reference records and an
  offline byte verifier. The clean D77 darwin/arm64 command binary records four external modules;
  its selected graph has 265 packages. All nineteen embedded metaschemas have published/tag URLs,
  hashes and differences: sixteen structurally match at least one observed official source; three
  remain unequal variants. All 57 compared JSON inputs pass duplicate/nonfinite rejection.
  Forty file-record checks including the binary and nine independent negative verifier controls
  pass; cached module verification, rebuild and executable help also pass. Local Claude advice is
  saved/read/assessed. No runtime/dependency change or Kiro call. The verifier deliberately reports
  no release clearance: historical resource licensing, full component/advisory review, packaging/
  clean-host install/uninstall and owner-rights gates remain open. Run admission, absent login and
  personal CLAUDE.md compatibility are unchanged.
- D77 retains standard-HOME personal rules through a validated native reference, keeping source
  paths for imports and exclusions. Thirty-five actual-Claude/local-responder invocations pass
  across natural/prepared/stripped controls, conditional Read activation, six unusual HOME path
  cases and full four-hop imports. Eight separate counterfactual invocations reproduce rejected
  personal CLAUDE.md adapters; their passing observations are not acceptance of those adapters.
  All four top-level controls pass in 24.203s with separate rule-body/import markers, source/import
  preservation and joined cleanup.
  Personal CLAUDE.md remains absent from the private profile: wrappers lose one import hop,
  direct links bypass original-root exclusions, and a rules entry can omit path-frontmatter body
  text. The prototype wrapper is removed. Local public-only Claude advice is saved/read/assessed.
  Rules share existing source bounds; final personal unit race checks pass in 5.372s. A reference
  does not freeze later source edits or enforce read-only client tool access. Full environment
  preservation and live alpha/release gates remain open. Applicable opt-ins-off race suites pass
  launcher 26.458s, interop 24.853s and command 1.833s; seven installed-client regressions pass
  together in 54.956s. Whole-repository/fixture vet, formatting, whitespace, build and executable
  help pass. The development executable includes D77; run admission is unchanged and live Kiro
  work still awaits login renewal.
- D76 fixes missing personal skills, legacy commands and agent definitions in the temporary Claude
  profile. Bounded source snapshots retain native scope and exact bytes without executing assets
  or modifying their original trees. Thirteen installed-Claude controls pass against local fake
  responses (8.481s), including direct expansion, missing-assets counterfactual, personal skill
  precedence and project agent precedence. No agent is dispatched and no Kiro model call runs.
  Unsafe/oversized sources reject before partial activation; private writes and cleanup preserve
  originals. Public-only local Claude advice is saved, read and assessed. Applicable opt-ins-off
  race suites pass launcher 24.245s, interop 24.644s and command 1.597s. Six applicable installed-
  client MCP/plugin/status/permission/default-tool regressions pass together in 47.157s; whole-
  repository vet, formatting and whitespace pass. Custom roots, relative helper execution, dynamic
  changes and live alpha/release work remain. The development executable is rebuilt with D76 and
  its public help command passes; run admission is unchanged and live work awaits verified login.
- D75 prepares and verifies process-loss/fresh-request recovery with actual Claude and independent
  fake ACP. One exact Read is intercepted before client delivery; the owned ACP group is killed,
  its real error reaches the client and old cleanup joins before a new text request completes in a
  different process. Both client invocations preserve the explicit owner, model/tools/metadata and
  user policy; the native billing-header fragment changes and is preserved unchanged in the request.
  Independent negative controls distinguish caller cancellation/timeouts, missing failure, changed
  policy, results, further tools and requests. The final rehearsal passes in 5.080s. Public-only
  local Claude advice is saved, read and assessed; earlier diagnostic failures are retained.
  The actual-Kiro attempt stops at preflight before any ACP/model work (3.946s). Both pinned 2.21.2
  executables currently return whoami exit 1/account:null; the separate read-only shape test passes
  in 4.816s but does not establish login. Login renewal has been requested. Live recovery stays open.
  Applicable core race suites pass ACP 5.882s, session 27.869s and interop 21.593s; whole-repository/
  fixture vet, formatting and whitespace pass. Only tests/evidence change and the D73 build remains
  current. Development policy admission remains enabled, but current login verification blocks launch.
- D74 verifies launcher cancellation after a delivered tool handoff with actual Kiro 2.21.2/v2 and
  Claude 2.1.263 (17.13s test, 18.514s race package). One exact Read reaches an effect-free held
  client hook while the driver waits for results. Eight launcher cancellations join in 1,061ms:
  one backend Close, closed driver, empty pool and one prepared/cleaned ACP process. Client, hook,
  relay, observed groups and private artifacts are gone; source settings/canary are unchanged.
  There are no tool results, completions, replacements or further requests. The guard and final
  actual-client/fake-ACP rehearsal pass first in 6.038s, including separately observed HTTP Finish.
  This exercises RunClient runtime ownership via a finite print wrapper, not the compiled foreground
  command's Ctrl+C behavior or a disconnect during streaming. The first local harness failure and
  scoped-option correction are recorded; no application or dependency changes and no live retry.
  Final opt-ins-off interop race tests pass in 22.082s and applicable launcher runtime regression in
  5.588s; whole-repository/fixture vet, formatting and whitespace pass.
  Development run stays enabled and the D73 binary remains current. Other alpha/release gates stay open.
- D73 supports successful client tool results followed by separate client text through bounded
  full-history recreation, including the pinned client's model-selected plugin skill expansion.
  Exact history/policy/result ownership, joined old cleanup, the original deadline and the shared
  recreation budget remain required; expired success and replay reject. Independent state/process
  tests precede the change. Actual Claude with the real gateway/relay and independent fake ACP
  completes one Skill/result/text round trip across two joined backend processes; synthetic native,
  prepared and hooks-disabled controls verify the same client shape and source/hook preservation.
  The final complete opt-ins-off race suite passes (session 27.252s, interop 22.639s, launcher
  23.546s), and all 31 installed-Claude top-level controls pass together in 252.391s. An earlier
  registry test accepted only one of two valid deadline sentinels and is corrected without changing
  timing or cleanup requirements. An earlier omit-betas completion failure remains unclassified;
  added fixed diagnostics and 20 finite positive follow-ups preserve the original acceptance rule.
  Whole-repository/fixture vet, formatting, whitespace, build and executable help pass. No dependency
  is added and no actual Kiro model call runs. The development binary includes D73, run stays enabled,
  and actual Kiro skills, full plugin completion and remaining alpha/release gates stay open.
- D72 adds bounded actual-Kiro wait-to-plugin experiments and separate protocol, final-text, display
  and cleanup evidence. The final actual allowance reaches three main requests, two exact tool/result
  pairs, the expanded registry, one native plugin call and two joined prepared processes. It fails
  overall because its 33-byte final answer lacks the required complete marker (68.463s race package);
  the following refusal case is not run. Sources and observed client/MCP/relay/group/artifact cleanup
  pass. Earlier failures and the public-only local Claude consultation are recorded and assessed.
  The final original/revised-marker fake-ACP allowance/refusal pairs and independent controls pass in
  25.368s; the shared UI/hook/Git regression passes in 17.655s. Final opt-ins-off race tests pass ACP
  5.439s and interop 20.887s; whole-repository/fixture vet, formatting and whitespace pass. Actual
  registry/tool progress is partial evidence, not a completed live plugin acceptance gate. Only
  tests/documents change and development run admission is unchanged. Its unchanged D70 executable
  is subsequently rebuilt by D73.
- D71 verifies default-tool Read refusal and joined session recreation with actual Kiro 2.21.2/v2
  and Claude 2.1.263 in 24.81s (26.304s race package). All 25 default tools and thinking/context
  declarations remain present. The exact single Read is hook-denied; its matching result and changed
  standing instruction reach a fresh session after old process/artifact cleanup. Two requests/two
  ACP processes complete with nonempty client output, unchanged canary/settings and observed cleanup.
  The local prepared-process rehearsal and earlier controls pass in 14.364s; guard tests pass in
  1.285s. Pre-live opt-ins-off race tests pass ACP 5.955s and interop 25.058s. A final guard control
  excludes results mixed with new questions; the recorded live request already meets this condition.
  Updated guards and installed-client/fake-ACP rehearsal pass in 5.399s; whole-repository/fixture vet,
  formatting and whitespace pass. One command submission was stopped by an approval-review
  timeout before execution; its permitted resubmission ran the sole live attempt. Actual registry
  changes/plugin turns and the remaining lifecycle/alpha/release gates remain open. Only tests and
  evidence change; no dependency or application code is added.
- D70 corrects D69: cumulative tool history was caused by the synthetic responder reusing a message
  ID. A paired actual-client control proves unique IDs append history normally (8.966s). All ordinary
  synthetic responses now get distinct IDs; the duplicate-ID case is an explicit counterfactual.
  The product keeps regrouped history and mixed prior/new results rejected. The required registry
  replacement now validates full prior history, exact delivered results and unchanged ownership/
  policy, joins old cleanup and preserves the original deadline under a default 16-recreation bound.
  Actual Claude with the real gateway/relay and fake ACP passes held MCP wait followed by plugin
  allowance/refusal in 13.770s: three main requests, one replacement, native counts one/zero,
  unchanged sources and joined processes. The subsequent plugin result resumes that same prompt.
  Final uncached whole-repository race tests pass (session 25.882s, interop 22.255s, launcher 22.678s).
  All 27 existing installed-Claude top-level controls pass together in 223.639s under race,
  including permission UI, bare refusal/deadline recovery, hooks, assets and status. Whole-repository/
  fixture vet, formatting, whitespace, build and executable help pass; the development binary is rebuilt.
  No actual Kiro turn runs; live recovery and remaining alpha/release gates are still open.
- D69 deterministically reproduces WaitForMcpServers followed by an owned plugin call. The MCP
  fixture holds initialization until the synthetic responder has validated the advertised wait
  schema; the actual client then changes its registry and combines prior/new calls and results.
  Canonical block comparisons prove the original prefix and earlier results remain equal while
  trailing standing messages change. The final six-test installed regression passes in 36.175s,
  with one native call, source preservation and joined cleanup. This is client-shape evidence,
  not support for that sequence through the driver/Kiro. D70 corrects the grouping inference and
  implements registry replacement while retaining strict history/result validation.
  Public-only local Claude advice was saved, read and assessed. The uncached opt-ins-off interop
  race suite passes in 20.774s; whole-repository/fixture vet, formatting and whitespace pass.
  No product code/dependency changes.
- D68 retains two bounded native plugin registration files in the private client root while plugin
  content stays at its read-only seed. Actual Claude 2.1.263 passes eleven first-session skill/hook/
  disable/mutation controls and three Git-source preservation controls. A pending local Git revision
  cannot be applied or removed through the prepared seed, while a later native update applies it.
  Sources remain unchanged and observed client/hook/MCP/backend cleanup passes. An earlier combined
  run exposed insufficient readiness evidence: MCP initialization/listing can precede inclusion in
  the first model request. The interactive control now also checks the client's connected `/mcp`
  panel before input. The final ten-test installed regression passes in 51.515s, including allowed
  plugin execution once, hook refusal with zero calls, ordinary default-tool traffic and policies.
  Immediate-input/dynamic-registry compatibility and model-selected skills remain open. All model
  responses use local synthetic fixtures or fake ACP; no Kiro model call runs. Applicable uncached
  opt-ins-off race suites pass: launcher 21.915s, interop 21.068s and command 1.355s; whole-repository
  go vet, formatting, whitespace, build and executable help pass. The development binary is rebuilt.
  No application dependency changes. The separately installed Git tool's test-only scope is recorded.
- D67 preserves existing user/project/local status commands instead of overriding them from the
  command-line layer. An existing user choice stays exact; a bounded conservative project-source
  check suppresses the entire optional default, preventing its refresh timer from merging into
  another command. Unsafe/ambiguous roots and linked worktrees omit that default without blocking
  client preparation or changing routing/hooks. Actual Claude 2.1.263 passes five scope/timer
  controls: only the selected command runs, source bytes remain unchanged, and an event-only
  project command runs once over 7.139s with no late poll. Existing/default status, startup ordering,
  two-turn completion notices and disabled-hook regressions pass together in 47.718s; the separately
  selected enabled user/project policy/hook case passes in 2.985s. All responses are local synthetic
  fixtures, with no Kiro/model-provider calls. Applicable uncached race suites pass: launcher
  23.325s, interop 21.563s and command 1.535s; whole-repository go vet, formatting, whitespace, build
  and executable help pass. Dynamic/managed/custom status sources remain unverified. No dependency
  changes; the development executable includes this correction.
- D66 verifies the owned plugin tool with actual Claude 2.1.263 and independent fake ACP through
  the real gateway, schema worker, relay and session driver. Controlled interactive readiness then
  client allowance produces exactly one native call; a user PreToolUse refusal produces zero and
  returns the matching error. Both complete after D63's single joined backend reconstruction, with
  source bytes/tree/modes unchanged and client/MCP/backend processes gone. A second print launch
  of the same private profile also completes the effect-free tool round trip; its initial warmup is
  a local synthetic fixture observation, not product policy. Immediate fresh print, dynamic tool
  changes, broader plugin assets and actual Kiro plugin/reconstruction remain unverified. Final
  installed plugin-source/tool/default-Read regression passes in 30.723s with race instrumentation;
  uncached opt-ins-off ACP and interop race suites pass in 5.697s and 21.071s. Whole-repository go vet
  and whitespace/formatting checks pass. Only tests and evidence change; no dependency is added.
- D65 supplies the standard user plugin directory through the client's read-only seed contract.
  Actual Claude 2.1.263 full print startup initializes an independently installed plugin's MCP peer
  and discovers its tools. Disable/re-enable choices, original source entries/content/modes and
  cleanup pass. Finite list/init-only commands did not establish this behavior. The final installed
  combined plugin/MCP/default-tool/policy/hook regression passes in 22.414s with local synthetic
  responses only. The plugin tool is absent from the first model request: later discovery and a
  complete tool round trip remain open, as do plugin hooks/skills/agents, existing status commands,
  custom roots and interactive plugin startup. No general asset or release completion is claimed.
  Applicable uncached race suites pass: launcher 22.836s, interop 24.018s and command 1.571s;
  whole-repository go vet, formatting, whitespace checks and the rebuilt development executable
  pass. No dependency is added.
- D64 restores standard-HOME user/local MCP declarations and existing decisions in the private
  client profile without copying unrelated provider or conversation state. Actual Claude 2.1.263
  passes fourteen natural/prepared configuration controls in 8.137s under race instrumentation:
  three active scopes, server disable/re-enable, project refusal, and local/project/user precedence
  for equal names. Prepared commands leave source bytes unchanged; all observed MCP processes and
  private profiles are cleaned up, and no tool/model call runs. Natural client commands do change
  the owned global file, so simply removing the private profile is not a preservation solution.
  Plugin/skill/agent assets, existing status commands, custom configuration roots and remote MCP
  OAuth remain open. Development launch admission is unchanged; no dependency is added.
- D64's final installed regression, client-mcp-regression.qMLLwo, passes in 15.642s with fake ACP
  and local gateway only: MCP controls (6.36s), ordinary default-tool text/Read denial (4.90s), user/
  project hook and permission preservation (1.70s), and disabled hooks (1.43s). Routing stays local,
  denied content remains unavailable, and enabled/disabled hooks retain their expected behavior.
  Applicable uncached race suites with installed opt-ins off pass: launcher 22.466s, interop 22.239s
  and command 1.775s. Whole-repository go vet, formatting, whitespace checks and the rebuilt
  development executable pass.
- D63 verifies ordinary Claude 2.1.263 text with 25 default tools and fixes a default-tool Read
  denial continuation whose standing system text changes. One exact full-history continuation can
  revoke/join old work and recreate with the supplied result and new instruction under the original
  deadline. Strict ownership/result validation and a one-restart allowance remain enforced. Both
  installed-client/fake-ACP cases pass in 7.585s under race instrumentation, with source settings
  unchanged and both observed process groups gone. Actual Kiro recovery, user-asset preservation
  and alpha/release gates remain; reconstruction can add provider work and loses hidden context.
- D63's final uncached race regressions pass with installed-client/model opt-ins disabled: relay
  9.972s, session 19.542s, Anthropic validation 8.089s, gateway 3.362s, interop 21.739s, launcher
  21.774s, command 1.554s and ACP transport 4.851s. Whole-repository go vet, formatting and whitespace
  checks pass. The development executable is rebuilt with this recovery path; no dependencies change.
- D62 enables development run on macOS arm64 for exact Kiro 2.21.2/v2 and Claude 2.1.263 after the
  D59-D61 isolation/tool gates. The shared configuration generator passes actual Bash approval and
  hook refusal (48.099s race package), with exact results/effects and complete observed cleanup.
  Compiled doctor/models/run with independent CLIs/ACP and an owned PTY passes in 7.965s. Actual
  installed doctor reports policy verified and launch_available true, with 19 models and no model
  prompt. The development executable is rebuilt. Default-client compatibility, environment overlay
  preservation and live alpha/release gates remain; persisted load is disabled for this launch path.
- D61 passes active initial skill controls on Kiro 2.21.2: explicit inclusion, default inheritance
  and suppression across owned launch and KIRO_HOME skill roots (17.755s race package). Both active
  sources disappear under suppression. The relative launch skill name is recorded separately from
  the absolute configuration-root name; ACP skill-command absence is not used as exclusion proof.
  All source bytes and process cleanup pass. Final independent interop race suite passes in 24.082s;
  whole-repository go vet passes. Production policy wiring is next; reload/load remain disabled.
- D60 adds original client names and client permission/hook authority to relay descriptions while
  preserving opaque wire names, complete descriptions and schemas. Registry identity changes with
  this metadata policy. Actual Kiro 2.21.2/v2 and Claude 2.1.263 now pass all six rule/hook cases:
  allowed Read/Write/Bash, denied Write/Bash and Bash hook refusal. Bash passes separately in
  25.858s; the other five pass in 118.095s, with exact effects/results and complete observed cleanup.
  Earlier Bash failures led to independently verified JSON comparison and bounded description-field
  corrections in the test observer. No failed attempt is counted as a pass. Interactive display
  remains verified with fake ACP; skill/source isolation and the production launch adapter remain.
- D59 migrates finite preflight to exact Kiro 2.21.2 main/helper after fresh installed account and
  CLI/ACP catalog checks pass (19.363s race package). All 19 model identities and aliases match;
  no model prompt is sent. Old/future/mismatched binaries reject before account lookup. Versioned
  cache and candidate identities remain separate, and production run still fails the policy gate.
  Final uncached race suites pass: launcher 22.353s, interop 24.294s, catalog 1.489s and command
  1.787s, with installed opt-ins off; whole-repository go vet and whitespace checks pass.
  Fresh 2.21.2 agent-directory controls (26.64s) and active MCP inclusion/exclusion (46.75s) also
  pass, with a combined race package of 74.661s and observed relay/process/artifact cleanup.
  One fresh 2.21.2 native-effect challenge passes in 23.784s: completed response, no tool event,
  canary disclosure or workspace/policy change, and complete observed cleanup. Its model prompt
  takes 6,037ms. This refreshes that initial-session result; remaining paths still gate run.
  A fresh actual Claude 2.1.263 / Kiro 2.21.2 Read-hook refusal also passes in 25.619s: one exposed
  Read, exact returned denial, same-prompt completion and client exit 0, with source/canary and
  cleanup checks passing. Each live experiment runs once. The development executable is rebuilt.
- D58 observes the installed Kiro main/helper reporting 2.21.2; the reason for the change is unknown.
  At that checkpoint, preflight remained pinned to 2.21.1 and rejected 2.21.2 before account lookup.
  The separate empty-agent read-only observer admits exactly 2.21.2 and sends no model prompt. One explicit file
  resource and seven default-resource controls pass: active AGENTS/steering entries become absent
  under suppression, including separate launch/session directories. Only the launch directory's
  settings override re-enables inheritance in this matrix. The absolute steering path identifies
  that directory; AGENTS.md is reported relatively and is not assigned an invented absolute base.
  All owned source bytes and observed cleanup checks pass. The seven-case race package passes in
  37.905s; the explicit-resource case passes in 6.181s. Skills, other default roots, reload/load
  and actual Kiro-approved tools remain unverified; D59 refreshes native effects and client refusal.
  Final uncached race suites pass: interop 24.291s, ACP 5.689s and launcher 21.469s, with installed
  opt-ins off. Whole-repository go vet, formatting and whitespace checks also pass.
- D57 fixes the next-question path after bare permission refusal. The client defers its matching
  error result until the next question and combines it with new text; no immediate cancellation
  request or Stop/PostToolBatch hook was observed. Exact owner/history/result validation now retires
  the old ACP before a fresh full-history prompt. Independent tests cover pending and expired owners,
  invalid inputs and replay rejection. Actual Claude/fake ACP passes both unchanged 45-second
  deadline cleanup (48.13s) and next-question recovery (7.37s), package 57.245s. The old process and
  private artifacts were gone before the replacement launched; the denied file stayed absent.
  This does not establish an immediate signal for silent UI interruption or real Kiro recovery.
  Final installed-client regression passes in 58.111s, retaining the original Read denial, six
  rule/hook cases and five interactive controls alongside observation/recovery. Relevant uncached
  race suites pass: session 17.193s, projection 1.911s, interop 23.582s, gateway 3.759s,
  Anthropic 8.240s and ACP 5.066s. Whole-repository go vet, formatting and whitespace checks pass.
- D56 adds actual interactive Claude/fake-ACP Write/Bash approval and refusal with an entered reason,
  plus a no-input Write counterfactual. The first five-case race run passes in 22.338s. The current
  screen, exact pending operation, displayed file content/command, absence before approval, returned
  denial reason, actual effects/hooks and process cleanup are checked independently. Narrow title
  requests have a separate bounded synthetic response and cannot count as tool continuation. D57
  adds bare No evidence; real Kiro-generated successful tools and broader isolation remain open.
  The final installed regression passes in 52.612s: five interactive cases, six rule/hook cases,
  the original Read denial and status-only UI. No project permission-settings file was created by
  any interactive case. Whole-repository go vet and whitespace checks also pass.
  The final uncached interop/requestfamily race suites pass in 21.854s/2.216s with installed opt-ins off.
- D55 passes six actual Claude/fake ACP tool cases: allowed Read/Write/Bash, denied Write/Bash and
  a Bash hook veto. The exact returned result resumes the same ACP prompt; allowed effects and
  hooks occur, denied effects do not, and observed relay/process/private artifact cleanup passes.
  The six race-enabled cases pass in 21.621s. D56 adds the separate interactive cases; real Kiro-generated
  successful tool requests remain unfinished. No external inference ran in D55.
  The final uncached ACP/interop race suites pass in 5.718s/21.814s with installed opt-ins disabled.
  The existing actual-Claude/fake-ACP Read-hook denial also passes after the shared guard change
  (3.52s test, 4.809s package); it still requires the original exact hook refusal.
- D52 establishes an active MCP inclusion/exclusion comparison on Kiro 2.21.1/v2: true starts the
  owned global/workspace settings/mcp.json servers, false excludes both, and the candidate retains
  only its relay. All observed processes and private relay artifacts are removed. This closes the
  missing initial-session positive control for those paths, not resource/reload/load restrictions.
- D54's fresh native-effect challenge passes with unchanged prompt/deadlines: end_turn, 865 assistant
  text bytes, no observed canary/tool event/file change and complete observed process cleanup. The
  prompt took 6,437ms; the race-enabled package passed in 28.003s. D53's earlier incomplete attempt
  remains a failure with an unproven cause. This initial-session result does not close resource,
  reload/load or successful client tool gates, and does not open production run by itself.
- D52's installed inclusion/exclusion comparison passed in 43.20s, while D53's model attempt failed
  in 37.64s. Those results are retained separately. The new native observer's first build failed on
  missing probe APIs; its final ten fake controls passed in 3.196s under race instrumentation.
  The final uncached ACP and interop race suites passed in 6.074s and 21.134s with all installed-model
  opt-ins disabled. Request validation, title classification and optional effort race suites also
  passed in 8.299s, 1.360s and 1.336s. Whole-repository go vet, formatting and whitespace checks passed.
- Development run now has a distinct documented gate from internal alpha and release. The initial
  compatibility claim is the exercised Claude Code Messages subset; optional web/usage and full API
  parity are not prerequisites for development launch. The production policy gate remains closed.
- D51 is the first approved real Kiro model-turn result: one client Read request, its exact Claude
  hook denial and the matching final completion passed through the prepared ACP/relay path. Cleanup
  and canary checks passed. Native/inherited execution restrictions and the production run gate stay
  unverified; this result does not close R06 or the complete live/release requirements.
- D42 implements authenticated relay group membership and bounded lifetime. D43 observed five
  explicitly registered relays, but its omitted-field standalone MCP control did not activate.
  D52's explicit true control resolves that initial comparison for two paths. Native execution
  restrictions and complete live/release gates remain open. Historical attempts appear below.
- D44's first-session controls keep the launch agent's inventory when a separate session workspace
  contains a same-named conflicting agent. Reload, inherited effects and native denial remain open.
- D46 verifies agreement of all 19 CLI/ACP model identities and client aliases, including auto, in
  one owned empty-agent session. No model selection or prompt was sent; live turn gates remain open.
- D47 connects cached status to a temporary client status-line command with UI-only authority,
  bounded HTTP/output and runtime cleanup. This supplies no live account-usage adapter or Kiro
  execution-policy proof; the dedicated evidence below distinguishes helper and actual UI checks.
- D48 observes visible status while another SessionStart hook is still running in installed Claude
  2.1.263. Hook completion and visible UI must remain separate from unverified full initialization.
- D49 adds a bounded startup model notice using the prepared alias and UI-only credentials. Actual
  Claude displays it, keeps existing hooks/Read denial, and excludes it from the exercised model
  inputs. Disabling hooks preserves conversation completion. Feature/policy verification remains open.
- D50 adds synchronous Stop completion notices and a bounded wait for terminal-response bookkeeping.
  Actual Claude separates one title request from two foreground turns and displays two completion
  notices plus the updated status. This uses a local synthetic responder, not a Kiro model call.
- The original fourteen repository Markdown documents were read in README order, with README and
  AGENTS first. LIVE_KIRO_TEST_PLAN.md now adds the concrete scope of a separately opted-in test.
- Specification-only baseline committed as `7b108dd`.
- Go selected by explicit user instruction; comparative experiments remain unmeasured.
- Local host: macOS 15.4 (24E248), arm64; Go 1.27.1 downloaded into the ignored repository cache.
- Initial public CLI black-box version/help checks: Kiro 2.21.1 and Claude Code 2.1.263, without
  a model request at that checkpoint. D51 below records the later first live model turn.
- Phase 1 tests first failed because implementation packages were absent, then passed after the new
  transport was implemented. Additional error-envelope/correlation/notification regression tests
  reproduced five failures before their fixes.
- Passed `go test -race ./internal/acp ./internal/kiroauth ./internal/ndjson` (ACP 6.759s on final run).
- Passed `go vet ./...` and `git diff --check`.
- Passed `go test ./internal/ndjson -run '^$' -fuzz FuzzObject -fuzztime=5s -parallel=2`:
  74,534 fuzz executions, no failing input; runtime 6.647s.
- Through Phase 3, `go list -m all` reported only this module; Phase 4 dependencies are recorded below.
- Phase 2 independent HTTP and request-validation tests were written before their adapters. The
  session test suite first failed because no implementation existed. An additional encoded-output
  overflow test reproduced an incomplete SSE sequence before the terminal-error fix.
- Passed `go test -race ./...` after integrating the fake ACP text backend: ACP 5.597s, session/HTTP
  integration 3.472s, gateway and parser suites passed (cached on this final integration run).
- The gateway race suite separately passed in 1.971s with loopback socket permission. It tests exact
  route credentials, malformed/body-size validation, SSE equivalence, early/late auth markers,
  first/total deadlines, output-byte limits, request admission, disconnects and failed writes.
- Phase 3 tests first failed on absent catalog/cache/effort/private-file APIs. Regression tests then
  reproduced contradictory model-selection acceptance, setup shortened by the turn timeout, cached
  effort status blocked by an RPC, and a trailing-separator directory-symlink bypass before fixes.
- Passed uncached `go test -race -count=1 ./...`: ACP 6.075s, gateway 2.110s, session 4.732s,
  catalog 1.878s, effort 2.427s, private files 2.877s; all other packages passed. Use `-count=1` for
  fixture changes because TestMain invokes an external build of the independent fake child.
- Passed `go vet ./...` after the Phase 3 implementation.

Commands use `GOTOOLCHAIN=go1.27.1`, `GOMODCACHE="$PWD/.cache/gomod"` and
`GOCACHE="$PWD/.cache/gobuild"` in this sandbox. No performance comparison with Rust is claimed.

### Public development commands and owned startup

D35 adds doctor/models/run, pinned executable/settings/account/catalog checks, stable keyed profile
identity and full internal startup/runtime ownership. New preflight tests first failed on absent APIs.
The independent successful runtime fixture suite passed in 7.520s: cached catalog/text, completed
tools, client exit during tool handoff and repeated cancellation all join cleanup. Only final delivered
responses save the last-model preference. Source settings and caller descriptors remain unchanged.

The actual compiled command boundary passed against independently authored CLIs in 6.461s, including
JSON doctor/model output and run exit 3 for the unverified policy. No installed Kiro/Claude process,
external inference or actual client tool was used by these tests. Post-preflight cancellation and
replaced-runtime regressions passed in 3.467s. The stable-key race suite passed in 3.941s and its
16-caller cold initialization passed ten repetitions in 1.933s. Concurrent first file creation can
produce a safe state failure on this host; no key divergence or overwrite was observed.

Command tests reproduced four edge failures before fixing cleanup diagnostics joined with another
error, empty explicit options, bounded timing writes and pinned version display. The final command
race suite passed in 1.997s, with command vet and diff checks. Review then reproduced missing parent
cancellation when the final runner cleanup canceled the caller; both Run and Inspect now preserve
that cancellation alongside cleanup errors.

The complete uncached `go test -race -p 2 -count=1 ./...` passed: command 1.912s, ACP 5.202s,
pool 2.803s, Anthropic 7.969s, childproc 5.970s, gateway 3.026s, launcher 13.685s and session 9.725s;
all remaining packages passed. Installed-client tests stayed opt-in and were skipped in this suite.

A subsequent installed-CLI doctor passed version/account preflight but its catalog command timed
out. A bounded observer confirmed exit -1, 3,672 output bytes, timeout and successful group cleanup
at 5.109 seconds. One fifteen-second observation then exited 0 at 8.559 seconds with a valid 19-model
catalog. No stderr or catalog/account values were logged. D35 now permits fifteen seconds only for
the exact catalog listing, retaining five seconds for ordinary checks and the original caller limit.
An independent delayed CLI verifies the separate deadlines and cleanup. Nine command regressions
also replaced generic catalog/state errors with fixed stage-specific instructions.

The final focused launcher/command race suites passed in 15.164s and 1.857s after these local changes.
`go vet ./...` and `git diff --check` passed. Rebuilt doctor then completed against unmodified installed
Kiro 2.21.1 and Claude Code 2.1.263: login verified, 19 catalog models, policy unverified and launch
unavailable. Its login/catalog phases took 2.661s/8.370s. This supersedes the earlier unknown login
observation for this check only; it does not prove future credential validity or effective tools.
No ACP session, model prompt, client tool effect or login/logout command was used. The installed
CLIs may maintain their own account/cache metadata; no claim of globally untouched CLI state follows.

The built-in Kiro policy adapter remains unavailable. Run cannot start actual model traffic, and
doctor explicitly reports that fact. A package-private fake adapter tests the complete runtime path;
no trust flag or user-supplied verification record can select it. Client initialization timing and
complete interactive behavior, asset preservation, verified capabilities, R16 request controls,
real policy/load proof and release work remain open. D47/D49/D50 add the separate status display,
startup model notice and turn-completion hook below.

The user subsequently explicitly authorized an independent local Claude CLI consultation and asked
that Claude save its answer to a file for review. That authorization superseded the earlier automatic
approval rejection of external transmission. A direct Claude 2.1.263 print-mode invocation completed
with exit 0 and wrote `.cache/claude-consult/work/answer.md` (4,762 bytes); the entire answer and the
CLI result were read. The owned working directory, safe mode, Write-only tool selection, empty strict
MCP configuration and no-session-persistence setting limited its inputs to the supplied independent
project facts. No previous implementation was supplied or consulted. An earlier invocation with a
different stripped environment failed; the successful direct invocation establishes neither its
cause nor a need for login/logout. This consultation used Claude inference, separately from the
product's Kiro routing. No Kiro model prompt had been sent at that checkpoint; D51 records the later
explicitly approved live turn.

The accepted advice is a disposable tool-denial experiment correlating relay arrival, client refusal
and absence of a sentinel effect. This would establish evidence for the exercised path only; R06
still requires native filesystem/shell/task/subagent and inherited-configuration attempts. The review
rejects unrestricted request/response recording, silent removal of unsupported constraints, an
experimental public gate bypass, and treating one text response or a capability table as complete
restriction proof. The answer's claim that Claude never recovers from a 400 also exceeds the narrower
D33 observations. Independent fixtures and state-machine tests remain required and useful. The
answer is advisory material, not an authoritative protocol contract.

### Documented client options and Kiro configuration isolation

Four local positive-response cases on unmodified Claude 2.1.263 passed in 1.919s (test 1.68s):

| Test-only options | Thinking declaration | Context declaration | Beta header values | Result |
| --- | --- | --- | --- | --- |
| Default | adaptive | present | 7 | One request, exit 0 |
| Disable thinking | absent | absent | 7 | One request, exit 0 |
| Disable experimental betas | adaptive | absent | 4 | One request, exit 0 |
| Both | absent | absent | 4 | One request, exit 0 |

Every case used an owned HOME/project, disabled tools and an independently authored local HTTP
response. None requested structured output. No Kiro, external model or actual client tool was used.
Only field names, fixed kinds, presence flags, counts and byte lengths were logged. The documented
variables are `CLAUDE_CODE_DISABLE_THINKING` and `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS`; neither is
adopted by production startup. Title generation, tool round trips, structured output and compaction
need separate observations. D36 records the limits of these results.

The new `TestKiroOwnedHomeAgentSelection` requires both pinned binaries, then attempts global-agent
inventory under an empty owned HOME and two owned KIRO_HOME roots. Its installed run failed in
2.667s: both version checks passed, but the first main-binary HOME-baseline `agent list` returned
exit 1 with an authentication marker (64 bounded bytes). No synthetic agent marker appeared. The
process group was gone and runner ownership released. Subsequent inventory cases were not run;
global-agent search-root selection and restriction remain unverified. This failure does not imply
that the existing real-HOME account is logged out. No session or model prompt was created.

The separately reviewed `TestKiroOwnedHomeIdentityContinuity` then passed in 9.486s (test 9.26s).
It retained the existing HOME only in the finite child commands and compared baseline, owned root A,
owned root B and baseline again with one ephemeral HMAC key. All four identities matched, both pinned
versions passed in each scope and every process group was gone. Only equality/status/size facts were
reported; no identity value, digest or credential was retained in the report. No credential copying,
login/logout, agent activation, ACP or model call occurred. This establishes continuity for these
read-only invocations, not configuration isolation or future authentication validity.

`TestKiroAccountHomeAgentSelection` independently supplies the existing HOME to child commands while
writing all agent fixtures in owned directories. The first main-binary A inventory contained its
expected marker but timed out after five seconds (870 bytes, exit -1); that output did not pass the
gate. A later observation allowed fifteen seconds only for these account-backed list commands,
retaining five-second versions and a seventy-five-second overall bound. It passed in 34.453s (test
33.05s): both main/helper listed the selected A/B marker and neither other synthetic marker, exited 0
and left no process group. This establishes distinguishable owned-agent search roots for those
invocations. It does not prove an exclusive inventory, agent activation, inherited-MCP exclusion or
execution restrictions. No real-HOME agent file was written and no model/session was created.

The fully synthetic `TestKiroOwnedHomeSettingsDiscovery` passed pinned versions and confirmed the
public help's KEY/VALUE positional interface, but its first setter returned exit 1 with 46 bounded
diagnostic bytes. The initial fixed authentication/syntax markers were absent. The process group was
gone. This installed run failed in 2.449s; no successful setting persistence or readback is claimed.
Expanded fixed diagnostics reproduced the failure in 4.017s and identified missing-file/directory
and OS error 2 markers, although the owned settings directory and cli.json existed. A bounded file
inspection in a subsequent 2.616s run found only an empty JSON object (2 bytes), without the requested
Boolean. The error is not reclassified as successful persistence or an authentication failure.
The real HOME was never used by these settings experiments.

A differential synthetic-HOME case precreated the documented default .kiro/settings/cli.json and
its parent, but still failed identically in 3.666s with an empty alternate-root file. This did not
support the hypothesis that merely supplying that fallback directory would repair the setter.

A second explicitly authorized direct Claude consultation completed with exit 0 and saved
`.cache/claude-consult/work/answer-settings.md` (2,558 bytes). The entire answer was read. A 120-second
CLI deadline with a five-second kill grace, safe mode, Write-only tools and empty strict MCP
configuration bounded the invocation. Its proposed independent A/B JSON getter experiment and
main/helper public-help comparison were adopted. Its stronger claims about credential storage and
exclusive agent isolation were rejected: matching account scopes and distinct synthetic markers do
not establish either claim. An arbitrary undocumented setting key was not adopted as a probe.

Both binaries' public settings help then advertised KEY/VALUE positionals (924/934 bounded bytes).
Independently seeded flat Boolean A/B JSON files still did not make the empty-HOME main getter
succeed: the first read returned exit 1 with the same missing-file/OS error 2 markers, in 3.551s.
The failure is therefore observed on this getter as well as the setter. Its underlying cause remains
unknown; no claim of successful settings isolation follows from the failed commands.

The account-HOME getter initially emitted 14 bytes but timed out at five seconds (6.021s test run).
With a fifteen-second getter-only limit it exited 0, but the bare-Boolean assertion failed because
the public command defaults to Markdown output (8.563s run). The actual installed help explicitly
advertises --format json; this was a probe output-format mistake, not evidence that the configured
value was ignored. Version/help retain five seconds, getters receive fifteen only in the account-HOME
observation, and the whole two-root observation is bounded at fifty-five seconds.

`TestKiroAccountHomeSettingsReadback` then passed in 15.923s (test 14.86s) using the exact read-only
command `settings chat.disableInheritingDefaultResources --format json`. It read true from independently
written A and false from B, both exit 0, with the expected flat JSON files and no surviving process
groups. No setter is allowed with the account HOME; the command wrapper admits only exact version,
help and that JSON getter. This establishes settings readback/search-root behavior for this key and
these invocations. Effective resource suppression and setter success remain unverified. Applying the
same JSON output option with a fully empty HOME still failed with the missing-file/OS error 2 markers
in 3.159s; that separate environment requirement remains unresolved.

Installed ACP help also explicitly advertises v1/v2/v3 engine selection (default v2) and, for v3,
--auth-method cli to keep credential resolution in the Kiro process. This confirms a separate engine
candidate exists in the pinned binary. No v3 ACP process/session was started, and no 3.x agent field
was consequently adopted for the current v2 adapter. Its configuration, protocol and restriction
behavior need separate observations before an engine decision.

The combined installed-Claude negative/positive race suite passed in 6.564s (six rejection/control
observations and four option observations). The observed failure shapes remain negative evidence,
not successful recovery. The final ordinary interop race suite passed in 2.109s with installed-client
opt-ins unset; `go vet ./...` and `git diff --check` passed. The explicit failed Kiro observations above
remain failed live gates, not results covered by that skipped run. No production option or dependency
changed.

### MCP configuration writers and inventory boundaries

The initial TestKiroOwnedMCPConfigurationDiscovery failed in 37.644s: both pinned versions and help
passed, but the two account-HOME global inventories each returned 37 bytes and the two workspace
inventories each returned 71 bytes, all exit 0 with none of the six owned standalone-file markers.
Every process group was gone. This did not establish configuration-root selection or MCP exclusion.

The first fully synthetic-HOME main-binary writer probe failed before writing: mcp add --help exited
1 with 46 bounded bytes (2.510s run). Direct helper invocation then completed help and the disabled
workspace writer, producing exactly one matching work/.kiro/settings/mcp.json of 136 bytes. Its
workspace list still omitted the marker and failed the original assertion (3.403s run, 2.47s test).
No real-HOME setter, authentication mutation or session was used. The main/helper difference remains
unexplained; the production launcher is not switched to the helper by these observations.

An enabled control initially failed the probe's requirement for an explicit false field: the writer
omits disabled in that case. The inspection now accepts its documented false default while still
rejecting a present non-Boolean value. This is a fixture assertion correction, not permission to
ignore an explicit disabling constraint.

The final differential setup independently authors a local empty agent, then compares --scope
workspace with --agent using the same /usr/bin/false server. The four-case run took 9.927s:

| Writer target and state | Matching owned file | Listed agent/server marker | Result of original complete-inventory assertion |
| --- | --- | --- | --- |
| Workspace file, disabled | settings/mcp.json, 136 bytes | Absent | Failed |
| Named agent, disabled | agents/dax-mcp-owned-agent.json, 354 bytes | Both present | Passed |
| Workspace file, enabled | settings/mcp.json, 112 bytes | Absent | Failed |
| Named agent, enabled | agents/dax-mcp-owned-agent.json, 330 bytes | Both present | Passed |

The relative files in this table are under the owned work/.kiro directory. Both state variants
completed their commands and joined all groups. The listed distinction is therefore not explained
by disabled filtering. It does not prove either file's effective treatment by ACP. The public list
output is agent-oriented in this observation; merely failing to list a standalone MCP declaration
cannot serve as inherited-configuration exclusion proof.

TestKiroOwnedMCPWriterInventoryObservation retains the successful named-agent cases and explicit
negative standalone-file controls. TestKiroOwnedMCPFileInventoryObservation retains the six-marker
negative A/B observation. Passing these narrowly named observations must not be reported as passing
R06 or the original complete-inventory assertion. D37 records that boundary. Their settings writes
are entirely under a synthetic HOME; the account-HOME case admits only finite version/help/list
commands. Captures are bounded and reports contain fixed markers/counts, not arbitrary user values.
No model prompt, MCP connect/status command, new dependency or production-policy change is involved.

The final opt-in installed-CLI race run passed in 50.167s: the four writer/list cases took 9.75s and
the main-binary A/B file observation took 38.71s. Every finite command exited normally and every
owned process group was gone. This confirms the positive/negative observations above; inherited
MCP exclusion and effective tool restrictions remain unverified. The ordinary interop race suite
with installed-client opt-ins unset passed in 1.331s; whole-repository go vet and whitespace checks
also passed. No broader acceptance or live-model result is inferred from the skipped opt-in cases.

A proposed third Claude consultation was rejected by automatic approval review before transmission.
The stated reason was external transmission of internal observation/path/configuration information
without specific approval for that payload and destination. The exact proposed text is saved in
.cache/claude-consult/work/prompt-mcp.txt; a user approval request is pending. It has not been sent
through an alternate route, and no answer-mcp.md is claimed. The two earlier completed consultations
remain the only successful external advisory invocations recorded above. Local observations continue
independently while that specific transmission is pending.

### Synthetic HOME identity and retained dependency notices

TestKiroOwnedHomeHelperIdentity compares an account-HOME baseline, a synthetic-HOME main identity,
a separate synthetic-HOME helper identity, and the original baseline again. Every case uses an
owned KIRO_HOME, explicit environment, pinned main/helper versions and the same ephemeral HMAC key.
The twelve-command installed run failed its continuity gate in 9.302s (test 9.08s): both synthetic
identities exited 1 with 17 bounded stdout bytes; the before/after account identities each exited 0
with 251 bytes and matched. All process groups were gone. No account values/digests, credentials,
agent/session data or model content were logged.

This rules out treating D37's successful helper configuration commands as authentication proof.
The two synthetic HOME cases remain failed live checks; the test is not changed to claim successful
identity continuity from their failure. D38 records the distinction and retains the production HOME,
executable and policy gate. No login/logout, credential copying or model prompt was used. The first
local compile exposed a test cleanup call incorrectly used as an error return; it was corrected to
the existing runner's void Close API and explicit active-process check before the installed run.

TestKiroPinnedACPInitializationOnly now gives the same owned KIRO_HOME to preflight and ACP while
retaining the existing account HOME. Its installed race run passed in 8.459s (test 6.62s; ACP start
through cleanup 4.112s). It negotiated version 1 with Kiro 2.21.1/v2, reporting loadSession, image
and HTTP MCP true; audio, embedded context and SSE MCP false. The group was gone. This is actual
bounded initialization evidence and supersedes the earlier preflight failure for this environment.
It sent no session/new or session/prompt and does not establish effective agent/tool restrictions.
The launch gate remains closed, and no production timeout, executable, HOME or cache identity changed.

Dependency review obtained CLDR 32's exact Unicode-DFS-2016 notice from its official release archive
and the exact LLVM compiler-rt license at the revision named by the installed Go race runtime. Both
texts were read in full, retained under third_party/notices with runtime/test scopes, and checked
byte-for-byte against the downloaded sources. DEPENDENCY_REVIEW.md records their URLs, sizes,
SHA-256 digests and remaining subcomponent/artifact limitations. The CLDR archive remains ignored;
no upstream tool implementation, locale corpus or fixture is incorporated into this project.

The official JSON Schema source README clarifies an AFL-or-BSD alternative, including at the 2020-12
release tag. Matching that authority and its copyright notice to all nineteen embedded metaschemas
remains open. Unicode 17 notices, full artifact/toolchain attribution, advisory review, owner rights
and project-license selection were also unfinished at that point (D121 later completes the
attribution and advisory items and moves owner rights and license selection outside the repository).
Retaining two dependency notices does not clear release or change any runtime dependency.

The final ordinary interop race suite passed in 1.323s with installed-client opt-ins unset;
go vet ./... and git diff --check passed. The separate installed initialize-only race result above
is positive evidence; the synthetic-HOME identity failures remain explicitly failed live gates.

### Read-only ACP command envelope and effective inventory observation

D39 adds an independently authored fake ACP inventory peer and a bounded test-only observer.
The first protocol suite failed on missing observer APIs. Nine fake cases now cover a successful
empty/one-item response, missing or unavailable advertisement, foreign ownership, malformed and
duplicate command names, negative command success and malformed success. A separate diagnostic
test checks event/byte limits, fixed error-marker privacy and unchanged authentication classification.
No fixture can dispatch a prompt or client tool. Its provenance manifest includes the public Kiro
ACP and 2.x references; all text and IDs are independently invented.

Actual Kiro 2.21.1/v2 successfully creates a session in an owned KIRO_HOME/workspace and advertises
25 commands, including tools. The original name/arguments interpretation fails with -32700;
fixed parse markers identify command, args and an object argument shape. The successful request is
`{sessionId, command: {command: "tools", args: {}}}` on `_kiro.dev/commands/execute`.
One intervening run failed account preflight before ACP (5.394s); no particular account state or
timeout cause is inferred. Later pinned checks pass without changing production deadlines.

The final installed race run of TestKiroPinnedReadOnlyToolsInventory passed in 14.267s (test 12.92s):

| Independently declared tools | Returned data.tools | Response bytes | Result |
| --- | --- | ---: | --- |
| Empty array | Empty array | 98 | Query succeeds; group gone |
| fs_read only | One item, named read | 814 | Query succeeds; group gone |

The earlier assertion that listed/configuration names are identical failed before the explicit
fs_read-to-read mapping was recorded. Item name, description, source and status have string types;
status/description values are not logged. A prior two-case shape observation passed in 15.558s but
did not yet assert names. The final check verifies both counts and the observed native name. These
are meaningful positive/negative inventory controls, not proof of attempted execution denial.

No model prompt, client tool effect, credential copying or login/logout was used. Each case uses
existing account HOME, an owned configuration/workspace and a newly authored agent, with all declared
resources/hooks/MCP servers empty. Kiro may maintain its own state. Only bounded schema field names,
kinds, sizes, fixed tool-name matches and parse markers survive in test diagnostics. Current effort
arguments are still unverified; the tools envelope does not confirm the old D09 effort interpretation.

The ordinary uncached interop and ACP race suites passed in 3.563s and 5.704s, with installed opt-ins
unset. Whole-repository go vet and git diff --check passed. No production code, dependency, timeout
or launch-policy gate changed. Inherited MCP/hook exclusion, effective relay initialization, native
execution attempts, live prompting and the other acceptance/release gates remain open.

### Actual relay enumeration and remaining process ownership

D40 extends the independent inventory peer with owned, missing, foreign-session and wrong-server
MCP readiness. The observer APIs first failed to compile before implementation. A later regression
demonstrated that a different server's unrelated description could incorrectly satisfy the intended
name check; readiness now requires the exact serverName field. Thirteen fake protocol cases cover
the complete observation workflow without a model prompt or client tool effect.

TestKiroPinnedRelayInventory builds this repository's actual relay, creates a fresh alias and D27
candidate, and leaves broker call admission closed. The initial installed run passed in 12.396s;
the subsequent server-name-aware run passed in 8.698s (test 7.43s). Kiro 2.21.1/v2 returned exactly
one fresh bare alias in a 254-byte response and two initialization notifications with matching
serverName/sessionId string fields. No native tool was listed. Pending relay work remained zero,
private relay configuration was removed and the ACP group was gone. These runs did not yet directly
identify the relay process or test forced parent loss.

The new independent PID/group wrapper preserves the actual relay's stdio and execs that same binary.
Its ordinary process-identity control passed together with the inventory regressions in 5.306s.
The instrumented installed run then **failed** the same-group ownership assertion in 9.274s
(test 8.87s): one relay process was recorded in a group different from ACP. That PID was gone after
normal ACP Close, so no surviving child was observed and the emergency PID kill path was not used.
The fresh alias, matching server notifications, zero pending work and artifact cleanup still passed.

This is a failed group-membership assumption and an unresolved production ownership/join path,
not a failed tool-list exchange or evidence of a leaked process in the normal-close run. The
installed ownership gate remains failed rather than treating the earlier uninstrumented success
as whole-tree cleanup proof. The next lifecycle work must establish supervised cleanup for the
separate relay group and test forced ACP loss and pending-tool cleanup. The diagnostic wrapper
does not itself grant that supervision or open the production policy gate.

No Kiro model prompt, external provider fallback, actual client tool, credential copying or
login/logout was used. No production dependency or timeout changed. Inherited MCP/hooks, native
execution attempts, full live client turns and the remaining R06/release requirements stay open.

After the exact-server regression and record bounds were finalized, ordinary uncached interop/ACP
race suites passed in 8.845s/7.975s, with installed opt-ins unset. Whole-repository go vet and
git diff --check passed. Those results do not supersede the separately failed installed group gate.

### Cooperative relay group-join experiment

D41 adds a separately named group-joining observation variant and an independent fake ACP mode
whose MCP child initially has its own group. The first normal-close test failed its movement
assertion; the completed experiment passed normal, forced and pending-tool shutdown with eight
concurrent Close calls. The group experiment and ordinary identity control passed their uncached
race run in 10.105s. No production relay lifecycle or execution-policy gate changed.

The first installed attempt stopped during account verification (5.107s), before ACP/relay setup;
the package failed in 7.131s. One retry retained the same deadlines and passed in 10.667s
(test 8.62s). One relay changed into the owned ACP group, one fresh bare alias appeared in the
254-byte inventory, and the relay PID, ACP group, pending work and private configuration were all
gone after normal shutdown. No native tool was listed and no model prompt or tool effect ran.

The experiment establishes that cooperative group joining is possible with this installed Kiro
version on macOS. Production still needs authenticated group binding, kernel peer verification and
a bounded persistent lifetime channel. The earlier D40 ownership failure and R06/live release
gates are not treated as resolved by this wrapper.

The final ordinary uncached interop and ACP race suites passed in 11.513s and 5.479s with installed
opt-ins unset. Whole-repository go vet and git diff --check also passed.

### Authenticated relay membership and cleanup

D42 replaces the experimental parent-group assumption with a product attachment exchange. The
child validates its supervisor's kernel PID/UID; the supervisor authenticates the session, supplies
the locally bound ACP group and verifies the child's kernel PID and actual membership. MCP cannot
start before attachment. Bound tool calls must come from that same peer. Child config version 2
rejects legacy configuration, and 65 bounded connections accommodate one lifetime channel plus
64 tool calls. Existing dependency versions and the fail-closed public run policy are unchanged.

The new independent private-protocol tests first failed to compile because attachment/binding and
cleanup-result APIs were absent. The first completed ten-case race run passed in 8.445s. Cases cover
wrong credentials, a supplied PID field, false joins, bad/missing acknowledgments, unbound setup,
duplicate connections, owner loss and a deliberately lingering child. Later regressions also check
the supervisor identity, legacy config and control calls from an unregistered process.

Actual MCP child tests initially failed both idle-input and pending-tool closure: the child remained
blocked after its control connection closed. Nonblocking owned stdio duplicates corrected this;
the suite then passed in 5.535s. A full unread output-pipe case was added and passed in the final
whole-repository run. The fake ACP matrix now exercises normal, forced and pending-tool shutdown
with both the experimental wrapper and the product's own attachment movement.

A stopped-relay regression passed in 6.836s: eight idle-close callers observe the same cleanup
failure, the ACP group is retired, final Close retains the error and the driver cannot be reused.
Manager eviction and TTL pruning initially lost that error (two failed cases, package 10.944s).
After retaining retired failures, blocking admission/discovery and joining pruning during shutdown,
the affected session, launcher and gateway race suites passed in 16.439s, 13.151s and 3.064s.

An initial default-parallel whole-repository race run failed two fake ACP initialization deadlines:
TestInitializeAndVersionRejection and the ready inventory case. All other packages passed. The
same deadlines were retained for the complete uncached `go test -race -count=1 -p 1 ./...`, which
passed: ACP 4.732s, interop 20.177s, relay 9.629s, MCP 6.149s and session 11.857s, with every other
package passing or containing no tests. The later manager change has the focused results above.
These results do not establish unrestricted concurrent-load stability; no timeout was widened.

The first installed product observation stopped at the existing account-check deadline (5.114s,
package 7.358s), before ACP/relay setup. One identical bounded retry passed in 11.236s (test 9.87s).
The plain wrapper recorded one relay initially leading a group different from ACP. The product
attachment then verified that exact PID in the ACP group. Kiro 2.21.1/v2 listed one fresh bare alias,
no native tool, and two matching MCP initialization notifications. The 254-byte inventory succeeded;
the relay PID, ACP group, pending work and private config were gone after normal shutdown.

This supersedes D40's unresolved production membership assumption for this exercised normal-close
path. Forced loss and suspended-tool shutdown have independent fake-process evidence; live prompts,
native tool effects, inherited MCP/hooks and the rest of R06 are still unverified. No Kiro model
prompt, actual client tool, credential copying, login/logout or external-provider fallback occurred.

Final whole-repository go vet, formatting and whitespace checks passed. Linux amd64 also compiled
with CGO_ENABLED=0 and GOPROXY=off; this is cross-build evidence only, with runtime validation on
the macOS host described above.

### MCP source startup controls

D43 adds independently authored multi-server and delayed-notification protocol fixtures before the
installed scope observation. The initial observer API test failed to compile on missing fields; the
implemented multiple/missing/late-source and prerequisite/privacy cases passed their focused race
suite in 4.741s. No production code changed.

The first installed attempt stopped on the unchanged five-second identity deadline (5.109s command,
10.548s package), before ACP or relay startup. The following attempt's terminal output was not
recoverable across restart and is not counted as evidence. Before another run, its tool session was
absent and a name/PID-only process check found no remaining Go inventory test or owned relay. No
unrelated Kiro process was signaled. Subsequent normalized stdout and exit status were saved in an
owner-only ignored local observation directory.

The retained `TestKiroPinnedMCPFileScopeObservation` run passed in 29.114s (27.55s test), with exit 0:

| Case | Actual observation | Limit of conclusion |
| --- | --- | --- |
| Explicit registration | Five independently observed relays attached; five fresh aliases listed; two readiness notifications per server; no native tool listed; response 1,000 bytes | Commands, MCP enumeration and normal cleanup work for these owned servers |
| Default standalone files | The primary relay attached and listed; all four standalone-file markers had no process record, attachment, readiness or listed alias; response 254 bytes | No inherited source was activated by these inputs during the bounded observation |

Every observed relay PID and the owned ACP group were gone after Close, pending/queued/sealed work
was zero, and the primary private configuration was removed. The explicit/default subtests took
14.60s/12.94s. Both identity preflights passed in this run. The post-query observation window was one
second; it is not evidence about later reloads. The flag-false and candidate comparisons were not run
because their inherited-source positive control was absent. This passing observation is not a passing
execution-restriction gate. No model prompt or client tool effect was requested.

Final uncached `go test -race -count=1 -p 1 ./internal/acp ./internal/interop` passed in 5.811s and
18.239s with installed-CLI opt-ins unset. It covers the changed independent protocol fixtures and
relay observers; the live results above remain separate evidence. Whole-repository `go vet ./...`,
format checks for the changed Go files and `git diff --check` passed.

### Launch and session directory selection

The independent other-cwd fixture verifies the exact session/new path and a different working
directory inode, before any installed observation. The protocol race suite passed in 3.954s; the
subsequent protocol/privacy suite passed in 3.611s after separating the installed inventory variants.

`TestKiroPinnedAgentDirectorySelection` passed on installed Kiro 2.21.1/v2 in 31.129s (29.87s test),
with exit 0 retained alongside normalized diagnostics in the ignored private observation directory:

| Launch agent tools | Session workspace agent | Listed tools | Test time |
| --- | --- | --- | --- |
| fs_read | Same directory and agent | read | 8.30s |
| fs_read | Separate directory, no agent | read | 7.45s |
| fs_read | Separate directory, same name, empty tools | read | 7.35s |
| Empty | Separate directory, same name, fs_read | Empty | 6.76s |

All version/account preflights, session creations and read-only queries succeeded. Nonempty results
were 814 bytes; the empty result was 98 bytes. The separate-directory cases preserved one or two
source agent files byte-for-byte. Every ACP group was gone after bounded Close. No model prompt,
MCP server or client tool effect was requested. D44 records the narrower selection conclusion;
the public run policy gate remains closed pending the rest of R06.

Final uncached `go test -race -count=1 -p 1 ./internal/acp ./internal/interop` passed in 5.396s and
18.666s with installed-CLI opt-ins unset. Whole-repository `go vet ./...`, changed-file format checks
and `git diff --check` passed. No production source or dependency changed at this checkpoint.

### One-turn client-denial experiment preparation

D45 and LIVE_KIRO_TEST_PLAN.md defined the proposed credit-consuming experiment. The actual Kiro
variant was unrun at this checkpoint and is skipped without its separate credit opt-in. D51 below
records the later explicit approval and successful live result. The code contains no
production run override. Its single ACP turn may include multiple internal Kiro model calls; no
fixed credit maximum is claimed.

The request-budget/denial/output guards first passed their independent race tests in 2.182s. An
additional regression then showed that client error-result text containing the synthetic canary
could reach the driver (1.098s failing run). The guard now checks that text before continuation and
shares its bounded fragment check with model text and tool arguments.

The initial actual-Claude/local-fake control passed in 6.252s (4.27s test) using the public session
MCP declaration. Moving the control onto the prepared-process path first failed because the fake
did not support the new launch mode (2.948s package); the client exited 1 before any tool exposure.
The independent chat-tools-client-launch mode now accepts its own relay launch manifest and
requires the hook-denial result before the original prompt can finish.

The resulting public-client/fake-ACP control and guard tests passed together in 5.010s (3.59s client
test). There were exactly two accepted backend requests, one exposed Read, one matching refusal and
one final completion. The hook marker was present, the canary was unchanged and absent from inspected
output, the relay PID and observed group were gone, the client exited 0 with 1,765 bounded output
bytes, and prepared launch/relay artifacts and HTTP/pool owners were released. Source settings stayed
unchanged. No Kiro, external model or actual client file read was involved in these controls.

The canary fix then passed the uncached ACP/interop/session race suites in 5.350s, 18.587s and
15.792s. A subsequent actual-client control and all guard cases passed in 4.360s (2.99s client test).
Client version checking was then explicitly limited to five seconds; its local control passed in
4.584s (2.71s test). These runs still did not establish that the test-only declaration options had
survived profile environment filtering.

Review found that the platform allowlist dropped those two options. A new absence assertion failed
in 2.543s before any backend start, reproducing the test-setup error. The harness now appends them to
the copied child command, supplies an explicitly empty strict-MCP configuration and retains that
request assertion. The final public-client/fake-ACP control plus guard suite passed in 4.942s (2.85s
client test), with the same exact call/refusal/completion counts, intact canary, successful cleanup
and client exit 0 (1,761 output bytes). The production environment policy remains unchanged.

Final whole-repository vet, changed-file formatting and diff checks passed. Invoking only the live
test with its credit flag explicitly disabled skipped immediately (1.250s package); this verifies
the opt-in boundary, not a successful live turn. The experiment and its later result are reviewable in
LIVE_KIRO_TEST_PLAN.md, which is now included in README's mandatory reading order.

### CLI and ACP model identity agreement

D46 adds a comparison against the actual session/new model result, using the existing production
decoder and alias mapping. New cases first failed on absent comparison APIs. Ten independent data
cases and three new fake ACP modes then passed with the existing inventory/privacy suite in 3.993s
under the race detector. They cover exact IDs, normalized-name collisions, reordered public select
options, selected-but-unlisted current models and malformed/differing catalogs. No synthetic fixture
supports a model selection or prompt, and disagreement must stop before the read-only tools query.

The first TestKiroPinnedCatalogAgreement attempt failed account preflight at 5.108s, with exit -1
and no stdout (5.412s package). ACP had not started. One identical bounded retry passed in 16.977s
(15.71s test): the account check took 1.424s and the finite CLI catalog command 9.257s. Both validated
catalogs contain 19 entries; all 19 backend IDs and their reversible client aliases agree, auto is
present in each, and the current selections match. The actual session uses the legacy model shape.
The empty-agent tools query succeeded with 98 response bytes and no listed tool. All finite-command
groups and the ACP group were gone. Normalized stdout and terminal exit status for both attempts
are retained in separate owner-only ignored observation directories.

This closes the observed CLI-to-session catalog identity check for that version/session, not live
model changes, generation semantics, the interactive model selector or R06. No model prompt, client
tool effect, credential copying or login/logout was involved. The D45 credit test was still unrun
at this catalog checkpoint; D51 records its later approved result. No production adapter,
dependency or permission changed in the catalog observation.

Final uncached `go test -race -count=1 -p 1 ./internal/acp ./internal/interop` passed in 5.650s and
18.594s with installed-CLI opt-ins empty and the credit flag explicitly disabled. Whole-repository
`go vet ./...`, changed-file formatting and `git diff --check` passed. These fixture results remain
separate from the successful installed catalog observation and its earlier failed preflight.

### Temporary client status display

D47 adds the internal `statusline --config` command and connects the launcher's generated UI token
to an owner-only configuration file. Its temporary host overlay requests five-second refreshes.
The child helper sees a cleared environment, reads no client stdin/transcript, and makes one bounded
GET to the exact cached status route. A last completed foreground turn remains visible when account
usage is unavailable. Optional model multipliers, credits and local token estimates stay distinct;
the display does not derive billing data from estimates.

The new formatter/helper/profile/dispatch tests initially failed on absent APIs. A late-invocation
regression then reproduced recreation of the removed runtime; privatefs.Open fixes that reader-only
path. A supplied-multiplier assertion also failed before the display was completed. Independent
tests cover URL/credential authority, redirects, ignored proxy variables, hostile shell-path bytes,
private file modes/link rejection, response caps, cancellation and source-settings preservation.
An actual helper with an unread stdin and a saturated stdout exits on its own two-second deadline.
The composed launcher test completes one fake ACP turn, reads its actual queued metric over HTTP,
and then verifies revoked configuration and absence of the removed runtime after cancellation.

The focused status/launcher/command race checks passed before installed UI observation; the final
runtime/status selection passed in 7.336s and the multiplier correction in 2.019s. No dependency was
added.

`TestClaudeStatuslineRefreshWithoutModelTurn` passed on installed Claude Code 2.1.263 in 7.16s
(9.044s package, including the setup-input regression). It rendered the synthetic last model and
made two authenticated status requests separated by 4,971ms. HTTP message requests and backend
starts were each zero; the synthetic catalog was queried once. The owned client PID/group were
absent after shutdown, both process owners had zero active children and source settings were
unchanged. Normalized evidence and exit 0 are retained in the ignored owner-only
`.cache/interop-observations/statusline.YAkSfo` directory; raw terminal text was not saved.

Earlier attempts failed before status rendering: an interactive invocation mistakenly used the
print-only no-session-persistence option, a terminal-size probe used the wrong macOS stty path, and
fresh profiles waited at theme, key, introductory or repeated trust dialogs. The corrected test uses
the documented CLAUDE_CODE_SKIP_PROMPT_HISTORY setting and a pre-seeded trust record for only its
fresh empty project. Recognized theme/key/note inputs remain bounded; directory dialogs receive no
input in the final observer. No real user profile or credential is copied. The final result verifies
idle status refresh on this prepared profile, not complete onboarding or trust-dialog behavior.
Those earlier failures are not reclassified as passing UI tests. No Kiro process, model credit,
login/logout command or client tool effect was used, and the production policy gate is unchanged.

The uncached whole-repository `go test -race -p 1 -count=1 -timeout=3m ./...` run used umask 077,
empty installed-CLI opt-ins and credit opt-in 0. All packages except privatefs and launcher passed;
their failures came from three old negative-fixture sources relying on creation mode 0644 without
accounting for umask. The created files were actually private, so accepting them was correct. The
fixtures now explicitly chmod those synthetic records; production checks were not relaxed. The
focused reproductions passed in 1.683s/3.114s, and both complete affected race suites then passed
under the same umask: privatefs 1.389s, launcher 16.025s. The first run's other results include ACP
5.023s, gateway 3.247s, interop 19.177s, session 16.622s, status 1.343s and statusline 2.589s.
Whole-repository vet, changed-file formatting and diff whitespace checks passed. This is a broad
run followed by affected-package revalidation, not a claim that the first run was entirely green.

### Interactive startup hook ordering

A new public-only Claude consultation completed with CLI exit 0 and a parsed successful one-turn
result. The complete 3,365-byte answer file and its result metadata were read; a local review is
saved alongside it at .cache/claude-consult/work/review-readiness.md. The prior restricted environment
returned an inner login error and supplied no advice despite its wrapper exit 0. The successful
direct invocation changed no credentials or account state through login/logout. Neither result
identifies the reason for the environment difference. The separately blocked internal MCP prompt
remains unsent; this was a distinct, approved public lifecycle question.

The new independent hook-process test first failed because the executable fixture was absent.
That run also encountered Go's local telemetry directory being written during temporary-directory
cleanup. The fixture builder now uses go telemetry off only in its disposable HOME; an attempted
GOTELEMETRY environment assignment was verified ineffective because Go exposes it as read-only.
After the helper was authored, the protocol and existing setup-input race checks passed in 3.481s.

TestClaudeStartupHookOrderingWithoutModelTurn passed against unmodified Claude Code 2.1.263 in
8.37s (9.676s package). Its two independent SessionStart hooks send only fixed loopback observations
and synthetic systemMessage notices. One callback is held until the other helper's PID is gone,
then for three more seconds. The client has an empty prepared project, no tools and strict empty
MCP settings; the normal D47 test-only trust/onboarding prerequisites apply.

| Observation relative to first hook callback | Time |
| --- | ---: |
| Held / fast hook entered | 0 / 1ms |
| Fast hook reported its first response returned / PID gone | 2 / 3ms |
| First status request / visible status | 514 / 520ms |
| Held callback released / helper reported its return | 3,005 / 3,006ms |
| Fast and held notices first visible | Both 3,023ms |

Status was visible while the held hook was still running. The two status polls were 4,622ms apart;
there was one synthetic catalog request, zero HTTP message requests and zero backend starts. Client
PID/group and both hook PIDs were gone, source settings were unchanged, and both process owners had
zero active children. The private normalized log and exit 0 are retained under
.cache/interop-observations/startup-order.gHCAPN. Raw terminal output remains in bounded memory only.

This is positive ordering and notice-display evidence, not a prompt-input usability test, latency
benchmark or full readiness guarantee. D48 retains the initialization gate; D49 subsequently adds
the product capability-notice payload/route/hook. No Kiro process, model credit, actual client tool,
authentication mutation, dependency or production-policy change was involved.

The complete uncached interop race suite passed in 20.011s with both installed-CLI opt-ins empty
and Kiro credit opt-in 0. The final invalid-callback regression also passed in 1.962s after seeding
the entered-but-unreleased state explicitly. The existing installed-client status test passed after
the shared observer refactor in 7.26s (8.566s package): two polls 5,008ms apart, visible status,
zero message requests/backend starts and joined cleanup. Its normalized log and exit 0 are under
.cache/interop-observations/startup-status.RBIDSF. Whole-repository vet, changed-file formatting and
diff checks passed. No unrelated production acceptance suite or live Kiro gate is claimed by these
test-only changes.

### Startup model notice and optional hooks

D49 adds the exact UI-authenticated model-capabilities POST, strict version-1 notice formatter and
internal model-notice command. The launcher installs a synchronous startup-only SessionStart hook
while preserving the client-controlled source hooks and permissions. The helper reuses the existing
private UI configuration and bounded HTTP reader; its output is only systemMessage JSON. It shows
the launch model, client tool ownership, unverified media/effort, currently unavailable native web
search and unreported provider usage. It performs no discovery, usage refresh or model work and
does not consume queued metrics. This does not verify Kiro policy or complete initialization.

The new contract/route/helper/dispatch tests first failed on absent APIs. Focused race tests then
passed for status (1.801s), gateway (1.549s), startupnotice (1.535s), existing statusline (2.357s) and
command dispatch (1.558s). The launcher/helper checks passed in 9.819s: actual compiled helpers retain
literal hostile shell paths, cleared environments, private credentials and a two-second exit even
with unread stdin and blocked stdout. Runtime composition reads the prepared launch alias without
another model start, then removes the credential at shutdown. Later failure/cancellation and
conflicting-startup-model checks passed in startupnotice 2.522s and launcher 3.813s. Redirects,
unauthorized/oversized/stalled responses, mismatched models and injected context fields cannot broaden
the display; caller cancellation keeps its cause. A preliminary command with malformed -p1 syntax
ran no tests; the reported runs use Go's -p 1 syntax.

Four installed-Claude 2.1.263 controls passed in 19.801s under the race detector. The interactive
status and held-peer-hook cases each made one startup-notice request and visibly displayed the
product message, with zero HTTP model requests/backend starts. Their status refresh intervals were
5,101ms and 4,862ms. Client PID/group and held/fast helper PIDs were gone after cleanup; terminal
exit 143 was the harness's intentional shutdown, and the tests exited 0. The existing ordering
distinction remained: visible status preceded release of the held hook. In that run status appeared
before the first hook callback, so its negative relative timestamp is valid rather than missing data.
The normalized log and exit 0 are retained under .cache/interop-observations/model-notice.LUl2oZ.

The final two synthetic-conversation controls also checked every model request body for the notice
and passed in 5.255s (individual tests 2.28s/1.44s). Each completed two local requests and a Read
denial with client exit 0 and zero UI-backend discovery/starts. With hooks enabled, the product notice
ran once alongside both user/project hooks; with disableAllHooks enabled, all three were absent and
the conversation still completed. The notice was absent from both model bodies in each control.
Source settings stayed unchanged. The owner-only normalized log and exit 0 are under
.cache/interop-observations/model-notice-context.6hsxGH. No Kiro process, model credit, external
inference or actual client file read was used. No dependency or production-policy change was made.

The final uncached `go test -race -p 1 -count=1 -timeout=3m ./...` passed across all packages under
umask 077, with installed-CLI opt-ins empty and Kiro credit opt-in 0. Results include ACP 4.921s,
gateway 3.024s, interop 19.777s, launcher 18.212s, session 16.186s, startupnotice 2.027s and existing
statusline 2.148s. The complete normalized log and exit 0 are retained under
.cache/interop-observations/model-notice-regression.xSyVut. Whole-repository vet, changed-file
formatting and diff checks also passed. This validates the local notice implementation and affected
regressions; the full product and live/release gates remain unfinished.

### Foreground completion hooks and delivery bookkeeping

D50 adds the synchronous Stop helper using the existing private UI credential and exact metrics
route. It emits only bounded systemMessage JSON, displays all retained foreground completions,
and emits no notice for empty/unavailable/malformed data. HTTP terminal delivery now registers
bookkeeping before final output; metrics draining waits at most 200ms for its initial snapshot.
Failures preserve queued records, and status/model notices stay independent. There is no
acknowledgement or guarantee of exactly-once visible display after a drain.

The new formatter, helper and command tests first failed on absent APIs. Independent held-finalizer
tests then reproduced premature JSON/SSE draining and failure-path record consumption before the
gateway fix. Focused delivery tests passed in 2.088s, including overlapping finalizations, canceled
waits, invalid auth/body, status availability and failed terminal output. The direct compiled helper
tests exercise 32 records above the old 1 KiB limit, literal hostile paths, cleared environments and
the two-second main deadline with unread stdin/blocked stdout.

Initial installed-Claude completion probes failed: two title/foreground request pairs were counted
as four main requests, and the first Stop drain included a false title completion. A synthetic
count_tokens endpoint was never called and did not change this outcome; no production counting
adapter was added. Bounded field/shape observations identified title-only JSON Schema and system
purpose signals, with thinking omitted by the documented test option. The independent classifier
test reproduced omission being classified as main before the fix. Classification now accepts omitted
thinking only with every other title condition; null/adaptive declarations, loose title wording,
ordinary structured output and tools stay main. The manager's fake-process metrics suite includes
an omitted-thinking title case. No client prompt/template was copied as a fixture.

After this correction, actual Claude 2.1.263 sent one title and two foreground requests, made two
Stop metric requests, and displayed both completion notices with no notice text in the later model
input. The remaining status assertion still failed because stripping terminal escapes misses words
assembled through cursor edits. A bounded 160x40 text-cell observer now reconstructs those updates;
independent cursor-edit, erasure, control-string and wide-character cases passed in 1.711s. This is
limited text observation, not a general terminal emulator or full interactive-readiness proof.

The installed two-turn control then passed in 8.987s (7.17s test), with one startup notice, one catalog
request, three status requests and a 4,627ms refresh interval. The updated last-model status was
observed as well as both foreground notices. Client PID/group disappeared, all process owners were
idle and source settings were unchanged. Client exit 143 is the intentional harness termination;
the test exited 0. Its normalized log/exit status are retained privately under
.cache/interop-observations/turn-metrics-screen.xmzu0D. Earlier failed controls remain recorded under
turn-metrics-count.J9uqNj, turn-metrics-purpose.Rta7F0, turn-metrics-title.Ut8KGd and
turn-metrics-display.FSLL5K. No Kiro prompt or external inference was used in these controls.

The earlier local Claude consultation on generic Stop/HTTP timing completed, saved its answer and
was reviewed; D50 records what was adopted and the narrower wait policy. This consultation is
separate from product Kiro traffic. The test observer additionally uses reviewed x/text/width at
the existing version, with its generated-data scope recorded in DEPENDENCY_REVIEW.md. Production
execution-policy, actual Kiro/private metadata, R16 compatibility and release gates remain open.

The complete uncached `go test -race -p 1 -count=1 -timeout=3m ./...` passed with both installed-CLI
opt-ins empty and Kiro credit opt-in 0. Results include ACP 5.146s, gateway 3.337s, interop 21.471s,
launcher 21.410s, session 16.318s, status 1.430s and turnnotice 1.562s; all other packages passed.
The private normalized log and exit 0 are under
.cache/interop-observations/turn-metrics-regression.O5QJfs. Offline go mod tidy using the installed
Go binary only reclassifies the existing x/text requirement as direct; versions/checksums are
unchanged. An initial invocation through the older bootstrap Go with GOSUMDB=off stopped at
toolchain verification before tidy; the direct installed binary completed it without fetching.
Whole-repository go vet passed afterward.

Six installed-Claude controls then passed together in 30.157s under the race detector, with Kiro
credit opt-in 0. The two-turn display again separated one title and two foreground requests, showed
both completion notices and the updated status, and excluded notice text from later input. Enabled
user/project Stop hooks ran alongside one metrics hook; disableAllHooks suppressed them all while
the same two-request conversation and Read denial completed. Idle and held-startup controls retained
visible status, one startup notice, zero model turns and bounded cleanup. The prepared one-prompt
Read-denial harness also passed against fake ACP in 3.13s: one exposed Read, one matching denial,
one final completion, unchanged canary and no surviving relay/group. No actual Kiro prompt was sent.
The private normalized log and exit 0 are under
.cache/interop-observations/turn-metrics-client-regression.xLl3b1.

### First live Kiro client-denial round trip

After the user explicitly approved the prepared one-attempt experiment, TestKiroLiveOnePromptClientDenial
ran once with the credit opt-in enabled and the pinned installed Kiro/Claude paths. No test or
production code was changed for this run. Kiro 2.21.1/v2, its authenticated account/catalog preflight
and the exact advertised auto model were used. The test passed in 40.91s, with race-enabled package
exit 0 at 42.402s. The normalized log and exit status are retained privately under
.cache/interop-observations/live-kiro-denial.de4u7z.

There were exactly two accepted backend requests: the initial request and its matching client-error
continuation. One Read call reached Claude, its PreToolUse hook refused it, the matching denial
returned through the relay and the same ACP turn ended successfully. Final completions, exposed
calls and matched denials were each one. The client exited 0. The synthetic canary was unchanged
and absent from checked client/model/tool output. The relay PID and its observed process group were
gone, the driver became idle before shutdown, all HTTP/pool owners joined, private launch/relay
artifacts were removed and the owned client settings/profile checks passed. The 1,771-byte client
result was inspected in bounded memory and not retained in the normalized log.

This is the first real Kiro inference result; earlier fake/no-prompt evidence remains historical.
No repeat ran. Provider call count, token/credit charges and a fixed credit cost were not measured.
The independently passing Read-denial control is now live evidence for this specific continuation
path, not full native-tool restriction proof or successful client file/shell execution. D51 leaves
native filesystem/shell/task/subagent attempts, inherited configuration/reload/load, R16 request
controls, other model/media/web behavior and release work open. The production run command remains
ErrPolicyUnverified with no override.

### Request constraints and negative client recovery evidence

D33 now inventories accepted request fields and the remaining R16 gaps. Known unmapped stop,
sampling, remote MCP, container, location and service-tier declarations reject with a fixed safe 400.
Validation also runs at internal manager/driver admission before an idle binding can be evicted or a
pending tool result consumed. Empty stop/server arrays and null container/location retain their
no-declaration meaning. No client profile setting or dependency changes.

The new tests first failed because the control-validation API was absent. The focused race suites
passed for Anthropic (2.031s), gateway (2.028s) and session (3.733s), including preservation of the
only idle binding when a different identity submits an unsupported control.

The applicable full uncached race suites then passed with package parallelism two: Anthropic 8.061s,
gateway 3.095s, session 9.552s, launcher 5.189s and interop 1.269s (installed-client cases opt-in).
The final session suite also verifies that a rejected continuation leaves its pending tool result
available to the next valid request on the same ACP prompt. `go vet ./...` and `git diff --check`
passed. These runs do not use live model inference.

Actual Claude Code 2.1.263 sent one adaptive-thinking request in each synthetic case. The positive
control completed locally. Four newly authored thinking-error shapes did not recover (one request,
exit 1); the generic shape also failed with two retries configured in both environment and owned
settings. The original recovery assertions failed. TestClaudeThinkingRejectionObservation records
these failures as negative evidence and requires the positive control to succeed; its race run passed
in 4.657s (six cases, 2.71s test). This is not a successful recovery gate or a general claim about all
upstream error wording. No model, Kiro prompt, client tool or real user profile was used. Logs retain
only known field/kind names, counts and lengths. Production retry policy remains unchanged.

The current max_tokens bound is validation only, and reasoning/context-management/structured-output
constraints still have no complete mapping. Existing title-isolation tests establish separation, not
JSON Schema output enforcement. R16, live client compatibility and release readiness remain open.

### Prepared catalog and last completed model

D34 connects the existing catalog cache and last-model policy to the internal launcher runtime.
Startup selects explicit configuration, then a compatible interactive preference, then Kiro's current
catalog model, using exact aliases. Model listing uses the prepared cache instead of starting ACP.
Runtime ownership joins HTTP/client/backend work before closing catalog refresh and removing the
private client profile. D35 later connects executable/account/catalog preflight and stable identity;
effective Kiro restriction verification remains open.

A new regression reproduced loss of the last-used preference after one-launch model/effort options
were removed. Preference schema 2 now excludes those options while retaining all other identity
boundaries; catalog identity still includes them. Old preferences are invalidated, not migrated.
The new launcher tests initially failed on absent model-preparation APIs. The focused race suites
then passed for catalog (2.164s) and launcher (4.498s).

The gateway wrapper saves the actual reported model only after final foreground delivery. Title and
agent work, tool handoff alone, cancellation, early Finish, authentication fallback and noninteractive
launches do not save. Completed foreground tool results can save. Unadvertised actual models and
local write failures retain a safe Boolean diagnostic rather than guessing or failing delivered text.

The independent runtime client also checks that its local model-list response contains the startup
alias. Full runtime cases exercise that cached list, final text/tool saves, exit/cancellation during
a suspended tool call, model-owner startup failure cleanup and source-settings preservation. The
focused runtime race suite passed in 5.091s. It does not use installed Kiro, model inference or the
actual Claude model-picker UI. Stable real profile/agent/capability identity remains a preflight gate.

The complete uncached `go test -race -p 2 -count=1 ./...` run then passed: ACP 5.017s, process pool
2.827s, Anthropic 8.144s, catalog 1.394s, child process 5.862s, gateway 3.004s, launcher 5.154s,
schema worker 3.420s and session 9.512s; all other packages passed. Installed-client observations
remain explicitly opt-in. `go vet ./...` and `git diff --check` also passed. No dependency was added.

## Phase 1 acceptance mapping

| Acceptance A/G behavior | Executable evidence |
| --- | --- |
| Version negotiation and rejection | TestInitializeAndVersionRejection |
| Concurrent reversed responses and notifications | TestConcurrentOutOfOrderAndNotification |
| Invalid frames/IDs/envelopes, truncated EOF, process exit; all waiters fail | TestMalformedFramesFailEveryWaiter, TestErrorEnvelopeTypesAndCorrelationPrecedeClassification, TestDuplicateResponseRetires |
| 8 MiB boundaries, duplicate keys, UTF-8/depth, fragmented framing | TestFramingBoundaries, TestStrictJSONObjects, TestDefaultFrameCeiling, FuzzObject |
| Oversize output, blocked pipe and explicit deadlines | TestOutboundLimitAndStalledPipe, TestConfiguredDeadlineWithoutCallerDeadline, TestCancellationAndTimeoutsRetire |
| Agent requests rejected; notifications never answered; directional IDs | TestAgentRequestsNeverAuthorizeEffects, TestJSONValueSemanticsAndAgentDirections |
| Credential redaction and bounded stderr, conservative auth errors | TestStderrRedactionAndTruncation, TestFailurePrivacyAndAuth, TestAccountExpiryBoundary |
| Whole group shutdown including surviving descendants and repeated callers | TestWholeProcessGroupShutdown (cooperative, stubborn, leader-exits; 8 concurrent Close calls) |
| Queue/pending bounds and clean child environment | TestPendingCountAndEventByteBudgets, TestQueueOverflowAndNoEnvironmentInheritance |

The independent child is `internal/acp/testdata/fake/main.go`; its provenance manifest records public
source documents. These are fake-process acceptance results, not live Kiro conformance or release
readiness. Load repetition, broader fuzzing and live gates remain in Phase 7.

## Phase 2 acceptance mapping

| Acceptance B/C/G behavior | Executable evidence |
| --- | --- |
| Exact model/UI route scopes, bearer/API key handling, duplicate/conflicting credentials | TestRouteAuthentication, TestDuplicateCredentialHeadersAndStartFailure |
| Loopback policy and request shape/body limit | TestLoopbackBindPolicy, TestHTTPValidationBeforeBackend, TestRequestValidationAndOrderedContent |
| Equivalent buffered/SSE text and zero provider usage | TestTextResponseAndExactSSE, TestHTTPThroughIndependentACPProcess |
| Early/late authentication fallback and safe ordinary errors | TestEarlyAndLateAuthenticationFallback, TestBackendFailureTimingAndPrivacy |
| Disconnect before first event/during stream, request/write/output limits | TestActiveRequestLimitAndDisconnectBeforeFirstEvent, TestDisconnectCancelsStreamingTurn, TestResponseWriteFailureCancels, TestEncodedOutputBudgetAndTerminalStreamError |
| Distinct first/total deadlines through a subprocess | TestFirstAndTotalDeadlineDiagnostics, TestHTTPThroughIndependentACPProcess |
| Prompt order and response barrier, wrong-session/canceled state disposal | TestOrderedPromptProjection, TestPublicTextPathAndOrderedCompletion, TestUnsafeStateIsDiscarded, TestCancellationAndSingleSessionAdmission |

These pass against independent fake processes. Model discovery/selection, actual session reuse,
relay/media/native web, subprocess environment/profile integration, and the launcher remain later
phase requirements. There is no executable live Kiro entry point yet. The initial text driver creates
fresh state for subsequent full-history requests in its original Phase 2 version. Phase 5 now adds
the reconciliation and sharing evidence below.

## Phase 3 acceptance mapping

| Acceptance D and cache/security behavior | Executable evidence |
| --- | --- |
| Unique, deterministic model IDs; strict reverse lookup; include omitted current model | TestStableCatalogMappingAndValidation, TestDerivedAliasCollisionIsRejected |
| Display-only multiplier and validated legacy/public model selectors | TestCreditMultiplierIsOnlyDisplayMetadata, TestSessionCatalogWireShapes |
| Selection before effort before prompt; inconsistent/unavailable selection rejects | TestModelSelectionThenOptionalEffortThenPrompt, TestUnknownModelAndInconsistentSelectionAreRejected |
| Initial configuration applies only through first completed turn | TestConfiguredInitialModelWinsOnlyFirstCompletedTurn |
| Auto/unsupported/unavailable/unknown effort behavior and rejected-probe ledger | TestEffortAvailabilityAndAutomaticModel, TestRejectedProbeIsNotRepeatedAndTransportRemainsFatal, TestAutoAndRepeatedRejectedEffortInProcessPath |
| Model-switch effective-state reset; optional rejection versus broken transport | TestEffortSuccessAndSwitchOrdering, TestBrokenTransportDuringEffortRemainsFatal |
| Independent setup/turn time and immediate status reads | TestSetupAndOwnedTurnHaveIndependentDeadlines, TestEffortStatusDoesNotWaitForPrivateCommand |
| Fresh/stale catalog identities, single-flight refresh, failure/backoff, last model scope | TestCatalogCacheCoalescesAndUsesCompatibleDiskState, TestStaleCatalogReturnsImmediatelyAndFailedRefreshPreservesData, TestFailedCatalogRefreshRetainsLastGoodDataAndBacksOff, TestLastInteractiveModelIsSeparateAndStrictlyValidated |
| Atomic owner-only files, bounded reads and link/special-file rejection | TestOwnerOnlyAtomicFiles, TestSymlinkPermissionsAndSpecialFilesAreRejected |

The last-model/cache policy modules pass independently; final launcher integration remains Phase 6.
Private effort wire shapes are explicit specification interpretations pending real Kiro validation
(decision D09). The unmodified client model-selector UI and all live release behavior remain unverified.

## Phase 4 implementation evidence

Schema-worker, registry, broker, socket, MCP child, mixed HTTP block and session-continuation tests
were written before their respective implementation and initially failed on absent APIs/behavior.
An additional regression reproduced a completed call's timer incorrectly canceling a newer batch;
the timer now checks its still-pending ownership before retiring a relay. Correlated auth expiry wins
over a racing relay disconnect, and notifications are drained again at the prompt completion barrier.

- Passed `go test -race -count=1 ./internal/toolregistry ./internal/schemacheck`: deterministic
  collision extension and registry ownership; real worker Draft 2020-12/numeric/retrieval/deadline cases.
- Passed `go test -race -count=1 ./internal/relay/...`: sealed batch atomicity, cross-owner/repeated/
  partial/late results, byte/admission limits, timeout/cancel, private file modes and cleanup; real
  MCP child lifecycle, suspended call plus ping, exact whitelist and cancellation.
- Passed `go test -race -count=1 ./internal/gateway ./internal/anthropic`: mixed text/tool event order,
  buffered/SSE content, exact large-number arguments, no partial/undeclared tool block and explicit
  rejection of unsupported tool-choice restrictions, plus the existing auth/deadline suites.
- Passed `go test -race -count=1 ./internal/session` in 6.188s: actual gateway → independent fake ACP
  → MCP child → private socket → matching client result, in both response modes; only one ACP prompt
  spans the round trip. Includes preserved tool error/output, ignored duplicate diagnostic tool
  updates, HTTP completion versus cancellation, no-consumer tool expiry, model/result replay refusal
  and graceful auth expiry while no HTTP response is open.
- Schema runtime dependencies are jsonschema v6.0.3 (Apache-2.0) and x/text v0.41.0 (BSD-3-Clause).
  `go mod tidy` and the compiled application/test graph are recorded in DEPENDENCY_REVIEW.md.
- Passed full uncached `go test -race -count=1 ./...`: ACP 6.689s, gateway 3.251s, broker 3.007s,
  MCP child 5.690s, schema worker 6.010s and session 8.122s; all other packages passed. After adding
  the original-HTTP-deadline regression, the session race suite passed again in 6.434s.
- Passed `go vet ./...` and `git diff --check`. Control-frame fuzzing passed 265,734 executions
  in 6.442s (`-fuzztime=5s -parallel=2`), with no failing input.

Black-box Kiro 2.21.1 configuration evidence remains limited: an independently authored temporary
profile with empty tools, empty MCP servers/resources, no hooks and `includeMcpJson: false` returned
exit 0 from `agent validate`. The default agent creation behavior advertised all tools (`*`) and
included inherited MCP configuration. The create command opened an editor; the owned editor/process
was terminated, and later probes suppressed editor invocation and used bounded process groups.
The later D27 negative control shows this exit status cannot prove syntax acceptance either. No
model request or client tool effect
has been run. Real Kiro restricted-profile/negotiation proof (R06), actual Claude model UI (R14), and
credit-consuming live opt-in gates remain open.

## Phase 5 implementation evidence

History, multi-session fake-process, manager and persistence tests were written before implementation
and first failed on missing behavior/APIs. A later regression reproduced capacity rejection despite
an available wholly idle process; admission now retires that idle group and retries once. Shared
deadline timers also reproduced an incorrect generic HTTP failure; diagnostics check the original
absolute deadline as well as context cancellation.

| Acceptance F/G behavior | Executable evidence |
| --- | --- |
| Main turns 1-3 reuse and only new deltas; safe truncation; duplicate/divergent recreation | TestThreeTurnsAndProvenTruncationSendOnlyNewContent, TestThreeTurnReuseTruncationAndDuplicateRecreation |
| Key order/cache hints, exact large numbers, changed system/assistant anchors | TestCanonicalizationPreservesArgumentNumbersAndSystemContext, TestDuplicateDivergenceAndUnprovenAssistantOnlyOverlap |
| Combined title classifier and concurrent title/main sessions | TestTitleRequiresAllSignals, TestManagerIsolatesTitleAndMainAndPreservesThreeTurns |
| Independent keys share compatible processes; active key/capacity restrictions | TestIndependentClientIDsShareCompatibleProcessAndActiveKeysStayBounded |
| Temporary creation owner, established routing, concurrent RPC/event ordering | TestEarlyCreationRoutingAndConcurrentResponseBarriers |
| Ambiguous creation and shared crash fail all attached waiters | TestAmbiguousCreationAndCrashInvalidateAllAttachedSessions |
| Compatibility, pool limits, idle TTL and repeated shutdown | TestCapacityCompatibilityAndIdleExpiry, TestIdleCapacityCanBeRecycledWithoutEvictingActiveOwners |
| Load replay suppressed and foreign session requests rejected | TestLoadReplayAndWrongSessionRequestsAreRejected |
| Strict persistent extension, null response, failed/mismatched/unsupported load and fresh fallback | TestPersistentLoadDiscardReplayAndInvalidateBeforePrompt |
| Private fixed-slot records, stable keyed digests, exclusive ownership, no raw conversation data | TestMetadataOnlyRecordsStableKeyAndHeldOwnership, TestFixedSlotsBoundStorageAndCollisionCannotCrossOwnership |
| Durable invalidation, replaced inode/link/mode refusal, abrupt owner exit | TestExclusiveLeaseAndDurableInvalidation, TestLeaseRefusesLinksAndUnsafeFileModes, TestAbruptOwnerExitReleasesLockAndKeepsOnlyIdleData |
| No title or unstable-identity persistence | TestTitleAndFallbackIdentityNeverPersist |

Passed uncached `go test -race -count=1 ./...`: ACP 6.758s, pool 4.429s, gateway 2.110s, history 2.604s,
schema worker 3.747s, session 7.780s and store 2.556s; all packages passed. The opt-in installed-client
test is skipped in the ordinary suite. Subsequent idle-admission, abrupt-exit and title-persistence
regressions passed in targeted race suites. `go vet ./...` passed before these final regressions;
final checkpoint checks are recorded with the implementation report.

The new fake `fake/pool.go` imports only standard packages. Its prompt guard exits if an idle JSON
record still exists when dispatch arrives, proving the actual invalidation-before-prompt ordering.
It captures only synthetic test input. Live Kiro loading, actual-client title/tool history shapes,
longer load/fuzz and clean-host release gates remain unverified. No new external dependency was added.

## Remaining work

### Local client runtime coordination

The new lifecycle tests first failed on missing RunClient APIs. D32 joins the local HTTP server,
isolated profile, attached client, backend, schema owner and optional usage cache. Model/UI tokens are
generated within this stage. Its timing fields distinguish actual process launch from still-unverified
client initialization. Every startup failure path also closes the transferred owners.

The initial focused race suite passed in 6.285s. It composes an independent HTTP client with the actual
gateway/session/schema/relay implementation and fake ACP: normal text, synthetic tool completion,
client exit after tool handoff, and repeated cancellation while waiting for tools all clean up. It
also covers canceled startup, malformed settings, failed exec, conflicting authority and invalid
limits. A suspended usage fetch joins, and an arbitrary synthetic cleanup error is replaced with a
fixed class while other cleanup continues. Source settings and caller descriptors remain unchanged.

At this checkpoint this was an internal runtime stage. D34/D35 subsequently connected catalog,
last-model and public CLI startup; D47/D49/D50 add status credentials/display, startup notices and
turn-completion hooks. Kiro restriction proof, client assets, complete interactive readiness and
verified capabilities remain unfinished.
The independent runtime fixture contains no upstream capture and performs no
actual client tool effect, installed-client invocation or Kiro model request. No dependency was added.

The final complete launcher race suite passed in 7.913s, including preservation of the profile until
backend shutdown completes and parent cancellation winning a concurrent child exit. `go vet ./...`
and `git diff --check` passed. The preceding D31 checkpoint's full all-package race suite remains the
latest broad run; this subsequent change is confined to the launcher and its independent fixtures.

### Prepared process and session lifetimes

The new pool/session tests first failed on absent APIs. D31 adds capacity admission before launch
preparation, one lifetime session per prepared process, and cleanup of partial or retired policy
artifacts after ACP/router shutdown. An additional failing regression reproduced repeated idle
release returning before cleanup; all callers now join the same result. The first cleanup failure
survives removal of its group, is returned by pool shutdown and stops further admission.

`go test -race -p 2 -count=1 ./internal/acppool ./internal/session` passed in 2.845s and 9.570s.
Independent fixtures cover separate policy/relay paths, unchanged public session cwd, exact aliases,
compatible turn reuse, tool_choice none replacing a prior policy, unaffected sibling ownership, and
the same ACP prompt across successful HTTP tool handoff. Both final delivery and cancellation join
the prepared configuration and relay cleanup. Partial preparation/start failure, pool cancellation,
cleanup failure and eight concurrent release/close callers are covered. `go vet ./...` passed.

Prepared session persistence explicitly rejects until profile/relay restoration is independently
verified. The generic shared-pool/session-load tests remain applicable. No Kiro execution permission,
live preparer or unverified-start bypass is added; D27's candidate and R06 remain unverified. The
synthetic launch manifests are independent controls, not Kiro configuration observations. No new
dependency, installed-client invocation or model request was needed for this change.

The complete uncached `go test -race -p 2 -count=1 ./...` passed after the final cancellation check:
ACP 5.030s, pool 3.011s, Anthropic 8.224s, childproc 5.989s, gateway 2.977s, launcher 1.604s and
session 9.064s; all remaining packages passed. Installed-client/Kiro tests remain opt-in and were
skipped. `git diff --check` passed.

### Restricted-agent candidate preparation

The new candidate-profile test first failed on the missing builder, then passed for empty/two-tool
registries. It checks literal relay-only aliases, no wildcard/native authority, the sole owned MCP
command, empty resources/hooks, no inherited MCP flag, private file modes and deterministic policy
identity. D27 keeps the result explicitly unverified; it is not wired into live model startup.
The initial filesystem fixture did not provide an owner-only runtime root; the test now explicitly
creates one with MkdirTemp instead of relaxing the production mode check. The launcher race suite,
including directory-link refusal and conflicting-file preservation, passed in 2.444s. `go vet ./...`
and `git diff --check` passed for this checkpoint.

The installed `kiro-cli acp --help` lists --agent as applying to the first session, --model/--effort,
and an explicit --agent-engine selector with v2 as the current default. The first-session wording
requires a separate effective-profile check before enabling shared or loaded live sessions. No
trust-all-tools flag was used. Candidate validation, process/profile lifecycle and R06 remain open.

An opt-in validation probe initially failed its assumption that an invalid tool-list type produces
a nonzero exit. Repeating the control at the exact same file path confirmed that both the candidate
and a tools value of 42 return exit 0. `TestKiroAgentValidationExitStatus` records this observation
(6.078s suite), with execution verification remaining false. Its declared MCP executable is the
fixed system `/usr/bin/false`; no Kiro prompt is submitted. This is not a passing restriction gate,
nor proof that Kiro effectively accepts malformed tools: only the exit-status test is inconclusive.
Earlier descriptions of successful syntax validation must be read as a zero command exit only.

### Phase 6 Kiro identity preflight and unauthenticated startup

Initial probes without the Kiro installation directory in PATH timed out. The installed public
`kiro-cli-chat` helper was then located and independently reported 2.21.1, matching `kiro-cli`.
With that directory in the explicit PATH, `whoami --format json` completed with exit 0. The complete
stdout was not JSON: a compact first object contained accountType/email/region/startUrl string fields,
followed by a short non-JSON postamble. The helper returned the same format. No field values or
postamble prose were logged. The earlier whole-stdout-object assumption was therefore incorrect.

New preflight tests first failed on missing APIs. D26's version-specific parser now bounds and checks
the leading identity object, rejects ambiguous JSON continuations and stores only a keyed account
scope. Independent tests verify version-check ordering, environment, stable/different account scopes,
malformed/duplicate/oversize output, command timeout and caller cancellation. The final launcher race
suite passed in 2.149s. The installed read-only `TestKiroPinnedLoginPreflight` passed in 4.388s
(2.50s test), with a verified CLI identity and a private scope digest. This supersedes the earlier
unknown login result; it does not claim that the next model request's credentials cannot expire.

The empty-HOME Kiro probe's syntax validator first timed out, then exited unsuccessfully after PATH
was corrected. The same validation command with the existing HOME exited 0. ACP was still
launched with the empty temporary HOME and no copied account files; it returned a recognized
authentication failure before initialization completed. `TestKiroIsolatedACPHandshake` passed as that
bounded observation in 6.766s, with no session prompt, model credits or client tool effect. This proves
local unauthenticated failure classification, not logged-in ACP conformance or effective restrictions.
No login/logout command was invoked, and no claim is made about the CLI's internal account-cache reads
or updates. `go vet ./...` and `git diff --check` passed at this checkpoint.

### Phase 6 temporary client settings

The profile tests first failed on the missing launcher component, then passed in 2.180s after its
implementation. They cover user-scope permissions/hooks preservation, explicit environment routing,
bounded JSON snapshots, invalid versions/endpoints/credentials, owner-only files, defensive copies,
missing settings and concurrent cleanup. D24 defines the pinned settings/environment contract and
remaining asset/interactive integration work.

The installed unmodified Claude 2.1.263 probe uses only new temporary settings, synthetic local HTTP
servers and owned harmless SessionStart hooks. It observed two model-envelope requests, one catalog
request, both user/project hooks, local env precedence and zero requests to the conflicting provider
endpoint. A user-level full Read deny remained effective despite project-level Read allowance; the
matching tool result was an error, then the client finished normally. All four source settings files
were unchanged and the runtime removed. No Kiro or external model request was made.

The first probe's path-specific Read pattern did not deny the tool; that independently authored test
was corrected to the unambiguous whole-tool Read rule without changing product permission behavior.
The corrected probe passed in 2.526s. Its actual continuation roles were user, system, assistant,
user, system. The later D25 work below handles an exact repetition of the last standing system
sequence; arbitrary new instructions during a suspended tool remain unsupported.
The completed profile race suite, including link/replaced-root cleanup, passed in 1.983s.
`go vet ./...` and `git diff --check` passed at this checkpoint.

### Phase 6 actual-client tool continuation

The local profile probe confirmed that the trailing system text repeats the prior standing update,
and that the decoder accepts the envelope. The session regression covers a two-message system
sequence: changed, older, partial, duplicated and reordered sequences, extra user/assistant content,
then the valid complete repetition and a rejected result replay. Rejections leave pending ownership
available. The first assertion incorrectly required Request for a new ordinary user request; it was
corrected to accept the existing Busy rejection as well. The focused race suite passed in 3.931s.

The first actual-client/ACP integration failed because its whole-tool deny left zero advertised
tools; the independent ACP peer requires one declared relay alias. The separate profile observation
now records that zero count explicitly. A new client-owned PreToolUse hook instead denies the declared
Read call when requested. The peer distinguishes a real MCP tool result from JSON-RPC failure and
requires the synthetic hook denial reason before completing its one ACP prompt.

All three opt-in unmodified Claude 2.1.263 tests passed together in 5.135s. The actual gateway →
independent ACP → MCP relay → client hook denial → matching result path passed in 2.11s, with exactly
two HTTP model requests, final idle state, unchanged source settings and joined driver cleanup. The
other probes verified envelope/header/discovery behavior and settings/environment precedence. No
Kiro, external inference or client file/shell tool effect was used. An approved real tool effect,
interactive UI and live Kiro execution restrictions remain separate release gates.

The full uncached `go test -race -p 2 -count=1 ./...` suite then passed: ACP 4.990s, pool 2.595s,
Anthropic 8.204s, gateway 2.262s, launcher 1.570s, session 8.550s and status 1.378s. All remaining
packages passed; installed-client tests are separately opt-in and were verified above. `go vet ./...`
and `git diff --check` passed after the continuation changes.

### Follow-up public Kiro configuration and catalog observations

The main and helper CLI validation observation passed in 10.224s (8.30s test). Both 2.21.1 binaries
advertise create/validate/list/edit and return zero for the generated candidate and a numeric tools
negative control. A bounded, test-only combined stdout/stderr capture distinguishes the control:
214 bytes containing fixed parse-error markers; the candidate has no diagnostic output. No diagnostic
prose, account data or unrestricted stderr was retained. D27 records that the syntax failure is
reported on stderr, while effective execution restrictions remain unverified.

The public help-only configuration probe passed in 6.686s (4.80s test). Chat advertises model/session
listing, model/agent/effort, legacy/TUI and engine choices; ACP advertises agent/model/effort/engine and
auth-method options. The probe records only flag names. No trust flag, alternate engine, cloud mode,
model prompt, login or configuration mutation command was invoked. The current 2.x reference's
version claims for candidate includeMcpJson/allowedTools fields still require black-box resolution.

The public `chat --list-models --format json` observation passed in 4.924s (3.08s test), without a chat
prompt or ACP session. It returned a JSON object with `default_model` (string) and `models` (array),
with 19 entries at observation time. The first item's field types were model_id/model_name/description/
rate_unit strings and context_window_tokens/rate_multiplier numbers. Only names, types and counts
were recorded, not account values or model output. This identifies a possible finite startup catalog
source; strict decoding, unit semantics and cache integration remain implementation work.

The subsequent strict CLI catalog adapter first failed on absent APIs, then passed
`go test -race -count=1 ./internal/launcher ./internal/catalog` in 2.069s and 1.529s. It reuses the
version/environment constructor, enforces the complete JSON and declared-default contract, and
rejects malformed/duplicate/oversized output without disclosing diagnostics. D30 records the subset.
The opt-in `TestKiroPinnedReadOnlyCatalog` passed in 4.394s (2.54s test): 19 models, an advertised
default and exact ID/alias round trips. All 19 rate-unit values were outside the test's fixed known
enum candidates; no raw units or credit multipliers were invented or recorded. Rate interpretation,
startup cache wiring and live ACP-ID comparison remain separate. `go vet ./...` passed.

The subsequent opt-in initialize-only probe failed its existing-account login preflight in 2.751s
(2.29s test), before starting ACP. It therefore supplies no initialization, capability or effective
restriction evidence. It sends neither session/new nor session/prompt even if initialization succeeds;
its owned candidate has no tools and an effect-free false MCP executable. No login/logout or model
request was attempted. The failed verification is not interpreted as proof of a particular account
state. Local fixture work can continue while live initialization remains unverified.

### Phase 6 owned HTTP server

New server tests first failed on absent APIs. D29 adds connection admission before HTTP parsing,
header/idle/body/write bounds, base-context cancellation and bounded drain/forced-close/join stages.
The first idle-connection test failed because its inner error declaration hid the socket read result;
the corrected test passed without changing product timeouts. Connection lifecycle tracking also
checks Go's final connection states, not just closed descriptors.

`go test -race -count=1 ./internal/gateway` passed in 3.347s, including connection capacity/reuse,
auth/SSE behavior, incomplete headers and unauthorized bodies, oversized headers, idle expiry,
canceled streaming, eight Close callers and bounded reporting for an intentionally uncooperative
catalog handler. That fixture is explicitly released and joined after verifying the failure report.

The new HTTP → session → independent ACP shutdown test initially omitted mandatory ClientInfo in its
fixture configuration and failed before starting ACP. After supplying the fixture identity it passed
in 3.863s overall (0.28s test). It observes the fake's owned PID through SSE, closes the actual loopback
server mid-turn, and verifies the handler/socket counts are zero, the driver is unstarted and the
process group no longer exists. No live Kiro/model or client tool effect was used. `go vet ./...` and
`git diff --check` passed. Full launcher sequencing and suspended-session cleanup remain separate work.

The complete uncached `go test -race -p 2 -count=1 ./...` then passed with the terminal and HTTP server
changes together: ACP 4.924s, pool 2.487s, Anthropic 8.453s, childproc 5.427s, gateway 2.963s,
launcher 2.011s and session 8.227s. All remaining packages passed; installed-client/Kiro tests remain
separately opt-in. No live model prompt is implied by this suite.

### Phase 6 attached client and terminal lifecycle

The previous checkpoint's full uncached `go test -race -p 2 -count=1 ./...` passed: ACP 5.071s,
pool 2.539s, Anthropic 8.009s, gateway 2.202s, launcher 1.544s, session 8.486s and status 1.241s;
all remaining packages passed, with live tests still opt-in.

New attached-client tests first failed on absent APIs. The descriptor/environment, output-stall,
input-stall, admission, repeated Close and whole-group cleanup implementation then passed its race
suite in 5.543s. The disposable macOS terminal fixture exposed two distinct issues: exact termios
comparison included the documented pending-input PENDIN state, and Ignore/Reset did not preserve the
parent's prior signal state. The test now excludes only PENDIN, and the implementation restores
foreground ownership through a bounded effect-free self invocation without changing signal handlers.
The helper initially hit its one-second deadline under the race runtime's default exit delay;
the helper's explicit environment now disables only that test-runtime delay, leaving its deadline
unchanged. D28 records the final behavior and limits.

Final `go test -race -count=1 ./internal/childproc ./internal/launcher` passed in 5.931s and 2.148s.
The actual disposable tty test covers normal/nonzero/forced termination, failed exec, competing tty
ownership, original termios/group restoration, existing SIGTTOU handler delivery and ignored-signal
preservation. No user terminal, real client, Kiro model or client tool effect was used in these tests.
`go vet ./...` and `git diff --check` passed. x/sys's compiled use is recorded in DEPENDENCY_REVIEW.md.
Gateway/session orchestration, actual interactive readiness and shell job control remain unfinished.

### Phase 6 finite launcher subprocess runner

Tests first failed because the CLI runner did not exist. `go test -race -count=1 ./internal/childproc`
then passed in 4.048s. Cases cover bounded stdout overflow, discarded 16 MiB stderr, exact exit code
without raw error disclosure, an explicit environment without inherited sentinels, process admission,
eight concurrent Close callers, and complete group removal for a TERM-ignoring descendant including
when its leader exits first. D23 records the limits; interactive client ownership remains separate.
The additional invalid-command regression passed in 2.241s: duplicate/invalid environment keys, NULs,
oversized inputs and relative executable paths are rejected before admission. `go vet ./...` and
`git diff --check` passed at this checkpoint.

The opt-in read-only Kiro probe verified `kiro-cli 2.21.1`. Its `whoami --format json` did not complete
within the five-second command deadline and was terminated through the owned cleanup path. The
observation test passed as a bounded probe (6.897s suite), but this is **not** evidence of successful
authentication or a verified whoami JSON schema. No account values, stderr, prompts or model calls
were retained. At that checkpoint login status remained unknown; a launcher must not infer logged-in
or logged-out from this timeout. The later D26 preflight above verifies the observed CLI identity. No
login, logout or settings mutation command was invoked.

Current official Kiro links redirect to CLI 3.0/IDE 1.0 documentation. The explicit
[CLI 2.x reference](https://kiro.dev/docs/cli/2x-reference/) describes different tool/permission/hook
configuration, and the [current configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/)
labels its version scope. Checked 2026-09-08. Installed 2.21.1 must not be configured by assuming 3.0
permission semantics or automatically upgraded. R06 still requires effective black-box restriction
proof; syntax validation alone does not establish supported fields or disabled inherited behavior.

### Phase 6 media evidence

Media validation and projection tests first failed on missing APIs. The implemented inline subset,
limits and capability contract are recorded in D19. Independent PNG/JPEG/GIF encoders generate the
test inputs; no upstream image fixture is copied. WebP uses the reviewed x/image header decoder.
Malformed WebP/MIME mismatches are covered. A later positive test below adds independently generated
lossless WebP; live media interoperability remains unverified. Synthetic PDF-shaped bytes test only the
header/transport contract, not document rendering.

Passed `go test -race -count=1 ./internal/anthropic ./internal/projection ./internal/session
./internal/acp ./internal/gateway`: 8.845s, 1.879s, 8.470s, 6.405s and 2.589s respectively. The fake
ACP sees native image bytes, the next turn sends only its text delta, and missing media capabilities
or an escaped prompt above the line limit do not cancel another active pooled session. Provider
token counts remain unchanged at zero. Actual Kiro media/embedding support is not inferred from this.

### Phase 6 streaming evidence

Keepalive tests first failed on the missing configuration/API, then passed with scheduled text,
first-event expiry, total expiry and authentication failure. The gateway/session race suites passed
in 2.959s/7.851s. The independent ACP HTTP suite also runs short ping intervals and verifies that
failure still discards the process and normal completion becomes idle. D20 records header commitment
and the unchanged owned deadlines. These are local fixture results, not live Kiro/client proof.

The focused independent ACP HTTP regression subsequently passed in 5.093s. Its old 150 ms total
budget sometimes expired during ordinary process startup under race instrumentation; the test now
uses separate bounded startup, first-event and deliberately slow-turn scenarios. Product deadlines
were not relaxed by that correction.

### Phase 6 usage cache and turn metrics

Cache/metadata/queue tests initially failed on missing APIs, followed by new integration failures on
absent manager and gateway metrics configuration. The independent fake emits three identical private
metadata notifications; two delivered main turns produce exactly two records in created/reused
states. Title, parent-agent, canceled and undelivered cases produce none. A real fake ACP/MCP relay
round trip publishes once only after its final response, never on its successful tool handoff.

The UI hook tests reject wrong model/UI authority, wrong methods/paths and unsupported/oversize bodies
without draining queued records. Missing account usage preserves the latest model-only status.
Usage tests cover 100 coalesced readers, TTL/backoff, failure retaining prior values, copied snapshots,
shutdown joining a canceled fetch and invalid numeric values. Private unknown strings never enter
numeric diagnostics. D21 records the provisional field mapping and best-effort bounded queue policy.

The public Claude gateway contract later exposed a gap in the background classifier: the agent
header identifies a first-level subagent without any parent header. A new regression first produced
one incorrect foreground record; the manager now excludes either agent header as well as title work.
The focused foreground/tool-handoff race suite passed in 4.038s after this correction.

Passed `go test -race -count=1 ./internal/session ./internal/gateway ./internal/status
./internal/kirofeature`: 9.554s, 3.025s, 2.152s and 2.344s respectively. Earlier focused metadata/cache
tests passed in 1.924s/1.946s; the main lifecycle regression passed in 3.471s. These are synthetic
protocol and module results; a real Kiro account-usage command, private payload mapping and actual
client hook bridge have not been verified. No model credits or client tool effects were consumed.

### Phase 6 local estimates

The estimator's tests first failed on absent APIs, then passed for deterministic UTF-8 byte estimates,
128-entry computation caching, ordered logical prefix accounting, JSON formatting, large integer
arguments and exclusion of declared media/thinking. New session assertions initially failed on the
missing completion estimate. The integration now verifies that text before tools, serialized tool
arguments and final text all contribute once across HTTP handoffs; consecutive main turns retain
logical prefix opportunity. D22 documents that this is not a provider tokenizer or cache-hit claim.

Passed `go test -race -count=1 ./internal/session ./internal/status ./internal/gateway` in 9.380s,
2.423s and 3.065s respectively. The earlier standalone status suite passed in 1.955s. No provider
usage fields changed, and the cache/records retain only keyed digests and numeric diagnostics.

The WebP positive fixture is independently generated from the public
[lossless bitstream format](https://developers.google.com/speed/webp/docs/webp_lossless_bitstream_specification)
and [RIFF container contract](https://developers.google.com/speed/webp/docs/riff_container), checked
2026-09-08. It uses newly chosen constant colors and single-symbol code alphabets, with no imported
image or upstream encoder/test code. Both opaque and transparent 2×3 images must fully decode to
their exact pixels before the gateway's header-only validation is tested. This adds local format
evidence without claiming Kiro image interoperability or full production pixel validation.

The focused WebP race test passed in 2.061s. `go vet ./...` and `git diff --check` also passed after
the estimator and positive media fixture were integrated.

The first full race run had one fake ACP initialization exceed its existing two-second test RPC
limit; other packages passed. The unchanged negotiation test then passed ten consecutive repetitions
(2.514s suite; first case 0.33s, later cases 0.03–0.05s). The complete uncached suite with package
parallelism bounded at two, `go test -race -p 2 -count=1 ./...`, passed: ACP 5.014s, pool 2.306s,
Anthropic 7.896s, gateway 2.230s, session 8.273s and status 1.338s. Product and test deadlines remain
unchanged. Higher build/test process concurrency and startup sensitivity remain part of Phase 7 load
verification; a single passing rerun is not a diagnosis of the initial timeout.

### Installed Kiro usage command surface

Read-only help probes on unmodified Kiro 2.21.1 returned exit 2 for `usage --help` (unrecognized
subcommand); `--help-all` and `user --help` advertise no account-usage subcommand. `chat --help`
documents list-model/list-session JSON formats, not a usage JSON interface. No chat prompt or slash
command was submitted. The public Kiro slash-command reference describes interactive `/usage`, but
does not establish a safe noninteractive adapter for this installed version. Account usage must remain
unavailable/model-only until a version-specific, non-model command path is independently verified;
do not guess a CLI command or turn a refresh into inference. Cache/metrics infrastructure can proceed.

### Post-Phase 5 input and relay hardening

Two failing regressions reproduced an invalid client model canceling an active pooled sibling and
unsupported system-message lifetime/effort fields disappearing during normalization. Invalid local
model/projection requests now dispose only their lease. Per-message effort and turn-scoped expiry
reject explicitly; standing `clear_at: "never"` is accepted. D18 records the public-source correction
and supported subset. This does not claim complete native Anthropic system-priority semantics.

Relay admission now follows the owned ACP prompt lifetime. It is closed during creation/load/idle
and atomically checked at prompt completion; pending or validating calls cannot cross into another
turn. The new `chat-tools-idle` fake attempts a call during session creation, requires its error,
and then exercises the normal client handoff. The relay API regression initially failed on absent
lifecycle methods before their implementation.

Passed `go test -race -count=1 ./internal/anthropic ./internal/session ./internal/relay/...`:
anthropic 1.736s, session 6.800s, broker 2.052s and MCP 3.714s. The added fake-process premature-call,
active-sibling and continuation checks then passed together in 3.869s. `go vet ./...` passed.
The installed-client shape observation also records message-level field names; its latest synthetic
sample contained only role/content and no clear_at field (1.454s overall). No real model/tool effect.

### Actual client envelope observation

The opt-in `TestClaudeClientGatewayContract` passed against installed unmodified Claude Code 2.1.263
using a synthetic local gateway (1.363s overall, 0.63s test). One message and one catalog request were
observed; the session header matched and both synthetic models appeared in the discovery cache.
Only field names/types/counts/Boolean outcomes were recorded. Global client settings were unchanged,
the private runtime was removed, and no model credits or client tool effects were used.

Two earlier attempts reproduced rejection of a trailing per-message system update. The decoder now
accepts only text in that role, finds the latest user correctly and preserves update order in ACP
projection. The parser/projection/catalog/session race suites then passed. Gateway header validation
and pending-tool cross-header rejection have independent regression tests. Decision D14 records
the exact scope: interactive UI and actual-client tool continuation remain unverified.

| Phase | Completion evidence required | State |
| --- | --- | --- |
| 1 | All acceptance A plus applicable G; independent fake child, framing, negotiation, correlation, notifications, stderr, deadlines, process-group cleanup | Passed on local macOS with fake ACP |
| 2 | Authenticated HTTP text path, exact SSE/non-streaming responses, authentication fallback, disconnect tests | Passed with independent fake ACP; broader B/C requirements tracked below |
| 3 | Model catalog/mapping/cache/selection and optional effort state | Passed independent module/process tests; launcher and live interoperability remain below |
| 4 | Restricted Kiro agent proof, MCP relay, schema validation, client-only tool effects and result ownership | Initial native/inheritance controls and all six actual Kiro/client rule/hook cases pass; interactive and bare-refusal controls pass with fake ACP; development run enabled, broader alpha/release paths remain |
| 5 | Request families/history/pool/persistence/resume and crash tests | Independent implementation tests pass; live client/Kiro and extended hardening remain |
| 6 | Media/web capabilities, cached usage/metrics, isolated launcher/profile and client interoperability | Pending |
| 7 | Full acceptance, fuzz/race/load, license inventory, macOS packaging/install/uninstall and opt-in live gates | Pending |

Owner rights, Kiro/private-extension usage permission, distribution intent and project license are
the owner's external items (D121) and are not confirmed by this repository. Do not publish a release
or declare legal clearance from anything recorded here. Live tests that consume credits remain
separately marked and opt-in under ACCEPTANCE_SPEC.md.
