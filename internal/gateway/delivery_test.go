package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/status"
)

// This independent finalizer holds successful delivery bookkeeping after the terminal write.
type heldCompletion struct {
	*fakeTurn
	queue            *status.TurnQueue
	entered, release chan struct{}
}

func (t *heldCompletion) Finish() {
	close(t.entered)
	<-t.release
	t.queue.Push(status.TurnRecord{Scope: strings.Repeat("b", 64), Model: t.Model(), SessionState: "created", Effort: kirofeature.Status{State: kirofeature.Unknown}})
	t.fakeTurn.Finish()
}

type deliveryBackend struct{ turns chan inference.Turn }

func (b deliveryBackend) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	select {
	case turn := <-b.turns:
		return turn, nil
	default:
		return nil, errors.New("independent completion fixture exhausted")
	}
}
func (deliveryBackend) Models(context.Context) ([]inference.Model, error) { return nil, nil }

func TestMetricsDrainWaitsForAlreadyDeliveredFinalization(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[streaming], func(t *testing.T) {
			q := status.NewTurnQueue()
			turn := &heldCompletion{fakeTurn: normal(), queue: q, entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(turn.release) })
			b := deliveryBackend{turns: make(chan inference.Turn, 1)}
			b.turns <- turn
			h, err := gateway.New(gateway.Config{Backend: b, Tokens: tokens, Metrics: q})
			if err != nil {
				t.Fatal(err)
			}
			modelDone := make(chan struct{})
			go func() { defer close(modelDone); request(h, "POST", "/v1/messages", tokens.Model, message(streaming)) }()
			t.Cleanup(func() { <-modelDone })
			select {
			case <-turn.entered:
			case <-time.After(time.Second):
				t.Fatal("finalizer did not arrive")
			}
			drained := make(chan *httptest.ResponseRecorder, 1)
			go func() { drained <- request(h, "POST", "/dax-kiro-proxy/hooks/turn-metrics", tokens.UI, "{}") }()
			select {
			case <-drained:
				t.Fatal("Stop-time drain missed the delivered turn while finalization was held")
			case <-time.After(30 * time.Millisecond):
			}
			// Other UI routes must stay independent from finalization.
			if request(h, "GET", "/dax-kiro-proxy/status/usage", tokens.UI, "").Code != 200 {
				t.Fatal("status became unavailable")
			}
			release.Do(func() { close(turn.release) })
			select {
			case out := <-drained:
				var page status.MetricsPage
				if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil || len(page.Records) != 1 {
					t.Fatal("completed turn absent after finalizer release")
				}
			case <-time.After(time.Second):
				t.Fatal("drain did not join released finalizer")
			}
			<-modelDone
			if len(q.Drain().Records) != 0 || q.Latest() == nil {
				t.Fatal("drain replayed or erased last status")
			}
		})
	}
}

func TestMetricsFinalizationWaitIsBoundedAndDoesNotDrainOnFailure(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "bad-auth", "bad-body"} {
		t.Run(mode, func(t *testing.T) {
			q := status.NewTurnQueue()
			q.Push(status.TurnRecord{Scope: strings.Repeat("a", 64), Model: "claude-dax-fixture", SessionState: "created", Effort: kirofeature.Status{State: kirofeature.Unknown}})
			turn := &heldCompletion{fakeTurn: normal(), queue: q, entered: make(chan struct{}), release: make(chan struct{})}
			defer close(turn.release)
			b := deliveryBackend{turns: make(chan inference.Turn, 1)}
			b.turns <- turn
			h, err := gateway.New(gateway.Config{Backend: b, Tokens: tokens, Metrics: q})
			if err != nil {
				t.Fatal(err)
			}
			modelDone := make(chan struct{})
			go func() { defer close(modelDone); request(h, "POST", "/v1/messages", tokens.Model, message(true)) }()
			t.Cleanup(func() { <-modelDone })
			select {
			case <-turn.entered:
			case <-time.After(time.Second):
				t.Fatal("finalizer did not arrive")
			}
			key, body, code := tokens.UI, "{}", 503
			if mode == "bad-auth" {
				key, code = tokens.Model, 401
			}
			if mode == "bad-body" {
				body, code = `{"model":"ignored"}`, 400
			}
			r := httptest.NewRequest("POST", "/dax-kiro-proxy/hooks/turn-metrics", strings.NewReader(body))
			r.Header.Set("x-api-key", key)
			if mode == "cancel" {
				ctx, stop := context.WithCancel(t.Context())
				stop()
				r = r.WithContext(ctx)
			}
			started := time.Now()
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code != code || time.Since(started) > time.Second || len(q.Drain().Records) != 1 {
				t.Fatal("failed UI wait consumed metrics or exceeded its bound", out.Code)
			}
			if mode != "timeout" && time.Since(started) > 100*time.Millisecond {
				t.Fatal("invalid/canceled hook waited on model finalization")
			}
		})
	}
}

