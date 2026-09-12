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

// instructionHandoff drives one tool handoff under an original standing instruction and returns
// the client's continuation carrying a changed standing instruction. With text, the results are
// successful and followed by client text, which takes the D73 recreation path; without text the
// result-only continuation is deferred to the next prompt (D123).
func instructionHandoff(t *testing.T, ctx context.Context, d *session.Driver, withText bool) (*anthropic.Request, int) {
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
	if withText {
		i := next.LatestUserIndex()
		for j := range next.Messages[i].Content {
			next.Messages[i].Content[j].Raw = json.RawMessage(strings.Replace(string(next.Messages[i].Content[j].Raw), `"is_error":true`, `"is_error":false`, 1))
		}
		raw, _ := json.Marshal(map[string]any{"type": "text", "text": "Continue under the updated instruction."})
		next.Messages[i].Content = append(next.Messages[i].Content, anthropic.Block{Type: "text", Text: "Continue under the updated instruction.", Raw: raw})
	}
	next.Messages = append(next.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Updated fixture standing instruction."}}})
	return next, pid
}

func TestChangedStandingInstructionDefersToTheNextPrompt(t *testing.T) {
	for _, isError := range []bool{false, true} {
		t.Run(fmt.Sprintf("error=%v", isError), func(t *testing.T) {
			d := toolDriver(t, "chat-tools-restart", 3*time.Second)
			next, oldPID := instructionHandoff(t, t.Context(), d, false)
			i := next.LatestUserIndex()
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
				func(r *anthropic.Request) { r.Messages[i].Content = nil },
				func(r *anthropic.Request) {
					r.Messages[i].Content = append(r.Messages[i].Content, r.Messages[i].Content[0])
				},
				func(r *anthropic.Request) {
					r.Messages[i].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"other-use","content":"synthetic"}`)
				},
				func(r *anthropic.Request) { r.Messages[len(r.Messages)-1].Role = "assistant" },
				func(r *anthropic.Request) { r.Messages = append(r.Messages, r.Messages[len(r.Messages)-1]) },
				func(r *anthropic.Request) {
					r.Messages[len(r.Messages)-1].Content = []anthropic.Block{{Type: "image", Raw: json.RawMessage(`{"type":"image"}`)}}
				},
			} {
				bad := cloneInterruptionRequest(next)
				mutate(bad)
				if _, err := d.Start(t.Context(), bad); !errors.Is(err, inference.ErrRequest) && !errors.Is(err, inference.ErrBusy) {
					t.Fatal("invalid rotated continuation accepted", err)
				}
				if d.State() != session.WaitingTools || syscall.Kill(oldPID, 0) != nil {
					t.Fatal("invalid rotated continuation retired pending ownership")
				}
			}
			// A truncated overlap is not rejected here: like a repeated standing instruction, the
			// deferred one delivers results into the existing backend turn without re-projecting
			// history, so the recreation path's exact-prefix rule does not apply.
			// The result-only continuation with a rotated standing message resumes the same backend
			// turn: the pending relay call receives the result and the original process answers.
			turn, err := d.Start(t.Context(), next)
			if err != nil {
				t.Fatal("rotated standing instruction was not deferred", err)
			}
			text, err := collect(t.Context(), turn)
			if err != nil || !strings.Contains(text, `"promptCount":1`) || !strings.Contains(text, "synthetic tool result") {
				turn.Cancel()
				t.Fatal("deferred continuation did not resume the pending prompt", err)
			}
			turn.Finish()
			if syscall.Kill(oldPID, 0) != nil || d.State() != session.Idle {
				t.Fatal("deferral recreated the backend process")
			}
			if _, err := d.Start(t.Context(), next); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("completed result was replayed")
			}
			// The next ordinary question extends the recorded history, including the rotated message,
			// and the same process receives that request's own standing message with the prompt; neither
			// the original nor the rotated text is replayed.
			question := cloneInterruptionRequest(next)
			raw, _ := json.Marshal(map[string]any{"type": "text", "text": text})
			question.Messages = append(question.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: text, Raw: raw}}})
			raw, _ = json.Marshal(map[string]any{"type": "text", "text": "Next independent question."})
			question.Messages = append(question.Messages, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "Next independent question.", Raw: raw}}})
			question.Messages = append(question.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Third fixture standing instruction."}}})
			got, echoed := observedTurn(t, d, question)
			if got.PID != oldPID || got.Count != 2 || !strings.Contains(echoed, "Third fixture standing instruction.") || !strings.Contains(echoed, "Next independent question.") || strings.Contains(echoed, "Original fixture standing instruction.") || strings.Contains(echoed, "Updated fixture standing instruction.") {
				t.Fatal("next prompt after a deferred rotation did not stay on the same process with its own standing message", got.PID, got.Count)
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
			next, oldPID := instructionHandoff(t, ctx, d, true)
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
	next, oldPID := instructionHandoff(t, t.Context(), d, true)
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
	i := last.LatestUserIndex()
	for j := range last.Messages[i].Content {
		last.Messages[i].Content[j].Raw = json.RawMessage(strings.Replace(string(last.Messages[i].Content[j].Raw), `"is_error":true`, `"is_error":false`, 1))
	}
	raw, _ := json.Marshal(map[string]any{"type": "text", "text": "Continue once more."})
	last.Messages[i].Content = append(last.Messages[i].Content, anthropic.Block{Type: "text", Text: "Continue once more.", Raw: raw})
	last.Messages = append(last.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Another fixture standing instruction."}}})
	if _, err := d.Start(t.Context(), last); !errors.Is(err, inference.ErrRequest) || d.State() != session.WaitingTools {
		t.Fatal("restart budget did not preserve pending ownership")
	}
	// A result-only continuation never consumes the recreation budget: rotating the standing
	// instruction alone resumes the same prompt even after the budget is exhausted.
	last.Messages[i].Content = last.Messages[i].Content[:len(last.Messages[i].Content)-1]
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
