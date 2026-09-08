package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kiroauth"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func toolDriver(t *testing.T, mode string, timeout time.Duration) *session.Driver {
	t.Helper()
	validator, err := schemacheck.New(schemacheck.Config{Executable: relayBinary, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validator.Close)
	d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Args: []string{mode}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "dax-fixture", Version: "1"}, Auth: kiroauth.Classifier{}, Limits: acp.Limits{GracePeriod: 50 * time.Millisecond, TermPeriod: 50 * time.Millisecond, KillPeriod: time.Second}}, Validator: validator, RelayExecutable: relayBinary, RelayLimits: relay.Limits{ToolTimeout: timeout}, TurnTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}
func toolRequest(t *testing.T) *anthropic.Request {
	t.Helper()
	r, err := anthropic.DecodeRequest([]byte(`{"model":"` + fixtureClientID + `","max_tokens":128,"messages":[{"role":"user","content":"request one synthetic tool"}],"tools":[{"name":"client_action","input_schema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func toolHandoff(t *testing.T, turn inference.Turn) (string, []anthropic.ToolUse) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var text strings.Builder
	var uses []anthropic.ToolUse
	for {
		e, err := turn.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		switch e.Kind {
		case inference.Text:
			text.WriteString(e.Text)
		case inference.Tools:
			uses = append(uses, e.Tools...)
		case inference.End:
			if e.StopReason != "tool_use" || len(uses) != 1 {
				t.Fatal("invalid handoff")
			}
			return text.String(), uses
		}
	}
}
func followup(t *testing.T, original *anthropic.Request, text string, uses []anthropic.ToolUse) *anthropic.Request {
	t.Helper()
	r := *original
	r.Messages = append([]anthropic.Message(nil), original.Messages...)
	assistant := anthropic.Message{Role: "assistant"}
	if text != "" {
		raw, _ := json.Marshal(map[string]any{"type": "text", "text": text})
		assistant.Content = append(assistant.Content, anthropic.Block{Type: "text", Text: text, Raw: raw})
	}
	user := anthropic.Message{Role: "user"}
	for _, use := range uses {
		raw, _ := json.Marshal(use.Block())
		assistant.Content = append(assistant.Content, anthropic.Block{Type: "tool_use", Raw: raw})
		raw, _ = json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": use.ID, "content": "synthetic tool result", "is_error": true})
		user.Content = append(user.Content, anthropic.Block{Type: "tool_result", Raw: raw})
	}
	r.Messages = append(r.Messages, assistant, user)
	return &r
}
func TestToolHandoffSurvivesHTTPContextAndResumesSamePrompt(t *testing.T) {
	d := toolDriver(t, "chat-tools", time.Second)
	r := toolRequest(t)
	ctx, cancel := context.WithCancel(context.Background())
	turn, err := d.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	prefix, uses := toolHandoff(t, turn)
	if _, err := d.Start(context.Background(), followup(t, r, prefix, uses)); !errors.Is(err, inference.ErrBusy) {
		t.Fatal("result admitted before response completion")
	}
	turn.Finish()
	cancel()
	if d.State() != session.WaitingTools {
		t.Fatal("successful handoff canceled the owned ACP turn")
	}
	next := followup(t, r, prefix, uses)
	changed := *next
	changed.Model = "claude-dax-wrong"
	if _, err := d.Start(context.Background(), &changed); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("model changed while waiting for tools")
	}
	resumed, err := d.Start(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	text, err := collect(context.Background(), resumed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `"promptCount":1`) || !strings.Contains(text, `"isError":true`) {
		t.Fatal("result lost or a second prompt was sent: " + text)
	}
	resumed.Finish()
	if d.State() != session.Idle {
		t.Fatal("completed tool turn not idle")
	}
	if _, err := d.Start(context.Background(), next); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("tool result replay started a new prompt")
	}
}
func TestToolWaitExpiresWithoutAnHTTPConsumer(t *testing.T) {
	d := toolDriver(t, "chat-tools", 70*time.Millisecond)
	r := toolRequest(t)
	turn, err := d.Start(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	prefix, uses := toolHandoff(t, turn)
	turn.Finish()
	until := time.Now().Add(2 * time.Second)
	for d.State() != session.Unstarted && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if d.State() != session.Unstarted {
		t.Fatal("orphaned tool wait was not cleaned up")
	}
	if _, err := d.Start(context.Background(), followup(t, r, prefix, uses)); err == nil {
		t.Fatal("late results resumed expired state")
	}
}
func TestToolDisconnectBeforeHandoffDiscardsState(t *testing.T) {
	d := toolDriver(t, "chat-tools", time.Second)
	r := toolRequest(t)
	turn, err := d.Start(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = toolHandoff(t, turn)
	turn.Cancel()
	if d.State() != session.Unstarted {
		t.Fatal("undelivered tool state retained")
	}
}

func TestFirstHTTPDeadlineIsNotResetByToolHandoff(t *testing.T) {
	d := toolDriver(t, "chat-tools", 2*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	started := time.Now()
	turn, err := d.Start(ctx, toolRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = toolHandoff(t, turn)
	turn.Finish()
	cancel()
	// Returning from the response cancels its context; only the retained absolute deadline applies.
	if d.State() != session.WaitingTools {
		t.Fatal("HTTP return canceled the underlying prompt")
	}
	until := started.Add(1500 * time.Millisecond)
	for d.State() != session.Unstarted && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if d.State() != session.Unstarted {
		t.Fatal("tool handoff reset the initial total HTTP deadline")
	}
}
