package childproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

var executable string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dax-cli-fixture-")
	if err != nil {
		panic(err)
	}
	executable = filepath.Join(dir, "fake-cli")
	command := exec.Command("go", "build", "-o", executable, "./testdata/fake")
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, "cannot build independent CLI fixture", string(output))
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func runner(t *testing.T) *childproc.Runner {
	t.Helper()
	r, err := childproc.New(childproc.Config{MaxProcesses: 1, MaxOutputBytes: 1024, Timeout: 5 * time.Second, GracePeriod: 30 * time.Millisecond, TermPeriod: 30 * time.Millisecond, KillPeriod: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}
func command(t *testing.T, mode string) childproc.Command {
	return childproc.Command{Executable: executable, Directory: t.TempDir(), Args: []string{mode}}
}
func TestCLIOutputEnvironmentAndErrorsStayBounded(t *testing.T) {
	r := runner(t)
	result, err := r.Run(context.Background(), command(t, "version"))
	if err != nil || string(result.Stdout) != "fixture-cli 1.2.3\n" || result.ExitCode != 0 {
		t.Fatal("finite command did not complete")
	}
	t.Setenv("DAX_RUNNER_SHOULD_NOT_INHERIT", "synthetic")
	cmd := command(t, "environment")
	cmd.Environment = []string{"ONLY_FOR_FIXTURE=yes"}
	result, err = r.Run(context.Background(), cmd)
	var env map[string]bool
	if err != nil || json.Unmarshal(result.Stdout, &env) != nil || env["inherited"] || !env["allowed"] {
		t.Fatal("child inherited ambient environment")
	}
	result, err = r.Run(context.Background(), command(t, "failure"))
	if !errors.Is(err, childproc.ErrExit) || result.ExitCode != 23 || strings.Contains(err.Error(), "synthetic") {
		t.Fatal("unsafe subprocess error or lost exit status")
	}
	result, err = r.Run(context.Background(), command(t, "stderr-flood"))
	if err != nil || string(result.Stdout) != "complete" {
		t.Fatal("discarded stderr blocked the command")
	}
	result, err = r.Run(context.Background(), command(t, "stdout-overflow"))
	if !errors.Is(err, childproc.ErrOutputLimit) || len(result.Stdout) > 1024 {
		t.Fatal("unbounded output accepted")
	}
}
func TestCLIWholeGroupCleanupSurvivesCancellationAndLeaderExit(t *testing.T) {
	for _, mode := range []string{"tree", "leader-exits"} {
		t.Run(mode, func(t *testing.T) {
			r := runner(t)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			started := time.Now()
			result, err := r.Run(ctx, command(t, mode))
			if mode == "tree" && !errors.Is(err, context.DeadlineExceeded) || mode == "leader-exits" && err != nil {
				t.Fatalf("unexpected command outcome: %v", err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("cleanup outlived its shielded bounds")
			}
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(result.Stdout)))
			if parseErr != nil || pid <= 0 {
				t.Fatal("independent descendant was not observed")
			}
			if !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
				t.Fatal("owned descendant or process group survived")
			}
		})
	}
}
func TestCLICapacityAndRepeatedCloseJoinOwnedWork(t *testing.T) {
	r := runner(t)
	cmd := command(t, "tree")
	done := make(chan error, 1)
	go func() { _, err := r.Run(context.Background(), cmd); done <- err }()
	until := time.Now().Add(time.Second)
	for r.Active() == 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if r.Active() != 1 {
		t.Fatal("command did not enter bounded admission")
	}
	if _, err := r.Run(context.Background(), command(t, "version")); !errors.Is(err, childproc.ErrBusy) {
		t.Fatal("process capacity exceeded")
	}
	var closed sync.WaitGroup
	for range 8 {
		closed.Go(r.Close)
	}
	closed.Wait()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown did not cancel owned work")
	}
	if r.Active() != 0 {
		t.Fatal("shutdown returned before active work joined")
	}
	if _, err := r.Run(context.Background(), command(t, "version")); !errors.Is(err, childproc.ErrClosed) {
		t.Fatal("closed runner admitted another command")
	}
}

func TestCLIRejectsAmbiguousEnvironmentAndUnboundedCommands(t *testing.T) {
	r := runner(t)
	base := command(t, "version")
	for _, env := range [][]string{{"KEY=a", "KEY=b"}, {"1KEY=x"}, {"KEY=x\x00y"}, {"missing-value"}, {"KEY=" + strings.Repeat("x", 64<<10)}} {
		bad := base
		bad.Environment = env
		if _, err := r.Run(context.Background(), bad); !errors.Is(err, childproc.ErrParameters) || r.Active() != 0 {
			t.Fatal("invalid environment reached command admission")
		}
	}
	base.Executable = "relative-executable"
	if _, err := r.Run(context.Background(), base); !errors.Is(err, childproc.ErrParameters) {
		t.Fatal("relative executable accepted")
	}
}
