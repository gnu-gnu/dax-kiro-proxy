package interop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/kiroauth"
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
		t.Logf("version=2.21.1, agent_syntax=true, initialized=false, failure=%s, elapsed_ms=%d", kiroSetupFailure(err), time.Since(started).Milliseconds())
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
	t.Logf("version=2.21.1, agent_syntax=true, initialized=true, load_session=%v, session_created=%v, model_count=%d, failure=%s, elapsed_ms=%d", caps.LoadSession, callErr == nil && decodeErr == nil, modelCount, kiroSetupFailure(callErr), time.Since(started).Milliseconds())
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
