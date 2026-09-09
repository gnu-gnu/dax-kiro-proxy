package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// An independently authored peer for the read-only command observation. These synthetic values
// describe a fixture, not Kiro's actual tool inventory. Any prompt or extra command terminates it.
func inventoryFixture(mode string) {
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 64<<10)
	stage, queries := 0, 0
	const session = "fixture-inventory-session"
	for input.Scan() {
		var q request
		if json.Unmarshal(input.Bytes(), &q) != nil {
			os.Exit(70)
		}
		switch {
		case stage == 0 && q.Method == "initialize":
			var p struct {
				Version int            `json:"protocolVersion"`
				Caps    map[string]any `json:"clientCapabilities"`
			}
			if json.Unmarshal(q.Params, &p) != nil || p.Version != 1 || p.Caps == nil || len(p.Caps) != 0 {
				os.Exit(71)
			}
			reply(q.ID, map[string]any{"protocolVersion": 1})
			stage++
		case stage == 1 && q.Method == "session/new":
			var p struct {
				CWD     string            `json:"cwd"`
				Servers []json.RawMessage `json:"mcpServers"`
			}
			if json.Unmarshal(q.Params, &p) != nil || !filepath.IsAbs(p.CWD) || p.Servers == nil || len(p.Servers) != 0 {
				os.Exit(72)
			}
			if mode == "inventory-other-cwd" {
				if len(os.Args) != 3 || p.CWD != os.Args[2] {
					os.Exit(77)
				}
				launch, launchErr := os.Stat(".")
				workspace, workspaceErr := os.Stat(p.CWD)
				if launchErr != nil || workspaceErr != nil || !workspace.IsDir() || os.SameFile(launch, workspace) {
					os.Exit(77)
				}
			}
			if mode == "inventory-mcp-ready" || mode == "inventory-mcp-foreign" || mode == "inventory-mcp-other-server" || mode == "inventory-mcp-multiple" || mode == "inventory-mcp-multiple-missing" || mode == "inventory-mcp-late" {
				owner := session
				server := "dax_session"
				if mode == "inventory-mcp-foreign" {
					owner = "fixture-other-session"
				}
				if mode == "inventory-mcp-other-server" {
					server = "fixture-other-server"
				}
				write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/mcp/server_initialized", "params": map[string]any{"sessionId": owner, "serverName": server, "description": "dax_session"}})
				if mode == "inventory-mcp-multiple" {
					write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/mcp/server_initialized", "params": map[string]any{"sessionId": session, "serverName": "dax_scope_fixture"}})
				}
			}
			if mode != "inventory-silent" {
				owner := session
				var name any = "/tools"
				if mode == "inventory-unavailable" {
					name = "/help"
				}
				if mode == "inventory-malformed" {
					name = 42
				}
				if mode == "inventory-foreign" {
					owner = "fixture-other-session"
				}
				commands := []any{map[string]any{"name": name}}
				if strings.HasPrefix(mode, "inventory-context") {
					subcommands := []string{"show", "add"}
					if mode == "inventory-context-no-show" {
						subcommands = []string{"add"}
					}
					commands = append(commands, map[string]any{"name": "context", "meta": map[string]any{"subcommands": subcommands}})
				}
				if mode == "inventory-duplicate" {
					commands = append(commands, map[string]any{"name": "tools"})
				}
				write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/commands/available", "params": map[string]any{"sessionId": owner, "commands": commands}})
			}
			result := map[string]any{"sessionId": session}
			switch mode {
			case "inventory-catalog", "inventory-catalog-mismatch":
				last := "fixture-model"
				if mode == "inventory-catalog-mismatch" {
					last = "fixture-other-model"
				}
				result["models"] = map[string]any{"currentModelId": "auto", "availableModels": []any{map[string]string{"modelId": "auto"}, map[string]string{"modelId": "fixture.model"}, map[string]string{"modelId": last}}}
			case "inventory-catalog-malformed":
				result["models"] = nil
			}
			reply(q.ID, result)
			stage++
		case stage == 2 && q.Method == "_kiro.dev/commands/execute":
			if !strings.HasPrefix(mode, "inventory-context") && mode != "inventory-ready" && mode != "inventory-other-cwd" && mode != "inventory-catalog" && mode != "inventory-listed" && mode != "inventory-mcp-ready" && mode != "inventory-mcp-multiple" && mode != "inventory-mcp-late" && mode != "inventory-rejected" && mode != "inventory-bad-result" {
				os.Exit(73)
			}
			var p struct {
				Session string `json:"sessionId"`
				Command struct {
					Name string                     `json:"command"`
					Args map[string]json.RawMessage `json:"args"`
				} `json:"command"`
			}
			if json.Unmarshal(q.Params, &p) != nil || p.Session != session {
				os.Exit(74)
			}
			if strings.HasPrefix(mode, "inventory-context") && mode != "inventory-context-no-show" && queries == 1 && p.Command.Name == "context" && len(p.Command.Args) == 2 && string(p.Command.Args["subcommand"]) == `"show"` && string(p.Command.Args["verbose"]) == "true" {
				queries++
				files := map[string]any{"tokens": 23, "items": []any{map[string]any{"name": "/owned/marker", "matched": true, "tokens": 23, "content": "private-resource-fixture"}}}
				if mode == "inventory-context-null-tokens" {
					files["tokens"] = nil
				}
				if mode == "inventory-context-null-items" {
					files["items"] = nil
				}
				reply(q.ID, map[string]any{"success": true, "data": map[string]any{"verbose": true, "breakdown": map[string]any{"contextFiles": files}}})
				continue
			}
			if p.Command.Name != "tools" || p.Command.Args == nil || len(p.Command.Args) != 0 || queries != 0 {
				os.Exit(74)
			}
			queries++
			var success any = mode != "inventory-rejected"
			if mode == "inventory-bad-result" {
				success = "true"
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Independent fixture inventory: fs_read"}}}})
			tools := []any{}
			if mode == "inventory-listed" {
				tools = append(tools, map[string]any{"name": "read", "description": "Independent fixture tool", "status": "ask", "source": "fixture"})
			}
			if mode == "inventory-mcp-ready" || mode == "inventory-mcp-multiple" || mode == "inventory-mcp-late" {
				tools = append(tools, map[string]any{"name": "fixture_relay_alias", "description": "Independent fixture alias", "status": "ask", "source": "fixture"})
			}
			if mode == "inventory-mcp-multiple" {
				tools = append(tools, map[string]any{"name": "@dax_scope_fixture/foreign_fixture_alias", "description": "Independent second alias", "status": "ask", "source": "fixture"})
			}
			reply(q.ID, map[string]any{"success": success, "output": "Independent fixture result", "data": map[string]any{"tools": tools}})
			if mode == "inventory-mcp-late" {
				time.Sleep(20 * time.Millisecond)
				write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/mcp/server_initialized", "params": map[string]any{"sessionId": session, "serverName": "dax_scope_fixture"}})
			}
		default:
			os.Exit(75)
		}
	}
	if input.Err() != nil {
		os.Exit(76)
	}
}
