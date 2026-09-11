package interop_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
)

func TestClaudePendingToolNativeResumeWithFakeACP(t *testing.T) {
	for _, mode := range []string{"release", "interrupt", "interrupt-preface"} {
		if !t.Run(mode, func(t *testing.T) { observeNativeToolHistory(t, false, "allow-bash", mode) }) {
			return
		}
	}
}

func TestKiroLivePendingToolNativeResume(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("pending native resume requires explicit Kiro credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" {
		t.Fatal("pinned Kiro path required")
	}
	observeNativeToolHistory(t, true, "allow-bash", "interrupt")
}

type heldRestart struct {
	root, id           string
	interrupted, ready bool
	lateChecked        bool
	pid, group         int
	postPID, postGroup int
	join               time.Duration
}

func prepareHeldRestart(t *testing.T, ctx context.Context, runner *childproc.Runner, root, settings string, e *clientEffectProbe, mode string) *heldRestart {
	t.Helper()
	if mode != "release" && mode != "interrupt" && mode != "interrupt-preface" || e.expect.Tool != "Bash" {
		t.Fatal("owned held restart mode")
	}
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		t.Fatal("owned native identifier")
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	h := &heldRestart{root: root, id: fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:]), interrupted: mode != "release"}
	t.Cleanup(func() {
		for _, owned := range [][2]int{{h.pid, h.group}, {h.postPID, h.postGroup}} {
			pid, group := owned[0], owned[1]
			current, err := syscall.Getpgid(pid)
			if pid > 1 && group > 1 && group != syscall.Getpgrp() && err == nil && current == group {
				t.Error("recorded native hook survived normal cleanup")
				_ = syscall.Kill(-group, syscall.SIGKILL)
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
					time.Sleep(10 * time.Millisecond)
				}
			}
		}
	})
	var operation struct{ Command string }
	if json.Unmarshal(e.expect.Input, &operation) != nil || operation.Command == "" {
		t.Fatal("owned held command")
	}
	expectation, _ := json.Marshal(map[string]string{"Session": h.id, "Command": operation.Command})
	manifest := filepath.Join(root, "restart-hook.json")
	if os.WriteFile(manifest, expectation, 0600) != nil {
		t.Fatal("owned hook expectation")
	}
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("owned hook source")
	}
	binary := filepath.Join(root, "resume-hook")
	if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", binary, "./testdata/resumehook"}}); err != nil {
		t.Fatal("build held resume hook")
	}
	settingsData, err := os.ReadFile(settings)
	var config map[string]any
	if err != nil || json.Unmarshal(settingsData, &config) != nil {
		t.Fatal("held settings")
	}
	hooks := map[string]any{}
	for _, entry := range []struct{ event, mode string }{{"PreToolUse", "pre"}, {"PostToolUse", "post"}} {
		hooks[entry.event] = []any{map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": probeShellQuote(binary) + " " + probeShellQuote(manifest) + " " + entry.mode, "timeout": 35}}}}
	}
	config["hooks"], config["autoMemoryEnabled"] = hooks, false
	settingsData, _ = json.Marshal(config)
	if os.WriteFile(settings, settingsData, 0600) != nil {
		t.Fatal("held native hook policy")
	}
	effectSpec, _ := json.Marshal(map[string]any{"input": e.expect.Input, "isError": false, "requiredText": "", "interrupted": h.interrupted, "preface": mode == "interrupt-preface"})
	if os.WriteFile(e.manifest, effectSpec, 0600) != nil {
		t.Fatal("held fake expectation")
	}
	return h
}

func (h *heldRestart) effectsAbsent(e *clientEffectProbe) bool {
	pre, err := readDenialArtifact(h.root, filepath.Base(e.pre), 64)
	if err != nil || string(pre) != "observed\n" {
		return false
	}
	for _, path := range []string{e.post, e.path, filepath.Join(h.root, "hook-failed"), filepath.Join(h.root, "hook-released"), filepath.Join(h.root, "post-receipt")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func (h *heldRestart) hookGone() bool {
	if h.pid <= 1 || h.group <= 1 || !errors.Is(syscall.Kill(h.pid, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-h.group, 0), syscall.ESRCH) {
		return false
	}
	data, err := readDenialArtifact(h.root, "post-receipt", 256)
	if os.IsNotExist(err) {
		return h.interrupted
	}
	var post struct {
		PID, Group int
		Digest     string
	}
	if err != nil || json.Unmarshal(data, &post) != nil || post.PID <= 1 || post.Group <= 1 || post.Group == syscall.Getpgrp() {
		return false
	}
	h.postPID, h.postGroup = post.PID, post.Group
	return errors.Is(syscall.Kill(post.PID, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-post.Group, 0), syscall.ESRCH)
}

func (h *heldRestart) lateRelease(ctx context.Context, e *clientEffectProbe) bool {
	if !h.interrupted || !h.hookGone() || !h.effectsAbsent(e) {
		return false
	}
	if os.WriteFile(filepath.Join(h.root, "hook-release"), []byte("release-owned-hook"), 0600) != nil {
		return false
	}
	timer, tick := time.NewTimer(300*time.Millisecond), time.NewTicker(20*time.Millisecond)
	defer timer.Stop()
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			h.lateChecked = h.hookGone() && h.effectsAbsent(e)
			return h.lateChecked
		case <-tick.C:
			if !h.hookGone() || !h.effectsAbsent(e) {
				return false
			}
		}
	}
}

