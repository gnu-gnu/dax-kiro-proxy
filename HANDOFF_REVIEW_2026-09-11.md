# Handoff review: D109–D120 (2026-09-10 to 2026-09-11)

This document summarizes one assistant session lineage's work on branch
`codex/kiro-isolation-checks` so that the previous maintainer can judge each change and decide what
to keep, revert or re-measure. It is a brief, not a record: the decision records D109–D120 in
`IMPLEMENTATION_DECISIONS.md`, the matching bullets in `DEVELOPMENT_STATUS.md`, the artifact
snapshots in `DEPENDENCY_REVIEW.md` and the header of `LIVE_KIRO_TEST_PLAN.md` remain the
authoritative text, and this brief defers to them wherever they differ.

Overtaken since this brief was written: D121–D131 (2026-09-11 to 2026-09-13) are recorded in the
authoritative documents. D126 moved `SupportedClientVersion` to 2.1.269 and installed that build on
2026-09-12; D125 strengthened trust publication. D127 installs progress and relay-setup timeout
corrections with independent review complete. D128 installs the resolved-call cancellation
correction, with review complete. D129 installs retained tool outcomes across finalization and
known-deadline cause preservation, with independent review complete. D130 installs the public
request-limit completion mapping, with independent review complete. The 2.1.267 statements
in sections 3, 4 and 6 describe the state as of 2026-09-11.

D131 fixes the account-command diagnostic/cleanup defect in the later handoff's
item 9. Independent process controls, all 27 race-tested packages, full vet and the three native
core controls pass. D131 is installed as `account-check`, with 144 byte checks, 18 component
checks and doctor passing on its first invocation. Independent review accepts `ac71b58` with no
actionable finding or refreeze; focused race and artifact checks pass independently.

## 1. Scope and branch state

- Commit range: `3464d12` (D109, 2026-09-10) through `c577dcf` (D120 live soak, 2026-09-11), 23
  commits, all on `codex/kiro-isolation-checks`. Every commit in the range carries a
  `Co-Authored-By: Claude …` trailer. One earlier commit, `8e7b4dc` (D103, 2026-09-10), carries a
  similar trailer from a separate session and is not covered here.
- `main` is a fast-forward ancestor of the branch (81 commits behind at `c577dcf`). No remote is
  configured and nothing has been pushed; merging is the user's later decision.
- Whole-range diff: 88 files, +33,537/−388 lines. Production (non-test, non-doc) code accounts for
  13 files, +560/−42 lines; the rest is tests, fixtures, inventories and documents.
- Dependencies: none added, removed or upgraded. Every artifact snapshot keeps the 267 import paths
  and the four external modules unchanged, with `release_clearance` false.

## 2. What triggered the work

- The host's Claude Code client updated itself from the measured 2.1.263 to 2.1.267 (observed
  2026-09-10) and the host's Kiro main/helper from the measured 2.21.2 to 2.21.3 (2026-09-11). The
  exact-version pins then rejected every `doctor` and `run`.
- Every interactive `run` showed the client's onboarding dialogs (theme, API-key approval, security
  notes), the unknown-model notice of the newer client, and the workspace-trust dialog for projects
  the user had never opened natively.
- The user reported that, in an earlier build, a session could not be reused after Kiro login was
  restored; D117 checks this on the current code.
- The release-candidate stage lists soak and descriptor/process/memory checks with no many-turn
  actual-client control, and plugin acceptance against the actual Kiro backend was still recorded as
  unverified.

## 3. Decisions that need the reviewer's judgment

Ranked by how far they move from the previously recorded policy. Each item names the alternative the
reviewer can choose.

