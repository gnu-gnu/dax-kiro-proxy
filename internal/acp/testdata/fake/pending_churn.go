package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// This peer requests one inert client action, or answers a fresh recovery question. Only its
// supplied MCP relay is executed; the synthetic action has no implementation or filesystem effect.
func pendingChurnFixture() {
	timer := time.AfterFunc(30*time.Second, func() { os.Exit(78) })
	defer timer.Stop()
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 64<<10)
	var child *fixtureRelay
	var childDone chan struct{}
	defer func() {
		if child != nil {
			_ = child.input.Close()
			select {
			case <-childDone:
			case <-time.After(time.Second):
				_ = child.cmd.Process.Kill()
				<-childDone
			}
		}
	}()
	frames, prompted := 0, false
	const sessionID = "owned-pending-session"
	for input.Scan() {
		frames++
		var q request
		if frames > 12 || json.Unmarshal(input.Bytes(), &q) != nil {
			os.Exit(79)
		}
		switch q.Method {
		case "initialize":
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}})
		case "session/new":
			var p struct {
				CWD string            `json:"cwd"`
				MCP []json.RawMessage `json:"mcpServers"`
			}
			if child != nil || json.Unmarshal(q.Params, &p) != nil || p.CWD == "" || len(p.MCP) != 1 {
				os.Exit(80)
			}
			child = startFixtureRelay(p.MCP[0], p.CWD)
			// Reap our owned child promptly even while this ACP process is idle. Delaying Wait
			// until ACP EOF would retain a dead child while the proxy verifies relay shutdown.
			childDone = make(chan struct{})
			go func() { _ = child.cmd.Wait(); close(childDone) }()
			reply(q.ID, map[string]any{"sessionId": sessionID, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Fixture"}}}})
		case "session/set_model":
			reply(q.ID, map[string]any{})
		case "session/prompt":
			var p struct {
				Session string            `json:"sessionId"`
				Prompt  []json.RawMessage `json:"prompt"`
			}
			if prompted || child == nil || len(child.cmd.Args) != 4 || json.Unmarshal(q.Params, &p) != nil || p.Session != sessionID || len(p.Prompt) == 0 || len(p.Prompt) > 8 {
				os.Exit(81)
			}
			prompted = true
			var last struct{ Type, Text string }
			if json.Unmarshal(p.Prompt[len(p.Prompt)-1], &last) != nil || last.Type != "text" {
				os.Exit(82)
			}
			observation, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "session": sessionID, "promptCount": 1, "prompt": p.Prompt, "relayPID": child.cmd.Process.Pid, "relayConfig": child.cmd.Args[3]})
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": string(observation)}}}})
			if strings.HasPrefix(last.Text, "PENDING_RECOVER_") {
				reply(q.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if !strings.HasPrefix(last.Text, "PENDING_START_") {
				os.Exit(83)
			}
			child.call()
			// An all-denial/new-question restart must abandon this RPC rather than deliver the
			// denial to the old prompt. A successful MCP envelope is therefore a fixture fault.
			if !child.responseError {
				os.Exit(84)
			}
		case "session/cancel":
		default:
			os.Exit(85)
		}
	}
	if input.Err() != nil {
		os.Exit(86)
	}
}
