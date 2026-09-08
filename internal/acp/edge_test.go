package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
)

func TestErrorEnvelopeTypesAndCorrelationPrecedeClassification(t *testing.T) {
	for _, kind := range []string{"null-error-code", "null-error-message"} {
		t.Run(kind, func(t *testing.T) {
			c := start(t, "normal")
			_, err := c.Call(context.Background(), "fixture/bad", map[string]string{"kind": kind})
			if !errors.Is(err, acp.ErrProtocol) {
				t.Fatalf("invalid error accepted: %v", err)
			}
			awaitClosed(t, c)
		})
	}
	c := start(t, "normal")
	_, err := c.Call(context.Background(), "fixture/uncorrelated-auth", map[string]any{})
	if !errors.Is(err, acp.ErrProtocol) {
		t.Fatalf("uncorrelated auth: %v", err)
	}
	awaitClosed(t, c)
}

func TestJSONValueSemanticsAndAgentDirections(t *testing.T) {
	c := start(t, "normal")
	call(t, c, "fixture/escaped-version", map[string]any{})
	var rejected struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(call(t, c, "fixture/unsupported-numeric", map[string]any{}), &rejected)
	if rejected.Code != -32601 {
		t.Fatalf("request ID confused with response: %+v", rejected)
	}
	call(t, c, "fixture/notify-permission", map[string]any{})
	call(t, c, "fixture/echo", map[string]any{})
	call(t, c, "fixture/visible-401", map[string]any{})
	call(t, c, "fixture/echo", map[string]any{})
}

func TestRejectedAdmissionDoesNotRetireHealthyProcess(t *testing.T) {
	c := start(t, "normal")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, "fixture/echo", map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-admission cancellation: %v", err)
	}
	if err := c.Notify(context.Background(), "fixture/notification", json.RawMessage(`{"x":1,"x":2}`)); !errors.Is(err, acp.ErrParameters) {
		t.Fatalf("duplicate notification params accepted: %v", err)
	}
	call(t, c, "fixture/echo", map[string]any{})
}

func TestConfiguredDeadlineWithoutCallerDeadline(t *testing.T) {
	cfg := config(t, "normal")
	cfg.Limits.RequestTimeout = 50 * time.Millisecond
	c, err := acp.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Call(context.Background(), "fixture/hang", map[string]any{})
	if !errors.Is(err, acp.ErrTimeout) {
		t.Fatalf("default deadline: %v", err)
	}
	awaitClosed(t, c)
}

func TestPendingCountAndEventByteBudgets(t *testing.T) {
	t.Run("pending requests", func(t *testing.T) {
		cfg := config(t, "normal")
		cfg.Limits.Pending = 1
		c, err := acp.Start(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		pending := make(chan error, 1)
		go func() { _, err := c.Call(context.Background(), "fixture/hang", map[string]any{}); pending <- err }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err = c.Next(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = c.Call(ctx, "fixture/echo", map[string]any{}); !errors.Is(err, acp.ErrOverloaded) {
			t.Fatalf("pending count not bounded: %v", err)
		}
		if c.Err() != nil {
			t.Fatal("rejected admission retired another request")
		}
		_ = c.Close()
		if err = <-pending; err == nil {
			t.Fatal("pending call not canceled")
		}
	})
	t.Run("event bytes", func(t *testing.T) {
		cfg := config(t, "normal")
		cfg.Limits.EventBytes = 100
		c, err := acp.Start(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		_, _ = c.Call(context.Background(), "fixture/notifications", map[string]any{})
		awaitClosed(t, c)
		if !errors.Is(c.Err(), acp.ErrOverloaded) {
			t.Fatalf("event bytes not bounded: %v", c.Err())
		}
	})
}
