package gateway_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
)

func TestProgressPreservesAnswerHeartbeatAndTotalDeadline(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, finish := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream-%t/finish-%t", stream, finish), func(t *testing.T) {
				turn := &scheduledTurn{}
				for at := time.Duration(0); at < 450*time.Millisecond; at += 10 * time.Millisecond {
					turn.steps = append(turn.steps, scheduledStep{at: at, event: inference.Event{Kind: inference.Progress}})
				}
				total := 350 * time.Millisecond
				if finish {
					total = time.Second
					turn.steps = append(turn.steps,
						scheduledStep{at: 450 * time.Millisecond, event: inference.Event{Kind: inference.Text, Text: "only answer"}},
						scheduledStep{at: 460 * time.Millisecond, event: inference.Event{Kind: inference.End, StopReason: "end_turn"}})
				}
				h, err := gateway.New(gateway.Config{Tokens: tokens, Backend: scheduledBackend{turn}, FirstEventTimeout: 200 * time.Millisecond, TurnTimeout: total, KeepAliveInterval: 40 * time.Millisecond})
				if err != nil {
					t.Fatal(err)
				}
				w := request(h, "POST", "/v1/messages", tokens.Model, message(stream))
				body := w.Body.String()
				if finish {
					if w.Code != 200 || !strings.Contains(body, "only answer") || turn.finished != 1 || turn.canceled != 0 {
						t.Fatal("validated progress did not preserve the later answer")
					}
				} else if !strings.Contains(body, "turn deadline") || strings.Contains(body, "first model event") || turn.finished != 0 || turn.canceled != 1 {
					t.Fatal("progress extended or reclassified the original turn deadline")
				}
				if stream {
					names, _ := events(t, body)
					pings, blocks := 0, 0
					for _, name := range names {
						if name == "ping" {
							pings++
						}
						if name == "content_block_start" {
							blocks++
						}
					}
					wantBlocks := 0
					if finish {
						wantBlocks = 1
					}
					if pings < 3 || blocks != wantBlocks {
						t.Fatalf("silent progress changed heartbeat/content: pings=%d blocks=%d", pings, blocks)
					}
				}
			})
		}
	}
}

func TestProgressRejectsUnexpectedPayload(t *testing.T) {
	for _, event := range []inference.Event{
		{Kind: inference.Progress, Text: "hidden content"},
		{Kind: inference.Progress, StopReason: "end_turn"},
		{Kind: inference.Progress, Tools: []anthropic.ToolUse{{ID: "unowned"}}},
	} {
		for _, stream := range []bool{false, true} {
			turn := &fakeTurn{steps: []step{{event: event}}}
			w := request(handler(t, &fakeBackend{turn: turn}, nil), "POST", "/v1/messages", tokens.Model, message(stream))
			if !strings.Contains(w.Body.String(), "Unsupported Kiro response event") || strings.Contains(w.Body.String(), "hidden content") || turn.canceled.Load() != 1 || turn.finished.Load() != 0 {
				t.Fatal("progress payload was exposed or accepted")
			}
		}
	}
}
