package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
)

const terminalStreamPrompt = `Begin with the concatenation of Ready and _47 without spaces. Then list integers 1 through 2000, one per line, without tools or any other text.`
const terminalFollowupPrompt = `Stop the counting task. Reply with the concatenation of Follow and _49 without spaces, and no other text.`
const terminalRecoveryPrompt = `Reply with the concatenation of Recovered and _53 without spaces, and no other text.`
const terminalModelFirst = `Reply with the concatenation of ModelFirst and _61 without spaces, and no other text.`
const terminalModelSecond = `Reply with the concatenation of ModelSecond and _67 without spaces, and no other text.`

func terminalModelPickerKey(screen, target string) (string, int) {
	if !strings.Contains(strings.ToLower(screen), "select model") {
		return "", -1
	}
	selected, wanted := -1, -1
	for i, line := range strings.Split(strings.ToLower(screen), "\n") {
		if strings.Contains(line, "❯") {
			selected = i
		}
		if strings.Contains(line, target) {
			wanted = i
		}
	}
	if selected < 0 || wanted < 0 {
		return "", -1
	}
	if selected == wanted {
		return "\r", selected
	}
	if selected < wanted {
		return "\x1b[B", selected
	}
	return "\x1b[A", selected
}

func terminalModelComplete(r terminalTrace, slot int) bool {
	if r.ModelFirstMarkers != 1 || r.ModelFollowMarkers != 1 {
		return false
	}
	return r.Clients == 1 && r.Prompts == 1 && r.Texts > 0 && r.Ends == 1 && r.Cancels == 0 && r.Canceled == 0 && r.FollowPrompts == 1 && r.FollowTexts > 0 && r.FollowEnds == 1 && r.FollowCancels == 0 && r.FollowCanceled == 0 && r.ModelFirstRecords == 1 && r.ModelFollowRecords == 1 && r.ModelFirstSlot == 1 && r.ModelFollowSlot == slot && (slot == 1 || slot == 2 && r.ModelTargetAcks > 0) && r.TitlePrompts <= 2 && r.Failures == 0 && r.PromptFailures == 0 && !r.Exited
}

func terminalCancelEligible(trace terminalTrace) bool {
	return trace.Prompts == 1 && trace.TitlePrompts <= 1 && trace.Texts >= 2 && trace.Ends == 0 && trace.Cancels == 0 && trace.Canceled == 0 && trace.Failures == 0 && trace.PromptFailures == 0 && !trace.Exited
}

func terminalHookEligible(trace terminalTrace) bool {
	return trace.Prompts == 1 && trace.TitlePrompts <= 1 && trace.HookHeld == 1 && trace.Hook > 1 && trace.HookReleased == 0 && trace.HookPost == 0 && trace.HookInterrupted == 0 && trace.Ends == 0 && trace.Cancels == 0 && trace.Canceled == 0 && trace.Failures == 0 && trace.PromptFailures == 0 && !trace.Exited
}

func terminalFollowupComplete(r terminalTrace) bool {
	return r.Clients == 1 && r.Prompts == 1 && r.Cancels > 0 && r.Ends == 0 && r.FollowPrompts == 1 && r.FollowACP > 1 && r.FollowACP != r.ACP && r.FollowTexts > 0 && r.FollowEnds == 1 && r.FollowCancels == 0 && r.FollowCanceled == 0 && r.FollowOldInput && r.FollowNewInput && !r.FollowNull && r.TitlePrompts <= 2 && r.Failures == 0 && r.PromptFailures == 0 && !r.Exited
}

// After Ctrl+C during a held tool, the old prompt never ends and nothing was cancelled over ACP;
// the hook was interrupted, no result reached the relay, and the new question completes elsewhere.
func terminalHeldFollowupComplete(r terminalTrace) bool {
	return r.Clients == 1 && r.Prompts == 1 && r.Ends == 0 && r.HookHeld == 1 && r.HookInterrupted == 1 && r.HookReleased == 0 && r.HookPost == 0 && r.RelayCalls == 1 && r.RelayResults == 0 && r.FollowPrompts == 1 && r.FollowACP > 1 && r.FollowACP != r.ACP && r.FollowTexts > 0 && r.FollowEnds == 1 && r.FollowCancels == 0 && r.FollowCanceled == 0 && r.FollowOldInput && r.FollowNewInput && !r.FollowNull && r.TitlePrompts <= 2 && r.Failures == 0 && r.PromptFailures == 0 && !r.Exited
}

// manufacturedToolResult reports a historical tool_result without is_error in the fixed counts.
func manufacturedToolResult(form string) bool {
	var uses, results, errorFlags, interrupted, continuation, placeholder int
	if _, err := fmt.Sscanf(form, "tu=%d tr=%d ef=%d ir=%d ct=%d ph=%d", &uses, &results, &errorFlags, &interrupted, &continuation, &placeholder); err != nil {
		return false
	}
	return results > errorFlags
}

func terminalExitConfirmation(screen string) bool {
	plain := strings.ToLower(strings.Join(strings.Fields(screen), " "))
	return strings.Contains(plain, "ctrl+d again to exit") || strings.Contains(plain, "ctrl-d again to exit")
}

type terminalReceipt struct {
	ModelSlot  int  `json:"model_slot"`
	NullMarker bool `json:"null_marker"`
	// Fixed counts of the historical tool blocks a follow-up prompt carried (D116).
	InterruptedForm string `json:"interrupted_form"`
	FollowScope     bool   `json:"follow_scope"`
	RecoveryScope   bool   `json:"recovery_scope"`
	SoakScope       int    `json:"soak_scope"`
	OldInput        bool   `json:"old_input"`
	NewInput        bool   `json:"new_input"`
	PartialMarker   bool   `json:"partial_marker"`
	Kind            string `json:"kind"`
	PID             int    `json:"pid"`
	Group           int    `json:"group"`
	Child           int    `json:"child"`
	Foreground      int    `json:"foreground"`
	Profile         string `json:"profile"`
	Endpoint        string `json:"endpoint"`
	Code            int    `json:"code"`
	Restored        bool   `json:"restored"`
	MainHint        bool   `json:"main_hint"`
	TitleHint       bool   `json:"title_hint"`
	SummaryHint     bool   `json:"summary_hint"`
	TitleScope      bool   `json:"title_scope"`
}

type terminalTrace struct {
	HistoryInputs, HistoryAnswers                                                             int
	ModelFirstMarkers, ModelFollowMarkers                                                     int
	ACPs                                                                                      int
	ModelFirstRecords, ModelFollowRecords, ModelFirstSlot, ModelFollowSlot, ModelTargetAcks   int
	PromptFailures                                                                            int
	FollowNull                                                                                bool
	Clients, FollowPrompts, FollowACP, FollowTexts, FollowEnds, FollowCancels, FollowCanceled int
	FollowOldInput, FollowNewInput, FollowPartial                                             bool
	FollowForm                                                                                string
	Hook, HookHeld, HookReleased, HookInterrupted, HookPost                                   int
	AuthExits                                                                                 int
	RecoveryPrompts, RecoveryTexts, RecoveryEnds, RecoveryACP                                 int
	SoakPrompts, SoakTexts, SoakEnds, SoakACP                                                 int
	RelayCalls, RelayResults                                                                  int
	Client, ACP, Agent, Proxy, Supervisor                                                     int
	Foreground                                                                                int
	Prompts, Texts, Cancels, Ends, Canceled, Failures                                         int
	Exited, Restored                                                                          bool
	ExitCode                                                                                  int
	Profile, Endpoint                                                                         string
	Groups, PIDs                                                                              []int
	Attempts, MainHints, TitleHints, SummaryHints                                             int
	TitlePrompts, TitleEnds                                                                   int
}

func readTerminalTrace(root string) (terminalTrace, error) {
	return readTerminalTraceBounded(root, 64<<10, 512<<10)
}

