package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func managerConfig(t *testing.T, mode string) session.ManagerConfig {
	return session.ManagerConfig{ProfileScope: "synthetic-profile", MaxSessions: 4, Session: session.Config{Process: acp.Config{Executable: fixture, Args: []string{mode}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "dax-manager-fixture", Version: "0"}, Limits: acp.Limits{GracePeriod: 20 * time.Millisecond, TermPeriod: 20 * time.Millisecond, KillPeriod: time.Second}}, TurnTimeout: 2 * time.Second}}
}
func manager(t *testing.T, mode string) *session.Manager {
	t.Helper()
	m, err := session.NewManager(managerConfig(t, mode))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}

func TestManagerRetainsEvictedRelayCleanupFailure(t *testing.T) {
	for _, action := range []string{"evict", "prune"} {
		t.Run(action, func(t *testing.T) {
			validator, err := schemacheck.New(schemacheck.Config{Executable: relayBinary, Directory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer validator.Close()
			cfg := managerConfig(t, "chat-tools-stopped-idle")
			cfg.MaxSessions = 1
			cfg.Session.Validator = validator
			cfg.Session.RelayExecutable = relayBinary
			if action == "prune" {
				cfg.IdleTTL = 10 * time.Millisecond
			}
			m, err := session.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			request := toolRequest(t)
			request.Identity.Session = "first-owned-binding"
			turn, err := m.Start(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			prefix, uses := toolHandoff(t, turn)
			turn.Finish()
			resumed, err := m.Start(t.Context(), followup(t, request, prefix, uses))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collect(t.Context(), resumed); err != nil {
				t.Fatal(err)
			}
			resumed.Finish()
			if action == "prune" {
				time.Sleep(20 * time.Millisecond)
				m.Prune()
			}
			next := *request
			next.Identity.Session = "second-owned-binding"
			turn, err = m.Start(t.Context(), &next)
			if turn != nil {
				turn.Cancel()
			}
			if !errors.Is(err, relay.ErrCleanup) {
				t.Fatal("retired relay cleanup failure did not stop admission")
			}
			if _, err := m.Models(t.Context()); !errors.Is(err, relay.ErrCleanup) {
				t.Fatal("discovery ignored retired cleanup failure")
			}
			if !errors.Is(m.Close(), relay.ErrCleanup) || !errors.Is(m.Close(), relay.ErrCleanup) {
				t.Fatal("manager shutdown lost retired cleanup failure")
			}
			if m.Stats().Processes != 0 {
				t.Fatal("retired manager kept an ACP process")
			}
		})
	}
}
func mainRequest(t *testing.T, id string) *anthropic.Request {
	r := sample(t)
	r.Identity.Session = id
	r.Messages = []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "synthetic first input"}}}}
	return r
}
func managerTurn(t *testing.T, m *session.Manager, r *anthropic.Request) (observedPrompt, string) {
	t.Helper()
	turn, err := m.Start(context.Background(), r)
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
		t.Fatal("invalid fake observation")
	}
	return got, text
}
func TestManagerIsolatesTitleAndMainAndPreservesThreeTurns(t *testing.T) {
	m := manager(t, "pool-normal")
	main := mainRequest(t, "client-session")
	title := mainRequest(t, "client-session")
	title.System = []anthropic.Block{{Type: "text", Text: "Create a short title for this conversation."}}
	title.Extra["thinking"] = json.RawMessage(`{"type":"disabled"}`)
	title.Extra["output_config"] = json.RawMessage(`{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}}`)
	var a, b observedPrompt
	var reply string
	var wg sync.WaitGroup
	wg.Go(func() { a, reply = managerTurn(t, m, main) })
	wg.Go(func() { b, _ = managerTurn(t, m, title) })
	wg.Wait()
	if a.PID == b.PID && a.Session == b.Session {
		t.Fatal("title captured main conversation")
	}
	for count := 2; count <= 3; count++ {
		main.Messages = append(main.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: reply}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "next synthetic input"}}})
		var got observedPrompt
		got, reply = managerTurn(t, m, main)
		if got.PID != a.PID || got.Session != a.Session || got.Count != count || len(got.Prompt) != 1 {
			t.Fatal("manager lost main-session continuity")
		}
	}
}
func TestIndependentClientIDsShareCompatibleProcessAndActiveKeysStayBounded(t *testing.T) {
	m := manager(t, "pool-normal")
	a, _ := managerTurn(t, m, mainRequest(t, "one"))
	b, _ := managerTurn(t, m, mainRequest(t, "two"))
	if a.PID != b.PID || a.Session == b.Session {
		t.Fatal("compatible client sessions did not share safely")
	}
	m2 := manager(t, "pool-hang")
	var turns []interface{ Cancel() }
	for _, id := range []string{"one", "two", "three", "four"} {
		turn, err := m2.Start(context.Background(), mainRequest(t, id))
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, turn)
	}
	if _, err := m2.Start(context.Background(), mainRequest(t, "one")); !errors.Is(err, session.ErrBusy) {
		t.Fatal("same key accepted a concurrent turn")
	}
	if _, err := m2.Start(context.Background(), mainRequest(t, "five")); !errors.Is(err, acp.ErrOverloaded) {
		t.Fatal("manager evicted active state to exceed capacity")
	}
	for _, turn := range turns {
		turn.Cancel()
	}
}
