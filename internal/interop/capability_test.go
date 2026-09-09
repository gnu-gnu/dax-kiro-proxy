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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

// These are negative interoperability observations, not proof of automatic capability recovery.
// The positive control uses the same profile and response fixture without an initial rejection.
func TestClaudeThinkingRejectionObservation(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for local capability-rejection observations; no model credits")
	}
	for _, tc := range []struct{ name, message, retries string }{
		{"positive-control", "", "0"},
		{"generic-no-retries", "This model does not support thinking.", "0"},
		{"generic-two-retries", "This model does not support thinking.", "2"},
		{"extra-input", "thinking: Extra inputs are not permitted", "0"},
		{"type-enum", "thinking.type: Input should be 'enabled' or 'disabled'", "0"},
		{"synthetic-token", "capability_rejected: thinking", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observeThinkingRejection(t, executable, tc.message, tc.retries, capabilityProbeOptions{})
		})
	}
}

type capabilityProbeOptions struct{ omitThinking, omitBetas bool }

// These documented knobs are observation inputs, not product defaults. Title, tool, structured
// output and compaction behavior require separate evidence before any launch-policy adoption.
func TestClaudeDocumentedCompatibilityOptions(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for local compatibility-option observations; no model credits")
	}
	for _, tc := range []struct {
		name    string
		options capabilityProbeOptions
	}{{"default", capabilityProbeOptions{}}, {"omit-thinking", capabilityProbeOptions{omitThinking: true}}, {"omit-betas", capabilityProbeOptions{omitBetas: true}}, {"omit-both", capabilityProbeOptions{true, true}}} {
		t.Run(tc.name, func(t *testing.T) { observeThinkingRejection(t, executable, "", "0", tc.options) })
	}
}

