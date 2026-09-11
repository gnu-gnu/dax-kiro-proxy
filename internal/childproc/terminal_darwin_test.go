package childproc_test

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"golang.org/x/sys/unix"
)

func TestAttachedForegroundAndTerminalRestoration(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	r, err := childproc.New(childproc.Config{Timeout: 15 * time.Second, MaxOutputBytes: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	passed := false
	defer func() {
		if passed {
			return
		}
		// script gives its child a distinct terminal session. These two owned numeric files permit
		// bounded emergency cleanup if an incorrect job-control implementation stops the harness.
		for _, name := range []string{"client.pid", "harness.pid"} {
			data, err := os.ReadFile(filepath.Join(root, name))
			pid, parseErr := strconv.Atoi(string(data))
			if err == nil && parseErr == nil && pid > 1 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	}()
	result, err := r.Run(t.Context(), childproc.Command{Executable: "/usr/bin/script", Directory: root,
		Args:        []string{"-q", os.DevNull, self, "-test.run=^TestAttachedTerminalHarness$", "-test.timeout=10s"},
		Environment: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "DAX_ATTACHED_TTY_FIXTURE=" + executable, "DAX_ATTACHED_TTY_ROOT=" + root, "GORACE=atexit_sleep_ms=0"}})
	if err != nil || !strings.Contains(string(result.Stdout), "independent-terminal-restoration-passed") {
		t.Fatalf("owned terminal fixture failed: %v, output=%s", err, result.Stdout)
	}
	passed = true
}

// This test runs only as a child of the system terminal allocator. It never observes the user's tty.
func TestAttachedTerminalHarness(t *testing.T) {
	if os.Getenv("DAX_ATTACHED_TTY_FIXTURE") == "" {
		t.Skip("only the owned terminal fixture runs this harness")
	}
	root := os.Getenv("DAX_ATTACHED_TTY_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("invalid fixture root")
	}
	if os.WriteFile(filepath.Join(root, "harness.pid"), []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
		t.Fatal("cannot record owned group")
	}
	fd := int(os.Stdin.Fd())
	originalGroup, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil || originalGroup != syscall.Getpgrp() || originalGroup != os.Getpid() {
		t.Fatal("terminal allocator did not give the harness its own foreground group")
	}
	original, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTTOU)
	defer signal.Stop(signals)
	ignored := signal.Ignored(syscall.SIGTTOU)
	assertRestored := func() {
		t.Helper()
		group, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		state, stateErr := unix.IoctlGetTermios(fd, unix.TIOCGETA)
		// macOS termios(4) identifies PENDIN as pending-input state. The tty driver sets it when
		// canonical input is restored. Compare all persistent settings without consuming input.
		expected := *original
		expected.Lflag &^= unix.PENDIN
		if state != nil {
			state.Lflag &^= unix.PENDIN
		}
		if err != nil || stateErr != nil || group != originalGroup || *state != expected {
			t.Fatalf("terminal foreground or input state was not restored: group=%d expected=%d state=%+v expected=%+v errors=%v/%v", group, originalGroup, state, original, err, stateErr)
		}
		if signal.Ignored(syscall.SIGTTOU) != ignored {
			t.Fatal("terminal signal disposition changed")
		}
	}
	a := attached(t)
	files := childproc.AttachedIO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Foreground: true}
	for _, mode := range []string{"terminal-exit", "terminal-failure", "terminal-hang"} {
		ctx, cancel := context.WithTimeout(t.Context(), 600*time.Millisecond)
		cmd := childproc.Command{Executable: executable, Directory: root, Args: []string{mode}, Environment: []string{"TERM=xterm-256color"}}
		p, err := a.Start(ctx, cmd, files)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if os.WriteFile(filepath.Join(root, "client.pid"), []byte(strconv.Itoa(p.PID())), 0600) != nil {
			cancel()
			t.Fatal("cannot record client group")
		}
		if mode == "terminal-hang" {
			other := attached(t)
			if _, err := other.Start(t.Context(), cmd, files); !errors.Is(err, childproc.ErrBusy) {
				t.Fatal("another owner could claim an active terminal")
			}
			other.Close()
		}
		result, err := p.Wait()
		cancel()
		switch mode {
		case "terminal-exit":
			if err != nil || result.ExitCode != 0 {
				t.Fatal("normal terminal exit failed", err)
			}
		case "terminal-failure":
			if !errors.Is(err, childproc.ErrExit) || result.ExitCode != 23 {
				t.Fatal("terminal failure was lost", err)
			}
		case "terminal-hang":
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, childproc.ErrCleanup) || errors.Is(err, childproc.ErrTerminal) {
				t.Fatal("forced terminal cleanup failed", err)
			}
		}
		assertRestored()
	}
	missing := childproc.Command{Executable: filepath.Join(root, "missing"), Directory: root}
	if _, err := a.Start(t.Context(), missing, files); !errors.Is(err, childproc.ErrStart) || errors.Is(err, childproc.ErrTerminal) {
		t.Fatal("terminal start failure was not restored", err)
	}
	assertRestored()
	if syscall.Kill(os.Getpid(), syscall.SIGTTOU) != nil {
		t.Fatal("cannot send own signal sentinel")
	}
	select {
	case <-signals:
	case <-time.After(time.Second):
		t.Fatal("terminal restoration removed an existing signal handler")
	}
	signal.Stop(signals)
	signal.Ignore(syscall.SIGTTOU)
	ignored = true
	defer signal.Reset(syscall.SIGTTOU)
	p, err := a.Start(t.Context(), childproc.Command{Executable: executable, Directory: root, Args: []string{"terminal-exit"}}, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	assertRestored()
	_, _ = os.Stdout.WriteString("independent-terminal-restoration-passed\n")
}

func TestAttachedForegroundRejectsNonTerminal(t *testing.T) {
	a, files := attached(t), attachedFiles(t)
	files.Foreground = true
	if _, err := a.Start(t.Context(), command(t, "version"), files); !errors.Is(err, childproc.ErrTerminalUnavailable) || errors.Is(err, childproc.ErrTerminal) {
		t.Fatal("nonterminal was treated as foreground tty")
	}
	files.Foreground = false
	p, err := a.Start(t.Context(), command(t, "version"), files)
	if err != nil {
		t.Fatal("terminal rejection retained admission", err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
}
