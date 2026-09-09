package interop_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalCancellationRequiresAnActiveMainResponse(t *testing.T) {
	active := terminalTrace{Prompts: 1, Texts: 2}
	for _, tc := range []struct {
		name   string
		change func(*terminalTrace)
		valid  bool
	}{
		{"main", func(*terminalTrace) {}, true},
		{"separate-title", func(r *terminalTrace) { r.TitlePrompts = 1; r.TitleEnds = 1 }, true},
		{"only-title", func(r *terminalTrace) { r.Prompts = 0; r.Texts = 0; r.TitlePrompts = 1 }, false},
		{"finished", func(r *terminalTrace) { r.Ends = 1 }, false},
		{"earlier-cancel", func(r *terminalTrace) { r.Cancels = 1 }, false},
		{"cancelled-reply", func(r *terminalTrace) { r.Canceled = 1 }, false},
		{"exited", func(r *terminalTrace) { r.Exited = true }, false},
		{"guard-failed", func(r *terminalTrace) { r.Failures = 1 }, false},
		{"third-prompt", func(r *terminalTrace) { r.TitlePrompts = 2 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := active
			tc.change(&r)
			if terminalCancelEligible(r) != tc.valid {
				t.Fatal("unproven response admitted for keyboard cancellation")
			}
		})
	}
}

func TestTerminalExitConfirmationRequiresTheObservedKey(t *testing.T) {
	for _, tc := range []struct {
		screen string
		valid  bool
	}{
		{"Press Ctrl+D again to exit", true},
		{"Press Ctrl-D again to exit", true},
		{"Press Ctrl+C again to exit", false},
		{"Press Ctrl+D to exit", false},
		{"again exit Ctrl+D", false},
	} {
		if terminalExitConfirmation(tc.screen) != tc.valid {
			t.Fatal("unrelated exit text admitted")
		}
	}
}

func TestTerminalReceiptsKeepAuxiliaryAndMainProcessesSeparate(t *testing.T) {
	root := t.TempDir()
	if os.Mkdir(filepath.Join(root, "events"), 0700) != nil {
		t.Fatal("fixture")
	}
	for _, scope := range []struct {
		name  string
		pid   int
		title bool
	}{{"main", 501, false}, {"title", 601, true}} {
		var data []byte
		for _, kind := range []string{"acp", "prompt", "text", "text"} {
			line, _ := json.Marshal(map[string]any{"kind": kind, "pid": scope.pid, "group": scope.pid, "title_scope": scope.title})
			data = append(data, append(line, '\n')...)
		}
		if scope.title {
			line, _ := json.Marshal(map[string]any{"kind": "end", "pid": scope.pid, "group": scope.pid, "title_scope": true})
			data = append(data, append(line, '\n')...)
		}
		if os.WriteFile(filepath.Join(root, "events", scope.name+".jsonl"), data, 0600) != nil {
			t.Fatal("fixture")
		}
	}
	r, err := readTerminalTrace(root)
	if err != nil || r.Prompts != 1 || r.Texts != 2 || r.Ends != 0 || r.TitlePrompts != 1 || r.TitleEnds != 1 || r.ACP != 501 || len(r.Groups) != 2 || len(r.PIDs) != 2 || !terminalCancelEligible(r) {
		t.Fatal("auxiliary receipt captured main lifecycle")
	}
}

func TestTerminalHookExitRequiresALiveUnreleasedCall(t *testing.T) {
	active := terminalTrace{Prompts: 1, HookHeld: 1, Hook: 123}
	for _, tc := range []struct {
		name   string
		change func(*terminalTrace)
		valid  bool
	}{
		{"held", func(*terminalTrace) {}, true},
		{"title-only", func(r *terminalTrace) { r.Prompts = 0; r.TitlePrompts = 1 }, false},
		{"no-hook", func(r *terminalTrace) { r.HookHeld = 0 }, false},
		{"repeated-hook", func(r *terminalTrace) { r.HookHeld = 2 }, false},
		{"released", func(r *terminalTrace) { r.HookReleased = 1 }, false},
		{"post-tool", func(r *terminalTrace) { r.HookPost = 1 }, false},
		{"ended", func(r *terminalTrace) { r.Ends = 1 }, false},
		{"cancelled", func(r *terminalTrace) { r.Cancels = 1 }, false},
		{"interrupted", func(r *terminalTrace) { r.HookInterrupted = 1 }, false},
		{"failed", func(r *terminalTrace) { r.Failures = 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := active
			tc.change(&trace)
			if terminalHookEligible(trace) != tc.valid {
				t.Fatal("unproven held hook admitted")
			}
		})
	}
}

func TestTerminalFollowupNeedsDistinctMainCompletionAndHistory(t *testing.T) {
	complete := terminalTrace{Clients: 1, Prompts: 1, ACP: 101, Cancels: 1, FollowPrompts: 1, FollowACP: 202, FollowTexts: 1, FollowEnds: 1, FollowOldInput: true, FollowNewInput: true}
	for _, tc := range []struct {
		name   string
		change func(*terminalTrace)
		valid  bool
	}{
		{"completed", func(*terminalTrace) {}, true},
		{"new-client", func(r *terminalTrace) { r.Clients = 2 }, false},
		{"same-backend", func(r *terminalTrace) { r.FollowACP = r.ACP }, false},
		{"not-cancelled", func(r *terminalTrace) { r.Cancels = 0 }, false},
		{"first-ended", func(r *terminalTrace) { r.Ends = 1 }, false},
		{"title-only", func(r *terminalTrace) { r.FollowEnds = 0; r.TitleEnds = 2 }, false},
		{"no-old-input", func(r *terminalTrace) { r.FollowOldInput = false }, false},
		{"no-new-input", func(r *terminalTrace) { r.FollowNewInput = false }, false},
		{"second-cancelled", func(r *terminalTrace) { r.FollowCancels = 1 }, false},
		{"automatic-retry", func(r *terminalTrace) { r.FollowPrompts = 2 }, false},
		{"non-success-result", func(r *terminalTrace) { r.PromptFailures = 1 }, false},
		{"detector-null", func(r *terminalTrace) { r.FollowNull = true }, false},
		{"failed", func(r *terminalTrace) { r.Failures = 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := complete
			tc.change(&r)
			if terminalFollowupComplete(r) != tc.valid {
				t.Fatal("unproven followup admitted")
			}
		})
	}
}
