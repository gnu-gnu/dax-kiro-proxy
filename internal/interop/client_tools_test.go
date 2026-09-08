package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

type clientToolBackend struct {
	*session.Driver
	catalog *catalog.Catalog
	starts  *atomic.Int32
	tools   *atomic.Int32
}

func (b clientToolBackend) Models(context.Context) ([]inference.Model, error) {
	return b.catalog.List(), nil
}
func (b clientToolBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	b.starts.Add(1)
	b.tools.Store(int32(len(r.Tools)))
	return b.Driver.Start(ctx, r)
}

func TestClaudeToolResultThroughGatewayACPAndMCP(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for the owned fake ACP/client tool test; no model credits")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-client-relay-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("Go toolchain is unavailable")
	}
	goPath, err = filepath.Abs(goPath)
	if err != nil {
		t.Fatal(err)
	}
	buildEnv := []string{}
	for _, key := range []string{"HOME", "PATH", "GOTOOLCHAIN", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOSUMDB"} {
		if value, ok := os.LookupEnv(key); ok {
			buildEnv = append(buildEnv, key+"="+value)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fake, relayPath := filepath.Join(root, "fake-acp"), filepath.Join(root, "relay")
	for _, target := range []struct{ output, source string }{{fake, "../acp/testdata/fake"}, {relayPath, "../../cmd/dax-kiro-proxy"}} {
		if _, err := runner.Run(t.Context(), childproc.Command{Executable: goPath, Directory: cwd, Environment: buildEnv, Args: []string{"build", "-o", target.output, target.source}}); err != nil {
			t.Fatalf("cannot build owned protocol fixture: %v", err)
		}
	}
	home, project, backendDir, workerDir := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "backend"), filepath.Join(root, "worker")
	for _, path := range []string{home, project, backendDir, workerDir} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("cannot create owned workspaces")
		}
	}
	userSettings := filepath.Join(home, "settings.json")
	denial, _ := json.Marshal(map[string]any{"permissions": map[string]string{"defaultMode": "manual"}, "hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": `printf '%s' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"independent fixture denial"}}'`, "timeout": 2}}}}}})
	if os.WriteFile(userSettings, denial, 0600) != nil {
		t.Fatal("cannot create owned permission policy")
	}
	before := fileFingerprint(t, userSettings)
	validator, err := schemacheck.New(schemacheck.Config{Executable: relayPath, Directory: workerDir})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Close()
	driver, err := session.New(session.Config{Process: acp.Config{Executable: fake, Args: []string{"chat-tools-client", filepath.Join(project, "denied-fixture")}, Directory: backendDir, ClientInfo: acp.Info{Name: "independent-client-probe", Version: "1"}}, Validator: validator, RelayExecutable: relayPath, TurnTimeout: 15 * time.Second, SetupTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	models, err := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent tool fixture"}}, "fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	model, err := models.ClientID("fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	var toolCount atomic.Int32
	handler, err := gateway.New(gateway.Config{Tokens: tokens, Backend: clientToolBackend{driver, models, &starts, &toolCount}, TurnTimeout: 15 * time.Second, FirstEventTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	version, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}, Args: []string{"--version"}})
	if err != nil || strings.TrimSpace(string(version.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified installed client version")
	}
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: userSettings, Executable: executable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	command := profile.Command()
	command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "Read", "--no-session-persistence", "--system-prompt", "Independent client relay exercise.", "Request the fixture tool and then finish.")
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	result, runErr := runner.Run(ctx, command)
	if runErr != nil || !bytes.Contains(result.Stdout, []byte("independent client relay complete")) {
		t.Fatalf("actual client tool continuation failed: error=%v, exit=%d, output_bytes=%d, driver_state=%s, requests=%d, tools=%d", runErr, result.ExitCode, len(result.Stdout), driver.State(), starts.Load(), toolCount.Load())
	}
	if starts.Load() != 2 || driver.State() != session.Idle || fileFingerprint(t, userSettings) != before {
		t.Fatal("tool turn did not settle or client source settings changed")
	}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("client=%s, streamed_tool_result=true, client_permission_denial=true, ACP_prompts=1, source_settings_unchanged=true, driver_closed=true", launcher.SupportedClientVersion)
}
