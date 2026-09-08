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
