package interop_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
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
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
)

var outputStyleMarkers = [4]string{"PersonalStyle_311", "PersonalSharedStyle_313", "ProjectSharedStyle_317", "AlternateStyle_331"}

type outputStyleProjection struct {
	Counts       [4]int
	ToolCounts   [2]int
	StyleBytes   int
	SystemBytes  int
	SystemBlocks int
	StyleDigest  [32]byte
}

// Retain only authored markers and a digest of their containing public system block.
func projectOutputStyle(r *anthropic.Request) (outputStyleProjection, error) {
	var p outputStyleProjection
	if r == nil || len(r.Tools) > 2 || len(r.System) > 16 {
		return p, inference.ErrRequest
	}
	for _, raw := range r.Tools {
		var tool struct{ Name string }
		if json.Unmarshal(raw, &tool) != nil {
			return p, inference.ErrRequest
		}
		switch tool.Name {
		case "Bash":
			p.ToolCounts[0]++
		case "Read":
			p.ToolCounts[1]++
		default:
			return p, inference.ErrRequest
		}
	}
	styleBlocks, projectContext := 0, 0
	for _, block := range r.System {
		p.SystemBytes += len(block.Text)
		p.SystemBlocks++
		if block.Type != "text" || len(block.Text) > 128<<10 || strings.Contains(block.Text, "ProjectMemory_337") {
			return p, inference.ErrRequest
		}
		found := false
		for index, marker := range outputStyleMarkers {
			n := strings.Count(block.Text, marker)
			p.Counts[index] += n
			found = found || n != 0
		}
		if found {
			styleBlocks++
			p.StyleBytes, p.StyleDigest = len(block.Text), sha256.Sum256([]byte(block.Text))
		}
	}
	for _, message := range r.Messages {
		for _, block := range message.Content {
			if block.Type != "text" || len(block.Text) > 128<<10 {
				return p, inference.ErrRequest
			}
			for _, marker := range outputStyleMarkers {
				if strings.Contains(block.Text, marker) {
					return p, inference.ErrRequest
				}
			}
			n := strings.Count(block.Text, "ProjectMemory_337")
			if n != 0 && message.Role != "user" {
				return p, inference.ErrRequest
			}
			projectContext += n
		}
	}
	count := 0
	for _, n := range p.Counts {
		count += n
	}
	if count > 1 || styleBlocks > 1 || projectContext != 1 {
		return p, inference.ErrRequest
	}
	return p, nil
}

func TestOutputStyleProjectionRequiresSeparateInstructionRoles(t *testing.T) {
	for _, kind := range []string{"style", "default", "user-style", "system-memory", "duplicate-style", "two-styles", "missing-memory", "assistant-memory"} {
		t.Run(kind, func(t *testing.T) {
			r := &anthropic.Request{System: []anthropic.Block{{Type: "text", Text: outputStyleMarkers[0]}}, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "ProjectMemory_337"}}}}}
			switch kind {
			case "default":
				r.System = nil
			case "user-style":
				r.Messages[0].Content = append(r.Messages[0].Content, r.System[0])
				r.System = nil
			case "system-memory":
				r.System[0].Text += " ProjectMemory_337"
			case "duplicate-style":
				r.System = append(r.System, r.System[0])
			case "two-styles":
				r.System[0].Text += " " + outputStyleMarkers[1]
			case "missing-memory":
				r.Messages = nil
			case "assistant-memory":
				r.Messages[0].Role = "assistant"
			}
			p, err := projectOutputStyle(r)
			valid := kind == "style" || kind == "default"
			if (err == nil) != valid || kind == "style" && (p.Counts[0] != 1 || p.StyleBytes == 0) {
				t.Fatal("output style provenance mismatch")
			}
		})
	}
}

type outputStyleBackend struct {
	mu         sync.Mutex
	starts     int
	projection outputStyleProjection
}

func (b *outputStyleBackend) Models(context.Context) ([]inference.Model, error) {
	return []inference.Model{{ID: "claude-dax-output-style", Name: "Independent style context"}}, nil
}
func (b *outputStyleBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts++
	p, err := projectOutputStyle(r)
	if b.starts != 1 || err != nil || p.ToolCounts != [2]int{1, 1} {
		return nil, inference.ErrRequest
	}
	b.projection = p
	return &completionDisplayTurn{model: r.Model, text: "OutputStyleDone_347"}, nil
}

