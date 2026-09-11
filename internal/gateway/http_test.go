package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/schemacheck"
)

var tokens = gateway.Tokens{Model: strings.Repeat("m", 43), UI: strings.Repeat("u", 43)}

type step struct {
	event inference.Event
	err   error
	wait  bool
}
type fakeTurn struct {
	steps              []step
	index              int
	canceled, finished atomic.Int32
}

func (t *fakeTurn) Model() string { return "claude-dax-fixture" }
func (t *fakeTurn) Next(ctx context.Context) (inference.Event, error) {
	if t.index >= len(t.steps) {
		return inference.Event{}, io.EOF
	}
	s := t.steps[t.index]
	t.index++
	if s.wait {
		<-ctx.Done()
		return inference.Event{}, ctx.Err()
	}
	return s.event, s.err
}
func (t *fakeTurn) Cancel() { t.canceled.Add(1) }
func (t *fakeTurn) Finish() { t.finished.Add(1) }

type fakeBackend struct {
	turn   *fakeTurn
	err    error
	starts atomic.Int32
}

func (b *fakeBackend) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	b.starts.Add(1)
	return b.turn, b.err
}
func (b *fakeBackend) Models(context.Context) ([]inference.Model, error) {
	return []inference.Model{{ID: "claude-dax-fixture", Name: "Fixture"}}, nil
}
func normal() *fakeTurn {
	return &fakeTurn{steps: []step{{event: inference.Event{Kind: inference.Text, Text: "birch "}}, {event: inference.Event{Kind: inference.Text, Text: "stone"}}, {event: inference.Event{Kind: inference.End, StopReason: "end_turn"}}}}
}
func handler(t *testing.T, b *fakeBackend, change func(*gateway.Config)) http.Handler {
	t.Helper()
	cfg := gateway.Config{Tokens: tokens, Backend: b, FirstEventTimeout: time.Second, TurnTimeout: 2 * time.Second}
	if change != nil {
		change(&cfg)
	}
	h, err := gateway.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func message(stream bool) string {
	if stream {
		return `{"model":"claude-dax-fixture","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"synthetic prompt"}]}`
	}
	return `{"model":"claude-dax-fixture","max_tokens":128,"messages":[{"role":"user","content":"synthetic prompt"}]}`
}
func request(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("x-api-key", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func events(t *testing.T, body string) ([]string, []map[string]any) {
	t.Helper()
	var names []string
	var data []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			names = append(names, strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") {
			var item map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &item); err != nil {
				t.Fatal(err)
			}
			data = append(data, item)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return names, data
}

func TestRouteAuthentication(t *testing.T) {
	b := &fakeBackend{turn: normal()}
	h := handler(t, b, nil)
	for _, path := range []string{"/v1/messages", "/messages", "/v1/models"} {
		method := "POST"
		if path == "/v1/models" {
			method = "GET"
		}
		for _, token := range []string{"", "wrong", tokens.UI} {
			w := request(h, method, path, token, message(false))
			if w.Code != 401 || strings.Contains(w.Body.String(), tokens.Model) {
				t.Fatalf("auth %s: %d", path, w.Code)
			}
		}
	}
	if b.starts.Load() != 0 {
		t.Fatal("unauthorized request reached backend")
	}
	for _, tc := range []struct {
		path, token string
		status      int
	}{{"/dax-kiro-proxy/status/usage", tokens.UI, 200}, {"/dax-kiro-proxy/status/usage", tokens.Model, 401}, {"/dax-kiro-proxy/status/usage/extra", tokens.UI, 404}, {"/dax-kiro-proxy/status/../v1/models", tokens.UI, 404}} {
		w := request(h, "GET", tc.path, tc.token, "")
		if w.Code != tc.status {
			t.Fatalf("UI route %s: %d", tc.path, w.Code)
		}
	}
	if w := request(h, "GET", "/health", "", ""); w.Code != 200 {
		t.Fatal("health")
	}
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+tokens.Model)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("bearer credential rejected")
	}
	r.Header.Set("x-api-key", tokens.UI)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("conflicting auth accepted")
	}
}

func TestHTTPValidationBeforeBackend(t *testing.T) {
	b := &fakeBackend{turn: normal()}
	h := handler(t, b, nil)
	for _, tc := range []struct {
		body   string
		status int
	}{{"[]", 400}, {"{", 400}, {`{"messages":[]}`, 400}, {strings.Repeat("x", (16<<20)+1), 413}} {
		w := request(h, "POST", "/v1/messages", tokens.Model, tc.body)
		if w.Code != tc.status {
			t.Errorf("body (%d bytes): %d", len(tc.body), w.Code)
		}
	}
	if b.starts.Load() != 0 {
		t.Fatal("invalid request reached backend")
	}
}

func TestTextResponseAndExactSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streaming"}[stream], func(t *testing.T) {
			turn := normal()
			h := handler(t, &fakeBackend{turn: turn}, nil)
			w := request(h, "POST", "/v1/messages", tokens.Model, message(stream))
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if stream {
				names, data := events(t, w.Body.String())
				want := "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop"
				if strings.Join(names, ",") != want {
					t.Fatalf("events: %v", names)
				}
				var text string
				for _, e := range data {
					if e["type"] == "content_block_delta" {
						text += e["delta"].(map[string]any)["text"].(string)
					}
				}
				if text != "birch stone" {
					t.Fatalf("text %q", text)
				}
				usage := data[0]["message"].(map[string]any)["usage"].(map[string]any)
				if usage["input_tokens"] != float64(0) || usage["output_tokens"] != float64(0) {
					t.Fatal("invented provider usage")
				}
			} else {
				var response map[string]any
				if json.Unmarshal(w.Body.Bytes(), &response) != nil {
					t.Fatal("invalid JSON")
				}
				if response["role"] != "assistant" || response["type"] != "message" || response["stop_reason"] != "end_turn" || response["content"].([]any)[0].(map[string]any)["text"] != "birch stone" {
					t.Fatalf("message: %v", response)
				}
			}
			if turn.finished.Load() != 1 || turn.canceled.Load() != 0 {
				t.Fatal("successful turn not completed exactly once")
			}
		})
	}
}

func TestEarlyAndLateAuthenticationFallback(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, late := range []bool{false, true} {
			turn := &fakeTurn{steps: []step{{err: acp.ErrAuthentication}}}
			if late {
				turn.steps = append([]step{{event: inference.Event{Kind: inference.Text, Text: "partial "}}}, turn.steps...)
			}
			w := request(handler(t, &fakeBackend{turn: turn}, nil), "POST", "/v1/messages", tokens.Model, message(stream))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "kiro-cli login") {
				t.Fatalf("auth fallback %t/%t: %d %s", stream, late, w.Code, w.Body.String())
			}
			response := w.Result()
			if stream && late {
				if response.Trailer.Get(gateway.AuthFallbackHeader) != "1" {
					t.Fatalf("late marker missing: %v", response.Trailer)
				}
			} else if response.Header.Get(gateway.AuthFallbackHeader) != "1" {
				t.Fatal("early fallback header missing")
			}
			if stream {
				names, _ := events(t, w.Body.String())
				if names[0] != "message_start" || names[len(names)-1] != "message_stop" || strings.Contains(strings.Join(names, ","), "error") {
					t.Fatalf("invalid auth stream: %v", names)
				}
			}
			if turn.canceled.Load() != 1 || turn.finished.Load() != 0 {
				t.Fatal("auth-failed backend reused")
			}
		}
	}
}

func TestBackendFailureTimingAndPrivacy(t *testing.T) {
	for _, late := range []bool{false, true} {
		turn := &fakeTurn{steps: []step{{err: errors.New("raw prompt and tool output sentinel")}}}
		if late {
			turn.steps = append([]step{{event: inference.Event{Kind: inference.Text, Text: "partial "}}}, turn.steps...)
		}
		w := request(handler(t, &fakeBackend{turn: turn}, nil), "POST", "/v1/messages", tokens.Model, message(true))
		if strings.Contains(w.Body.String(), "sentinel") {
			t.Fatal("backend details leaked")
		}
		if late {
			names, _ := events(t, w.Body.String())
			if names[len(names)-1] != "error" {
				t.Fatalf("missing stream error: %v", names)
			}
		} else if w.Code != 502 {
			t.Fatalf("early failure %d", w.Code)
		}
		if turn.canceled.Load() != 1 {
			t.Fatal("failed turn not discarded")
		}
	}
}

func TestFirstAndTotalDeadlineDiagnostics(t *testing.T) {
	for _, late := range []bool{false, true} {
		turn := &fakeTurn{steps: []step{{wait: true}}}
		if late {
			turn.steps = append([]step{{event: inference.Event{Kind: inference.Text, Text: "partial "}}}, turn.steps...)
		}
		h := handler(t, &fakeBackend{turn: turn}, func(c *gateway.Config) {
			c.FirstEventTimeout = 20 * time.Millisecond
			c.TurnTimeout = 60 * time.Millisecond
		})
		w := request(h, "POST", "/v1/messages", tokens.Model, message(true))
		want := "first model event"
		if late {
			want = "turn deadline"
		}
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("deadline not distinguished: %s", w.Body.String())
		}
		if turn.canceled.Load() != 1 {
			t.Fatal("timeout did not cancel")
		}
	}
}

func TestDisconnectCancelsStreamingTurn(t *testing.T) {
	turn := &fakeTurn{steps: []step{{event: inference.Event{Kind: inference.Text, Text: "visible"}}, {wait: true}}}
	server := httptest.NewServer(handler(t, &fakeBackend{turn: turn}, nil))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/messages", strings.NewReader(message(true)))
	r.Header.Set("x-api-key", tokens.Model)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for turn.canceled.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if turn.canceled.Load() != 1 {
		t.Fatal("disconnect left turn alive")
	}
}

