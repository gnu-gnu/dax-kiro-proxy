// These opt-in observations run an unmodified installed client against a synthetic local server.
// They never call Kiro or an external inference provider and never retain a client's prompt body.
package interop_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
)

type boundedOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(p), (1<<20)-len(b.data))
	b.data = append(b.data, p[:n]...)
	return len(p), nil
}
func (b *boundedOutput) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

type contractObservation struct {
	Messages, Models                     int
	SessionHeaderMatches, DecodeAccepted bool
	Fields                               []string
	ToolCount                            int
	ToolFields                           []string
	Roles                                []string
	ContentTypes                         [][]string
	SystemTypes                          []string
	MaxTokens                            int64
	ModelMatches                         bool
}

func TestClaudeClientGatewayContract(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY to an installed unmodified client; fixture-only, no model credits")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("client executable must be absolute")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-client-probe-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	profile := filepath.Join(root, "profile")
	scratch := filepath.Join(root, "tmp")
	for _, dir := range []string{workspace, profile, scratch} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	settings := filepath.Join(profile, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"permissions":{"defaultMode":"manual"},"skipWebFetchPreflight":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	protected := []string{filepath.Join(os.Getenv("HOME"), ".claude.json"), filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")}
	before := make([][32]byte, len(protected))
	for i, path := range protected {
		before[i] = fileFingerprint(t, path)
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(entropy[:])
	const sessionID = "2bb45d18-d8d2-43dd-a0b1-bf8dd42f9951"
	const model = "claude-dax-interoperability-one"
	const answer = "synthetic client round trip complete"
	var mu sync.Mutex
	observation := contractObservation{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("x-api-key") != token && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			mu.Lock()
			observation.Models++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": model, "display_name": "Synthetic one", "description": "Independent gateway fixture"}, map[string]any{"id": "claude-dax-interoperability-two", "display_name": "Synthetic two", "description": "Independent second fixture"}}})
			return
		}
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, anthropic.MaxBodyBytes+1))
		if err != nil || len(body) > anthropic.MaxBodyBytes {
			w.WriteHeader(413)
			return
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil {
			w.WriteHeader(400)
			return
		}
		var requestModel string
		_ = json.Unmarshal(fields["model"], &requestModel)
		decoded, decodeErr := anthropic.DecodeRequest(body)
		mu.Lock()
		observation.Messages++
		observation.SessionHeaderMatches = r.Header.Get("x-claude-code-session-id") == sessionID
		observation.DecodeAccepted = decodeErr == nil
		_ = json.Unmarshal(fields["max_tokens"], &observation.MaxTokens)
		observation.ModelMatches = requestModel == model
		var messageShapes []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(fields["messages"], &messageShapes)
		observation.Roles = nil
		observation.ContentTypes = nil
		for _, m := range messageShapes {
			observation.Roles = append(observation.Roles, m.Role)
			observation.ContentTypes = append(observation.ContentTypes, blockTypes(m.Content))
		}
		observation.SystemTypes = blockTypes(fields["system"])
		observation.Fields = nil
		for k := range fields {
			observation.Fields = append(observation.Fields, k)
		}
		sort.Strings(observation.Fields)
		if decoded != nil {
			observation.ToolCount = len(decoded.Tools)
			if len(decoded.Tools) > 0 {
				var tool map[string]any
				_ = json.Unmarshal(decoded.Tools[0], &tool)
				observation.ToolFields = nil
				for k := range tool {
					observation.ToolFields = append(observation.ToolFields, k)
				}
				sort.Strings(observation.ToolFields)
			}
		}
		mu.Unlock()
		// No request values or prompts survive this handler. Only field names and Boolean checks do.
		result := map[string]any{"id": "msg_independent_fixture", "type": "message", "role": "assistant", "model": requestModel, "content": []any{map[string]any{"type": "text", "text": answer}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}
		if string(fields["stream"]) != "true" {
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		start := map[string]any{"id": "msg_independent_fixture", "type": "message", "role": "assistant", "model": requestModel, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}
		for _, event := range []map[string]any{{"type": "message_start", "message": start}, {"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}, {"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": answer}}, {"type": "content_block_stop", "index": 0}, {"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 0}}, {"type": "message_stop"}} {
			encoded, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()
	cmd := exec.Command(executable, "--print", "--output-format", "json", "--model", model, "--session-id", sessionID, "--settings", settings, "--setting-sources", "", "--strict-mcp-config", "--tools", "Read", "--no-session-persistence", "--system-prompt", "Synthetic local protocol exercise.", "Return the fixture response.")
	cmd.Dir = workspace
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8", "CLAUDE_CONFIG_DIR=" + profile, "ANTHROPIC_BASE_URL=" + server.URL, "ANTHROPIC_API_KEY=" + token, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1", "CLAUDE_CODE_MAX_RETRIES=0", "NO_PROXY=127.0.0.1,localhost"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr boundedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal("cannot start isolated client")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	select {
	case err = <-done:
	case <-time.After(15 * time.Second):
		timedOut = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			err = <-done
		}
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	for i, path := range protected {
		if fileFingerprint(t, path) != before[i] {
			t.Error("client global settings changed")
		}
	}
	if timedOut || err != nil || !bytes.Contains(stdout.Bytes(), []byte(answer)) {
		t.Fatalf("client did not finish the local fixture (timeout=%v, exit_error=%v, stdout_bytes=%d, stderr_bytes=%d)", timedOut, err, len(stdout.Bytes()), len(stderr.Bytes()))
	}
	mu.Lock()
	defer mu.Unlock()
	report, _ := json.Marshal(observation)
	t.Log(string(report))
	if observation.Messages == 0 || !observation.SessionHeaderMatches || !observation.DecodeAccepted {
		t.Fatal("observed request does not satisfy the gateway session/decoder contract")
	}
	// Print mode may exit before the asynchronous discovery request; interactive UI is a separate gate.
	cache, err := os.ReadFile(filepath.Join(profile, "cache", "gateway-models.json"))
	t.Logf("discovery_cache_contains_both=%v", err == nil && strings.Contains(string(cache), model) && strings.Contains(string(cache), "claude-dax-interoperability-two"))
}
func fileFingerprint(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return [32]byte{}
	}
	if err != nil {
		t.Fatal("cannot fingerprint protected settings")
	}
	return sha256.Sum256(data)
}

func blockTypes(raw []byte) []string {
	if len(raw) > 0 && raw[0] == '"' {
		return []string{"string"}
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return []string{"invalid-content-shape"}
	}
	result := []string{}
	for _, b := range blocks {
		switch b.Type {
		case "text", "tool_use", "tool_result", "thinking", "redacted_thinking", "image", "document", "tool_reference":
			result = append(result, b.Type)
		default:
			result = append(result, "unrecognized-block")
		}
	}
	return result
}
