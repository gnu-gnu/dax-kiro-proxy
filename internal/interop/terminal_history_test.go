package interop_test

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type terminalHistoryPlan struct {
	Home, Project, ID, Seed, State string
	Stage                          int
	Picker                         bool
	Name                           string
}

func TestCompiledRunNativeHistoryWithFakeACP(t *testing.T) { runCompiledNativeHistory(t, "") }

func TestCompiledRunNativeHistoryPickerWithFakeACP(t *testing.T) {
	runCompiledHistoryScenario(t, "", true)
}

func TestKiroLiveCompiledRunNativeHistoryPicker(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("native picker requires explicit Kiro credit opt-in")
	}
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" {
		t.Fatal("pinned Kiro executable required")
	}
	runCompiledHistoryScenario(t, kiro, true)
}

func TestNativeHistoryPickerRequiresOneExactSelectedRow(t *testing.T) {
	const row = "OwnedResume_113"
	for _, tc := range []struct {
		screen   string
		selected bool
	}{
		{"Resume Session\n❯ OwnedResume_113\nEnter to resume", true},
		{"❯ /resume\nResume Session\n❯ OwnedResume_113\nEnter to resume", true},
		{"❯ OwnedResume_113\nEnter to resume", false},
		{"Resume Session\n❯ OtherSession\n  OwnedResume_113", false},
		{"Resume Session\n❯ OwnedResume_113\n  OwnedResume_113", false},
		{"Resume Session\n❯ OwnedResume_113suffix", false},
		{"Resume Session\n❯ OtherOwnedResume_113", false},
	} {
		if terminalHistoryPickerSelected(tc.screen, row) != tc.selected {
			t.Fatal("ambiguous native session picker accepted")
		}
	}
}

func TestKiroLiveCompiledRunNativeHistory(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("native restart requires explicit Kiro credit opt-in")
	}
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" {
		t.Fatal("pinned Kiro executable required")
	}
	runCompiledNativeHistory(t, kiro)
}

func runCompiledNativeHistory(t *testing.T, kiro string) {
	runCompiledHistoryScenario(t, kiro, false)
}

func runCompiledHistoryScenario(t *testing.T, kiro string, picker bool) {
	t.Helper()
	if os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Skip("pinned Claude executable required")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-terminal-history-")
	if err != nil {
		t.Fatal("shared owned history root")
	}
	defer os.RemoveAll(root)
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		t.Fatal("owned native session identity")
	}
	uuid[6], uuid[8] = (uuid[6]&15)|64, (uuid[8]&63)|128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
	plan := terminalHistoryPlan{Home: filepath.Join(root, "home"), Project: filepath.Join(root, "project"), ID: id, Seed: rand.Text(), Stage: 1, State: filepath.Join(root, "state")}
	if picker {
		plan.Picker, plan.Name = true, "OwnedResume_113"
	}
	first := runTerminalScenario(t, "history-initial", kiro, &plan)
	if t.Failed() {
		return
	}
	plan.Stage = 2
	second := runTerminalScenario(t, "history-resume", kiro, &plan)
	if t.Failed() {
		return
	}
	if first.Client == second.Client || first.Proxy == second.Proxy || first.ACP == second.ACP || first.Profile == second.Profile || first.Endpoint == second.Endpoint {
		t.Error("native restart reused an observed owner")
	}
}

func terminalHistoryPickerSelected(screen, name string) bool {
	if name == "" {
		return false
	}
	lines := strings.Split(screen, "\n")
	header := -1
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), "resume session") {
			header = i
			break
		}
	}
	if header < 0 {
		return false
	}
	rows, selected := 0, false
	for _, line := range lines[header+1:] {
		prefix, tail, ok := strings.Cut(line, name)
		if !ok {
			continue
		}
		if len(prefix) > 0 {
			c := prefix[len(prefix)-1]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
				continue
			}
		}
		if len(tail) > 0 {
			c := tail[0]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
				continue
			}
		}
		rows++
		selected = selected || strings.Contains(line, "❯")
	}
	return rows == 1 && selected
}
