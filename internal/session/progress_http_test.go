package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/session"
)

func TestIndependentProgressHTTPDeadlinesAndCleanup(t *testing.T) {
	for _, mode := range []string{"thought", "plan", "tool", "none", "total", "cancel"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", mode, stream), func(t *testing.T) {
				witness := filepath.Join(t.TempDir(), "owned-pid")
				d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Args: []string{"progress-peer-" + mode, witness}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "progress-fixture", Version: "1"}, Limits: acp.Limits{GracePeriod: 50 * time.Millisecond, TermPeriod: 50 * time.Millisecond, KillPeriod: time.Second}}, TurnTimeout: 3 * time.Second})
				if err != nil {
					t.Fatal("cannot create independent progress driver")
				}
				t.Cleanup(func() { _ = d.Close() })
				tokens, err := gateway.NewTokens()
				if err != nil {
					t.Fatal("cannot create fixture credentials")
				}
				total := 2 * time.Second
				if mode == "total" {
					total = 700 * time.Millisecond
				}
				h, err := gateway.New(gateway.Config{Backend: d, Tokens: tokens, FirstEventTimeout: 200 * time.Millisecond, TurnTimeout: total, KeepAliveInterval: 40 * time.Millisecond})
				if err != nil {
					t.Fatal("cannot create progress gateway")
				}
				body, _ := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 128, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "Independent progress question."}}})
				r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(body)))
				r.Header.Set("x-api-key", tokens.Model)
				if mode == "cancel" {
					// A disconnected HTTP caller cancels without changing the owned turn deadline.
					ctx, cancel := context.WithCancel(r.Context())
					defer cancel()
					timer := time.AfterFunc(700*time.Millisecond, cancel)
					defer timer.Stop()
					r = r.WithContext(ctx)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				result := w.Body.String()
				wantSuccess := mode == "thought" || mode == "plan" || mode == "tool"
				if wantSuccess {
					if w.Code != 200 || !strings.Contains(result, "Progress fixture answer.") || d.State() != session.Idle {
						t.Fatal("progress failed to preserve delayed completion")
					}
				} else {
					if d.State() != session.Unstarted || strings.Contains(result, "Progress fixture answer.") {
						t.Fatal("failed or canceled turn retained state or produced a final answer")
					}
					if mode == "none" && !strings.Contains(result, "first model event") || mode == "total" && (!strings.Contains(result, "turn deadline") || strings.Contains(result, "first model event")) {
						t.Fatal("progress changed deadline classification")
					}
					if mode == "cancel" && (!errors.Is(r.Context().Err(), context.Canceled) || strings.Contains(result, "first model event") || strings.Contains(result, "event: error")) {
						t.Fatal("progress failed before the caller canceled")
					}
				}
				if strings.Contains(result, "Unpublished") || strings.Contains(result, "observed-fixture-call") || strings.Contains(result, "tool_use") {
					t.Fatal("informational progress crossed into answer content or tool execution")
				}
				if stream && strings.Count(result, "event: ping\n") < 2 {
					t.Fatal("frequent ACP updates starved the heartbeat")
				}
				if err := d.Close(); err != nil {
					t.Fatal("progress driver cleanup failed")
				}
				raw, err := os.ReadFile(witness)
				pid, parseErr := strconv.Atoi(string(raw))
				if err != nil || parseErr != nil || pid <= 1 || !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
					t.Fatal("independent progress process or group was not joined")
				}
			})
		}
	}
}
