// Independent public ACP peer for search conversion, ownership and cancellation controls.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

func main() {
	mode := os.Args[1]
	encoder := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if encoder.Encode(v) != nil {
			os.Exit(80)
		}
	}
	reply := func(id json.RawMessage, v any) { send(map[string]any{"jsonrpc": "2.0", "id": id, "result": v}) }
	update := func(v any) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "search-fixture", "update": v}})
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var q struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &q) != nil {
			os.Exit(81)
		}
		switch q.Method {
		case "initialize":
			var p struct {
				ProtocolVersion    int
				ClientCapabilities map[string]any
			}
			if json.Unmarshal(q.Params, &p) != nil || p.ProtocolVersion != 1 || p.ClientCapabilities == nil || len(p.ClientCapabilities) != 0 {
				os.Exit(82)
			}
			reply(q.ID, map[string]int{"protocolVersion": 1})
		case "session/new":
			var p struct {
				CWD string
				MCP []any `json:"mcpServers"`
			}
			if json.Unmarshal(q.Params, &p) != nil || !filepath.IsAbs(p.CWD) || p.MCP == nil || len(p.MCP) != 0 {
				os.Exit(83)
			}
			reply(q.ID, map[string]any{"sessionId": "search-fixture", "models": map[string]any{"currentModelId": "fixture-auto", "availableModels": []any{map[string]string{"modelId": "fixture-auto"}, map[string]string{"modelId": "fixture-selected"}}}})
			send(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/commands/available", "params": map[string]any{"sessionId": "search-fixture", "commands": []any{map[string]string{"name": "tools"}}}})
		case "_kiro.dev/commands/execute":
			var p struct {
				SessionID string
				Command   struct {
					Command string
					Args    map[string]any
				}
			}
			if json.Unmarshal(q.Params, &p) != nil || p.SessionID != "search-fixture" || p.Command.Command != "tools" || p.Command.Args == nil || len(p.Command.Args) != 0 {
				os.Exit(84)
			}
			tools := []any{map[string]string{"name": "web_search"}}
			if mode == "extra-tool" {
				tools = append(tools, map[string]string{"name": "execute_bash"})
			}
			reply(q.ID, map[string]any{"success": true, "data": map[string]any{"tools": tools}})
		case "session/set_model":
			var p struct{ SessionID, ModelID string }
			if json.Unmarshal(q.Params, &p) != nil || p.SessionID != "search-fixture" || p.ModelID != "fixture-selected" {
				os.Exit(85)
			}
			reply(q.ID, map[string]any{})
		case "session/prompt":
			if os.WriteFile(filepath.Join(os.Args[2], "prompt-seen"), nil, 0600) != nil {
				os.Exit(86)
			}
			if mode == "crash" {
				os.Exit(87)
			}
			if mode == "hang" {
				continue
			}
			if mode == "native-denied" || mode == "native-denied-no-budget" {
				// No budget is consumed here: this simulates a pre-execution refusal.
				failure := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "denied-one", "title": "web_search", "kind": "search", "status": "failed", "rawInput": map[string]string{"query": "independent blocked query"}, "content": []any{map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": "Independent pre-execution refusal"}}}}
				update(failure)
				update(failure)
				reply(q.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if os.WriteFile(filepath.Join(os.Args[2], "search-0"), nil, 0600) != nil {
				os.Exit(88)
			}
			update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "search-one", "title": "web_search", "kind": "search", "status": "in_progress", "rawInput": map[string]string{"query": "independent query"}})
			result := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "search-one", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]string{"type": "resource_link", "uri": "https://example.org/fixture", "name": "Independent public link"}}}}
			if mode == "native-output" || mode == "native-no-budget" {
				delete(result, "content")
				result["rawOutput"] = map[string]any{"items": []any{map[string]any{"owned_transport": map[string]any{"query": "independent query", "error": nil, "results": []any{map[string]string{"url": "https://example.org/fixture", "title": "Independent native result", "snippet": "Synthetic excerpt"}}}}}}
				if mode == "native-no-budget" {
					if os.Remove(filepath.Join(os.Args[2], "search-0")) != nil {
						os.Exit(90)
					}
				}
			}
			update(result)
			update(result)
			update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "Independent search complete."}})
			reply(q.ID, map[string]string{"stopReason": "end_turn"})
		case "session/cancel":
		default:
			if len(q.ID) > 0 {
				os.Exit(89)
			}
		}
	}
}
