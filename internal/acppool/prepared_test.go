package acppool_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
)

// Prepared launch fixtures own only their invented marker file and the independently built fake.
// The marker must outlive the entire process group, including retirement initiated by another caller.
func TestPreparedLaunchOwnsOneSessionAndCleansAfterGroupExit(t *testing.T) {
	cfg := config(t, "pool-normal")
	cfg.MaxProcesses, cfg.IdleTTL = 3, time.Minute
	p, err := acppool.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var prepared, cleaned atomic.Int32
	prepare := func(ctx context.Context) (acppool.PreparedProcess, error) {
		prepared.Add(1)
		dir, err := os.MkdirTemp(t.TempDir(), "owned-launch-")
		if err != nil {
			return acppool.PreparedProcess{}, err
		}
		process := cfg.Process
		process.Directory = dir
		return acppool.PreparedProcess{Config: process, Cleanup: func() error {
			cleaned.Add(1)
			return os.RemoveAll(dir)
		}}, ctx.Err()
	}
	a, err := p.AcquirePrepared(t.Context(), "same-policy", prepare)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.AcquirePrepared(t.Context(), "same-policy", prepare)
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Acquire(t.Context(), "same-policy")
	if err != nil {
		t.Fatal(err)
	}
	if a.PID() == b.PID() || a.PID() == c.PID() || b.PID() == c.PID() || prepared.Load() != 2 {
		t.Fatal("prepared process shared another session or skipped its preparation")
	}
	_, err = a.Create(t.Context(), map[string]any{"cwd": cfg.Process.Directory, "mcpServers": []any{}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcquirePrepared(t.Context(), "same-policy", prepare); !errors.Is(err, acp.ErrOverloaded) || prepared.Load() != 2 {
		t.Fatal("capacity failure executed a resource preparation callback")
	}
	pid := a.PID()
	if err := a.SetIdle(true); err != nil {
		t.Fatal(err)
	}
	if err := a.ReleaseIdle(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) || cleaned.Load() != 1 || p.Stats().Processes != 2 {
		t.Fatal("single-session release did not join its group and owned files")
	}
	var closes sync.WaitGroup
	for range 8 {
		closes.Go(func() {
			if err := p.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	closes.Wait()
	if cleaned.Load() != 2 || p.Stats().Processes != 0 {
		t.Fatal("repeated pool close retained or repeated preparation cleanup")
	}
}

func TestPreparedCleanupWaitsForProcessAndRetainsCapacityUntilComplete(t *testing.T) {
	cfg := config(t, "pool-normal")
	cfg.MaxProcesses = 1
	p, err := acppool.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var pid atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	cleanupError := errors.New("independent cleanup failure")
	l, err := p.AcquirePrepared(t.Context(), "owned", func(context.Context) (acppool.PreparedProcess, error) {
		return acppool.PreparedProcess{Config: cfg.Process, Cleanup: func() error {
			if !errors.Is(syscall.Kill(-int(pid.Load()), 0), syscall.ESRCH) {
				t.Error("owned artifact cleanup preceded complete process-group shutdown")
			}
			close(entered)
			<-release
			return cleanupError
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	pid.Store(int64(l.PID()))
	closed := make(chan error, 1)
	go func() { closed <- l.Close() }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup never began")
	}
	if p.Stats().Processes != 1 {
		t.Fatal("unfinished cleanup released process capacity")
	}
	if _, err := p.Acquire(t.Context(), "new"); !errors.Is(err, acp.ErrOverloaded) {
		t.Fatal("new launch bypassed unfinished cleanup")
	}
	unblock()
	if err := <-closed; !errors.Is(err, cleanupError) {
		t.Fatal("lease hid prepared-resource cleanup failure")
	}
	if err := l.Close(); !errors.Is(err, cleanupError) {
		t.Fatal("repeated lease close changed cleanup result")
	}
	if _, err := p.Acquire(t.Context(), "after-failure"); !errors.Is(err, acppool.ErrCleanup) {
		t.Fatal("pool admitted more artifacts after cleanup failed")
	}
	if err := p.Close(); !errors.Is(err, acppool.ErrCleanup) || !errors.Is(err, cleanupError) {
		t.Fatal("pool shutdown forgot a previously retired cleanup failure")
	}
}

func TestPreparedFailureAndCancellationReleasePartialArtifacts(t *testing.T) {
	for _, mode := range []string{"prepare-error", "start-error", "canceled-setup"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config(t, "pool-normal")
			p, err := acppool.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			marker := filepath.Join(t.TempDir(), "independent-partial-marker")
			var cleanup atomic.Int32
			l, err := p.AcquirePrepared(ctx, "partial", func(ctx context.Context) (acppool.PreparedProcess, error) {
				if err := os.WriteFile(marker, []byte("fixture"), 0600); err != nil {
					return acppool.PreparedProcess{}, err
				}
				result := acppool.PreparedProcess{Config: cfg.Process, Cleanup: func() error { cleanup.Add(1); return os.Remove(marker) }}
				switch mode {
				case "prepare-error":
					return result, acp.ErrParameters
				case "start-error":
					result.Config.Executable = filepath.Join(cfg.Process.Directory, "absent-fixture")
				case "canceled-setup":
					cancel()
				}
				return result, ctx.Err()
			})
			if err == nil || l != nil || cleanup.Load() != 1 || p.Stats().Processes != 0 {
				t.Fatal("failed preparation leaked a lease, slot or artifact")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("partial marker remained")
			}
		})
	}
}

func TestCloseCancelsAndJoinsPreparedSetup(t *testing.T) {
	p := pool(t, "pool-normal")
	entered := make(chan struct{})
	var cleaned atomic.Int32
	result := make(chan error, 1)
	go func() {
		_, err := p.AcquirePrepared(t.Context(), "setup", func(ctx context.Context) (acppool.PreparedProcess, error) {
			close(entered)
			<-ctx.Done()
			return acppool.PreparedProcess{Cleanup: func() error { cleaned.Add(1); return nil }}, ctx.Err()
		})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preparation not called")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil || cleaned.Load() != 1 || p.Stats().Processes != 0 {
		t.Fatal("pool close failed to cancel/join preparation and cleanup")
	}
	var calls atomic.Int32
	prepare := func(context.Context) (acppool.PreparedProcess, error) {
		calls.Add(1)
		return acppool.PreparedProcess{}, nil
	}
	if _, err := p.AcquirePrepared(t.Context(), "closed", prepare); !errors.Is(err, acp.ErrClosed) || calls.Load() != 0 {
		t.Fatal("closed pool admitted preparation")
	}
	if _, err := p.AcquirePrepared(t.Context(), "closed", nil); !errors.Is(err, acp.ErrParameters) {
		t.Fatal("missing preparer accepted")
	}
}

func TestRepeatedPreparedIdleReleaseJoinsTheSameCleanup(t *testing.T) {
	cfg := config(t, "pool-normal")
	p, err := acppool.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	cleanupError := errors.New("independent repeated-release failure")
	l, err := p.AcquirePrepared(t.Context(), "repeat", func(context.Context) (acppool.PreparedProcess, error) {
		return acppool.PreparedProcess{Config: cfg.Process, Cleanup: func() error { close(entered); <-release; return cleanupError }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetIdle(true); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 8)
	go func() { done <- l.ReleaseIdle() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("idle release failed to start cleanup")
	}
	for range 7 {
		go func() { done <- l.ReleaseIdle() }()
	}
	select {
	case <-done:
		t.Fatal("repeated idle release reported success before cleanup joined")
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	for range 8 {
		select {
		case err := <-done:
			if !errors.Is(err, cleanupError) {
				t.Fatal("repeated release hid the shared cleanup result")
			}
		case <-time.After(time.Second):
			t.Fatal("idle release caller did not join")
		}
	}
}