func runHeldRestartClient(t *testing.T, ctx context.Context, runner *childproc.Runner, command childproc.Command, b *toolRestartBackend, h *heldRestart, e *clientEffectProbe) (childproc.Result, error) {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		result childproc.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() { r, err := runner.Run(runCtx, command); done <- outcome{r, err} }()
	timer, tick := time.NewTimer(45*time.Second), time.NewTicker(20*time.Millisecond)
	defer timer.Stop()
	defer tick.Stop()
	var got outcome
	returned := false
wait:
	for {
		select {
		case got = <-done:
			returned = true
			break wait
		case <-ctx.Done():
			break wait
		case <-timer.C:
			break wait
		case <-tick.C:
			data, err := readDenialArtifact(h.root, "held-receipt", 256)
			var record struct {
				PID, Group int
				Digest     string
			}
			if err != nil || json.Unmarshal(data, &record) != nil {
				continue
			}
			group, err := syscall.Getpgid(record.PID)
			if err != nil || record.PID <= 1 || record.Group <= 1 || group != record.Group || group == syscall.Getpgrp() {
				continue
			}
			b.mu.Lock()
			digest := sha256.Sum256([]byte(b.issued.ID))
			valid := !b.failed && b.starts == 1 && b.uses == 1 && b.handoffs == 1 && b.results == 0 && b.ends == 0 && b.issued.ID != "" && record.Digest == hex.EncodeToString(digest[:])
			b.mu.Unlock()
			if valid && h.effectsAbsent(e) {
				h.pid, h.group, h.ready = record.PID, record.Group, true
				break wait
			}
		}
	}
	started := time.Now()
	if h.ready && !h.interrupted {
		if os.WriteFile(filepath.Join(h.root, "hook-release"), []byte("release-owned-hook"), 0600) != nil {
			t.Error("owned live hook release")
			cancel()
		}
	} else {
		for range 8 {
			cancel()
		}
	}
	if !returned {
		select {
		case got = <-done:
			returned = true
		case <-time.After(8 * time.Second):
			t.Error("held native client did not join within observation bound")
			cancel()
			runner.Close()
			select {
			case got = <-done:
				returned = true
			default:
			}
		}
	}
	h.join = time.Since(started)
	if !h.ready || !returned {
		t.Error("delivered tool and exact live native hook were not established")
	}
	return got.result, got.err
}

func TestInterruptedNativeToolPairRejectsClaimedSuccess(t *testing.T) {
	base := `{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":"EffectQuestion_131"},{"role":"assistant","content":[{"type":"tool_use","id":"owned-pending","name":"Bash","input":{"command":"printf owned >> /owned/effect"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"owned-pending","is_error":true,"content":"Independent cancelled operation."}]},{"role":"user","content":"EffectFollow_137"}]}`
	r, err := anthropic.DecodeRequest([]byte(base))
	if err != nil {
		t.Fatal("independent interrupted fixture")
	}
	pair, err := recordedToolPair(r, true, true)
	if err != nil || !pair.failed || pair.use.ID != "owned-pending" {
		t.Fatal("explicit native interruption rejected")
	}
	if _, err := completedPair(r, true); err == nil {
		t.Fatal("interruption counted as completed success")
	}
	var result map[string]any
	if json.Unmarshal(r.Messages[2].Content[0].Raw, &result) != nil {
		t.Fatal("independent result")
	}
	result["is_error"] = false
	r.Messages[2].Content[0].Raw, _ = json.Marshal(result)
	if _, err := recordedToolPair(r, true, true); err == nil {
		t.Fatal("successful result accepted for unexecuted operation")
	}
}