// readTerminalTraceBounded reads the owned receipts within explicit per-file and total byte bounds;
// the soak mode raises them in proportion to its declared turn budget.
func readTerminalTraceBounded(root string, fileLimit, totalLimit int64) (terminalTrace, error) {
	var trace terminalTrace
	entries, err := os.ReadDir(filepath.Join(root, "events"))
	if err != nil || len(entries) > 32 {
		return trace, errors.New("terminal receipt directory")
	}
	total := 0
	groups, pids := map[int]bool{}, map[int]bool{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > fileLimit {
			return trace, errors.New("terminal receipt file")
		}
		data, err := os.ReadFile(filepath.Join(root, "events", entry.Name()))
		total += len(data)
		if err != nil || int64(total) > totalLimit {
			return trace, errors.New("terminal receipt bytes")
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines[:len(lines)-1] {
			var r terminalReceipt
			if json.Unmarshal([]byte(line), &r) != nil || r.PID <= 1 || r.Group <= 1 {
				return trace, errors.New("terminal receipt shape")
			}
			groups[r.Group], pids[r.PID] = true, true
			if r.Child > 1 {
				pids[r.Child] = true
			}
			switch r.Kind {
			case "history-input":
				trace.HistoryInputs++
			case "history-answer":
				trace.HistoryAnswers++
			case "answer-marker":
				if r.ModelSlot < 1 || r.ModelSlot > 2 {
					return trace, errors.New("unknown answer model")
				}
				if !r.TitleScope {
					if r.FollowScope {
						trace.ModelFollowMarkers++
					} else {
						trace.ModelFirstMarkers++
					}
				}
			case "model-ack", "prompt-model":
				if r.ModelSlot < 1 || r.ModelSlot > 2 {
					return trace, errors.New("unknown observed model slot")
				}
				if r.Kind == "model-ack" && r.ModelSlot == 2 {
					trace.ModelTargetAcks++
				}
				if r.Kind == "prompt-model" && !r.TitleScope {
					if r.FollowScope {
						trace.ModelFollowRecords++
						trace.ModelFollowSlot = r.ModelSlot
					} else {
						trace.ModelFirstRecords++
						trace.ModelFirstSlot = r.ModelSlot
					}
				}
			case "relay":
			case "relay-called":
				trace.RelayCalls++
			case "relay-result":
				trace.RelayResults++
			case "hook-held":
				trace.Hook, trace.HookHeld = r.PID, trace.HookHeld+1
			case "hook-released":
				trace.HookReleased++
			case "hook-interrupted":
				trace.HookInterrupted++
			case "hook-post":
				trace.HookPost++
			case "acp-auth-exit":
				trace.AuthExits++
			case "client":
				trace.Clients++
				trace.Client, trace.Foreground, trace.Profile, trace.Endpoint = r.PID, r.Foreground, r.Profile, r.Endpoint
			case "acp":
				trace.ACPs++
			case "agent":
				trace.Agent = r.Child
			case "supervisor":
				trace.Supervisor = r.PID
			case "proxy":
				trace.Proxy = r.Child
			case "proxy-exit":
				trace.Exited, trace.Restored, trace.ExitCode = true, r.Restored, r.Code
			case "prompt":
				if r.TitleScope {
					trace.TitlePrompts++
				} else if r.SoakScope > 0 {
					trace.SoakPrompts++
					trace.SoakACP = r.Group
				} else if r.RecoveryScope {
					trace.RecoveryPrompts++
					trace.RecoveryACP = r.Group
				} else if r.FollowScope {
					trace.FollowPrompts++
					trace.FollowACP = r.Group
				} else {
					trace.Prompts++
					trace.ACP = r.Group
				}
			case "prompt-attempt":
				if !r.TitleScope && r.FollowScope {
					trace.FollowOldInput, trace.FollowNewInput, trace.FollowPartial = r.OldInput, r.NewInput, r.PartialMarker
					trace.FollowNull, trace.FollowForm = r.NullMarker, r.InterruptedForm
				}
				trace.Attempts++
				if r.MainHint {
					trace.MainHints++
				}
				if r.TitleHint {
					trace.TitleHints++
				}
				if r.SummaryHint {
					trace.SummaryHints++
				}
			case "text":
				if !r.TitleScope && r.SoakScope > 0 {
					trace.SoakTexts++
				} else if !r.TitleScope && r.RecoveryScope {
					trace.RecoveryTexts++
				} else if !r.TitleScope && r.FollowScope {
					trace.FollowTexts++
				} else if !r.TitleScope {
					trace.Texts++
				}
			case "cancel":
				if !r.TitleScope && r.FollowScope {
					trace.FollowCancels++
				} else if !r.TitleScope {
					trace.Cancels++
				}
			case "end":
				if r.TitleScope {
					trace.TitleEnds++
				} else if r.SoakScope > 0 {
					trace.SoakEnds++
				} else if r.RecoveryScope {
					trace.RecoveryEnds++
				} else if r.FollowScope {
					trace.FollowEnds++
				} else {
					trace.Ends++
				}
			case "cancelled":
				if !r.TitleScope && r.FollowScope {
					trace.FollowCanceled++
				} else if !r.TitleScope {
					trace.Canceled++
				}
			case "failed-result":
				trace.PromptFailures++
			case "guard-failed":
				trace.Failures++
			default:
				return trace, errors.New("unknown terminal receipt")
			}
		}
	}
	for group := range groups {
		trace.Groups = append(trace.Groups, group)
	}
	for pid := range pids {
		trace.PIDs = append(trace.PIDs, pid)
	}
	return trace, nil
}

func TestCompiledRunKeyboardWithFakeACP(t *testing.T) {
	for _, mode := range []string{"natural-completion", "ordinary-key", "cancel"} {
		if !t.Run(mode, func(t *testing.T) { runCompiledTerminalStream(t, mode, "") }) {
			return
		}
	}
}

func TestKiroLiveCompiledRunKeyboardCancellation(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("explicit live terminal opt-in required")
	}
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" {
		t.Fatal("pinned live Kiro is required")
	}
	runCompiledTerminalStream(t, "cancel", kiro)
}

func TestCompiledRunHeldHookKeyboardWithFakeACP(t *testing.T) {
	for _, mode := range []string{"held-hook-release", "held-hook-exit"} {
		if !t.Run(mode, func(t *testing.T) { runCompiledTerminalStream(t, mode, "") }) {
			return
		}
	}
}

func TestKiroLiveCompiledRunHeldHookKeyboardExit(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("explicit live held-hook terminal opt-in required")
	}
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Fatal("both pinned executables are required")
	}
	runCompiledTerminalStream(t, "held-hook-exit", kiro)
}

func TestCompiledRunInterruptedFollowupWithFakeACP(t *testing.T) {
	runCompiledTerminalStream(t, "cancel-followup", "")
}

// One ordinary question completes, then the independent peer reproduces the measured logged-out
// boundary on the next question (D117). The client must show the gateway's login instruction as a
// normal completion, not an API error, while the original client and proxy stay live until exit.
// Many ordinary turns in one client session with the independent Kiro fixture (D120): the proxy's
// resident size, open descriptors and owned process count are sampled after every turn and must
// stay bounded after a short warm-up, with every turn admitted, answered and cleaned up.
func TestCompiledRunSoakTurnsWithFakeACP(t *testing.T) {
	runCompiledTerminalStream(t, "soak-turns", "")
}

// The same many-turn soak against the actual Kiro process, sampling its process group's resident
// size as well (D120). Every turn is one model call; the credit opt-in is explicit.
func TestKiroLiveCompiledRunSoakTurns(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("live soak requires explicit Kiro credit opt-in")
	}
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Fatal("both pinned executables are required")
	}
	runCompiledTerminalStream(t, "soak-turns", kiro)
}

type ownedProcessSample struct{ rssKB, fds, procs, acpRSSKB, acpProcs int }

// sampleOwnedProcess reads the owned proxy's resident size, open descriptor count and process-group
// size, and the backend process group's summed resident size and size, through finite system
// commands; a failed sample reads as zero and is visible in the log.
func sampleOwnedProcess(pid, acpGroup int) ownedProcessSample {
	var s ownedProcessSample
	run := func(name string, args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, name, args...).Output()
		return string(out)
	}
	s.rssKB, _ = strconv.Atoi(strings.TrimSpace(run("/bin/ps", "-o", "rss=", "-p", strconv.Itoa(pid))))
	s.fds = max(0, strings.Count(run("/usr/sbin/lsof", "-p", strconv.Itoa(pid)), "\n")-1)
	if group, err := syscall.Getpgid(pid); err == nil {
		s.procs = len(strings.Fields(run("/usr/bin/pgrep", "-g", strconv.Itoa(group))))
	}
	if acpGroup > 1 {
		pids := strings.Fields(run("/usr/bin/pgrep", "-g", strconv.Itoa(acpGroup)))
		s.acpProcs = len(pids)
		if len(pids) > 0 {
			for _, field := range strings.Fields(run("/bin/ps", "-o", "rss=", "-p", strings.Join(pids, ","))) {
				n, _ := strconv.Atoi(field)
				s.acpRSSKB += n
			}
		}
	}
	return s
}

