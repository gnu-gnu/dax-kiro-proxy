package session_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

// Flush real terminal bytes to the client, then hold the server before its delivery commit.
type terminalFlush struct {
	http.ResponseWriter
	streaming, terminal bool
	entered, release    chan struct{}
	once                sync.Once
}

func (w *terminalFlush) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *terminalFlush) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err == nil && (!w.streaming || bytes.Contains(p, []byte("event: message_stop\n"))) {
		w.terminal = true
	}
	return n, err
}
func (w *terminalFlush) FlushError() error {
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		return err
	}
	if w.terminal {
		var err error
		w.once.Do(func() {
			close(w.entered)
			select {
			case <-w.release:
			case <-time.After(5 * time.Second):
				err = errors.New("owned terminal flush wait expired")
			}
		})
		return err
	}
	return nil
}

func TestImmediateToolResultAfterTerminalBytes(t *testing.T) {
	for _, tc := range []struct {
		name               string
		streaming, managed bool
	}{{"buffered", false, false}, {"streaming", true, false}, {"manager-buffered", false, true}, {"manager-streaming", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			streaming := tc.streaming
			var d interface {
				inference.Backend
				Close() error
			}
			if tc.managed {
				validator, err := schemacheck.New(schemacheck.Config{Executable: relayBinary, Directory: t.TempDir()})
				if err != nil {
					t.Fatal("validator setup failed")
				}
				t.Cleanup(validator.Close)
				cfg := managerConfig(t, "chat-tools")
				cfg.Session.Validator, cfg.Session.RelayExecutable = validator, relayBinary
				cfg.Session.RelayLimits = relay.Limits{ToolTimeout: 4 * time.Second}
				cfg.Session.TurnTimeout = 8 * time.Second
				d, err = session.NewManager(cfg)
				if err != nil {
					t.Fatal("manager setup failed")
				}
				t.Cleanup(func() { _ = d.Close() })
			} else {
				d = toolDriver(t, "chat-tools", 4*time.Second, func(c *session.Config) { c.TurnTimeout = 8 * time.Second })
			}
			h, err := gateway.New(gateway.Config{Backend: d, Tokens: gateway.Tokens{Model: strings.Repeat("m", 43), UI: strings.Repeat("u", 43)}, TurnTimeout: 8 * time.Second, FirstEventTimeout: 3 * time.Second})
			if err != nil {
				t.Fatal("gateway setup failed")
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Owned-Hold") == "1" {
					w = &terminalFlush{ResponseWriter: w, streaming: streaming, entered: entered, release: release}
				}
				h.ServeHTTP(w, r)
			}))
			t.Cleanup(func() { unblock(); s.Close() })
			transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 3}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 6 * time.Second}
			original := toolRequest(t)
			payload := map[string]any{"model": original.Model, "max_tokens": 128, "stream": streaming, "messages": []any{map[string]any{"role": "user", "content": "request one synthetic tool"}}, "tools": original.Tools}
			send := func(value any, hold bool) (*http.Response, error) {
				body, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				r, err := http.NewRequestWithContext(t.Context(), "POST", s.URL+"/v1/messages", bytes.NewReader(body))
				if err != nil {
					return nil, err
				}
				r.Header.Set("x-api-key", strings.Repeat("m", 43))
				r.Header.Set("Content-Type", "application/json")
				if hold {
					r.Header.Set("X-Owned-Hold", "1")
				}
				return client.Do(r)
			}
			first, err := send(payload, true)
			if err != nil {
				t.Fatal("initial HTTP request failed")
			}
			defer first.Body.Close()
			var blocks []anthropic.ResponseBlock
			var reason string
			if !streaming {
				var response anthropic.Response
				if json.NewDecoder(io.LimitReader(first.Body, 64<<10)).Decode(&response) != nil || response.StopReason == nil {
					t.Fatal("initial JSON response incomplete")
				}
				blocks, reason = response.Content, *response.StopReason
			} else {
				scan := bufio.NewScanner(io.LimitReader(first.Body, 64<<10))
				var wire strings.Builder
				terminal := false
				for scan.Scan() {
					line := scan.Text()
					wire.WriteString(line + "\n")
					terminal = terminal || line == "event: message_stop"
					if terminal && line == "" {
						break
					}
				}
				if scan.Err() != nil || !terminal {
					t.Fatal("initial SSE response incomplete")
				}
				w := httptest.NewRecorder()
				w.Body.WriteString(wire.String())
				blocks, reason = responseBlocks(t, w, true)
			}
			if first.StatusCode != 200 || reason != "tool_use" || len(blocks) != 2 || blocks[1].Type != "tool_use" {
				t.Fatal("initial tool handoff incomplete")
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("terminal flush did not enter")
			}
			payload["messages"] = []any{map[string]any{"role": "user", "content": "request one synthetic tool"}, map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": blocks[1].ID, "content": "owned immediate result", "is_error": true}}}}
			type result struct {
				response *http.Response
				err      error
			}
			next := make(chan result, 1)
			go func() { response, err := send(payload, false); next <- result{response, err} }()
			var got result
			select {
			case got = <-next:
				unblock()
				if got.response != nil {
					got.response.Body.Close()
					t.Fatalf("immediate result returned before delivery commit: status=%d", got.response.StatusCode)
				}
				t.Fatal("immediate result failed before delivery commit")
			case <-time.After(40 * time.Millisecond):
			}
			unblock()
			select {
			case got = <-next:
			case <-time.After(4 * time.Second):
				t.Fatal("immediate result did not resume after delivery commit")
			}
			if got.err != nil || got.response == nil {
				t.Fatal("immediate result HTTP request failed")
			}
			defer got.response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(got.response.Body, 64<<10))
			if err != nil || got.response.StatusCode != 200 {
				t.Fatal("immediate result did not complete successfully")
			}
			w := httptest.NewRecorder()
			w.Body.Write(body)
			answer, stop := responseBlocks(t, w, streaming)
			if stop != "end_turn" || len(answer) != 1 || answer[0].Text == nil || !strings.Contains(*answer[0].Text, `"promptCount":1`) || !strings.Contains(*answer[0].Text, "owned immediate result") {
				t.Fatal("immediate result did not resume the original prompt exactly")
			}
			if d.Close() != nil {
				t.Fatal("owned driver cleanup failed")
			}
		})
	}
}
