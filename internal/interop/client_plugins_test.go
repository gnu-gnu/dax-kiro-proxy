package interop_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

// The marketplace, plugin and MCP peer are owned fixtures. Public client commands install and
// activate the plugin; this test never downloads a plugin or calls an external model/tool.
func TestClaudeClientPluginSeedSources(t *testing.T) {
	observeClaudePluginSources(t, "sources")
}

func TestClaudePluginToolRoundTripShape(t *testing.T) {
	observeClaudePluginSources(t, "warm-tool")
}

func TestClaudePluginInteractiveFirstTool(t *testing.T) {
	observeClaudePluginSources(t, "interactive-tool")
}

func TestClaudePluginWaitThenToolShape(t *testing.T) {
	observeClaudePluginSources(t, "wait-tool")
}

func TestClaudePluginDuplicateMessageIDControl(t *testing.T) {
	observeClaudePluginSources(t, "wait-tool-duplicate")
}

func TestClaudePluginWaitThroughGatewayAndACP(t *testing.T) {
	t.Run("allowed", func(t *testing.T) { observeClaudePluginSources(t, "proxy-wait-tool") })
	t.Run("hook_denied", func(t *testing.T) { observeClaudePluginSources(t, "proxy-wait-tool-denied") })
}

func TestClaudeGuardedPluginRegistrySequence(t *testing.T) {
	t.Run("allowed", func(t *testing.T) { observeClaudePluginSources(t, "guarded-wait-tool") })
	t.Run("hook_denied", func(t *testing.T) { observeClaudePluginSources(t, "guarded-wait-tool-denied") })
}

func TestClaudePluginResultPhrase(t *testing.T) {
	t.Run("allowed", func(t *testing.T) { observeClaudePluginSources(t, "guarded-wait-tool-phrase") })
	t.Run("hook_denied", func(t *testing.T) { observeClaudePluginSources(t, "guarded-wait-tool-phrase-denied") })
}

func TestClaudePluginToolThroughGatewayAndACP(t *testing.T) {
	t.Run("allowed", func(t *testing.T) { observeClaudePluginSources(t, "proxy-tool") })
	t.Run("hook_denied", func(t *testing.T) { observeClaudePluginSources(t, "proxy-tool-denied") })
}

