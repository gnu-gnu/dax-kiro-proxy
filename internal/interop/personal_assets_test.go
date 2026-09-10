package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
// responder never dispatches an agent or shell. The explicit conditional-rule controls request
// exactly one Read of an owned fixture through the actual client's permission system.
func TestClaudePersonalCustomizationSources(t *testing.T) {
	observePersonalCustomizationSources(t, personalSourceOptions{})
}

func TestClaudePersonalRuleSources(t *testing.T) {
	observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, rulesOnly: true})
}

func TestClaudePersonalRulePathCharacters(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{
		{"quotes", "dax \"quoted\" 'single'-"}, {"brackets", "dax (round) [square]-"},
		{"at-dollar", "dax @at dollar$-"}, {"backticks", "dax `tick`-"},
		{"backslash", "dax \\backslash-"}, {"tab", "dax tab\t-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, rulesOnly: true, homePrefix: tc.prefix})
		})
	}
}

func TestClaudePersonalRuleImportDepth(t *testing.T) {
	observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, rulesOnly: true, chainLength: 5})
}

// These controls assert defects in rejected memory adapters. A passing observation is not
// acceptance of personal CLAUDE.md support; none of these adapters is used in production.
func TestClaudePersonalMemoryAdapterCounterfactuals(t *testing.T) {
	t.Run("original-exclusion-bypass", func(t *testing.T) {
		observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, modes: []string{"natural", "natural_excluded", "linked", "linked_excluded"}})
	})
	t.Run("wrapper-depth-loss", func(t *testing.T) {
		observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, chainLength: 5, modes: []string{"natural", "tilde"}})
	})
	t.Run("rules-entry-body-loss", func(t *testing.T) {
		observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, rootFrontmatter: true, chainLength: 5, modes: []string{"natural", "rule_entry"}})
	})
	t.Run("named-rules-entry-body-and-order-loss", func(t *testing.T) {
		observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, rootFrontmatter: true, chainLength: 5, modes: []string{"natural", "rule_named"}})
	})
}

// An explicitly supplied alias exclusion is an experimental control, not an inferred
// effective policy or a production adapter. Native root semantics remain the acceptance bar.
func TestClaudePersonalRootAliasExclusionControls(t *testing.T) {
	for _, scope := range []string{"user", "project", "local"} {
		t.Run(scope, func(t *testing.T) {
			observePersonalCustomizationSources(t, personalSourceOptions{
				instructions: true, chainLength: 5, rootFrontmatter: true, aliasExclusion: true,
				exclusionScope: scope,
				modes:          []string{"natural", "linked", "natural_excluded", "linked_alias_excluded"},
			})
		})
	}
}

// These observations describe a rejected adapter, not product acceptance. Without history
// suppression it writes a source transcript; suppression alone still fails the separate
// interactive/plugin source-preservation counterfactual.
func TestClaudePersonalRootEnvironmentTranscriptCounterfactual(t *testing.T) {
	observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, chainLength: 5, rootFrontmatter: true, persistSession: true, modes: []string{"natural", "redirect_env"}})
}

func TestClaudePersonalRootEnvironmentNoHistoryObservations(t *testing.T) {
	observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, chainLength: 5, rootFrontmatter: true, persistSession: true, skipHistory: true, modes: []string{"natural", "redirect_env", "natural_excluded", "redirect_env_excluded", "natural_read", "redirect_env_read"}})
}

// The measured additional-directory candidate loses imports and moves personal root text after
// project instructions. A passing counterfactual records that defect, not product compatibility.
func TestClaudeAdditionalDirectoryMemoryCounterfactual(t *testing.T) {
	observePersonalCustomizationSources(t, personalSourceOptions{instructions: true, chainLength: 5, rootFrontmatter: true, modes: []string{"natural", "additional", "natural_excluded", "additional_excluded"}})
}

// Even if original exclusions were mapped onto an alias, a pattern matching only the
// temporary path can hide otherwise active personal memory. No adapter is enabled.
func TestClaudePersonalRootAliasOverExclusionCounterfactual(t *testing.T) {
	observePersonalCustomizationSources(t, personalSourceOptions{
		instructions: true, chainLength: 5, rootFrontmatter: true, privatePathExclusion: true,
		modes: []string{"natural", "linked_alias_hidden"},
	})
}

