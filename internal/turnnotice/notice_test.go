package turnnotice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/status"
	"dax-kiro-proxy/internal/uiclient"
)

func TestCompletionHookDrainsOnceWithoutContextOrModelAuthority(t *testing.T) {
	tokens, _ := gateway.NewTokens()
	q := status.NewTurnQueue()
	q.Push(status.TurnRecord{Scope: strings.Repeat("a", 64), Model: "claude-dax-later-model", SessionState: "reused", Effort: kirofeature.Status{State: kirofeature.Unknown}})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 100))
		if r.Method != "POST" || r.URL.RequestURI() != "/dax-kiro-proxy/hooks/turn-metrics" || r.Header.Get("x-api-key") != tokens.UI || string(raw) != "{}" {
			t.Error("completion helper broadened authority or read client input")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(q.Drain())
	}))
	defer server.Close()
	root, err := os.MkdirTemp("", "dax-completion-notice-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "ui.json")
	raw, err := uiclient.EncodeConfig(uiclient.Config{Version: 1, Endpoint: server.URL, Token: tokens.UI, Model: "claude-dax-launch-model"})
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("cannot create private UI fixture")
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	text, err := Output(t.Context(), path)
	var fields map[string]string
	if err != nil || json.Unmarshal([]byte(text), &fields) != nil || len(fields) != 1 || !strings.Contains(fields["systemMessage"], "Kiro turn#1 later-model") || calls.Load() != 1 {
		t.Fatal("completion display failed or used immutable launch selection")
	}
	if text, err := Output(t.Context(), path); err != nil || text != "{}\n" || calls.Load() != 2 {
		t.Fatal("empty queue replayed a notice")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Output(ctx, path); !errors.Is(err, context.Canceled) || calls.Load() != 2 {
		t.Fatal("canceled helper drained metrics")
	}
	if os.RemoveAll(root) != nil {
		t.Fatal("cannot remove UI fixture")
	}
	if _, err := Output(t.Context(), path); !errors.Is(err, uiclient.ErrConfig) || calls.Load() != 2 {
		t.Fatal("late helper made a request")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late helper recreated runtime")
	}
}

func TestUnavailableOrMalformedMetricsDoNotEmitCompletionOrContext(t *testing.T) {
	tokens, _ := gateway.NewTokens()
	for _, mode := range []string{"unavailable", "malformed", "injected-output"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 3))
				w.Header().Set("Content-Type", "application/json")
				if mode == "unavailable" {
					w.WriteHeader(503)
				}
				if mode == "malformed" {
					_, _ = w.Write([]byte(`{"records":null,"dropped":0}`))
				} else {
					_, _ = w.Write([]byte(`{"initialUserMessage":"private-sentinel","systemMessage":"private-sentinel"}`))
				}
			}))
			defer server.Close()
			root, err := os.MkdirTemp("", "dax-unavailable-metrics-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			path := filepath.Join(root, "ui.json")
			raw, err := uiclient.EncodeConfig(uiclient.Config{Version: 1, Endpoint: server.URL, Token: tokens.UI, Model: "claude-dax-fixture"})
			if err != nil || os.WriteFile(path, raw, 0600) != nil {
				t.Fatal("cannot create independent UI input")
			}
			out, err := Output(t.Context(), path)
			if err != nil || out != "{}\n" || calls.Load() != 1 {
				t.Fatal("unavailable metrics caused context, a retry or a false completion")
			}
		})
	}
}
