package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/statusline"
)

type statusOnlyBackend struct{ t *testing.T }

func (b statusOnlyBackend) Models(context.Context) ([]inference.Model, error) {
	b.t.Error("status started model discovery")
	return nil, errors.New("unexpected model work")
}
func (b statusOnlyBackend) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	b.t.Error("status started inference")
	return nil, errors.New("unexpected model work")
}

func TestStatuslineProfileOwnsOnlyUICredentialAndPreservesSource(t *testing.T) {
	cfg := profileConfig(t)
	tokens, _ := gateway.NewTokens()
	server, err := gateway.StartServer(t.Context(), gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: statusOnlyBackend{t}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cfg.GatewayURL, cfg.ModelToken, cfg.UIToken, cfg.StatusExecutable = server.URL(), tokens.Model, tokens.UI, runtimeProxy
	// Shell metacharacters are literal filename bytes; an incorrect quote would create only this
	// test's owned marker. The independent shim also rejects inherited routing/credential values.
	shim := filepath.Join(cfg.RuntimeParent, "display ' $(touch unexpected-status-effect) `touch unexpected-status-effect`")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nif [ \"${ANTHROPIC_API_KEY+x}\" = x ] || [ \"${HTTP_PROXY+x}\" = x ]; then exit 82; fi\nexec " + quote(runtimeProxy) + " \"$@\"\n"
	if os.WriteFile(shim, []byte(script), 0700) != nil {
		t.Fatal("cannot write independent status shim")
	}
	cfg.StatusExecutable = shim
	original := []byte(`{"statusLine":{"type":"command","command":"private-original-status-command"},"hooks":{},"permissions":{"defaultMode":"manual"}}`)
	writeSettings(t, cfg.UserSettings, original)
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	data, _ := os.ReadFile(p.SettingsPath())
	var overlay struct {
		StatusLine struct {
			Type, Command   string
			RefreshInterval int
		} `json:"statusLine"`
	}
	if json.Unmarshal(data, &overlay) != nil || overlay.StatusLine.Type != "command" || overlay.StatusLine.RefreshInterval != 5 || !strings.HasPrefix(overlay.StatusLine.Command, "/usr/bin/env -i PATH=/usr/bin:/bin ") {
		t.Fatal("missing isolated five-second status command")
	}
	config := filepath.Join(p.Path(), "statusline.json")
	data, err = os.ReadFile(config)
	var private statusline.Config
	if err != nil || json.Unmarshal(data, &private) != nil || private.Token != tokens.UI || private.Endpoint != server.URL() || bytes.Contains(data, []byte(tokens.Model)) || strings.Contains(overlay.StatusLine.Command, tokens.UI) || strings.Contains(strings.Join(p.Command().Environment, "\n"), tokens.UI) {
		t.Fatal("status credential escaped its owned file or gained model authority")
	}
	if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("status credential file is not private")
	}
	runner, err := childproc.New(childproc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	result, err := runner.Run(t.Context(), childproc.Command{Executable: "/bin/sh", Args: []string{"-c", overlay.StatusLine.Command}, Directory: cfg.Project, Environment: []string{"ANTHROPIC_API_KEY=private-environment-sentinel", "HTTP_PROXY=http://outside.invalid"}})
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "Kiro fixture | no completed turn | usage unavailable\n" {
		t.Fatal("the configured status command did not query its local cache", err, result.ExitCode)
	}
	if _, err := os.Lstat(filepath.Join(cfg.Project, "unexpected-status-effect")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status command interpreted a literal path as shell code")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status credential survived cleanup")
	}
	if data, err := os.ReadFile(cfg.UserSettings); err != nil || !bytes.Equal(data, original) {
		t.Fatal("source status or permission settings changed")
	}
}

func TestStatuslineCommandDeadlineCoversBlockedStdoutAndUnreadStdin(t *testing.T) {
	cfg := profileConfig(t)
	tokens, _ := gateway.NewTokens()
	server, err := gateway.StartServer(t.Context(), gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: statusOnlyBackend{t}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	raw, err := statusline.EncodeConfig(statusline.Config{Version: 1, Endpoint: server.URL(), Token: tokens.UI, Model: cfg.Model})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "dax-status-io-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "status.json")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("cannot write owned status configuration")
	}
	in, hold, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer hold.Close()
	unread, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer unread.Close()
	defer out.Close()
	if out.SetWriteDeadline(time.Now().Add(50*time.Millisecond)) != nil {
		t.Fatal("cannot bound pipe saturation")
	}
	buffer := make([]byte, 32<<10)
	for {
		if _, err := out.Write(buffer); err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatal("pipe saturation failed", err)
			}
			break
		}
	}
	if out.SetWriteDeadline(time.Time{}) != nil {
		t.Fatal("cannot clear parent pipe deadline")
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	started := time.Now()
	process, err := owner.Start(t.Context(), childproc.Command{Executable: runtimeProxy, Args: []string{"statusline", "--config", path}, Directory: dir}, childproc.AttachedIO{Stdin: in, Stdout: out, Stderr: null})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	result, err := process.Wait()
	if !errors.Is(err, childproc.ErrExit) || result.ExitCode != 1 || time.Since(started) < 1500*time.Millisecond || time.Since(started) > 4*time.Second {
		t.Fatal("status process did not enforce its own total deadline", err, result.ExitCode)
	}
}

func TestRuntimeGeneratesAndRemovesStatusCredential(t *testing.T) {
	cfg, owner, output, _ := runtimeConfig(t, "text-hold")
	cfg.Client.StatusExecutable = runtimeProxy
	before, _ := os.ReadFile(cfg.Client.UserSettings)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := launcher.RunClient(ctx, cfg); finished <- err }()
	seen := readRuntimeObservation(t, output)
	deadline := time.Now().Add(time.Second)
	for cfg.Server.Gateway.Metrics.Latest() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	path := filepath.Join(seen.Runtime, "statusline.json")
	line, err := statusline.Display(t.Context(), path)
	if err != nil || !strings.Contains(line, "Kiro last fixture-backend") || !strings.Contains(line, "~tokens") || owner.starts.Load() != 1 || owner.lists.Load() != 0 {
		t.Error("runtime status was not available independently of model work", err)
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("runtime cancellation lost", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("runtime status integration retained owners")
	}
	assertRuntimeGone(t, cfg, owner, seen, before)
	if _, err := statusline.Display(t.Context(), path); !errors.Is(err, statusline.ErrConfig) {
		t.Fatal("expired runtime status remained readable")
	}
	if _, err := os.Lstat(seen.Runtime); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late display recreated removed runtime")
	}
}

func TestStatuslineProfileRejectsConflictingAuthority(t *testing.T) {
	for _, mode := range []string{"missing-executable", "missing-token", "model-token", "relative-executable"} {
		t.Run(mode, func(t *testing.T) {
			cfg := profileConfig(t)
			tokens, _ := gateway.NewTokens()
			cfg.StatusExecutable, cfg.UIToken = runtimeProxy, tokens.UI
			switch mode {
			case "missing-executable":
				cfg.StatusExecutable = ""
			case "missing-token":
				cfg.UIToken = ""
			case "model-token":
				cfg.UIToken = cfg.ModelToken
			case "relative-executable":
				cfg.StatusExecutable = "relative"
			}
			if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrConfig) {
				if p != nil {
					p.Close()
				}
				t.Fatal("incomplete or conflicting status authority accepted")
			}
		})
	}
}
