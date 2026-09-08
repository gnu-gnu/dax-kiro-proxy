package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/kiroauth"
)

var fake string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dax-acp-fixture-")
	if err != nil {
		panic(err)
	}
	fake = filepath.Join(dir, "fake-acp")
	cmd := exec.Command("go", "build", "-o", fake, "./testdata/fake")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build independent fixture: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
func config(t *testing.T, mode string) acp.Config {
	t.Helper()
	return acp.Config{Executable: fake, Args: []string{mode}, Directory: t.TempDir(), Environment: []string{},
		ClientInfo: acp.Info{Name: "fixture-client", Version: "1"}, Auth: kiroauth.Classifier{},
		Limits: acp.Limits{RequestTimeout: 2 * time.Second, WriteTimeout: 200 * time.Millisecond, CancelTimeout: 50 * time.Millisecond, GracePeriod: 100 * time.Millisecond, TermPeriod: 100 * time.Millisecond, KillPeriod: time.Second}}
}
func start(t *testing.T, mode string) *acp.Client {
	t.Helper()
	c, err := acp.Start(context.Background(), config(t, mode))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}
func call(t *testing.T, c *acp.Client, method string, params any) json.RawMessage {
	t.Helper()
	result, err := c.Call(context.Background(), method, params)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func awaitClosed(t *testing.T, c *acp.Client) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup exceeded bound")
	}
}

