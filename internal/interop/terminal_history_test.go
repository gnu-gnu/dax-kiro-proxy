package interop_test

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type terminalHistoryPlan struct {
	Home, Project, ID, Seed string
	Stage                   int
}

func TestCompiledRunNativeHistoryWithFakeACP(t *testing.T) { runCompiledNativeHistory(t, "") }

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
	plan := terminalHistoryPlan{Home: filepath.Join(root, "home"), Project: filepath.Join(root, "project"), ID: id, Seed: rand.Text(), Stage: 1}
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