func TestMetricsDrainJoinsOverlappingFinalizations(t *testing.T) {
	q := status.NewTurnQueue()
	b := deliveryBackend{turns: make(chan inference.Turn, 2)}
	var releases [2]sync.Once
	turns := [2]*heldCompletion{}
	for i := range turns {
		turns[i] = &heldCompletion{fakeTurn: normal(), queue: q, entered: make(chan struct{}), release: make(chan struct{})}
		b.turns <- turns[i]
		defer releases[i].Do(func() { close(turns[i].release) })
	}
	h, err := gateway.New(gateway.Config{Backend: b, Tokens: tokens, Metrics: q})
	if err != nil {
		t.Fatal(err)
	}
	var modelCalls sync.WaitGroup
	for range turns {
		modelCalls.Add(1)
		go func() { defer modelCalls.Done(); request(h, "POST", "/v1/messages", tokens.Model, message(false)) }()
	}
	t.Cleanup(modelCalls.Wait)
	for _, turn := range turns {
		select {
		case <-turn.entered:
		case <-time.After(time.Second):
			t.Fatal("overlapping finalizer absent")
		}
	}
	outcomes := make(chan *httptest.ResponseRecorder, 1)
	go func() { outcomes <- request(h, "POST", "/dax-kiro-proxy/hooks/turn-metrics", tokens.UI, "{}") }()
	select {
	case <-outcomes:
		t.Fatal("overlapping deliveries were skipped")
	case <-time.After(20 * time.Millisecond):
	}
	releases[0].Do(func() { close(turns[0].release) })
	select {
	case <-outcomes:
		t.Fatal("first completion released a second pending finalizer")
	case <-time.After(20 * time.Millisecond):
	}
	releases[1].Do(func() { close(turns[1].release) })
	select {
	case out := <-outcomes:
		var page status.MetricsPage
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil || len(page.Records) != 2 {
			t.Fatal("overlapping completion records missing")
		}
	case <-time.After(time.Second):
		t.Fatal("overlapping completion wait was not released")
	}
}

type failedTerminalWrite struct{ header http.Header }

func (w failedTerminalWrite) Header() http.Header { return w.header }
func (failedTerminalWrite) WriteHeader(int)       {}
func (failedTerminalWrite) Write([]byte) (int, error) {
	return 0, errors.New("independent write failure")
}

func TestFailedTerminalWriteReleasesMetricsWaitWithoutPublishing(t *testing.T) {
	q := status.NewTurnQueue()
	turn := &heldCompletion{fakeTurn: normal(), queue: q, entered: make(chan struct{}), release: make(chan struct{})}
	b := deliveryBackend{turns: make(chan inference.Turn, 1)}
	b.turns <- turn
	h, err := gateway.New(gateway.Config{Backend: b, Tokens: tokens, Metrics: q})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(message(false)))
	r.Header.Set("x-api-key", tokens.Model)
	h.ServeHTTP(failedTerminalWrite{header: make(http.Header)}, r)
	started := time.Now()
	out := request(h, "POST", "/dax-kiro-proxy/hooks/turn-metrics", tokens.UI, "{}")
	if out.Code != 200 || time.Since(started) > 100*time.Millisecond || turn.finished.Load() != 0 || turn.canceled.Load() != 1 || q.Latest() != nil {
		t.Fatal("failed terminal write left pending finalization or published a completion")
	}
}