// An unmodified installed client receives newly authored local responses in an empty owned HOME.
// Prompts, settings and responses are synthetic. No Kiro/model request or client tool is possible.
func observeThinkingRejection(t *testing.T, executable, errorMessage, retries string, options capabilityProbeOptions) {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "dax-capability-probe-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, path := range []string{home, project} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("cannot prepare private capability probe")
		}
	}
	runner, err := childproc.New(childproc.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	version, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}, Args: []string{"--version"}})
	if err != nil || strings.TrimSpace(string(version.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified client version")
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-dax-capability-observation"
	const answer = "independent capability recovery completed"
	var mu sync.Mutex
	var thinkingKinds []string
	var fieldSets [][]string
	var contextDeclarations, formatDeclarations []bool
	var betaHeaderCounts []int
	unknownFields := 0
	rejected, accepted := 0, 0
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
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Independent capability probe"}}})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, anthropic.MaxBodyBytes+1))
		var fields map[string]json.RawMessage
		if err != nil || len(body) > anthropic.MaxBodyBytes || json.Unmarshal(body, &fields) != nil {
			w.WriteHeader(400)
			return
		}
		var thinking struct {
			Type string `json:"type"`
		}
		kind := "absent"
		if value, ok := fields["thinking"]; ok {
			kind = "unrecognized"
			if json.Unmarshal(value, &thinking) == nil {
				switch thinking.Type {
				case "adaptive", "enabled", "disabled":
					kind = thinking.Type
				}
			}
		}
		names := make([]string, 0, len(fields))
		unknown := 0
		for key := range fields {
			switch key {
			case "model", "messages", "system", "stream", "max_tokens", "tools", "tool_choice", "thinking", "context_management", "metadata", "output_config", "cache_control", "temperature", "stop_sequences", "top_k", "top_p":
				names = append(names, key)
			default:
				unknown++
			}
		}
		sort.Strings(names)
		mu.Lock()
		if len(thinkingKinds) >= 4 {
			mu.Unlock()
			w.WriteHeader(400)
			return
		}
		thinkingKinds = append(thinkingKinds, kind)
		fieldSets = append(fieldSets, names)
		contextDeclarations = append(contextDeclarations, fields["context_management"] != nil)
		var outputConfig map[string]json.RawMessage
		_ = json.Unmarshal(fields["output_config"], &outputConfig)
		formatDeclarations = append(formatDeclarations, outputConfig["format"] != nil)
		betaCount := 0
		for _, line := range r.Header.Values("anthropic-beta") {
			for _, value := range strings.Split(line, ",") {
				if strings.TrimSpace(value) != "" {
					betaCount++
				}
			}
		}
		betaHeaderCounts = append(betaHeaderCounts, betaCount)
		unknownFields += unknown
		unsupported := errorMessage != "" && kind != "absent" && kind != "disabled"
		if unsupported {
			rejected++
		} else {
			accepted++
		}
		mu.Unlock()
		if unsupported {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]string{"type": "invalid_request_error", "message": errorMessage}})
			return
		}
		writeObservedMessage(w, string(fields["stream"]) == "true", model, []map[string]any{{"type": "text", "text": answer}}, "end_turn")
	}))
	defer server.Close()
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: filepath.Join(home, "settings.json"), Executable: executable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	command := profile.Command()
	for i, entry := range command.Environment {
		if strings.HasPrefix(entry, "CLAUDE_CODE_MAX_RETRIES=") {
			command.Environment[i] = "CLAUDE_CODE_MAX_RETRIES=" + retries
		}
	}
	overlay, err := os.ReadFile(profile.SettingsPath())
	var settings map[string]json.RawMessage
	var env map[string]string
	if err != nil || json.Unmarshal(overlay, &settings) != nil || json.Unmarshal(settings["env"], &env) != nil {
		t.Fatal("cannot read owned probe settings")
	}
	env["CLAUDE_CODE_MAX_RETRIES"] = retries
	for key, enabled := range map[string]bool{"CLAUDE_CODE_DISABLE_THINKING": options.omitThinking, "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": options.omitBetas} {
		if enabled {
			env[key] = "1"
			command.Environment = append(command.Environment, key+"=1")
		}
	}
	settings["env"], _ = json.Marshal(env)
	overlay, _ = json.Marshal(settings)
	if os.WriteFile(profile.SettingsPath(), overlay, 0600) != nil {
		t.Fatal("cannot update owned probe settings")
	}
	// These isolation flags belong only to this finite observation, not the product launcher.
	command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence", "--system-prompt", "Independent protocol observation.", "Return the local fixture answer.")
	result, runErr := runner.Run(t.Context(), command)
	mu.Lock()
	defer mu.Unlock()
	completed := runErr == nil && result.ExitCode == 0 && bytes.Contains(result.Stdout, []byte(answer)) && accepted == 1
	var completion struct {
		Result string
		Error  bool `json:"is_error"`
	}
	validJSON := json.Unmarshal(result.Stdout, &completion) == nil
	t.Logf("run_error=%v, cleanup_error=%v, io_error=%v, deadline_error=%v, exit_error=%v, output_limit=%v, raw_answer_present=%v, result_json=%v, result_matches=%v, client_error=%v", runErr != nil, errors.Is(runErr, childproc.ErrCleanup), errors.Is(runErr, childproc.ErrIO), errors.Is(runErr, context.DeadlineExceeded), errors.Is(runErr, childproc.ErrExit), errors.Is(runErr, childproc.ErrOutputLimit), bytes.Contains(result.Stdout, []byte(answer)), validJSON, validJSON && completion.Result == answer, completion.Error)
	t.Logf("version=%s, retries=%s, omit_thinking=%v, omit_betas=%v, completed=%v, recovered=%v, exit=%d, request_count=%d, rejected=%d, accepted=%d, thinking_kinds=%v, context_declarations=%v, format_declarations=%v, beta_header_counts=%v, fields=%v, unknown_fields=%d, stdout_bytes=%d", launcher.SupportedClientVersion, retries, options.omitThinking, options.omitBetas, completed, completed && rejected > 0, result.ExitCode, len(thinkingKinds), rejected, accepted, thinkingKinds, contextDeclarations, formatDeclarations, betaHeaderCounts, fieldSets, unknownFields, len(result.Stdout))
	expectedThinking := "adaptive"
	if options.omitThinking {
		expectedThinking = "absent"
	}
	if len(thinkingKinds) != 1 || thinkingKinds[0] != expectedThinking {
		t.Fatal("pinned client request shape changed; review this observation")
	}
	if options.omitBetas && (len(contextDeclarations) != 1 || contextDeclarations[0]) {
		t.Fatal("context-management declaration survived the documented omission option")
	}
	if errorMessage == "" {
		if !completed || rejected != 0 {
			t.Fatal("positive control did not complete the local response")
		}
	} else if !errors.Is(runErr, childproc.ErrExit) || result.ExitCode != 1 || rejected != 1 || accepted != 0 || bytes.Contains(result.Stdout, []byte(answer)) {
		t.Fatal("observed rejection behavior changed; automatic recovery remains unverified")
	}
}
