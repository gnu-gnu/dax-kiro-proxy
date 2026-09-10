package interop_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

func TestClaudeResumedToolPolicyWithFakeACP(t *testing.T) {
	for _, policy := range []string{"allow-bash", "deny-bash", "hook-bash"} {
		if !t.Run(policy, func(t *testing.T) { observeToolHistory(t, false, "allow-bash", "", policy) }) {
			return
		}
	}
}

func TestResumedPolicyHandoffRequiresFreshOwnership(t *testing.T) {
	expect := &toolEffectExpectation{Tool: "Bash", Input: json.RawMessage(`{"command":"printf new >> /owned/new"}`)}
	for _, kind := range []string{"fresh", "old-id", "other-operation", "missing-history", "repeated"} {
		t.Run(kind, func(t *testing.T) {
			var cancelled atomic.Int32
			call := anthropic.ToolUse{ID: "new-owned-call", Name: expect.Tool, Input: expect.Input}
			b := &toolRestartBackend{stage: 1, followup: true, historyChecks: 1, expect: expect, previous: completedToolPair{use: anthropic.ToolUse{ID: "old-owned-call"}}, observeProcess: func() error { return nil }, beforeUse: func() bool { return true }}
			switch kind {
			case "old-id":
				call.ID = b.previous.use.ID
			case "other-operation":
				call.Input = json.RawMessage(`{"command":"printf old >> /owned/old"}`)
			case "missing-history":
				b.historyChecks = 0
			case "repeated":
				b.uses = 1
			}
			turn := &toolRestartTurn{owner: b, request: 1, Turn: &denialTurnFixture{events: []inference.Event{{Kind: inference.Tools, Tools: []anthropic.ToolUse{call}}}, cancel: &cancelled}}
			_, err := turn.Next(t.Context())
			if kind == "fresh" {
				if err != nil || b.failed || b.uses != 1 || b.issued.ID != call.ID || cancelled.Load() != 0 {
					t.Fatal("fresh owned handoff rejected")
				}
			} else if !errors.Is(err, inference.ErrRequest) || !b.failed || cancelled.Load() != 1 {
				t.Fatal("unproven, repeated or changed handoff reached the client")
			}
		})
	}
}

func TestResumedPolicyRefusalWitnessRejectsEffects(t *testing.T) {
	for _, policy := range []string{"deny-bash", "hook-bash", "ui-deny-bash", "ui-hook-bash"} {
		t.Run(policy, func(t *testing.T) {
			root := t.TempDir()
			if os.Chmod(root, 0700) != nil {
				t.Fatal("owned witness directory")
			}
			e := &clientEffectProbe{kind: policy, interactive: strings.HasPrefix(policy, "ui-"), expect: &toolEffectExpectation{Tool: "Bash", IsError: true}, path: filepath.Join(root, "effect"), pre: filepath.Join(root, "pre"), post: filepath.Join(root, "post")}
			if (strings.TrimPrefix(policy, "ui-") == "hook-bash" || e.interactive) && followupEffectMatches(e) {
				t.Fatal("absent hook counted as a veto")
			}
			if os.WriteFile(e.pre, []byte("observed\n"), 0600) != nil || !followupEffectMatches(e) {
				t.Fatal("matching refusal witness rejected")
			}
			for _, path := range []string{e.path, e.post} {
				if os.WriteFile(path, []byte("observed\n"), 0600) != nil || followupEffectMatches(e) {
					t.Fatal("effect or success hook accepted for refused work")
				}
				if os.Remove(path) != nil {
					t.Fatal("owned witness cleanup")
				}
			}
			if os.WriteFile(e.pre, []byte("observed\nobserved\n"), 0600) != nil || followupEffectMatches(e) {
				t.Fatal("repeated refusal hook accepted")
			}
		})
	}
}

