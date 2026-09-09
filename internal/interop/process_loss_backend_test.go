package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

const processLossSystem = "Independent process recovery exercise. Request the listed Read client tool only when the user asks for Read, exactly once with the specified file_path and no other arguments. Otherwise finish with a brief text acknowledgement. Do not use native tools, inspect other files or configuration, delegate, retry, or disclose file content."
const processLossRecoveryPrompt = "Do not request any tool. Reply with a brief acknowledgement of this new independent request."

type processLossBackend struct {
	guard                            *onePromptBackend
	lose                             func(context.Context) error
	starts                           atomic.Int32
	failureObserved, recoveryAllowed atomic.Bool
	completions, textEvents          atomic.Int32
	mu                               sync.Mutex
	first                            *anthropic.Request
	policySame                       bool
	lossShape                        struct{ SignalWaitError, NextError, Transport, Closed, Canceled, Deadline, EOF, Protocol, CallerDone bool }
	comparison                       defaultClientComparison
	systemShape                      struct {
		FirstBlocks, NextBlocks, FirstPolicyBlocks, NextPolicyBlocks, ChangedBlock, CommonPrefix, CommonSuffix int
		BillingPrefix                                                                                          bool
	}
	recoveryShape struct {
		Valid, ExactPrompt, ContainsPrompt bool
		Messages, LatestUser, LatestBlocks int
	}
}

func (b *processLossBackend) Models(ctx context.Context) ([]inference.Model, error) {
	return b.guard.Models(ctx)
}

func (b *processLossBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	n := b.starts.Add(1)
	if r == nil || n > 2 {
		return nil, inference.ErrRequest
	}
	if n == 1 {
		b.mu.Lock()
		b.first = r
		b.mu.Unlock()
		turn, err := b.guard.Start(ctx, r)
		if err != nil {
			return nil, err
		}
		return &processLossTurn{Turn: turn, owner: b}, nil
	}
	b.mu.Lock()
	first := b.first
	if first != nil {
		b.comparison = compareDefaultRequests(first, r)
		s := &b.systemShape
		s.FirstBlocks, s.NextBlocks = len(first.System), len(r.System)
		for _, block := range first.System {
			if block.Text == processLossSystem {
				s.FirstPolicyBlocks++
			}
		}
		for _, block := range r.System {
			if block.Text == processLossSystem {
				s.NextPolicyBlocks++
			}
		}
		for i := range min(len(first.System), len(r.System)) {
			a, c := first.System[i].Text, r.System[i].Text
			if a == c {
				continue
			}
			s.ChangedBlock = i + 1
			s.BillingPrefix = strings.HasPrefix(a, "x-anthropic-billing-header:") && strings.HasPrefix(c, "x-anthropic-billing-header:")
			for s.CommonPrefix < min(len(a), len(c)) && a[s.CommonPrefix] == c[s.CommonPrefix] {
				s.CommonPrefix++
			}
			for s.CommonSuffix < min(len(a), len(c))-s.CommonPrefix && a[len(a)-1-s.CommonSuffix] == c[len(c)-1-s.CommonSuffix] {
				s.CommonSuffix++
			}
		}
	}
	b.recoveryShape.Valid = recoveryTextRequest(r)
	b.recoveryShape.Messages = len(r.Messages)
	b.recoveryShape.LatestUser = r.LatestUserIndex()
	if i := r.LatestUserIndex(); i >= 0 {
		b.recoveryShape.LatestBlocks = len(r.Messages[i].Content)
		for _, block := range r.Messages[i].Content {
			b.recoveryShape.ExactPrompt = b.recoveryShape.ExactPrompt || block.Text == processLossRecoveryPrompt
			b.recoveryShape.ContainsPrompt = b.recoveryShape.ContainsPrompt || strings.Contains(block.Text, processLossRecoveryPrompt)
		}
	}
	valid := first != nil && first.Identity.Session != "" && first.Identity == r.Identity && first.Model == r.Model && first.Effort == r.Effort && sameRecoverySystem(first.System, r.System) && reflect.DeepEqual(first.Tools, r.Tools) && bytes.Equal(first.Extra["metadata"], r.Extra["metadata"]) && bytes.Equal(first.Extra["tool_choice"], r.Extra["tool_choice"])
	b.policySame = valid
	b.mu.Unlock()
	if !valid || !b.failureObserved.Load() || !b.recoveryAllowed.Load() || b.guard.driver.State() != session.Unstarted || !recoveryTextRequest(r) {
		return nil, inference.ErrRequest
	}
	turn, err := b.guard.driver.Start(ctx, r)
	if err != nil {
		return nil, err
	}
	return &processLossTurn{Turn: turn, owner: b, recovery: true}, nil
}

