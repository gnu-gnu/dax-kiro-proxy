package interop_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/toolregistry"
)

// Observe the actual client's declaration, pass it through the production registry/worker, and
// complete an effect-free tool round trip. Only that owned tool and an advertised native MCP wait
// can be requested by this local responder. There is no Kiro or external model request.
func TestClaudeMCPSchemaDialects(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for local MCP dialect observations; no model credits")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("client executable must be absolute")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-schema-observation-")
	if err != nil {
		t.Fatal("cannot create owned observation root")
	}
	defer os.RemoveAll(root)
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("cannot prepare bounded observation runner")
	}
	defer runner.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot locate independent fixture source")
	}
	buildEnv := []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=go1.27.1", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0", "GOCACHE=" + os.Getenv("GOCACHE"), "GOMODCACHE=" + os.Getenv("GOMODCACHE")}
	peer, proxy := filepath.Join(root, "schema-peer"), filepath.Join(root, "schema-worker")
	for _, build := range []struct{ output, source string }{{peer, "./testdata/schemapeer"}, {proxy, "../../cmd/dax-kiro-proxy"}} {
		if _, err := runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: buildEnv, Args: []string{"build", "-o", build.output, build.source}}); err != nil {
			t.Fatal("cannot build independent schema control")
		}
	}
	version, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "DISABLE_AUTOUPDATER=1"}})
	clientVersion, verified := launcher.ClientVersionFromOutput(version.Stdout)
	if err != nil || !verified || clientVersion != launcher.SupportedClientVersion {
		t.Fatal("schema control requires the measured client")
	}
	for _, tc := range []struct {
		name, dialect string
		legacyTuple   bool
	}{
		{"default", "", false},
		{"draft-2020", "https://json-schema.org/draft/2020-12/schema", false},
		{"draft-7", "http://json-schema.org/draft-07/schema#", true},
		{"draft-2019", "https://json-schema.org/draft/2019-09/schema", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arm := filepath.Join(root, tc.name)
			home, project := filepath.Join(arm, "home"), filepath.Join(arm, "project")
			for _, dir := range []string{filepath.Join(home, ".claude"), project} {
				if os.MkdirAll(dir, 0700) != nil {
					t.Fatal("cannot prepare owned client workspace")
				}
			}
			write := func(path string, value any) {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil || os.WriteFile(path, data, 0600) != nil {
					t.Fatal("cannot write owned schema control input")
				}
			}
			settings := filepath.Join(home, ".claude", "settings.json")
			write(settings, map[string]bool{"disableAllHooks": true})
			global := filepath.Join(home, ".claude.json")
			write(global, map[string]any{})
			tuple := map[string]any{"type": "array", "prefixItems": []any{map[string]string{"type": "integer"}}, "items": false, "minItems": 1}
			if tc.legacyTuple {
				delete(tuple, "prefixItems")
				tuple["items"] = []any{map[string]string{"type": "integer"}}
				tuple["additionalItems"] = false
			}
			schema := map[string]any{"type": "object", "properties": map[string]any{"tuple": tuple}, "required": []string{"tuple"}, "additionalProperties": false}
			if tc.dialect != "" {
				schema["$schema"] = tc.dialect
			}
			schemaPath, witness, config := filepath.Join(arm, "schema.json"), filepath.Join(arm, "witness"), filepath.Join(arm, "mcp.json")
			write(schemaPath, schema)
			write(config, map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"command": peer, "args": []string{schemaPath, witness}}}})
			sources := []string{settings, global, schemaPath, config}
			before := make([][32]byte, len(sources))
			for i, path := range sources {
				before[i] = fileFingerprint(t, path)
			}
			expected, _ := json.Marshal(schema)
			validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: arm})
			if err != nil {
				t.Fatal("cannot prepare bounded schema worker")
			}
			defer validator.Close()
			tokens, err := gateway.NewTokens()
			if err != nil {
				t.Fatal("cannot prepare local authentication")
			}
			const model, name, answer = "claude-dax-schema-control", "mcp__owned__schema_probe", "Independent schema observation complete."
			var mu sync.Mutex
			requests, mains := 0, 0
			var waitDigest [32]byte
			bad, waited, called, complete, exact, admitted, validArgs, invalidRejected := false, false, false, false, false, false, false, false
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+tokens.Model && r.Header.Get("x-api-key") != tokens.Model {
					w.WriteHeader(401)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" && r.URL.Path == "/v1/models" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Owned schema control"}}})
					return
				}
				if r.Method == "POST" && r.URL.Path == "/v1/messages/count_tokens" {
					_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": 1})
					return
				}
				if r.Method != "POST" || r.URL.Path != "/v1/messages" {
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
				body, readErr := io.ReadAll(io.LimitReader(r.Body, anthropic.MaxBodyBytes+1))
				input, decodeErr := anthropic.DecodeRequest(body)
				if readErr != nil || len(body) > anthropic.MaxBodyBytes || decodeErr != nil || input.Model != model {
					bad = true
					w.WriteHeader(400)
					return
				}
				if requestfamily.Classify(input) == requestfamily.Title {
					writeObservedMessage(w, input.Stream, model, []map[string]any{{"type": "text", "text": `{"title":"Independent schema fixture"}`}}, "end_turn")
					return
				}
				mains++
				results, resultErr := input.LatestToolResults()
				if resultErr != nil || mains > 3 || complete {
					bad = true
					w.WriteHeader(400)
					return
				}
				if called {
					found := 0
					for _, result := range results {
						if result.ID == "schema_call" && !result.IsError && len(result.Content) == 1 && result.Content[0].Type == "text" && result.Content[0].Text == "independent schema tool complete" {
							found++
						} else {
							data, _ := json.Marshal(result)
							bad = bad || result.ID != "schema_wait" || !waited || result.IsError || sha256.Sum256(data) != waitDigest
						}
					}
					if bad || found != 1 || len(results) > 2 {
						bad = true
						w.WriteHeader(400)
						return
					}
					complete = true
					writeObservedMessage(w, input.Stream, model, []map[string]any{{"type": "text", "text": answer}}, "end_turn")
					return
				}
				var owned, wait json.RawMessage
				for _, raw := range input.Tools {
					var tool struct {
						Name   string
						Schema json.RawMessage `json:"input_schema"`
					}
					if json.Unmarshal(raw, &tool) != nil {
						bad = true
						continue
					}
					if tool.Name == name {
						owned = raw
						canonical, err := jsoncanon.Object(tool.Schema)
						exact = err == nil && bytes.Equal(canonical, expected)
					} else if tool.Name == "WaitForMcpServers" {
						wait = tool.Schema
					}
				}
				if owned == nil && !waited && wait != nil && len(results) == 0 && validator.Validate(r.Context(), wait, []byte(`{}`)) == nil {
					waited = true
					writeObservedMessage(w, input.Stream, model, []map[string]any{{"type": "tool_use", "id": "schema_wait", "name": "WaitForMcpServers", "input": json.RawMessage(`{}`)}}, "tool_use")
					return
				}
				if !exact || bad || owned == nil || (!waited && len(results) != 0) || (waited && (len(results) != 1 || results[0].ID != "schema_wait" || results[0].IsError)) {
					bad = true
					w.WriteHeader(400)
					return
				}
				if waited {
					data, _ := json.Marshal(results[0])
					waitDigest = sha256.Sum256(data)
				}
				registry, err := toolregistry.Build(r.Context(), []json.RawMessage{owned}, nil, validator)
				admitted = err == nil
				if admitted {
					tool := registry.Tools()[0]
					_, err = registry.Validate(r.Context(), tool.Alias, []byte(`{"tuple":[7]}`))
					validArgs = err == nil && tool.Name == name && bytes.Equal(tool.Schema, expected)
					_, err = registry.Validate(r.Context(), tool.Alias, []byte(`{"tuple":[7,8]}`))
					invalidRejected = errors.Is(err, toolregistry.ErrArguments)
				}
				if !admitted || !validArgs || !invalidRejected {
					bad = true
					w.WriteHeader(400)
					return
				}
				called = true
				writeObservedMessage(w, input.Stream, model, []map[string]any{{"type": "tool_use", "id": "schema_call", "name": name, "input": json.RawMessage(`{"tuple":[7]}`)}}, "tool_use")
			}))
			server.Config.ReadHeaderTimeout = time.Second
			server.Config.ReadTimeout = 5 * time.Second
			server.Config.WriteTimeout = 6 * time.Second
			server.Config.MaxHeaderBytes = 16 << 10
			server.Start()
			defer server.Close()
			profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: arm, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: clientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
			if err != nil {
				t.Fatal("cannot prepare isolated schema client")
			}
			defer profile.Close()
			command := profile.Command()
			command.Args = append(command.Args, "--print", "--output-format", "json", "--no-session-persistence", "--strict-mcp-config", "--mcp-config", config, "--allowedTools", name, "--system-prompt", "Independent local MCP schema observation.", "Complete the owned tool exercise.")
			result, runErr := runner.Run(t.Context(), command)
			var response struct {
				Result  string
				IsError bool `json:"is_error"`
			}
			parsed := json.Unmarshal(result.Stdout, &response) == nil
			groupGone := result.PID > 1 && errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
			mu.Lock()
			t.Logf("client=%s requests=%d main_requests=%d waited=%v exact_schema=%v registry_admitted=%v valid_arguments=%v invalid_rejected=%v complete=%v run_failed=%v exit=%d parsed=%v group_joined=%v", clientVersion, requests, mains, waited, exact, admitted, validArgs, invalidRejected, complete, runErr != nil, result.ExitCode, parsed, groupGone)
			ok := !bad && exact && admitted && validArgs && invalidRejected && called && complete && mains >= 2 && mains <= 3
			mu.Unlock()
			if !ok || runErr != nil || !parsed || response.IsError || response.Result != answer || !groupGone {
				t.Fatal("native schema declaration or owned tool round trip failed")
			}
			data, err := os.ReadFile(witness)
			if err != nil || len(data) > 512 {
				t.Fatal("missing bounded peer witness")
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 4 {
				t.Fatal("unexpected peer lifecycle count")
			}
			owner := 0
			for i, event := range []string{"started", "initialized", "listed", "called"} {
				var pid int
				var label string
				if _, err := fmt.Sscanf(lines[i], "%d %s", &pid, &label); err != nil || pid <= 1 || label != event || i > 0 && pid != owner {
					t.Fatal("invalid owned peer lifecycle")
				}
				owner = pid
			}
			if !errors.Is(syscall.Kill(owner, 0), syscall.ESRCH) {
				t.Fatal("owned MCP peer survived client cleanup")
			}
			for i, path := range sources {
				if fileFingerprint(t, path) != before[i] {
					t.Fatal("schema control changed source input")
				}
			}
			validator.Close()
			if stats := validator.Stats(); stats.Active != 0 || stats.Idle != 0 {
				t.Fatal("schema worker retained after cleanup")
			}
			if profile.Close() != nil {
				t.Fatal("schema profile cleanup failed")
			}
			if _, err := os.Stat(profile.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("schema profile retained after cleanup")
			}
			t.Log("peer_calls=1 peer_joined=true sources_unchanged=true profile_removed=true worker_closed=true")
		})
	}
}
