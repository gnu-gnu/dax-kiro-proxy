package interop_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

// Only public finite preflight runs here. The caller must separately guard and opt in model work.
func prepareLiveKiroProbe(t *testing.T, ctx context.Context, runner *childproc.Runner, executable, root, backend, configuration string) (*catalog.Catalog, launcher.KiroExecution) {
	t.Helper()
	if os.MkdirAll(filepath.Join(configuration, "settings"), 0700) != nil || os.WriteFile(filepath.Join(configuration, "settings", "cli.json"), []byte(`{"chat.disableInheritingDefaultResources":true}`), 0600) != nil {
		t.Fatal("cannot write owned Kiro probe settings")
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal("cannot create ephemeral identity key")
	}
	commands := 0
	bounded := kiroHomeIdentityRunner(func(ctx context.Context, command childproc.Command) (childproc.Result, error) {
		class := ""
		switch {
		case len(command.Args) == 1 && command.Args[0] == "--version" && command.Executable == executable:
			class = "main-version"
		case len(command.Args) == 1 && command.Args[0] == "--version" && command.Executable == filepath.Join(filepath.Dir(executable), "kiro-cli-chat"):
			class = "helper-version"
		case strings.Join(command.Args, " ") == "whoami --format json" && command.Executable == executable:
			class = "identity"
		case strings.Join(command.Args, " ") == "chat --list-models --format json" && command.Executable == executable:
			class = "catalog"
		}
		commands++
		if class == "" || commands > 6 {
			t.Fatal("prepared preflight escaped its finite command allowlist")
		}
		command.Environment = append(append([]string(nil), command.Environment...), "KIRO_HOME="+configuration)
		limit := 5 * time.Second
		if len(command.Args) == 4 && strings.Join(command.Args, " ") == "chat --list-models --format json" {
			limit = 15 * time.Second
		}
		bounded, stop := context.WithTimeout(ctx, limit)
		defer stop()
		result, runErr := runner.Run(bounded, command)
		gone := result.PID == 0 || errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
		joined := gone && runner.Active() == 0 && !errors.Is(runErr, childproc.ErrCleanup)
		t.Logf("prepared_preflight_command=%s exit=%d stdout_bytes=%d failure=%s cleanup_joined=%v", class, result.ExitCode, len(result.Stdout), kiroHomeProbeFailure(runErr), joined)
		if !joined {
			t.Fatal("prepared preflight retained process ownership")
		}
		return result, runErr
	})
	cfg := launcher.KiroConfig{Executable: executable, Home: os.Getenv("HOME"), Directory: backend, ScopeKey: key}
	info, err := launcher.CheckKiro(ctx, bounded, cfg)
	if err != nil {
		t.Fatal("Kiro version/account preflight failed before model work")
	}
	catalogContext, stop := context.WithTimeout(ctx, 15*time.Second)
	models, err := launcher.ReadKiroCatalog(catalogContext, bounded, cfg)
	stop()
	if err != nil {
		t.Fatal("Kiro catalog preflight failed before model work")
	}
	execution, err := launcher.PrepareKiroExecution(ctx, launcher.KiroExecutionConfig{Installation: info, Home: os.Getenv("HOME"), Project: backend, RuntimeDirectory: root})
	if err != nil {
		t.Fatal("cannot prepare built-in Kiro execution configuration")
	}
	return models, execution
}

// Exercise the identical live preparation with no ACP process or model prompt. Only the
// finite version/account/catalog commands run, followed by owned configuration creation.
func TestKiroPreparedReadOnlyPreflight(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set pinned Kiro for read-only prepared preflight")
	}
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	if os.Chmod(root, 0700) != nil || os.Mkdir(backend, 0700) != nil {
		t.Fatal("owned preflight directories")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, Timeout: 20 * time.Second, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal("bounded preflight runner")
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	models, _ := prepareLiveKiroProbe(t, ctx, runner, executable, root, backend, filepath.Join(root, "configuration"))
	if len(models.List()) == 0 || runner.Active() != 0 {
		t.Fatal("prepared preflight did not settle")
	}
	if os.RemoveAll(root) != nil {
		t.Fatal("owned preflight configuration cleanup")
	}
	t.Log("prepared_preflight=true model_requests=0 acp_sessions=0 owned_configuration_removed=true")
}