func TestClaudePersonalOutputStylePreservation(t *testing.T) {
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set pinned Claude for local output style controls; no external inference")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-output-styles-")
	if err != nil {
		t.Fatal("owned style root")
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	write := func(path, value string) {
		t.Helper()
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, []byte(value), 0600) != nil {
			t.Fatal("owned style source")
		}
	}
	userSettings, global := filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json")
	write(global, `{}`)
	write(filepath.Join(project, "CLAUDE.md"), "ProjectMemory_337\n")
	style := func(path, name, marker string, keep bool) {
		flag := ""
		if keep {
			flag = "keep-coding-instructions: true\n"
		}
		write(path, "---\nname: "+name+"\ndescription: Independent style scope control.\n"+flag+"---\n"+marker+"\nUse short text responses without requesting tools.\n")
	}
	personal := filepath.Join(home, ".claude", "output-styles", "owned-personal.md")
	style(filepath.Join(home, ".claude", "output-styles", "owned-shared.md"), "owned-shared", outputStyleMarkers[1], false)
	style(filepath.Join(project, ".claude", "output-styles", "owned-shared.md"), "owned-shared", outputStyleMarkers[2], false)
	style(filepath.Join(home, ".claude", "output-styles", "owned-alternate.md"), "owned-alternate", outputStyleMarkers[3], false)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxProcesses: 1, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("style runner")
	}
	defer runner.Close()
	version, err := runner.Run(ctx, childproc.Command{Executable: client, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, Args: []string{"--version"}})
	if err != nil || strings.TrimSpace(string(version.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified style client")
	}
	var normal, keep outputStyleProjection
	for _, tc := range []struct {
		name, user, project, local string
		keep                       bool
		selected                   int
	}{
		{"personal", "owned-personal", "", "", false, 0},
		{"keep-coding-declaration", "owned-personal", "", "", true, 0},
		{"same-name-project", "owned-shared", "", "", false, 2},
		{"project-selection", "owned-personal", "owned-shared", "", false, 2},
		{"local-selection", "owned-personal", "owned-shared", "owned-alternate", false, 3},
		{"local-default", "owned-personal", "owned-shared", "Default", false, -1},
	} {
		if !t.Run(tc.name, func(t *testing.T) {
			style(personal, "owned-personal", outputStyleMarkers[0], tc.keep)
			settings, _ := json.Marshal(map[string]any{"disableAllHooks": true, "autoMemoryEnabled": false, "outputStyle": tc.user})
			write(userSettings, string(settings))
			for _, layer := range []struct{ name, selected string }{{"settings.json", tc.project}, {"settings.local.json", tc.local}} {
				data := map[string]any{}
				if layer.selected != "" {
					data["outputStyle"] = layer.selected
				}
				encoded, _ := json.Marshal(data)
				write(filepath.Join(project, ".claude", layer.name), string(encoded))
			}
			var reference outputStyleProjection
			for _, mode := range []string{"natural", "prepared"} {
				if !t.Run(mode, func(t *testing.T) {
					beforeHome, beforeProject := boundedPluginTree(t, filepath.Join(home, ".claude")), boundedPluginTree(t, project)
					beforeStyles := boundedPluginTree(t, filepath.Join(home, ".claude", "output-styles"))
					beforeSettings := fileFingerprint(t, userSettings)
					beforeGlobal := fileFingerprint(t, global)
					tokens, err := gateway.NewTokens()
					if err != nil {
						t.Fatal("style credentials")
					}
					backend := &outputStyleBackend{}
					server, err := gateway.StartServer(ctx, gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, Gateway: gateway.Config{Tokens: tokens, Backend: backend, TurnTimeout: 15 * time.Second, FirstEventTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second}})
					if err != nil {
						t.Fatal("style gateway")
					}
					defer server.Close()
					profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: userSettings, Executable: client, Version: launcher.SupportedClientVersion, Model: "claude-dax-output-style", GatewayURL: server.URL(), ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
					if err != nil {
						t.Fatal("style profile")
					}
					defer profile.Close()
					command := profile.Command()
					if mode == "natural" {
						env := []string{}
						for _, value := range command.Environment {
							if !strings.HasPrefix(value, "CLAUDE_CONFIG_DIR=") {
								env = append(env, value)
							}
						}
						command.Environment = env
					}
					command.Args = append(command.Args, "--tools", "Bash,Read", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--print", "--output-format", "json", "--no-session-persistence", "Return a short text response without using tools.")
					result, runErr := runner.Run(ctx, command)
					var response struct {
						Result        string
						IsError       bool `json:"is_error"`
						Type, Subtype string
					}
					complete := runErr == nil && result.ExitCode == 0 && json.Unmarshal(result.Stdout, &response) == nil && !response.IsError && response.Type == "result" && response.Subtype == "success" && response.Result == "OutputStyleDone_347"
					if !complete {
						lower := strings.ToLower(string(result.Stdout))
						markers := map[string]bool{}
						for _, marker := range []string{"400", "401", "404", "429", "output style", "outputstyle", "output-style", "unknown", "invalid", "unsupported", "trust", "login", "log in", "permission", "model", "max_tokens", "thinking", "context_management", "stop_sequences", "metadata", "effort", "temperature", "tools", "settings", "not found", "enoent", "eacces", "already", "must", "cannot", "error", "unknown option", "--print", "worktree", "json", "session", "no such file", "no space", "directory", "path", "home", "yaml", "style"} {
							markers[marker] = strings.Contains(lower, marker)
						}
						t.Logf("client_exit=%d stdout_bytes=%d json_result=%v error_result=%v timeout=%v fixed_failure_markers=%v", result.ExitCode, len(result.Stdout), response.Type == "result", response.IsError, errors.Is(runErr, context.DeadlineExceeded), markers)
					}
					serverErr, profileErr := server.Close(), profile.Close()
					backend.mu.Lock()
					seen, starts := backend.projection, backend.starts
					backend.mu.Unlock()
					if mode == "natural" {
						reference = seen
					}
					want := [4]int{}
					if tc.selected >= 0 {
						want[tc.selected] = 1
					}
					homeUnchanged := reflect.DeepEqual(beforeHome, boundedPluginTree(t, filepath.Join(home, ".claude")))
					globalUnchanged := fileFingerprint(t, global) == beforeGlobal
					sources := reflect.DeepEqual(beforeStyles, boundedPluginTree(t, filepath.Join(home, ".claude", "output-styles"))) && fileFingerprint(t, userSettings) == beforeSettings && reflect.DeepEqual(beforeProject, boundedPluginTree(t, project))
					if mode == "prepared" {
						sources = sources && homeUnchanged && globalUnchanged
					}
					_, removed := os.Lstat(profile.Path())
					gone := serverErr == nil && profileErr == nil && os.IsNotExist(removed) && runner.Active() == 0 && result.PID > 1 && errors.Is(syscall.Kill(result.PID, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
					connection, dialErr := net.DialTimeout("tcp", strings.TrimPrefix(server.URL(), "http://"), time.Second)
					if connection != nil {
						connection.Close()
					}
					gone = gone && dialErr != nil
					equivalent := seen == reference
					t.Logf("mode=%s requests=%d selected_markers=%v style_system_bytes=%d total_system_bytes=%d system_blocks=%d native_system_block_matches=%v complete=%v source_styles_settings_unchanged=%v home_tree_unchanged=%v global_unchanged=%v ownership_removed=%v", mode, starts, seen.Counts, seen.StyleBytes, seen.SystemBytes, seen.SystemBlocks, equivalent, complete, sources, homeUnchanged, globalUnchanged, gone)
					if !complete || starts != 1 || seen.Counts != want || !equivalent || !sources || !gone {
						t.Fatal("native output style preservation failed")
					}
					if mode == "natural" && tc.name == "personal" {
						normal = seen
					}
					if mode == "natural" && tc.name == "keep-coding-declaration" {
						keep = seen
					}
				}) {
					return
				}
			}
		}) {
			return
		}
	}
	if normal.SystemBytes != 0 && keep.SystemBytes != 0 {
		// Compare against the running native reference, without defining undocumented coding prose.
		t.Logf("native_keep_coding_system_size_changed=%v native_keep_coding_style_block_changed=%v", normal.SystemBytes != keep.SystemBytes, normal.StyleDigest != keep.StyleDigest)
	}
}
