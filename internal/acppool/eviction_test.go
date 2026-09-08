package acppool_test

import (
	"context"
	"dax-kiro-proxy/internal/acppool"
	"testing"
)

func TestIdleCapacityCanBeRecycledWithoutEvictingActiveOwners(t *testing.T) {
	cfg := config(t, "pool-normal")
	cfg.MaxProcesses = 1
	p, err := acppool.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a := acquire(t, p, "old-policy", "")
	if err := a.SetIdle(true); err != nil {
		t.Fatal(err)
	}
	b, err := p.Acquire(context.Background(), "new-policy")
	if err != nil {
		t.Fatal("idle capacity was rejected instead of recycled")
	}
	if a.Err() == nil || b.PID() == a.PID() {
		t.Fatal("incompatible process reused")
	}
}
