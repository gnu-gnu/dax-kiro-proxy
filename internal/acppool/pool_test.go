package acppool_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
)

var fake string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dax-pool-fixture-")
	if err != nil {
		panic(err)
	}
	fake = filepath.Join(dir, "fake-acp")
	cmd := exec.Command("go", "build", "-o", fake, "../acp/testdata/fake")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Run() != nil {
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func config(t *testing.T, mode string) acppool.Config {
	return acppool.Config{Process: acp.Config{Executable: fake, Args: []string{mode}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "dax-pool-test", Version: "0"}, Limits: acp.Limits{GracePeriod: 20 * time.Millisecond, TermPeriod: 20 * time.Millisecond, KillPeriod: time.Second}}, MaxProcesses: 2, IdleTTL: 30 * time.Millisecond}
}
func pool(t *testing.T, mode string) *acppool.Pool {
	t.Helper()
	p, err := acppool.New(config(t, mode))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}
func acquire(t *testing.T, p *acppool.Pool, scope, load string) *acppool.Lease {
	t.Helper()
	l, err := p.Acquire(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Create(context.Background(), map[string]any{"cwd": filepath.Dir(fake), "mcpServers": []any{}}, load)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func drain(l *acppool.Lease) ([]acp.Notification, error) {
	var events []acp.Notification
	for {
		n, ok, err := l.TryNext()
		if err != nil {
			return events, err
		}
		if !ok {
			return events, nil
		}
		events = append(events, n)
	}
}

func TestEarlyCreationRoutingAndConcurrentResponseBarriers(t *testing.T) {
	p := pool(t, "pool-early")
	a := acquire(t, p, "compatible", "")
	b := acquire(t, p, "compatible", "")
	if a.PID() != b.PID() || a.ID() == b.ID() {
		t.Fatal("compatible sessions did not share one initialized process")
	}
	an, err := drain(a)
	if err != nil || len(an) != 2 {
		t.Fatal("established notifications were captured by session/new")
	}
	bn, err := drain(b)
	if err != nil || len(bn) != 1 {
		t.Fatal("early notification lost before creation reply")
	}
	for _, n := range an {
		if n.SessionID != a.ID() {
			t.Fatal("notification crossed session")
		}
	}
	if bn[0].SessionID != b.ID() {
		t.Fatal("early owner mismatched")
	}
	p2 := pool(t, "pool-concurrent")
	a = acquire(t, p2, "same", "")
	b = acquire(t, p2, "same", "")
	var group sync.WaitGroup
	for _, l := range []*acppool.Lease{a, b} {
		group.Go(func() {
			_, err := l.Call(context.Background(), "session/prompt", map[string]any{"sessionId": l.ID(), "prompt": []any{map[string]string{"type": "text", "text": "synthetic input"}}})
			if err != nil {
				t.Error(err)
				return
			}
			events, err := drain(l)
			if err != nil || len(events) != 32 {
				t.Errorf("final RPC reply preceded notification barrier: count=%d err=%v", len(events), err)
				return
			}
			for _, n := range events {
				if n.SessionID != l.ID() {
					t.Error("concurrent event crossed sessions")
				}
			}
		})
	}
	group.Wait()
}
func TestAmbiguousCreationAndCrashInvalidateAllAttachedSessions(t *testing.T) {
	p := pool(t, "pool-ambiguous")
	l, err := p.Acquire(context.Background(), "scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Create(context.Background(), map[string]any{"cwd": filepath.Dir(fake), "mcpServers": []any{}}, ""); err == nil {
		t.Fatal("ambiguous creation succeeded")
	}
	if l.Err() == nil {
		t.Fatal("ambiguous group remained healthy")
	}
	p = pool(t, "pool-hang")
	a := acquire(t, p, "scope", "")
	b := acquire(t, p, "scope", "")
	failed := make(chan error, 1)
	go func() {
		_, err := a.Call(context.Background(), "session/prompt", map[string]any{"sessionId": a.ID(), "prompt": []any{map[string]string{"type": "text", "text": "wait"}}})
		failed <- err
	}()
	_, _ = b.Call(context.Background(), "fixture/crash", map[string]string{"sessionId": b.ID()})
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("crash returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("shared-process waiter leaked")
	}
	if a.Err() == nil || b.Err() == nil {
		t.Fatal("crash did not invalidate both attached sessions")
	}
}
func TestCapacityCompatibilityAndIdleExpiry(t *testing.T) {
	p := pool(t, "pool-normal")
	a := acquire(t, p, "policy-A", "")
	b := acquire(t, p, "policy-B", "")
	if a.PID() == b.PID() {
		t.Fatal("different policy shared a process")
	}
	_ = acquire(t, p, "policy-A", "")
	_ = acquire(t, p, "policy-B", "")
	if _, err := p.Acquire(context.Background(), "policy-A"); !errors.Is(err, acp.ErrOverloaded) {
		t.Fatal("unbounded process/session admission")
	}
	if err := a.SetIdle(true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	p.Prune()
	if b.Err() != nil || a.Err() != nil || p.Stats().Processes != 2 {
		t.Fatal("partial idle group evicted active ownership")
	}
	p2 := pool(t, "pool-normal")
	l := acquire(t, p2, "scope", "")
	if err := l.SetIdle(true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	p2.Prune()
	if l.Err() == nil {
		t.Fatal("expired idle process remained reusable")
	}
	var callers sync.WaitGroup
	for range 8 {
		callers.Go(func() {
			if err := p2.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	callers.Wait()
	if p2.Stats().Processes != 0 {
		t.Fatal("repeated close left processes")
	}
}
func TestLoadReplayAndWrongSessionRequestsAreRejected(t *testing.T) {
	p := pool(t, "pool-normal")
	l := acquire(t, p, "scope", "stored-session")
	events, err := drain(l)
	if err != nil || len(events) != 0 {
		t.Fatal("load replay leaked into a new turn")
	}
	if _, err = l.Call(context.Background(), "session/prompt", map[string]any{"sessionId": "foreign", "prompt": []any{map[string]string{"type": "text", "text": "bad"}}}); !errors.Is(err, acp.ErrParameters) {
		t.Fatal("lease sent another session's request")
	}
	if _, err = l.Call(context.Background(), "session/prompt", map[string]any{"sessionId": l.ID(), "prompt": []any{map[string]string{"type": "text", "text": "new delta"}}}); err != nil {
		t.Fatal(err)
	}
	events, err = drain(l)
	if err != nil || len(events) != 1 || strings.Contains(string(events[0].Params), "synthetic replay") {
		t.Fatal("live notification did not follow replay suppression")
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(events[0].Params, &params) != nil {
		t.Fatal("invalid fixture")
	}
}
