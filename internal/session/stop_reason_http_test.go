package session_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/session"
)

func TestStoppedPromptHTTPPreservesAnswerAndNextDelta(t *testing.T) {
	for _, reason := range []string{"end_turn", "max_tokens", "refusal", "max_turn_requests"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", reason, stream), func(t *testing.T) {
				witness := filepath.Join(t.TempDir(), "owner")
				d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Args: []string{"stop-peer-" + reason, witness}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "stop-fixture", Version: "1"}}, TurnTimeout: 5 * time.Second})
				if err != nil {
					t.Fatal("cannot prepare completion driver")
				}
				t.Cleanup(func() { _ = d.Close() })
				h, err := gateway.New(gateway.Config{Backend: d, Tokens: gateway.Tokens{Model: strings.Repeat("m", 43), UI: strings.Repeat("u", 43)}, TurnTimeout: 5 * time.Second, FirstEventTimeout: 3 * time.Second})
				if err != nil {
					t.Fatal("cannot prepare completion gateway")
				}
				const question = "Answer the independent completion exercise."
				const answer = "A copper triangle marks the completed first segment."
				const next = "Continue the independent completion exercise."
				body := map[string]any{"model": fixtureClientID, "max_tokens": 128, "stream": stream, "messages": []any{map[string]string{"role": "user", "content": question}}}
				first := invokeStoppedHTTP(t, h, body)
				if first.Code != 200 || d.State() != session.Idle {
					t.Fatal("public prompt completion was turned into a failed session")
				}
				blocks, stop := responseBlocks(t, first, stream)
				want := reason
				if reason == "max_turn_requests" {
					want = "pause_turn"
				}
				if stop != want || len(blocks) != 1 || blocks[0].Text == nil || *blocks[0].Text != answer {
					t.Fatal("HTTP completion changed its answer or limit class")
				}
				readWitness := func() (int, int, bool) {
					t.Helper()
					raw, err := os.ReadFile(witness)
					var facts struct {
						PID, Prompts int
						SecondDelta  bool
					}
					if err != nil || len(raw) > 1024 || json.Unmarshal(raw, &facts) != nil || facts.PID <= 1 {
						t.Fatal("independent process witness missing")
					}
					return facts.PID, facts.Prompts, facts.SecondDelta
				}
				pid, prompts, _ := readWitness()
				if prompts != 1 {
					t.Fatal("completion issued an unrequested prompt")
				}
				body["messages"] = []any{map[string]string{"role": "user", "content": question}, map[string]any{"role": "assistant", "content": blocks}, map[string]string{"role": "user", "content": next}}
				second := invokeStoppedHTTP(t, h, body)
				result, stop := responseBlocks(t, second, stream)
				owner, prompts, delta := readWitness()
				if second.Code != 200 || stop != "end_turn" || len(result) != 1 || result[0].Text == nil || *result[0].Text != "The independent continuation is complete." || d.State() != session.Idle || owner != pid || prompts != 2 || !delta {
					t.Fatal("explicit next question lost history or repeated previous model work")
				}
				if d.Close() != nil || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
					t.Fatal("completion fixture group was not joined")
				}
			})
		}
	}
}

func invokeStoppedHTTP(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal("cannot encode completion fixture request")
	}
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(raw)))
	r.Header.Set("x-api-key", strings.Repeat("m", 43))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
