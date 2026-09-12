package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/ndjson"
)

const mcpTimingPrompt = "Independent protocol timing exercise. Call owned_wait exactly once with an empty object. " +
	"Do not use another tool, inspect files or configuration, or retry. " +
	"After the call succeeds or fails, finish with a short statement of its status."

var errMCPTiming = errors.New("owned MCP timing observation is inconclusive")

type timingACP interface {
	Call(context.Context, string, any) (json.RawMessage, error)
	Next(context.Context) (acp.Notification, error)
	TryNext() (acp.Notification, bool, error)
}

type mcpTimingProbe struct {
	attempted                               atomic.Bool
	callID                                  string
	terminalAt                              time.Time
	Notifications, Bytes, Calls, ToolEvents int
	PromptSent, Completed                   bool
	Terminal                                string
}

// The same single-use dispatcher is used by live observation and independent guard controls.
func (p *mcpTimingProbe) exercise(ctx context.Context, client timingACP, session string, limit, tail time.Duration) error {
	if !p.attempted.CompareAndSwap(false, true) || session == "" || limit <= 0 || limit > 45*time.Second || tail < 0 || tail > 4*time.Second || tail >= limit {
		return errMCPTiming
	}
	selection, stop := context.WithTimeout(ctx, 5*time.Second)
	raw, err := client.Call(selection, "session/set_model", map[string]string{"sessionId": session, "modelId": "auto"})
	stop()
	if _, shapeErr := ndjson.Object(raw); err != nil || shapeErr != nil {
		return errMCPTiming
	}
	turn, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	events, stopEvents := context.WithCancel(turn)
	defer stopEvents()
	type outcome struct {
		raw json.RawMessage
		err error
	}
	done := make(chan outcome, 1)
	p.PromptSent = true
	go func() {
		raw, err := client.Call(turn, "session/prompt", map[string]any{"sessionId": session, "prompt": []any{map[string]string{"type": "text", "text": mcpTimingPrompt}}})
		done <- outcome{raw, err}
		stopEvents()
	}()
	var observed error
	for {
		n, err := client.Next(events)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				observed = err
			}
			break
		}
		if observed = p.observe(n, session); observed != nil {
			break
		}
	}
	if observed != nil {
		cancel()
	}
	result := <-done
	for observed == nil {
		n, ok, err := client.TryNext()
		if err != nil {
			observed = err
			break
		}
		if !ok {
			break
		}
		observed = p.observe(n, session)
	}
	var completion struct{ StopReason string }
	p.Completed = result.err == nil && json.Unmarshal(result.raw, &completion) == nil && completion.StopReason == "end_turn"
	if observed != nil || !p.Completed || turn.Err() != nil {
		return errMCPTiming
	}
	// Observe a late MCP reply without opening another prompt or extending the original deadline.
	if tail > 0 {
		settle, stop := context.WithTimeout(turn, tail)
		defer stop()
		for {
			n, err := client.Next(settle)
			if errors.Is(err, context.DeadlineExceeded) && turn.Err() == nil {
				break
			}
			if err != nil || p.observe(n, session) != nil {
				return errMCPTiming
			}
		}
	}
	if p.Calls != 1 || p.Terminal == "" {
		return errMCPTiming
	}
	return nil
}

