package sessionstore_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/sessionstore"
)

func TestAbruptOwnerExitReleasesLockAndKeepsOnlyIdleData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	store, err := sessionstore.New(path)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("c", 64)
	for _, stage := range []string{"idle", "invalidated"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSessionStoreAbruptExitHelper$")
		cmd.Env = []string{"DAX_STORE_CRASH_DIRECTORY=" + path, "DAX_STORE_CRASH_STAGE=" + stage}
		err := cmd.Run()
		cancel()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 77 {
			t.Fatal("synthetic owner did not exit at its crash point")
		}
		lease, err := store.Claim(key)
		if err != nil {
			t.Fatal("OS did not release the exited owner's lease")
		}
		restored, err := lease.Read()
		if stage == "idle" {
			if err != nil || restored.SessionID != "stored-fixture" {
				t.Fatal("durable idle record lost at process exit")
			}
		} else if !os.IsNotExist(err) {
			t.Fatal("invalidated record resurrected at process exit")
		}
		lease.Close()
	}
}
func TestSessionStoreAbruptExitHelper(t *testing.T) {
	path := os.Getenv("DAX_STORE_CRASH_DIRECTORY")
	if path == "" {
		t.Skip("independent subprocess crash helper")
	}
	s, err := sessionstore.New(path)
	if err != nil {
		os.Exit(78)
	}
	key := strings.Repeat("c", 64)
	lease, err := s.Claim(key)
	if err != nil {
		os.Exit(79)
	}
	if lease.Save(record(t, key)) != nil {
		os.Exit(80)
	}
	if os.Getenv("DAX_STORE_CRASH_STAGE") == "invalidated" {
		if lease.Invalidate() != nil {
			os.Exit(81)
		}
	}
	// Exit intentionally skips Lease.Close and test defers. The parent verifies the OS lock lifetime.
	os.Exit(77)
}
