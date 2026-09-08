package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

func TestUnsupportedControlDoesNotEvictIdleBindingOrStartProcess(t *testing.T) {
	cfg := managerConfig(t, "pool-normal")
	cfg.MaxSessions = 1
	m, err := session.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	r := mainRequest(t, "retained")
	first, reply := managerTurn(t, m, r)
	before := m.Stats()
	bad := mainRequest(t, "rejected")
	bad.Extra["stop_sequences"] = json.RawMessage(`["private-marker"]`)
	if _, err := m.Start(t.Context(), bad); !errors.Is(err, anthropic.ErrRequest) {
		t.Fatal("unmapped control reached a session")
	}
	if after := m.Stats(); after != before {
		t.Fatal("rejected control mutated process admission")
	}
	r.Messages = append(r.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: reply}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "next synthetic input"}}})
	next, _ := managerTurn(t, m, r)
	if next.PID != first.PID || next.Session != first.Session || next.Count != 2 {
		t.Fatal("rejected control evicted unrelated idle conversation state")
	}
}

func TestDriverRejectsUnsupportedControlBeforeSetup(t *testing.T) {
	d := driver(t, "normal")
	r := sample(t)
	r.Extra["mcp_servers"] = json.RawMessage(`[{"url":"https://private.invalid"}]`)
	if _, err := d.Start(t.Context(), r); !errors.Is(err, anthropic.ErrRequest) || d.State() != session.Unstarted {
		t.Fatal("unsupported server execution directive reached ACP setup")
	}
}

func TestUnsupportedControlDoesNotConsumePendingToolResult(t *testing.T) {
	d := toolDriver(t, "chat-tools", 2*time.Second)
	r := toolRequest(t)
	turn, err := d.Start(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	prefix, uses := toolHandoff(t, turn)
	turn.Finish()
	next := followup(t, r, prefix, uses)
	bad := *next
	bad.Extra = maps.Clone(next.Extra)
	bad.Extra["stop_sequences"] = json.RawMessage(`["private-stop"]`)
	if _, err := d.Start(t.Context(), &bad); !errors.Is(err, anthropic.ErrRequest) || d.State() != session.WaitingTools {
		t.Fatal("unsupported constraint changed the pending tool state")
	}
	resumed, err := d.Start(t.Context(), next)
	if err != nil {
		t.Fatal("valid result could not resume after control rejection", err)
	}
	text, err := collect(t.Context(), resumed)
	if err != nil || !strings.Contains(text, `"promptCount":1`) || !strings.Contains(text, "synthetic tool result") {
		resumed.Cancel()
		t.Fatal("control rejection consumed the tool result or replaced its ACP prompt")
	}
	resumed.Finish()
}

func TestInvalidClientModelDoesNotCancelActiveSibling(t *testing.T) {
	m := manager(t, "pool-hang")
	active, err := m.Start(context.Background(), mainRequest(t, "active"))
	if err != nil {
		t.Fatal(err)
	}
	defer active.Cancel()
	bad := mainRequest(t, "invalid")
	bad.Model = "claude-dax-absent"
	if _, err = m.Start(context.Background(), bad); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("invalid model was not rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = active.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("client validation failure retired a healthy active sibling")
	}
}
