package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/inference"
)

func TestPromptStopReasonsDrainTextBeforeCompletion(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"end_turn", "end_turn"}, {"max_tokens", "max_tokens"},
		{"refusal", "refusal"}, {"max_turn_requests", "pause_turn"},
	} {
		for _, late := range []bool{false, true} {
			t.Run(tc.source+map[bool]string{false: "/queued", true: "/late"}[late], func(t *testing.T) {
				r := progressRound(t, []string{
					`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}}`,
					`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"last"}}`,
				}, late)
				r.turn.result, _ = json.Marshal(map[string]string{"stopReason": tc.source})
				var text string
				for range 2 {
					event, err := r.Next(t.Context())
					if err != nil || event.Kind != inference.Text {
						t.Fatal("completion lost a preceding answer chunk")
					}
					text += event.Text
				}
				event, err := r.Next(t.Context())
				if err != nil || event.Kind != inference.End || event.StopReason != tc.want || text != "first last" || !r.success {
					t.Fatal("public prompt stop reason lost its text or completion class")
				}
				if _, err := r.Next(t.Context()); !errors.Is(err, io.EOF) {
					t.Fatal("completed prompt emitted another event")
				}
			})
		}
	}
}

func TestUnsupportedPromptCompletionFailsExplicitly(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `[]`, `{"stopReason":null}`, `{"stopReason":1}`,
		`{"stopReason":""}`, `{"stopReason":"invented"}`, `{"stopReason":"pause_turn"}`,
		`{"stopReason":"end_turn","stopReason":"refusal"}`, `{"stopReason":"cancelled"}`} {
		r := progressRound(t, nil, false)
		r.turn.result = json.RawMessage(raw)
		want := acp.ErrProtocol
		if raw == `{"stopReason":"cancelled"}` {
			want = context.Canceled
		}
		if _, err := r.Next(t.Context()); !errors.Is(err, want) || r.success {
			t.Fatal("unsupported or malformed completion was accepted")
		}
	}
	// A supported stop reason cannot turn unsupported answer content into a successful response.
	r := progressRound(t, []string{
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"visible prefix"}}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"image","mimeType":"image/png","data":"iVBORw0KGgo="}}`,
	}, false)
	r.turn.result = json.RawMessage(`{"stopReason":"max_turn_requests"}`)
	if event, err := r.Next(t.Context()); err != nil || event.Text != "visible prefix" {
		t.Fatal("preceding answer text was lost")
	}
	if _, err := r.Next(t.Context()); !errors.Is(err, acp.ErrProtocol) || r.success {
		t.Fatal("unsupported answer content silently became a completion")
	}
}

func TestRequestLimitWithoutTextDoesNotInventAnAnswer(t *testing.T) {
	r := progressRound(t, nil, false)
	r.turn.result = json.RawMessage(`{"stopReason":"max_turn_requests"}`)
	event, err := r.Next(t.Context())
	if err != nil || event.Kind != inference.End || event.StopReason != "pause_turn" || r.text.Len() != 0 || event.Text != "" || !r.success {
		t.Fatal("empty request-limit completion failed or invented answer text")
	}
}