func soakPrompt(i int) string {
	return fmt.Sprintf("Reply with the concatenation of Soak and _%d without spaces, and no other text.", i)
}

func TestCompiledRunAuthExpiryFollowupWithFakeACP(t *testing.T) {
	runCompiledTerminalStream(t, "auth-expiry-followup", "")
}

// The login is lost while the client holds a relayed tool call (D117): after the fixture exits at
// the measured boundary and the hook is released, the client must see the login completion, and
// once the login is marked restored the same session must answer one more question, typed again
// at most once if the stale pending outcome reports the expiry one more time.
func TestCompiledRunHeldHookAuthExpiryWithFakeACP(t *testing.T) {
	runCompiledTerminalStream(t, "held-hook-auth-expiry", "")
}

// Ctrl+C while the client holds a tool through its PreToolUse hook, then one new question in the
// same client session (D116). The hook must be interrupted, no tool result delivered, and the new
// question answered by a fresh ACP process while the original client/proxy stay live.
func TestCompiledRunHeldHookFollowupWithFakeACP(t *testing.T) {
	runCompiledTerminalStream(t, "held-hook-followup", "")
}

func TestCompiledRunModelSelectionWithFakeACP(t *testing.T) {
	for _, mode := range []string{"model-unchanged", "model-switch", "model-wide-switch"} {
		if !t.Run(mode, func(t *testing.T) { runCompiledTerminalStream(t, mode, "") }) {
			return
		}
	}
}

func TestKiroLiveCompiledRunModelSelection(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("explicit live model-selection opt-in required")
	}
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Fatal("both pinned executables required")
	}
	runCompiledTerminalStream(t, "model-switch", kiro)
}

func TestKiroLiveCompiledRunInterruptedFollowup(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("explicit live interrupted-followup opt-in required")
	}
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Fatal("both pinned executables are required")
	}
	runCompiledTerminalStream(t, "cancel-followup", kiro)
}

func runCompiledTerminalStream(t *testing.T, mode, kiro string) {
	runTerminalScenario(t, mode, kiro, nil)
}

