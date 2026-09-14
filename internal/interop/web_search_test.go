// Independent local observation: no external model or Kiro, no retained request bodies.
package interop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/toolregistry"
	"dax-kiro-proxy/internal/websearch"
)

func TestClaudeWebSearchConversion(t *testing.T) {
	if os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Skip("local native client control; no Kiro model credits")
	}
	if !observeSearchConversion() {
		t.Fatal("native search conversion failed; see structural observations")
	}
}
func observeSearchConversion() bool {
	root, err := os.MkdirTemp("/private/tmp", "dax-websearch-owned-")
	if err != nil {
		fmt.Println("stage=root ok=false")
		return false
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, p := range []string{filepath.Join(home, ".claude"), project} {
		if os.MkdirAll(p, 0700) != nil {
			return false
		}
	}
	settings, global := filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json")
	if os.WriteFile(settings, []byte(`{"disableAllHooks":true,"autoMemoryEnabled":false}`), 0600) != nil || os.WriteFile(global, []byte(`{}`), 0600) != nil {
		return false
	}
	beforeSettings, _ := os.ReadFile(settings)
	beforeGlobal, _ := os.ReadFile(global)
	exe := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if !filepath.IsAbs(exe) {
		fmt.Println("stage=opt_in ok=false")
		return false
	}
	runner, err := childproc.New(childproc.Config{Timeout: 25 * time.Second, MaxOutputBytes: 128 << 10, MaxProcesses: 1})
	if err != nil {
		return false
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	version, err := runner.Run(ctx, childproc.Command{Executable: exe, Directory: project, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "DISABLE_AUTOUPDATER=1"}})
	v, ok := launcher.ClientVersionFromOutput(version.Stdout)
	if err != nil || !ok {
		fmt.Println("stage=version admitted=false")
		return false
	}
	fmt.Printf("stage=version version=%s measured=%t\n", v, v == launcher.SupportedClientVersion)
	worker, _ := filepath.Abs("../../dist/dax-kiro-proxy")
	validator, err := schemacheck.New(schemacheck.Config{Executable: worker, Directory: root, MaxWorkers: 1})
	if err != nil {
		return false
	}
	defer validator.Close()
	tokens, err := gateway.NewTokens()
	if err != nil {
		return false
	}
	const model = "claude-dax-web-observation"
	const answer = "Independent web observation complete."
	const query = `{"query":"independent local fixture search"}`
	var mu sync.Mutex
	requests, nested, mains := 0, 0, 0
	called, completed, resultError, resultHasURL, bad := false, false, false, false, false
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, h *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if h.Method == "HEAD" {
			w.WriteHeader(404)
			return
		}
		if h.Header.Get("Authorization") != "Bearer "+tokens.Model && h.Header.Get("x-api-key") != tokens.Model {
			w.WriteHeader(401)
			return
		}
		if h.Method == "GET" && h.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Owned web control"}}})
			return
		}
		if h.Method == "POST" && h.URL.Path == "/v1/messages/count_tokens" {
			io.WriteString(w, `{"input_tokens":1}`)
			return
		}
		if h.Method != "POST" || h.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		requests++
		if requests > 4 {
			bad = true
			w.WriteHeader(429)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(h.Body, anthropic.MaxBodyBytes+1))
		r, de := anthropic.DecodeRequest(raw)
		if e != nil || len(raw) > anthropic.MaxBodyBytes || de != nil {
			bad = true
			fmt.Println("stage=decode ok=false")
			w.WriteHeader(400)
			return
		}
		serverTool := false
		var clientSchema json.RawMessage
		for _, t := range r.Tools {
			var d struct {
				Name, Type string
				Schema     json.RawMessage `json:"input_schema"`
			}
			if json.Unmarshal(t, &d) != nil {
				bad = true
				w.WriteHeader(400)
				return
			}
			if d.Name == "WebSearch" {
				clientSchema = d.Schema
			}
			if len(d.Type) >= 11 && d.Type[:11] == "web_search_" {
				serverTool = true
				kind := "other_web_version"
				switch d.Type {
				case "web_search_20250305", "web_search_20260209", "web_search_20260318":
					kind = d.Type
				}
				fmt.Printf("server_tool=%s\n", kind)
			}
		}
		_, policyErr := r.ToolPolicy()
		_, registryErr := toolregistry.Build(h.Context(), r.Tools, nil, validator)
		if serverTool {
			nested++
			fmt.Printf("stage=nested count=%d decoded=true client_content=%v tool_policy=%v registry=%v stream=%v\n", nested, r.ClientContent(), policyErr == nil, registryErr == nil, r.Stream)
			if !called || nested != 1 || policyErr != nil || !errors.Is(registryErr, toolregistry.ErrRegistry) {
				bad = true
			}
			spec, matched, searchErr := anthropic.SearchDeclaration(r.Tools)
			fmt.Printf("search_declaration_match=%v valid=%v limit=%d\n", matched, searchErr == nil, spec.MaxUses)
			if searchErr != nil || !matched {
				bad = true
				w.WriteHeader(400)
				return
			}
			_, admissionErr := websearch.ValidateRequest(r, spec)
			fmt.Printf("search_request_admitted=%t model_matches_configured=%t\n", admissionErr == nil, r.Model == model)
			if admissionErr != nil || r.Model != model {
				bad = true
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			stream, e := anthropic.BeginStream(w, http.NewResponseController(w).Flush, "msg_search_result", model)
			if e == nil {
				e = stream.Search(anthropic.SearchExchange{ID: "srvtoolu_native_test", Query: "independent local fixture search", Results: []anthropic.SearchResult{{Type: "web_search_result", URL: "https://example.org/independent-search", Title: "Independent search reference"}}})
			}
			if e == nil {
				e = stream.Text("Independent search reference: https://example.org/independent-search")
			}
			if e == nil {
				e = stream.End("end_turn")
			}
			if e != nil {
				bad = true
			}
			return
		}
		mains++
		send := func(tool bool) {
			id := fmt.Sprintf("msg_owned_web_%d", requests)
			stop := "end_turn"
			blocks := []anthropic.ResponseBlock{{Type: "text", Text: searchPointer(answer)}}
			use := anthropic.ToolUse{ID: "owned_web_call", Name: "WebSearch", Input: json.RawMessage(query)}
			if tool {
				stop = "tool_use"
				blocks = []anthropic.ResponseBlock{use.Block()}
			}
			if !r.Stream {
				json.NewEncoder(w).Encode(anthropic.NewBlocksResponse(id, model, blocks, stop))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			s, e := anthropic.BeginStream(w, http.NewResponseController(w).Flush, id, model)
			if e == nil {
				if tool {
					e = s.Tool(use)
				} else {
					e = s.Text(answer)
				}
			}
			if e == nil {
				e = s.End(stop)
			}
			if e != nil {
				bad = true
			}
		}
		if !called {
			valid := clientSchema != nil && validator.Validate(h.Context(), clientSchema, []byte(query)) == nil
			fmt.Printf("stage=main decoded=true client_content=%v tool_policy=%v registry=%v search_advertised=%v arguments_valid=%v\n", r.ClientContent(), policyErr == nil, registryErr == nil, clientSchema != nil, valid)
			if !valid || policyErr != nil || registryErr != nil || !r.ClientContent() {
				bad = true
				w.WriteHeader(400)
				return
			}
			called = true
			send(true)
			return
		}
		results, e := r.LatestToolResults()
		if e != nil || len(results) != 1 || results[0].ID != "owned_web_call" || completed {
			bad = true
			w.WriteHeader(400)
			return
		}
		resultError = results[0].IsError
		for _, b := range results[0].Content {
			resultHasURL = resultHasURL || bytes.Contains([]byte(b.Text), []byte("https://example.org/independent-search"))
		}
		completed = true
		send(false)
	}))
	server.Config.ReadHeaderTimeout = time.Second
	server.Config.ReadTimeout = 5 * time.Second
	server.Config.WriteTimeout = 5 * time.Second
	server.Config.MaxHeaderBytes = 16 << 10
	server.Start()
	defer server.Close()
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: exe, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		fmt.Println("stage=profile ok=false")
		return false
	}
	defer profile.Close()
	cmd := profile.Command()
	cmd.Args = append(cmd.Args, "--print", "--output-format", "json", "--tools", "WebSearch", "--allowedTools", "WebSearch", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-session-persistence", "Perform the independent local fixture exchange.")
	result, runErr := runner.Run(ctx, cmd)
	runner.Close()
	validator.Close()
	stats := validator.Stats()
	validatorClosed := stats.Active == 0 && stats.Idle == 0
	joined := result.PID > 0 && errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
	profileClosed := profile.Close() == nil
	_, removed := os.Lstat(profile.Path())
	afterSettings, _ := os.ReadFile(settings)
	afterGlobal, _ := os.ReadFile(global)
	unchanged := sha256.Sum256(beforeSettings) == sha256.Sum256(afterSettings) && sha256.Sum256(beforeGlobal) == sha256.Sum256(afterGlobal)
	mu.Lock()
	defer mu.Unlock()
	good := runErr == nil && result.ExitCode == 0 && bytes.Contains(result.Stdout, []byte(answer)) && !bad && requests == 3 && mains == 2 && nested == 1 && completed && !resultError && resultHasURL && joined && validatorClosed && profileClosed && os.IsNotExist(removed) && unchanged
	fmt.Printf("version=%s requests=%d main_requests=%d nested_requests=%d result_error=%v result_has_url=%v completion=%v exit=%d run_error=%v joined=%v validator_closed=%v profile_removed=%v sources_unchanged=%v established=%v\n", v, requests, mains, nested, resultError, resultHasURL, completed, result.ExitCode, runErr != nil, joined, validatorClosed, profileClosed && os.IsNotExist(removed), unchanged, good)
	return good
}
func searchPointer(s string) *string { return &s }