func TestLoopbackBindPolicy(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", "example.com:0"} {
		if listener, err := gateway.Listen(address, false); err == nil {
			_ = listener.Close()
			t.Fatalf("unsafe bind accepted: %s", address)
		}
	}
	listener, err := gateway.Listen("127.0.0.1:0", false)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if !strings.HasPrefix(listener.Addr().String(), "127.0.0.1:") {
		t.Fatal("not loopback")
	}
}

func TestDuplicateCredentialHeadersAndStartFailure(t *testing.T) {
	b := &fakeBackend{err: acp.ErrAuthentication}
	h := handler(t, b, nil)
	for _, name := range []string{"x-api-key", "Authorization"} {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		value := tokens.Model
		if name == "Authorization" {
			value = "Bearer " + value
		}
		r.Header.Add(name, value)
		r.Header.Add(name, value)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("duplicate credentials accepted")
		}
	}
	for _, stream := range []bool{false, true} {
		w := request(h, "POST", "/v1/messages", tokens.Model, message(stream))
		if w.Code != 200 || w.Result().Header.Get(gateway.AuthFallbackHeader) != "1" || !strings.Contains(w.Body.String(), "kiro-cli login") {
			t.Fatal("startup auth failure was not a completion")
		}
	}
}

func TestEncodedOutputBudgetAndTerminalStreamError(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		turn := &fakeTurn{steps: []step{{event: inference.Event{Kind: inference.Text, Text: strings.Repeat("\x00", 1000)}}, {event: inference.Event{Kind: inference.End, StopReason: "end_turn"}}}}
		h := handler(t, &fakeBackend{turn: turn}, func(c *gateway.Config) { c.MaxOutputBytes = 2048 })
		w := request(h, "POST", "/v1/messages", tokens.Model, message(streaming))
		if w.Body.Len() > 2048 {
			t.Fatal("encoded output exceeded byte budget")
		}
		if streaming {
			names, _ := events(t, w.Body.String())
			if names[len(names)-1] != "error" {
				t.Fatalf("output overflow left incomplete stream: %v", names)
			}
		} else if w.Code != 502 {
			t.Fatalf("buffered overflow status %d", w.Code)
		}
		if turn.canceled.Load() != 1 || turn.finished.Load() != 0 {
			t.Fatal("output overflow reused session")
		}
	}
}

type blockedBackend struct {
	started chan struct{}
	turn    *fakeTurn
}

func (b *blockedBackend) Start(ctx context.Context, _ *anthropic.Request) (inference.Turn, error) {
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (b *blockedBackend) Models(context.Context) ([]inference.Model, error) { return nil, nil }
func TestActiveRequestLimitAndDisconnectBeforeFirstEvent(t *testing.T) {
	b := &blockedBackend{started: make(chan struct{})}
	h, err := gateway.New(gateway.Config{Backend: b, Tokens: tokens, MaxActiveRequests: 1, FirstEventTimeout: time.Second, TurnTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(message(false))).WithContext(ctx)
	r.Header.Set("x-api-key", tokens.Model)
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	<-b.started
	w := request(h, "POST", "/messages", tokens.Model, message(false))
	if w.Code != 429 {
		t.Fatalf("overload status %d", w.Code)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("backend startup outlived cancellation")
	}
	turn := &fakeTurn{steps: []step{{wait: true}}}
	handler := handler(t, &fakeBackend{turn: turn}, nil)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	r = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(message(true))).WithContext(ctx)
	r.Header.Set("x-api-key", tokens.Model)
	done = make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("no-event cancellation blocked")
	}
	if turn.canceled.Load() != 1 {
		t.Fatal("no-event disconnect did not discard turn")
	}
}

type failingWriter struct{ header http.Header }

func (w *failingWriter) Header() http.Header       { return w.header }
func (w *failingWriter) WriteHeader(int)           {}
func (w *failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (w *failingWriter) Flush()                    {}
func TestResponseWriteFailureCancels(t *testing.T) {
	for _, stream := range []bool{false, true} {
		turn := normal()
		h := handler(t, &fakeBackend{turn: turn}, nil)
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(message(stream)))
		r.Header.Set("x-api-key", tokens.Model)
		h.ServeHTTP(&failingWriter{header: make(http.Header)}, r)
		if turn.canceled.Load() != 1 || turn.finished.Load() != 0 {
			t.Fatal("failed delivery committed backend state")
		}
	}
}

func TestSchemaCapacityErrorsAreNotRequestErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		kind string
	}{
		{errors.Join(inference.ErrRequest, schemacheck.ErrOverloaded), 429, "overloaded_error"},
		{errors.Join(inference.ErrRequest, schemacheck.ErrWorker), 502, "api_error"},
		{errors.Join(inference.ErrRequest, schemacheck.ErrBudget), 400, "time budget"},
		{errors.Join(inference.ErrRequest, schemacheck.ErrClosed), 502, "api_error"},
	} {
		w := request(handler(t, &fakeBackend{err: tc.err}, nil), "POST", "/messages", tokens.Model, message(false))
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.kind) || strings.Contains(w.Body.String(), "incompatible") {
			t.Fatalf("schema capacity mapped to %d %s", w.Code, w.Body.String())
		}
	}
}
