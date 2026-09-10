package interop_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

// The product's model IDs are deliberately absent from the client's built-in catalog (D08).
// A client build later than the measured one emits an unknown-model notice on stderr and clamps
// auto-compact to an assumed window unless its opt-out is set. The prepared profile sets that
// opt-out (D112). Compare the unmodified client without it against the prepared profile with it.
const unknownModelNotice = "isn't described by this version's model catalog"

func TestClaudeUnknownModelWindowNotice(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY to an installed unmodified client; local fixture only, no model credits")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("client executable must be absolute")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-unknown-model-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, project, scratch, natural := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "tmp"), filepath.Join(root, "natural")
	for _, dir := range []string{home, filepath.Join(home, ".claude"), project, scratch, natural} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"permissions":{"defaultMode":"manual"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	version, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "DISABLE_AUTOUPDATER=1"}})
	clientVersion, ok := launcher.ClientVersionFromOutput(version.Stdout)
	if err != nil || !ok {
		t.Fatal("unverified installed client version")
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-dax-windowprobe-0123456789abcdef"
	const answer = "synthetic unknown-model fixture complete"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != tokens.Model && r.Header.Get("Authorization") != "Bearer "+tokens.Model {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": model, "display_name": "Synthetic window probe", "description": "Independent gateway fixture"}}})
		case r.URL.Path == "/v1/messages/count_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
		case r.Method == "POST" && r.URL.Path == "/v1/messages":
			var fields map[string]json.RawMessage
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&fields) != nil {
				w.WriteHeader(400)
				return
			}
			message := map[string]any{"id": "msg_independent_fixture", "type": "message", "role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": answer}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}
			if string(fields["stream"]) != "true" {
				_ = json.NewEncoder(w).Encode(message)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			start := map[string]any{"id": "msg_independent_fixture", "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}
			for _, event := range []map[string]any{{"type": "message_start", "message": start}, {"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}, {"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": answer}}, {"type": "content_block_stop", "index": 0}, {"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 0}}, {"type": "message_stop"}} {
				encoded, _ := json.Marshal(event)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
				w.(http.Flusher).Flush()
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	// Text output is required: with --output-format json the client omits this human notice and
	// keeps only its unrecognized-model diagnostic, which is why JSON-mode controls did not see it.
	args := []string{"--print", "--output-format", "text", "--model", model, "--strict-mcp-config", "--tools", "Read", "--no-session-persistence", "--system-prompt", "Synthetic local protocol exercise.", "Return the fixture response."}
	// Natural arms run the unmodified client with the same routing and discovery but without the
	// product profile; the arm without the opt-out is the positive control.
	naturalCommand := func(jsonOutput, optOut bool) *exec.Cmd {
		arm := append([]string{"--settings", settings, "--setting-sources", ""}, args...)
		if jsonOutput {
			for i := range arm {
				if arm[i] == "text" && i > 0 && arm[i-1] == "--output-format" {
					arm[i] = "json"
				}
			}
		}
		cmd := exec.Command(executable, arm...)
		cmd.Dir = project
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8", "CLAUDE_CONFIG_DIR=" + natural, "ANTHROPIC_BASE_URL=" + server.URL, "ANTHROPIC_API_KEY=" + tokens.Model, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1", "CLAUDE_CODE_MAX_RETRIES=0", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "NO_PROXY=127.0.0.1,localhost"}
		if optOut {
			cmd.Env = append(cmd.Env, "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1")
		}
		return cmd
	}
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: clientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if profile.Close() != nil {
			t.Error("profile cleanup failed")
		}
	}()
	prepared := profile.Command()
	optOut := false
	for _, entry := range prepared.Environment {
		optOut = optOut || entry == "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1"
	}
	if !optOut {
		t.Fatal("prepared environment lacks the unknown-model window opt-out")
	}
	preparedCommand := exec.Command(prepared.Executable, append(append([]string{}, prepared.Args...), args...)...)
	preparedCommand.Dir = prepared.Directory
	preparedCommand.Env = prepared.Environment
	before := fileFingerprint(t, settings)
	observe := func(name string, cmd *exec.Cmd) (noticed, unrecognized bool) {
		t.Helper()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var stdout, stderr boundedOutput
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("%s: cannot start client", name)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		var runErr error
		timedOut := false
		select {
		case runErr = <-done:
		case <-time.After(20 * time.Second):
			timedOut = true
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case runErr = <-done:
			case <-time.After(time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				runErr = <-done
			}
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		noticed = bytes.Contains(stderr.Bytes(), []byte(unknownModelNotice))
		unrecognized = bytes.Contains(stderr.Bytes(), []byte("[claude-code:unrecognized_model]"))
		completed := bytes.Contains(stdout.Bytes(), []byte(answer))
		t.Logf("%s: version=%s, completed=%v, notice=%v, unrecognized_diagnostic=%v, timed_out=%v, exit_error=%v, stderr_bytes=%d", name, clientVersion, completed, noticed, unrecognized, timedOut, runErr != nil, len(stderr.Bytes()))
		if timedOut || runErr != nil || !completed {
			t.Fatalf("%s: client did not finish the local fixture", name)
		}
		return noticed, unrecognized
	}
	positiveNotice, _ := observe("natural", naturalCommand(false, false))
	jsonNotice, _ := observe("natural_json_output", naturalCommand(true, false))
	suppressedNotice, _ := observe("natural_opt_out", naturalCommand(false, true))
	preparedNotice, _ := observe("prepared", preparedCommand)
	if fileFingerprint(t, settings) != before {
		t.Error("source settings changed")
	}
	if !positiveNotice {
		if clientVersion == launcher.SupportedClientVersion {
			t.Log("the measured build emits no unknown-model notice; the suppression control is not exercised here")
			return
		}
		t.Fatal("positive control absent: the unmodified client did not emit the unknown-model notice")
	}
	t.Logf("json_output_hides_notice=%v", !jsonNotice)
	if suppressedNotice {
		t.Fatal("the opt-out did not suppress the unknown-model notice")
	}
	if preparedNotice {
		t.Fatal("the prepared profile still emits the unknown-model window notice")
	}
}
