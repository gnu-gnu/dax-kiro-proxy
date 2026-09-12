package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/status"
)

// The first empty poll can publish completion and its preceding notifications together.
type progressPeer struct {
	backendClient
	queue []acp.Notification
	done  chan struct{}
	late  bool
}

func (*progressPeer) Err() error { return nil }
func (p *progressPeer) TryNext() (acp.Notification, bool, error) {
	if p.late {
		p.late = false
		close(p.done)
		return acp.Notification{}, false, nil
	}
	if len(p.queue) == 0 {
		return acp.Notification{}, false, nil
	}
	e := p.queue[0]
	p.queue = p.queue[1:]
	return e, true, nil
}

func TestProgressNotificationsPreserveOrderedAnswer(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "late-drain"}[late], func(t *testing.T) {
			updates := []string{
				`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"private reasoning"}}`,
				`{"sessionUpdate":"plan","entries":[{"content":"private plan","priority":"high","status":"in_progress"}]}`,
				`{"sessionUpdate":"tool_call","toolCallId":"observed-1","title":"private title"}`,
				`{"sessionUpdate":"tool_call_update","toolCallId":"observed-1","status":"completed","rawOutput":{"private":"result"}}`,
				`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"visible answer"}}`,
			}
			r := progressRound(t, updates, late)
			for i, want := range []inference.Kind{inference.Progress, inference.Progress, inference.Progress, inference.Progress, inference.Text, inference.End} {
				e, err := r.Next(t.Context())
				if err != nil || e.Kind != want {
					t.Fatalf("notification sequence mismatch: index=%d kind=%d failed=%v", i, e.Kind, err != nil)
				}
				if want == inference.Progress && (e.Text != "" || e.StopReason != "" || len(e.Tools) != 0) {
					t.Fatal("progress retained private content")
				}
			}
			if r.text.String() != "visible answer" {
				t.Fatal("non-answer progress entered the history buffer")
			}
			var expected status.VisibleOutput
			expected.AddText("visible answer")
			if r.turn.visibleOutput != expected {
				t.Fatal("progress entered visible-output accounting")
			}
		})
	}
}

func TestProgressTrackingBoundAndFreshTurn(t *testing.T) {
	var updates []string
	for i := 0; i <= 4096; i++ {
		updates = append(updates, fmt.Sprintf(`{"sessionUpdate":"tool_call","toolCallId":"bounded-%d","title":"step"}`, i))
	}
	updates = append(updates,
		`{"sessionUpdate":"tool_call_update","toolCallId":"bounded-0","status":"completed"}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"bounded-4096","status":"completed"}`)
	r := progressRound(t, updates, false)
	for i := 0; i < 4098; i++ {
		e, err := r.Next(t.Context())
		if err != nil || e.Kind != inference.Progress {
			t.Fatalf("bounded tool activity lost: index=%d", i)
		}
	}
	e, err := r.Next(t.Context())
	if err != nil || e.Kind != inference.End || len(r.turn.progressTools) != 4096 {
		t.Fatal("progress tracking exceeded its bound or admitted an untracked update")
	}
	fresh := progressRound(t, []string{updates[len(updates)-2]}, false)
	e, err = fresh.Next(t.Context())
	if err != nil || e.Kind != inference.End {
		t.Fatal("tool activity from a previous prompt established fresh progress")
	}
	for _, size := range []int{1024, 1025} {
		fields, _ := json.Marshal(map[string]any{"sessionUpdate": "tool_call", "toolCallId": strings.Repeat("a", size), "title": "step"})
		candidate := progressRound(t, []string{string(fields)}, false)
		e, err := candidate.Next(t.Context())
		want := inference.Progress
		if size > 1024 {
			want = inference.End
		}
		if err != nil || e.Kind != want {
			t.Fatalf("progress identifier bound failed: bytes=%d", size)
		}
	}
}

func TestNonProgressUpdatesDoNotSatisfyFirstEvent(t *testing.T) {
	for name, update := range map[string]string{
		"unknown":             `{"sessionUpdate":"future_activity","content":{"type":"text","text":"opaque"}}`,
		"empty-thought":       `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":""}}`,
		"blank-thought":       `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"  \n"}}`,
		"nontext-thought":     `{"sessionUpdate":"agent_thought_chunk","content":{"type":"image","data":"opaque"}}`,
		"invalid-thought":     `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":7}}`,
		"empty-plan":          `{"sessionUpdate":"plan","entries":[]}`,
		"invalid-plan":        `{"sessionUpdate":"plan","entries":[{"content":"step","priority":"urgent","status":"pending"}]}`,
		"missing-plan-status": `{"sessionUpdate":"plan","entries":[{"content":"step","priority":"high"}]}`,
		"empty-title":         `{"sessionUpdate":"tool_call","toolCallId":"observed-1","title":""}`,
		"missing-id":          `{"sessionUpdate":"tool_call","title":"step"}`,
		"invalid-status":      `{"sessionUpdate":"tool_call","toolCallId":"observed-1","title":"step","status":"invented"}`,
		"invalid-kind":        `{"sessionUpdate":"tool_call","toolCallId":"observed-1","title":"step","kind":"invented"}`,
		"unknown-call":        `{"sessionUpdate":"tool_call_update","toolCallId":"unobserved","status":"completed"}`,
		"usage":               `{"sessionUpdate":"usage_update","used":100,"size":1000}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := progressRound(t, []string{update}, false)
			e, err := r.Next(t.Context())
			if err != nil || e.Kind != inference.End || r.text.Len() != 0 {
				t.Fatal("informational update changed completion or counted as progress")
			}
		})
	}
	for _, update := range []string{
		`{"sessionUpdate":"tool_call_update","toolCallId":"observed-1"}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"observed-1","status":null}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"observed-1","status":"invented"}`,
	} {
		r := progressRound(t, []string{`{"sessionUpdate":"tool_call","toolCallId":"observed-1","title":"step"}`, update}, false)
		_, _ = r.Next(t.Context())
		e, err := r.Next(t.Context())
		if err != nil || e.Kind != inference.End {
			t.Fatal("empty or malformed tool status counted as progress")
		}
	}
}

func TestForeignProgressDoesNotBecomeOwned(t *testing.T) {
	r := progressRound(t, []string{`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"other turn"}}`}, false)
	peer := r.turn.client.(*progressPeer)
	peer.queue[0].SessionID = "foreign"
	peer.queue[0].Params = json.RawMessage(strings.ReplaceAll(string(peer.queue[0].Params), "progress-owner", "foreign"))
	_, err := r.Next(t.Context())
	if !errors.Is(err, acp.ErrProtocol) {
		t.Fatal("foreign progress did not fail closed")
	}
}

func progressRound(t *testing.T, updates []string, late bool) *round {
	t.Helper()
	done := make(chan struct{})
	peer := &progressPeer{done: done, late: late}
	for _, update := range updates {
		peer.queue = append(peer.queue, acp.Notification{Method: "session/update", SessionID: "progress-owner", Params: json.RawMessage(`{"sessionId":"progress-owner","update":` + update + `}`)})
	}
	if !late {
		close(done)
	}
	return &round{turn: &turn{client: peer, id: "progress-owner", owned: t.Context(), done: done, result: json.RawMessage(`{"stopReason":"end_turn"}`)}}
}