func TestInitializeAndVersionRejection(t *testing.T) {
	c := start(t, "normal")
	if !c.Capabilities().LoadSession || !c.Capabilities().Prompt.Image {
		t.Fatal("negotiated capabilities missing")
	}
	_, err := acp.Start(context.Background(), config(t, "version"))
	if !errors.Is(err, acp.ErrProtocol) {
		t.Fatalf("version: %v", err)
	}
}
func TestConcurrentOutOfOrderAndNotification(t *testing.T) {
	c := start(t, "normal")
	var wg sync.WaitGroup
	for _, value := range []string{"first", "second"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			result, err := c.Call(context.Background(), "fixture/pair", map[string]string{"value": value})
			if err != nil {
				t.Error(err)
				return
			}
			var got map[string]string
			_ = json.Unmarshal(result, &got)
			if got["value"] != value {
				t.Errorf("mismatched result %s", got["value"])
			}
		}(value)
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := c.Next(ctx)
	if err != nil || event.Method != "session/update" || event.SessionID != "fixture-session" || !strings.Contains(string(event.Params), "rill") {
		t.Fatalf("notification: %+v %v", event, err)
	}
}
func TestMalformedFramesFailEveryWaiter(t *testing.T) {
	for _, kind := range []string{"json", "array", "version", "unknown-id", "string-id", "null-id", "fractional-id", "both", "missing-result", "duplicate-key", "oversize", "truncated", "exit"} {
		t.Run(kind, func(t *testing.T) {
			c := start(t, "normal")
			pending := make(chan error, 1)
			go func() { _, err := c.Call(context.Background(), "fixture/hang", map[string]any{}); pending <- err }()
			_, err := c.Call(context.Background(), "fixture/bad", map[string]string{"kind": kind})
			if err == nil {
				t.Fatal("accepted invalid response")
			}
			select {
			case err := <-pending:
				if err == nil {
					t.Fatal("pending call succeeded")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("waiter leaked")
			}
			awaitClosed(t, c)
		})
	}
}
func TestOutboundLimitAndStalledPipe(t *testing.T) {
	t.Run("outbound", func(t *testing.T) {
		c := start(t, "normal")
		_, err := c.Call(context.Background(), "fixture/echo", map[string]string{"text": strings.Repeat("x", 8<<20)})
		if !errors.Is(err, acp.ErrFrameTooLarge) {
			t.Fatalf("outbound: %v", err)
		}
		awaitClosed(t, c)
	})
	t.Run("blocked write", func(t *testing.T) {
		c := start(t, "normal")
		call(t, c, "fixture/stop-reading", map[string]any{})
		_, err := c.Call(context.Background(), "fixture/echo", map[string]string{"text": strings.Repeat("x", 1<<20)})
		if err == nil {
			t.Fatal("stalled write succeeded")
		}
		awaitClosed(t, c)
	})
}
func TestAgentRequestsNeverAuthorizeEffects(t *testing.T) {
	c := start(t, "normal")
	var selected struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(call(t, c, "fixture/permission", map[string]any{}), &selected)
	if selected.Outcome.Outcome != "selected" || selected.Outcome.OptionID != "deny-me" {
		t.Fatalf("permission: %+v", selected)
	}
	_ = json.Unmarshal(call(t, c, "fixture/permission-empty", map[string]any{}), &selected)
	if selected.Outcome.Outcome != "cancelled" {
		t.Fatalf("permission without rejection: %+v", selected)
	}
	var rejected struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(call(t, c, "fixture/unsupported", map[string]any{}), &rejected)
	if rejected.Code != -32601 {
		t.Fatalf("unsupported method: %+v", rejected)
	}
}
func TestCancellationAndTimeoutsRetire(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			c := start(t, "normal")
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			if cancelEarly {
				go func() { time.Sleep(10 * time.Millisecond); cancel() }()
			}
			_, err := c.Call(ctx, "session/prompt", map[string]any{"sessionId": "fixture-session", "prompt": []any{}})
			if !errors.Is(err, context.Canceled) && !errors.Is(err, acp.ErrTimeout) {
				t.Fatalf("cancel: %v", err)
			}
			awaitClosed(t, c)
		})
	}
	cfg := config(t, "init-hang")
	cfg.Limits.RequestTimeout = 40 * time.Millisecond
	_, err := acp.Start(context.Background(), cfg)
	if !errors.Is(err, acp.ErrTimeout) {
		t.Fatalf("initialize timeout: %v", err)
	}
}
func TestWholeProcessGroupShutdown(t *testing.T) {
	for _, mode := range []string{"normal", "stubborn", "leader-exits"} {
		t.Run(mode, func(t *testing.T) {
			c := start(t, mode)
			var descendant struct{ PID, Group int }
			_ = json.Unmarshal(call(t, c, "fixture/grandchild", map[string]any{}), &descendant)
			if descendant.Group != c.PID() {
				t.Fatalf("group %d != leader %d", descendant.Group, c.PID())
			}
			if mode == "leader-exits" {
				_, _ = c.Call(context.Background(), "fixture/exit-leader", map[string]any{})
			}
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := c.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			awaitClosed(t, c)
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && syscall.Kill(descendant.PID, 0) == nil {
				time.Sleep(10 * time.Millisecond)
			}
			if syscall.Kill(descendant.PID, 0) == nil {
				_ = syscall.Kill(descendant.PID, syscall.SIGKILL)
				t.Fatalf("grandchild %d survived", descendant.PID)
			}
			if syscall.Kill(c.PID(), 0) == nil {
				t.Fatal("leader survived")
			}
		})
	}
}
func TestFailurePrivacyAndAuth(t *testing.T) {
	for _, method := range []string{"fixture/auth-stderr", "fixture/auth-error"} {
		t.Run(method, func(t *testing.T) {
			c := start(t, "normal")
			_, err := c.Call(context.Background(), method, map[string]any{})
			if !errors.Is(err, acp.ErrAuthentication) {
				t.Fatalf("auth: %v", err)
			}
			awaitClosed(t, c)
		})
	}
	c := start(t, "normal")
	_, err := c.Call(context.Background(), "fixture/generic-error", map[string]any{})
	if err == nil || errors.Is(err, acp.ErrAuthentication) || strings.Contains(err.Error(), "private-prompt-sentinel") {
		t.Fatalf("unsafe error: %v", err)
	}
	call(t, c, "fixture/stderr", map[string]any{})
	call(t, c, "fixture/echo", map[string]any{})
	stats := c.Diagnostics()
	if stats.StderrBytes > 64<<10 || stats.StderrLines > 64 || strings.Contains(fmt.Sprint(stats), "synthetic-secret") {
		t.Fatalf("stderr budget/privacy: %+v", stats)
	}
}
func TestQueueOverflowAndNoEnvironmentInheritance(t *testing.T) {
	t.Setenv("UNEXPECTED_PROVIDER_SECRET", "must-not-inherit")
	c := start(t, "normal")
	var env map[string]string
	_ = json.Unmarshal(call(t, c, "fixture/environment", map[string]any{}), &env)
	if env["unexpected"] != "" {
		t.Fatal("environment inherited")
	}
	cfg := config(t, "normal")
	cfg.Limits.EventQueue = 2
	limited, err := acp.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = limited.Close() })
	_, _ = limited.Call(context.Background(), "fixture/notifications", map[string]any{})
	awaitClosed(t, limited)
	if !errors.Is(limited.Err(), acp.ErrOverloaded) {
		t.Fatalf("queue: %v", limited.Err())
	}
	call(t, c, "fixture/echo", map[string]any{})
}
func TestDuplicateResponseRetires(t *testing.T) {
	c := start(t, "normal")
	_, _ = c.Call(context.Background(), "fixture/duplicate-response", map[string]any{})
	awaitClosed(t, c)
	if !errors.Is(c.Err(), acp.ErrProtocol) {
		t.Fatalf("duplicate: %v", c.Err())
	}
}
