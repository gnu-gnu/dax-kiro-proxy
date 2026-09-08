package interop_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

type observedRelayProcess struct{ pid, group int }

func relayJoinOutcome(executable string) string {
	f, err := os.OpenFile(filepath.Join(filepath.Dir(executable), "group-join.txt"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "unavailable"
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 32 {
		return "invalid"
	}
	raw, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil {
		return "unavailable"
	}
	switch string(raw) {
	case "changed\n", "unchanged\n", "parent-unavailable\n", "permission\n", "failed\n":
		return strings.TrimSuffix(string(raw), "\n")
	default:
		return "invalid"
	}
}

func relayProcessRecords(executable string) ([]observedRelayProcess, error) {
	const limit = 1024
	f, err := os.OpenFile(filepath.Join(filepath.Dir(executable), "relay-processes.txt"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("relay process record unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > limit {
		return nil, errors.New("invalid relay process record")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(raw) == 0 || len(raw) > limit || raw[len(raw)-1] != '\n' {
		return nil, errors.New("invalid relay process record")
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) > 32 {
		return nil, errors.New("relay process record count exceeded")
	}
	result := make([]observedRelayProcess, 0, len(lines))
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, errors.New("invalid relay process identity")
		}
		pid, pidErr := strconv.ParseInt(parts[0], 10, 32)
		group, groupErr := strconv.ParseInt(parts[1], 10, 32)
		if pidErr != nil || groupErr != nil || pid <= 1 || group <= 1 {
			return nil, errors.New("invalid relay process identity")
		}
		result = append(result, observedRelayProcess{int(pid), int(group)})
	}
	return result, nil
}

// Direct observations come from the freshly built wrapper, not global process-table searches.
// The live caller invokes this after ACP shutdown and before removing the private wrapper directory.
func checkRelayProcessCleanup(t *testing.T, executable string, group int) {
	t.Helper()
	records, err := relayProcessRecords(executable)
	if err != nil {
		t.Error(err)
		return
	}
	allGone, sameGroup, groupLeaders := true, true, true
	deadline := time.Now().Add(time.Second)
	for _, record := range records {
		sameGroup = sameGroup && record.group == group
		groupLeaders = groupLeaders && record.group == record.pid
		if errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH) {
			continue
		}
		allGone = false
		// This PID was recorded by an owned wrapper in this probe. Clean up a survivor without
		// signaling an unexpected group that could also contain unrelated processes.
		_ = syscall.Kill(record.pid, syscall.SIGKILL)
		for time.Now().Before(deadline) && !errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Logf("relay_process_count=%d, relay_same_acp_group=%v, relay_group_leaders=%v, relay_processes_gone_after_acp_close=%v", len(records), sameGroup, groupLeaders, allGone)
	if !allGone || !sameGroup {
		t.Error("ACP shutdown did not establish relay process ownership and cleanup")
	}
}

func TestRelayObservationPreservesProcessIdentity(t *testing.T) {
	executable := buildRelayObserver(t)
	runner, err := childproc.New(childproc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	result, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: filepath.Dir(executable), Args: []string{"--version"}, Environment: []string{"PATH=/usr/bin:/bin"}})
	if err != nil || result.ExitCode != 0 {
		t.Fatal("owned wrapper did not execute the adjacent development binary")
	}
	records, err := relayProcessRecords(executable)
	if err != nil || len(records) != 1 || records[0].pid != result.PID || records[0].group != result.PID {
		t.Fatal("wrapper did not preserve its owned process identity")
	}
	checkRelayProcessCleanup(t, executable, result.PID)
}
