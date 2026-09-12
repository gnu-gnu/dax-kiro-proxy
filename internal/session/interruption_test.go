package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

func TestNewQuestionAfterDeniedToolsRecreatesWithoutReplaying(t *testing.T) {
	for _, tc := range []struct{ expired, rotated bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		expired := tc.expired
		t.Run(fmt.Sprintf("expired=%v/rotated=%v", expired, tc.rotated), func(t *testing.T) {
			timeout := 3 * time.Second
			if expired {
				timeout = 100 * time.Millisecond
			}
			d := toolDriver(t, "chat-tools-restart", timeout, func(c *session.Config) { c.TurnTimeout = 5 * time.Second })
			r := toolRequest(t)
			standing := anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Preserve the client permission boundary."}}}
			r.Messages = append(r.Messages, standing)
			turn, err := d.Start(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			prefix, uses := toolHandoff(t, turn)
			turn.Finish()
			var oldPID int
			if n, _ := fmt.Sscanf(prefix, "first process %d", &oldPID); n != 1 || oldPID <= 1 {
				t.Fatal("missing independent process identity")
			}
			next := followup(t, r, prefix, uses)
			i := next.LatestUserIndex()
			next.Messages[i].Content = append(next.Messages[i].Content,
				anthropic.Block{Type: "text", Text: "The previous operation was declined."},
				anthropic.Block{Type: "text", Text: "Next independent question."})
			// A rotated one-message standing instruction is accepted on this fresh-prompt path (D123).
			trailing := standing
			if tc.rotated {
				trailing = anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Rotated standing after denial."}}}
			}
			next.Messages = append(next.Messages, trailing)
			if expired {
				until := time.Now().Add(2 * time.Second)
				for d.State() != session.Unstarted && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if d.State() != session.Unstarted {
					t.Fatal("old tool deadline failed to retire")
				}
			}
			before := d.State()
			for _, mutate := range []func(*anthropic.Request){
				func(b *anthropic.Request) { b.Model = "claude-dax-absent" },
				func(b *anthropic.Request) { b.Identity.Session = "unrelated-conversation" },
				func(b *anthropic.Request) { b.Messages[0].Content[0].Text = "changed original request" },
				// A rotated one-message standing instruction is accepted on this fresh-prompt path (D123);
				// a stacked sequence or a non-text standing block still rejects.
				func(b *anthropic.Request) { b.Messages = append(b.Messages, b.Messages[len(b.Messages)-1]) },
				func(b *anthropic.Request) {
					b.Messages[len(b.Messages)-1].Content = []anthropic.Block{{Type: "image", Raw: json.RawMessage(`{"type":"image"}`)}}
				},
				func(b *anthropic.Request) {
					b.Messages[i].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"unrelated","is_error":true,"content":"denied"}`)
				},
				func(b *anthropic.Request) {
					b.Messages[i].Content = append(b.Messages[i].Content, b.Messages[i].Content[0])
				},
				func(b *anthropic.Request) { b.Messages[i].Content[1].Text = ""; b.Messages[i].Content[2].Text = "  " },
			} {
				bad := cloneInterruptionRequest(next)
				mutate(bad)
				if _, err := d.Start(t.Context(), bad); !errors.Is(err, inference.ErrRequest) {
					t.Fatal("invalid recovery was not rejected", err)
				}
				if d.State() != before {
					t.Fatal("invalid recovery consumed pending ownership")
				}
			}
			if expired {
				// A live successful result plus text uses bounded continuation recreation. It
				// cannot revive the retired denial's separate new-question recovery window.
				bad := cloneInterruptionRequest(next)
				bad.Messages[i].Content[0].Raw = json.RawMessage(strings.Replace(string(bad.Messages[i].Content[0].Raw), `"is_error":true`, `"is_error":false`, 1))
				if _, err := d.Start(t.Context(), bad); !errors.Is(err, inference.ErrRequest) || d.State() != before {
					t.Fatal("expired successful result revived retired work", err)
				}
			}
			got, text := observedTurn(t, d, next)
			if got.PID == oldPID || got.Count != 1 || len(got.Prompt) < 2 || !strings.Contains(text, "Next independent question.") || !strings.Contains(text, "synthetic tool result") || !strings.Contains(text, "request one synthetic tool") {
				t.Fatal("recovery reused the old process, replayed a tool, or omitted full client history")
			}
			if tc.rotated && !strings.Contains(text, "Rotated standing after denial.") {
				t.Fatal("rotated standing instruction was not carried by the fresh full prompt")
			}
			if !errors.Is(syscall.Kill(oldPID, 0), syscall.ESRCH) {
				t.Fatal("old process survived recovery")
			}
			if _, err := d.Start(context.Background(), next); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("recovery replay was accepted")
			}
		})
	}
}

func cloneInterruptionRequest(r *anthropic.Request) *anthropic.Request {
	b := *r
	b.Messages = append([]anthropic.Message(nil), r.Messages...)
	for i := range b.Messages {
		b.Messages[i].Content = append([]anthropic.Block(nil), r.Messages[i].Content...)
	}
	return &b
}