// Measured native representations of an interrupted client tool after explicit-ID resume. The
// 2.1.263 build omits the unfinished pair and stands in a placeholder or the partial text
// ("abandoned"). The 2.1.267 build retains the pair with a fixed error result and a fixed
// continuation line in the same user message, before the same placeholder ("retained"). Neither
// is a successful result or a proxy-visible cancellation; old runtime/effect assertions remain
// separate prerequisites for accepting the new, explicitly non-executing turn.
const (
	retainedInterruptionResult       = "[Request interrupted by user for tool use]"
	retainedInterruptionContinuation = "Continue from where you left off."
)

func abandonedNativeToolHistory(r *anthropic.Request, question, callID, preface string) bool {
	return nativeInterruptedHistoryForm(r, question, callID, preface, pendingRestartQuestion) != ""
}

func abandonedNativeToolHistoryQuestion(r *anthropic.Request, question, callID, preface, nextQuestion string) bool {
	return nativeInterruptedHistoryForm(r, question, callID, preface, nextQuestion) != ""
}

func nativeInterruptedHistoryForm(r *anthropic.Request, question, callID, preface, nextQuestion string) string {
	if r == nil || question == "" || callID == "" || nextQuestion == "" || len(r.Messages) > 16 || !r.ClientContent() {
		return ""
	}
	old, placeholder, next := 0, 0, 0
	uses, results, continuation, resultIndex := 0, 0, 0, -1
	for index, message := range r.Messages {
		for _, block := range message.Content {
			switch block.Type {
			case "tool_use":
				var use anthropic.ToolUse
				if message.Role != "assistant" || old != 1 || uses != 0 || placeholder != 0 || next != 0 || json.Unmarshal(block.Raw, &use) != nil || use.ID != callID {
					return ""
				}
				uses++
			case "tool_result":
				value, err := anthropic.DecodeToolResult(block.Raw)
				if message.Role != "user" || uses != 1 || results != 0 || placeholder != 0 || next != 0 || err != nil || value.ID != callID || !value.IsError || len(value.Content) != 1 || value.Content[0].Type != "text" || value.Content[0].Text != retainedInterruptionResult {
					return ""
				}
				results, resultIndex = 1, index
			case "text":
				if len(block.Text) > 64<<10 || strings.Contains(block.Text, callID) || strings.Contains(block.Text, "ToolArchiveReady_131") || strings.Contains(block.Text, "UnsentEffect_139") {
					return ""
				}
				if message.Role == "user" && block.Text == retainedInterruptionContinuation {
					if results != 1 || index != resultIndex || continuation != 0 || placeholder != 0 {
						return ""
					}
					continuation++
					continue
				}
				if strings.Contains(block.Text, "EffectQuestion_131") {
					if message.Role != "user" || placeholder != 0 || next != 0 || uses != 0 || strings.Count(block.Text, question) != 1 {
						return ""
					}
					old += strings.Count(block.Text, "EffectQuestion_131")
				}
				if message.Role == "assistant" {
					if old != 1 || next != 0 || uses != results || (uses == 1 && continuation != 1) || (block.Text != "No response requested." && (preface == "" || block.Text != preface)) {
						return ""
					}
					placeholder++
				}
				if strings.Contains(block.Text, "EffectFollow_137") {
					if message.Role != "user" || old != 1 || placeholder != 1 || index != r.LatestUserIndex() || block.Text != nextQuestion {
						return ""
					}
					next++
				}
			default:
				return ""
			}
		}
	}
	if old != 1 || placeholder != 1 || next != 1 {
		return ""
	}
	if uses == 0 && results == 0 && continuation == 0 {
		return "abandoned"
	}
	if uses == 1 && results == 1 && continuation == 1 {
		return "retained"
	}
	return ""
}

const pendingRestartQuestion = "EffectFollow_137: The earlier operation was interrupted before execution. Do not execute it or any new tool. Continue from this conversation and reply only with the concatenation of ToolArchiveResumed and _137 without spaces."

