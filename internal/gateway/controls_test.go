package gateway_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnsupportedControlsRejectBeforeBackendAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, control := range []string{`"stop_sequences":["private-marker"]`, `"mcp_servers":[{"authorization_token":"private-token"}]`, `"container":"private-container"`, `"inference_geo":"private-region"`, `"temperature":0`} {
			b := &fakeBackend{turn: normal()}
			body := strings.TrimSuffix(message(stream), "}") + "," + control + "}"
			w := request(handler(t, b, nil), "POST", "/v1/messages", tokens.Model, body)
			var envelope struct {
				Type  string                         `json:"type"`
				Error struct{ Type, Message string } `json:"error"`
			}
			if w.Code != 400 || b.starts.Load() != 0 || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Type != "error" || envelope.Error.Type != "invalid_request_error" {
				t.Fatal("unsupported control did not reject before backend dispatch and SSE commitment")
			}
			if !strings.Contains(envelope.Error.Message, "not supported") || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), tokens.Model) {
				t.Fatal("control rejection must be actionable and omit supplied values and credentials")
			}
		}
	}
}
