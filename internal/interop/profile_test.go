package interop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/status"
)

// All files, hooks and endpoints here are independently authored and owned by this test. The
// unmodified client must enforce its own deny rule; no Kiro process or external inference is used.
func TestClaudeIsolatedProfilePreservesPoliciesAndGateway(t *testing.T) {
	observeClientProfileHooks(t, false)
}

func TestClaudeDisabledHooksPreserveConversationAndPermissions(t *testing.T) {
	observeClientProfileHooks(t, true)
}

func observeClientProfileHooks(t *testing.T, disabled bool) {
	t.Helper()
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for a local fixture and owned hook test; no model credits")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-profile-observation-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, path := range []string{home, filepath.Join(home, ".claude"), project, filepath.Join(project, ".claude")} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("cannot prepare independent profile inputs")
		}
	}
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	version, err := runner.Run(context.Background(), childproc.Command{Executable: executable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil || strings.TrimSpace(string(version.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified installed client version")
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	var wrongRoute atomic.Int32
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wrongRoute.Add(1); w.WriteHeader(503) }))
	defer wrong.Close()
	const model = "claude-dax-profile-observation"
	const answer = "owned profile contract complete"
	denied := filepath.Join(project, "private-fixture.txt")
	writeProbeJSON := func(path string, value any) {
		t.Helper()
		data, e := json.Marshal(value)
		if e != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot write independent settings")
		}
	}
	if os.WriteFile(denied, []byte("synthetic-content-must-not-be-returned"), 0600) != nil {
		t.Fatal("cannot write independent denied input")
	}
	userMarker, projectMarker, helperMarker := filepath.Join(root, "user-hook"), filepath.Join(root, "project-hook"), filepath.Join(root, "credential-helper")
	userStop, projectStop := filepath.Join(root, "user-stop"), filepath.Join(root, "project-stop")
	hook := func(command, stopped string) any {
		return map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 2}}}}, "Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": stopped, "timeout": 2}}}}}
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	userPath := filepath.Join(home, ".claude", "settings.json")
	projectPath := filepath.Join(project, ".claude", "settings.json")
	localPath := filepath.Join(project, ".claude", "settings.local.json")
	globalPath := filepath.Join(home, ".claude.json")
	writeProbeJSON(userPath, map[string]any{"permissions": map[string]any{"defaultMode": "manual", "deny": []string{"Read"}}, "hooks": hook("/usr/bin/touch "+quote(userMarker), "/usr/bin/touch "+quote(userStop)), "disableAllHooks": disabled, "env": map[string]string{"ANTHROPIC_BASE_URL": wrong.URL, "ANTHROPIC_API_KEY": "synthetic-user-key", "DAX_PROFILE_LAYER": "user"}, "apiKeyHelper": "/usr/bin/touch " + quote(helperMarker)})
	writeProbeJSON(projectPath, map[string]any{"permissions": map[string]any{"allow": []string{"Read"}}, "hooks": hook("test \"$DAX_PROFILE_LAYER\" = local && /usr/bin/touch "+quote(projectMarker), "/usr/bin/touch "+quote(projectStop)), "env": map[string]string{"DAX_PROFILE_LAYER": "project", "ANTHROPIC_BASE_URL": wrong.URL, "ANTHROPIC_API_KEY": "synthetic-project-key", "CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_BEDROCK_BASE_URL": wrong.URL}})
	writeProbeJSON(localPath, map[string]any{"env": map[string]string{"DAX_PROFILE_LAYER": "local", "ANTHROPIC_BASE_URL": wrong.URL, "CLAUDE_CODE_USE_VERTEX": "1", "ANTHROPIC_VERTEX_BASE_URL": wrong.URL}})
	writeProbeJSON(globalPath, map[string]any{})
	protected := []string{userPath, projectPath, localPath, globalPath}
	before := make([][32]byte, len(protected))
	for i, path := range protected {
		before[i] = fileFingerprint(t, path)
	}
	var mu sync.Mutex
	messages, models := 0, 0
	advertisedTools := 0
	toolError, routeTokenOK, continuationDecoded := false, true, false
	noticeInModelBody := false
	var continuationRoles []string
	var systemDigests [][32]byte
	repeatedSystem := true
	uiBackend := &statusProbeBackend{}
	metrics := status.NewTurnQueue()
	ui, err := gateway.New(gateway.Config{Tokens: tokens, Backend: uiBackend, LaunchModel: model, Metrics: metrics})
	if err != nil {
		t.Fatal("cannot prepare independent local UI routes")
	}
	var noticeRequests atomic.Int32
	var metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dax-kiro-proxy/") {
			if r.Method == "POST" && r.URL.Path == "/dax-kiro-proxy/hooks/model-capabilities" && r.Header.Get("x-api-key") == tokens.UI {
				noticeRequests.Add(1)
			}
			if r.Method == "POST" && r.URL.Path == "/dax-kiro-proxy/hooks/turn-metrics" && r.Header.Get("x-api-key") == tokens.UI {
				metricRequests.Add(1)
			}
			ui.ServeHTTP(w, r)
			return
		}
		if r.Method == "HEAD" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+tokens.Model && r.Header.Get("x-api-key") != tokens.Model {
			mu.Lock()
			routeTokenOK = false
			mu.Unlock()
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			mu.Lock()
			models++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": model, "display_name": "Independent profile fixture"}}})
			return
		}
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": 1})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		body, e := io.ReadAll(io.LimitReader(r.Body, (16<<20)+1))
		if e != nil || len(body) > 16<<20 {
			w.WriteHeader(413)
			return
		}
		var request struct {
			Stream   bool   `json:"stream"`
			Model    string `json:"model"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(body, &request) != nil || request.Model != model {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		messages++
		noticeInModelBody = noticeInModelBody || bytes.Contains(body, []byte("Kiro launch ")) || bytes.Contains(body, []byte("Kiro model capabilities unavailable.")) || bytes.Contains(body, []byte("Kiro turn#"))
		round := messages
		decoded, decodeErr := anthropic.DecodeRequest(body)
		if decodeErr == nil {
			advertisedTools = len(decoded.Tools)
			for i, message := range decoded.Messages {
				if message.Role != "system" {
					continue
				}
				texts := make([]string, 0, len(message.Content))
				for _, block := range message.Content {
					texts = append(texts, block.Text)
				}
				canonical, _ := json.Marshal(texts)
				digest := sha256.Sum256(canonical)
				if round == 1 {
					systemDigests = append(systemDigests, digest)
				} else if i > decoded.LatestUserIndex() {
					matched := false
					for _, old := range systemDigests {
						matched = matched || old == digest
					}
					repeatedSystem = repeatedSystem && matched
				}
			}
		}
		if round == 2 {
			continuationDecoded = decodeErr == nil
			for _, message := range request.Messages {
				continuationRoles = append(continuationRoles, message.Role)
				var blocks []struct {
					Type  string `json:"type"`
					ID    string `json:"tool_use_id"`
					Error bool   `json:"is_error"`
				}
				if json.Unmarshal(message.Content, &blocks) == nil {
					for _, block := range blocks {
						if block.Type == "tool_result" && block.ID == "toolu_owned_profile" {
							toolError = block.Error
						}
					}
				}
			}
		}
		mu.Unlock()
		if round > 2 {
			w.WriteHeader(500)
			return
		}
		if round == 1 {
			writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "tool_use", "id": "toolu_owned_profile", "name": "Read", "input": map[string]string{"file_path": denied}}}, "tool_use")
		} else {
			writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "text", "text": answer}}, "end_turn")
			metrics.Push(status.TurnRecord{Scope: strings.Repeat("d", 64), Model: model, SessionState: "created", Effort: kirofeature.Status{State: kirofeature.Unknown}})
		}
	}))
	defer server.Close()
	proxy := filepath.Join(filepath.Dir(buildRelayObserver(t)), "owned-relay")
	p, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: userPath, Executable: executable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, UIToken: tokens.UI, StatusExecutable: proxy, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "ANTHROPIC_API_KEY=synthetic-ambient-key"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	command := p.Command()
	command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "Read", "--no-session-persistence", "--system-prompt", "Independent local client-policy exercise.", "Request the fixture tool, then finish.")
	result, runErr := runner.Run(context.Background(), command)
	for i, path := range protected {
		if fileFingerprint(t, path) != before[i] {
			t.Error("source client settings changed")
		}
	}
	userHook, projectHook := false, false
	if _, e := os.Stat(userMarker); e == nil {
		userHook = true
	}
	if _, e := os.Stat(projectMarker); e == nil {
		projectHook = true
	}
	_, helperErr := os.Stat(helperMarker)
	_, userStopErr := os.Stat(userStop)
	_, projectStopErr := os.Stat(projectStop)
	mu.Lock()
	defer mu.Unlock()
	t.Logf("client=%s, messages=%d, models=%d, wrong_route=%d, user_hook=%v, project_hook_with_local_env=%v, tool_denied=%v, advertised_tools=%d, continuation_roles=%v, trailing_system_repeats_prior=%v, decoder_accepts=%v, exit=%d", launcher.SupportedClientVersion, messages, models, wrongRoute.Load(), userHook, projectHook, toolError, advertisedTools, continuationRoles, repeatedSystem, continuationDecoded, result.ExitCode)
	t.Logf("hooks_disabled=%v, startup_notice_requests=%d, notice_in_model_body=%v, ui_model_starts=%d, ui_model_lists=%d", disabled, noticeRequests.Load(), noticeInModelBody, uiBackend.starts.Load(), uiBackend.lists.Load())
	t.Logf("metric_requests=%d, user_stop_hook=%v, project_stop_hook=%v", metricRequests.Load(), userStopErr == nil, projectStopErr == nil)
	if runErr != nil || !bytes.Contains(result.Stdout, []byte(answer)) {
		t.Fatalf("client fixture did not finish: safe_error=%v, stdout_bytes=%d", runErr, len(result.Stdout))
	}
	expectedNotices := int32(1)
	if disabled {
		expectedNotices = 0
	}
	if messages != 2 || !continuationDecoded || !toolError || !routeTokenOK || wrongRoute.Load() != 0 || userHook == disabled || projectHook == disabled || (userStopErr == nil) == disabled || (projectStopErr == nil) == disabled || !os.IsNotExist(helperErr) || noticeRequests.Load() != expectedNotices || metricRequests.Load() != expectedNotices || noticeInModelBody || uiBackend.starts.Load() != 0 || uiBackend.lists.Load() != 0 || len(metrics.Drain().Records) != int(1-expectedNotices) {
		t.Fatal("isolated client profile contract was not preserved")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Path()); !os.IsNotExist(err) {
		t.Fatal("owned runtime survived cleanup")
	}
}

var observedMessageSequence atomic.Uint64

func writeObservedMessage(w http.ResponseWriter, stream bool, model string, blocks []map[string]any, stop string) {
	writeObservedMessageID(w, stream, model, fmt.Sprintf("msg_owned_%d", observedMessageSequence.Add(1)), blocks, stop)
}

func writeObservedMessageID(w http.ResponseWriter, stream bool, model, id string, blocks []map[string]any, stop string) {
	message := map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": blocks, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0}}
	if !stream {
		_ = json.NewEncoder(w).Encode(message)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		w.(http.Flusher).Flush()
	}
	message["content"], message["stop_reason"] = []any{}, nil
	send(map[string]any{"type": "message_start", "message": message})
	for i, block := range blocks {
		start := map[string]any{"type": block["type"]}
		var delta map[string]any
		if block["type"] == "text" {
			start["text"] = ""
			delta = map[string]any{"type": "text_delta", "text": block["text"]}
		} else {
			start["id"], start["name"], start["input"] = block["id"], block["name"], map[string]any{}
			input, _ := json.Marshal(block["input"])
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(input)}
		}
		send(map[string]any{"type": "content_block_start", "index": i, "content_block": start})
		send(map[string]any{"type": "content_block_delta", "index": i, "delta": delta})
		send(map[string]any{"type": "content_block_stop", "index": i})
	}
	send(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 0}})
	send(map[string]any{"type": "message_stop"})
}