func runTerminalScenario(t *testing.T, mode, kiro string, history *terminalHistoryPlan) terminalTrace {
	t.Helper()
	heldHook := strings.HasPrefix(mode, "held-hook-")
	heldFollowup := mode == "held-hook-followup"
	authExpiry := mode == "auth-expiry-followup"
	heldAuth := mode == "held-hook-auth-expiry"
	trustMode := mode == "trust-dialog"
	soakMode := mode == "soak-turns"
	soakTurns := 0
	if soakMode {
		soakTurns = 20
		if raw := os.Getenv("DAX_INTEROP_SOAK_TURNS"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 5 || n > 200 {
				t.Fatal("DAX_INTEROP_SOAK_TURNS must be between 5 and 200")
			}
			soakTurns = n
		}
	}
	var trustPlan *terminalHistoryPlan
	if history != nil && history.Trust {
		trustPlan, history = history, nil
	}
	if trustMode != (trustPlan != nil) {
		t.Fatal("trust-dialog mode requires its two-launch plan")
	}
	followup := mode == "cancel-followup" || heldFollowup
	modelCheck := strings.HasPrefix(mode, "model-")
	modelSlot := 1
	if strings.HasSuffix(mode, "switch") {
		modelSlot = 2
	}
	modelIDs := [2]string{"fixture-backend", "fixture-target"}
	modelLabel := []string{"independent first", "independent second"}[modelSlot-1]
	modelEntries := 2
	if mode == "model-wide-switch" {
		modelEntries = 19
	}
	var modelLabels []string
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for an owned compiled-run terminal")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("measured development platform only")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-keyboard-")
	if err != nil {
		t.Fatal("cannot prepare terminal root")
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	traceFileLimit, traceTotalLimit := int64(64<<10), int64(512<<10)
	captureLimit, terminalLifetime, scenarioLifetime := 256<<10, 70*time.Second, 2*time.Minute
	if soakMode {
		// Every turn redraws the screen and writes fixed-shape receipts; bound both in proportion.
		traceFileLimit, traceTotalLimit = 2<<20, 8<<20
		captureLimit += soakTurns * 16 << 10
		perTurn := time.Second
		if kiro != "" {
			perTurn = 15 * time.Second // an actual model turn
		}
		terminalLifetime += time.Duration(soakTurns) * perTurn
		scenarioLifetime += time.Duration(soakTurns) * perTurn
	}
	readTrace := func() (terminalTrace, error) { return readTerminalTraceBounded(root, traceFileLimit, traceTotalLimit) }
	home, project, bin, artifacts := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "bin"), filepath.Join(root, "runtime")
	if history != nil {
		home, project = history.Home, history.Project
	}
	if trustPlan != nil {
		home, project = trustPlan.Home, trustPlan.Project
	}
	secondLaunch := history != nil && history.Stage == 2 || trustPlan != nil && trustPlan.Stage == 2
	for _, dir := range []string{home, project, bin, artifacts, filepath.Join(root, "events"), filepath.Join(home, ".claude"), filepath.Join(root, "tmp")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			if secondLaunch && (dir == home || dir == project || dir == filepath.Join(home, ".claude")) && errors.Is(err, os.ErrExist) {
				if info, e := os.Lstat(dir); e == nil && info.IsDir() {
					continue
				}
			}
			t.Fatal("cannot prepare terminal fixture directory")
		}
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	settingsData := []byte(`{"disableAllHooks":true,"statusLine":{"type":"command","command":"/usr/bin/true"}}`)
	readFixture, prompt := filepath.Join(project, "read-fixture"), terminalStreamPrompt
	historyMarker := ""
	if history != nil {
		settingsData = []byte(`{"disableAllHooks":true,"autoMemoryEnabled":false,"statusLine":{"type":"command","command":"/usr/bin/true"}}`)
		prompt = "Remember UIHistoryFirst_101 " + history.Seed + " for later. Reply only with the concatenation of ArchiveUI and _101 without spaces."
		historyMarker = "ArchiveUI_101"
		if history.Stage == 2 {
			prompt = "UIHistoryNext_107: Reply only with the earlier remembered token, a space, and the concatenation of ArchiveUI and _107 without spaces."
			historyMarker = "ArchiveUI_107"
		}
	}
	if modelCheck {
		prompt = terminalModelFirst
	}
	if heldHook {
		prompt = "Use Read exactly once with file_path " + readFixture + ". Then reply with the concatenation of HookControl and _47 without spaces. Do not use any other tool."
		hooks := map[string]any{}
		for event, role := range map[string]string{"PreToolUse": "held-hook", "PostToolUse": "post-hook"} {
			hooks[event] = []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": "exec " + probeShellQuote(filepath.Join(bin, role)), "timeout": 25}}}}
		}
		settingsData, _ = json.Marshal(map[string]any{"hooks": hooks, "statusLine": map[string]string{"type": "command", "command": "/usr/bin/true"}})
		if os.WriteFile(readFixture, []byte("OwnedHookRead_47\n"), 0600) != nil {
			t.Fatal("cannot prepare owned Read fixture")
		}
	}
	// The trust-dialog mode seeds a richer global file so the write-back's other bytes are checked.
	const trustSeed = "{\n  \"numStartups\": 3,\n  \"hasCompletedOnboarding\": true,\n  \"projects\": {\n    \"/owned/other\": {\n      \"hasTrustDialogAccepted\": true,\n      \"allowedTools\": []\n    }\n  },\n  \"mcpServers\": {}\n}\n"
	// Ordinary modes launch a project the user trusted natively, carried by the projection (D64); the
	// trust-dialog mode leaves the project out so the client asks.
	globalSeed, _ := json.Marshal(map[string]any{"projects": map[string]any{project: map[string]bool{"hasTrustDialogAccepted": true}}})
	if trustMode {
		globalSeed = []byte(trustSeed)
	}
	if !secondLaunch {
		if os.WriteFile(settings, settingsData, 0600) != nil || os.WriteFile(filepath.Join(home, ".claude.json"), globalSeed, 0600) != nil {
			t.Fatal("cannot prepare owned terminal settings")
		}
	}
	beforeSettings, beforeGlobal := fileFingerprint(t, settings), fileFingerprint(t, filepath.Join(home, ".claude.json"))
	ctx, stop := context.WithTimeout(t.Context(), scenarioLifetime)
	defer stop()
	runner, err := childproc.New(childproc.Config{Timeout: 45 * time.Second, MaxOutputBytes: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	if modelCheck {
		var c *catalog.Catalog
		if kiro != "" {
			c = terminalDiscoverModels(t, ctx, runner, kiro, root)
		} else {
			rows := []catalog.Backend{{ID: modelIDs[0], Name: "Independent first"}, {ID: modelIDs[1], Name: "Independent second"}}
			for i := 2; i < modelEntries; i++ {
				rows = append(rows, catalog.Backend{ID: fmt.Sprintf("fixture-spare-%02d", i), Name: fmt.Sprintf("Independent unused model %02d", i)})
			}
			c, err = catalog.New(rows, modelIDs[0])
			if err != nil {
				t.Fatal("independent model catalog")
			}
		}
		modelIDs, modelLabels, modelLabel, err = terminalModelPlan(c)
		if err != nil {
			t.Fatal("current catalog cannot identify an unambiguous model pair")
		}
		if modelSlot == 1 {
			for _, row := range c.List() {
				backend, _ := c.Resolve(row.ID)
				if backend.ID == modelIDs[0] {
					modelLabel = strings.ToLower(row.Name)
				}
			}
		}
	}
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	proxy, peer := filepath.Join(bin, "proxy"), filepath.Join(bin, "peer")
	for _, build := range [][2]string{{proxy, "../../cmd/dax-kiro-proxy"}, {peer, "./testdata/terminalpeer"}} {
		if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", build[0], build[1]}}); err != nil {
			t.Fatal("cannot build owned terminal executable")
		}
	}
	for _, name := range []string{"supervisor", "claude", "kiro-cli", "kiro-cli-chat", "held-hook", "post-hook"} {
		if os.Link(peer, filepath.Join(bin, name)) != nil {
			t.Fatal("cannot create owned executable role")
		}
	}
	stateDirectory := filepath.Join(root, "state")
	if history != nil && history.State != "" {
		stateDirectory = history.State
	}
	args := []string{"run", "--client", filepath.Join(bin, "claude"), "--kiro", filepath.Join(bin, "kiro-cli"), "--settings", settings, "--runtime-dir", artifacts, "--state-dir", stateDirectory}
	historyStage, historySeed, historyID, historyName := 0, "", "", ""
	if history != nil {
		historyStage, historySeed, historyID, historyName = history.Stage, history.Seed, history.ID, history.Name
		if history.Stage == 1 || history.Picker {
			args = append(args, "--client-history")
		} else {
			args = append(args, "--resume", history.ID)
		}
	}
	config, _ := json.Marshal(map[string]any{"Root": root, "Proxy": proxy, "Client": client, "Kiro": kiro, "AccountHome": os.Getenv("HOME"), "Project": project, "Args": args, "HeldHook": heldHook, "AuthExpiry": authExpiry || heldAuth, "AuthCut": heldAuth, "SoakTurns": soakTurns, "AllowFollowup": followup || authExpiry || modelCheck, "ModelCheck": modelCheck, "ModelIDs": modelIDs, "ModelEntries": modelEntries, "HistoryStage": historyStage, "HistorySeed": historySeed, "HistoryID": historyID, "HistoryName": historyName})
	if os.WriteFile(filepath.Join(bin, "terminal.json"), config, 0600) != nil {
		t.Fatal("cannot write terminal role configuration")
	}
	var trace terminalTrace
	observedGroups := make(map[int]bool)
	defer func() {
		if final, err := readTrace(); err == nil {
			for _, group := range final.Groups {
				observedGroups[group] = true
			}
		}
		var killed []int
		for group := range observedGroups {
			if group > 1 && group != syscall.Getpgrp() && !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
				t.Error("owned terminal group required emergency cleanup")
				_ = syscall.Kill(-group, syscall.SIGKILL)
				killed = append(killed, group)
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for len(killed) > 0 && time.Now().Before(deadline) {
			remaining := killed[:0]
			for _, group := range killed {
				if !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
					remaining = append(remaining, group)
				}
			}
			killed = remaining
			if len(killed) > 0 {
				time.Sleep(25 * time.Millisecond)
			}
		}
		if len(killed) > 0 {
			t.Error("owned terminal group remained after emergency cleanup deadline")
		}
	}()
	owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: terminalLifetime})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	command := childproc.Command{Executable: "/usr/bin/script", Directory: project, Args: []string{"-q", os.DevNull, filepath.Join(bin, "supervisor")}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + filepath.Join(root, "tmp"), "TERM=xterm-256color", "COLUMNS=160", "LINES=40"}}
	stage, beforeKey := 0, 0
	var keyAt time.Time
	var heldAt time.Time
	var heldObserved, hookAtConfirmation bool
	var originalClient, originalProxy int
	var originalProfile, originalEndpoint string
	var followupObserved, authFallbackObserved, apiErrorVisible, recoveryObserved bool
	var followupAt, recoveryAt time.Time
	groupsBeforeRecovery := make(map[int]bool)
	loginMessages, recoveryAttempts := 0, 0
	var trustDialogSeen, trustAnswered bool
	var trustActions [3]int // up, down, confirmed yes
	var trustKeyAt time.Time
	soakIndex := 0
	var soakAt time.Time
	var soakSamples []ownedProcessSample
	var historyLoaded, historyAnswered bool
	var historyPickerSeen, historyPicked bool
	var historyPickerAt time.Time
	var historyPickerHints uint32
	var modelObserved, modelPickerObserved bool
	modelMenu := terminalModelMenu{labels: modelLabels, target: modelLabel}
	var modelLastMove time.Time
	var modelStage14At time.Time
	menuScreenDumped, confirmScreenDumped := false, false
	boundedScreen := func(screen string) string {
		var kept []string
		for _, line := range strings.Split(screen, "\n") {
			if line = strings.TrimRight(line, " "); line != "" {
				kept = append(kept, line)
			}
		}
		joined := strings.Join(kept, "\n")
		return joined[:min(len(joined), 2500)]
	}
	var modelMenuFacts string
	var modelScreenHints uint32
	groupsBeforeFollowup := make(map[int]bool)
	var receiptErr error
	var canceledAlive, foregroundObserved, exitKey bool
	var screenHints uint32
	result, setup, runErr := runObservedTerminalLimited(ctx, owner, command, nil, func(screen string) string {
		var err error
		trace, err = readTrace()
		if err != nil {
			receiptErr = err
			return ""
		}
		for _, group := range trace.Groups {
			observedGroups[group] = true
		}
		lower := strings.ToLower(strings.Join(strings.Fields(screen), " "))
		if modelCheck {
			for i, marker := range []string{"select model", "independent first", "independent second", "set model to", "model changed", "modelsecond_67"} {
				if strings.Contains(lower, marker) {
					modelScreenHints |= 1 << i
				}
			}
		}
		if stage >= 3 {
			for i, marker := range []string{"exit", "again", "ctrl", "interrupted", "thinking", "esc to cancel", "do you want", "continue"} {
				if strings.Contains(lower, marker) {
					screenHints |= 1 << i
				}
			}
		}
		if trace.Failures > 0 || trace.PromptFailures > 0 {
			stop()
			return ""
		}
		switch stage {
		case 0:
			// The client's own workspace-trust dialog: move to "Yes, I trust this folder" and confirm
			// only after a fresh render shows the selection there (D118).
			if trustMode && strings.Contains(lower, "trust this folder") && strings.Contains(lower, "no, exit") {
				trustDialogSeen = true
				if time.Since(trustKeyAt) < 400*time.Millisecond {
					return ""
				}
				key := terminalTrustChoiceKey(screen)
				if key == "" {
					return ""
				}
				trustKeyAt = time.Now()
				switch key {
				case "\x1b[A":
					trustActions[0]++
				case "\x1b[B":
					trustActions[1]++
				case "\r":
					trustActions[2]++
					trustAnswered = true
				}
				return key
			}
			if trace.Client > 1 && trace.Foreground == trace.Client && statusProjectVisible(lower, project) && strings.Contains(screen, "❯") && !strings.Contains(lower, "do you want") && !strings.Contains(lower, "enter to continue") {
				if history != nil && history.Stage == 2 && history.Picker && !historyPicked {
					stage = 20
					return "/resume"
				}
				if history != nil && history.Stage == 2 && !strings.Contains(screen, "ArchiveUI_101") {
					return ""
				}
				historyLoaded = history != nil && history.Stage == 2
				foregroundObserved = true
				originalClient, originalProxy, originalProfile, originalEndpoint = trace.Client, trace.Proxy, trace.Profile, trace.Endpoint
				stage = 1
				return prompt
			}
		case 1:
			if strings.Contains(strings.Join(strings.Fields(screen), " "), prompt) {
				stage = 2
				return "\r"
			}
		case 2:
			if history != nil {
				if trace.HistoryInputs == 1 && trace.HistoryAnswers == 1 && trace.Ends == 1 && strings.Contains(screen, historyMarker) && (history.Stage == 1 || strings.Contains(screen, history.Seed)) {
					historyAnswered = true
					stage, exitKey = 5, true
					return "\x04"
				}
			} else if modelCheck {
				if trace.Prompts == 1 && trace.Ends == 1 && trace.ModelFirstRecords == 1 && trace.ModelFirstSlot == 1 && strings.Contains(screen, "ModelFirst_61") {
					stage = 10
					return "/model"
				}
			} else if heldHook {
				if terminalHookEligible(trace) && (kiro != "" || trace.RelayCalls == 1 && trace.RelayResults == 0) && syscall.Kill(trace.Hook, 0) == nil && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil {
					if heldAt.IsZero() {
						heldAt = time.Now()
					}
					if time.Since(heldAt) < 500*time.Millisecond {
						return ""
					}
					heldObserved = true
					if heldAuth {
						if os.WriteFile(filepath.Join(root, "auth-cut"), []byte("owned-auth-cut"), 0600) != nil {
							receiptErr = errors.New("cannot cut owned authentication")
							stop()
							return ""
						}
						stage, keyAt = 40, time.Now()
						return ""
					}
					if heldFollowup {
						stage, keyAt = 3, time.Now()
						return "\x03"
					}
					if mode == "held-hook-release" {
						if os.WriteFile(filepath.Join(root, "hook-release"), []byte("release-owned-hook"), 0600) != nil {
							receiptErr = errors.New("cannot release owned hook")
							stop()
						}
						stage = 7
						return ""
					}
					stage, exitKey, keyAt = 5, true, time.Now()
					return "\x04"
				}
			} else if authExpiry {
				if trace.Ends == 1 {
					if os.WriteFile(filepath.Join(root, "followup-allowed"), []byte("owned-new-question"), 0600) != nil {
						receiptErr = errors.New("cannot admit owned new question")
						stop()
						return ""
					}
					for _, group := range trace.Groups {
						groupsBeforeFollowup[group] = true
					}
					stage, followupAt = 8, time.Now()
					return terminalFollowupPrompt
				}
			} else if soakMode {
				if trace.Ends == 1 {
					soakSamples = append(soakSamples, sampleOwnedProcess(trace.Proxy, trace.ACP))
					soakIndex = 1
					if os.WriteFile(filepath.Join(root, "soak-allowed"), []byte("1"), 0600) != nil {
						receiptErr = errors.New("cannot admit owned soak question")
						stop()
						return ""
					}
					stage, soakAt = 50, time.Now()
					return soakPrompt(soakIndex)
				}
			} else if mode == "natural-completion" || trustMode {
				if trace.Ends == 1 {
					stage = 5
					exitKey = true
					return "\x04"
				}
			} else if terminalCancelEligible(trace) && strings.Contains(screen, "Ready_47") {
				beforeKey = trace.Texts
				keyAt = time.Now()
				stage = 3
				if mode == "cancel" || followup {
					return "\x03"
				}
				return "x"
			}
		case 3:
			if mode == "ordinary-key" {
				if trace.Ends == 1 && trace.Texts > beforeKey && trace.Cancels == 0 {
					stage = 4
					return "\x15"
				}
			} else if heldFollowup {
				// The completed HTTP tool handoff leaves nothing for the client to cancel; only the
				// interrupted hook and the returned prompt show the interruption before the new question.
				if trace.HookInterrupted == 1 && trace.HookReleased == 0 && trace.RelayResults == 0 && strings.Contains(screen, "❯") && time.Since(keyAt) > 500*time.Millisecond {
					canceledAlive = trace.Client > 1 && trace.Proxy > 1 && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil && time.Since(keyAt) < 8*time.Second
					if !canceledAlive || trace.Client != originalClient || trace.Proxy != originalProxy || trace.Profile != originalProfile || trace.Endpoint != originalEndpoint {
						receiptErr = errors.New("original foreground owner absent after hook interruption")
						stop()
						return ""
					}
					if os.WriteFile(filepath.Join(root, "followup-allowed"), []byte("owned-new-question"), 0600) != nil {
						receiptErr = errors.New("cannot admit owned new question")
						stop()
						return ""
					}
					for _, group := range trace.Groups {
						groupsBeforeFollowup[group] = true
					}
					stage = 8
					return terminalFollowupPrompt
				}
			} else if trace.Cancels > 0 && trace.ACP > 1 && errors.Is(syscall.Kill(-trace.ACP, 0), syscall.ESRCH) && strings.Contains(screen, "❯") {
				canceledAlive = trace.Client > 1 && trace.Proxy > 1 && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil && time.Since(keyAt) < 8*time.Second
				if followup {
					if !canceledAlive || trace.Client != originalClient || trace.Proxy != originalProxy || trace.Profile != originalProfile || trace.Endpoint != originalEndpoint {
						receiptErr = errors.New("original foreground owner absent after interruption")
						stop()
						return ""
					}
					if os.WriteFile(filepath.Join(root, "followup-allowed"), []byte("owned-new-question"), 0600) != nil {
						receiptErr = errors.New("cannot admit owned new question")
						stop()
						return ""
					}
					for _, group := range trace.Groups {
						groupsBeforeFollowup[group] = true
					}
					stage = 8
					return terminalFollowupPrompt
				}
				stage = 5
				exitKey = true
				return "\x04"
			}
		case 4:
			stage = 5
			exitKey = true
			return "\x04"
		case 5:
			if terminalExitConfirmation(screen) {
				hookAtConfirmation = trace.Hook > 1 && syscall.Kill(trace.Hook, 0) == nil && trace.HookReleased == 0
				stage = 6
				return "\x04"
			}
		case 7:
			if trace.HookReleased == 1 && trace.HookPost == 1 && trace.RelayResults == 1 && trace.Ends == 1 && strings.Contains(screen, "HookControl_47") {
				stage, exitKey = 5, true
				return "\x04"
			}
		case 8:
			if strings.Contains(strings.Join(strings.Fields(screen), " "), terminalFollowupPrompt) {
				stage = 9
				return "\r"
			}
		case 9:
			if authExpiry {
				if time.Since(followupAt) > 20*time.Second {
					receiptErr = errors.New("login fallback not observed")
					stop()
					return ""
				}
				if trace.FollowPrompts == 1 && trace.AuthExits == 1 && strings.Contains(lower, "kiro authentication expired") && strings.Contains(lower, "kiro-cli login") && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil && trace.Client == originalClient && trace.Proxy == originalProxy {
					authFallbackObserved = true
					apiErrorVisible = strings.Contains(lower, "api error") || strings.Contains(lower, "api_error")
					// The login is "restored": the fixture answers again, and the same client session
					// must carry one more ordinary question through a fresh ACP process.
					if os.WriteFile(filepath.Join(root, "recovery-allowed"), []byte("owned-recovery-question"), 0600) != nil {
						receiptErr = errors.New("cannot admit owned recovery question")
						stop()
						return ""
					}
					for _, group := range trace.Groups {
						groupsBeforeRecovery[group] = true
					}
					stage, recoveryAt = 30, time.Now()
					return terminalRecoveryPrompt
				}
				return ""
			}
			complete := terminalFollowupComplete(trace)
			if heldFollowup {
				complete = terminalHeldFollowupComplete(trace)
			}
			if complete && !groupsBeforeFollowup[trace.FollowACP] && strings.Contains(screen, "Follow_49") && trace.Client == originalClient && trace.Proxy == originalProxy && trace.Profile == originalProfile && trace.Endpoint == originalEndpoint && syscall.Kill(originalClient, 0) == nil && syscall.Kill(originalProxy, 0) == nil && errors.Is(syscall.Kill(-trace.ACP, 0), syscall.ESRCH) {
				followupObserved = true
				stage, exitKey = 5, true
				return "\x04"
			}
		case 50:
			if strings.Contains(strings.Join(strings.Fields(screen), " "), soakPrompt(soakIndex)) {
				stage = 51
				return "\r"
			}
		case 51:
			if time.Since(soakAt) > 45*time.Second {
				receiptErr = fmt.Errorf("soak turn %d not completed", soakIndex)
				stop()
				return ""
			}
			if trace.SoakEnds >= soakIndex && trace.SoakPrompts >= soakIndex && strings.Contains(screen, "Soak_"+strconv.Itoa(soakIndex)) && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil {
				soakSamples = append(soakSamples, sampleOwnedProcess(trace.Proxy, trace.SoakACP))
				if soakIndex >= soakTurns {
					stage, exitKey = 5, true
					return "\x04"
				}
				soakIndex++
				if os.WriteFile(filepath.Join(root, "soak-allowed"), []byte(strconv.Itoa(soakIndex)), 0600) != nil {
					receiptErr = errors.New("cannot admit owned soak question")
					stop()
					return ""
				}
				stage, soakAt = 50, time.Now()
				return soakPrompt(soakIndex)
			}
		case 40:
			if time.Since(keyAt) > 10*time.Second {
				receiptErr = errors.New("authentication cut not observed by the fixture")
				stop()
				return ""
			}
			if trace.AuthExits == 1 {
				if os.WriteFile(filepath.Join(root, "hook-release"), []byte("release-owned-hook"), 0600) != nil {
					receiptErr = errors.New("cannot release owned hook")
					stop()
					return ""
				}
				stage, keyAt = 41, time.Now()
			}
		case 41:
			if time.Since(keyAt) > 25*time.Second {
				receiptErr = errors.New("login fallback after the held tool not observed")
				stop()
				return ""
			}
			if trace.HookReleased == 1 && strings.Contains(lower, "kiro authentication expired") && strings.Contains(lower, "kiro-cli login") && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil && trace.Client == originalClient && trace.Proxy == originalProxy {
				authFallbackObserved = true
				apiErrorVisible = strings.Contains(lower, "api error") || strings.Contains(lower, "api_error")
				if apiErrorVisible {
					t.Logf("fallback_screen=%q", boundedScreen(screen))
				}
				loginMessages = strings.Count(lower, "kiro authentication expired")
				if os.WriteFile(filepath.Join(root, "recovery-allowed"), []byte("owned-recovery-question"), 0600) != nil {
					receiptErr = errors.New("cannot admit owned recovery question")
					stop()
					return ""
				}
				for _, group := range trace.Groups {
					groupsBeforeRecovery[group] = true
				}
				stage, recoveryAt = 30, time.Now()
				return terminalRecoveryPrompt
			}
		case 30:
			if strings.Contains(strings.Join(strings.Fields(screen), " "), terminalRecoveryPrompt) {
				stage = 31
				return "\r"
			}
		case 31:
			if time.Since(recoveryAt) > 25*time.Second {
				receiptErr = errors.New("recovery after restored login not observed")
				stop()
				return ""
			}
			// A stale pending outcome may report the expiry once more for the first question after
			// the login is restored; the observer retypes the question at most once and records it.
			if heldAuth && trace.RecoveryPrompts == 0 && strings.Count(lower, "kiro authentication expired") > loginMessages && recoveryAttempts == 0 && time.Since(recoveryAt) > time.Second {
				recoveryAttempts++
				loginMessages = strings.Count(lower, "kiro authentication expired")
				stage, recoveryAt = 30, time.Now()
				return terminalRecoveryPrompt
			}
			if trace.RecoveryPrompts == 1 && trace.RecoveryEnds == 1 && trace.RecoveryTexts > 0 && strings.Contains(screen, "Recovered_53") && !groupsBeforeRecovery[trace.RecoveryACP] && trace.Client == originalClient && trace.Proxy == originalProxy && trace.Profile == originalProfile && trace.Endpoint == originalEndpoint && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil {
				recoveryObserved = true
				stage, exitKey = 5, true
				return "\x04"
			}
		case 10:
			if strings.Contains(screen, "/model") {
				stage = 11
				return "\r"
			}
		case 11:
			key := modelMenu.next(screen, time.Now())
			facts := fmt.Sprintf("actions=%d header=%v footer=%v glyphs=%d recognized_selected_labels=%d currently_visible_labels=%d focused_labels=%d", modelMenu.moves, modelMenu.lastHeader, modelMenu.lastFooter, modelMenu.lastGlyphs, modelMenu.lastKnown, modelMenu.lastVisible, len(modelMenu.focused))
			if facts != modelMenuFacts {
				t.Log("model_menu " + facts)
				modelMenuFacts = facts
			}
			// Bounded excerpt of the synthetic picker screen when its selection glyph is missing.
			if !menuScreenDumped && modelMenu.lastHeader && modelMenu.lastGlyphs == 0 {
				menuScreenDumped = true
				t.Logf("model_menu_screen=%q", boundedScreen(screen))
			}
			if key != "" || modelLastMove.IsZero() {
				modelLastMove = time.Now()
			}
			if time.Since(modelLastMove) > 5*time.Second {
				receiptErr = errors.New("model menu observation stalled")
				stop()
				return ""
			}
			modelPickerObserved = modelMenu.covered()
			if key == "\r" {
				stage, modelStage14At = 14, time.Now()
			}
			return key
		case 14:
			if !confirmScreenDumped && time.Since(modelStage14At) > 3*time.Second {
				confirmScreenDumped = true
				t.Logf("model_confirmation_screen=%q", boundedScreen(screen))
			}
			if time.Since(modelStage14At) > 15*time.Second {
				receiptErr = errors.New("model selection confirmation not observed")
				stop()
				return ""
			}
			if strings.Contains(lower, "set model to") && !strings.Contains(lower, modelLabel) {
				receiptErr = errors.New("model picker confirmed a different row")
				stop()
				return ""
			}
			if strings.Contains(lower, "set model to") && strings.Contains(lower, modelLabel) {
				if os.WriteFile(filepath.Join(root, "followup-allowed"), []byte("owned-new-question"), 0600) != nil {
					receiptErr = errors.New("cannot admit post-selection question")
					stop()
					return ""
				}
				stage = 15
				return terminalModelSecond
			}
		case 15:
			if strings.Contains(strings.Join(strings.Fields(screen), " "), terminalModelSecond) {
				stage = 16
				return "\r"
			}
		case 16:
			if terminalModelComplete(trace, modelSlot) && strings.Contains(screen, "ModelSecond_67") && trace.Client == originalClient && trace.Proxy == originalProxy && trace.Profile == originalProfile && trace.Endpoint == originalEndpoint && syscall.Kill(originalClient, 0) == nil && syscall.Kill(originalProxy, 0) == nil {
				modelObserved = true
				stage, exitKey = 5, true
				return "\x04"
			}
		case 20:
			if strings.Contains(screen, "/resume") {
				stage, historyPickerAt = 21, time.Now()
				return "\r"
			}
		case 21:
			for i, marker := range []string{"resume session", "session", "search", "enter", "conversation", "no conversations", strings.ToLower(history.Name)} {
				if strings.Contains(lower, marker) {
					historyPickerHints |= 1 << i
				}
			}
			if terminalHistoryPickerSelected(screen, history.Name) {
				historyPickerSeen = true
				stage = 22
				return "\r"
			}
			if time.Since(historyPickerAt) > 10*time.Second {
				receiptErr = errors.New("native history picker selection not observed")
				stop()
			}
		case 22:
			if strings.Contains(screen, "ArchiveUI_101") && strings.Contains(screen, "❯") {
				historyPicked = true
				stage = 0
			}
		}
		return ""
	}, true, captureLimit)
	trace, err = readTrace()
	exitLatency := time.Duration(0)
	if !keyAt.IsZero() {
		exitLatency = time.Since(keyAt)
	}
	groupsGone := true
	for _, pid := range trace.Groups {
		groupsGone = groupsGone && pid > 1 && errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
	}
	pidsGone := len(trace.PIDs) > 0
	for _, pid := range trace.PIDs {
		pidsGone = pidsGone && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}
	entries, artifactErr := os.ReadDir(artifacts)
	_, profileErr := os.Lstat(trace.Profile)
	endpoint, parseErr := url.Parse(trace.Endpoint)
	listenerGone := false
	if parseErr == nil && endpoint.Host != "" {
		conn, dialErr := net.DialTimeout("tcp", endpoint.Host, 100*time.Millisecond)
		listenerGone = dialErr != nil
		if conn != nil {
			conn.Close()
		}
	}
	globalUnchanged := beforeGlobal == fileFingerprint(t, filepath.Join(home, ".claude.json"))
	sources := beforeSettings == fileFingerprint(t, settings) && globalUnchanged
	trustWritten := false
	if trustMode {
		after, readErr := os.ReadFile(filepath.Join(home, ".claude.json"))
		// The write-back must add exactly the client's own key for this project and nothing else.
		var fields struct {
			Projects map[string]struct {
				Trusted bool `json:"hasTrustDialogAccepted"`
			} `json:"projects"`
		}
		if readErr == nil && json.Unmarshal(after, &fields) == nil {
			for key, entry := range fields.Projects {
				resolved, resolveErr := filepath.EvalSymlinks(key)
				if !entry.Trusted || key == "/owned/other" || resolveErr != nil || resolved != project {
					continue
				}
				encoded, _ := json.Marshal(key)
				fragment := string(encoded) + `:{"hasTrustDialogAccepted":true},`
				trustWritten = strings.Count(string(after), fragment) == 1 && strings.Replace(string(after), fragment, "", 1) == trustSeed
			}
		}
		if trustPlan.Stage == 1 {
			sources = beforeSettings == fileFingerprint(t, settings) && trustWritten
		}
		t.Logf("trust_stage=%d trust_dialog_seen=%v trust_answered=%v trust_written=%v global_unchanged=%v trust_actions=%v", trustPlan.Stage, trustDialogSeen, trustAnswered, trustWritten, globalUnchanged, trustActions)
	}
	modelRestored := false
	if modelCheck && modelObserved && groupsGone && pidsGone {
		before := trace
		inspection, inspectErr := runner.Run(ctx, childproc.Command{Executable: proxy, Directory: project, Environment: command.Environment, Args: []string{"doctor", "--json", "--kiro", filepath.Join(bin, "kiro-cli"), "--client", filepath.Join(bin, "claude"), "--settings", settings, "--runtime-dir", artifacts, "--state-dir", filepath.Join(root, "state")}})
		var report struct {
			SelectedModel string `json:"selected_model"`
		}
		models, modelErr := catalog.New([]catalog.Backend{{ID: modelIDs[0]}, {ID: modelIDs[1]}}, modelIDs[0])
		if modelErr != nil {
			t.Fatal("independent model expectation")
		}
		expected, _ := models.ClientID(modelIDs[modelSlot-1])
		after, afterErr := readTrace()
		modelRestored = inspectErr == nil && json.Unmarshal(inspection.Stdout, &report) == nil && report.SelectedModel == expected && afterErr == nil && after.Clients == before.Clients && after.ACPs == before.ACPs && len(after.PIDs) == len(before.PIDs) && len(after.Groups) == len(before.Groups) && after.Attempts == before.Attempts
		sources = sources && beforeSettings == fileFingerprint(t, settings) && beforeGlobal == fileFingerprint(t, filepath.Join(home, ".claude.json"))
	}
	lateReleaseQuiet := true
	if heldHook {
		content, readErr := os.ReadFile(readFixture)
		sources = sources && readErr == nil && string(content) == "OwnedHookRead_47\n"
		if mode == "held-hook-exit" && groupsGone && pidsGone {
			if os.WriteFile(filepath.Join(root, "hook-release"), []byte("release-owned-hook"), 0600) != nil {
				lateReleaseQuiet = false
			}
			time.Sleep(300 * time.Millisecond)
			after, err := readTrace()
			lateReleaseQuiet = lateReleaseQuiet && err == nil && after.HookReleased == 0 && after.HookPost == 0 && after.RelayResults == 0 && after.Prompts == trace.Prompts && after.TitlePrompts == trace.TitlePrompts && after.Ends == 0 && after.Failures == 0 && after.PromptFailures == 0
		}
		t.Logf("held_observed=%v hook_held=%d hook_released=%d hook_interrupted=%d hook_post=%d hook_alive_at_exit_confirmation=%v relay_calls=%d relay_results=%d late_release_quiet=%v exit_ms=%d", heldObserved, trace.HookHeld, trace.HookReleased, trace.HookInterrupted, trace.HookPost, hookAtConfirmation, trace.RelayCalls, trace.RelayResults, lateReleaseQuiet, exitLatency.Milliseconds())
	}
	t.Logf("non_success_prompt_results=%d", trace.PromptFailures)
	if modelCheck {
		t.Logf("unadvertised_navigation_row_frames=%d", modelMenu.unadvertisedRows)
		t.Logf("model_catalog_entries=%d rendered_labels=%d focused_labels=%d menu_actions=%d bounded_reversals=%d first_correlated_markers=%d next_correlated_markers=%d", len(modelLabels), len(modelMenu.seen), len(modelMenu.focused), modelMenu.moves, modelMenu.reversals, trace.ModelFirstMarkers, trace.ModelFollowMarkers)
		t.Logf("next_preflight_restored_model_without_client_or_acp=%v", modelRestored)
		t.Logf("model_picker_observed=%v model_transition_observed=%v model_screen_hint_bits=%d first_model_records=%d first_model_slot=%d next_model_records=%d next_model_slot=%d target_acknowledgements=%d", modelPickerObserved, modelObserved, modelScreenHints, trace.ModelFirstRecords, trace.ModelFirstSlot, trace.ModelFollowRecords, trace.ModelFollowSlot, trace.ModelTargetAcks)
	}
	t.Logf("fixed_screen_hint_bits=%d observed_groups=%d observed_pids=%d", screenHints, len(trace.Groups), len(trace.PIDs))
	if followup {
		t.Logf("absent_marker_detected=%v followup_group_previously_observed=%v", trace.FollowNull, groupsBeforeFollowup[trace.FollowACP])
		t.Logf("followup_observed=%v clients=%d follow_prompts=%d follow_texts=%d follow_ends=%d follow_cancels=%d follow_cancelled_replies=%d distinct_acp=%v old_input_preserved=%v new_input_preserved=%v partial_marker_present=%v same_client=%v same_proxy=%v same_profile=%v same_listener=%v", followupObserved, trace.Clients, trace.FollowPrompts, trace.FollowTexts, trace.FollowEnds, trace.FollowCancels, trace.FollowCanceled, trace.FollowACP > 1 && trace.FollowACP != trace.ACP, trace.FollowOldInput, trace.FollowNewInput, trace.FollowPartial, trace.Client == originalClient, trace.Proxy == originalProxy, trace.Profile == originalProfile, trace.Endpoint == originalEndpoint)
	}
	t.Logf("prompt_attempts=%d main_hints=%d title_hints=%d summary_hints=%d title_prompts=%d title_ends=%d", trace.Attempts, trace.MainHints, trace.TitleHints, trace.SummaryHints, trace.TitlePrompts, trace.TitleEnds)
	t.Logf("live_kiro=%v mode=%s stage=%d setup=%d prompts=%d texts=%d cancels=%d ends=%d cancelled_replies=%d guard_failures=%d key_after_texts=%d foreground_observed=%v client_alive_after_cancel=%v keyboard_exit=%v proxy_exited=%v proxy_exit=%d terminal_restored=%v groups_gone=%v recorded_pids_gone=%v listener_gone=%v runtime_removed=%v profile_removed=%v sources_unchanged=%v output_bytes=%d command_exit=%d", kiro != "", mode, stage, setup, trace.Prompts, trace.Texts, trace.Cancels, trace.Ends, trace.Canceled, trace.Failures, beforeKey, foregroundObserved, canceledAlive, exitKey, trace.Exited, trace.ExitCode, trace.Restored, groupsGone, pidsGone, listenerGone, artifactErr == nil && len(entries) == 0, os.IsNotExist(profileErr), sources, len(result.Stdout), result.ExitCode)
	titleLimit := 1
	if followup || modelCheck {
		titleLimit = 2
	}
	if authExpiry || heldAuth {
		titleLimit = 3
	}
	if soakMode {
		titleLimit = soakTurns + 2
	}
	valid := runErr == nil && result.ExitCode == 0 && receiptErr == nil && err == nil && foregroundObserved && exitKey && trace.Exited && trace.ExitCode == 0 && trace.Restored && groupsGone && pidsGone && listenerGone && artifactErr == nil && len(entries) == 0 && os.IsNotExist(profileErr) && sources && trace.Prompts == 1 && trace.TitlePrompts <= titleLimit && trace.Failures == 0 && trace.PromptFailures == 0
	if history != nil {
		t.Logf("native_history_stage=%d previous_answer_visible_before_input=%v active_answer_observed=%v history_inputs=%d history_answers=%d", history.Stage, historyLoaded, historyAnswered, trace.HistoryInputs, trace.HistoryAnswers)
		valid = valid && historyAnswered && (history.Stage == 1 || historyLoaded)
		if history.Picker && history.Stage == 2 {
			t.Logf("native_history_picker_selected=%v history_loaded_after_selection=%v fixed_picker_hint_bits=%d", historyPickerSeen, historyPicked, historyPickerHints)
			valid = valid && historyPickerSeen && historyPicked
		}
	}
	if modelCheck {
		valid = valid && modelPickerObserved && modelObserved && modelRestored && trace.FollowEnds == 1
	} else if mode == "held-hook-exit" {
		valid = valid && heldObserved && trace.HookReleased == 0 && trace.HookPost == 0 && trace.RelayResults == 0 && trace.Ends == 0 && !strings.Contains(string(result.Stdout), "OwnedHookRead_47") && exitLatency < 8*time.Second && lateReleaseQuiet
	} else if mode == "held-hook-release" {
		valid = valid && heldObserved && trace.HookReleased == 1 && trace.HookPost == 1 && trace.RelayResults == 1 && trace.Ends == 1 && trace.Cancels == 0
	} else if soakMode {
		// Bounded resource facts after a three-turn warm-up: the last sample must stay within 32 MiB
		// of resident growth, four descriptors and the same owned process count.
		bounded := len(soakSamples) == soakTurns+1
		var warm, last, peak ownedProcessSample
		if len(soakSamples) >= 4 {
			warm, last = soakSamples[3], soakSamples[len(soakSamples)-1]
			for _, sample := range soakSamples {
				peak.rssKB, peak.fds, peak.procs = max(peak.rssKB, sample.rssKB), max(peak.fds, sample.fds), max(peak.procs, sample.procs)
				peak.acpRSSKB, peak.acpProcs = max(peak.acpRSSKB, sample.acpRSSKB), max(peak.acpProcs, sample.acpProcs)
			}
			bounded = bounded && warm.rssKB > 0 && warm.fds > 0 && warm.procs > 0 && last.rssKB-warm.rssKB <= 32<<10 && last.fds <= warm.fds+4 && last.procs <= warm.procs
			// The backend group: the same process count throughout and, for the actual Kiro, a first
			// declared resident envelope of 256 MiB over the warm-up sample.
			bounded = bounded && warm.acpProcs > 0 && last.acpProcs == warm.acpProcs && last.acpRSSKB-warm.acpRSSKB <= 256<<10
		} else {
			bounded = false
		}
		t.Logf("soak_turns=%d samples=%d soak_prompts=%d soak_ends=%d soak_texts=%d warm=%+v last=%+v peak=%+v bounded=%v title_prompts=%d", soakTurns, len(soakSamples), trace.SoakPrompts, trace.SoakEnds, trace.SoakTexts, warm, last, peak, bounded, trace.TitlePrompts)
		valid = valid && bounded && trace.SoakPrompts == soakTurns && trace.SoakEnds == soakTurns && trace.Ends == 1 && trace.Cancels == 0 && trace.Failures == 0
	} else if trustMode {
		if trustPlan.Stage == 1 {
			valid = valid && trustDialogSeen && trustAnswered && trustWritten && trace.Cancels == 0 && trace.Ends == 1
		} else {
			valid = valid && !trustDialogSeen && !trustAnswered && globalUnchanged && trace.Cancels == 0 && trace.Ends == 1
		}
	} else if heldAuth {
		t.Logf("auth_fallback_observed=%v api_error_visible=%v login_messages=%d recovery_attempts=%d acp_auth_exits=%d hook_released=%d hook_post=%d relay_results=%d first_ends=%d", authFallbackObserved, apiErrorVisible, loginMessages, recoveryAttempts, trace.AuthExits, trace.HookReleased, trace.HookPost, trace.RelayResults, trace.Ends)
		t.Logf("recovery_observed=%v recovery_prompts=%d recovery_texts=%d recovery_ends=%d recovery_group_previously_observed=%v title_prompts=%d", recoveryObserved, trace.RecoveryPrompts, trace.RecoveryTexts, trace.RecoveryEnds, groupsBeforeRecovery[trace.RecoveryACP], trace.TitlePrompts)
		valid = valid && heldObserved && authFallbackObserved && !apiErrorVisible && recoveryObserved && trace.AuthExits == 1 && trace.HookReleased == 1 && trace.RelayResults == 0 && trace.RecoveryPrompts == 1 && trace.RecoveryEnds == 1 && trace.Ends == 0 && trace.Failures == 0 && !strings.Contains(string(result.Stdout), "OwnedHookRead_47")
	} else if authExpiry {
		t.Logf("auth_fallback_observed=%v api_error_visible=%v follow_prompts=%d follow_ends=%d follow_texts=%d acp_auth_exits=%d first_ends=%d", authFallbackObserved, apiErrorVisible, trace.FollowPrompts, trace.FollowEnds, trace.FollowTexts, trace.AuthExits, trace.Ends)
		t.Logf("recovery_observed=%v recovery_prompts=%d recovery_texts=%d recovery_ends=%d recovery_group_previously_observed=%v title_prompts=%d", recoveryObserved, trace.RecoveryPrompts, trace.RecoveryTexts, trace.RecoveryEnds, groupsBeforeRecovery[trace.RecoveryACP], trace.TitlePrompts)
		valid = valid && authFallbackObserved && !apiErrorVisible && recoveryObserved && trace.FollowPrompts == 1 && trace.FollowEnds == 0 && trace.FollowTexts == 0 && trace.AuthExits == 1 && trace.RecoveryPrompts == 1 && trace.RecoveryEnds == 1 && trace.Ends == 1 && trace.Cancels == 0 && trace.Failures == 0
	} else if heldFollowup {
		t.Logf("interrupted_history_form=%q manufactured_tool_result=%v old_prompt_cancels=%d read_delivered=%v", trace.FollowForm, manufacturedToolResult(trace.FollowForm), trace.Cancels, strings.Contains(string(result.Stdout), "OwnedHookRead_47"))
		valid = valid && heldObserved && canceledAlive && followupObserved && trace.HookInterrupted == 1 && trace.HookReleased == 0 && trace.HookPost == 0 && trace.RelayResults == 0 && trace.Ends == 0 && trace.FollowPrompts == 1 && trace.FollowEnds == 1 && trace.FollowForm != "" && !manufacturedToolResult(trace.FollowForm) && !strings.Contains(string(result.Stdout), "OwnedHookRead_47")
	} else if followup {
		valid = valid && canceledAlive && followupObserved && trace.FollowPrompts == 1 && trace.FollowEnds == 1 && trace.FollowCancels == 0 && trace.FollowCanceled == 0 && trace.Ends == 0
	} else if mode == "cancel" {
		valid = valid && canceledAlive && trace.Cancels >= 1 && trace.Ends == 0
	} else {
		valid = valid && trace.Cancels == 0 && trace.Ends == 1
	}
	if !valid {
		t.Error("typed keyboard cancellation/completion and exit were not established")
	}
	return trace
}
