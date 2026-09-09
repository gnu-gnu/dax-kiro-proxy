package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

const terminalStreamPrompt = `Begin with the concatenation of Ready and _47 without spaces. Then list integers 1 through 2000, one per line, without tools or any other text.`
const terminalFollowupPrompt = `Stop the counting task. Reply with the concatenation of Follow and _49 without spaces, and no other text.`

func terminalCancelEligible(trace terminalTrace) bool {
	return trace.Prompts == 1 && trace.TitlePrompts <= 1 && trace.Texts >= 2 && trace.Ends == 0 && trace.Cancels == 0 && trace.Canceled == 0 && trace.Failures == 0 && trace.PromptFailures == 0 && !trace.Exited
}

func terminalHookEligible(trace terminalTrace) bool {
	return trace.Prompts == 1 && trace.TitlePrompts <= 1 && trace.HookHeld == 1 && trace.Hook > 1 && trace.HookReleased == 0 && trace.HookPost == 0 && trace.HookInterrupted == 0 && trace.Ends == 0 && trace.Cancels == 0 && trace.Canceled == 0 && trace.Failures == 0 && trace.PromptFailures == 0 && !trace.Exited
}

func terminalFollowupComplete(r terminalTrace) bool {
	return r.Clients == 1 && r.Prompts == 1 && r.Cancels > 0 && r.Ends == 0 && r.FollowPrompts == 1 && r.FollowACP > 1 && r.FollowACP != r.ACP && r.FollowTexts > 0 && r.FollowEnds == 1 && r.FollowCancels == 0 && r.FollowCanceled == 0 && r.FollowOldInput && r.FollowNewInput && !r.FollowNull && r.TitlePrompts <= 2 && r.Failures == 0 && r.PromptFailures == 0 && !r.Exited
}

func terminalExitConfirmation(screen string) bool {
	plain := strings.ToLower(strings.Join(strings.Fields(screen), " "))
	return strings.Contains(plain, "ctrl+d again to exit") || strings.Contains(plain, "ctrl-d again to exit")
}

type terminalReceipt struct {
	NullMarker    bool   `json:"null_marker"`
	FollowScope   bool   `json:"follow_scope"`
	OldInput      bool   `json:"old_input"`
	NewInput      bool   `json:"new_input"`
	PartialMarker bool   `json:"partial_marker"`
	Kind          string `json:"kind"`
	PID           int    `json:"pid"`
	Group         int    `json:"group"`
	Child         int    `json:"child"`
	Foreground    int    `json:"foreground"`
	Profile       string `json:"profile"`
	Endpoint      string `json:"endpoint"`
	Code          int    `json:"code"`
	Restored      bool   `json:"restored"`
	MainHint      bool   `json:"main_hint"`
	TitleHint     bool   `json:"title_hint"`
	SummaryHint   bool   `json:"summary_hint"`
	TitleScope    bool   `json:"title_scope"`
}

