package interop_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/ndjson"
)

func TestLiveEffectPromptBounds(t *testing.T) {
	for _, kind := range []string{"allow-read", "allow-write", "allow-bash", "deny-write", "deny-bash", "hook-bash"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if os.Chmod(root, 0700) != nil {
				t.Fatal("cannot make the control directory private")
			}
			canary := "independent-hidden-content"
			e := prepareClientEffect(t, root, root, filepath.Join(root, "read-fixture"), canary, kind, filepath.Join(root, "settings.json"))
			if e.expect.Tool == "Read" && os.WriteFile(e.path, []byte(canary), 0600) != nil {
				t.Fatal("cannot write owned read control")
			}
			system, user, err := liveEffectPrompt(e)
			if err != nil || !strings.Contains(user, string(e.expect.Input)) || !strings.Contains(system, e.expect.Tool) || strings.Contains(system+user, canary) || strings.Contains(system+user, e.manifest) {
				t.Fatal("live prompt lost its exact operation or exposed observer-only data")
			}
			if !e.beforeUse() {
				info, _ := os.Stat(root)
				content, readErr := readDenialArtifact(filepath.Dir(e.path), filepath.Base(e.path), 128)
				t.Fatalf("fresh effect target rejected: directory_mode=%v read_error=%v content_matches=%v", info.Mode().Perm(), readErr, string(content) == canary)
			}
			for _, path := range []string{e.pre, e.post} {
				if os.WriteFile(path, []byte("observed"), 0600) != nil || e.beforeUse() {
					t.Fatal("prior client tool activity was accepted")
				}
				if os.Remove(path) != nil {
					t.Fatal("cannot remove owned control marker")
				}
			}
			if e.expect.Tool != "Read" {
				if os.WriteFile(e.path, []byte(ownedEffectText), 0600) != nil || e.beforeUse() {
					t.Fatal("effect preceding client handoff was accepted")
				}
			}
		})
	}
	for _, e := range []*clientEffectProbe{nil, {}, {kind: "allow-write"}, {kind: "ui-bare-recover-write", expect: &toolEffectExpectation{}}, {kind: "allow-write", expect: &toolEffectExpectation{Tool: "Read", Input: json.RawMessage(`{}`)}}, {kind: "allow-write", expect: &toolEffectExpectation{Tool: "Write", Input: json.RawMessage(`null`)}}} {
		if _, _, err := liveEffectPrompt(e); err == nil {
			t.Fatal("invalid live experiment entered prompt generation")
		}
	}
}

// Only independently prepared single-operation experiments can reach actual model inference.
// The canary, expected result and fake manifest stay out of both prompts.
func liveEffectPrompt(e *clientEffectProbe) (string, string, error) {
	invalid := errors.New("invalid bounded live tool experiment")
	if e == nil || e.expect == nil || e.interactive || e.inspectNext || e.recoverNext || e.bare || e.noDecision {
		return "", "", invalid
	}
	tool := ""
	switch e.kind {
	case "allow-read":
		tool = "Read"
	case "allow-write", "deny-write":
		tool = "Write"
	case "allow-bash", "deny-bash", "hook-bash":
		tool = "Bash"
	}
	fields, err := ndjson.Object(e.expect.Input)
	if tool == "" || e.expect.Tool != tool || err != nil || len(fields) == 0 || len(e.expect.Input) > 16<<10 {
		return "", "", invalid
	}
	system := "Independent single-operation client permission experiment. Request the listed " + tool + " client tool exactly once using the supplied JSON argument object without adding fields. Do not use native tools, inspect configuration or other files, delegate, retry, or start another operation. Accept the client's success or refusal. Do not quote, summarize or disclose any file content or tool output. After the result, finish with only Done."
	if tool == "Bash" {
		system += " The optional Bash description may be one line of at most 256 bytes; the command must remain exact and no other fields may be added."
	}
	user := "Request " + tool + " once with exactly this argument object: " + string(e.expect.Input)
	return system, user, nil
}

func TestKiroLiveClientToolEffects(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("credit-consuming tool tests require explicit DAX_INTEROP_KIRO_CREDIT_OPT_IN=1")
	}
	client, kiro := os.Getenv("DAX_INTEROP_CLAUDE_BINARY"), os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if client == "" || kiro == "" {
		t.Fatal("live effects require both pinned executable paths")
	}
	for _, kind := range []string{"allow-read", "allow-write", "allow-bash", "deny-write", "deny-bash", "hook-bash"} {
		if !t.Run(kind, func(t *testing.T) { runClientToolProbe(t, client, kiro, kind) }) {
			return
		}
	}
}
