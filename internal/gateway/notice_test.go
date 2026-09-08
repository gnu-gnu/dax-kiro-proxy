package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/status"
)

type noticeBackend struct {
	fakeBackend
	lists atomic.Int32
}

func (b *noticeBackend) Models(context.Context) ([]inference.Model, error) {
	b.lists.Add(1)
	return nil, errors.New("model discovery must not occur in a UI hook")
}

func TestModelNoticeUsesOnlyPreparedStateAndUIAuthority(t *testing.T) {
	const route = "/dax-kiro-proxy/hooks/model-capabilities"
	b := &noticeBackend{}
	queue := status.NewTurnQueue()
	queue.Push(status.TurnRecord{Scope: strings.Repeat("a", 64), Model: "claude-dax-fixture", SessionState: "created", Effort: kirofeature.Status{State: kirofeature.Unknown}})
	h, err := gateway.New(gateway.Config{Backend: b, Tokens: tokens, LaunchModel: "claude-dax-fixture", Metrics: queue})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path, key, body string
		code                    int
	}{
		{"POST", route, "", "{}", 401},
		{"POST", route, tokens.Model, "{}", 401},
		{"POST", route, tokens.UI, `{"model":"claude-dax-other"}`, 400},
		{"POST", route, tokens.UI, `{"prompt":"private-value"}`, 400},
		{"POST", route, tokens.UI, `null`, 400},
		{"POST", route, tokens.UI, strings.Repeat(" ", 4097), 413},
		{"GET", route, tokens.UI, "", 404},
		{"POST", route + "/", tokens.UI, "{}", 404},
		{"POST", "/v1/messages", tokens.UI, "{}", 401},
		{"GET", "/v1/models", tokens.UI, "", 401},
	} {
		out := request(h, c.method, c.path, c.key, c.body)
		if out.Code != c.code || strings.Contains(out.Body.String(), "private-value") {
			t.Fatal("startup hook broadened route/body authority or disclosed input", out.Code)
		}
	}
	for _, body := range []string{"", "{}", strings.Repeat(" ", 4094) + "{}"} {
		out := request(h, "POST", route, tokens.UI, body)
		var notice status.ModelNotice
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &notice) != nil || notice.Model != "claude-dax-fixture" || notice.ImageInput != "unknown" {
			t.Fatal("prepared startup information unavailable")
		}
	}
	if b.starts.Load() != 0 || b.lists.Load() != 0 || len(queue.Drain().Records) != 1 {
		t.Fatal("startup information triggered model work or consumed turn metrics")
	}
	unprepared, err := gateway.New(gateway.Config{Backend: b, Tokens: tokens})
	if err != nil || request(unprepared, "POST", route, tokens.UI, "{}").Code != 503 || b.lists.Load() != 0 {
		t.Fatal("missing prepared information triggered discovery")
	}
}