type terminalTrace struct {
	PromptFailures                                                                            int
	FollowNull                                                                                bool
	Clients, FollowPrompts, FollowACP, FollowTexts, FollowEnds, FollowCancels, FollowCanceled int
	FollowOldInput, FollowNewInput, FollowPartial                                             bool
	Hook, HookHeld, HookReleased, HookInterrupted, HookPost                                   int
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
	var trace terminalTrace
	entries, err := os.ReadDir(filepath.Join(root, "events"))
	if err != nil || len(entries) > 32 {
		return trace, errors.New("terminal receipt directory")
	}
	total := 0
	groups, pids := map[int]bool{}, map[int]bool{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return trace, errors.New("terminal receipt file")
		}
		data, err := os.ReadFile(filepath.Join(root, "events", entry.Name()))
		total += len(data)
		if err != nil || total > 512<<10 {
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
			case "client":
				trace.Clients++
				trace.Client, trace.Foreground, trace.Profile, trace.Endpoint = r.PID, r.Foreground, r.Profile, r.Endpoint
			case "acp":
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
					trace.FollowNull = r.NullMarker
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
				if !r.TitleScope && r.FollowScope {
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
	t.Helper()
	heldHook := strings.HasPrefix(mode, "held-hook-")
	followup := mode == "cancel-followup"
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
	home, project, bin, artifacts := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "bin"), filepath.Join(root, "runtime")
	for _, dir := range []string{home, project, bin, artifacts, filepath.Join(root, "events"), filepath.Join(home, ".claude"), filepath.Join(root, "tmp")} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot prepare terminal fixture directory")
		}
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	settingsData := []byte(`{"disableAllHooks":true,"statusLine":{"type":"command","command":"/usr/bin/true"}}`)
	readFixture, prompt := filepath.Join(project, "read-fixture"), terminalStreamPrompt
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
	if os.WriteFile(settings, settingsData, 0600) != nil || os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{}`), 0600) != nil {
		t.Fatal("cannot prepare owned terminal settings")
	}
	beforeSettings, beforeGlobal := fileFingerprint(t, settings), fileFingerprint(t, filepath.Join(home, ".claude.json"))
	ctx, stop := context.WithTimeout(t.Context(), 2*time.Minute)
	defer stop()
	runner, err := childproc.New(childproc.Config{Timeout: 45 * time.Second, MaxOutputBytes: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
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
	args := []string{"run", "--client", filepath.Join(bin, "claude"), "--kiro", filepath.Join(bin, "kiro-cli"), "--settings", settings, "--runtime-dir", artifacts, "--state-dir", filepath.Join(root, "state")}
	config, _ := json.Marshal(map[string]any{"Root": root, "Proxy": proxy, "Client": client, "Kiro": kiro, "AccountHome": os.Getenv("HOME"), "Project": project, "Args": args, "HeldHook": heldHook, "AllowFollowup": followup})
	if os.WriteFile(filepath.Join(bin, "terminal.json"), config, 0600) != nil {
		t.Fatal("cannot write terminal role configuration")
	}
	var trace terminalTrace
	observedGroups := make(map[int]bool)
	defer func() {
		if final, err := readTerminalTrace(root); err == nil {
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
	owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 70 * time.Second})
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
	var followupObserved bool
	groupsBeforeFollowup := make(map[int]bool)
	var receiptErr error
	var canceledAlive, foregroundObserved, exitKey bool
	var screenHints uint32
	result, setup, runErr := runObservedTerminal(ctx, owner, command, nil, func(screen string) string {
		var err error
		trace, err = readTerminalTrace(root)
		if err != nil {
			receiptErr = err
			return ""
		}
		for _, group := range trace.Groups {
			observedGroups[group] = true
		}
		lower := strings.ToLower(strings.Join(strings.Fields(screen), " "))
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
			if trace.Client > 1 && trace.Foreground == trace.Client && statusProjectVisible(lower, project) && strings.Contains(screen, "❯") && !strings.Contains(lower, "do you want") && !strings.Contains(lower, "enter to continue") {
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
			if heldHook {
				if terminalHookEligible(trace) && (kiro != "" || trace.RelayCalls == 1 && trace.RelayResults == 0) && syscall.Kill(trace.Hook, 0) == nil && syscall.Kill(trace.Client, 0) == nil && syscall.Kill(trace.Proxy, 0) == nil {
					if heldAt.IsZero() {
						heldAt = time.Now()
					}
					if time.Since(heldAt) < 500*time.Millisecond {
						return ""
					}
					heldObserved = true
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
			} else if mode == "natural-completion" {
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
			if terminalFollowupComplete(trace) && !groupsBeforeFollowup[trace.FollowACP] && strings.Contains(screen, "Follow_49") && trace.Client == originalClient && trace.Proxy == originalProxy && trace.Profile == originalProfile && trace.Endpoint == originalEndpoint && syscall.Kill(originalClient, 0) == nil && syscall.Kill(originalProxy, 0) == nil && errors.Is(syscall.Kill(-trace.ACP, 0), syscall.ESRCH) {
				followupObserved = true
				stage, exitKey = 5, true
				return "\x04"
			}
		}
		return ""
	}, true)
	trace, err = readTerminalTrace(root)
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
	sources := beforeSettings == fileFingerprint(t, settings) && beforeGlobal == fileFingerprint(t, filepath.Join(home, ".claude.json"))
	lateReleaseQuiet := true
	if heldHook {
		content, readErr := os.ReadFile(readFixture)
		sources = sources && readErr == nil && string(content) == "OwnedHookRead_47\n"
		if mode == "held-hook-exit" && groupsGone && pidsGone {
			if os.WriteFile(filepath.Join(root, "hook-release"), []byte("release-owned-hook"), 0600) != nil {
				lateReleaseQuiet = false
			}
			time.Sleep(300 * time.Millisecond)
			after, err := readTerminalTrace(root)
			lateReleaseQuiet = lateReleaseQuiet && err == nil && after.HookReleased == 0 && after.HookPost == 0 && after.RelayResults == 0 && after.Prompts == trace.Prompts && after.TitlePrompts == trace.TitlePrompts && after.Ends == 0 && after.Failures == 0 && after.PromptFailures == 0
		}
		t.Logf("held_observed=%v hook_held=%d hook_released=%d hook_interrupted=%d hook_post=%d hook_alive_at_exit_confirmation=%v relay_calls=%d relay_results=%d late_release_quiet=%v exit_ms=%d", heldObserved, trace.HookHeld, trace.HookReleased, trace.HookInterrupted, trace.HookPost, hookAtConfirmation, trace.RelayCalls, trace.RelayResults, lateReleaseQuiet, exitLatency.Milliseconds())
	}
	t.Logf("non_success_prompt_results=%d", trace.PromptFailures)
	t.Logf("fixed_screen_hint_bits=%d observed_groups=%d observed_pids=%d", screenHints, len(trace.Groups), len(trace.PIDs))
	if followup {
		t.Logf("absent_marker_detected=%v followup_group_previously_observed=%v", trace.FollowNull, groupsBeforeFollowup[trace.FollowACP])
		t.Logf("followup_observed=%v clients=%d follow_prompts=%d follow_texts=%d follow_ends=%d follow_cancels=%d follow_cancelled_replies=%d distinct_acp=%v old_input_preserved=%v new_input_preserved=%v partial_marker_present=%v same_client=%v same_proxy=%v same_profile=%v same_listener=%v", followupObserved, trace.Clients, trace.FollowPrompts, trace.FollowTexts, trace.FollowEnds, trace.FollowCancels, trace.FollowCanceled, trace.FollowACP > 1 && trace.FollowACP != trace.ACP, trace.FollowOldInput, trace.FollowNewInput, trace.FollowPartial, trace.Client == originalClient, trace.Proxy == originalProxy, trace.Profile == originalProfile, trace.Endpoint == originalEndpoint)
	}
	t.Logf("prompt_attempts=%d main_hints=%d title_hints=%d summary_hints=%d title_prompts=%d title_ends=%d", trace.Attempts, trace.MainHints, trace.TitleHints, trace.SummaryHints, trace.TitlePrompts, trace.TitleEnds)
	t.Logf("live_kiro=%v mode=%s stage=%d setup=%d prompts=%d texts=%d cancels=%d ends=%d cancelled_replies=%d guard_failures=%d key_after_texts=%d foreground_observed=%v client_alive_after_cancel=%v keyboard_exit=%v proxy_exited=%v proxy_exit=%d terminal_restored=%v groups_gone=%v recorded_pids_gone=%v listener_gone=%v runtime_removed=%v profile_removed=%v sources_unchanged=%v output_bytes=%d command_exit=%d", kiro != "", mode, stage, setup, trace.Prompts, trace.Texts, trace.Cancels, trace.Ends, trace.Canceled, trace.Failures, beforeKey, foregroundObserved, canceledAlive, exitKey, trace.Exited, trace.ExitCode, trace.Restored, groupsGone, pidsGone, listenerGone, artifactErr == nil && len(entries) == 0, os.IsNotExist(profileErr), sources, len(result.Stdout), result.ExitCode)
	titleLimit := 1
	if followup {
		titleLimit = 2
	}
	valid := runErr == nil && result.ExitCode == 0 && receiptErr == nil && err == nil && foregroundObserved && exitKey && trace.Exited && trace.ExitCode == 0 && trace.Restored && groupsGone && pidsGone && listenerGone && artifactErr == nil && len(entries) == 0 && os.IsNotExist(profileErr) && sources && trace.Prompts == 1 && trace.TitlePrompts <= titleLimit && trace.Failures == 0 && trace.PromptFailures == 0
	if mode == "held-hook-exit" {
		valid = valid && heldObserved && trace.HookReleased == 0 && trace.HookPost == 0 && trace.RelayResults == 0 && trace.Ends == 0 && !strings.Contains(string(result.Stdout), "OwnedHookRead_47") && exitLatency < 8*time.Second && lateReleaseQuiet
	} else if mode == "held-hook-release" {
		valid = valid && heldObserved && trace.HookReleased == 1 && trace.HookPost == 1 && trace.RelayResults == 1 && trace.Ends == 1 && trace.Cancels == 0
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
}
