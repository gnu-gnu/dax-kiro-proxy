package gateway_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

func TestSearchGatewayDelivery(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		x := anthropic.SearchExchange{ID: "srvtoolu_gateway", Query: "synthetic search", Results: []anthropic.SearchResult{{Type: "web_search_result", URL: "https://example.org/found", Title: "Found"}}}
		turn := &fakeTurn{steps: []step{{event: inference.Event{Kind: inference.Search, Searches: []anthropic.SearchExchange{x}}}, {event: inference.Event{Kind: inference.Text, Text: "Search finished."}}, {event: inference.Event{Kind: inference.End, StopReason: "end_turn"}}}}
		h := handler(t, &fakeBackend{turn: turn}, nil)
		body := fmt.Sprintf(`{"model":"claude-dax-fixture","max_tokens":256,"stream":%t,"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],"messages":[{"role":"user","content":"synthetic search"}]}`, streaming)
		w := request(h, "POST", "/v1/messages", tokens.Model, body)
		if w.Code != 200 || turn.finished.Load() != 1 || turn.canceled.Load() != 0 {
			t.Fatalf("search delivery stream=%t status=%d finish=%d cancel=%d", streaming, w.Code, turn.finished.Load(), turn.canceled.Load())
		}
		if streaming {
			names, data := events(t, w.Body.String())
			if len(names) == 0 || names[len(names)-1] != "message_stop" {
				t.Fatal("missing terminal stream")
			}
			found := false
			for _, e := range data {
				if e["type"] == "content_block_start" {
					b := e["content_block"].(map[string]any)
					if b["type"] == "web_search_tool_result" {
						found = b["tool_use_id"] == x.ID
					}
				}
			}
			if !found {
				t.Fatal("missing correlated search block")
			}
		} else {
			var response anthropic.Response
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Content) != 3 || response.Content[1].ToolUseID != x.ID || response.Usage.ServerTools == nil || response.Usage.ServerTools.WebSearchRequests != 1 {
				t.Fatal("incorrect JSON search result")
			}
		}
	}
}

func TestSearchGatewayRejectsUnsolicitedDuplicateAndExcessResults(t *testing.T) {
	x := anthropic.SearchExchange{ID: "srvtoolu_same", Query: "synthetic", Results: []anthropic.SearchResult{}}
	for _, kind := range []string{"unsolicited", "duplicate", "excess", "mixed"} {
		turn := &fakeTurn{steps: []step{{event: inference.Event{Kind: inference.Search, Searches: []anthropic.SearchExchange{x}}}, {event: inference.Event{Kind: inference.End, StopReason: "end_turn"}}}}
		body := strings.TrimSuffix(message(false), "}") + `,"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`
		switch kind {
		case "unsolicited":
			body = message(false)
		case "duplicate":
			turn.steps[0].event.Searches = append(turn.steps[0].event.Searches, x)
		case "excess":
			y := x
			y.ID = "srvtoolu_other"
			turn.steps[0].event.Searches = append(turn.steps[0].event.Searches, y)
		case "mixed":
			turn.steps[0].event.Text = "unvalidated content"
		}
		w := request(handler(t, &fakeBackend{turn: turn}, nil), "POST", "/v1/messages", tokens.Model, body)
		if w.Code != 502 || turn.canceled.Load() != 1 || strings.Contains(w.Body.String(), "srvtoolu_") {
			t.Fatalf("invalid search kind=%s status=%d", kind, w.Code)
		}
	}
}
