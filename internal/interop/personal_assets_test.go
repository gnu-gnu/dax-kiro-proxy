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
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

// Only independently authored assets in an owned HOME/project are loaded. The synthetic
// responder emits text only: it never dispatches a skill, agent, shell or filesystem tool.
func TestClaudePersonalCustomizationSources(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for independent personal asset controls; no external inference")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-personal-assets-")
	if err != nil {
		t.Fatal("cannot create owned asset root")
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	write := func(path, value string) {
		t.Helper()
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, []byte(value), 0600) != nil {
			t.Fatal("cannot create independent asset")
		}
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	write(settings, `{"disableAllHooks":true}`)
	write(filepath.Join(home, ".claude.json"), `{}`)
	for _, source := range []struct{ path, scope string }{{home, "personal"}, {project, "project"}} {
		base := filepath.Join(source.path, ".claude")
		name := "dax-" + source.scope
		write(filepath.Join(base, "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: Independent "+source.scope+" skill availability control.\n---\nDAX_"+source.scope+"_SKILL_BODY\nReturn text without using tools.\n")
		write(filepath.Join(base, "commands", name+"-command.md"), "---\ndescription: Independent "+source.scope+" command availability control.\n---\nDAX_"+source.scope+"_COMMAND_BODY\nReturn text without using tools.\n")
		write(filepath.Join(base, "agents", name+"-agent.md"), "---\nname: "+name+"-agent\ndescription: Independent "+source.scope+" agent availability control.\ntools: []\nmodel: inherit\n---\nReturn text without using tools.\n")
		write(filepath.Join(base, "skills", "dax-shared", "SKILL.md"), "---\nname: dax-shared\ndescription: Independent scope collision.\n---\nDAX_"+source.scope+"_SHARED_BODY\nReturn text without using tools.\n")
		write(filepath.Join(base, "agents", "dax-shared-agent.md"), "---\nname: dax-shared-agent\ndescription: Independent "+source.scope+" agent collision winner.\ntools: []\nmodel: inherit\n---\nReturn text without using tools.\n")
	}
	const model, answer = "claude-dax-personal-assets", "Independent personal assets complete."
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	type observation struct {
		Requests                                                                       int
		Decoded                                                                        bool
		PersonalSkill, ProjectSkill, PersonalAgent, ProjectAgent                       bool
		PersonalSkillBody, ProjectSkillBody, PersonalCommandBody, ProjectCommandBody   bool
		PersonalSharedBody, ProjectSharedBody, PersonalAgentWinner, ProjectAgentWinner bool
	}
	var mu sync.Mutex
	var got observation
	requests := 0
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
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Independent personal assets"}}})
		case "/v1/messages/count_tokens":
			_, _ = w.Write([]byte(`{"input_tokens":1}`))
		case "/v1/messages":
			mu.Lock()
			defer mu.Unlock()
			requests++
			got.Requests++
			if requests > 16 || got.Requests > 1 {
				w.WriteHeader(429)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			if err != nil || len(body) > 1<<20 {
				w.WriteHeader(400)
				return
			}
			request, err := anthropic.DecodeRequest(body)
			got.Decoded = err == nil
			if err != nil {
				w.WriteHeader(400)
				return
			}
			got.PersonalSkill = bytes.Contains(body, []byte("Independent personal skill availability control."))
			got.ProjectSkill = bytes.Contains(body, []byte("Independent project skill availability control."))
			got.PersonalAgent = bytes.Contains(body, []byte("Independent personal agent availability control."))
			got.ProjectAgent = bytes.Contains(body, []byte("Independent project agent availability control."))
			got.PersonalSkillBody = bytes.Contains(body, []byte("DAX_personal_SKILL_BODY"))
			got.ProjectSkillBody = bytes.Contains(body, []byte("DAX_project_SKILL_BODY"))
			got.PersonalCommandBody = bytes.Contains(body, []byte("DAX_personal_COMMAND_BODY"))
			got.ProjectCommandBody = bytes.Contains(body, []byte("DAX_project_COMMAND_BODY"))
			got.PersonalSharedBody = bytes.Contains(body, []byte("DAX_personal_SHARED_BODY"))
			got.ProjectSharedBody = bytes.Contains(body, []byte("DAX_project_SHARED_BODY"))
			got.PersonalAgentWinner = bytes.Contains(body, []byte("Independent personal agent collision winner."))
			got.ProjectAgentWinner = bytes.Contains(body, []byte("Independent project agent collision winner."))
			writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "text", "text": answer}}, "end_turn")
		default:
			w.WriteHeader(404)
		}
	}))
	server.Config.ReadHeaderTimeout, server.Config.ReadTimeout, server.Config.WriteTimeout = time.Second, 5*time.Second, 5*time.Second
	server.Config.MaxHeaderBytes = 16 << 10
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: 15 * time.Second, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	cfg := launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}}
	for _, mode := range []string{"natural", "stripped", "prepared"} {
		prompts := []string{"Return one brief text response without using tools.", "/dax-personal", "/dax-project", "/dax-personal-command", "/dax-project-command", "/dax-shared"}
		if mode == "stripped" {
			prompts = prompts[:1]
		}
		for _, prompt := range prompts {
			if !t.Run(mode+"/"+strings.TrimPrefix(strings.Split(prompt, " ")[0], "/"), func(t *testing.T) {
				profile, err := launcher.PrepareClient(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer profile.Close()
				if mode == "stripped" {
					for _, name := range []string{"skills", "commands", "agents"} {
						if os.RemoveAll(filepath.Join(profile.Path(), "client", name)) != nil {
							t.Fatal("cannot prepare independent missing-assets counterfactual")
						}
					}
				}
				command := profile.Command()
				if mode == "natural" {
					env := make([]string, 0, len(command.Environment))
					for _, entry := range command.Environment {
						if !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") {
							env = append(env, entry)
						}
					}
					command.Environment = env
				}
				version := command
				version.Args = []string{"--version"}
				v, err := runner.Run(ctx, version)
				if err != nil || strings.TrimSpace(string(v.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
					t.Fatal("unverified installed client")
				}
				beforeHome, beforeProject := boundedPluginTree(t, filepath.Join(home, ".claude")), boundedPluginTree(t, project)
				beforeGlobal := fileFingerprint(t, filepath.Join(home, ".claude.json"))
				mu.Lock()
				got = observation{}
				mu.Unlock()
				command.Args = append(command.Args, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--print", "--output-format", "json", "--no-session-persistence", prompt)
				result, err := runner.Run(ctx, command)
				mu.Lock()
				seen := got
				mu.Unlock()
				var completion struct {
					IsError bool   `json:"is_error"`
					Result  string `json:"result"`
				}
				complete := err == nil && result.ExitCode == 0 && json.Unmarshal(result.Stdout, &completion) == nil && !completion.IsError && completion.Result == answer
				groupGone := result.PID > 1 && errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
				t.Logf("observed=%+v, complete=%v, client_exit=%d, group_gone=%v", seen, complete, result.ExitCode, groupGone)
				wantPersonal := mode != "stripped"
				if !complete || !groupGone || seen.Requests != 1 || !seen.Decoded || seen.PersonalSkill != wantPersonal || !seen.ProjectSkill || seen.PersonalAgent != wantPersonal || !seen.ProjectAgent || seen.PersonalAgentWinner || !seen.ProjectAgentWinner {
					t.Error("personal/project asset activation or lifecycle mismatch")
				}
				if seen.PersonalSkillBody != (prompt == "/dax-personal") || seen.ProjectSkillBody != (prompt == "/dax-project") || seen.PersonalCommandBody != (prompt == "/dax-personal-command") || seen.ProjectCommandBody != (prompt == "/dax-project-command") {
					t.Error("client expanded an unexpected or missing asset body")
				}
				if seen.PersonalSharedBody != (prompt == "/dax-shared") || seen.ProjectSharedBody {
					t.Error("native personal skill precedence changed")
				}
				if mode != "natural" && (!reflect.DeepEqual(beforeHome, boundedPluginTree(t, filepath.Join(home, ".claude"))) || !reflect.DeepEqual(beforeProject, boundedPluginTree(t, project)) || beforeGlobal != fileFingerprint(t, filepath.Join(home, ".claude.json"))) {
					t.Error("prepared run changed an owned source")
				}
				if profile.Close() != nil {
					t.Error("profile cleanup failed")
				}
				if _, err := os.Lstat(profile.Path()); !os.IsNotExist(err) {
					t.Error("private profile remains")
				}
			}) {
				return
			}
		}
	}
}
