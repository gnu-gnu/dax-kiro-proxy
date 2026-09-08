package gateway_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
)

type identityBackend struct{ received *anthropic.Request }

func (b *identityBackend) Models(context.Context) ([]inference.Model, error) { return nil, nil }
func (b *identityBackend) Start(_ context.Context, r *anthropic.Request) (inference.Turn, error) {
	b.received = r
	return normal(), nil
}
func TestClientConversationHeadersAreBoundedAndUnambiguous(t *testing.T) {
	for _, value := range []string{"synthetic-session", "", "two,values", "space value", "bad\tvalue", "한글", strings.Repeat("a", 129), "duplicate"} {
		invalid := value != "synthetic-session"
		b := &identityBackend{}
		h, err := gateway.New(gateway.Config{Tokens: tokens, Backend: b})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(message(false)))
		r.Header.Set("x-api-key", tokens.Model)
		r.Header.Set("x-claude-code-session-id", value)
		r.Header.Set("x-claude-code-agent-id", "synthetic-agent")
		r.Header.Set("x-claude-code-parent-agent-id", "synthetic-parent")
		if value == "duplicate" {
			r.Header.Add("x-claude-code-session-id", "another-session")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if invalid {
			if w.Code != 400 || b.received != nil {
				t.Fatal("ambiguous session header reached backend")
			}
			continue
		}
		if w.Code != 200 || b.received == nil || b.received.Identity.Session != "synthetic-session" || b.received.Identity.Agent != "synthetic-agent" || b.received.Identity.ParentAgent != "synthetic-parent" {
			t.Fatal("public conversation identity was lost")
		}
	}
}