func TestAbandonedNativeToolHistoryDoesNotInventResults(t *testing.T) {
	question := "EffectQuestion_131: perform the independent operation"
	r := &anthropic.Request{Messages: []anthropic.Message{
		{Role: "user", Content: []anthropic.Block{{Type: "text", Text: question}}},
		{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: "No response requested."}}},
		{Role: "user", Content: []anthropic.Block{{Type: "text", Text: pendingRestartQuestion}}},
	}}
	if !abandonedNativeToolHistory(r, question, "owned-pending", "") {
		t.Fatal("measured native non-completion representation rejected")
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) { r.Messages[0].Content[0].Text = "EffectQuestion_131: changed operation" },
		func(r *anthropic.Request) { r.Messages[1].Content[0].Text = "ToolArchiveReady_131" },
		func(r *anthropic.Request) { r.Messages[1].Content[0].Text = "owned-pending" },
		func(r *anthropic.Request) { r.Messages[1].Content[0].Text = "" },
		func(r *anthropic.Request) { r.Messages[1].Role = "user" },
		func(r *anthropic.Request) { r.Messages[1], r.Messages[2] = r.Messages[2], r.Messages[1] },
		func(r *anthropic.Request) { r.Messages[2].Content[0].Text = "EffectFollow_137: perform it again" },
		func(r *anthropic.Request) {
			r.Messages[1].Content = append(r.Messages[1].Content, r.Messages[1].Content[0])
		},
	} {
		copy := *r
		copy.Messages = append([]anthropic.Message{}, r.Messages...)
		for index := range copy.Messages {
			copy.Messages[index].Content = append([]anthropic.Block{}, r.Messages[index].Content...)
		}
		mutate(&copy)
		if abandonedNativeToolHistory(&copy, question, "owned-pending", "") {
			t.Fatal("changed or falsely completed native history accepted")
		}
	}
	retained := &anthropic.Request{Messages: []anthropic.Message{
		{Role: "user", Content: []anthropic.Block{{Type: "text", Text: question}}},
		{Role: "assistant", Content: []anthropic.Block{{Type: "tool_use", Raw: json.RawMessage(`{"type":"tool_use","id":"owned-pending","name":"Bash","input":{"command":"printf owned"}}`)}}},
		{Role: "user", Content: []anthropic.Block{{Type: "tool_result", Raw: json.RawMessage(`{"type":"tool_result","tool_use_id":"owned-pending","is_error":true,"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}`)}, {Type: "text", Text: "Continue from where you left off."}}},
		{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: "No response requested."}}},
		{Role: "user", Content: []anthropic.Block{{Type: "text", Text: pendingRestartQuestion}}},
	}}
	if nativeInterruptedHistoryForm(retained, question, "owned-pending", "", pendingRestartQuestion) != "retained" || nativeInterruptedHistoryForm(r, question, "owned-pending", "", pendingRestartQuestion) != "abandoned" {
		t.Fatal("measured native interrupted representations were not distinguished")
	}
	for name, mutate := range map[string]func(*anthropic.Request){
		"successful result": func(r *anthropic.Request) {
			r.Messages[2].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"owned-pending","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}`)
		},
		"foreign result id": func(r *anthropic.Request) {
			r.Messages[2].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"other","is_error":true,"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}`)
		},
		"foreign call id": func(r *anthropic.Request) {
			r.Messages[1].Content[0].Raw = json.RawMessage(`{"type":"tool_use","id":"other","name":"Bash","input":{}}`)
		},
		"other notice": func(r *anthropic.Request) {
			r.Messages[2].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"owned-pending","is_error":true,"content":[{"type":"text","text":"[Request interrupted by user]"}]}`)
		},
		"no continuation": func(r *anthropic.Request) { r.Messages[2].Content = r.Messages[2].Content[:1] },
		"moved continuation": func(r *anthropic.Request) {
			r.Messages[2].Content = r.Messages[2].Content[:1]
			r.Messages = append(r.Messages[:3], append([]anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "Continue from where you left off."}}}}, r.Messages[3:]...)...)
		},
		"no placeholder": func(r *anthropic.Request) { r.Messages = append(r.Messages[:3], r.Messages[4:]...) },
		"repeated call": func(r *anthropic.Request) {
			r.Messages = append(r.Messages[:2], append([]anthropic.Message{r.Messages[1]}, r.Messages[2:]...)...)
		},
		"two result parts": func(r *anthropic.Request) {
			r.Messages[2].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"owned-pending","is_error":true,"content":[{"type":"text","text":"[Request interrupted by user for tool use]"},{"type":"text","text":"extra"}]}`)
		},
	} {
		copy := *retained
		copy.Messages = append([]anthropic.Message{}, retained.Messages...)
		for index := range copy.Messages {
			copy.Messages[index].Content = append([]anthropic.Block{}, retained.Messages[index].Content...)
		}
		mutate(&copy)
		if form := nativeInterruptedHistoryForm(&copy, question, "owned-pending", "", pendingRestartQuestion); form != "" {
			t.Fatalf("%s: mutated retained history accepted as %q", name, form)
		}
	}
	r.Messages[1].Content[0].Text = "Independent partial assistant text"
	if !abandonedNativeToolHistory(r, question, "owned-pending", "Independent partial assistant text") || abandonedNativeToolHistory(r, question, "owned-pending", "different partial text") {
		t.Fatal("partial assistant text was not matched to its original output")
	}
}