func observeClaudePluginSources(t *testing.T, mode string) {
	t.Helper()
	liveMode := strings.HasPrefix(mode, "live-wait-tool")
	guardedMode := strings.HasPrefix(mode, "guarded-wait-tool")
	phraseMode := liveMode || strings.HasPrefix(mode, "guarded-wait-tool-phrase")
	if liveMode && (os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" || os.Getenv("DAX_INTEROP_KIRO_BINARY") == "") {
		t.Fatal("live plugin mode requires explicit Kiro opt-in")
	}
	proxyMode, denied := liveMode || guardedMode || strings.HasPrefix(mode, "proxy-tool") || strings.HasPrefix(mode, "proxy-wait-tool"), strings.HasSuffix(mode, "-denied")
	waitMode := liveMode || guardedMode || strings.HasPrefix(mode, "wait-tool") || strings.HasPrefix(mode, "proxy-wait-tool")
	proxyRequests := int32(2)
	if waitMode {
		proxyRequests = 3
	}
	toolRoundTrip, interactive := mode != "sources", mode == "interactive-tool" || proxyMode || waitMode
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
	finalMarker, resultSuffix := "", ""
	if liveMode || guardedMode {
		finalMarker = rand.Text()
		resultSuffix = finalMarker[len(finalMarker)/2:]
		if phraseMode {
			finalMarker = "VERIFIED " + resultSuffix
		}
	}
	writeJSON(settings, map[string]any{})
	if denied {
		reason := clientDenialReason
		if resultSuffix != "" {
			reason += "; Y=" + resultSuffix
		}
		output, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason}})
		if err != nil {
			t.Fatal("cannot encode owned refusal")
		}
		writeJSON(settings, map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": ownedPluginToolName, "hooks": []any{map[string]any{"type": "command", "command": "printf '%s' " + probeShellQuote(string(output)), "timeout": 2}}}}}})
	}
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	lifetime := 2 * time.Minute
	if liveMode || guardedMode {
		lifetime = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), lifetime)
	defer cancel()
	var exchange *pluginToolExchange
	if toolRoundTrip {
		workerDir := filepath.Join(root, "worker")
		if os.Mkdir(workerDir, 0700) != nil {
			t.Fatal("cannot create owned schema worker directory")
		}
		proxy := filepath.Join(filepath.Dir(buildRelayObserver(t)), "owned-relay")
		validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: workerDir})
		if err != nil {
			t.Fatal(err)
		}
		defer validator.Close()
		exchange = &pluginToolExchange{validator: validator}
		exchange.duplicateMessageIDs = mode == "wait-tool-duplicate"
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	model := "claude-dax-plugin-fixture"
	var proxyHandler http.Handler
	var proxyBackend *defaultClientBackend
	var sequence *pluginSequenceGuard
	var finishLive func()
	processLedger := filepath.Join(root, "proxy-processes")
	if proxyMode {
		backendDir := filepath.Join(root, "backend")
		if os.Mkdir(backendDir, 0700) != nil {
			t.Fatal("cannot create owned plugin backend directory")
		}
		var driver *session.Driver
		var models *catalog.Catalog
		var beforeUse func(int) error
		backendModel, turnLimit, firstLimit := "fixture-backend", 15*time.Second, 10*time.Second
		if guardedMode {
			turnLimit, firstLimit = 45*time.Second, 20*time.Second
		}
		if liveMode {
			driver, models, beforeUse, finishLive = prepareLivePluginDriver(t, ctx, runner, root, backendDir, exchange.validator)
			defer finishLive()
			backendModel, turnLimit, firstLimit = "auto", 45*time.Second, 20*time.Second
		} else {
			fake := buildDenialACPFixture(t, ctx, runner, root)
			proxy := filepath.Join(filepath.Dir(buildRelayObserver(t)), "owned-relay")
			args := []string{"chat-tools-plugin-client", ownedPluginToolName, processLedger}
			if waitMode {
				args[0] = "chat-tools-plugin-wait"
			}
			if guardedMode {
				args[0] = "chat-tools-plugin-wait-marker"
				markerPath := filepath.Join(backendDir, "owned-final-marker")
				if os.WriteFile(markerPath, []byte(finalMarker), 0600) != nil {
					t.Fatal("cannot write owned fake response marker")
				}
				args = append(args, markerPath)
			}
			if denied {
				args = append(args, "denied")
			}
			driver, err = session.New(session.Config{Process: acp.Config{Executable: fake, Args: args, Directory: backendDir, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, ClientInfo: acp.Info{Name: "independent-plugin-client", Version: "1"}}, Validator: exchange.validator, RelayExecutable: proxy, SetupTimeout: 10 * time.Second, TurnTimeout: turnLimit})
			if err != nil {
				t.Fatal(err)
			}
			defer driver.Close()
			models, err = catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent plugin fixture"}}, "fixture-backend")
			if err != nil {
				t.Fatal(err)
			}
		}
		model, err = models.ClientID(backendModel)
		if err != nil {
			t.Fatal(err)
		}
		proxyBackend = &defaultClientBackend{Driver: driver, catalog: models, limit: proxyRequests}
		var guarded inference.Backend = proxyBackend
		if waitMode {
			sequence = &pluginSequenceGuard{Backend: proxyBackend, denied: denied, beforeUse: beforeUse, finalMarker: finalMarker, resultSuffix: resultSuffix}
			guarded = sequence
			defer func() {
				stats := sequence.snapshot()
				t.Logf("guarded_plugin_sequence=%+v", stats)
				if stats.Failed || stats.Requests != 3 || stats.Uses != 2 || stats.Results != 2 || stats.Completions != 1 || stats.TextBytes == 0 || (finalMarker != "" && !stats.FinalMarker) {
					t.Error("bounded plugin sequence did not complete")
				}
			}()
		}
		proxyHandler, err = gateway.New(gateway.Config{Tokens: tokens, Backend: guarded, TurnTimeout: turnLimit, FirstEventTimeout: firstLimit})
		if err != nil {
			t.Fatal(err)
		}
	}
	var messageRequests, advertisedPlugin atomic.Int32
	var exchangeEnabled atomic.Bool
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
			if messageRequests.Add(1) > 4 {
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
			if proxyHandler != nil {
				decoded, err := anthropic.DecodeRequest(body)
				if err != nil {
					w.WriteHeader(400)
					return
				}
				if requestfamily.Classify(decoded) == requestfamily.Title {
					writeObservedMessage(w, decoded.Stream, model, []map[string]any{{"type": "text", "text": `{"title":"Independent plugin fixture"}`}}, "end_turn")
					return
				}
				if waitMode {
					for _, block := range decoded.Messages[decoded.LatestUserIndex()].Content {
						if block.Type == "tool_result" {
							var value struct{ Content json.RawMessage }
							if json.Unmarshal(block.Raw, &value) == nil {
								t.Logf("wait_path_result_string=%v", len(value.Content) > 0 && value.Content[0] == '"')
							}
						}
					}
					exchange.mu.Lock()
					if !exchange.waited {
						var waitSchema json.RawMessage
						for _, raw := range decoded.Tools {
							var tool struct {
								Name   string
								Schema json.RawMessage `json:"input_schema"`
							}
							if json.Unmarshal(raw, &tool) == nil && tool.Name == "WaitForMcpServers" {
								waitSchema = tool.Schema
							}
						}
						exchange.waitAdvertised = waitSchema != nil
						exchange.waitEmptyAccepted = waitSchema != nil && exchange.validator.Validate(r.Context(), waitSchema, []byte(`{}`)) == nil
						if !exchange.waitEmptyAccepted || exchange.releaseWait == nil {
							exchange.failed = true
							exchange.mu.Unlock()
							w.WriteHeader(400)
							return
						}
						if err := exchange.releaseWait(); err != nil {
							exchange.failed = true
							exchange.mu.Unlock()
							w.WriteHeader(500)
							return
						}
						exchange.waited = true
					}
					exchange.mu.Unlock()
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				if resultSuffix != "" && proxyBackend.starts.Load() == 0 && bytes.Contains(body, []byte(resultSuffix)) {
					w.WriteHeader(400)
					return
				}
				proxyHandler.ServeHTTP(w, r)
				proxyBackend.mu.Lock()
				comparison := proxyBackend.comparison
				proxyBackend.mu.Unlock()
				exchange.mu.Lock()
				exchange.stage, exchange.comparison = "real_gateway", comparison
				exchange.toolCount = len(input.Tools)
				for _, tool := range input.Tools {
					if strings.HasPrefix(tool.Name, "mcp__") {
						exchange.mcpCount++
					}
					exchange.rawWait = exchange.rawWait || tool.Name == "WaitForMcpServers"
					exchange.rawToolSearch = exchange.rawToolSearch || tool.Name == "ToolSearch"
				}
				exchange.toolRequested = proxyBackend.starts.Load() == proxyRequests
				exchange.failed = proxyBackend.failed.Load()
				if sequence != nil {
					exchange.failed = exchange.failed || sequence.snapshot().Failed
				}
				exchange.complete = exchange.toolRequested && !exchange.failed && proxyBackend.State() == session.Idle
				exchange.mu.Unlock()
				return
			}
			if exchange != nil && exchangeEnabled.Load() {
				exchange.respond(w, r, body, model)
				return
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
			if exchange != nil {
				exchange.verify(t)
			}
			t.Fatalf("owned plugin command failed: exit=%d, stdout_bytes=%d", result.ExitCode, len(result.Stdout))
		}
		return result
	}
	if version := run(t, natural, "--version"); !launcher.CompatibleClientOutput(version.Stdout) {
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
	peerArgs := []string{"plugin", observations}
	if waitMode {
		peerArgs = append(peerArgs, "hold-initialize")
		if resultSuffix != "" {
			peerArgs = append(peerArgs, resultSuffix)
		}
		exchange.releaseWait = func() error {
			f, err := os.OpenFile(filepath.Join(observations, "release-plugin"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			return f.Close()
		}
	}
	writeJSON(filepath.Join(market, "owned-plugin", ".mcp.json"), map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"command": peer, "args": peerArgs}}})
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
	cases := []struct {
		name     string
		expected int
	}{
		{"natural", 1},
		{"prepared", 1},
		{"seed", 2},
		{"seed_print", 3},
		{"seed_print_disabled", 3},
		{"seed_print_reenabled", 4},
	}
	if toolRoundTrip {
		cases = cases[:1]
		cases[0].name = "seed_print_tool"
		cases[0].expected = 2
		if interactive {
			cases[0].expected = 1
		}
	}
	for _, tc := range cases {
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
					// Keep the original profile counterfactual without a seed or native registrations.
					if os.RemoveAll(filepath.Join(fresh.Path(), "client", "plugins")) != nil {
						t.Fatal("cannot prepare owned profile counterfactual")
					}
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
				prompt := "Return a short text response without calling any tool."
				if toolRoundTrip && !interactive {
					// A local synthetic warmup distinguishes first seed bootstrap from a second
					// launch of the same private profile. This is an observation, not launcher policy.
					wantInitializations.Store(1)
					wantToolListings.Store(1)
					warm := run(t, command, "--print", "--output-format", "json", "--no-session-persistence", "Return a short text response without calling any tool.")
					started, initialized, calls := inspectClientAssetProcesses(t, filepath.Join(observations, "plugin"))
					if !bytes.Contains(warm.Stdout, []byte("independent plugin observation complete")) || started != 1 || initialized != 1 || calls != 0 {
						t.Fatal("owned profile warmup did not complete without tools")
					}
					t.Log("same_private_profile_warmed=true")
					exchangeEnabled.Store(true)
				}
				if toolRoundTrip {
					exchangeEnabled.Store(true)
					command.Args = append(command.Args, "--allowedTools", ownedPluginToolName)
					prompt = "Call the independent plugin's effect-free probe and return its result."
				}
				if waitMode {
					waitPrompt := "Wait for the independent plugin, call its effect-free probe, and return its result."
					answer := "independent plugin observation complete"
					uiLifetime := time.Duration(0)
					if liveMode || guardedMode {
						middle := len(finalMarker) / 2
						waitPrompt = "WaitForMcpServers {}, then " + ownedPluginToolName + " {} once each. After both, reply X+Y; X=" + finalMarker[:middle] + ", Y is in the result."
						if phraseMode {
							waitPrompt = "WaitForMcpServers {}, then " + ownedPluginToolName + " {} once each. Reply exactly VERIFIED Y, using Y from the result."
						}
						answer = finalMarker
						if len(waitPrompt) > 150 || strings.Contains(waitPrompt, resultSuffix) {
							t.Fatal("derived marker prompt is ambiguous or exceeds one terminal row")
						}
						uiLifetime = time.Minute
					}
					result = runAssetInteractive(t, ctx, command, root, project, assetUIControl{
						Prompt: waitPrompt, Lifetime: uiLifetime,
						Answer: answer, Requests: messageRequests.Load,
						Ready: func() bool {
							ledger, err := readDenialArtifact(observations, "plugin", 8192)
							return err == nil && bytes.Contains(ledger, []byte(" held\n"))
						},
						Complete: func() bool {
							exchange.mu.Lock()
							defer exchange.mu.Unlock()
							return exchange.waited && exchange.complete && !exchange.failed
						},
					})
				} else if interactive {
					result = runPluginInteractive(t, ctx, command, root, project, observations, exchange, &messageRequests)
				} else {
					result = run(t, command, "--print", "--output-format", "json", "--no-session-persistence", prompt)
				}
				t.Logf("synthetic_messages=%d, plugin_advertisements=%d", messageRequests.Load(), advertisedPlugin.Load())
				if (!toolRoundTrip && messageRequests.Load() != beforeRequests+1) || (!interactive && !bytes.Contains(result.Stdout, []byte("independent plugin observation complete"))) {
					t.Error("synthetic plugin conversation did not complete")
				}
			} else {
				result = run(t, command, "mcp", "list")
			}
			started, initialized, called := inspectClientAssetProcesses(t, filepath.Join(observations, "plugin"))
			listsAfter := bytes.Count(boundedAssetFile(t, filepath.Join(observations, "plugin")), []byte(" listed\n"))
			t.Logf("plugin_named=%v, starts=%d, initializations=%d, calls=%d, tool_lists=%d", bytes.Contains(result.Stdout, []byte("plugin:dax-owned:owned")), started, initialized, called, listsAfter)
			wantCalls := 0
			if toolRoundTrip {
				wantCalls = 1
				if denied {
					wantCalls = 0
				}
				exchange.verify(t)
			}
			if started != tc.expected || initialized != started || called != wantCalls {
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
	if proxyBackend != nil {
		if liveMode {
			finishLive()
		} else {
			if err := proxyBackend.Close(); err != nil {
				t.Error("plugin backend cleanup failed")
			}
			pids := strings.Fields(string(boundedAssetFile(t, processLedger)))
			// A plain tool round trip resolves into the pending prompt and defers the rotated standing
			// instruction (D123); the wait flows still recreate through results followed by text (D73).
			wantProcesses := 1
			if waitMode {
				wantProcesses = 2
			}
			if len(pids) != wantProcesses {
				t.Error("unexpected bounded backend reconstruction count")
			}
			for _, raw := range pids {
				pid, err := strconv.Atoi(raw)
				if err != nil || pid <= 1 || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
					t.Error("plugin backend process group survived cleanup")
				}
			}
			t.Logf("proxy_main_requests=%d, proxy_failed=%v, backend_processes=%d", proxyBackend.starts.Load(), proxyBackend.failed.Load(), len(pids))
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
