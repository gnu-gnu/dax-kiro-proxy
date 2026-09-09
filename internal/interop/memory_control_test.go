package interop_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/ndjson"
)

// The SDK documents context and settings inspection, but its prose does not define
// every CLI wire envelope. These independently authored read-only requests test the
// pinned public executable as a black box. No SDK code or provider inference is used.
func TestClaudeMemoryControlObservations(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for owned memory controls")
	}
	for _, mode := range []string{"rule-plain", "rule-conditional", "rule-excluded", "user-alias", "user-alias-excluded"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp("/private/tmp", "dax-memory-control-")
			if err != nil {
				t.Fatal("cannot prepare owned control root")
			}
			defer os.RemoveAll(root)
			home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
			base := filepath.Join(home, ".claude")
			if os.MkdirAll(base, 0700) != nil || os.Mkdir(project, 0700) != nil {
				t.Fatal("fixture")
			}
			original := filepath.Join(base, "CLAUDE.md")
			content := "Independent root memory marker.\n"
			if mode == "rule-conditional" {
				content = "---\npaths:\n  - \"never-opened/*.go\"\n---\n" + content
			}
			if os.WriteFile(original, []byte(content), 0600) != nil || os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0600) != nil {
				t.Fatal("fixture")
			}
			settings := filepath.Join(base, "settings.json")
			value := map[string]any{"disableAllHooks": true, "autoMemoryEnabled": false}
			excluded := strings.HasSuffix(mode, "excluded")
			if excluded {
				value["claudeMdExcludes"] = []string{original}
			}
			data, _ := json.Marshal(value)
			if os.WriteFile(settings, data, 0600) != nil {
				t.Fatal("fixture")
			}
			tokens, err := gateway.NewTokens()
			if err != nil {
				t.Fatal(err)
			}
			var modelRequests, counts atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "HEAD" {
					w.WriteHeader(404)
					return
				}
				if r.Header.Get("x-api-key") != tokens.Model && r.Header.Get("Authorization") != "Bearer "+tokens.Model {
					w.WriteHeader(401)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/models":
					_, _ = w.Write([]byte(`{"data":[{"id":"claude-dax-memory-control","display_name":"Independent memory"}]}`))
				case "/v1/messages/count_tokens":
					counts.Add(1)
					_, _ = w.Write([]byte(`{"input_tokens":1}`))
				case "/v1/messages":
					modelRequests.Add(1)
					w.WriteHeader(400)
				default:
					w.WriteHeader(404)
				}
			}))
			server.Config.ReadHeaderTimeout = time.Second
			server.Config.ReadTimeout = 5 * time.Second
			server.Config.WriteTimeout = 5 * time.Second
			server.Config.MaxHeaderBytes = 16 << 10
			server.Start()
			defer server.Close()
			profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: launcher.SupportedClientVersion, Model: "claude-dax-memory-control", GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
			if err != nil {
				t.Fatal(err)
			}
			defer profile.Close()
			destination := filepath.Join(profile.Path(), "client", "CLAUDE.md")
			if !strings.HasPrefix(mode, "user-alias") {
				rules := filepath.Join(profile.Path(), "client", "rules")
				if os.Mkdir(rules, 0700) != nil {
					t.Fatal("fixture")
				}
				destination = filepath.Join(rules, "root.md")
			}
			if os.Symlink(original, destination) != nil {
				t.Fatal("fixture")
			}
			before := boundedPluginTree(t, home)
			command := profile.Command()
			version := command
			version.Args = []string{"--version"}
			runner, err := childproc.New(childproc.Config{Timeout: 5 * time.Second, MaxOutputBytes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()
			v, err := runner.Run(t.Context(), version)
			if err != nil || strings.TrimSpace(string(v.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
				t.Fatal("unverified client")
			}
			command.Args = append(command.Args, "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`)
			stdin, send, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stdin.Close(); send.Close() })
			read, stdout, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { read.Close(); stdout.Close() })
			null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { null.Close() })
			owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 15 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			process, err := owner.Start(t.Context(), command, childproc.AttachedIO{Stdin: stdin, Stdout: stdout, Stderr: null})
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			stdin.Close()
			stdout.Close()
			reader := bufio.NewReaderSize(read, 64<<10)
			frames := 0
			request := func(id, method string, extra map[string]any) (map[string]json.RawMessage, bool) {
				t.Helper()
				body := map[string]any{"subtype": method}
				for k, v := range extra {
					body[k] = v
				}
				data, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": body})
				if _, err := send.Write(append(data, '\n')); err != nil {
					t.Fatal("control input failed")
				}
				if read.SetReadDeadline(time.Now().Add(5*time.Second)) != nil {
					t.Fatal("control deadline")
				}
				for {
					frames++
					if frames > 32 {
						t.Fatal("control frame limit")
					}
					line, err := reader.ReadSlice('\n')
					if err != nil {
						t.Fatal("control response absent or oversized", err)
					}
					fields, err := ndjson.Object(line)
					if err != nil {
						t.Fatal("invalid control response")
					}
					if string(fields["type"]) != `"control_response"` {
						continue
					}
					response, err := ndjson.Object(fields["response"])
					if err != nil {
						t.Fatal("invalid control result")
					}
					var gotID string
					if json.Unmarshal(response["request_id"], &gotID) != nil || gotID != id {
						t.Fatal("uncorrelated control response")
					}
					if string(response["subtype"]) != `"success"` {
						return nil, false
					}
					result, err := ndjson.Object(response["response"])
					if err != nil {
						t.Fatal("invalid successful control result")
					}
					return result, true
				}
			}
			if _, ok := request("dax-memory-init", "initialize", nil); !ok {
				t.Fatal("client control initialization rejected")
			}
			settingsResult, settingsOK := request("dax-memory-settings", "get_settings", nil)
			effective, effectiveErr := ndjson.Object(settingsResult["effective"])
			var excludes []string
			if raw, exists := effective["claudeMdExcludes"]; exists && json.Unmarshal(raw, &excludes) != nil {
				t.Fatal("invalid effective exclusion list")
			}
			wantExcludes := []string(nil)
			if excluded {
				wantExcludes = []string{original}
			}
			if !settingsOK || effectiveErr != nil || !reflect.DeepEqual(excludes, wantExcludes) {
				t.Error("live effective exclusions differ")
			}
			contextResult, contextOK := request("dax-memory-context", "get_context_usage", map[string]any{"detail": "summary"})
			var memories []struct {
				Path, Type string
				Tokens     float64
			}
			if contextOK && json.Unmarshal(contextResult["memoryFiles"], &memories) != nil {
				t.Fatal("invalid memory listing")
			}
			originalListed, aliasListed, homeRelative, projectRelative := false, false, false, false
			relative, err := filepath.Rel(project, original)
			if err != nil {
				t.Fatal("owned path comparison")
			}
			for _, memory := range memories {
				originalListed = originalListed || memory.Path == original
				aliasListed = aliasListed || memory.Path == destination
				homeRelative = homeRelative || memory.Path == "~/.claude/CLAUDE.md"
				projectRelative = projectRelative || memory.Path == relative
			}
			keys := func(m map[string]json.RawMessage) string {
				var expected []string
				for _, key := range []string{"effective", "settings", "sources", "provenance", "claudeMdExcludes"} {
					if _, ok := m[key]; ok {
						expected = append(expected, key)
					}
				}
				return strings.Join(expected, ",")
			}
			t.Logf("settings_ok=%v settings_fixed_keys=%s context_ok=%v memory_count=%d original_listed=%v alias_listed=%v home_relative=%v project_relative=%v model_requests=%d count_requests=%d", settingsOK, keys(settingsResult), contextOK, len(memories), originalListed, aliasListed, homeRelative, projectRelative, modelRequests.Load(), counts.Load())
			wantMemories := 1
			if mode == "rule-conditional" || mode == "rule-excluded" {
				wantMemories = 0
			}
			if len(memories) != wantMemories {
				t.Error("memory exclusion/conditional observation changed")
			}
			if originalListed != (mode == "rule-plain") || aliasListed != strings.HasPrefix(mode, "user-alias") || homeRelative || projectRelative {
				t.Error("native memory path identity changed")
			}
			if !contextOK || modelRequests.Load() != 0 || counts.Load() != 0 {
				t.Error("effect-free context inspection unavailable")
			}
			send.Close()
			result, err := process.Wait()
			if err != nil || result.ExitCode != 0 || !errors.Is(syscall.Kill(-process.PID(), 0), syscall.ESRCH) {
				t.Error("control process cleanup failed", err)
			}
			after := boundedPluginTree(t, home)
			if !reflect.DeepEqual(before, after) {
				t.Error("memory control changed original files")
			}
			if profile.Close() != nil {
				t.Error("profile cleanup failed")
			}
			if _, err := os.Lstat(profile.Path()); !os.IsNotExist(err) {
				t.Error("control profile remains")
			}
		})
	}
}

// This candidate intentionally redirects only the owned flag overlay, after process-level
// configuration has pointed at the private profile. It is never used by the launcher.
func redirectOwnedClientConfig(t *testing.T, profile *launcher.ClientProfile, home string, skipHistory bool) {
	t.Helper()
	data, err := os.ReadFile(profile.SettingsPath())
	var overlay map[string]any
	if err != nil || json.Unmarshal(data, &overlay) != nil {
		t.Fatal("cannot read owned environment candidate")
	}
	env, ok := overlay["env"].(map[string]any)
	if !ok {
		t.Fatal("owned overlay has no environment")
	}
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(home, ".claude")
	if skipHistory {
		env["CLAUDE_CODE_SKIP_PROMPT_HISTORY"] = "1"
	}
	data, err = json.Marshal(overlay)
	if err != nil || os.WriteFile(profile.SettingsPath(), data, 0600) != nil {
		t.Fatal("cannot encode owned environment candidate")
	}
}
