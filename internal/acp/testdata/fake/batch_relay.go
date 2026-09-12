package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

// Independently authored three-call ACP/MCP peer. Client operations have no implementation.
// Only the supplied relay child is executed, and only fixed synthetic results are accepted.
func batchRelayFixture() {
	deadline := time.AfterFunc(40*time.Second, func() { os.Exit(80) })
	defer deadline.Stop()
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
	frames, prompts := 0, 0
	const session = "owned-batch-session"
	for input.Scan() {
		frames++
		var q request
		if frames > 16 || json.Unmarshal(input.Bytes(), &q) != nil {
			os.Exit(81)
		}
		switch q.Method {
		case "initialize":
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}})
		case "session/new":
			var p struct {
				CWD string
				MCP []json.RawMessage `json:"mcpServers"`
			}
			if child != nil || json.Unmarshal(q.Params, &p) != nil || p.CWD == "" || len(p.MCP) != 1 {
				os.Exit(82)
			}
			child = startFixtureRelay(p.MCP[0], p.CWD)
			childDone = make(chan struct{})
			go func() { _ = child.cmd.Wait(); close(childDone) }()
			reply(q.ID, map[string]any{"sessionId": session, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Fixture"}}}})
		case "session/set_model":
			reply(q.ID, map[string]any{})
		case "session/prompt":
			var p struct {
				SessionID string
				Prompt    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
			}
			if child == nil || prompts >= 3 || json.Unmarshal(q.Params, &p) != nil || p.SessionID != session || len(p.Prompt) == 0 || len(p.Prompt) > 16 {
				os.Exit(83)
			}
			last := p.Prompt[len(p.Prompt)-1]
			if last.Type != "text" || len(last.Text) > 64 {
				os.Exit(83)
			}
			prompts++
			observed := map[string]any{"pid": os.Getpid(), "relayPID": child.cmd.Process.Pid, "relayConfig": child.cmd.Args[3], "promptCount": prompts, "prompt": p.Prompt}
			complete := func() {
				text, _ := json.Marshal(observed)
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": string(text)}}}})
				reply(q.ID, map[string]any{"stopReason": "end_turn", "observed": observed})
			}
			if strings.HasPrefix(last.Text, "BATCH_RECOVER_") {
				if prompts != 1 || len(p.Prompt) != 10 {
					os.Exit(83)
				}
				observed["calls"], observed["errors"] = 0, 0
				complete()
				continue
			}
			if len(p.Prompt) != 1 || !strings.HasPrefix(last.Text, "BATCH_REPLY_") {
				os.Exit(83)
			}
			base := 100 + prompts*3
			for n := 1; n <= 3; n++ {
				child.send(base+n, "tools/call", map[string]any{"name": child.alias, "arguments": map[string]int{"n": n}})
			}
			seen := map[int]bool{}
			results := make([]string, 3)
			for range 3 {
				// ReadSlice is bounded by the existing 4-KiB reader; no full output is retained.
				line, err := child.output.ReadSlice('\n')
				var response struct {
					JSONRPC string
					ID      int
					Error   json.RawMessage
					Result  struct {
						IsError bool
						Content []struct{ Type, Text string }
					}
				}
				if err != nil || json.Unmarshal(line, &response) != nil || response.JSONRPC != "2.0" || len(response.Error) != 0 {
					os.Exit(84)
				}
				n := response.ID - base
				if n < 1 || n > 3 || seen[n] || response.Result.IsError != (n == 2) || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" || response.Result.Content[0].Text != last.Text+"_"+strconv.Itoa(n) {
					os.Exit(85)
				}
				seen[n] = true
				results[n-1] = response.Result.Content[0].Text
			}
			digest := sha256.Sum256([]byte(strings.Join(results, "\n")))
			observed["calls"], observed["errors"], observed["digest"] = 3, 1, hex.EncodeToString(digest[:])
			complete()
		case "session/cancel":
		default:
			os.Exit(86)
		}
	}
	if input.Err() != nil {
		os.Exit(87)
	}
}
