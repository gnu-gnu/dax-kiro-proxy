package session_test

import (
	"context"
	"dax-kiro-proxy/internal/inference"
	"errors"
	"testing"
	"time"
)

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