1. **D110 and D114: admission by major version (user-directed deviation).** `ACCEPTANCE_SPEC.md`
   section I pinned the client and Kiro exactly. Now a Claude Code build whose major version equals
   the measured pin's, and a Kiro main/helper pair reporting one build with the measured major,
   launch without new measurement. `doctor` labels such builds `(unmeasured; measured <pin>)`, the
   startup report carries `client_version_measured` and `kiro_version_measured`, and evidence
   collected on an admitted build must be recorded with its observed version. Section I records this
   as a directed exception. Alternatives: restore exact pins (revert `d8d3321`, `93182c9` for the
   client and `23c69d4` for Kiro, keeping the D113/D114 pin moves), or add an explicit trust
   override instead of silent admission. Risk: a later 2.x build can change request shapes,
   onboarding, hooks, ACP negotiation or CLI output without any control noticing until a measured
   migration is run.
2. **D118: the launcher's only write to a user source file (reviewed D64 exception).** After the
   client exits, a yes answer to the client's workspace-trust dialog is spliced into
   `~/.claude.json` as exactly `projects[<client key>].hasTrustDialogAccepted = true`. Guards: only
   when the source existed at launch and lacked the value, only when its bytes equal the launch-time
   digest, only when the private profile records the answer for the launch project, spliced without
   re-encoding, written beside the source with its permission bits and renamed into place, links and
   unsafe modes abort, absent sources are never created, "No, exit" records nothing, every doubt
   skips silently. The user chose native parity over the two rejected alternatives: auto-trusting
   the launch directory (bypasses the client's security gate for project hooks and settings) and a
   product-private trust store (native Claude Code would not see it). Limits the reviewer should
   weigh: the control exercised an owned HOME, never the user's actual global file; symlinked launch
   paths are matched by resolved comparison but were measured only with a plain path. Alternative:
   revert `102c4a2`, `f522c4d` and `18525ee`, which restores the once-per-launch dialog for
   proxy-only projects.
3. **D115: Bearer-only client environment and the projected onboarding flag.** D24 set both
   `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_API_KEY`; the prepared environment now sets only the Bearer
   token, which removes the client's per-launch key-approval prompt and its both-variables warning.
   The gateway accepts either header, so routing is unchanged. The private `.claude.json` projection
   additionally carries the global Boolean `hasCompletedOnboarding` from the user's file, which
   suppresses the theme picker and security notes. Measured on 2.1.267 only; an admitted unmeasured
   build could treat the headers differently.
4. **D112: an undocumented client environment variable.** The prepared environment sets
   `CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1`, named by the client's own notice text
   and absent from the public environment reference (checked 2026-09-10). It restores the measured
   build's wait-for-the-API behavior for the product's catalog-absent model IDs; it does not
   describe the model or claim a context window. The alternative documented by the client
   (`behavesAs`, `modelOverrides`, a `[1m]` suffix) would assert an unmeasured equivalence between a
   Kiro model and a Claude model.
5. **D111: a reviewed projection exclusion.** The client's top-level `claudeAiMcpEverConnected`
   array (names of claude.ai connectors ever connected) is omitted from the MCP projection instead
   of rejected. It declares no server and no decision; every other unrecognized MCP-related key
   still rejects.
6. **D109: native images inside historical tool results.** Fresh-session projection now validates
   inline images nested in `tool_result` content and emits them as native ACP image parts between
   `tool_result`/`tool_result_content`/`tool_result_end` JSON markers inside public ACP text parts.
   Existing media limits apply to the combined count. `PROTOCOL_SPEC.md` records the marker
   convention.
7. **D113 and D114: the measured pins moved.** `SupportedClientVersion` is 2.1.267 and
   `SupportedKiroVersion` is 2.21.3 after fresh regressions on the new builds (68 installed-client
   controls; 39 finite Kiro controls). Earlier live evidence recorded for 2.1.263/2.21.2 stays
   labeled with those builds; the 18 credit-consuming live controls that existed then were rerun on
   the new pair (section 6).

## 4. Production changes by decision

| Decision | Production files | Behavior change | Artifact snapshot (inventory, SHA-256 prefix) |
| --- | --- | --- | --- |
| D109 | `internal/projection/media.go`, `internal/projection/text.go`, `internal/anthropic/client_tools.go` | Nested result images become native ACP images with JSON boundary markers; plain-text projection rejects supported result images | `tool-images`, `5c8cd3d2` (built from uncommitted inputs, parent `86ec14b`) |
| D110 | `internal/launcher/startup.go`, `internal/launcher/profile.go`, `cmd/dax-kiro-proxy/main.go` | Client admitted by major version; detected version recorded; `client_version_measured`; doctor unmeasured rendering | `client-version`, `590d7c6a` (D110–D112, revision `93182c9`) |
| D111 | `internal/launcher/client_mcp.go` | `claudeAiMcpEverConnected` excluded from global and per-project projection | same |
| D112 | `internal/launcher/profile.go` | Unknown-model window-enforcement opt-out in the prepared environment | same |
| D113 | `internal/launcher/profile.go` | Measured client pin 2.1.267 | `measured-client`, `840b8333` (revision `e6f96e9`) |
| D114 | `internal/launcher/kiro.go`, `kiro_execution.go`, `kiro_usage.go`, `profile.go`, `startup.go`, `cmd/dax-kiro-proxy/main.go`, `internal/kirofeature/usage.go` (comment) | Kiro pair admitted by major version, same build required on main and helper; measured pin 2.21.3; policy digest names 2.21.3; `kiro_version_measured`; doctor rendering | `measured-kiro`, `c0996317` (revision `fe71d19`) |
| D115 | `internal/launcher/profile.go`, `internal/launcher/client_mcp.go` | Bearer-only host environment; projected `hasCompletedOnboarding` | `onboarding`, `720b6f6e` (revision `52b460b`) |
| D118 | `internal/launcher/client_trust.go` (new), `client_mcp.go`, `profile.go`, `runtime.go` | Post-exit trust write-back with the guards above; `ClientRunResult.ProjectTrustPersisted` | `project-trust`, `0b2f416b` (revision `f522c4d`) |

D116, D117, D119 and D120 change no production code. The installed executable on this host is the
D118 build (`~/.local/bin/dax-kiro-proxy`, SHA-256 `0b2f416b…`); its `doctor` reports login and
execution policy verified, launch available, and Kiro 2.21.3 and Claude Code 2.1.267 both measured.
Each snapshot was rebuilt from the named clean committed revision (D109 excepted, as recorded),
verified with `tools/verify_dependency_inventory.py` (141 byte checks, 142 from D118), frozen with a
`freeze-d1xx.py` script and installed with `install --force`; the verifier's snapshot choices grew
accordingly.

## 5. Test-only changes and harness facts worth knowing

- **Compiled-command terminal observer** (`internal/interop/terminal_stream_test.go` with the
  independent terminal peer under `internal/interop/testdata/terminalpeer`): new modes
  `held-hook-followup` (D116), `auth-expiry-followup` and `held-hook-auth-expiry` (D117),
  `trust-dialog` (D118) and `soak-turns` (D120). Observer bounds (receipt files 64 KiB/512 KiB,
  retained capture 256 KiB, terminal lifetime 70s, scenario 2min, peer frame guard 1,024 frames/8
  MiB) are now per-mode parameters; only the soak mode scales them with its declared turn count.
  `DAX_INTEROP_SOAK_TURNS` accepts 5..200.
- **Screen reconstructor** (D113): parses two-byte intermediate escapes (`ESC ( B`) and
  DCS/APC/PM/SOS strings as non-text; picker actions wait for a frame unchanged for 100ms; a
  confirmation naming a different row fails immediately.
- **Interrupted-history forms** (D113, D116): 2.1.267 may retain an interrupted tool pair on restart
  (`is_error` result with `[Request interrupted by user for tool use]`, then `Continue from where
  you left off.`) beside the abandoned form of 2.1.263, and sends a third same-session shape after
  Ctrl+C on a held tool (`tu=1 tr=0 ef=0 ir=1 ct=0 ph=0`). The HTTP guard and the independent ACP
  peer classify each form and must agree.
- **Version gates in tests**: the installed-client and Kiro evidence gates use the shared admission
  parser and log the observed version instead of failing on an admitted unmeasured build.
- **Kiro 2.21.3 login scope**: login lives under the account HOME (`Library/Application
  Support/kiro-cli/data.sqlite3`); a synthetic HOME is logged out (`whoami` prints
  `{"account":null}`, exit 1). Five synthetic-HOME controls now require those diagnostics. D117 uses
  this to reproduce the logged-out boundary without `kiro-cli logout`: `kiro-cli acp` writes nothing
  on stdout, one recognized stderr line, exit 1 at `initialize`.
- **Trust seeding**: terminal scenarios seed native trust in the owned global file, not the private
  profile, because a fixture write into the private profile is indistinguishable from a user's
  answer and D118 would persist it.
- **Load-sensitive controls** (commit `c5e9d30`): the existing-statusline precedence and the two
  personal-asset controls got a bounded startup-notice wait and shape-only diagnostics after failing
  only under full-batch load; both pass standalone and in three repetitions.
- New test files: `internal/interop/image_history_test.go`, `unknown_model_test.go`,
  `kiro_auth_test.go`, `live_plugin_assets_test.go`, `internal/launcher/client_trust_test.go`,
  `internal/acp/testdata/fake/image_history.go`.

## 6. Verification state at handoff

| Check | Result | Log under `.cache/history-review/` |
| --- | --- | --- |
| Installed-client batch on 2.1.267, credits off | 72 controls pass (69 in a 637.174s batch; 3 load-sensitive controls standalone, 35.929s) | `d118-*`, `d115-claude-regression.log`, `d113-claude-regression-clean.log` |
| Finite Kiro controls on 2.21.3, credits off | 39 pass, 18 live skipped, 307.128s | `d114-kiro-finite.log` |
| Credit-consuming live controls on 2.21.3/2.1.267 (user-authorized) | the 18 pre-existing `TestKiroLive*` controls pass (16 in one 877.124s package run); D119's skill and hooks controls pass (3 model turns); D120's 20-turn live soak passes (59.55s, 21 model turns); all 21 live functions at `c577dcf` therefore pass on the pair | `live-recheck-kiro2213-claude21267.log`, `live-recheck-2-kiro2213-claude21267.log`, `d119-live-plugin-assets.log`, `d120-live-soak-20.log` |
| Compiled-command fixture controls | 11 pass, 105.776s | `d120-compiled-run-b.log` |
| Fixture soak | 20 turns (14.59s) and 100 turns (27.52s): proxy 16,464→18,240 KiB and 17,776→20,736 KiB, descriptors 22→22, owned processes 2→2 | `d120-soak-20.log`, `d120-soak-100.log` |
| Live soak resource envelope | proxy 17,776→18,096 KiB (peak 18,384), descriptors 24→22, 2 processes; four-process backend group 319,568→106,560 KiB (a 19-turn first attempt sampled 101,856→94,560 KiB) | `d120-live-soak-20.log`, `d120-live-soak-19-frame-budget.log` |
| Whole-repository race suites (opt-ins off) and vet | pass on the final code of each production decision; interop race 28.820s after D120 | `d1xx-all-race.log`, `d1xx-all-vet.log`, `d120-race-b.log` |
| Churn controls at maximum waves | 64 waves (4.48s) and 32 pending-tool waves (9.61s) end at the descriptor/goroutine/heap floor | `d120-churn-64.log`, `d120-pending-churn-32.log` |

The backend group's warm-up sample in the live soak (about 312 MiB) is three times the first
attempt's and falls to about 104 MiB by the end; the record calls it a transient of the actual
backend with an unmeasured cause. The soak's backend envelope (256 MiB growth over warm-up) is a
first declared value, not a derived one.

## 7. Retained artifacts, inventories and logs

- Committed: `third_party/inventory/macos-arm64-<name>.json` for `tool-images`, `client-version`,
  `measured-client`, `measured-kiro`, `onboarding` and `project-trust`, each hashing its
  predecessor; the verifier's snapshot list in `tools/verify_dependency_inventory.py`.
- Host-local only (`.cache/` is ignored by git): every `d1xx-*.log` cited by the records, the
  retained executables `dax-kiro-proxy-d109` … `dax-kiro-proxy-d118`, the `freeze-d1xx.py` scripts,
  `d1xx-build-info.json`/`d1xx-command-packages.json`/`d1xx-head.txt`, and the expect procedures
  `d112-pty-observe.exp` and `d115-pty-observe.exp`. A reviewer on another machine cannot read
  these; the records quote their fixed facts.
- Logs contain fixed-shape facts only: counts, timings, sizes and classifications. No prompts, tool
  outputs, credentials or terminal captures are retained.

## 8. Open items and known limits

- Clean macOS install/uninstall on a host without the development tree (phase 7).
- Dependency inventory and advisory scan: completed after this brief by D121 (component record,
  verifier `--components`, govulncheck clean in three modes); the owner's rights checklist was moved
  outside the repository's scope at the owner's direction; `release_clearance` stays false by
  construction.
- Admitted unmeasured builds (section 3, item 1) run the measured execution policy without new
  measurement until a D113/D114-style migration.
- D118 was never observed against the user's own global file, and a symlinked launch path was not
  measured.
- A token that expires inside a running Kiro process is emulated from the measured logged-out
  boundary, not observed; the late-expiry path after streaming starts is covered by gateway unit
  controls only.
- A longer live soak, a combined concurrency-plus-soak run, mid-session plugin enable/disable,
  Kiro-side skills and agents, and remote marketplaces on the actual backend remain separate.
- D102 (personal-rules alias exclusion) was re-checked on 2.1.267: no native relocation interface
  exists; the gap stays recorded with no product change.
- The D113 picker observation notes that Enter in `/model` now also sets the client's default for
  new sessions; the disposed private profile does not carry it forward, and what the client persists
  there was not observed.

## 9. Reproducing the evidence on this host

Environment used by every run in the range (opt-ins off unless stated):

```sh
umask 077
export GOTOOLCHAIN=go1.27.1
export GOMODCACHE=$PWD/.cache/gomod GOCACHE=$PWD/.cache/gobuild
export DAX_INTEROP_CLAUDE_BINARY=/Users/geunwooshim/.local/bin/claude
export DAX_INTEROP_KIRO_BINARY=/Users/geunwooshim/.local/bin/kiro-cli
export DAX_INTEROP_KIRO_CREDIT_OPT_IN=0
go test ./internal/interop -run '^TestCompiledRun.*WithFakeACP$' -count=1 -v -timeout 20m
go test -race ./... -count=1
go vet ./...
```

- Credit-consuming controls (`TestKiroLive*`, `TestKiroLiveCompiledRunSoakTurns`, the live plugin
  modes) need `DAX_INTEROP_KIRO_CREDIT_OPT_IN=1` and the user's explicit approval for each run;
  never run two client- or Kiro-launching tests concurrently.
- `umask 077` is required: private readers reject group/other-readable files, and a permissive umask
  fails owned fixtures for a reason unrelated to the client.
- Never run `kiro-cli login` or `kiro-cli logout` from a test; the logged-out boundary is reproduced
  from a synthetic HOME. The user's own `kiro-cli` session may be running on the host.
- Host memory pressure has killed long batches; rerun the batch rather than silently splitting it.
- The complete installed-client set is `-run '^Test(Claude|CompiledRun)'` in `internal/interop` (73
  functions at `c577dcf`; the D118 batch ran 72 in 637s before the D120 soak control was added); the
  finite Kiro set is `-run '^TestKiro'` with credits off, which skips the 21 live functions.

## 10. Documents amended in this range

- `IMPLEMENTATION_DECISIONS.md`: D109–D120 added (+649 lines).
- `DEVELOPMENT_STATUS.md`: one bullet per decision at the top of "Current evidence"; live
  re-verification and D102 re-check noted.
- `ACCEPTANCE_SPEC.md`: section F records the retained interrupted form and the same-session shape;
  section I records the directed major-version exceptions; section G's soak paragraph names the D120
  runs.
- `README.md`: measured combination now Kiro 2.21.3 / Claude Code 2.1.267, admission wording,
  D109–D120 notes in the development narrative, the D118 write-back described beside the
  source-preservation statement.
- `LIVE_KIRO_TEST_PLAN.md`: header records the pin moves, the `umask 077` requirement, the
  18-control live re-verification and the 19th live control; D119 and D120 body sections were added
  with this brief.
- `DEPENDENCY_REVIEW.md`: artifact snapshots D109, D110–D112, D113, D114, D115 and D118.
- `PROTOCOL_SPEC.md` (D109 result-image markers) and `PRODUCT_SPEC.md` (six-line amendment) were
  touched; `tools/verify_dependency_inventory.py` lists the new snapshots.

## 11. Commit map for selective review or revert

| Decision | Commits (oldest first) |
| --- | --- |
| D109 | `3464d12` |
| D110–D112 | `d8d3321`, `3ffb6d0`, `93182c9`, `3f2ec2d` (freeze) |
| D113 | `cbd0638`, `dea53b3`, `e6f96e9`, `1f0ef80` (freeze) |
| D114 | `23c69d4`, `fe71d19`, `99cefd8` (freeze) |
| D115 | `547e766`, `52b460b`, `b915993` (freeze) |
| D116 | `f8557ff` |
| Live re-verification on 2.21.3/2.1.267 | `1fde741`, `141a646` |
| D117 | `2959d33` |
| D118 | `102c4a2`, `f522c4d`, `18525ee` (freeze) |
| D102 re-check, load-sensitive bounds | `c5e9d30` |
| D119 | `f236f18` |
| D120 | `72454a6`, `c577dcf` |

Reverting a production decision also invalidates the artifact snapshots that follow it, because each
inventory hashes its predecessor; a revert needs a fresh clean-revision rebuild and freeze in the
D79 manner, and the installed executable must be reinstalled from that build.

## 12. Documentation corrections made with this brief

An audit of every root document against the D109–D120 state found present-tense statements that the
records had overtaken. They were corrected in the same commit as this brief, without changing any
recorded evidence:

- `PRODUCT_SPEC.md`: the no-settings-modification rule and the source-preservation sentence now name
  the D118 exception; the live plugin gate wording points at D119.
- `README.md`: the current-phase paragraph, the doctor field list (Kiro measurement fields), the
  live-evidence block (one sentence stating the 2026-09-11 re-verification on 2.21.3/2.1.267), the
  plugin-gate and soak sentences, and the usage adapter version.
- `DEVELOPMENT_STATUS.md`: the last-updated date, a live re-verification bullet that had been
  embedded mid-line in the D117 bullet, the D113 bullet's "remain open" items closed by D115, the
  "other sixteen live controls remain 2.21.2 evidence" sentence, and the D94/D95 soak residues.
- `PROTOCOL_SPEC.md`: the account-usage and effort paragraphs name the measured 2.21.3 pin and the
  D114 admission.
- `FLOWS_AND_STATE.md`: startup step 7 and shutdown step 10 name the guarded D118 write-back.
- `LIVE_KIRO_TEST_PLAN.md`: historical preparation notes moved to past tense, "exact-version
  preflight" qualified by D114, the D72 plugin-gate sentence points at the new D119 section, the
  D94/D95 residues name D120, and D119/D120 sections were added.
- `ACCEPTANCE_SPEC.md`: the release-blocking invariant "global/project client settings are modified"
  and the development-run stage row carry the D118 qualifier.
- `IMPLEMENTATION_DECISIONS.md`: D13's "current executable" sentence moved to past tense; D119's
  unfilled regression placeholder was replaced with the recorded four-control result.
