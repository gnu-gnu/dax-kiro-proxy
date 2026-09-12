package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/session"
)

type deliveryStart struct {
	turn inference.Turn
	err  error
}

func awaitDeliveryStart(t *testing.T, results <-chan deliveryStart) deliveryStart {
	t.Helper()
	select {
	case result := <-results:
		if result.turn != nil {
			t.Cleanup(result.turn.Cancel)
		}
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("delivery waiter did not join")
		return deliveryStart{}
	}
}

func assertDeliveryWait(t *testing.T, results <-chan deliveryStart) {
	t.Helper()
	select {
	case result := <-results:
		if result.turn != nil {
			result.turn.Cancel()
		}
		t.Fatal("following request escaped the delivery wait")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestTerminalDeliveryWaitBoundsAndRetirement(t *testing.T) {
	for _, ending := range []string{"finish", "cancel", "timeout", "close"} {
		t.Run(ending, func(t *testing.T) {
			timeout := 3 * time.Second
			if ending == "timeout" {
				timeout = 400 * time.Millisecond
			}
			d := toolDriver(t, "chat-tools", timeout, func(c *session.Config) { c.TurnTimeout = 5 * time.Second })
			original := toolRequest(t)
			first, err := d.Start(t.Context(), original)
			if err != nil {
				t.Fatal("initial tool turn failed")
			}
			defer first.Cancel()
			prefix, tools := toolHandoff(t, first)
			next := followup(t, original, prefix, tools)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			results := make(chan deliveryStart, 1)
			go func() { turn, err := d.Start(ctx, next); results <- deliveryStart{turn, err} }()
			assertDeliveryWait(t, results)
			if _, err := d.Start(t.Context(), next); !errors.Is(err, inference.ErrBusy) {
				t.Fatal("more than one delivery waiter was admitted")
			}
			cancel()
			canceled := awaitDeliveryStart(t, results)
			if canceled.turn != nil || !errors.Is(canceled.err, context.Canceled) || d.State() != session.Prompting {
				t.Fatal("canceling the waiter changed the preceding tool owner")
			}
			go func() { turn, err := d.Start(t.Context(), next); results <- deliveryStart{turn, err} }()
			assertDeliveryWait(t, results)
			switch ending {
			case "finish":
				first.Finish()
			case "cancel":
				first.Cancel()
			case "close":
				if d.Close() != nil {
					t.Fatal("driver close failed")
				}
			}
			got := awaitDeliveryStart(t, results)
			if ending == "finish" {
				if got.err != nil || got.turn == nil {
					t.Fatal("successful delivery did not release the next result")
				}
				text, err := collect(t.Context(), got.turn)
				if err != nil || !strings.Contains(text, `"promptCount":1`) || !strings.Contains(text, "synthetic tool result") {
					t.Fatal("delivery wait changed result ownership or restarted the prompt")
				}
				got.turn.Finish()
			} else {
				if got.turn != nil || got.err == nil {
					t.Fatal("failed delivery admitted a tool result")
				}
				if ending == "cancel" && !errors.Is(got.err, context.Canceled) || ending == "timeout" && !errors.Is(got.err, relay.ErrTimeout) {
					t.Fatal("delivery waiter lost the retained failure class")
				}
				first.Finish()
			}
			if d.Close() != nil {
				t.Fatal("delivery fixture cleanup failed")
			}
		})
	}
}

func TestNextQuestionWaitsForTextDelivery(t *testing.T) {
	d := driver(t, "chat-continuity")
	original := sample(t)
	original.Messages = []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "first owned input"}}}}
	first, err := d.Start(t.Context(), original)
	if err != nil {
		t.Fatal("first text turn failed")
	}
	defer first.Cancel()
	text, err := collect(t.Context(), first)
	var before observedPrompt
	if err != nil || json.Unmarshal([]byte(text), &before) != nil || before.PID <= 1 || before.Count != 1 {
		t.Fatal("first text observation incomplete")
	}
	next := *original
	next.Messages = append(append([]anthropic.Message(nil), original.Messages...), anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: text}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "next owned input"}}})
	results := make(chan deliveryStart, 1)
	go func() { turn, err := d.Start(t.Context(), &next); results <- deliveryStart{turn, err} }()
	assertDeliveryWait(t, results)
	first.Finish()
	got := awaitDeliveryStart(t, results)
	if got.err != nil || got.turn == nil {
		t.Fatal("following question failed after text delivery")
	}
	text, err = collect(t.Context(), got.turn)
	var after observedPrompt
	if err != nil || json.Unmarshal([]byte(text), &after) != nil || after.PID != before.PID || after.Session != before.Session || after.Count != 2 || len(after.Prompt) != 1 || after.Prompt[0].Text != "next owned input" {
		t.Fatal("following question lost exact committed history or backend reuse")
	}
	got.turn.Finish()
	if d.Close() != nil {
		t.Fatal("text delivery cleanup failed")
	}
}
