package session_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/session"
)

func TestHTTPThroughIndependentACPProcess(t *testing.T) {
	for _, mode := range []string{"chat", "chat-auth", "chat-before", "chat-slow"} {
		for _, stream := range []bool{false, true} {
			d := driver(t, mode)
			tokens, err := gateway.NewTokens()
			if err != nil {
				t.Fatal(err)
			}
			first, total := 500*time.Millisecond, 2*time.Second
			if mode == "chat-before" {
				first, total = 100*time.Millisecond, time.Second
			}
			if mode == "chat-slow" {
				total = 800 * time.Millisecond
			}
			h, err := gateway.New(gateway.Config{Backend: d, Tokens: tokens, FirstEventTimeout: first, TurnTimeout: total, KeepAliveInterval: 10 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 128, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}})
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(payload)))
			r.Header.Set("x-api-key", tokens.Model)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			switch mode {
			case "chat":
				if w.Code != 200 || !strings.Contains(w.Body.String(), "stone") {
					t.Fatalf("text path %t: %d %s", stream, w.Code, w.Body.String())
				}
				if d.State() != session.Idle {
					t.Fatal("successful ACP turn not idle")
				}
			case "chat-auth":
				if w.Code != 200 || !strings.Contains(w.Body.String(), "kiro-cli login") {
					t.Fatal("auth path did not complete normally")
				}
			case "chat-before":
				if !strings.Contains(w.Body.String(), "first model event") {
					t.Fatalf("first deadline not propagated: %s", w.Body.String())
				}
			case "chat-slow":
				if !strings.Contains(w.Body.String(), "turn deadline") {
					t.Fatalf("turn deadline not propagated: %s", w.Body.String())
				}
			}
			if mode != "chat" && d.State() != session.Unstarted {
				t.Fatal("failed HTTP turn retained ACP state")
			}
			if stream && (mode == "chat-before" || mode == "chat-slow") && !strings.Contains(w.Body.String(), "event: ping\n") {
				t.Fatal("silent ACP wait did not emit a keepalive")
			}
		}
	}
}
