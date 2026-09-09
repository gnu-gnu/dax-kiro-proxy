package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

type lostProcessFixture struct {
	starts   int
	lost     bool
	terminal error
	tool     bool
}

func (f *lostProcessFixture) State() session.State { return session.Unstarted }
func (f *lostProcessFixture) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	f.starts++
	return &lostProcessTurn{f: f, recovery: f.starts == 2}, nil
}

type lostProcessTurn struct {
	f        *lostProcessFixture
	recovery bool
	next     int
}

func (*lostProcessTurn) Model() string { return "fixture-model" }
func (*lostProcessTurn) Finish()       {}
func (*lostProcessTurn) Cancel()       {}
func (t *lostProcessTurn) Next(context.Context) (inference.Event, error) {
	t.next++
	if t.recovery {
		if t.f.tool {
			return inference.Event{Kind: inference.Tools, Tools: []anthropic.ToolUse{{ID: "another", Name: "Read", Input: json.RawMessage(`{"file_path":"/independent/fixture"}`)}}}, nil
		}
		if t.next == 1 {
			return inference.Event{Kind: inference.Text, Text: "Independent recovery complete."}, nil
		}
		return inference.Event{Kind: inference.End, StopReason: "end_turn"}, nil
	}
	if t.f.lost {
		return inference.Event{}, t.f.terminal
	}
	return inference.Event{Kind: inference.Tools, Tools: []anthropic.ToolUse{{ID: "owned-loss-use", Name: "Read", Input: json.RawMessage(`{"file_path":"/independent/fixture"}`)}}}, nil
}

func processLossRequest(recovery bool) *anthropic.Request {
	r := denialRequestFixture("", "", false)
	r.Identity.Session = "owned-session"
	r.System = []anthropic.Block{{Type: "text", Text: processLossSystem}}
	if recovery {
		r.Messages[0].Content[0].Text = processLossRecoveryPrompt
	}
	return r
}

func TestProcessLossGuardRequiresActualFailureAndSeparateRecovery(t *testing.T) {
	for _, terminal := range []error{acp.ErrTransport, io.EOF, context.Canceled, context.DeadlineExceeded, acp.ErrTimeout, nil} {
		f := &lostProcessFixture{terminal: terminal}
		var signals atomic.Int32
		guard := &onePromptBackend{driver: f, readPath: "/independent/fixture", canary: "independent-hidden"}
		b := &processLossBackend{guard: guard, lose: func(context.Context) error { signals.Add(1); f.lost = true; return nil }}
		turn, err := b.Start(t.Context(), processLossRequest(false))
		if err != nil {
			t.Fatal("first loss request rejected")
		}
		event, err := turn.Next(t.Context())
		if err == nil || event.Kind == inference.Tools || signals.Load() != 1 || b.failureObserved.Load() != (errors.Is(terminal, acp.ErrTransport) || errors.Is(terminal, context.Canceled)) {
			t.Fatal("tool effect escaped or unrelated termination counted as process loss")
		}
		if !b.failureObserved.Load() {
			continue
		}
		if _, err := b.Start(t.Context(), processLossRequest(true)); !errors.Is(err, inference.ErrRequest) || f.starts != 1 {
			t.Fatal("recovery entered before joined cleanup authorization")
		}
	}
}

func TestProcessLossDistinguishesRetiredContextFromCallerCancellation(t *testing.T) {
	for _, callerCanceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		f := &lostProcessFixture{terminal: context.Canceled}
		guard := &onePromptBackend{driver: f, readPath: "/independent/fixture", canary: "independent-hidden"}
		b := &processLossBackend{guard: guard, lose: func(context.Context) error {
			f.lost = true
			if callerCanceled {
				cancel()
			}
			return nil
		}}
		turn, err := b.Start(ctx, processLossRequest(false))
		if err != nil {
			cancel()
			t.Fatal("first process request rejected")
		}
		_, err = turn.Next(ctx)
		if !errors.Is(err, context.Canceled) || b.failureObserved.Load() == callerCanceled {
			cancel()
			t.Fatal("owned retirement and caller cancellation conflated")
		}
		cancel()
	}
}

