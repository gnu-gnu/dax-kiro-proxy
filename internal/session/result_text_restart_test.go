package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

func successfulResultText(t *testing.T, r *anthropic.Request) {
	t.Helper()
	i := r.LatestUserIndex()
	var result map[string]any
	if i < 0 || len(r.Messages[i].Content) != 1 || json.Unmarshal(r.Messages[i].Content[0].Raw, &result) != nil {
		t.Fatal("invalid owned result fixture")
	}
	result["is_error"] = false
	r.Messages[i].Content[0].Raw, _ = json.Marshal(result)
	r.Messages[i].Content = append(r.Messages[i].Content, anthropic.Block{Type: "text", Text: "Next independent question. Apply the independent client expansion."})
}

func TestSuccessfulResultWithClientTextRecreatesExactHistory(t *testing.T) {
	const frameLimit = 64 << 10
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "repeated_standing", true: "changed_standing"}[changed], func(t *testing.T) {
			d := toolDriver(t, "chat-tools-restart", 10*time.Second, func(c *session.Config) { c.TurnTimeout = 10 * time.Second; c.Process.Limits.FrameBytes = frameLimit })
			next, old := instructionHandoff(t, t.Context(), d, false)
			successfulResultText(t, next)
			i := next.LatestUserIndex()
			if !changed {
				next.Messages[i+1] = next.Messages[i-2]
			}
			for index, mutate := range []func(*anthropic.Request){
				func(r *anthropic.Request) { r.Identity.Session = "other-client" },
				func(r *anthropic.Request) { r.Messages = r.Messages[2:] },
				func(r *anthropic.Request) { r.Messages[0].Content[0].Text = "Changed previous question" },
				func(r *anthropic.Request) {
					r.System = []anthropic.Block{{Type: "text", Text: "Changed top-level policy"}}
				},
				func(r *anthropic.Request) {
					r.Messages[i].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"foreign","content":"Unowned result"}`)
				},
				func(r *anthropic.Request) {
					r.Messages[i].Content = append(r.Messages[i].Content, r.Messages[i].Content[0])
				},
				func(r *anthropic.Request) {
					r.Messages[i].Content[0], r.Messages[i].Content[1] = r.Messages[i].Content[1], r.Messages[i].Content[0]
				},
				func(r *anthropic.Request) { r.Messages[i].Content[1].Text = " \n " },
				func(r *anthropic.Request) {
					r.Messages[i].Content[1] = anthropic.Block{Type: "image", Raw: json.RawMessage(`{"type":"image","source":{"type":"url","url":"https://example.invalid/owned"}}`)}
				},
				func(r *anthropic.Request) { r.Messages[i].Content[1].Text = strings.Repeat("x", frameLimit) },
				func(r *anthropic.Request) {
					r.Messages = append(r.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Additional changed sequence"}}})
				},
			} {
				bad := cloneInterruptionRequest(next)
				mutate(bad)
				candidate, err := d.Start(t.Context(), bad)
				if !errors.Is(err, inference.ErrRequest) || d.State() != session.WaitingTools || syscall.Kill(old, 0) != nil {
					if candidate != nil {
						candidate.Cancel()
					}
					t.Fatalf("invalid text continuation %d consumed owned work: %v", index, err)
				}
			}
			got, text := observedTurn(t, d, next)
			if got.PID == old || got.Count != 1 || !strings.Contains(text, "independent client expansion") || !strings.Contains(text, "synthetic tool result") || !strings.Contains(text, "Earlier independent question") || !errors.Is(syscall.Kill(-old, 0), syscall.ESRCH) {
				t.Fatal("text continuation lost history or reused pending work")
			}
			if _, err := d.Start(t.Context(), next); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("completed result replay accepted")
			}
		})
	}
}

func TestSuccessfulResultTextRetainsDeadlineAndFailedReplacementCleanup(t *testing.T) {
	for _, mode := range []string{"chat-tools-system-hold", "chat-tools-system-fail"} {
		t.Run(mode, func(t *testing.T) {
			d := toolDriver(t, mode, 3*time.Second)
			ctx, cancel := context.WithTimeout(t.Context(), 1200*time.Millisecond)
			defer cancel()
			deadline, _ := ctx.Deadline()
			next, old := instructionHandoff(t, ctx, d, false)
			successfulResultText(t, next)
			turn, err := d.Start(t.Context(), next)
			if err == nil {
				_, err = collect(t.Context(), turn)
				turn.Cancel()
			}
			if err == nil || errors.Is(err, inference.ErrRequest) {
				t.Fatal("valid client text did not reach bounded replacement", err)
			}
			if mode == "chat-tools-system-hold" && (!errors.Is(err, context.DeadlineExceeded) || time.Now().After(deadline.Add(500*time.Millisecond))) {
				t.Fatal("client text reset original deadline", err)
			}
			if !errors.Is(syscall.Kill(-old, 0), syscall.ESRCH) || d.State() != session.Unstarted {
				t.Fatal("partial replacement survived")
			}
			if _, err := d.Start(t.Context(), next); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("expired successful text revived owned work")
			}
		})
	}
}

func TestSuccessfulResultTextSharesRecreationBudget(t *testing.T) {
	d := toolDriver(t, "chat-tools-system-repeat", 10*time.Second, func(c *session.Config) { c.MaxRecreations = 1; c.TurnTimeout = 10 * time.Second })
	next, first := instructionHandoff(t, t.Context(), d, false)
	successfulResultText(t, next)
	last, second := registryHandoff(t, t.Context(), d, next)
	last.Messages = append(last.Messages, next.Messages[len(next.Messages)-1])
	plain := cloneInterruptionRequest(last)
	successfulResultText(t, last)
	if _, err := d.Start(t.Context(), last); !errors.Is(err, inference.ErrRequest) || d.State() != session.WaitingTools || syscall.Kill(second, 0) != nil {
		t.Fatal("client text bypassed recreation budget")
	}
	turn, err := d.Start(t.Context(), plain)
	if err != nil {
		t.Fatal("budget rejection consumed pending result", err)
	}
	if _, err := collect(t.Context(), turn); err != nil {
		turn.Cancel()
		t.Fatal(err)
	}
	turn.Finish()
	if first == second || !errors.Is(syscall.Kill(-first, 0), syscall.ESRCH) || d.State() != session.Idle {
		t.Fatal("text replacement did not join old process")
	}
}
