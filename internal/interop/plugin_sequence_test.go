package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
)

type pluginSequenceStats struct {
	Requests, Uses, Results, Completions, TextBytes int
	Failed                                          bool
	FinalMarker                                     bool
	Events, BeforeToolTextBytes                     int
	Failure, StartError, ReadError                  string
}

func pluginSequenceErrorClass(err error) string {
	for _, entry := range []struct {
		err   error
		label string
	}{
		{acp.ErrAuthentication, "authentication"}, {context.DeadlineExceeded, "deadline"},
		{acp.ErrTimeout, "deadline"}, {context.Canceled, "canceled"},
		{acp.ErrTransport, "transport"}, {acp.ErrProtocol, "protocol"},
		{acp.ErrClosed, "closed"}, {acp.ErrOverloaded, "resource_limit"},
		{acp.ErrFrameTooLarge, "frame_limit"}, {inference.ErrRequest, "request"},
		{inference.ErrBusy, "busy"},
	} {
		if errors.Is(err, entry.err) {
			return entry.label
		}
	}
	return "other"
}

// A live experiment may advertise the client's full registry but expose only these two operations.
// Keep all request/result/output data in bounded memory; diagnostics contain only counters and flags.
type pluginSequenceGuard struct {
	inference.Backend
	denied              bool
	beforeUse           func(int) error
	finalMarker         string
	resultSuffix        string
	gate                sync.Mutex
	mu                  sync.Mutex
	attempts, delivered int
	ids                 [2]string
	stats               pluginSequenceStats
}

func (g *pluginSequenceGuard) snapshot() pluginSequenceStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

func (g *pluginSequenceGuard) failLocked(reason string) {
	g.stats.Failed = true
	if g.stats.Failure == "" {
		g.stats.Failure = reason
	}
}

func (g *pluginSequenceGuard) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if !g.gate.TryLock() {
		return nil, inference.ErrBusy
	}
	defer g.gate.Unlock()
	g.mu.Lock()
	if !g.stats.Failed && g.attempts < 3 && g.delivered != g.attempts {
		g.mu.Unlock()
		return nil, inference.ErrBusy
	}
	g.attempts++
	n := g.attempts
	valid := n <= 3 && r != nil && !g.stats.Failed && g.delivered == n-1
	if valid {
		valid = g.acceptRequest(r, n)
	}
	if !valid {
		g.failLocked("request_shape")
		g.mu.Unlock()
		return nil, inference.ErrRequest
	}
	g.stats.Requests++
	if n > 1 {
		g.stats.Results++
	}
	g.mu.Unlock()
	turn, err := g.Backend.Start(ctx, r)
	if err != nil {
		g.mu.Lock()
		g.failLocked("backend_start")
		g.stats.StartError = pluginSequenceErrorClass(err)
		g.mu.Unlock()
		return nil, err
	}
	return &pluginSequenceTurn{Turn: turn, guard: g, round: n}, nil
}

func (g *pluginSequenceGuard) acceptRequest(r *anthropic.Request, n int) bool {
	if len(r.Tools) == 0 || len(r.Tools) > 128 {
		return false
	}
	names := make(map[string]bool, len(r.Tools))
	for _, raw := range r.Tools {
		fields, err := ndjson.Object(raw)
		var name string
		if err != nil || json.Unmarshal(fields["name"], &name) != nil || name == "" || names[name] {
			return false
		}
		names[name] = true
	}
	results, err := r.LatestToolResults()
	if err != nil {
		return false
	}
	if n == 1 {
		if g.finalMarker != "" {
			for _, message := range r.Messages {
				for _, block := range message.Content {
					if strings.Contains(block.Text, g.finalMarker) || (g.resultSuffix != "" && strings.Contains(block.Text, g.resultSuffix)) {
						return false
					}
				}
			}
		}
		return len(results) == 0 && names["WaitForMcpServers"] && !names[ownedPluginToolName]
	}
	i := r.LatestUserIndex()
	if i < 0 || len(r.Messages[i].Content) != 1 || len(results) != 1 || !names[ownedPluginToolName] || g.ids[n-2] == "" || results[0].ID != g.ids[n-2] {
		return false
	}
	result := results[0]
	if result.IsError != (n == 3 && g.denied) || len(result.Content) != 1 || result.Content[0].Type != "text" {
		return false
	}
	text := result.Content[0].Text
	if text == "" || len(text) > 64<<10 {
		return false
	}
	if g.finalMarker != "" && strings.Contains(text, g.finalMarker) {
		return false
	}
	if n == 2 {
		return g.resultSuffix == "" || !strings.Contains(text, g.resultSuffix)
	}
	suffix := ""
	if g.resultSuffix != "" {
		suffix = "; Y=" + g.resultSuffix
	}
	if g.denied {
		return strings.Contains(text, clientDenialReason+suffix)
	}
	return text == "independent client asset result"+suffix
}

