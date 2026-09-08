package interop_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

// Compare only public version/identity commands. The helper can list owned agents without the
// main entry point's synthetic-HOME failure, but that does not establish identity continuity.
// No credentials are copied, no account command mutates login, and no agent/session is started.
func TestKiroOwnedHomeHelperIdentity(t *testing.T) {
	executable, accountHome := os.Getenv("DAX_INTEROP_KIRO_BINARY"), os.Getenv("HOME")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for synthetic HOME main/helper identity comparison; no session or prompt")
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(accountHome) {
		t.Fatal("identity comparison requires absolute executable and account HOME")
	}
	root := t.TempDir()
	work, mainHome, helperHome := filepath.Join(root, "work"), filepath.Join(root, "main-home"), filepath.Join(root, "helper-home")
	accountConfig, mainConfig, helperConfig := filepath.Join(root, "account-config"), filepath.Join(root, "main-config"), filepath.Join(root, "helper-config")
	for _, directory := range []string{work, mainHome, helperHome, accountConfig, mainConfig, helperConfig} {
		if os.Mkdir(directory, 0700) != nil {
			t.Fatal("cannot create owned identity comparison directories")
		}
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal("cannot create ephemeral identity comparison key")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, MaxOutputBytes: 64 << 10, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal("cannot create bounded identity comparison runner")
	}
	t.Cleanup(func() {
		runner.Close()
		if runner.Active() != 0 {
			t.Error("identity comparison runner cleanup failed")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
	defer cancel()
	helper := filepath.Join(filepath.Dir(executable), "kiro-cli-chat")
	baseline, commands := "", 0
	for _, selected := range []struct {
		name, home, configuration string
		useHelper                 bool
	}{
		{"baseline-before", accountHome, accountConfig, false},
		{"synthetic-main", mainHome, mainConfig, false},
		{"synthetic-helper", helperHome, helperConfig, true},
		{"baseline-after", accountHome, accountConfig, false},
	} {
		t.Run(selected.name, func(t *testing.T) {
			observed := kiroHomeIdentityRunner(func(callContext context.Context, command childproc.Command) (childproc.Result, error) {
				t.Helper()
				class := ""
				switch {
				case len(command.Args) == 1 && command.Args[0] == "--version" && command.Executable == executable:
					class = "main-version"
				case len(command.Args) == 1 && command.Args[0] == "--version" && command.Executable == helper:
					class = "helper-version"
				case len(command.Args) == 3 && command.Args[0] == "whoami" && command.Args[1] == "--format" && command.Args[2] == "json" && command.Executable == executable:
					class = "identity"
					if selected.useHelper {
						command.Executable = helper
					}
				}
				if class == "" || commands >= 12 {
					t.Fatal("identity comparison escaped its exact command/count allowlist")
				}
				commands++
				command.Environment = append(append([]string(nil), command.Environment...), "KIRO_HOME="+selected.configuration)
				result, runErr := runner.Run(callContext, command)
				groupGone := result.PID == 0 || errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
				cleanupJoined := groupGone && runner.Active() == 0 && !errors.Is(runErr, childproc.ErrCleanup)
				t.Logf("command=%s, exit=%d, output_bytes=%d, failure=%s, cleanup_joined=%v", class, result.ExitCode, len(result.Stdout), kiroHomeProbeFailure(runErr), cleanupJoined)
				if !cleanupJoined {
					t.Fatal("identity comparison process group survived cleanup")
				}
				return result, runErr
			})
			info, checkErr := launcher.CheckKiro(ctx, observed, launcher.KiroConfig{Executable: executable, Home: selected.home, Directory: work, ScopeKey: key})
			verified := checkErr == nil && info.ProfileScope != ""
			if selected.name == "baseline-before" && verified {
				baseline = info.ProfileScope
			}
			equal := verified && baseline != "" && info.ProfileScope == baseline
			t.Logf("identity_verified=%v, baseline_equal=%v, synthetic_home=%v, helper_identity=%v, session_created=false, prompt_sent=false", verified, equal, selected.home != accountHome, selected.useHelper)
			if !equal {
				t.Error("identity continuity was not established for this HOME/entry point")
			}
		})
		if selected.name == "baseline-before" && baseline == "" {
			t.Fatal("initial account baseline failed; no alternate HOME is queried")
		}
	}
	t.Log("execution_restriction_verified=false; no production HOME or executable selection changed")
}
