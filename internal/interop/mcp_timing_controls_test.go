package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
)

type timingControl struct {
	events                chan acp.Notification
	input                 []acp.Notification
	hang, rejectSelection bool
	prompts               atomic.Int32
}

func (c *timingControl) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method == "session/set_model" {
		if c.rejectSelection {
			return nil, errors.New("owned selection rejection")
		}
		return json.RawMessage(`{}`), nil
	}
	if method != "session/prompt" || c.prompts.Add(1) != 1 {
		return nil, errMCPTiming
	}
	data, err := json.Marshal(params)
	var q struct {
		SessionID string
		Prompt    []struct{ Type, Text string }
	}
	if err != nil || json.Unmarshal(data, &q) != nil || q.SessionID != "owned-session" || len(q.Prompt) != 1 || q.Prompt[0].Type != "text" || q.Prompt[0].Text != mcpTimingPrompt {
		return nil, errMCPTiming
	}
	for _, n := range c.input {
		select {
		case c.events <- n:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return json.RawMessage(`{"stopReason":"end_turn"}`), nil
}
func (c *timingControl) Next(ctx context.Context) (acp.Notification, error) {
	select {
	case n := <-c.events:
		return n, nil
	case <-ctx.Done():
		return acp.Notification{}, ctx.Err()
	}
}
func (c *timingControl) TryNext() (acp.Notification, bool, error) {
	select {
	case n := <-c.events:
		return n, true, nil
	default:
		return acp.Notification{}, false, nil
	}
}
func timingEvent(session, kind, id, status string) acp.Notification {
	data, _ := json.Marshal(map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": kind, "toolCallId": id, "status": status, "rawOutput": "Independent text that must never appear in diagnostics."}})
	return acp.Notification{Method: "session/update", Params: data}
}

func TestMCPTimingPromptAndCorrelationGuards(t *testing.T) {
	for _, mode := range []string{"completed", "failed", "foreign-session", "foreign-call", "extra-call", "late-success", "unknown-status", "missing-terminal", "missing-call", "selection-rejected", "deadline", "notification-limit", "frame-limit"} {
		t.Run(mode, func(t *testing.T) {
			start := timingEvent("owned-session", "tool_call", "owned-call", "in_progress")
			end := timingEvent("owned-session", "tool_call_update", "owned-call", "completed")
			c := &timingControl{events: make(chan acp.Notification, 1), input: []acp.Notification{start, end}}
			switch mode {
			case "failed":
				c.input[1] = timingEvent("owned-session", "tool_call_update", "owned-call", "failed")
			case "foreign-session":
				c.input[0] = timingEvent("other-session", "tool_call", "owned-call", "in_progress")
			case "foreign-call":
				c.input[1] = timingEvent("owned-session", "tool_call_update", "other-call", "completed")
			case "extra-call":
				c.input = append(c.input, timingEvent("owned-session", "tool_call", "other-call", "pending"))
			case "late-success":
				c.input[1] = timingEvent("owned-session", "tool_call_update", "owned-call", "failed")
				c.input = append(c.input, end)
			case "unknown-status":
				c.input[1] = timingEvent("owned-session", "tool_call_update", "owned-call", "unrecognized")
			case "missing-terminal":
				c.input = c.input[:1]
			case "missing-call":
				c.input = c.input[1:]
			case "selection-rejected":
				c.rejectSelection = true
			case "deadline":
				c.hang = true
			case "notification-limit":
				for i := 0; i < 256; i++ {
					c.input = append(c.input, acp.Notification{Method: "owned", Params: json.RawMessage(`{}`)})
				}
			case "frame-limit":
				c.input[0].Params = json.RawMessage(strings.Repeat("x", (64<<10)+1))
			}
			p := new(mcpTimingProbe)
			limit := time.Second
			if c.hang {
				limit = 20 * time.Millisecond
			}
			err := p.exercise(t.Context(), c, "owned-session", limit, 0)
			want := mode == "completed" || mode == "failed"
			if (err == nil) != want {
				t.Fatalf("timing control accepted=%v want=%v", err == nil, want)
			}
			count := c.prompts.Load()
			if p.exercise(t.Context(), c, "owned-session", limit, 0) == nil || c.prompts.Load() != count || count > 1 {
				t.Fatal("a second model prompt escaped the once-only bound")
			}
			if c.rejectSelection && count != 0 {
				t.Fatal("model work followed failed selection")
			}
			encoded, _ := json.Marshal(p)
			if strings.Contains(string(encoded), "Independent text") || strings.Contains(string(encoded), "owned-call") || strings.Contains(string(encoded), mcpTimingPrompt) {
				t.Fatal("timing report retained payload or identifiers")
			}
		})
	}
}

func TestMCPTimingEvidenceRejectsAmbiguity(t *testing.T) {
	base := time.Now().Add(-4 * time.Second)
	for _, mode := range []string{"short", "long", "no-call", "early-result", "late-failure", "wrong-terminal", "extra-result", "clock-shift", "foreign-group", "unknown-kind", "lifetime"} {
		t.Run(mode, func(t *testing.T) {
			marks := make([]mcpTimingMark, 0, 7)
			for i, kind := range []string{"started", "initialize_sent", "initialized_notice", "list_sent", "call_received", "call_sent", "input_closed"} {
				nanos := int64(i) * int64(time.Millisecond)
				if i >= 5 {
					nanos += int64(3 * time.Second)
				}
				marks = append(marks, mcpTimingMark{PID: 12345, Group: 12344, Seq: i + 1, Kind: kind, UnixNano: base.UnixNano() + nanos, Nanos: nanos})
			}
			p := &mcpTimingProbe{Completed: true, Calls: 1, Terminal: "failed", terminalAt: base.Add(1504 * time.Millisecond)}
			short := mode != "long"
			if !short {
				p.Terminal, p.terminalAt = "completed", base.Add(3010*time.Millisecond)
			}
			switch mode {
			case "no-call":
				marks[4].Kind = "ping_sent"
			case "early-result":
				marks[5].Nanos -= int64(time.Second)
				marks[5].UnixNano -= int64(time.Second)
			case "late-failure":
				p.terminalAt = base.Add(3100 * time.Millisecond)
			case "wrong-terminal":
				p.Terminal = "completed"
			case "extra-result":
				marks[2].Kind = "late_call_sent"
			case "clock-shift":
				marks[5].UnixNano += int64(time.Second)
			case "foreign-group":
				marks[3].Group++
			case "unknown-kind":
				marks[1].Kind = "untrusted-payload"
			case "lifetime":
				marks[6].Kind = "lifetime_closed"
			}
			var lines strings.Builder
			for _, mark := range marks {
				json.NewEncoder(&lines).Encode(mark)
			}
			parsed, err := timingMarks([]byte(lines.String()), 12344)
			_, ok := timingEstablished(parsed, p, short)
			if (err == nil && ok) != (mode == "short" || mode == "long") {
				t.Fatal("ambiguous timing evidence was accepted or a valid control rejected")
			}
		})
	}
}

func TestMCPDefaultWaitDistinguishesCompletionEarlyFailureAndMissingEvidence(t *testing.T) {
	for _, mode := range []string{"completed", "early-failure", "late-failure", "missing-call", "early-reply", "foreign-cancel", "clock-shift", "no-completion"} {
		t.Run(mode, func(t *testing.T) {
			base := time.Now().Add(-140 * time.Second)
			marks := []mcpTimingMark{}
			for i, kind := range []string{"started", "initialize_sent", "initialized_notice", "list_sent", "call_received", "call_sent", "input_closed"} {
				nanos := time.Duration(i) * time.Millisecond
				if i >= 5 {
					nanos += 135 * time.Second
				}
				marks = append(marks, mcpTimingMark{PID: 12345, Group: 12344, Seq: i + 1, Kind: kind, UnixNano: base.Add(nanos).UnixNano(), Nanos: int64(nanos)})
			}
			p := &mcpTimingProbe{Completed: true, Calls: 1, Terminal: "completed", terminalAt: base.Add(135010 * time.Millisecond)}
			want := "completed"
			switch mode {
			case "early-failure":
				p.Terminal, p.terminalAt = "failed", base.Add(120*time.Second)
				marks[5].Kind = "call_cancelled"
				want = "early_failure"
			case "late-failure":
				p.Terminal = "failed"
				want = "inconclusive"
			case "missing-call":
				marks[4].Kind = "ping_sent"
				want = "inconclusive"
			case "early-reply":
				marks[5].Nanos -= int64(time.Second)
				marks[5].UnixNano -= int64(time.Second)
				want = "inconclusive"
			case "foreign-cancel":
				marks[2].Kind = "foreign_cancel"
				want = "inconclusive"
			case "clock-shift":
				marks[5].UnixNano += int64(time.Second)
				want = "inconclusive"
			case "no-completion":
				p.Completed = false
				want = "inconclusive"
			}
			var data strings.Builder
			for _, m := range marks {
				json.NewEncoder(&data).Encode(m)
			}
			parsed, err := timingMarksWithin([]byte(data.String()), 12344, 235*time.Second)
			_, outcome := defaultWaitEvidence(parsed, p)
			if err != nil {
				outcome = "inconclusive"
			}
			if outcome != want {
				t.Fatalf("outcome=%s want=%s", outcome, want)
			}
			if _, err := timingMarks([]byte(data.String()), 12344); err == nil {
				t.Fatal("extended witness escaped the original short lifetime")
			}
		})
	}
}

func TestMCPDefaultWaitDispatchRemainsSingleUseAndBounded(t *testing.T) {
	c := &timingControl{events: make(chan acp.Notification, 1), input: []acp.Notification{
		timingEvent("owned-session", "tool_call", "owned-call", "in_progress"),
		timingEvent("owned-session", "tool_call_update", "owned-call", "completed"),
	}}
	p := new(mcpTimingProbe)
	if p.exerciseWithin(t.Context(), c, "owned-session", 181*time.Second, 0, 180*time.Second) == nil || c.prompts.Load() != 0 {
		t.Fatal("overlong observation sent a prompt")
	}
	p = new(mcpTimingProbe)
	if p.exerciseWithin(t.Context(), c, "owned-session", time.Second, 0, 180*time.Second) != nil {
		t.Fatal("bounded observation did not complete")
	}
	if p.exerciseWithin(t.Context(), c, "owned-session", time.Second, 0, 180*time.Second) == nil || c.prompts.Load() != 1 {
		t.Fatal("default wait retried")
	}
}