// The public client changes a short billing-header fragment across fresh print invocations.
// This observation predicate never rewrites the request supplied to the application driver.
func sameRecoverySystem(first, next []anthropic.Block) bool {
	if len(first) == 0 || len(first) != len(next) || len(first) > 16 {
		return false
	}
	policy := false
	for i, a := range first {
		b := next[i]
		policy = policy || a.Text == processLossSystem && b.Text == processLossSystem
		if reflect.DeepEqual(a, b) {
			continue
		}
		const prefix = "x-anthropic-billing-header:"
		if i != 0 || a.Type != "text" || b.Type != "text" || len(a.Text) != len(b.Text) || len(a.Text) > 256 || !strings.HasPrefix(a.Text, prefix) || !strings.HasPrefix(b.Text, prefix) || strings.ContainsAny(a.Text+b.Text, "\r\n\x00") {
			return false
		}
		start, end := 0, len(a.Text)
		for start < end && a.Text[start] == b.Text[start] {
			start++
		}
		for end > start && a.Text[end-1] == b.Text[end-1] {
			end--
		}
		if end == start || end-start > 32 {
			return false
		}
		for _, text := range []string{a.Text[start:end], b.Text[start:end]} {
			for _, c := range text {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
					return false
				}
			}
		}
		var af, bf map[string]json.RawMessage
		if json.Unmarshal(a.Raw, &af) != nil || json.Unmarshal(b.Raw, &bf) != nil {
			return false
		}
		delete(af, "text")
		delete(bf, "text")
		if !reflect.DeepEqual(af, bf) {
			return false
		}
	}
	return policy
}

func recoveryTextRequest(r *anthropic.Request) bool {
	if r.LatestUserIndex() != 0 || len(r.Messages) > 2 || len(r.Messages[0].Content) > 2 {
		return false
	}
	found := false
	for i, message := range r.Messages {
		if i == 1 && message.Role != "system" {
			return false
		}
		for _, block := range message.Content {
			if block.Type != "text" {
				return false
			}
			if i == 0 && block.Text == processLossRecoveryPrompt {
				found = true
			}
		}
	}
	return found
}

type processLossTurn struct {
	inference.Turn
	owner           *processLossBackend
	recovery, ended bool
}

func (t *processLossTurn) Next(ctx context.Context) (inference.Event, error) {
	event, err := t.Turn.Next(ctx)
	if err != nil {
		return event, err
	}
	if event.Kind == inference.Text {
		t.owner.textEvents.Add(1)
	}
	if t.recovery {
		if event.Kind == inference.Text {
			return event, nil
		}
		if event.Kind == inference.End && event.StopReason == "end_turn" {
			t.ended = true
			return event, nil
		}
		t.Turn.Cancel()
		return inference.Event{}, inference.ErrRequest
	}
	if event.Kind == inference.Tools {
		// The inner exact-operation guard has validated this call. It never reaches the client.
		if err := t.owner.lose(ctx); err != nil {
			t.owner.mu.Lock()
			t.owner.lossShape.SignalWaitError = true
			t.owner.mu.Unlock()
			t.Turn.Cancel()
			return inference.Event{}, inference.ErrRequest
		}
		_, err = t.Turn.Next(ctx)
		t.owner.mu.Lock()
		s := &t.owner.lossShape
		s.NextError = err != nil
		s.Transport = errors.Is(err, acp.ErrTransport)
		s.Closed = errors.Is(err, acp.ErrClosed)
		s.Canceled = errors.Is(err, context.Canceled)
		s.Deadline = errors.Is(err, context.DeadlineExceeded) || errors.Is(err, acp.ErrTimeout)
		s.EOF = errors.Is(err, io.EOF)
		s.Protocol = errors.Is(err, acp.ErrProtocol)
		s.CallerDone = ctx.Err() != nil
		t.owner.mu.Unlock()
		// The observer has joined the deliberately killed owned group. Internal retirement may
		// cancel the backend context before EOF wins; a canceled caller never proves this case.
		if ctx.Err() == nil && (errors.Is(err, acp.ErrTransport) || errors.Is(err, acp.ErrClosed) || errors.Is(err, context.Canceled)) {
			t.owner.failureObserved.Store(true)
		}
		if err == nil {
			t.Turn.Cancel()
			return inference.Event{}, inference.ErrRequest
		}
		return inference.Event{}, err
	}
	if event.Kind != inference.Text {
		t.Turn.Cancel()
		return inference.Event{}, inference.ErrRequest
	}
	return event, nil
}

func (t *processLossTurn) Finish() {
	t.Turn.Finish()
	if t.recovery && t.ended {
		t.owner.completions.Add(1)
	}
}
