package interop_test

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
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
	bounded := kiroHomeIdentityRunner(func(ctx context.Context, command childproc.Command) (childproc.Result, error) {
		command.Environment = append(append([]string(nil), command.Environment...), "KIRO_HOME="+configuration)
		limit := 5 * time.Second
		if len(command.Args) == 4 && strings.Join(command.Args, " ") == "chat --list-models --format json" {
			limit = 15 * time.Second
		}
		bounded, stop := context.WithTimeout(ctx, limit)
		defer stop()
		return runner.Run(bounded, command)
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
