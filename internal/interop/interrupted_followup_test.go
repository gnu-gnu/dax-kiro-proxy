package interop_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

func TestClaudeInterruptedResumedToolPolicyWithFakeACP(t *testing.T) {
	for _, history := range []string{"interrupt", "interrupt-preface"} {
		for _, policy := range []string{"allow-bash", "deny-bash", "hook-bash"} {
			if !t.Run(history+"/"+policy, func(t *testing.T) {
				observeToolHistory(t, false, "allow-bash", history, policy)
			}) {
				return
			}
		}
	}
}

func TestClaudeInterruptedResumedInteractiveToolPolicyWithFakeACP(t *testing.T) {
	for _, history := range []string{"interrupt", "interrupt-preface"} {
		for _, policy := range []string{"ui-allow-bash", "ui-deny-bash", "ui-hook-bash"} {
			if !t.Run(history+"/"+policy, func(t *testing.T) {
				observeToolHistory(t, false, "allow-bash", history, policy)
			}) {
				return
			}
		}
	}
}

func TestKiroLiveInterruptedResumedToolPolicy(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("interrupted resume with new work requires explicit Kiro credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" {
		t.Fatal("pinned Kiro path required")
	}
	// One bounded episode: one cancelled old handoff, one new allowed operation and its
	// continuation. Do not retry dispatched model work or expand the live cases after failure.
	observeToolHistory(t, true, "allow-bash", "interrupt", "allow-bash")
}

func TestInterruptedFollowupRejectsInventedHistoryAndChangedWork(t *testing.T) {
	const oldQuestion = "EffectQuestion_131: perform the independent old operation"
	const question = "EffectFollow_137: request the separate new operation once"
	base := `{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":"` + oldQuestion + `"},{"role":"assistant","content":"OwnedPendingPrefix_149"},{"role":"user","content":"` + question + `"}]}`
	withResult := strings.TrimSuffix(base, "]}") + `,{"role":"assistant","content":[{"type":"tool_use","id":"new-owned-call","name":"Bash","input":{"command":"printf new >> /owned/new"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"new-owned-call","content":"independent new result"}]}]}`
	prior := completedToolPair{text: "OwnedPendingPrefix_149", use: anthropic.ToolUse{ID: "old-owned-call", Name: "Bash", Input: json.RawMessage(`{"command":"printf old >> /owned/old"}`)}}
	expect := &toolEffectExpectation{Tool: "Bash", Input: json.RawMessage(`{"command":"printf new >> /owned/new"}`)}
	issued := anthropic.ToolUse{ID: "new-owned-call", Name: "Bash", Input: expect.Input}
	decode := func(raw string) *anthropic.Request {
		t.Helper()
		r, err := anthropic.DecodeRequest([]byte(raw))
		if err != nil {
			t.Fatal("independent interrupted follow-up request")
		}
		return r
	}
	check := func(r *anthropic.Request, result bool) bool {
		_, err := resumedInterruptedOperationPair(r, prior, issued, expect, oldQuestion, question, result, nil)
		return err == nil
	}
	if !check(decode(base), false) || !check(decode(withResult), true) || check(decode(base), true) || check(decode(withResult), false) {
		t.Fatal("interrupted prefix and new pair phases not separated")
	}
	if !check(decode(strings.Replace(base, prior.text, "No response requested.", 1)), false) {
		t.Fatal("native non-completion placeholder rejected")
	}
	for _, change := range [][2]string{
		{oldQuestion, "EffectQuestion_131: changed historical question"},
		{prior.text, "different old partial response"},
		{prior.text, "ToolArchiveReady_131"},
		{prior.text, "old-owned-call"},
		{question, "EffectFollow_137: changed new instruction"},
		{"new-owned-call", "old-owned-call"},
		{`"tool_use_id":"new-owned-call"`, `"tool_use_id":"foreign-call"`},
		{"printf new >> /owned/new", "printf old >> /owned/old"},
		{`"content":"independent new result"`, `"content":"independent new result","is_error":true`},
	} {
		if check(decode(strings.ReplaceAll(withResult, change[0], change[1])), true) {
			t.Fatal("changed old history, new work, ownership or outcome accepted")
		}
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) { r.Messages[0], r.Messages[1] = r.Messages[1], r.Messages[0] },
		func(r *anthropic.Request) { r.Messages[2], r.Messages[3] = r.Messages[3], r.Messages[2] },
		func(r *anthropic.Request) { r.Messages[3], r.Messages[4] = r.Messages[4], r.Messages[3] },
		func(r *anthropic.Request) {
			r.Messages[1].Content = append(r.Messages[1].Content, r.Messages[1].Content[0])
		},
		func(r *anthropic.Request) {
			r.Messages[4].Content = append(r.Messages[4].Content, r.Messages[4].Content[0])
		},
		func(r *anthropic.Request) {
			r.Messages[1].Content = append(r.Messages[1].Content, r.Messages[3].Content[0])
		},
		func(r *anthropic.Request) { r.Messages = r.Messages[:4] },
	} {
		r := decode(withResult)
		mutate(r)
		if check(r, true) {
			t.Fatal("invented, duplicated, reordered or missing history accepted")
		}
	}
	expect.IsError, expect.RequiredText = true, "independent veto"
	refusal := strings.Replace(withResult, `"content":"independent new result"`, `"content":"independent veto","is_error":true`, 1)
	if !check(decode(refusal), true) || check(decode(withResult), true) || check(decode(strings.Replace(refusal, "independent veto", "different reason", 1)), true) {
		t.Fatal("current tool refusal lost its identity, status or reason")
	}
}

func TestInterruptedFollowupCannotFinishWithoutNewResult(t *testing.T) {
	for _, results := range []int{0, 1} {
		var cancelled atomic.Int32
		b := &toolRestartBackend{stage: 1, followup: true, interrupted: true, abandoned: true, results: results, observeProcess: func() error { return nil }}
		turn := &toolRestartTurn{owner: b, request: 2, Turn: &denialTurnFixture{events: []inference.Event{{Kind: inference.End, StopReason: "end_turn"}}, cancel: &cancelled}}
		_, err := turn.Next(t.Context())
		if results == 0 && (!errors.Is(err, inference.ErrRequest) || !b.failed || cancelled.Load() != 1) {
			t.Fatal("abandoned old history incorrectly satisfied the new result requirement")
		}
		if results == 1 && (err != nil || b.failed || b.ends != 1 || cancelled.Load() != 0) {
			t.Fatal("new result did not permit completion")
		}
	}
}
