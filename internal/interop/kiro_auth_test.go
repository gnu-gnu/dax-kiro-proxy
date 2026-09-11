package interop_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/kiroauth"
	"dax-kiro-proxy/internal/launcher"
)

// The measured 2.21.3 build keeps its login under the account HOME, so a synthetic HOME reproduces
// the logged-out state without logging the account out (D117). This probe requires the CLI to
// report no account first, then starts the product's ACP client against the unmodified executable
// and records how the logged-out session fails and whether the injected classifier recognizes it.
// No credential is copied and no model request can succeed; the account login is never changed.
func TestKiroLoggedOutACPClassification(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for the logged-out ACP classification probe; no login change")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-kiro-loggedout-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, cwd, scratch := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	for _, path := range []string{home, cwd, scratch, filepath.Join(cwd, ".kiro"), filepath.Join(cwd, ".kiro", "agents")} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("cannot create logged-out probe directories")
		}
	}
	const name = "dax-loggedout-observation"
	agent := filepath.Join(cwd, ".kiro", "agents", name+".json")
	if os.WriteFile(agent, []byte(`{"name":"`+name+`","description":"Independent logged-out observation","tools":[],"allowedTools":[],"mcpServers":{},"resources":[],"hooks":{},"includeMcpJson":false}`), 0600) != nil {
		t.Fatal("cannot create independent probe agent")
	}
	env := []string{"HOME=" + home, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"}
	runner, err := childproc.New(childproc.Config{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	command := childproc.Command{Executable: executable, Directory: cwd, Environment: env, Args: []string{"--version"}}
	version, err := runner.Run(t.Context(), command)
	kiroVersion, admitted := launcher.KiroVersionFromOutput("kiro-cli", version.Stdout)
	if err != nil || !admitted {
		t.Fatal("unverified Kiro version for this probe")
	}
	// The synthetic HOME must report no account before any session is started; otherwise a prompt
	// could reach the provider.
	command.Args = []string{"whoami", "--format", "json"}
	identity, identityErr := runner.Run(t.Context(), command)
	loggedOut := identityErr != nil && identity.ExitCode != 0 && strings.Contains(string(identity.Stdout), `"account":null`)
	t.Logf("version=%s, synthetic_home_logged_out=%v, whoami_exit=%d", kiroVersion, loggedOut, identity.ExitCode)
	if !loggedOut {
		t.Fatal("synthetic HOME did not report a logged-out account; no session is started")
	}
	command.Args = []string{"agent", "validate", "--path", agent}
	command.Environment = append([]string(nil), env...)
	command.Environment[0] = "HOME=" + os.Getenv("HOME")
	validated, err := runner.Run(t.Context(), command)
	if err != nil || validated.ExitCode != 0 {
		t.Fatalf("probe agent validation did not complete successfully: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	started := time.Now()
	client, err := acp.Start(ctx, acp.Config{Executable: executable, Args: []string{"acp", "--agent", name, "--agent-engine", "v2"}, Directory: cwd, Environment: env, ClientInfo: acp.Info{Name: "dax-loggedout-observation", Version: "1"}, Auth: kiroauth.Classifier{}, Limits: acp.Limits{RequestTimeout: 8 * time.Second}})
	if err != nil {
		t.Logf("stage=initialize, failure=%s, authentication_recognized=%v, elapsed_ms=%d", kiroSetupFailure(err), kiroSetupFailure(err) == "authentication", time.Since(started).Milliseconds())
		return
	}
	defer client.Close()
	raw, newErr := client.Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	t.Logf("stage=session_new, failure=%s, result_bytes=%d, elapsed_ms=%d", kiroSetupFailure(newErr), len(raw), time.Since(started).Milliseconds())
	promptClass := "not-sent"
	if newErr == nil {
		var session struct {
			SessionID string `json:"sessionId"`
		}
		if len(raw) > 64<<10 || json.Unmarshal(raw, &session) != nil || session.SessionID == "" {
			t.Fatal("session/new result lacks a session id")
		}
		_, promptErr := client.Call(ctx, "session/prompt", map[string]any{"sessionId": session.SessionID, "prompt": []map[string]string{{"type": "text", "text": "Reply with the single word ready."}}})
		promptClass = kiroSetupFailure(promptErr)
		if promptErr == nil {
			t.Fatal("a logged-out session completed a prompt; the login state of this probe is not synthetic")
		}
	}
	clientErr := kiroSetupFailure(client.Err())
	closeErr := client.Close()
	recognized := promptClass == "authentication" || kiroSetupFailure(newErr) == "authentication" || clientErr == "authentication"
	t.Logf("stage=session_prompt, failure=%s, client_failure=%s, authentication_recognized=%v, close_failure=%s, elapsed_ms=%d", promptClass, clientErr, recognized, kiroSetupFailure(closeErr), time.Since(started).Milliseconds())
	if !recognized {
		t.Error("the logged-out session failure was not classified as authentication expiry")
	}
}

// Record the boundary at which the logged-out ACP process fails, with fixed markers only: whether
// stdout carried a JSON-RPC error for initialize and its code, whether the error message or a
// stderr line names the login command, and the exit code. The independent peer reproduces the
// measured boundary for the product's graceful fallback control.
func TestKiroLoggedOutACPShape(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for the logged-out ACP shape probe; no login change")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-kiro-loggedout-shape-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, cwd, scratch := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	for _, path := range []string{home, cwd, scratch, filepath.Join(cwd, ".kiro"), filepath.Join(cwd, ".kiro", "agents")} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("cannot create logged-out shape directories")
		}
	}
	const name = "dax-loggedout-shape"
	if os.WriteFile(filepath.Join(cwd, ".kiro", "agents", name+".json"), []byte(`{"name":"`+name+`","description":"Independent logged-out shape observation","tools":[],"allowedTools":[],"mcpServers":{},"resources":[],"hooks":{},"includeMcpJson":false}`), 0600) != nil {
		t.Fatal("cannot create independent probe agent")
	}
	env := []string{"HOME=" + home, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"}
	runner, err := childproc.New(childproc.Config{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	identity, identityErr := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: cwd, Environment: env, Args: []string{"whoami", "--format", "json"}})
	if identityErr == nil || identity.ExitCode == 0 || !strings.Contains(string(identity.Stdout), `"account":null`) {
		t.Fatal("synthetic HOME did not report a logged-out account; no session is started")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "acp", "--agent", name, "--agent-engine", "v2")
	cmd.Dir, cmd.Env = cwd, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{},"clientInfo":{"name":"dax-loggedout-shape","version":"1"}}}` + "\n")
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal("cannot start logged-out ACP process")
	}
	waitErr := cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	// Only fixed markers leave the probe: a JSON-RPC error's code and whether any message or stderr
	// line names the login command or a login-required phrase.
	jsonError, code, messageRecognized := false, 0, false
	for _, line := range strings.Split(strings.TrimSpace(string(stdout.Bytes())), "\n") {
		var packet struct {
			ID    json.RawMessage `json:"id"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(line), &packet) == nil && packet.Error != nil {
			jsonError, code = true, packet.Error.Code
			messageRecognized = messageRecognized || kiroauth.Classifier{}.Error(packet.Error.Code, packet.Error.Message, nil)
		}
	}
	stderrRecognized, stderrLines := false, 0
	for _, line := range strings.Split(string(stderr.Bytes()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		stderrLines++
		stderrRecognized = stderrRecognized || kiroauth.Classifier{}.Stderr([]byte(line))
	}
	t.Logf("exit=%d, wait_error=%v, timed_out=%v, stdout_bytes=%d, stderr_lines=%d, json_error=%v, json_error_code=%d, json_message_recognized=%v, stderr_recognized=%v, elapsed_ms=%d", exit, waitErr != nil, ctx.Err() != nil, len(stdout.Bytes()), stderrLines, jsonError, code, messageRecognized, stderrRecognized, time.Since(started).Milliseconds())
	if ctx.Err() != nil || (!messageRecognized && !stderrRecognized) {
		t.Error("the logged-out ACP process did not fail at a recognized authentication boundary")
	}
}
