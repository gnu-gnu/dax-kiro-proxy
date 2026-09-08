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

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/kiroauth"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/toolregistry"
)

// This probe sends initialize and session/new only with a newly owned HOME/cwd/profile. The finite
// syntax validator uses the existing HOME; no account files are copied into the ACP environment.
// It never submits session/prompt, logs in, or starts an existing user agent.
func TestKiroIsolatedACPHandshake(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for isolated protocol setup only; no model prompt")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-kiro-handshake-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, cwd, scratch := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	for _, path := range []string{home, cwd, scratch, filepath.Join(cwd, ".kiro"), filepath.Join(cwd, ".kiro", "agents")} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("cannot create isolated Kiro probe")
		}
	}
	const name = "dax-protocol-observation"
	agent := filepath.Join(cwd, ".kiro", "agents", name+".json")
	if os.WriteFile(agent, []byte(`{"name":"dax-protocol-observation","description":"Independent protocol setup observation","tools":[],"allowedTools":[],"mcpServers":{},"resources":[],"hooks":{},"includeMcpJson":false}`), 0600) != nil {
		t.Fatal("cannot create independent probe agent")
	}
	env := []string{"HOME=" + home, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"}
	runner, err := childproc.New(childproc.Config{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	command := childproc.Command{Executable: executable, Directory: cwd, Environment: env, Args: []string{"--version"}}
	version, err := runner.Run(t.Context(), command)
	if err != nil || strings.TrimSpace(string(version.Stdout)) != "kiro-cli 2.21.1" {
		t.Fatal("unverified Kiro version for this probe")
	}
	command.Args = []string{"agent", "validate", "--path", agent}
	command.Environment = append([]string(nil), env...)
	command.Environment[0] = "HOME=" + os.Getenv("HOME")
	validated, err := runner.Run(t.Context(), command)
	t.Logf("agent_validate_exit=%d, stdout_bytes=%d, output_shape=%v", validated.ExitCode, len(validated.Stdout), kiroOutputShape(validated.Stdout))
	if err != nil || validated.ExitCode != 0 {
		t.Fatalf("probe agent validation did not complete successfully: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	client, err := acp.Start(ctx, acp.Config{Executable: executable, Args: []string{"acp", "--agent", name, "--agent-engine", "v2"}, Directory: cwd, Environment: env, ClientInfo: acp.Info{Name: "dax-protocol-observation", Version: "1"}, Auth: kiroauth.Classifier{}, Limits: acp.Limits{RequestTimeout: 5 * time.Second}})
	if err != nil {
		t.Logf("version=2.21.1, agent_validate_exit=0, initialized=false, failure=%s, elapsed_ms=%d", kiroSetupFailure(err), time.Since(started).Milliseconds())
		return
	}
	defer client.Close()
	raw, callErr := client.Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	sessionState, decodeErr := catalog.DecodeSession(raw)
	modelCount := 0
	if decodeErr == nil {
		modelCount = len(sessionState.Catalog.List())
	}
	caps := client.Capabilities()
	if err := client.Close(); err != nil {
		t.Fatalf("owned Kiro process cleanup failed: %s", kiroSetupFailure(err))
	}
	t.Logf("version=2.21.1, agent_validate_exit=0, initialized=true, load_session=%v, session_created=%v, model_count=%d, failure=%s, elapsed_ms=%d", caps.LoadSession, callErr == nil && decodeErr == nil, modelCount, kiroSetupFailure(callErr), time.Since(started).Milliseconds())
}

func kiroSetupFailure(err error) string {
	if err == nil {
		return "none"
	}
	for _, item := range []struct {
		err  error
		name string
	}{{acp.ErrAuthentication, "authentication"}, {acp.ErrProtocol, "protocol"}, {acp.ErrTransport, "transport"}, {acp.ErrTimeout, "timeout"}, {context.DeadlineExceeded, "deadline"}, {context.Canceled, "canceled"}, {acp.ErrParameters, "parameters"}, {acp.ErrOverloaded, "capacity"}} {
		if errors.Is(err, item.err) {
			return item.name
		}
	}
	return "unclassified"
}

// Account-backed initialization stops before session/new or session/prompt. The independently owned
// candidate has no tools; even an unexpected attempt to start its sole MCP command only runs false.
func TestKiroPinnedACPInitializationOnly(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for initialize only with the existing account; no session or prompt")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-kiro-initialize-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	configuration := filepath.Join(root, "kiro-home")
	if os.Mkdir(configuration, 0700) != nil {
		t.Fatal("cannot create owned Kiro initialization configuration root")
	}
	runner, err := childproc.New(childproc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	observed := kiroHomeIdentityRunner(func(ctx context.Context, command childproc.Command) (childproc.Result, error) {
		command.Environment = append(append([]string(nil), command.Environment...), "KIRO_HOME="+configuration)
		return runner.Run(ctx, command)
	})
	info, err := launcher.CheckKiro(t.Context(), observed, launcher.KiroConfig{Executable: executable, Home: os.Getenv("HOME"), Directory: root, ScopeKey: key})
	if err != nil {
		t.Fatalf("initialize probe preflight failed: %v", err)
	}
	registry, err := toolregistry.Build(t.Context(), nil, nil, syntaxFixtureValidator{})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := launcher.WriteCandidateAgent(launcher.AgentConfig{Directory: root, Registry: registry, RelayExecutable: "/usr/bin/false", RelayConfig: filepath.Join(root, "unused-control.json")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	client, err := acp.Start(ctx, acp.Config{Executable: executable, Directory: root, Args: []string{"acp", "--agent", agent.Name, "--agent-engine", "v2"},
		Environment: []string{"HOME=" + os.Getenv("HOME"), "KIRO_HOME=" + configuration, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + root, "TERM=dumb", "LANG=en_US.UTF-8"},
		ClientInfo:  acp.Info{Name: "dax-initialize-observation", Version: "1"}, Auth: kiroauth.Classifier{}, Limits: acp.Limits{RequestTimeout: 5 * time.Second}})
	if err != nil {
		t.Fatalf("pinned ACP initialize failed: %s", kiroSetupFailure(err))
	}
	defer client.Close()
	caps, pid := client.Capabilities(), client.PID()
	if err := client.Close(); err != nil {
		t.Fatalf("initialize-only cleanup failed: %s", kiroSetupFailure(err))
	}
	if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
		t.Fatal("initialize-only process group survived cleanup")
	}
	t.Logf("version=%s, protocol=1, initialized=true, owned_configuration_root=true, session_created=false, prompt_sent=false, load_session=%v, image=%v, audio=%v, embedded_context=%v, mcp_http=%v, mcp_sse=%v, cleanup_joined=true, elapsed_ms=%d", info.Version, caps.LoadSession, caps.Prompt.Image, caps.Prompt.Audio, caps.Prompt.EmbeddedContext, caps.MCP.HTTP, caps.MCP.SSE, time.Since(started).Milliseconds())
}