type pluginSequenceTurn struct {
	inference.Turn
	guard  *pluginSequenceGuard
	round  int
	bytes  int
	ended  bool
	tail   string
	finish sync.Once
}

func (t *pluginSequenceTurn) Next(ctx context.Context) (inference.Event, error) {
	event, err := t.Turn.Next(ctx)
	if err != nil {
		if errors.Is(err, io.EOF) {
			t.guard.mu.Lock()
			valid := t.ended
			if !valid {
				t.guard.failLocked("missing_end")
			}
			t.guard.mu.Unlock()
			if !valid {
				t.Turn.Cancel()
				return inference.Event{}, inference.ErrRequest
			}
		} else {
			t.guard.mu.Lock()
			t.guard.stats.ReadError = pluginSequenceErrorClass(err)
			t.guard.mu.Unlock()
		}
		return event, err
	}
	g := t.guard
	g.mu.Lock()
	valid := !g.stats.Failed && !t.ended
	reason := "event_state"
	g.stats.Events++
	if valid {
		switch event.Kind {
		case inference.Text:
			reason = "text_limit"
			t.bytes += len(event.Text)
			if t.round < 3 {
				g.stats.BeforeToolTextBytes += len(event.Text)
			}
			valid = t.bytes <= 64<<10
			if g.finalMarker != "" {
				joined := t.tail + event.Text
				if strings.Contains(joined, g.finalMarker) {
					if t.round == 3 {
						g.stats.FinalMarker = true
					} else {
						valid = false
						reason = "early_marker"
					}
				}
				if valid && t.round < 3 && g.resultSuffix != "" && strings.Contains(joined, g.resultSuffix) {
					valid, reason = false, "early_result_token"
				}
				keep := min(len(joined), len(g.finalMarker)-1)
				t.tail = strings.Clone(joined[len(joined)-keep:])
			}
			if t.round == 3 {
				g.stats.TextBytes += len(event.Text)
			}
		case inference.Tools:
			reason = "tool_batch"
			valid = t.round <= 2 && len(event.Tools) == 1 && g.stats.Uses == t.round-1
			if valid {
				use := event.Tools[0]
				name := "WaitForMcpServers"
				if t.round == 2 {
					name = ownedPluginToolName
				}
				args, err := ndjson.Object(use.Input)
				switch {
				case use.Name != name:
					valid, reason = false, "tool_name"
				case use.ID == "" || len(use.ID) > 256 || (t.round == 2 && use.ID == g.ids[0]):
					valid, reason = false, "tool_identity"
				case len(use.Input) > 1024 || err != nil || len(args) != 0:
					valid, reason = false, "tool_arguments"
				}
				if valid && g.beforeUse != nil {
					valid = g.beforeUse(t.round) == nil
					reason = "relay_identity"
				}
				if valid {
					g.ids[t.round-1] = use.ID
					g.stats.Uses++
				}
			}
		case inference.End:
			reason = "end_condition"
			if t.round < 3 {
				valid = event.StopReason == "tool_use" && g.stats.Uses == t.round
			} else {
				valid = event.StopReason == "end_turn" && g.stats.Uses == 2 && g.stats.Results == 2 && g.stats.TextBytes > 0
				valid = valid && (g.finalMarker == "" || g.stats.FinalMarker)
			}
			if valid {
				t.ended = true
				if t.round == 3 {
					g.stats.Completions++
				}
			}
		default:
			valid = false
			reason = "event_kind"
		}
	}
	if !valid {
		g.failLocked(reason)
	}
	g.mu.Unlock()
	if !valid {
		t.Turn.Cancel()
		return inference.Event{}, inference.ErrRequest
	}
	return event, nil
}

func (t *pluginSequenceTurn) Cancel() {
	t.guard.mu.Lock()
	if !t.ended && !t.guard.stats.Failed {
		reason := "canceled_before_completion"
		if t.guard.stats.ReadError != "" {
			reason = "backend_read"
		}
		t.guard.failLocked(reason)
	}
	t.guard.mu.Unlock()
	t.Turn.Cancel()
}

func (t *pluginSequenceTurn) Finish() {
	t.finish.Do(func() {
		t.Turn.Finish()
		t.guard.mu.Lock()
		defer t.guard.mu.Unlock()
		if t.ended && !t.guard.stats.Failed {
			t.guard.delivered = t.round
		}
	})
}
