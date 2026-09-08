package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/session"
)

type observedPrompt struct {
	Loaded  bool   `json:"loaded"`
	PID     int    `json:"pid"`
	Session string `json:"session"`
	Count   int    `json:"promptCount"`
	Prompt  []struct {
		Text string `json:"text"`
	} `json:"prompt"`
}

func observedTurn(t *testing.T, d *session.Driver, r *anthropic.Request) (observedPrompt, string) {
	t.Helper()
	turn, err := d.Start(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	text, err := collect(context.Background(), turn)
	if err != nil {
		turn.Cancel()
		t.Fatal(err)
	}
	turn.Finish()
	var got observedPrompt
	if json.Unmarshal([]byte(text), &got) != nil {
		t.Fatal("invalid independent observation")
	}
	return got, text
}
func TestThreeTurnReuseTruncationAndDuplicateRecreation(t *testing.T) {
	d := driver(t, "chat-continuity")
	r := sample(t)
	r.Messages = []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "first input"}}}}
	first, reply := observedTurn(t, d, r)
	r.Messages = append(r.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: reply}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "second input"}}})
	second, reply := observedTurn(t, d, r)
	if second.PID != first.PID || second.Session != first.Session || second.Count != 2 || len(second.Prompt) != 1 || second.Prompt[0].Text != "second input" {
		t.Fatal("turn two resent history or created another backend")
	}
	r.Messages = append(r.Messages[1:], anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: reply}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "third input"}}})
	third, _ := observedTurn(t, d, r)
	if third.PID != first.PID || third.Count != 3 || len(third.Prompt) != 1 || third.Prompt[0].Text != "third input" {
		t.Fatal("proven truncated turn did not send just its delta")
	}
	duplicate, _ := observedTurn(t, d, r)
	if duplicate.PID == first.PID || duplicate.Count != 1 || len(duplicate.Prompt) == 1 {
		t.Fatal("completed duplicate reused or omitted safe full context")
	}
	changed := *r
	changed.Messages = []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "divergent input"}}}}
	diverged, _ := observedTurn(t, d, &changed)
	if diverged.PID == duplicate.PID || diverged.Count != 1 {
		t.Fatal("idle divergent history reused")
	}
}
