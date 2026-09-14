package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/websearch"
)

func TestSearchBudgetCompiledHelper(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private helper fixture")
	}
	build, err := childproc.New(childproc.Config{Timeout: 30 * time.Second, MaxProcesses: 1, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal("bounded builder")
	}
	defer build.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("source directory")
	}
	binary := filepath.Join(root, "proxy")
	if _, err := build.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: os.Environ(), Args: []string{"build", "-o", binary, "."}}); err != nil {
		t.Fatal("helper build failed")
	}
	runner, err := childproc.New(childproc.Config{Timeout: 4 * time.Second, MaxProcesses: 1, MaxOutputBytes: 4096})
	if err != nil {
		t.Fatal("bounded helper runner")
	}
	defer runner.Close()
	for _, tc := range []struct {
		name, input string
		code, count int
	}{
		{"missing-context", "", 2, 0},
		{"allowed", "{}", 0, 1},
		{"exhausted", "{}", 2, 1},
	} {
		command := childproc.Command{Executable: "/bin/sh", Directory: root, Environment: []string{"HOME=" + root, "PATH=/usr/bin:/bin"}, Args: []string{"-c", `printf '%s' "$1" | "$2" web-search-budget "$3" 1`, "owned-budget-helper", tc.input, binary, root}}
		r, err := runner.Run(t.Context(), command)
		count, countErr := websearch.BudgetCount(root, 1)
		if r.ExitCode != tc.code || tc.code == 0 && err != nil || tc.code != 0 && !errors.Is(err, childproc.ErrExit) || len(r.Stdout) != 0 || countErr != nil || count != tc.count || runner.Active() != 0 || !errors.Is(syscall.Kill(-r.PID, 0), syscall.ESRCH) {
			t.Fatalf("compiled helper boundary failed: %s", tc.name)
		}
	}
}
