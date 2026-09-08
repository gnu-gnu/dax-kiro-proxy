package gateway_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
)

type scheduledStep struct {
	at    time.Duration
	event inference.Event
	err   error
}
type scheduledTurn struct {
	origin                    time.Time
	steps                     []scheduledStep
	index, finished, canceled int
}

func (t *scheduledTurn) Model() string { return "claude-dax-fixture" }
func (t *scheduledTurn) Next(ctx context.Context) (inference.Event, error) {
	if t.index == len(t.steps) {
		<-ctx.Done()
		return inference.Event{}, ctx.Err()
	}
	step := t.steps[t.index]
	timer := time.NewTimer(time.Until(t.origin.Add(step.at)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return inference.Event{}, ctx.Err()
	case <-timer.C:
		t.index++
		return step.event, step.err
	}
}
func (t *scheduledTurn) Finish() { t.finished++ }
func (t *scheduledTurn) Cancel() { t.canceled++ }

type scheduledBackend struct{ turn *scheduledTurn }

func (b scheduledBackend) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	b.turn.origin = time.Now()
	return b.turn, nil
}
func (scheduledBackend) Models(context.Context) ([]inference.Model, error) { return nil, io.EOF }
func TestPingsPreserveCompletionAndDoNotSatisfyTurnDeadlines(t *testing.T) {
	for _, kind := range []string{"complete", "first-deadline", "total-deadline", "auth"} {
		t.Run(kind, func(t *testing.T) {
			turn := &scheduledTurn{}
			first, total := 200*time.Millisecond, 600*time.Millisecond
			switch kind {
			case "complete":
				turn.steps = []scheduledStep{{at: 80 * time.Millisecond, event: inference.Event{Kind: inference.Text, Text: "visible answer"}}, {at: 240 * time.Millisecond, event: inference.Event{Kind: inference.End, StopReason: "end_turn"}}}
			case "first-deadline":
				first = 120 * time.Millisecond
			case "total-deadline":
				total = 300 * time.Millisecond
				turn.steps = []scheduledStep{{at: 80 * time.Millisecond, event: inference.Event{Kind: inference.Text, Text: "partial answer"}}}
			case "auth":
				turn.steps = []scheduledStep{{at: 100 * time.Millisecond, err: acp.ErrAuthentication}}
			}
			h, err := gateway.New(gateway.Config{Tokens: tokens, Backend: scheduledBackend{turn}, FirstEventTimeout: first, TurnTimeout: total, KeepAliveInterval: 20 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			w := request(h, "POST", "/v1/messages", tokens.Model, message(true))
			names, data := events(t, w.Body.String())
			if w.Code != 200 || len(names) < 2 || names[0] != "message_start" || strings.Count(w.Body.String(), "event: message_start\n") != 1 {
				t.Fatal("heartbeat did not keep one ordered message")
			}
			pings := 0
			for i, name := range names {
				if name == "ping" {
					pings++
					if data[i]["type"] != "ping" || len(data[i]) != 1 {
						t.Fatal("invalid ping envelope")
					}
				}
			}
			if pings < 1 {
				t.Fatal("silent stream had no keepalive")
			}
			switch kind {
			case "complete":
				if names[len(names)-1] != "message_stop" || turn.finished != 1 || turn.canceled != 0 || !strings.Contains(w.Body.String(), "visible answer") {
					t.Fatal("ping changed successful delivery")
				}
			case "first-deadline", "total-deadline":
				want := "first model event"
				if kind == "total-deadline" {
					want = "turn deadline"
				}
				if names[len(names)-1] != "error" || !strings.Contains(w.Body.String(), want) || turn.finished != 0 || turn.canceled != 1 {
					t.Fatal("pings extended or reclassified the model deadline")
				}
			case "auth":
				if names[len(names)-1] != "message_stop" || strings.Contains(w.Body.String(), "event: error") || !strings.Contains(w.Body.String(), "kiro-cli login") || w.Result().Trailer.Get(gateway.AuthFallbackHeader) != "1" {
					t.Fatal("authentication fallback failed after keepalive commitment")
				}
			}
		})
	}
}
