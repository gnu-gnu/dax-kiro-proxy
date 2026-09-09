package interop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

// The marketplace, plugin and MCP peer are owned fixtures. Public client commands install and
// activate the plugin; this test never downloads a plugin or calls an external model/tool.
func TestClaudeClientPluginSeedSources(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for owned plugin-source controls; no external inference")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-client-plugins-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, project, observations := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "observations")
	market := filepath.Join(root, "market")
	for _, path := range []string{filepath.Join(home, ".claude"), project, observations, filepath.Join(market, ".claude-plugin"), filepath.Join(market, "owned-plugin", ".claude-plugin")} {
		if os.MkdirAll(path, 0700) != nil {
			t.Fatal("cannot create owned plugin source")
		}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot write owned plugin fixture")
		}
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	writeJSON(settings, map[string]any{})
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-dax-plugin-fixture"
	var messageRequests, advertisedPlugin atomic.Int32
	var wantInitializations atomic.Int32
	var wantToolListings atomic.Int32
	var requireQuietWindow atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Independent plugin fixture"}}})
		case "/v1/messages/count_tokens":
			_, _ = w.Write([]byte(`{"input_tokens":1}`))
		case "/v1/messages":
			if messageRequests.Add(1) > 3 {
				w.WriteHeader(429)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			var input struct {
				Stream bool `json:"stream"`
				Tools  []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err != nil || len(body) > 1<<20 || json.Unmarshal(body, &input) != nil {
				w.WriteHeader(400)
				return
			}
			for _, tool := range input.Tools {
				if tool.Name == "mcp__plugin_dax-owned_owned__owned_probe" {
					advertisedPlugin.Add(1)
				}
			}
			// Do not finish the synthetic turn before the asynchronously loaded owned peer has
			// completed initialization. This observes lifecycle readiness without asking for a tool.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				f, err := os.Open(filepath.Join(observations, "plugin"))
				if err == nil {
					data, readErr := io.ReadAll(io.LimitReader(f, 8193))
					f.Close()
					if readErr == nil && len(data) <= 8192 && int32(bytes.Count(data, []byte(" initialized\n"))) >= wantInitializations.Load() && int32(bytes.Count(data, []byte(" listed\n"))) >= wantToolListings.Load() && !requireQuietWindow.Load() {
						break
					}
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
			writeObservedMessage(w, input.Stream, model, []map[string]any{{"type": "text", "text": "independent plugin observation complete"}}, "end_turn")
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	config := launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}}
	p, err := launcher.PrepareClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	natural := p.Command()
	natural.Args = nil
	natural.Environment = nil
	for _, entry := range p.Command().Environment {
		if !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") && !strings.HasPrefix(entry, "CLAUDE_CODE_PLUGIN_SEED_DIR=") {
			natural.Environment = append(natural.Environment, entry)
		}
	}
	run := func(t *testing.T, command childproc.Command, args ...string) childproc.Result {
		t.Helper()
		command.Args = append(append([]string(nil), command.Args...), args...)
		result, err := runner.Run(ctx, command)
		if err != nil {
			t.Fatalf("owned plugin command failed: exit=%d, stdout_bytes=%d", result.ExitCode, len(result.Stdout))
		}
		return result
	}
	if version := run(t, natural, "--version"); strings.TrimSpace(string(version.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified client version")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	buildEnv := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(key); value != "" {
			buildEnv = append(buildEnv, key+"="+value)
		}
	}
	peer := filepath.Join(root, "client-asset-peer")
	if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: buildEnv, Args: []string{"build", "-o", peer, "./testdata/clientassets"}}); err != nil {
		t.Fatal("cannot build independent plugin MCP peer")
	}
	const pluginID = "dax-owned@dax-owned-market"
	writeJSON(filepath.Join(market, ".claude-plugin", "marketplace.json"), map[string]any{"name": "dax-owned-market", "owner": map[string]string{"name": "Independent fixture"}, "plugins": []any{map[string]string{"name": "dax-owned", "source": "./owned-plugin"}}})
	writeJSON(filepath.Join(market, "owned-plugin", ".claude-plugin", "plugin.json"), map[string]string{"name": "dax-owned", "version": "1.0.0", "description": "Independent no-effect client preservation fixture."})
	writeJSON(filepath.Join(market, "owned-plugin", ".mcp.json"), map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"command": peer, "args": []string{"plugin", observations}}}})
	run(t, natural, "plugin", "marketplace", "add", market)
	run(t, natural, "plugin", "install", pluginID, "--scope", "user")
	if result := run(t, natural, "plugin", "list", "--json"); !bytes.Contains(result.Stdout, []byte(pluginID)) {
		t.Fatal("public installer did not register owned plugin")
	}
	seed := filepath.Join(home, ".claude", "plugins")
	for _, fixturePath := range []string{"known_marketplaces.json", "installed_plugins.json", "marketplaces/dax-owned-market/.claude-plugin/marketplace.json", "cache/dax-owned-market/dax-owned/1.0.0/.claude-plugin/plugin.json"} {
		_, err := os.Stat(filepath.Join(seed, fixturePath))
		t.Logf("owned_seed_entry=%s, exists=%v", fixturePath, err == nil)
	}
	for _, tc := range []struct {
		name     string
		expected int
	}{
		{"natural", 1},
		{"prepared", 1},
		{"seed", 1},
		{"seed_print", 2},
		{"seed_print_disabled", 2},
		{"seed_print_reenabled", 3},
	} {
		if tc.name == "seed_print_disabled" {
			run(t, natural, "plugin", "disable", pluginID, "--scope", "user")
		}
		if tc.name == "seed_print_reenabled" {
			run(t, natural, "plugin", "enable", pluginID, "--scope", "user")
		}
		t.Run(tc.name, func(t *testing.T) {
			command := natural
			if tc.name != "natural" {
				fresh, err := launcher.PrepareClient(config)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if fresh.Close() != nil {
						t.Error("plugin profile cleanup failed")
					}
				}()
				command = fresh.Command()
				if tc.name == "prepared" {
					// Keep an explicit unseeded counterfactual after production integration.
					original := command.Environment
					command.Environment = nil
					for _, entry := range original {
						if !strings.HasPrefix(entry, "CLAUDE_CODE_PLUGIN_SEED_DIR=") {
							command.Environment = append(command.Environment, entry)
						}
					}
				} else {
					matched := 0
					for _, entry := range command.Environment {
						if entry == "CLAUDE_CODE_PLUGIN_SEED_DIR="+seed {
							matched++
						}
					}
					if matched != 1 {
						t.Fatal("prepared client did not supply the owned plugin seed")
					}
				}
			}
			before := boundedPluginTree(t, seed)
			settingsBefore := boundedAssetFile(t, settings)
			globalBefore := boundedAssetFile(t, filepath.Join(home, ".claude.json"))
			listsBefore := 0
			if _, err := os.Stat(filepath.Join(observations, "plugin")); err == nil {
				listsBefore = bytes.Count(boundedAssetFile(t, filepath.Join(observations, "plugin")), []byte(" listed\n"))
			} else if !os.IsNotExist(err) {
				t.Fatal("cannot inspect owned plugin lifecycle")
			}
			if tc.name == "seed" {
				run(t, command, "--init-only")
				listing := run(t, command, "plugin", "list", "--json")
				t.Logf("seed_plugin_listed=%v", bytes.Contains(listing.Stdout, []byte(pluginID)))
			}
			var result childproc.Result
			if strings.HasPrefix(tc.name, "seed_print") {
				wantInitializations.Store(int32(tc.expected))
				requireQuietWindow.Store(tc.name == "seed_print_disabled")
				wantToolListings.Store(int32(listsBefore + 1))
				beforeRequests := messageRequests.Load()
				result = run(t, command, "--print", "--output-format", "json", "--no-session-persistence", "Return a short text response without calling any tool.")
				t.Logf("synthetic_messages=%d, plugin_advertisements=%d", messageRequests.Load(), advertisedPlugin.Load())
				if messageRequests.Load() != beforeRequests+1 || !bytes.Contains(result.Stdout, []byte("independent plugin observation complete")) {
					t.Error("synthetic plugin print conversation did not complete")
				}
			} else {
				result = run(t, command, "mcp", "list")
			}
			started, initialized, called := inspectClientAssetProcesses(t, filepath.Join(observations, "plugin"))
			listsAfter := bytes.Count(boundedAssetFile(t, filepath.Join(observations, "plugin")), []byte(" listed\n"))
			t.Logf("plugin_named=%v, starts=%d, initializations=%d, calls=%d, tool_lists=%d", bytes.Contains(result.Stdout, []byte("plugin:dax-owned:owned")), started, initialized, called, listsAfter)
			if started != tc.expected || initialized != started || called != 0 {
				t.Error("plugin connection did not match active source control")
			}
			if strings.HasPrefix(tc.name, "seed_print") && ((tc.name == "seed_print_disabled" && listsAfter != listsBefore) || (tc.name != "seed_print_disabled" && listsAfter <= listsBefore)) {
				t.Error("plugin tool discovery did not match enabled state")
			}
			if tc.name != "natural" && (!reflect.DeepEqual(before, boundedPluginTree(t, seed)) || !bytes.Equal(settingsBefore, boundedAssetFile(t, settings)) || !bytes.Equal(globalBefore, boundedAssetFile(t, filepath.Join(home, ".claude.json")))) {
				t.Error("prepared plugin command changed source state")
			}
		})
		if t.Failed() {
			return
		}
	}
	if err := p.Close(); err != nil {
		t.Error("initial plugin profile cleanup failed")
	}
}

type pluginSourceEntry struct {
	mode fs.FileMode
	sum  [32]byte
}

// Fingerprint only the bounded owned fixture tree, including empty directories, modes and link
// targets without following them. Modification times are not treated as source content.
func boundedPluginTree(t *testing.T, path string) map[string]pluginSourceEntry {
	t.Helper()
	result := make(map[string]pluginSourceEntry)
	total, entries := 0, 0
	if err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > 256 {
			t.Fatal("owned plugin tree exceeds entry bound")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[path] = pluginSourceEntry{mode: info.Mode()}
			return nil
		}
		var data []byte
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			data = []byte(target)
		} else if entry.Type().IsRegular() {
			data = boundedAssetFile(t, path)
		} else {
			t.Fatal("unexpected owned plugin entry type")
		}
		total += len(data)
		if total > 8<<20 {
			t.Fatal("owned plugin tree exceeds byte bound")
		}
		result[path] = pluginSourceEntry{mode: info.Mode(), sum: sha256.Sum256(data)}
		return nil
	}); err != nil {
		t.Fatal("cannot inspect owned plugin tree")
	}
	return result
}