func TestKiroLiveResumedToolPolicy(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("resumed tool policy requires explicit Kiro credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" {
		t.Fatal("pinned Kiro path required")
	}
	for _, policy := range []string{"allow-bash", "deny-bash", "hook-bash"} {
		if !t.Run(policy, func(t *testing.T) { observeToolHistory(t, true, "allow-bash", "", policy) }) {
			return
		}
	}
}

func TestResumedToolPolicyKeepsOldAndNewResultsSeparate(t *testing.T) {
	base := `{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":"EffectQuestion_131"},{"role":"assistant","content":[{"type":"tool_use","id":"prior-owned-call","name":"Bash","input":{"command":"printf prior >> /owned/prior"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"prior-owned-call","content":"prior result"}]},{"role":"assistant","content":"ToolArchiveReady_131"},{"role":"user","content":"EffectFollow_137: request the separate operation"}]}`
	question := "EffectFollow_137: request the separate operation"
	decode := func(raw string) *anthropic.Request {
		r, err := anthropic.DecodeRequest([]byte(raw))
		if err != nil {
			t.Fatal("independent resumed policy fixture")
		}
		return r
	}
	prior, err := completedPair(decode(base), true)
	if err != nil {
		t.Fatal("independent completed pair")
	}
	expect := &toolEffectExpectation{Tool: "Bash", Input: json.RawMessage(`{"command":"printf next >> /owned/next"}`)}
	issued := anthropic.ToolUse{ID: "fresh-owned-call", Name: expect.Tool, Input: expect.Input}
	if _, err := resumedOperationPair(decode(base), prior, anthropic.ToolUse{}, expect, question, false); err != nil {
		t.Fatal("new question with preserved prior pair rejected")
	}
	withResult := strings.TrimSuffix(base, "]}") + `,{"role":"assistant","content":[{"type":"tool_use","id":"fresh-owned-call","name":"Bash","input":{"command":"printf next >> /owned/next"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"fresh-owned-call","content":"new result"}]}]}`
	if pair, err := resumedOperationPair(decode(withResult), prior, issued, expect, question, true); err != nil || pair.use.ID != issued.ID || pair.failed {
		t.Fatal("matching fresh successful operation rejected")
	}
	for _, replacement := range [][2]string{
		{"prior result", "changed historical result"},
		{"printf prior >> /owned/prior", "printf prior >> /unowned/prior"},
		{"ToolArchiveReady_131", "missing old completion"},
		{"fresh-owned-call", "prior-owned-call"},
		{`"tool_use_id":"fresh-owned-call"`, `"tool_use_id":"unowned-call"`},
		{"printf next >> /owned/next", "printf prior >> /owned/prior"},
		{`"content":"new result"`, `"content":"new result","is_error":true`},
		{question, "EffectFollow_137: changed instruction"},
	} {
		if _, err := resumedOperationPair(decode(strings.ReplaceAll(withResult, replacement[0], replacement[1])), prior, issued, expect, question, true); err == nil {
			t.Fatal("changed ownership, operation, history or outcome accepted")
		}
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) { r.Messages[4], r.Messages[5] = r.Messages[5], r.Messages[4] },
		func(r *anthropic.Request) { r.Messages[5], r.Messages[6] = r.Messages[6], r.Messages[5] },
		func(r *anthropic.Request) {
			r.Messages[6].Content = append(r.Messages[6].Content, r.Messages[6].Content[0])
		},
		func(r *anthropic.Request) {
			r.Messages[5].Content = append(r.Messages[5].Content, r.Messages[5].Content[0])
		},
		func(r *anthropic.Request) { r.Messages = r.Messages[:6] },
		func(r *anthropic.Request) {
			r.Messages[5].Content = append([]anthropic.Block{{Type: "text", Text: "ToolArchiveResumed_137"}}, r.Messages[5].Content...)
		},
	} {
		r := decode(withResult)
		mutate(r)
		if _, err := resumedOperationPair(r, prior, issued, expect, question, true); err == nil {
			t.Fatal("missing, repeated or reordered new operation accepted")
		}
	}
	if _, err := resumedOperationPair(decode(withResult), prior, issued, expect, question, false); err == nil {
		t.Fatal("unsolicited result accepted before new handoff")
	}
	expect.IsError, expect.RequiredText = true, "Independent hook refusal"
	denied := strings.Replace(withResult, `"content":"new result"`, `"content":"Independent hook refusal","is_error":true`, 1)
	if pair, err := resumedOperationPair(decode(denied), prior, issued, expect, question, true); err != nil || !pair.failed {
		t.Fatal("fresh refusal lost its matching identity or failure status")
	}
	if _, err := resumedOperationPair(decode(withResult), prior, issued, expect, question, true); err == nil {
		t.Fatal("fresh success accepted for the refused operation")
	}
	if _, err := resumedOperationPair(decode(strings.Replace(denied, "Independent hook refusal", "different failure", 1)), prior, issued, expect, question, true); err == nil {
		t.Fatal("hook refusal reason lost")
	}
}
