package session_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/session"
)

func TestHTTPThroughIndependentACPProcess(t *testing.T) {
	for _, mode := range []string{"chat", "chat-auth", "chat-before", "chat-slow"} {
		for _, stream := range []bool{false, true} {
			d := driver(t, mode)
			tokens, err := gateway.NewTokens()
			if err != nil {
				t.Fatal(err)
			}
			first, total := 500*time.Millisecond, 2*time.Second
			if mode == "chat-before" {
				first, total = 100*time.Millisecond, time.Second
			}
			if mode == "chat-slow" {
				total = 800 * time.Millisecond
			}
			h, err := gateway.New(gateway.Config{Backend: d, Tokens: tokens, FirstEventTimeout: first, TurnTimeout: total, KeepAliveInterval: 10 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 128, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}})
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(payload)))
			r.Header.Set("x-api-key", tokens.Model)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			switch mode {
			case "chat":
				if w.Code != 200 || !strings.Contains(w.Body.String(), "stone") {
					t.Fatalf("text path %t: %d %s", stream, w.Code, w.Body.String())
				}
				if d.State() != session.Idle {
					t.Fatal("successful ACP turn not idle")
				}
			case "chat-auth":
				if w.Code != 200 || !strings.Contains(w.Body.String(), "kiro-cli login") {
					t.Fatal("auth path did not complete normally")
				}
			case "chat-before":
				if !strings.Contains(w.Body.String(), "first model event") {
					t.Fatalf("first deadline not propagated: %s", w.Body.String())
				}
			case "chat-slow":
				if !strings.Contains(w.Body.String(), "turn deadline") {
					t.Fatalf("turn deadline not propagated: %s", w.Body.String())
				}
			}
			if mode != "chat" && d.State() != session.Unstarted {
				t.Fatal("failed HTTP turn retained ACP state")
			}
			if stream && (mode == "chat-before" || mode == "chat-slow") && !strings.Contains(w.Body.String(), "event: ping\n") {
				t.Fatal("silent ACP wait did not emit a keepalive")
			}
		}
	}
}

func TestOwnedHTTPServerShutdownJoinsIndependentACPGroup(t *testing.T) {
	d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Directory: t.TempDir(), Args: []string{"chat-slow-pid"}, ClientInfo: acp.Info{Name: "independent-server-fixture", Version: "1"}, Limits: acp.Limits{GracePeriod: 50 * time.Millisecond, TermPeriod: 50 * time.Millisecond, KillPeriod: time.Second}}, TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	s, err := gateway.StartServer(t.Context(), gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: d, FirstEventTimeout: 3 * time.Second, TurnTimeout: 5 * time.Second}, ShutdownTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	payload, _ := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 128, "stream": true, "messages": []any{map[string]any{"role": "user", "content": "independent shutdown fixture"}}})
	r, _ := http.NewRequestWithContext(t.Context(), "POST", s.URL()+"/v1/messages", strings.NewReader(string(payload)))
	r.Header.Set("x-api-key", tokens.Model)
	r.Header.Set("Content-Type", "application/json")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("independent ACP request status %d", response.StatusCode)
	}
	reader := bufio.NewScanner(response.Body)
	pid := 0
	for reader.Scan() {
		line := reader.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type  string
			Delta struct{ Text string }
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			t.Fatal("invalid synthetic SSE")
		}
		if event.Type == "content_block_delta" {
			pid, err = strconv.Atoi(strings.TrimPrefix(event.Delta.Text, "owned-pid:"))
			if err != nil || pid <= 1 {
				t.Fatal("independent ACP PID was not observed")
			}
			break
		}
	}
	if pid <= 1 || d.State() != session.Prompting {
		t.Fatal("ACP fixture did not remain in its active turn")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if d.State() != session.Unstarted || s.Stats().Handlers != 0 || s.Stats().Connections != 0 {
		t.Fatal("HTTP shutdown retained a partial ACP session")
	}
	if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
		t.Fatal("HTTP shutdown returned before its ACP group was gone")
	}
}
