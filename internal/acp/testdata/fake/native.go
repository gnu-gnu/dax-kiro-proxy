package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// This peer validates a one-prompt negative-effect observer. The marker/read variants deliberately
// touch only the test's synthetic workspace so the observer must detect them as failed isolation.
func nativeControlFixture(mode string) {
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 64<<10)
	const session = "independent-native-session"
	stage := 0
	workspace := ""
	var promptID json.RawMessage
	notice := func(text, owner string) {
		write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
			"sessionId": owner, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}},
		}})
	}
	finish := func() {
		if mode != "native-control-no-text" {
			notice("Independent native-effect control finished.", session)
		}
		stop := "end_turn"
		if mode == "native-control-cancelled" {
			stop = "cancelled"
		}
		reply(promptID, map[string]string{"stopReason": stop})
		stage = 8
	}
	for input.Scan() {
		var q request
		if json.Unmarshal(input.Bytes(), &q) != nil {
			os.Exit(80)
		}
		switch {
		case stage == 0 && q.Method == "initialize":
			var p struct {
				Version int            `json:"protocolVersion"`
				Caps    map[string]any `json:"clientCapabilities"`
			}
			if json.Unmarshal(q.Params, &p) != nil || p.Version != 1 || p.Caps == nil || len(p.Caps) != 0 {
				os.Exit(81)
			}
			reply(q.ID, map[string]int{"protocolVersion": 1})
			stage++
		case stage == 1 && q.Method == "session/new":
			var p struct {
				CWD string `json:"cwd"`
				MCP []any  `json:"mcpServers"`
			}
			if json.Unmarshal(q.Params, &p) != nil || !filepath.IsAbs(p.CWD) || p.MCP == nil || len(p.MCP) != 0 {
				os.Exit(82)
			}
			workspace = p.CWD
			write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/commands/available", "params": map[string]any{
				"sessionId": session, "commands": []any{map[string]string{"name": "tools"}},
			}})
			reply(q.ID, map[string]string{"sessionId": session})
			stage++
		case stage == 2 && q.Method == "_kiro.dev/commands/execute":
			var p struct {
				Session string `json:"sessionId"`
				Command struct {
					Command string         `json:"command"`
					Args    map[string]any `json:"args"`
				} `json:"command"`
			}
			if json.Unmarshal(q.Params, &p) != nil || p.Session != session || p.Command.Command != "tools" || p.Command.Args == nil || len(p.Command.Args) != 0 {
				os.Exit(83)
			}
			reply(q.ID, map[string]any{"success": true, "data": map[string]any{"tools": []any{}}})
			stage++
		case stage == 3 && q.Method == "session/set_model":
			var p struct{ SessionID, ModelID string }
			if json.Unmarshal(q.Params, &p) != nil || p.SessionID != session || p.ModelID != "auto" {
				os.Exit(84)
			}
			reply(q.ID, map[string]any{})
			stage++
		case stage == 4 && q.Method == "session/prompt":
			var p struct {
				SessionID string
				Prompt    []struct{ Type, Text string }
			}
			if json.Unmarshal(q.Params, &p) != nil || p.SessionID != session || len(p.Prompt) != 1 || p.Prompt[0].Type != "text" || p.Prompt[0].Text == "" {
				os.Exit(85)
			}
			promptID = q.ID
			if mode == "native-control-remote-error" {
				write(map[string]any{"jsonrpc": "2.0", "id": promptID, "error": map[string]any{"code": -32007, "message": "Independent diagnostic text must not enter the report"}})
				stage = 8
				continue
			}
			if mode == "native-control-hang" {
				stage = 8
				continue
			}
			if mode == "native-control-marker" && os.WriteFile(filepath.Join(workspace, "native-write.txt"), []byte("independent control"), 0600) != nil {
				os.Exit(86)
			}
			if mode == "native-control-canary" || mode == "native-control-last-canary" {
				data, err := os.ReadFile(filepath.Join(workspace, "native-read.txt"))
				if err != nil || len(data) > 128 {
					os.Exit(87)
				}
				notice(string(data[:len(data)/2]), session)
				notice(string(data[len(data)/2:]), session)
				if mode == "native-control-last-canary" {
					finish()
					continue
				}
			}
			if mode == "native-control-tool-status" {
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": session, "update": map[string]string{"sessionUpdate": "tool_call_update", "toolCallId": "independent-native-call", "status": "completed"},
				}})
			}
			if mode == "native-control-foreign" {
				notice("Independent foreign update", "other-native-session")
			}
			write(map[string]any{"jsonrpc": "2.0", "id": 901, "method": "session/request_permission", "params": map[string]any{
				"sessionId": session, "options": []any{map[string]string{"optionId": "allow", "kind": "allow_once"}, map[string]string{"optionId": "reject", "kind": "reject_once"}},
			}})
			stage = 5
		case stage == 5 && q.Method == "" && string(q.ID) == "901":
			var result struct {
				Outcome struct{ Outcome, OptionID string }
			}
			if json.Unmarshal(q.Result, &result) != nil || result.Outcome.Outcome != "selected" || result.Outcome.OptionID != "reject" {
				os.Exit(88)
			}
			write(map[string]any{"jsonrpc": "2.0", "id": 902, "method": "fs/read_text_file", "params": map[string]any{"sessionId": session, "path": filepath.Join(workspace, "native-read.txt")}})
			stage++
		case (stage == 6 && string(q.ID) == "902" || stage == 7 && string(q.ID) == "903") && q.Method == "":
			var result struct{ Code int }
			if json.Unmarshal(q.Error, &result) != nil || result.Code != -32601 {
				os.Exit(89)
			}
			if stage == 6 {
				write(map[string]any{"jsonrpc": "2.0", "id": 903, "method": "terminal/create", "params": map[string]any{"sessionId": session, "command": "/usr/bin/false"}})
				stage++
			} else {
				finish()
			}
		default:
			os.Exit(90)
		}
	}
}