// This reproduces a current compatibility defect, not a passing preservation gate: an
// exclusion matching only the private alias removes an otherwise active personal rule.
func TestClaudePersonalRuleAliasExclusionCounterfactual(t *testing.T) {
	for _, scope := range []string{"user", "project", "local"} {
		t.Run(scope, func(t *testing.T) {
			observePersonalCustomizationSources(t, personalSourceOptions{
				instructions: true, rulesOnly: true, chainLength: 5, exclusionScope: scope,
				ruleAliasExclusion: "**/client/rules/general.md",
				modes:              []string{"natural", "prepared", "natural_excluded", "prepared_excluded"},
			})
		})
	}
}

type personalSourceOptions struct {
	instructions         bool
	homePrefix           string
	chainLength          int
	rootFrontmatter      bool
	rulesOnly            bool
	modes                []string
	aliasExclusion       bool
	exclusionScope       string
	persistSession       bool
	skipHistory          bool
	privatePathExclusion bool
	ruleAliasExclusion   string
}

func observePersonalCustomizationSources(t *testing.T, options personalSourceOptions) {
	t.Helper()
	instructions, unusualPaths := options.instructions, options.homePrefix
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for independent personal asset controls; no external inference")
	}
	prefix := "dax-personal-assets-"
	if instructions {
		prefix = "dax personal instructions-"
	}
	root, err := os.MkdirTemp("/private/tmp", prefix)
	if err != nil {
		t.Fatal("cannot create owned asset root")
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	if unusualPaths != "" {
		home = filepath.Join(root, unusualPaths+"home")
	}
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
		if instructions {
			frontmatter := ""
			if options.rootFrontmatter {
				frontmatter = "---\npaths:\n  - \"never-opened/*.go\"\n---\n"
			}
			if !options.rulesOnly {
				write(filepath.Join(base, "CLAUDE.md"), frontmatter+"DAX_"+source.scope+"_INSTRUCTIONS\n@../relative-instructions.txt\n")
				write(filepath.Join(source.path, "relative-instructions.txt"), "DAX_"+source.scope+"_RELATIVE_IMPORT\n")
			}
			write(filepath.Join(base, "rules", "general.md"), "DAX_"+source.scope+"_RULE_BODY_END\n@../../rule-context.txt\n")
			write(filepath.Join(source.path, "rule-context.txt"), "DAX_"+source.scope+"_RULE_IMPORT\n")
			for i := 1; i <= options.chainLength; i++ {
				filename := fmt.Sprintf("hop%d.txt", i)
				if i == 1 {
					filename = "relative-instructions.txt"
					if options.rulesOnly {
						filename = "rule-context.txt"
					}
				}
				content := fmt.Sprintf("DAX_%s_HOP_%d_END\n", source.scope, i)
				if i == 1 {
					marker := "RELATIVE_IMPORT"
					if options.rulesOnly {
						marker = "RULE_IMPORT"
					}
					content += "DAX_" + source.scope + "_" + marker + "\n"
				}
				if i < options.chainLength {
					content += fmt.Sprintf("@hop%d.txt\n", i+1)
				}
				write(filepath.Join(source.path, filename), content)
			}
			write(filepath.Join(base, "rules", "conditional.md"), "---\npaths:\n  - \"never-opened/*.go\"\n---\nDAX_"+source.scope+"_CONDITIONAL\n")
		}
	}
	const model, answer = "claude-dax-personal-assets", "Independent personal assets complete."
	readPath := filepath.Join(project, "never-opened", "owned.go")
	if instructions {
		write(readPath, "Independent conditional source.\n")
	}
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
		PersonalInstructions, ProjectInstructions, PersonalImport, ProjectImport       bool
		PersonalRule, ProjectRule, Conditional                                         bool
		PersonalRuleImport, ProjectRuleImport                                          bool
		ReadRequested, ReadMatched, PersonalConditional, ProjectConditional            bool
		PersonalHops, ProjectHops                                                      []int
		InstructionOrder                                                               []string
		DuplicateInstructions                                                          bool
	}
	var mu sync.Mutex
	var got observation
	requests := 0
	readMode := false
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
			maxRequests := 1
			if readMode {
				maxRequests = 2
			}
			if requests > 32 || got.Requests > maxRequests {
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
			got.PersonalInstructions = bytes.Contains(body, []byte("DAX_personal_INSTRUCTIONS"))
			got.ProjectInstructions = bytes.Contains(body, []byte("DAX_project_INSTRUCTIONS"))
			got.PersonalImport = bytes.Contains(body, []byte("DAX_personal_RELATIVE_IMPORT"))
			got.ProjectImport = bytes.Contains(body, []byte("DAX_project_RELATIVE_IMPORT"))
			got.PersonalRule = bytes.Contains(body, []byte("DAX_personal_RULE_BODY_END"))
			got.ProjectRule = bytes.Contains(body, []byte("DAX_project_RULE_BODY_END"))
			got.PersonalRuleImport = bytes.Contains(body, []byte("DAX_personal_RULE_IMPORT"))
			got.ProjectRuleImport = bytes.Contains(body, []byte("DAX_project_RULE_IMPORT"))
			got.Conditional = bytes.Contains(body, []byte("DAX_personal_CONDITIONAL")) || bytes.Contains(body, []byte("DAX_project_CONDITIONAL"))
			got.PersonalConditional = bytes.Contains(body, []byte("DAX_personal_CONDITIONAL"))
			got.ProjectConditional = bytes.Contains(body, []byte("DAX_project_CONDITIONAL"))
			if instructions {
				got.DuplicateInstructions = false
				var positions []struct {
					marker string
					at     int
				}
				for _, marker := range []string{"DAX_personal_INSTRUCTIONS", "DAX_personal_RELATIVE_IMPORT", "DAX_personal_RULE_BODY_END", "DAX_personal_RULE_IMPORT", "DAX_project_INSTRUCTIONS", "DAX_project_RELATIVE_IMPORT", "DAX_project_RULE_BODY_END", "DAX_project_RULE_IMPORT"} {
					if bytes.Count(body, []byte(marker)) > 1 {
						got.DuplicateInstructions = true
					}
					if at := bytes.Index(body, []byte(marker)); at >= 0 {
						positions = append(positions, struct {
							marker string
							at     int
						}{marker, at})
					}
				}
				sort.Slice(positions, func(i, j int) bool { return positions[i].at < positions[j].at })
				got.InstructionOrder = nil
				for _, p := range positions {
					got.InstructionOrder = append(got.InstructionOrder, p.marker)
				}
			}
			if options.chainLength > 0 {
				got.PersonalHops, got.ProjectHops = nil, nil
				for i := 1; i <= options.chainLength; i++ {
					if bytes.Contains(body, []byte(fmt.Sprintf("DAX_personal_HOP_%d_END", i))) {
						got.PersonalHops = append(got.PersonalHops, i)
					}
					if bytes.Contains(body, []byte(fmt.Sprintf("DAX_project_HOP_%d_END", i))) {
						got.ProjectHops = append(got.ProjectHops, i)
					}
				}
			}
			if readMode {
				if got.Requests == 1 {
					listed := false
					for _, raw := range request.Tools {
						var tool struct{ Name string }
						if json.Unmarshal(raw, &tool) == nil && tool.Name == "Read" {
							listed = true
						}
					}
					if !listed || got.Conditional {
						w.WriteHeader(400)
						return
					}
					got.ReadRequested = true
					writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "tool_use", "id": "toolu_dax_conditional_read", "name": "Read", "input": map[string]string{"file_path": readPath}}}, "tool_use")
					return
				}
				results, err := request.LatestToolResults()
				if err == nil && len(results) == 1 && results[0].ID == "toolu_dax_conditional_read" && !results[0].IsError {
					for _, block := range results[0].Content {
						if block.Type == "text" && strings.Contains(block.Text, "Independent conditional source.") {
							got.ReadMatched = true
						}
					}
				}
				if !got.ReadMatched {
					w.WriteHeader(400)
					return
				}
			}
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
	modes := []string{"natural", "stripped", "prepared"}
	if instructions {
		modes = []string{"natural", "stripped", "natural_excluded", "prepared", "prepared_excluded", "natural_read", "prepared_read"}
	}
	if unusualPaths != "" {
		modes = []string{"natural", "natural_excluded", "prepared", "prepared_excluded"}
	}
	if options.chainLength > 0 {
		modes = []string{"natural", "prepared", "natural_excluded", "prepared_excluded"}
	}
	if options.modes != nil {
		modes = options.modes
	}
	var nativeOrder []string
	for _, mode := range modes {
		prompts := []string{"Return one brief text response without using tools.", "/dax-personal", "/dax-project", "/dax-personal-command", "/dax-project-command", "/dax-shared"}
		if mode == "stripped" || instructions {
			prompts = prompts[:1]
		}
		for _, prompt := range prompts {
			if !t.Run(mode+"/"+strings.TrimPrefix(strings.Split(prompt, " ")[0], "/"), func(t *testing.T) {
				excluded := strings.HasSuffix(mode, "_excluded")
				read := strings.HasSuffix(mode, "_read")
				source := map[string]any{"disableAllHooks": true}
				if options.privatePathExclusion {
					source["claudeMdExcludes"] = []string{"**/client/CLAUDE.md"}
				}
				if options.ruleAliasExclusion != "" {
					source["claudeMdExcludes"] = []string{options.ruleAliasExclusion}
				}
				if read {
					source["permissions"] = map[string]any{"allow": []string{"Read(/" + readPath + ")"}, "deny": []string{"Bash", "Write", "Edit"}}
				}
				if excluded {
					source["claudeMdExcludes"] = []string{filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".claude", "rules", "general.md")}
				}
				if options.exclusionScope == "project" || options.exclusionScope == "local" {
					name := "settings.json"
					if options.exclusionScope == "local" {
						name = "settings.local.json"
					}
					layer := map[string]any{}
					if patterns, present := source["claudeMdExcludes"]; present {
						layer["claudeMdExcludes"] = patterns
						delete(source, "claudeMdExcludes")
					}
					data, err := json.Marshal(layer)
					if err != nil {
						t.Fatal("cannot encode independent settings layer")
					}
					write(filepath.Join(project, ".claude", name), string(data))
				}
				encoded, err := json.Marshal(source)
				if err != nil {
					t.Fatal("cannot encode independent exclusion")
				}
				write(settings, string(encoded))
				profile, err := launcher.PrepareClient(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer profile.Close()
				if instructions && !strings.HasPrefix(mode, "prepared") {
					for _, name := range []string{"CLAUDE.md", "rules"} {
						if os.RemoveAll(filepath.Join(profile.Path(), "client", name)) != nil {
							t.Fatal("cannot isolate independent instruction candidate")
						}
					}
				}
				if strings.HasPrefix(mode, "linked") {
					for _, name := range []string{"CLAUDE.md", "rules"} {
						if os.Symlink(filepath.Join(home, ".claude", name), filepath.Join(profile.Path(), "client", name)) != nil {
							t.Fatal("cannot create independent instruction reference")
						}
					}
					if options.aliasExclusion && excluded {
						// This exact path is deliberately given by the fixture. This control
						// does not implement a glob matcher or obtain effective native settings.
						data, err := os.ReadFile(profile.SettingsPath())
						var overlay map[string]any
						if err != nil || json.Unmarshal(data, &overlay) != nil {
							t.Fatal("cannot read owned overlay")
						}
						overlay["claudeMdExcludes"] = []string{filepath.Join(profile.Path(), "client", "CLAUDE.md")}
						data, err = json.Marshal(overlay)
						if err != nil {
							t.Fatal("cannot encode owned alias control")
						}
						write(profile.SettingsPath(), string(data))
					}
				}
				if strings.HasPrefix(mode, "tilde") {
					write(filepath.Join(profile.Path(), "client", "CLAUDE.md"), "@~/.claude/CLAUDE.md\n")
					if os.Symlink(filepath.Join(home, ".claude", "rules"), filepath.Join(profile.Path(), "client", "rules")) != nil {
						t.Fatal("cannot prepare native HOME import candidate")
					}
				}
				if mode == "rule_entry" || mode == "rule_named" {
					rules := filepath.Join(profile.Path(), "client", "rules")
					name := "00-personal-instructions.md"
					if mode == "rule_named" {
						name = "CLAUDE.md"
					}
					if os.Mkdir(rules, 0700) != nil || os.Symlink(filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(rules, name)) != nil || os.Symlink(filepath.Join(home, ".claude", "rules"), filepath.Join(rules, "source")) != nil {
						t.Fatal("cannot create independent native rules entry")
					}
				}
				if mode == "stripped" {
					for _, name := range []string{"skills", "commands", "agents", "CLAUDE.md", "rules"} {
						if os.RemoveAll(filepath.Join(profile.Path(), "client", name)) != nil {
							t.Fatal("cannot prepare independent missing-assets counterfactual")
						}
					}
				}
				if strings.HasPrefix(mode, "redirect_env") {
					redirectOwnedClientConfig(t, profile, home, options.skipHistory)
				}
				command := profile.Command()
				if strings.HasPrefix(mode, "natural") {
					env := make([]string, 0, len(command.Environment))
					for _, entry := range command.Environment {
						if !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") {
							env = append(env, entry)
						}
					}
					command.Environment = env
				}
				if strings.HasPrefix(mode, "additional") {
					if os.Symlink(filepath.Join(home, ".claude", "rules"), filepath.Join(profile.Path(), "client", "rules")) != nil {
						t.Fatal("cannot retain the independent native personal rules source")
					}
					command.Args = append(command.Args, "--add-dir", filepath.Join(home, ".claude"))
					command.Environment = append(command.Environment, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1")
				}
				version := command
				version.Args = []string{"--version"}
				v, err := runner.Run(ctx, version)
				if err != nil || !launcher.CompatibleClientOutput(v.Stdout) {
					t.Fatal("unverified installed client")
				}
				beforeHome, beforeProject := boundedPluginTree(t, filepath.Join(home, ".claude")), boundedPluginTree(t, project)
				beforeGlobal := fileFingerprint(t, filepath.Join(home, ".claude.json"))
				importFingerprints := make(map[string][32]byte)
				for _, name := range []string{"relative-instructions.txt", "rule-context.txt", "hop2.txt", "hop3.txt", "hop4.txt", "hop5.txt"} {
					path := filepath.Join(home, name)
					importFingerprints[path] = fileFingerprint(t, path)
				}
				mu.Lock()
				got = observation{}
				readMode = read
				mu.Unlock()
				if read {
					prompt = "Read the one independently owned conditional fixture and give a brief text response."
				}
				command.Args = append(command.Args, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--print", "--output-format", "json")
				if !options.persistSession {
					command.Args = append(command.Args, "--no-session-persistence")
				}
				command.Args = append(command.Args, prompt)
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
				if seen.DuplicateInstructions {
					t.Error("instruction content was duplicated")
				}
				if mode == "natural" {
					nativeOrder = append([]string(nil), seen.InstructionOrder...)
				}
				wantOrder := nativeOrder
				missingRootBody := (mode == "rule_entry" || mode == "rule_named") && options.rootFrontmatter
				if missingRootBody {
					wantOrder = append([]string(nil), nativeOrder[1:]...)
				}
				if mode == "rule_named" {
					if len(nativeOrder) != 8 {
						t.Fatal("incomplete native ordering control")
					}
					wantOrder = append([]string{nativeOrder[2], nativeOrder[3], nativeOrder[1]}, nativeOrder[4:]...)
				}
				additional := strings.HasPrefix(mode, "additional")
				if additional && !excluded {
					if len(nativeOrder) != 8 {
						t.Fatal("incomplete additional-directory ordering control")
					}
					wantOrder = append(append([]string{}, nativeOrder[2:]...), nativeOrder[0])
				}
				aliasHidden := mode == "linked_alias_hidden"
				if aliasHidden {
					if len(nativeOrder) != 8 {
						t.Fatal("incomplete native alias-exclusion control")
					}
					wantOrder = nativeOrder[2:]
				}
				ruleAliasHidden := options.ruleAliasExclusion != "" && strings.HasPrefix(mode, "prepared") && !excluded
				if ruleAliasHidden {
					if !options.rulesOnly || len(nativeOrder) != 4 {
						t.Fatal("incomplete native rule-alias control")
					}
					wantOrder = nativeOrder[2:]
				}
				if instructions && !excluded && wantPersonal && !reflect.DeepEqual(wantOrder, seen.InstructionOrder) {
					t.Error("native instruction ordering changed")
				}
				if options.chainLength > 0 {
					wantHops := []int{1, 2, 3, 4}
					if !reflect.DeepEqual(seen.ProjectHops, wantHops) {
						t.Error("native project control did not reach exactly four import hops")
					}
					if mode == "tilde" {
						wantHops = []int{1, 2, 3} // Measured rejected wrapper defect, not product acceptance.
					}
					if excluded || additional || aliasHidden || ruleAliasHidden {
						wantHops = nil
					}
					if !reflect.DeepEqual(seen.PersonalHops, wantHops) {
						t.Error("personal import-depth observation changed")
					}
				}
				wantRequests := 1
				if read {
					wantRequests = 2
				}
				if !complete || !groupGone || seen.Requests != wantRequests || !seen.Decoded || seen.PersonalSkill != wantPersonal || !seen.ProjectSkill || seen.PersonalAgent != wantPersonal || !seen.ProjectAgent || seen.PersonalAgentWinner || !seen.ProjectAgentWinner {
					t.Error("personal/project asset activation or lifecycle mismatch")
				}
				if seen.PersonalSkillBody != (prompt == "/dax-personal") || seen.ProjectSkillBody != (prompt == "/dax-project") || seen.PersonalCommandBody != (prompt == "/dax-personal-command") || seen.ProjectCommandBody != (prompt == "/dax-project-command") {
					t.Error("client expanded an unexpected or missing asset body")
				}
				if seen.PersonalSharedBody != (prompt == "/dax-shared") || seen.ProjectSharedBody {
					t.Error("native personal skill precedence changed")
				}
				wantInstructions := instructions && wantPersonal && !excluded
				wantRule := wantInstructions && !ruleAliasHidden
				// The rejected direct-link candidate demonstrably bypasses this original-path
				// CLAUDE.md exclusion. Keep it as a counterfactual, never a successful adapter.
				wantMemory := (wantInstructions || mode == "linked_excluded") && !options.rulesOnly && !aliasHidden
				wantProjectMemory := instructions && !options.rulesOnly
				if seen.PersonalInstructions != (wantMemory && !missingRootBody) || seen.PersonalImport != (wantMemory && !additional) || seen.PersonalRule != wantRule || seen.PersonalRuleImport != wantRule || seen.ProjectInstructions != wantProjectMemory || seen.ProjectImport != wantProjectMemory || seen.ProjectRule != instructions || seen.ProjectRuleImport != instructions || seen.Conditional != read || seen.PersonalConditional != read || seen.ProjectConditional != read || seen.ReadRequested != read || seen.ReadMatched != read {
					t.Error("instruction/import scope or conditional rule activation changed")
				}
				afterHome := boundedPluginTree(t, filepath.Join(home, ".claude"))
				projectChanged := !reflect.DeepEqual(beforeProject, boundedPluginTree(t, project))
				globalChanged := beforeGlobal != fileFingerprint(t, filepath.Join(home, ".claude.json"))
				rejectedTranscriptWrite := mode == "redirect_env" && options.persistSession && !options.skipHistory
				if options.persistSession {
					categories := map[string]int{}
					transcripts, existingChanged := 0, false
					paths := make(map[string]bool)
					for path := range beforeHome {
						paths[path] = true
					}
					for path := range afterHome {
						paths[path] = true
					}
					for path := range paths {
						before, hadBefore := beforeHome[path]
						after, hasAfter := afterHome[path]
						if hadBefore == hasAfter && before == after {
							continue
						}
						existingChanged = existingChanged || hadBefore
						rel, err := filepath.Rel(filepath.Join(home, ".claude"), path)
						if err != nil {
							t.Fatal("cannot classify owned source change")
						}
						category := strings.SplitN(rel, string(filepath.Separator), 2)[0]
						switch category {
						case "settings.json", "projects", "plugins", "debug", "todos", "session-env", "history.jsonl":
						default:
							category = "other"
						}
						categories[category]++
						if !hadBefore && hasAfter && category == "projects" && strings.HasSuffix(path, ".jsonl") && after.mode.IsRegular() && bytes.Contains(boundedAssetFile(t, path), []byte(answer)) {
							transcripts++
						}
					}
					t.Logf("owned_source_changes=%v, new_answer_transcripts=%d, existing_source_changed=%v, project_changed=%v, global_changed=%v", categories, transcripts, existingChanged, projectChanged, globalChanged)
					if strings.HasPrefix(mode, "natural") && transcripts != 1 {
						t.Error("native persistence control did not save one completed owned transcript")
					}
					if rejectedTranscriptWrite && (transcripts != 1 || existingChanged || len(categories) != 1 || categories["projects"] != 1 || projectChanged || globalChanged) {
						t.Error("rejected environment adapter no longer reproduces the source transcript write")
					}
				}
				if !strings.HasPrefix(mode, "natural") && !rejectedTranscriptWrite && (!reflect.DeepEqual(beforeHome, afterHome) || projectChanged || globalChanged) {
					t.Error("prepared run changed an owned source")
				}
				for path, before := range importFingerprints {
					if fileFingerprint(t, path) != before {
						t.Error("client changed an original import source")
					}
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
