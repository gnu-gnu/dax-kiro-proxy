package gateway_test

import (
	"context"
	"testing"
	"time"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/status"
)

func TestUsageStatusIsImmediateWhileRefreshIsBlocked(t *testing.T) {
	entered := make(chan struct{})
	cache, err := status.NewUsageCache(status.UsageConfig{Fetch: func(ctx context.Context) (status.UsageData, error) {
		close(entered)
		<-ctx.Done()
		return status.UsageData{}, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	b := &fakeBackend{turn: normal()}
	h := handler(t, b, func(cfg *gateway.Config) { cfg.Usage = cache })
	returned := make(chan int, 1)
	go func() { returned <- request(h, "GET", "/dax-kiro-proxy/status/usage", tokens.UI, "").Code }()
	select {
	case code := <-returned:
		if code != 200 {
			t.Fatal("status failed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("UI waited for an upstream usage command")
	}
	<-entered
	if b.starts.Load() != 0 {
		t.Fatal("usage refresh started a model turn")
	}
	if w := request(h, "POST", "/v1/messages", tokens.Model, message(false)); w.Code != 200 {
		t.Fatal("usage refresh blocked model requests")
	}
	if w := request(h, "GET", "/dax-kiro-proxy/status/usage", tokens.Model, ""); w.Code != 401 {
		t.Fatal("usage endpoint changed credential scope")
	}
}
