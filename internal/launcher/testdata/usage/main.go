// Independent synthetic usage peer; no product imports or installed-client captures.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func record(kind string) {
	f, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "usage-observations"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(70)
	}
	if _, err = fmt.Fprintf(f, "%s %d\n", kind, os.Getpid()); err != nil || f.Close() != nil {
		os.Exit(71)
	}
}
func emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		os.Exit(72)
	}
	fmt.Println(string(b))
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		record("version")
		fmt.Println(filepath.Base(os.Args[0]) + " 2.21.3")
		return
	}
	if strings.Join(os.Args[1:], " ") == "whoami --format json" {
		record("identity")
		fmt.Println(`{"accountType":"synthetic","email":"usage-peer@example.invalid"}`)
		return
	}
	if strings.Join(os.Args[1:], " ") != "acp --agent-engine v2 --agent dax-account-usage" {
		os.Exit(73)
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(74)
	}
	source, err := os.ReadFile(filepath.Join(cwd, ".kiro", "agents", "dax-account-usage.json"))
	if err != nil {
		os.Exit(75)
	}
	var agent struct {
		Tools, AllowedTools, Resources []any
		MCPServers, Hooks              map[string]any
		IncludeMcpJson                 bool
	}
	if json.Unmarshal(source, &agent) != nil || agent.Tools == nil || len(agent.Tools) != 0 || agent.AllowedTools == nil || len(agent.AllowedTools) != 0 || agent.Resources == nil || len(agent.Resources) != 0 || agent.MCPServers == nil || len(agent.MCPServers) != 0 || len(agent.Hooks) != 0 || agent.IncludeMcpJson {
		os.Exit(76)
	}
	settings, err := os.ReadFile(filepath.Join(os.Getenv("KIRO_HOME"), "settings", "cli.json"))
	if err != nil || string(settings) != `{"chat.disableInheritingDefaultResources":true}` || os.Getenv("TERM") != "dumb" {
		os.Exit(77)
	}
	record("acp")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 128<<10)
	step := 0
	for scanner.Scan() {
		var r struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
			Result json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			os.Exit(78)
		}
		if r.Method == "" {
			record("permission-denied")
			continue
		}
		step++
		var result any
		switch step {
		case 1:
			var p struct{ ClientCapabilities map[string]any }
			if r.Method != "initialize" || json.Unmarshal(r.Params, &p) != nil || len(p.ClientCapabilities) != 0 {
				os.Exit(79)
			}
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
		case 2:
			var p struct {
				Cwd        string
				MCPServers []any
			}
			if r.Method != "session/new" || json.Unmarshal(r.Params, &p) != nil || !filepath.IsAbs(p.Cwd) || p.MCPServers == nil || len(p.MCPServers) != 0 {
				record("session-binding-failed")
				os.Exit(80)
			}
			requested, requestErr := os.Stat(p.Cwd)
			current, currentErr := os.Stat(cwd)
			if requestErr != nil || currentErr != nil || !os.SameFile(requested, current) {
				record("session-directory-failed")
				os.Exit(80)
			}
			emit(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/commands/available", "params": map[string]any{"sessionId": "synthetic-usage", "commands": []any{map[string]any{"name": "usage"}, map[string]any{"name": "tools"}}}})
			result = map[string]any{"sessionId": "synthetic-usage"}
		case 3, 4:
			var p struct {
				SessionID string
				Command   struct {
					Command string
					Args    map[string]any
				}
			}
			command := "tools"
			if step == 4 {
				command = "usage"
			}
			if r.Method != "_kiro.dev/commands/execute" || json.Unmarshal(r.Params, &p) != nil || p.SessionID != "synthetic-usage" || p.Command.Command != command || p.Command.Args == nil || len(p.Command.Args) != 0 {
				os.Exit(81)
			}
			record(command)
			if step == 3 {
				result = map[string]any{"success": true, "data": map[string]any{"tools": []any{}}}
			} else {
				if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "usage-hold")); err == nil {
					continue
				}
				result = map[string]any{"success": true, "data": map[string]any{"usageBreakdowns": []any{map[string]any{"resourceType": "CREDIT", "used": 17.25, "hasLimit": true, "limit": 120}}}}
			}
		default:
			record("unexpected-rpc")
			os.Exit(82)
		}
		emit(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": result})
	}
}
