package session_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/session"
)

func TestHTTPToolRoundTripThroughIndependentACPAndMCP(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streaming"}[streaming], func(t *testing.T) {
			d := toolDriver(t, "chat-tools", time.Second)
			h, err := gateway.New(gateway.Config{Tokens: gateway.Tokens{Model: strings.Repeat("m", 43), UI: strings.Repeat("u", 43)}, Backend: d, TurnTimeout: 3 * time.Second, FirstEventTimeout: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			initial := map[string]any{"model": fixtureClientID, "max_tokens": 128, "stream": streaming, "messages": []any{map[string]any{"role": "user", "content": "request one synthetic tool"}}, "tools": []any{map[string]any{"name": "client_action", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer"}}, "required": []string{"n"}}}}}
			first := invokeToolHTTP(t, h, initial)
			blocks, reason := responseBlocks(t, first, streaming)
			if reason != "tool_use" || len(blocks) != 2 || blocks[1].Name != "client_action" || d.State() != session.WaitingTools {
				t.Fatal("HTTP handoff did not retain the owned tool turn")
			}
			initial["messages"] = []any{map[string]any{"role": "user", "content": "request one synthetic tool"}, map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": blocks[1].ID, "content": "approved fixture result", "is_error": true}}}}
			second := invokeToolHTTP(t, h, initial)
			result, reason := responseBlocks(t, second, streaming)
			if reason != "end_turn" || len(result) != 1 || result[0].Text == nil || !strings.Contains(*result[0].Text, `"promptCount":1`) || !strings.Contains(*result[0].Text, "approved fixture result") || d.State() != session.Idle {
				t.Fatal("HTTP result did not resume the original ACP request")
			}
			body, _ := json.Marshal(initial)
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(body)))
			r.Header.Set("x-api-key", strings.Repeat("m", 43))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatal("replayed HTTP tool result reached backend")
			}
		})
	}
}
func invokeToolHTTP(t *testing.T, h http.Handler, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(value)
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(body)))
	r.Header.Set("x-api-key", strings.Repeat("m", 43))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("HTTP tool round failed: %d %s", w.Code, w.Body.String())
	}
	return w
}
func responseBlocks(t *testing.T, w *httptest.ResponseRecorder, streaming bool) ([]anthropic.ResponseBlock, string) {
	t.Helper()
	if !streaming {
		var r anthropic.Response
		if json.Unmarshal(w.Body.Bytes(), &r) != nil || r.StopReason == nil {
			t.Fatal("invalid buffered response")
		}
		return r.Content, *r.StopReason
	}
	var blocks []anthropic.ResponseBlock
	var inputs []string
	reason := ""
	for _, packet := range strings.Split(strings.TrimSpace(w.Body.String()), "\n\n") {
		lines := strings.Split(packet, "\n")
		if len(lines) != 2 {
			t.Fatal("invalid event framing")
		}
		var e struct {
			Type  string
			Index int
			Block anthropic.ResponseBlock `json:"content_block"`
			Delta struct {
				Type, Text string
				Partial    string `json:"partial_json"`
				Stop       string `json:"stop_reason"`
			}
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &e) != nil {
			t.Fatal("invalid event JSON")
		}
		switch e.Type {
		case "content_block_start":
			if e.Index != len(blocks) {
				t.Fatal("nonsequential blocks")
			}
			blocks = append(blocks, e.Block)
			inputs = append(inputs, "")
		case "content_block_delta":
			if e.Index >= len(blocks) {
				t.Fatal("delta before start")
			}
			if e.Delta.Type == "text_delta" {
				*blocks[e.Index].Text += e.Delta.Text
			} else if e.Delta.Type == "input_json_delta" {
				inputs[e.Index] += e.Delta.Partial
			}
		case "content_block_stop":
			if blocks[e.Index].Type == "tool_use" {
				blocks[e.Index].Input = json.RawMessage(inputs[e.Index])
			}
		case "message_delta":
			reason = e.Delta.Stop
		case "error":
			t.Fatal("unexpected SSE error")
		}
	}
	return blocks, reason
}

func TestAuthenticationExpiryWhileNoToolHTTPResponseIsOpen(t *testing.T) {
	d := toolDriver(t, "chat-tools-auth", time.Second)
	r := toolRequest(t)
	turn, err := d.Start(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	prefix, uses := toolHandoff(t, turn)
	turn.Finish()
	until := time.Now().Add(2 * time.Second)
	for d.State() != session.Unstarted && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	h, err := gateway.New(gateway.Config{Tokens: gateway.Tokens{Model: strings.Repeat("m", 43), UI: strings.Repeat("u", 43)}, Backend: d, TurnTimeout: 3 * time.Second, FirstEventTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	next := followup(t, r, prefix, uses)
	var messages []any
	for _, m := range next.Messages {
		var blocks []json.RawMessage
		for _, b := range m.Content {
			if b.Type == "text" {
				raw, _ := json.Marshal(map[string]any{"type": "text", "text": b.Text})
				blocks = append(blocks, raw)
			} else {
				blocks = append(blocks, b.Raw)
			}
		}
		messages = append(messages, map[string]any{"role": m.Role, "content": blocks})
	}
	w := invokeToolHTTP(t, h, map[string]any{"model": r.Model, "max_tokens": 128, "messages": messages, "tools": r.Tools})
	if w.Header().Get(gateway.AuthFallbackHeader) != "1" || !strings.Contains(w.Body.String(), "kiro-cli login") {
		t.Fatal("scoped auth expiry was lost during tool waiting")
	}
}
