package childproc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

func attached(t *testing.T) *childproc.Attached {
	t.Helper()
	a, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 5 * time.Second, GracePeriod: 30 * time.Millisecond, TermPeriod: 30 * time.Millisecond, KillPeriod: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}
func attachedFiles(t *testing.T) childproc.AttachedIO {
	t.Helper()
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "output-")
	if err != nil {
		t.Fatal(err)
	}
	errors, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); output.Close(); errors.Close() })
	return childproc.AttachedIO{Stdin: input, Stdout: output, Stderr: errors}
}
func TestAttachedClientStreamsWithoutCapturingOrInheriting(t *testing.T) {
	a, io := attached(t), attachedFiles(t)
	t.Setenv("DAX_RUNNER_SHOULD_NOT_INHERIT", "synthetic")
	cmd := command(t, "environment")
	cmd.Environment = []string{"ONLY_FOR_FIXTURE=yes"}
	p, err := a.Start(t.Context(), cmd, io)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Wait()
	if err != nil || result.ExitCode != 0 || result.PID != p.PID() || len(result.Stdout) != 0 {
		t.Fatal("attached output was captured or exit was lost")
	}
	data, err := os.ReadFile(io.Stdout.Name())
	if err != nil || !strings.Contains(string(data), `"allowed":true`) || !strings.Contains(string(data), `"inherited":false`) {
		t.Fatal("explicit child environment was not preserved")
	}
	if _, err := io.Stdout.WriteString("caller-still-owns-file"); err != nil {
		t.Fatal("caller descriptor was closed")
	}
	cmd.Args = []string{"failure"}
	p, err = a.Start(t.Context(), cmd, io)
	if err != nil {
		t.Fatal(err)
	}
	result, err = p.Wait()
	if !errors.Is(err, childproc.ErrExit) || result.ExitCode != 23 || strings.Contains(err.Error(), "synthetic") {
		t.Fatal("attached error exposed child output")
	}
	if a.Active() != 0 {
		t.Fatal("completed client retained admission")
	}
}
func TestAttachedCancellationAndLeaderExitRemoveWholeGroup(t *testing.T) {
	for _, mode := range []string{"tree", "leader-exits"} {
		t.Run(mode, func(t *testing.T) {
			a, io := attached(t), attachedFiles(t)
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			p, err := a.Start(ctx, command(t, mode), io)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			_, err = p.Wait()
			if mode == "tree" && !errors.Is(err, context.DeadlineExceeded) || mode == "leader-exits" && err != nil {
				t.Fatal("wrong attached lifetime result", err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("attached shutdown exceeded bounds")
			}
			data, err := os.ReadFile(io.Stdout.Name())
			descendant, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || parseErr != nil || descendant <= 0 {
				t.Fatal("fixture descendant was not observed")
			}
			if !errors.Is(syscall.Kill(descendant, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-p.PID(), 0), syscall.ESRCH) {
				t.Fatal("client descendant survived cleanup")
			}
		})
	}
}
func TestAttachedBoundedAdmissionAndRepeatedClose(t *testing.T) {
	a, io := attached(t), attachedFiles(t)
	p, err := a.Start(t.Context(), command(t, "tree"), io)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(t.Context(), command(t, "version"), io); !errors.Is(err, childproc.ErrBusy) {
		t.Fatal("multiple attached clients admitted")
	}
	var joined sync.WaitGroup
	for range 8 {
		joined.Go(a.Close)
		joined.Go(func() { p.Close() })
	}
	joined.Wait()
	if _, err := p.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal("client shutdown did not cancel lifetime")
	}
	if a.Active() != 0 {
		t.Fatal("Close returned before cleanup joined")
	}
	if _, err := a.Start(t.Context(), command(t, "version"), io); !errors.Is(err, childproc.ErrClosed) {
		t.Fatal("closed owner admitted a client")
	}
}
func TestAttachedBlockedIOCannotPreventShutdown(t *testing.T) {
	for _, mode := range []string{"read-input", "stdout-overflow", "stderr-flood"} {
		t.Run(mode, func(t *testing.T) {
			a, io := attached(t), attachedFiles(t)
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			defer w.Close()
			switch mode {
			case "read-input":
				io.Stdin = r
			case "stdout-overflow":
				io.Stdout = w
			default:
				io.Stderr = w
			}
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			p, err := a.Start(ctx, command(t, mode), io)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if _, err := p.Wait(); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("blocked descriptor ignored deadline", err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("descriptor copy goroutine blocked cleanup")
			}
		})
	}
}
func TestAttachedRejectsInvalidInputsAndRecoversFromStartFailure(t *testing.T) {
	a, io := attached(t), attachedFiles(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Start(ctx, command(t, "version"), io); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled startup accepted")
	}
	bad := command(t, "version")
	bad.Environment = []string{"KEY=one", "KEY=two"}
	if _, err := a.Start(t.Context(), bad, io); !errors.Is(err, childproc.ErrParameters) {
		t.Fatal("ambiguous environment accepted")
	}
	if _, err := a.Start(t.Context(), command(t, "version"), childproc.AttachedIO{}); !errors.Is(err, childproc.ErrParameters) {
		t.Fatal("implicit ambient descriptors accepted")
	}
	bad = command(t, "version")
	bad.Executable = filepath.Join(bad.Directory, "missing-binary")
	if _, err := a.Start(t.Context(), bad, io); !errors.Is(err, childproc.ErrStart) {
		t.Fatal("missing binary started")
	}
	if a.Active() != 0 {
		t.Fatal("failed startup retained admission")
	}
	p, err := a.Start(t.Context(), command(t, "version"), io)
	if err != nil {
		t.Fatal("failed start poisoned next admission", err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []time.Duration{-time.Second, 8 * 24 * time.Hour} {
		if _, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: limit}); !errors.Is(err, childproc.ErrParameters) {
			t.Fatal("unbounded client lifetime accepted")
		}
	}
}
