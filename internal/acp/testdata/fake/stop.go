package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// A finite independent peer supplies a completed first answer and witnesses the next prompt delta.
func stopFixture(reason string) {
	if len(os.Args) != 3 {
		os.Exit(111)
	}
	deadline := time.AfterFunc(20*time.Second, func() { os.Exit(112) })
	defer deadline.Stop()
	const sessionID = "owned-stop-session"
	const question = "Answer the independent completion exercise."
	const followup = "Continue the independent completion exercise."
	const answer = "A copper triangle marks the completed first segment."
	facts := struct {
		PID, Prompts int
		SecondDelta  bool
	}{PID: os.Getpid()}
	publish := func() {
		raw, err := json.Marshal(facts)
		if err != nil || os.WriteFile(os.Args[2], raw, 0600) != nil {
			os.Exit(113)
		}
	}
	publish()
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	frames := 0
	for scan.Scan() {
		frames++
		var q request
		if frames > 64 || json.Unmarshal(scan.Bytes(), &q) != nil {
			os.Exit(114)
		}
		switch q.Method {
		case "initialize":
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}})
		case "session/new":
			reply(q.ID, map[string]any{"sessionId": sessionID, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Stop fixture"}}}})
		case "session/prompt":
			var params struct {
				Session string            `json:"sessionId"`
				Prompt  []json.RawMessage `json:"prompt"`
			}
			facts.Prompts++
			if facts.Prompts > 2 || json.Unmarshal(q.Params, &params) != nil || params.Session != sessionID || len(params.Prompt) == 0 {
				os.Exit(115)
			}
			if facts.Prompts == 1 && !strings.Contains(string(q.Params), question) {
				os.Exit(116)
			}
			chunks, stop := []string{"A copper triangle ", "marks the completed first segment."}, reason
			if facts.Prompts == 2 {
				facts.SecondDelta = strings.Contains(string(q.Params), followup) && !strings.Contains(string(q.Params), question) && !strings.Contains(string(q.Params), answer)
				chunks, stop = []string{"The independent continuation is complete."}, "end_turn"
			}
			publish()
			for _, chunk := range chunks {
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": chunk}}}})
			}
			reply(q.ID, map[string]string{"stopReason": stop})
		case "session/cancel":
			// Every prompt was completed synchronously; this notification has no pending reply.
		default:
			if len(q.ID) != 0 {
				write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32601, "message": "unsupported fixture request"}})
			}
		}
	}
}
