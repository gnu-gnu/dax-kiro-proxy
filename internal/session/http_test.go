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
			h, err := gateway.New(gateway.Config{Backend: d, Tokens: tokens, FirstEventTimeout: 30 * time.Millisecond, TurnTimeout: 150 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"model": "claude-dax-fixture", "max_tokens": 128, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}})
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
		}
	}
}
