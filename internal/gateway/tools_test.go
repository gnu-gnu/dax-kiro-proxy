package gateway_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

func toolsRequest(stream bool) string {
	raw := message(stream)
	return strings.TrimSuffix(raw, "}") + `,"tools":[{"name":"client_action","input_schema":{"type":"object"}}]}`
}
func TestHTTPMixedToolBlocksAndSuccessfulHandoff(t *testing.T) {
	for _, stream := range []bool{false, true} {
		turn := &fakeTurn{steps: []step{{event: inference.Event{Kind: inference.Text, Text: "requesting client action"}}, {event: inference.Event{Kind: inference.Tools, Tools: []anthropic.ToolUse{{ID: "toolu_fixture", Name: "client_action", Input: json.RawMessage(`{"n":9007199254740993}`)}}}}, {event: inference.Event{Kind: inference.End, StopReason: "tool_use"}}}}
		w := request(handler(t, &fakeBackend{turn: turn}, nil), "POST", "/v1/messages", tokens.Model, toolsRequest(stream))
		if w.Code != 200 || turn.finished.Load() != 1 || turn.canceled.Load() != 0 {
			t.Fatalf("tool handoff failed: status %d", w.Code)
		}
		if !stream {
			var response anthropic.Response
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Content) != 2 || response.Content[0].Type != "text" || response.Content[1].Type != "tool_use" || response.Content[1].Name != "client_action" || string(response.Content[1].Input) != `{"n":9007199254740993}` || *response.StopReason != "tool_use" {
				t.Fatal("buffered tool blocks differ")
			}
		} else {
			names, data := events(t, w.Body.String())
			if names[len(names)-1] != "message_stop" {
				t.Fatal("tool SSE did not complete")
			}
			found := false
			for _, event := range data {
				if delta, ok := event["delta"].(map[string]any); ok && delta["type"] == "input_json_delta" {
					if delta["partial_json"] != `{"n":9007199254740993}` {
						t.Fatal("tool input rounded")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("tool input delta absent")
			}
		}
	}
}
func TestToolRestrictionsAndMalformedOutput(t *testing.T) {
	for _, choice := range []string{`{"type":"any"}`, `{"type":"tool","name":"client_action"}`, `{"type":"auto","disable_parallel_tool_use":true}`, `{"type":"none","unexpected":true}`} {
		b := &fakeBackend{turn: normal()}
		body := strings.TrimSuffix(toolsRequest(false), "}") + `,"tool_choice":` + choice + `}`
		w := request(handler(t, b, nil), "POST", "/v1/messages", tokens.Model, body)
		if w.Code != 400 || b.starts.Load() != 0 {
			t.Fatal("unsupported tool restriction ignored")
		}
	}
	for _, tool := range []anthropic.ToolUse{{ID: "toolu_fixture", Name: "undeclared", Input: json.RawMessage(`{}`)}, {ID: "toolu_fixture", Name: "client_action", Input: json.RawMessage(`{"n":`)}} {
		turn := &fakeTurn{steps: []step{{event: inference.Event{Kind: inference.Tools, Tools: []anthropic.ToolUse{tool}}}}}
		w := request(handler(t, &fakeBackend{turn: turn}, nil), "POST", "/v1/messages", tokens.Model, toolsRequest(true))
		if strings.Contains(w.Body.String(), `"type":"tool_use"`) || turn.canceled.Load() != 1 {
			t.Fatal("undeclared or partial tool reached client")
		}
	}
}
