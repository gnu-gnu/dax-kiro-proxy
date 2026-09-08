package gateway_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/status"
)

func TestMetricsHookScopesCredentialsAndValidatesBeforeDraining(t *testing.T) {
	q := status.NewTurnQueue()
	q.Push(status.TurnRecord{Scope: strings.Repeat("a", 64), Model: "claude-dax-fixture", SessionState: "created", Effort: kirofeature.Status{State: kirofeature.Unknown}})
	b := &fakeBackend{turn: normal()}
	h := handler(t, b, func(cfg *gateway.Config) { cfg.Metrics = q })
	const hook = "/dax-kiro-proxy/hooks/turn-metrics"
	for _, token := range []string{"", tokens.Model} {
		if w := request(h, "POST", hook, token, "{}"); w.Code != 401 {
			t.Fatal("hook accepted wrong credential")
		}
	}
	for _, body := range []string{`null`, `[]`, `{"prompt":"synthetic prompt"}`, `{"a":1,"a":2}`, strings.Repeat(" ", 4097)} {
		if w := request(h, "POST", hook, tokens.UI, body); w.Code != 400 && w.Code != 413 {
			t.Fatal("hook accepted unsupported body")
		}
	}
	if w := request(h, "GET", hook, tokens.UI, ""); w.Code != 404 {
		t.Fatal("hook method scope broadened")
	}
	if w := request(h, "POST", hook+"/extra", tokens.UI, "{}"); w.Code != 404 {
		t.Fatal("hook path scope broadened")
	}
	w := request(h, "POST", hook, tokens.UI, "{}")
	var page status.MetricsPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Records) != 1 {
		t.Fatal("invalid requests consumed a queued completion")
	}
	w = request(h, "POST", hook, tokens.UI, "")
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Records) != 0 {
		t.Fatal("hook replayed records")
	}
	w = request(h, "GET", "/dax-kiro-proxy/status/usage", tokens.UI, "")
	var snapshot struct {
		Available bool               `json:"available"`
		Latest    *status.TurnRecord `json:"latest_turn"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot.Available || snapshot.Latest == nil || snapshot.Latest.Sequence != 1 {
		t.Fatal("missing account usage removed model-only status")
	}
	if b.starts.Load() != 0 {
		t.Fatal("UI routes started model turns")
	}
	if w := request(h, "POST", "/v1/messages", tokens.UI, message(false)); w.Code != 401 {
		t.Fatal("UI credential gained model authority")
	}
}
