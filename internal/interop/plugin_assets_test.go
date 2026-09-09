package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

const pluginSkillBody = "Independent skill invocation marker for client asset preservation."
const pluginStartContext = "Independent plugin startup context marker."

// The public client installs an independently authored local plugin, executes its hooks and
// expands its skill. All model responses come from this bounded loopback fixture. Neither
// installed user assets nor an external model are involved.
func TestClaudePluginHooksAndSkillSources(t *testing.T) {
	observePluginAssets(t, false)
}

func TestClaudeGitPluginSourcePreservation(t *testing.T) {
	observePluginAssets(t, true)
}

// Passing this control reproduces rejected source writes. The product profile must continue
// passing the ordinary preservation tests, which do not permit these writes.
func TestClaudeRedirectedProfilePluginCounterfactual(t *testing.T) {
	observePluginAssetProfile(t, false, pluginAssetsOnly, true)
}

func observePluginAssets(t *testing.T, gitSource bool) {
	observePluginAssetMode(t, gitSource, pluginAssetsOnly)
}

func observePluginAssetMode(t *testing.T, gitSource bool, mode pluginAssetMode) {
	observePluginAssetProfile(t, gitSource, mode, false)
}

func observePluginAssetProfile(t *testing.T, gitSource bool, mode pluginAssetMode, redirectProfile bool) {
	t.Helper()
	modelSelected, gatewayMode := mode != pluginAssetsOnly, mode == pluginGatewaySkill
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for owned plugin hook/skill controls")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-plugin-assets-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	market, observations := filepath.Join(root, "market"), filepath.Join(root, "observations")
	for _, path := range []string{home, project, observations, filepath.Join(home, ".claude")} {
		if os.MkdirAll(path, 0700) != nil {
			t.Fatal("cannot prepare owned plugin directories")
		}
	}
	write := func(path string, data []byte) {
		t.Helper()
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot write owned plugin fixture")
		}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal("cannot encode owned fixture")
		}
		write(path, data)
	}
	settings, global := filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json")
	write(settings, []byte(`{}`))
	write(global, []byte(`{}`))
	tokens, _ := gateway.NewTokens()
	model := "claude-dax-plugin-assets"
	const answer = "independent plugin assets complete"
	var proxyBackend *defaultClientBackend
	var proxyHandler http.Handler
	var validator *schemacheck.Pool
	if modelSelected {
		workerDir := filepath.Join(root, "worker")
		if os.Mkdir(workerDir, 0700) != nil {
			t.Fatal("cannot create owned skill schema worker")
		}
		proxy := filepath.Join(filepath.Dir(buildRelayObserver(t)), "owned-relay")
		validator, err = schemacheck.New(schemacheck.Config{Executable: proxy, Directory: workerDir})
		if err != nil {
			t.Fatal(err)
		}
		defer validator.Close()
		if gatewayMode {
			var finish func()
			proxyBackend, proxyHandler, model, finish = prepareSkillGateway(t, root, proxy, tokens, validator)
			defer finish()
		}
	}
	type observation struct {
		requests                                                int
		decoded, skillListed, skillBody, hookContext, skillTool bool
		skillCall, resultMatch, latestResult                    bool
		results                                                 int
		comparison                                              defaultClientComparison
		shape                                                   string
		expandedText, proxyComplete                             bool
	}
	var mu sync.Mutex
	var seen observation
	var total int
	var first *anthropic.Request
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
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Independent plugin assets"}}})
		case "/v1/messages/count_tokens":
			_, _ = w.Write([]byte(`{"input_tokens":1}`))
		case "/v1/messages":
			mu.Lock()
			defer mu.Unlock()
			total++
			if total > 16 {
				w.WriteHeader(429)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			if err != nil || len(body) > 1<<20 {
				w.WriteHeader(400)
				return
			}
			request, err := anthropic.DecodeRequest(body)
			if err == nil && requestfamily.Classify(request) == requestfamily.Title {
				writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "text", "text": `{"title":"Independent assets"}`}}, "end_turn")
				return
			}
			seen.requests++
			seen.decoded = err == nil
			seen.skillListed = bytes.Contains(body, []byte("dax-assets:owned-skill"))
			seen.skillBody = bytes.Contains(body, []byte(pluginSkillBody))
			seen.hookContext = bytes.Contains(body, []byte(pluginStartContext))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			var skillSchema json.RawMessage
			for _, raw := range request.Tools {
				var tool struct {
					Name   string
					Schema json.RawMessage `json:"input_schema"`
				}
				if json.Unmarshal(raw, &tool) == nil && tool.Name == "Skill" {
					seen.skillTool = true
					skillSchema = tool.Schema
				}
			}
			if modelSelected {
				if len(request.Messages) > 32 {
					w.WriteHeader(400)
					return
				}
				request.Identity = anthropic.ClientIdentity{Session: r.Header.Get("x-claude-code-session-id"), Agent: r.Header.Get("x-claude-code-agent-id"), ParentAgent: r.Header.Get("x-claude-code-parent-agent-id")}
				if seen.requests == 1 {
					input := json.RawMessage(`{"skill":"dax-assets:owned-skill"}`)
					if seen.skillBody || !seen.skillListed || skillSchema == nil || validator.Validate(r.Context(), skillSchema, input) != nil {
						w.WriteHeader(400)
						return
					}
					first, seen.skillCall = request, true
					if !gatewayMode {
						writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "tool_use", "id": ownedSkillUseID, "name": "Skill", "input": input}}, "tool_use")
						return
					}
				}
				if seen.requests != 1 && seen.requests != 2 || first == nil {
					w.WriteHeader(429)
					return
				}
				if seen.requests == 2 {
					expectedID := ownedSkillUseID
					if gatewayMode {
						expectedID = observedSkillID(request)
					}
					seen.results, seen.resultMatch = inspectSkillReturn(request, expectedID)
					seen.comparison = compareDefaultRequests(first, request)
					results, err := request.LatestToolResults()
					seen.latestResult = err == nil && len(results) == 1 && results[0].ID == expectedID
					if i := request.LatestUserIndex(); i >= 0 {
						for _, block := range request.Messages[i].Content {
							if block.Type == "text" && strings.Contains(block.Text, pluginSkillBody) {
								seen.expandedText = true
							}
						}
					}
					var shape []string
					for _, message := range request.Messages {
						if len(message.Content) > 32 {
							w.WriteHeader(400)
							return
						}
						var kinds []string
						for _, block := range message.Content {
							kinds = append(kinds, block.Type)
						}
						shape = append(shape, message.Role+"["+strings.Join(kinds, ",")+"]")
					}
					seen.shape = strings.Join(shape, " ")
					if !seen.resultMatch {
						w.WriteHeader(400)
						return
					}
				}
				if gatewayMode {
					r.Body = io.NopCloser(bytes.NewReader(body))
					proxyHandler.ServeHTTP(w, r)
					seen.proxyComplete = proxyBackend.starts.Load() == 2 && !proxyBackend.failed.Load() && proxyBackend.State() == session.Idle
					return
				}
			}
			writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "text", "text": answer}}, "end_turn")
		default:
			w.WriteHeader(404)
		}
	}))
	server.Config.ReadHeaderTimeout, server.Config.ReadTimeout, server.Config.WriteTimeout, server.Config.IdleTimeout = time.Second, 2*time.Second, 2*time.Second, 2*time.Second
	server.Config.MaxHeaderBytes = 8 << 10
	if gatewayMode {
		server.Config.WriteTimeout = 20 * time.Second
	}
	server.Start()
	defer server.Close()
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cfg := launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}}
	initial, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer initial.Close()
	natural := initial.Command()
	natural.Args = nil
	natural.Environment = nil
	for _, entry := range initial.Command().Environment {
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
	const pluginID = "dax-assets@dax-assets-market"
	writeJSON(filepath.Join(market, ".claude-plugin", "marketplace.json"), map[string]any{"name": "dax-assets-market", "owner": map[string]string{"name": "Independent fixture"}, "plugins": []any{map[string]string{"name": "dax-assets", "source": "./owned-plugin"}}})
	plugin := filepath.Join(market, "owned-plugin")
	writeJSON(filepath.Join(plugin, ".claude-plugin", "plugin.json"), map[string]string{"name": "dax-assets", "version": "1.0.0", "description": "Independent client hook and skill preservation fixture."})
	write(filepath.Join(plugin, "skills", "owned-skill", "SKILL.md"), []byte("---\nname: owned-skill\ndescription: Independent asset control with no tool effects.\n---\n"+pluginSkillBody+"\nReturn a brief text answer without executing tools.\n"))
	hookScript := "#!/bin/sh\nset -euC\numask 077\ncase \"$1\" in start|stop) ;; *) exit 70;; esac\nprintf '%s\\n' \"$$\" > \"$2/$1.$$\"\nif [ \"$1\" = start ]; then\n printf '%s\\n' '{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"" + pluginStartContext + "\"}}'\nelse\n printf '{}\\n'\nfi\n"
	write(filepath.Join(plugin, "scripts", "observe.sh"), []byte(hookScript))
	hooks := map[string]any{}
	for event, label := range map[string]string{"SessionStart": "start", "Stop": "stop"} {
		hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/bin/sh \"${CLAUDE_PLUGIN_ROOT}/scripts/observe.sh\" " + label + " " + probeShellQuote(observations), "timeout": 2}}}}
	}
	writeJSON(filepath.Join(plugin, "hooks", "hooks.json"), map[string]any{"hooks": hooks})
	git := func(args ...string) {
		t.Helper()
		config := []string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Independent Fixture", "-c", "user.email=fixture@example.invalid"}
		_, err := runner.Run(ctx, childproc.Command{Executable: "/usr/bin/git", Directory: market, Args: append(config, args...), Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}})
		if err != nil {
			t.Fatal("independent local Git fixture command failed")
		}
	}
	marketSource := market
	if gitSource {
		git("init", "--initial-branch=fixture")
		git("add", ".")
		git("commit", "-m", "Independent plugin fixture")
		// The client rejects file: marketplace URLs. A fixture-only Git URL rewrite lets its
		// ordinary HTTPS source path clone this owned local repository without a network host.
		marketSource = "https://independent.invalid/owned-market.git"
		write(filepath.Join(home, ".gitconfig"), []byte("[url \"file://"+market+"/.git\"]\n\tinsteadOf = "+marketSource+"\n"))
		resolved, err := runner.Run(ctx, childproc.Command{Executable: "/usr/bin/git", Directory: market, Args: []string{"ls-remote", marketSource, "HEAD"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}})
		if err != nil || len(strings.Fields(string(resolved.Stdout))) != 2 || !strings.HasSuffix(strings.TrimSpace(string(resolved.Stdout)), "HEAD") {
			t.Fatal("owned Git URL rewrite did not resolve locally")
		}
		t.Log("owned_git_url_rewrite_resolves=true")
	}
	run(t, natural, "plugin", "marketplace", "add", marketSource)
	run(t, natural, "plugin", "install", pluginID, "--scope", "user")
	seed := filepath.Join(home, ".claude", "plugins")
	if gitSource {
		write(filepath.Join(plugin, "pending-revision.txt"), []byte("second independent fixture revision"))
		git("add", ".")
		git("commit", "-m", "Independent pending update")
	}
	cases := []struct {
		name, management             string
		skill, enabled, disableHooks bool
	}{
		{"natural_normal", "", false, true, false},
		{"enabled_normal", "", false, true, false},
		{"enabled_skill", "", true, true, false},
		{"interactive_skill", "", true, true, false},
		{"disabled", "disable", false, false, false},
		{"reenabled_skill", "enable", true, true, false},
		{"hooks_disabled_skill", "", true, true, true},
		{"hooks_reenabled_skill", "", true, true, false},
		{"private_uninstall_skill", "", true, true, false},
		{"private_market_update_skill", "", true, true, false},
		{"private_market_remove_skill", "", true, true, false},
	}
	if gitSource {
		cases = cases[len(cases)-3:]
	}
	if modelSelected {
		cases = cases[:3]
		cases[0].name, cases[1].name, cases[2].name = "natural_model_skill", "prepared_model_skill", "hooks_disabled_model_skill"
		for i := range cases {
			cases[i].skill = false
		}
		cases[2].disableHooks = true
	}
	if gatewayMode {
		cases = cases[1:2]
	}
	if redirectProfile {
		cases = append(cases[:1:1], cases[2], cases[3], cases[8])
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.management != "" {
				run(t, natural, "plugin", tc.management, pluginID, "--scope", "user")
			}
			var source map[string]any
			if json.Unmarshal(boundedAssetFile(t, settings), &source) != nil {
				t.Fatal("invalid owned plugin settings")
			}
			source["disableAllHooks"] = tc.disableHooks
			writeJSON(settings, source)
			beforeSeed, beforeMarket := boundedPluginTree(t, seed), boundedPluginTree(t, market)
			beforeSettings, beforeGlobal := fileFingerprint(t, settings), fileFingerprint(t, global)
			var sourceSettings map[string]any
			if redirectProfile && json.Unmarshal(boundedAssetFile(t, settings), &sourceSettings) != nil {
				t.Fatal("invalid owned settings diagnostic source")
			}
			beforeGit := fileFingerprint(t, filepath.Join(home, ".gitconfig"))
			oldStarts, oldStops := inspectPluginHookProcesses(t, observations)
			mu.Lock()
			seen = observation{}
			first = nil
			beforeRequests := total
			mu.Unlock()
			profile, err := launcher.PrepareClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer profile.Close()
			if redirectProfile && !strings.HasPrefix(tc.name, "natural_") {
				redirectOwnedClientConfig(t, profile, home, true)
			}
			prompt := "Return a short text response without calling tools."
			if tc.skill {
				prompt = "/dax-assets:owned-skill"
			}
			if modelSelected {
				prompt = "Use the independently installed skill and return a brief text response."
			}
			command := profile.Command()
			if strings.HasPrefix(tc.name, "natural_") {
				command = natural
			}
			var result childproc.Result
			interactive := tc.name == "interactive_skill"
			if interactive {
				uiRoot := filepath.Join(root, tc.name)
				if os.Mkdir(uiRoot, 0700) != nil {
					t.Fatal("cannot create owned asset UI directory")
				}
				result = runAssetInteractive(t, ctx, command, uiRoot, project, assetUIControl{
					Prompt: prompt, Answer: answer,
					Requests: func() int32 { mu.Lock(); defer mu.Unlock(); return int32(total - beforeRequests) },
					Ready:    func() bool { return countPluginHookFiles(observations, "start.") > oldStarts },
					Complete: func() bool {
						mu.Lock()
						defer mu.Unlock()
						return seen.requests == 1 && countPluginHookFiles(observations, "stop.") > oldStops
					},
				})
			} else {
				result = run(t, command, "--print", "--output-format", "json", "--no-session-persistence", prompt)
			}
			starts, stops := inspectPluginHookProcesses(t, observations)
			mu.Lock()
			got := seen
			mu.Unlock()
			wantHooks := 0
			if tc.enabled && !tc.disableHooks {
				wantHooks = 1
			}
			completed := bytes.Contains(result.Stdout, []byte(answer))
			if interactive {
				completed = !t.Failed()
			} // The shared UI observer asserts rendered text.
			t.Logf("requests=%d, decoded=%v, skill_listed=%v, skill_body=%v, hook_context=%v, skill_tool=%v, startup_hooks=%d, stop_hooks=%d, completed=%v", got.requests, got.decoded, got.skillListed, got.skillBody, got.hookContext, got.skillTool, starts-oldStarts, stops-oldStops, completed)
			wantRequests := 1
			if modelSelected {
				t.Logf("skill_called=%v, results=%d, matching_result=%v, latest_result=%v, client_skill_text=%v, proxy_complete=%v, comparison=%+v, history_shape=%s", got.skillCall, got.results, got.resultMatch, got.latestResult, got.expandedText, got.proxyComplete, got.comparison, got.shape)
				wantRequests = 2
				if !got.skillCall || !got.resultMatch || !got.expandedText || got.results != 1 || (gatewayMode && !got.proxyComplete) {
					t.Error("model-selected skill round trip not established")
				}
			}
			if starts-oldStarts != wantHooks || stops-oldStops != wantHooks || !completed || got.requests != wantRequests || !got.decoded || got.skillListed != tc.enabled || got.skillBody != (tc.skill || modelSelected) || got.hookContext != (wantHooks == 1) {
				t.Error("plugin hook or skill activation did not match source controls")
			}
			if strings.HasPrefix(tc.name, "private_") {
				mutation := profile.Command()
				args := []string{"plugin", "uninstall", pluginID, "--scope", "user"}
				if tc.name == "private_market_update_skill" {
					args = []string{"plugin", "marketplace", "update", "dax-assets-market"}
				}
				if tc.name == "private_market_remove_skill" {
					args = []string{"plugin", "marketplace", "remove", "dax-assets-market"}
				}
				mutation.Args = append(mutation.Args, args...)
				// Capture this owned command's diagnostics only in bounded memory, then retain
				// fixed classifications. No raw stderr is persisted or printed by the test.
				mutation.Args = append([]string{"-c", `exec "$@" 2>&1`, "owned-plugin-mutation", mutation.Executable}, mutation.Args...)
				mutation.Executable = "/bin/sh"
				result, err := runner.Run(ctx, mutation)
				seedRefusal := result.ExitCode == 1 && bytes.Contains(bytes.ToLower(result.Stdout), []byte("seed"))
				t.Logf("private_plugin_mutation_exit=%d, finite_exit_error=%v, seed_mutation_refused=%v", result.ExitCode, errors.Is(err, childproc.ErrExit), seedRefusal)
				if err != nil && !errors.Is(err, childproc.ErrExit) {
					t.Error("owned plugin mutation control failed to finish")
				}
				if gitSource && tc.name != "private_uninstall_skill" && !seedRefusal {
					t.Error("Git seed mutation did not explicitly refuse")
				}
				if tc.name == "private_uninstall_skill" && result.ExitCode != 0 {
					t.Error("private plugin uninstall failed")
				}
			}
			seedChanged := !reflect.DeepEqual(beforeSeed, boundedPluginTree(t, seed))
			marketChanged := !reflect.DeepEqual(beforeMarket, boundedPluginTree(t, market))
			settingsChanged, globalChanged := beforeSettings != fileFingerprint(t, settings), beforeGlobal != fileFingerprint(t, global)
			gitChanged := beforeGit != fileFingerprint(t, filepath.Join(home, ".gitconfig"))
			if redirectProfile {
				t.Logf("owned_seed_changed=%v, owned_market_changed=%v, owned_settings_changed=%v, owned_global_changed=%v, owned_git_changed=%v", seedChanged, marketChanged, settingsChanged, globalChanged, gitChanged)
				var afterSettings map[string]any
				if json.Unmarshal(boundedAssetFile(t, settings), &afterSettings) != nil {
					t.Fatal("invalid owned settings diagnostic result")
				}
				changed := []string{}
				for _, key := range []string{"$schema", "effortLevel", "enabledPlugins", "permissions", "autoMemoryEnabled", "model", "voiceEnabled"} {
					if !reflect.DeepEqual(sourceSettings[key], afterSettings[key]) {
						changed = append(changed, key)
					}
					delete(sourceSettings, key)
					delete(afterSettings, key)
				}
				t.Logf("owned_settings_changed_known_keys=%v, other_settings_fields_changed=%v", changed, !reflect.DeepEqual(sourceSettings, afterSettings))
			}
			if redirectProfile && (interactive || tc.name == "private_uninstall_skill") {
				if !settingsChanged || seedChanged != (tc.name == "private_uninstall_skill") || marketChanged || globalChanged || gitChanged {
					t.Error("rejected profile no longer reproduces its source-write defect")
				}
				if tc.name == "private_uninstall_skill" {
					var installed struct {
						Plugins map[string]json.RawMessage `json:"plugins"`
					}
					if json.Unmarshal(boundedAssetFile(t, filepath.Join(seed, "installed_plugins.json")), &installed) != nil || installed.Plugins == nil {
						t.Fatal("invalid owned plugin registration result")
					}
					if _, exists := installed.Plugins[pluginID]; exists {
						t.Error("rejected profile did not remove the original owned plugin registration")
					}
				}
			} else if !strings.HasPrefix(tc.name, "natural_") && (seedChanged || marketChanged || settingsChanged || globalChanged || gitChanged) {
				t.Error("prepared plugin startup changed original sources")
			}
			if profile.Close() != nil {
				t.Error("plugin asset profile cleanup failed")
			}
		})
		if t.Failed() {
			return
		}
	}
	if gitSource {
		// The explicit native control runs after prepared preservation assertions. It proves
		// an update was available, and only changes this independently authored local fixture.
		run(t, natural, "plugin", "marketplace", "update", "dax-assets-market")
		updated := filepath.Join(seed, "marketplaces", "dax-assets-market", "owned-plugin", "pending-revision.txt")
		if data := boundedAssetFile(t, updated); string(data) != "second independent fixture revision" {
			t.Error("native update did not apply the pending owned revision")
		}
		t.Log("native_marketplace_applied_pending_revision=true")
	}
}

func countPluginHookFiles(directory, prefix string) int {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) > 32 {
		return -1
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			count++
		}
	}
	return count
}

func inspectPluginHookProcesses(t *testing.T, directory string) (starts, stops int) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) > 32 {
		t.Fatal("invalid owned hook observation directory")
	}
	for _, entry := range entries {
		label, number, ok := strings.Cut(entry.Name(), ".")
		pid, err := strconv.Atoi(number)
		if !ok || err != nil || pid <= 1 || (label != "start" && label != "stop") {
			t.Fatal("invalid owned hook process record")
		}
		if string(bytes.TrimSpace(boundedAssetFile(t, filepath.Join(directory, entry.Name())))) != number || !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			t.Fatal("owned hook process survived or record mismatched")
		}
		if label == "start" {
			starts++
		} else {
			stops++
		}
	}
	return
}
