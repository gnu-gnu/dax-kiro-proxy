package gateway_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

func TestCompletionReasonPreservesTextAndToolOwnership(t *testing.T) {
	for _, stop := range []string{"end_turn", "max_tokens", "refusal", "pause_turn", "tool_use", "max_turn_requests", "cancelled", "invented", ""} {
		for _, tools := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/tools-%t/stream-%t", stop, tools, stream), func(t *testing.T) {
					steps := []step{{event: inference.Event{Kind: inference.Text, Text: "preserved completion text"}}}
					if tools {
						steps = append(steps, step{event: inference.Event{Kind: inference.Tools, Tools: []anthropic.ToolUse{{ID: "toolu_owned", Name: "client_action", Input: json.RawMessage(`{}`)}}}})
					}
					steps = append(steps, step{event: inference.Event{Kind: inference.End, StopReason: stop}})
					turn := &fakeTurn{steps: steps}
					w := request(handler(t, &fakeBackend{turn: turn}, nil), "POST", "/v1/messages", tokens.Model, toolsRequest(stream))
					valid := !tools && (stop == "end_turn" || stop == "max_tokens" || stop == "refusal" || stop == "pause_turn") || tools && stop == "tool_use"
					if !valid {
						if turn.finished.Load() != 0 || turn.canceled.Load() != 1 || !stream && w.Code != 502 || stream && (!strings.Contains(w.Body.String(), "event: error\n") || strings.Contains(w.Body.String(), "event: message_stop\n")) {
							t.Fatal("invalid completion committed state or claimed successful delivery")
						}
						return
					}
					if w.Code != 200 || turn.finished.Load() != 1 || turn.canceled.Load() != 0 {
						t.Fatal("valid completion did not finalize once")
					}
					if !stream {
						var response anthropic.Response
						if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.StopReason == nil || *response.StopReason != stop || len(response.Content) == 0 || response.Content[0].Text == nil || *response.Content[0].Text != "preserved completion text" {
							t.Fatal("buffered completion changed its reason or text")
						}
					} else {
						names, packets := events(t, w.Body.String())
						text, reason := "", ""
						for _, packet := range packets {
							if delta, ok := packet["delta"].(map[string]any); ok {
								if value, ok := delta["text"].(string); ok {
									text += value
								}
								if value, ok := delta["stop_reason"].(string); ok {
									reason = value
								}
							}
						}
						if names[len(names)-1] != "message_stop" || text != "preserved completion text" || reason != stop {
							t.Fatal("streamed completion changed its reason or text")
						}
					}
				})
			}
		}
	}
}
