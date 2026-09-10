package interop_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
)

// Check the historical pair independently before admitting a distinct current pair.
func resumedOperationPair(r *anthropic.Request, prior completedToolPair, issued anthropic.ToolUse, expect *toolEffectExpectation, question string, resultExpected bool) (completedToolPair, error) {
	return resumedPairAfterHistory(r, prior, issued, expect, question, resultExpected, func(prefix *anthropic.Request) bool {
		old, err := completedPair(prefix, true)
		return err == nil && sameCompletedPair(old, prior)
	})
}

func resumedInterruptedOperationPair(r *anthropic.Request, prior completedToolPair, issued anthropic.ToolUse, expect *toolEffectExpectation, oldQuestion, question string, resultExpected bool) (completedToolPair, error) {
	return resumedPairAfterHistory(r, prior, issued, expect, question, resultExpected, func(prefix *anthropic.Request) bool {
		return abandonedNativeToolHistoryQuestion(prefix, oldQuestion, prior.use.ID, prior.text, question)
	})
}

func resumedPairAfterHistory(r *anthropic.Request, prior completedToolPair, issued anthropic.ToolUse, expect *toolEffectExpectation, question string, resultExpected bool, historyMatches func(*anthropic.Request) bool) (completedToolPair, error) {
	var pair completedToolPair
	bad := errors.New("resumed operation history mismatch")
	if r == nil || expect == nil || question == "" || len(r.Messages) > 16 || !r.ClientContent() {
		return pair, bad
	}
	boundary, questions := -1, 0
	for index, message := range r.Messages {
		for _, block := range message.Content {
			if len(block.Raw) > 64<<10 || len(block.Text) > 64<<10 {
				return pair, bad
			}
			if block.Type == "text" && strings.Contains(block.Text, "EffectFollow_137") {
				if message.Role != "user" || block.Text != question {
					return pair, bad
				}
				boundary, questions = index, questions+1
			}
		}
	}
	if boundary < 0 || questions != 1 {
		return pair, bad
	}
	prefix := *r
	prefix.Messages = r.Messages[:boundary+1]
	if !historyMatches(&prefix) {
		return pair, bad
	}
	uses, results := 0, 0
	for index := boundary + 1; index < len(r.Messages); index++ {
		message := r.Messages[index]
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				if message.Role != "assistant" && message.Role != "system" || strings.Contains(block.Text, "ToolArchiveResumed_137") || strings.Contains(block.Text, "ToolArchiveReady_131") || strings.Contains(block.Text, "EffectQuestion_131") || strings.Contains(block.Text, "UnsentEffect_139") {
					return pair, bad
				}
			case "tool_use":
				uses++
				if !resultExpected || uses != 1 || results != 0 || message.Role != "assistant" || json.Unmarshal(block.Raw, &pair.use) != nil || !pair.use.Valid() || !expect.matches(pair.use) || pair.use.ID == prior.use.ID || pair.use.ID != issued.ID || pair.use.Name != issued.Name {
					return pair, bad
				}
				pair.input = canonicalToolObject(pair.use.Input)
				if pair.input == nil || !bytes.Equal(pair.input, canonicalToolObject(issued.Input)) {
					return pair, bad
				}
			case "tool_result":
				results++
				value, err := anthropic.DecodeToolResult(block.Raw)
				if !resultExpected || err != nil || uses != 1 || results != 1 || message.Role != "user" || index != r.LatestUserIndex() || value.ID != pair.use.ID || value.IsError != expect.IsError || len(value.Content) == 0 {
					return pair, bad
				}
				var content []string
				for _, part := range value.Content {
					if part.Type != "text" || len(part.Text) > 64<<10 {
						return pair, bad
					}
					content = append(content, part.Text)
				}
				if !strings.Contains(strings.Join(content, "\n"), expect.RequiredText) {
					return pair, bad
				}
				pair.result, _ = json.Marshal(content)
				pair.failed = value.IsError
			default:
				return pair, bad
			}
		}
	}
	if uses != btoi(resultExpected) || results != btoi(resultExpected) || !resultExpected && r.LatestUserIndex() != boundary {
		return pair, bad
	}
	return pair, nil
}

func prepareFollowupEffect(t *testing.T, root, project, policy string) (*clientEffectProbe, string) {
	t.Helper()
	basePolicy := strings.TrimPrefix(policy, "ui-")
	if basePolicy != "allow-bash" && basePolicy != "deny-bash" && basePolicy != "hook-bash" {
		t.Fatal("unknown resumed operation policy")
	}
	owned, target := filepath.Join(root, "followup-policy"), filepath.Join(project, "followup")
	for _, path := range []string{owned, target} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("resumed operation directory")
		}
	}
	settings := filepath.Join(owned, "settings.json")
	e := prepareClientEffect(t, owned, target, "", "", policy, settings)
	e.expect.Input, _ = json.Marshal(map[string]string{"command": "printf '%s\\n' " + probeShellQuote(ownedEffectText) + " >> " + probeShellQuote(e.path)})
	manifest, _ := json.Marshal(map[string]any{"input": e.expect.Input, "isError": e.expect.IsError, "requiredText": e.expect.RequiredText, "followupEffect": true})
	if os.WriteFile(e.manifest, manifest, 0600) != nil {
		t.Fatal("resumed operation expectation")
	}
	for _, entry := range []struct{ event, marker string }{{"PreToolUse", e.pre}, {"PostToolUse", e.post}} {
		output := `{}`
		if basePolicy == "hook-bash" && entry.event == "PreToolUse" {
			data, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": clientDenialReason}})
			output = string(data)
		}
		body := "#!/bin/sh\nset -eu\numask 077\nprintf '%s\\n' observed >> " + probeShellQuote(entry.marker) + "\nprintf '%s' " + probeShellQuote(output) + "\n"
		if os.WriteFile(filepath.Join(owned, entry.event+"-effect.sh"), []byte(body), 0700) != nil {
			t.Fatal("resumed operation hook")
		}
	}
	return e, settings
}

func followupEffectMatches(e *clientEffectProbe) bool {
	if !e.expect.IsError {
		return toolRestartEffectsOnce(e)
	}
	for _, path := range []string{e.path, e.post} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return false
		}
	}
	pre, err := readDenialArtifact(filepath.Dir(e.pre), filepath.Base(e.pre), 64)
	if strings.TrimPrefix(e.kind, "ui-") == "hook-bash" || e.interactive {
		return err == nil && string(pre) == "observed\n"
	}
	return os.IsNotExist(err) || err == nil && string(pre) == "observed\n"
}