func (p *mcpTimingProbe) observe(n acp.Notification, session string) error {
	p.Notifications++
	p.Bytes += len(n.Params)
	if p.Notifications > 256 || len(n.Params) > 64<<10 || p.Bytes > 1<<20 {
		return errMCPTiming
	}
	fields, err := ndjson.Object(n.Params)
	if err != nil {
		return errMCPTiming
	}
	if raw, present := fields["sessionId"]; present {
		var owner string
		if json.Unmarshal(raw, &owner) != nil || owner != session {
			return errMCPTiming
		}
	}
	if n.Method != "session/update" {
		return nil
	}
	var update struct{ SessionUpdate, ToolCallID, Status string }
	if fields["sessionId"] == nil || json.Unmarshal(fields["update"], &update) != nil {
		return errMCPTiming
	}
	if update.SessionUpdate != "tool_call" && update.SessionUpdate != "tool_call_update" {
		return nil
	}
	p.ToolEvents++
	if p.ToolEvents > 16 || update.ToolCallID == "" || len(update.ToolCallID) > 1024 {
		return errMCPTiming
	}
	if update.SessionUpdate == "tool_call" {
		p.Calls++
		if p.Calls != 1 {
			return errMCPTiming
		}
		p.callID = update.ToolCallID
	} else if p.callID != update.ToolCallID {
		return errMCPTiming
	}
	switch update.Status {
	case "", "pending", "in_progress":
		if p.Terminal != "" && update.Status != "" {
			return errMCPTiming
		}
	case "completed", "failed":
		if p.Terminal != "" && p.Terminal != update.Status {
			return errMCPTiming
		}
		if p.Terminal == "" {
			p.Terminal, p.terminalAt = update.Status, time.Now()
		}
	default:
		return errMCPTiming
	}
	return nil
}

type mcpTimingMark struct {
	PID, Group, Seq int
	Kind            string
	UnixNano, Nanos int64
}

func timingMarks(data []byte, group int) ([]mcpTimingMark, error) {
	if len(data) == 0 || len(data) > 32<<10 {
		return nil, errMCPTiming
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) > 96 {
		return nil, errMCPTiming
	}
	marks := make([]mcpTimingMark, 0, len(lines))
	for i, line := range lines {
		var m mcpTimingMark
		if json.Unmarshal(line, &m) != nil || m.PID <= 1 || group <= 1 || m.Group != group || m.Seq != i+1 || m.Nanos < 0 || m.Nanos > int64(75*time.Second) || m.UnixNano <= 0 {
			return nil, errMCPTiming
		}
		switch m.Kind {
		case "started", "initialize_sent", "initialized_notice", "list_sent", "ping_sent", "call_received", "call_sent", "late_call_sent", "call_cancelled", "foreign_cancel", "repeated_cancel", "after_result_cancel", "input_closed", "lifetime_closed", "call_rejected":
		default:
			return nil, errMCPTiming
		}
		if i > 0 {
			first, previous := marks[0], marks[i-1]
			drift := (m.UnixNano - first.UnixNano) - (m.Nanos - first.Nanos)
			if m.PID != first.PID || m.Nanos < previous.Nanos || drift < -int64(100*time.Millisecond) || drift > int64(100*time.Millisecond) {
				return nil, errMCPTiming
			}
		}
		marks = append(marks, m)
	}
	return marks, nil
}

// A model's prose is never evidence. Only the sole owned MCP call and correlated ACP status count.
func timingEstablished(marks []mcpTimingMark, p *mcpTimingProbe, short bool) (int64, bool) {
	counts := map[string]int{}
	var received, sent mcpTimingMark
	for _, m := range marks {
		counts[m.Kind]++
		if m.Kind == "call_received" {
			received = m
		}
		if m.Kind == "call_sent" || m.Kind == "late_call_sent" {
			sent = m
		}
	}
	if !p.Completed || p.Calls != 1 || p.terminalAt.IsZero() || counts["started"] != 1 || counts["initialize_sent"] != 1 || counts["initialized_notice"] != 1 || counts["list_sent"] != 1 || counts["call_received"] != 1 || counts["call_sent"]+counts["late_call_sent"] != 1 || counts["call_rejected"] != 0 || counts["lifetime_closed"] != 0 || counts["input_closed"] != 1 {
		return 0, false
	}
	if sent.Nanos-received.Nanos < int64(3*time.Second) || sent.Seq <= received.Seq {
		return 0, false
	}
	elapsed := (p.terminalAt.UnixNano() - received.UnixNano) / int64(time.Millisecond)
	if short {
		return elapsed, p.Terminal == "failed" && elapsed >= 1000 && elapsed <= 2200 && p.terminalAt.UnixNano() < sent.UnixNano-int64(200*time.Millisecond)
	}
	return elapsed, p.Terminal == "completed" && elapsed >= 2900 && p.terminalAt.UnixNano() >= sent.UnixNano-int64(100*time.Millisecond) && counts["call_cancelled"] == 0 && counts["late_call_sent"] == 0
}
