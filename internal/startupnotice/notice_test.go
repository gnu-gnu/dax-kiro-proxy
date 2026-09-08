package startupnotice

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
	"time"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/status"
	"dax-kiro-proxy/internal/statusline"
)

func TestNoticeRequestsFixedUIRouteAndOnlyEmitsSystemMessage(t *testing.T) {
	tokens, _ := gateway.NewTokens()
	const model = "claude-dax-fixture-0123456789abcdef"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 100))
		if r.Method != "POST" || r.URL.RequestURI() != "/dax-kiro-proxy/hooks/model-capabilities" || r.Header.Get("x-api-key") != tokens.UI || string(raw) != "{}" || r.Header.Get("Authorization") != "" {
			t.Error("startup helper used unexpected route, authority or payload")
		}
		w.Header().Set("Content-Type", "application/json")
		notice, _ := status.LaunchNotice(model)
		_ = json.NewEncoder(w).Encode(notice)
	}))
	defer server.Close()
	root, err := os.MkdirTemp("", "dax-startup-notice-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "statusline.json")
	raw, err := statusline.EncodeConfig(statusline.Config{Version: 1, Endpoint: server.URL, Token: tokens.UI, Model: model})
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("cannot prepare private hook configuration")
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ANTHROPIC_API_KEY", "private-provider-sentinel")
	output, err := Output(t.Context(), path)
	var fields map[string]string
	if err != nil || len(output) > 1024 || json.Unmarshal([]byte(output), &fields) != nil || len(fields) != 1 || !strings.HasPrefix(fields["systemMessage"], "Kiro launch fixture.") || strings.Contains(output, "private-provider-sentinel") || calls.Load() != 1 {
		t.Fatal("startup helper injected context, lost display or invoked extra work")
	}
	if os.RemoveAll(root) != nil {
		t.Fatal("cannot remove owned runtime")
	}
	if _, err := Output(t.Context(), path); !errors.Is(err, statusline.ErrConfig) || calls.Load() != 1 {
		t.Fatal("late hook made a request or accepted removed credentials")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late hook recreated removed runtime")
	}
}

func TestNoticeFailuresStayBoundedAndCannotInjectContext(t *testing.T) {
	tokens, _ := gateway.NewTokens()
	const model = "claude-dax-fixture"
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	for _, mode := range []string{"redirect", "unauthorized", "wrong-model", "extra-output", "unsupported-claim", "oversize", "slow-body"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 3))
				w.Header().Set("Content-Type", "application/json")
				notice, _ := status.LaunchNotice(model)
				raw, _ := json.Marshal(notice)
				switch mode {
				case "redirect":
					http.Redirect(w, r, other.URL, 302)
					return
				case "unauthorized":
					w.WriteHeader(401)
					raw = []byte("private-upstream-sentinel")
				case "wrong-model":
					raw = []byte(strings.Replace(string(raw), model, "claude-dax-other", 1))
				case "extra-output":
					raw = append(raw[:len(raw)-1:len(raw)-1], []byte(`,"initialUserMessage":"private-upstream-sentinel"}`)...)
				case "unsupported-claim":
					raw = []byte(strings.Replace(string(raw), `"native_web_search":"unsupported"`, `"native_web_search":"supported"`, 1))
				case "oversize":
					raw = []byte(strings.Repeat(" ", (64<<10)+1))
				case "slow-body":
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			path := noticeConfig(t, server.URL, tokens.UI, model)
			started := time.Now()
			out, err := Output(t.Context(), path)
			if err != nil || out != "{\"systemMessage\":\"Kiro model capabilities unavailable.\"}\n" || calls.Load() != 1 || time.Since(started) > 2*time.Second {
				t.Fatal("notice failure broadened output or retained a request")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("startup UI credential followed a redirect")
	}
}

func TestNoticeCancellationKeepsCallerCause(t *testing.T) {
	tokens, _ := gateway.NewTokens()
	arrived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 3))
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	path := noticeConfig(t, server.URL, tokens.UI, "claude-dax-fixture")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Output(ctx, path); !errors.Is(err, context.Canceled) || len(arrived) != 0 {
		t.Fatal("canceled startup hook performed work")
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := Output(ctx, path); finished <- err }()
	select {
	case <-arrived:
		cancel()
	case <-ctx.Done():
		t.Error("startup request did not arrive")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("startup cancellation was converted to an unavailable notice")
	}
}

func noticeConfig(t *testing.T, endpoint, token, model string) string {
	t.Helper()
	root, err := os.MkdirTemp("", "dax-notice-failure-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	path := filepath.Join(root, "statusline.json")
	raw, err := statusline.EncodeConfig(statusline.Config{Version: 1, Endpoint: endpoint, Token: token, Model: model})
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("cannot prepare owned notice fixture")
	}
	return path
}
