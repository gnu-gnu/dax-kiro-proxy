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
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

func instructionHandoff(t *testing.T, ctx context.Context, d *session.Driver) (*anthropic.Request, int) {
	t.Helper()
	r := toolRequest(t)
	r.Messages = append([]anthropic.Message{
		{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "Earlier independent question."}}},
		{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: "Earlier independent answer."}}},
	}, r.Messages...)
	r.Messages = append(r.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Original fixture standing instruction."}}})
	turn, err := d.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	text, uses := toolHandoff(t, turn)
	turn.Finish()
	var pid int
	if n, _ := fmt.Sscanf(text, "first process %d", &pid); n != 1 || pid <= 1 {
		t.Fatal("missing owned ACP identity")
	}
	next := followup(t, r, text, uses)
	next.Messages = append(next.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Updated fixture standing instruction."}}})
	return next, pid
}

func TestChangedStandingInstructionRecreatesFromCompleteOwnedHistory(t *testing.T) {
	for _, isError := range []bool{false, true} {
		t.Run(fmt.Sprintf("error=%v", isError), func(t *testing.T) {
			d := toolDriver(t, "chat-tools-system-restart", 3*time.Second)
			next, oldPID := instructionHandoff(t, t.Context(), d)
			i := next.LatestUserIndex()
			h := history.New([32]byte{1})
			pending, err := h.Pending(next.Messages[:i-1], next.Messages[i-1].Content)
			truncated := cloneInterruptionRequest(next)
			truncated.Messages = truncated.Messages[2:]
			plan, planErr := h.Plan(pending, truncated)
			if err != nil || planErr != nil || plan.Mode != history.Extend {
				t.Fatal("control did not establish otherwise valid truncated overlap")
			}
			if !isError {
				next.Messages[i].Content[0].Raw = json.RawMessage(strings.Replace(string(next.Messages[i].Content[0].Raw), `"is_error":true`, `"is_error":false`, 1))
			}
			for _, mutate := range []func(*anthropic.Request){
				func(r *anthropic.Request) { r.Identity.Session = "another-owner" },
				func(r *anthropic.Request) { r.Model = "claude-dax-absent" },
				func(r *anthropic.Request) { r.Effort = "high" },
				func(r *anthropic.Request) {
					r.Tools = []json.RawMessage{json.RawMessage(`{"name":"invalid","input_schema":{"type":"array"}}`)}
				},
				func(r *anthropic.Request) { r.Messages[0].Content[0].Text = "altered history" },
				func(r *anthropic.Request) { r.Messages = r.Messages[1:] },
				func(r *anthropic.Request) { r.Messages = r.Messages[2:] },
				func(r *anthropic.Request) { r.Messages[i].Content = nil },
				func(r *anthropic.Request) {
					r.Messages[i].Content = append(r.Messages[i].Content, r.Messages[i].Content[0])
				},
				func(r *anthropic.Request) {
					r.Messages[i].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"other-use","content":"synthetic"}`)
				},
				func(r *anthropic.Request) { r.Messages[len(r.Messages)-1].Role = "assistant" },
				func(r *anthropic.Request) { r.Messages = append(r.Messages, r.Messages[len(r.Messages)-1]) },
			} {
				bad := cloneInterruptionRequest(next)
				mutate(bad)
				if _, err := d.Start(t.Context(), bad); !errors.Is(err, inference.ErrRequest) && !errors.Is(err, inference.ErrBusy) {
					t.Fatal("invalid instruction recovery accepted", err)
				}
				if d.State() != session.WaitingTools || syscall.Kill(oldPID, 0) != nil {
					t.Fatal("invalid instruction recovery retired pending ownership")
				}
			}
			got, text := observedTurn(t, d, next)
			if got.PID == oldPID || got.Count != 1 || !strings.Contains(text, "Original fixture standing instruction.") || !strings.Contains(text, "Updated fixture standing instruction.") || !strings.Contains(text, "synthetic tool result") || !strings.Contains(text, "request one synthetic tool") {
				t.Fatal("instruction recovery omitted history or reused old work")
			}
			if !errors.Is(syscall.Kill(-oldPID, 0), syscall.ESRCH) {
				t.Fatal("old process group survived recovery")
			}
			if _, err := d.Start(t.Context(), next); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("instruction recovery replay accepted")
			}
		})
	}
}

func TestInstructionRecoveryPreservesDeadlineAndJoinsFailedReplacement(t *testing.T) {
	for _, mode := range []string{"chat-tools-system-hold", "chat-tools-system-fail"} {
		t.Run(mode, func(t *testing.T) {
			d := toolDriver(t, mode, 3*time.Second)
			ctx, cancel := context.WithTimeout(t.Context(), 1200*time.Millisecond)
			defer cancel()
			deadline, _ := ctx.Deadline()
			next, oldPID := instructionHandoff(t, ctx, d)
			// The next HTTP round carries a later context; recovery must keep the first deadline.
			turn, err := d.Start(t.Context(), next)
			if err == nil {
				_, err = collect(t.Context(), turn)
				turn.Cancel()
			}
			if err == nil {
				t.Fatal("unfinished replacement reported success")
			}
			if mode == "chat-tools-system-hold" && (!errors.Is(err, context.DeadlineExceeded) || time.Now().After(deadline.Add(500*time.Millisecond))) {
				t.Fatal("recovery reset the original deadline", err)
			}
			if !errors.Is(syscall.Kill(-oldPID, 0), syscall.ESRCH) || d.State() != session.Unstarted {
				t.Fatal("failed replacement retained old or partial state")
			}
			if _, err := d.Start(t.Context(), next); err == nil {
				t.Fatal("failed replacement replayed the tool result")
			}
		})
	}
}

func TestInstructionRecoveryBudgetCannotRestartEachToolHandoff(t *testing.T) {
	d := toolDriver(t, "chat-tools-system-repeat", 3*time.Second, func(c *session.Config) { c.MaxRecreations = 1 })
	next, oldPID := instructionHandoff(t, t.Context(), d)
	turn, err := d.Start(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	text, uses := toolHandoff(t, turn)
	turn.Finish()
	if !errors.Is(syscall.Kill(-oldPID, 0), syscall.ESRCH) {
		t.Fatal("first owner survived")
	}
	last := followup(t, next, text, uses)
	last.Messages = append(last.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Another fixture standing instruction."}}})
	if _, err := d.Start(t.Context(), last); !errors.Is(err, inference.ErrRequest) || d.State() != session.WaitingTools {
		t.Fatal("restart budget did not preserve pending ownership")
	}
	// Repeating the instruction already in this replacement still resumes the same prompt.
	last.Messages[len(last.Messages)-1] = next.Messages[len(next.Messages)-1]
	resumed, err := d.Start(t.Context(), last)
	if err != nil {
		t.Fatal(err)
	}
	output, err := collect(t.Context(), resumed)
	if err != nil || !strings.Contains(output, `"promptCount":1`) {
		t.Fatal("budget exhaustion broke a compatible continuation", err)
	}
	resumed.Finish()
}
