//go:build darwin || linux

package installation

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

func TestInstallationCrashFixture(t *testing.T) {
	bin, source, phase := os.Getenv("DAX_INSTALLATION_FIXTURE_BIN"), os.Getenv("DAX_INSTALLATION_FIXTURE_SOURCE"), os.Getenv("DAX_INSTALLATION_FIXTURE_PHASE")
	if bin == "" || source == "" || (phase != "prepared" && phase != "published") {
		t.Skip("owned subprocess fixture only")
	}
	err := install(t.Context(), bin, source, true, func(at string) error {
		if at == phase {
			os.Exit(71)
		}
		return nil
	})
	t.Fatal("crash checkpoint not reached", err)
}

func TestProcessExitBeforeAndAfterPublicationHasBoundedRecovery(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	for _, phase := range []string{"prepared", "published"} {
		t.Run(phase, func(t *testing.T) {
			bin, source, old := transactionFixture(t, true)
			result, err := runner.Run(t.Context(), childproc.Command{
				Executable: self, Directory: filepath.Dir(bin), Args: []string{"-test.run=^TestInstallationCrashFixture$", "-test.count=1"},
				Environment: []string{"PATH=/usr/bin:/bin", "DAX_INSTALLATION_FIXTURE_BIN=" + bin, "DAX_INSTALLATION_FIXTURE_SOURCE=" + source, "DAX_INSTALLATION_FIXTURE_PHASE=" + phase},
			})
			if !errors.Is(err, childproc.ErrExit) || result.ExitCode != 71 || !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
				t.Fatal("owned crash fixture did not exit and join", err, result.ExitCode)
			}
			current, err := filepath.EvalSymlinks(filepath.Join(bin, executableName))
			if err != nil || (current == old) != (phase == "prepared") {
				t.Fatal("wrong publication after process exit")
			}
			lease, err := Lease(current)
			if err != nil || lease == nil {
				t.Fatal("crashed exclusive lock not released", err)
			}
			lease.Close()
			if err := Install(t.Context(), bin, source, true); err != nil {
				t.Fatal("valid crash artifact not recovered", err)
			}
			items, err := os.ReadDir(filepath.Join(bin, managerName))
			if err != nil || len(items) != 4 {
				t.Fatal("recovery retained old artifacts")
			}
			if err := Uninstall(t.Context(), bin); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLockIdentityReplacementCannotAuthorizeMutation(t *testing.T) {
	bin, _, _ := transactionFixture(t, true)
	s, err := openStore(t.Context(), bin, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lock := filepath.Join(bin, managerName, "lock")
	if os.Rename(lock, filepath.Join(filepath.Dir(bin), "retained-lock")) != nil || os.WriteFile(lock, nil, 0600) != nil {
		t.Fatal("fixture")
	}
	if !errors.Is(s.check(), ErrUnmanaged) || !errors.Is(removeManager(s), ErrUnmanaged) {
		t.Fatal("replaced lock inode authorized mutation")
	}
	if _, err := os.Lstat(filepath.Join(bin, executableName)); err != nil {
		t.Fatal("identity refusal removed public entry")
	}
}
