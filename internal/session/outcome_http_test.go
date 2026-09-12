package session_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/session"
)

// Hold the successful final write after its bytes are observable, before the handler can Finish.
type heldToolWrite struct {
	*httptest.ResponseRecorder
	streaming bool
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (w *heldToolWrite) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	terminal := !w.streaming || bytes.Contains(p, []byte(`"type":"message_stop"`))
	if err == nil && terminal {
		w.once.Do(func() {
			close(w.entered)
			<-w.release
		})
	}
	return n, err
}

func TestToolOutcomeSurvivesFinalHTTPWriteBeforeFinish(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		streaming, freshBeforeFinish bool
	}{
		{"buffered-retained", false, false}, {"streaming-retained", true, false},
		{"buffered-recovered", false, true}, {"streaming-recovered", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streaming := tc.streaming
			d := toolDriver(t, "chat-tools-restart", 500*time.Millisecond)
			h, err := gateway.New(gateway.Config{Tokens: gateway.Tokens{Model: strings.Repeat("m", 43), UI: strings.Repeat("u", 43)}, Backend: d, TurnTimeout: 5 * time.Second, FirstEventTimeout: 3 * time.Second})
			if err != nil {
				t.Fatal("gateway fixture preparation failed")
			}
			original := toolRequest(t)
			body, err := json.Marshal(map[string]any{"model": original.Model, "max_tokens": 128, "stream": streaming,
				"messages": []any{map[string]any{"role": "user", "content": "request one synthetic tool"}}, "tools": original.Tools})
			if err != nil {
				t.Fatal("request fixture encoding failed")
			}
			request := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(body))
			request.Header.Set("x-api-key", strings.Repeat("m", 43))
			request.Header.Set("Content-Type", "application/json")
			writer := &heldToolWrite{ResponseRecorder: httptest.NewRecorder(), streaming: streaming, entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			unblock := func() { release.Do(func() { close(writer.release) }) }
			finished := make(chan struct{})
			go func() { h.ServeHTTP(writer, request); close(finished) }()
			t.Cleanup(func() {
				unblock()
				select {
				case <-finished:
				case <-time.After(6 * time.Second):
					t.Error("held HTTP handler did not join")
				}
			})
			select {
			case <-writer.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("final response write was not observed")
			}
			blocks, reason := responseBlocks(t, writer.ResponseRecorder, streaming)
			if writer.Code != 200 || reason != "tool_use" || len(blocks) != 2 || blocks[0].Text == nil || blocks[1].Type != "tool_use" {
				t.Fatal("held write did not contain a complete tool response")
			}
			var pid int
			if n, _ := fmt.Sscanf(*blocks[0].Text, "first process %d", &pid); n != 1 || pid <= 1 {
				t.Fatal("owned process witness missing")
			}
			next := followup(t, original, *blocks[0].Text, []anthropic.ToolUse{{ID: blocks[1].ID, Name: blocks[1].Name, Input: blocks[1].Input}})
			waitCtx, stopWait := context.WithTimeout(t.Context(), 20*time.Millisecond)
			_, waitErr := d.Start(waitCtx, next)
			stopWait()
			if !errors.Is(waitErr, context.DeadlineExceeded) || d.State() != session.Prompting {
				t.Fatal("result was admitted before finalization or canceled its owner")
			}
			until := time.Now().Add(3 * time.Second)
			for d.State() != session.Unstarted && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if d.State() != session.Unstarted || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
				t.Fatal("expired handoff retained its owned process")
			}
			for range 2 {
				if _, err := d.Start(context.Background(), next); !errors.Is(err, relay.ErrTimeout) {
					t.Fatal("matching result lost the expired handoff outcome")
				}
			}
			for _, mutate := range []func(*anthropic.Request){
				func(r *anthropic.Request) {
					r.Messages[0].Content[0].Text = "different fixture history"
					r.Messages[0].Content[0].Raw = nil
				},
				func(r *anthropic.Request) { r.Identity.Session = "other-owner" },
				func(r *anthropic.Request) { r.Model = "other-model" },
				func(r *anthropic.Request) { r.Messages = r.Messages[1:] },
				func(r *anthropic.Request) {
					r.Messages[2].Content = append(r.Messages[2].Content, r.Messages[2].Content[0])
				},
				func(r *anthropic.Request) {
					r.Messages[2].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"toolu_unknown","content":"unrelated result","is_error":true}`)
				},
			} {
				bad := cloneInterruptionRequest(next)
				mutate(bad)
				if _, err := d.Start(t.Context(), bad); !errors.Is(err, inference.ErrRequest) {
					t.Fatal("retired outcome matched unrelated or incomplete ownership")
				}
			}
			if _, err := d.Start(t.Context(), next); !errors.Is(err, relay.ErrTimeout) {
				t.Fatal("invalid retry consumed the retained outcome")
			}
			finishOld := func() {
				unblock()
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("late finalization did not return")
				}
			}
			if !tc.freshBeforeFinish {
				finishOld()
				if _, err := d.Start(t.Context(), next); !errors.Is(err, relay.ErrTimeout) {
					t.Fatal("late Finish overwrote the retained outcome")
				}
			}
			fresh := cloneInterruptionRequest(next)
			fresh.Messages[2].Content = append(fresh.Messages[2].Content, anthropic.Block{Type: "text", Text: "Next independent question."})
			recovered, err := d.Start(t.Context(), fresh)
			if err != nil {
				t.Fatal("matching all-denial history could not start a fresh question")
			}
			defer recovered.Cancel()
			if tc.freshBeforeFinish {
				finishOld()
			}
			if d.State() != session.Prompting {
				t.Fatal("old response finalization replaced fresh turn ownership")
			}
			text, err := collect(t.Context(), recovered)
			var observation observedPrompt
			if err != nil || json.Unmarshal([]byte(text), &observation) != nil || observation.PID <= 1 || observation.PID == pid || observation.Count != 1 ||
				!strings.Contains(text, "Next independent question.") || !strings.Contains(text, "synthetic tool result") || !strings.Contains(text, "request one synthetic tool") {
				t.Fatal("recovery did not complete in a fresh process")
			}
			recovered.Finish()
			if d.State() != session.Idle {
				t.Fatal("recovered response did not become idle")
			}
			if _, err := d.Start(t.Context(), next); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("retired batch remained reusable after a fresh turn")
			}
			if d.Close() != nil || !errors.Is(syscall.Kill(-observation.PID, 0), syscall.ESRCH) {
				t.Fatal("driver cleanup failed")
			}
		})
	}
}
