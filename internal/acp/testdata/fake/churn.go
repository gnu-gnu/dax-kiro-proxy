package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// One independently owned process accepts one session and at most two prompts. Its second
// notification proves that cancellation interrupts an actual response, not an unstarted request.
func churnFixture() {
	timer := time.AfterFunc(30*time.Second, func() { os.Exit(71) })
	defer timer.Stop()
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 64<<10)
	created, prompts, frames := false, 0, 0
	const sessionID = "owned-churn-session"
	for input.Scan() {
		frames++
		var q request
		if frames > 12 || json.Unmarshal(input.Bytes(), &q) != nil {
			os.Exit(72)
		}
		switch q.Method {
		case "initialize":
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}})
		case "session/new":
			var p struct {
				CWD string            `json:"cwd"`
				MCP []json.RawMessage `json:"mcpServers"`
			}
			if created || json.Unmarshal(q.Params, &p) != nil || p.CWD == "" || p.MCP == nil || len(p.MCP) != 0 {
				os.Exit(73)
			}
			created = true
			reply(q.ID, map[string]any{"sessionId": sessionID, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Fixture"}}}})
		case "session/set_model":
			reply(q.ID, map[string]any{})
		case "session/prompt":
			var p struct {
				Session string            `json:"sessionId"`
				Prompt  []json.RawMessage `json:"prompt"`
			}
			prompts++
			if !created || prompts > 2 || json.Unmarshal(q.Params, &p) != nil || p.Session != sessionID || len(p.Prompt) == 0 || len(p.Prompt) > 8 {
				os.Exit(74)
			}
			if prompts == 2 {
				var block struct{ Type, Text string }
				if len(p.Prompt) != 1 || json.Unmarshal(p.Prompt[0], &block) != nil || block.Type != "text" || !strings.HasPrefix(block.Text, "CHURN_HOLD_") {
					os.Exit(75)
				}
			}
			observation, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "session": sessionID, "promptCount": prompts, "prompt": p.Prompt, "loaded": false})
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": string(observation)}}}})
			if prompts == 1 {
				reply(q.ID, map[string]string{"stopReason": "end_turn"})
			}
		case "session/cancel":
			// Deliberately keep the prompt outstanding; the transport must retire this process.
		default:
			os.Exit(76)
		}
	}
	if input.Err() != nil {
		os.Exit(77)
	}
}