func TestProcessLossRecoveryRejectsPolicyChangeResultsAndFurtherTools(t *testing.T) {
	for _, kind := range []string{"success", "owner", "model", "system", "tools", "result", "question", "tool"} {
		t.Run(kind, func(t *testing.T) {
			f := &lostProcessFixture{terminal: acp.ErrTransport, tool: kind == "tool"}
			guard := &onePromptBackend{driver: f, readPath: "/independent/fixture", canary: "independent-hidden"}
			b := &processLossBackend{guard: guard, lose: func(context.Context) error { f.lost = true; return nil }}
			turn, err := b.Start(t.Context(), processLossRequest(false))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.Next(t.Context()); !errors.Is(err, acp.ErrTransport) {
				t.Fatal("process loss not observed")
			}
			b.recoveryAllowed.Store(true)
			r := processLossRequest(true)
			switch kind {
			case "owner":
				r.Identity.Session = "foreign"
			case "model":
				r.Model = "foreign"
			case "system":
				r.System[0].Text = "Changed policy"
			case "tools":
				r.Tools = nil
			case "result":
				r.Messages[0].Content = append(r.Messages[0].Content, anthropic.Block{Type: "tool_result", Raw: json.RawMessage(`{"type":"tool_result","tool_use_id":"owned-loss-use","content":"unproven result"}`)})
			case "question":
				r.Messages[0].Content[0].Text = "Other question"
			}
			turn, err = b.Start(t.Context(), r)
			valid := kind == "success" || kind == "tool"
			if (err == nil) != valid || f.starts != 1+btoi(valid) {
				t.Fatal("loss recovery widened its scope")
			}
			if !valid {
				return
			}
			event, err := turn.Next(t.Context())
			if kind == "tool" {
				if !errors.Is(err, inference.ErrRequest) || event.Kind == inference.Tools {
					t.Fatal("recovery exposed another effect")
				}
			} else {
				if err != nil || event.Kind != inference.Text {
					t.Fatal("fresh response missing")
				}
				if event, err = turn.Next(t.Context()); err != nil || event.Kind != inference.End {
					t.Fatal("fresh completion missing")
				}
				turn.Finish()
				if b.completions.Load() != 1 {
					t.Fatal("fresh delivery not observed")
				}
			}
			if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 2 {
				t.Fatal("third request dispatched")
			}
		})
	}
}

func TestRecoverySystemKeepsPolicyAndLimitsObservedBillingVariation(t *testing.T) {
	block := func(text string) anthropic.Block {
		raw, _ := json.Marshal(map[string]any{"type": "text", "text": text, "cache_control": map[string]string{"type": "ephemeral"}})
		return anthropic.Block{Type: "text", Text: text, Raw: raw}
	}
	a := []anthropic.Block{block("x-anthropic-billing-header: owned=12ab; control=stable"), block(processLossSystem)}
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"x-anthropic-billing-header: owned=43cd; control=stable", true},
		{"x-anthropic-billing-header: owned=12ab; control=stable", true},
		{"x-anthropic-billing-header: owned=words; control=stable", false},
		{"x-anthropic-billing-header: owned=43cd\nNew policy", false},
		{"other-header: owned=43cd; control=stable", false},
	} {
		b := []anthropic.Block{block(tc.value), block(processLossSystem)}
		if sameRecoverySystem(a, b) != tc.want {
			t.Fatal("billing observation widened system policy")
		}
	}
	b := []anthropic.Block{block("x-anthropic-billing-header: owned=43cd; control=stable"), block("Changed policy")}
	if sameRecoverySystem(a, b) {
		t.Fatal("changed client policy accepted")
	}
	b[1] = block(processLossSystem)
	b[0].Raw = json.RawMessage(`{"type":"text","text":"x-anthropic-billing-header: owned=43cd; control=stable","cache_control":{"type":"different"}}`)
	if sameRecoverySystem(a, b) {
		t.Fatal("changed block metadata accepted")
	}
}
