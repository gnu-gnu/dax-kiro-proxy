package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/status"
)

// A turn owns one ACP prompt. Each round owns exactly one HTTP response, including a tool handoff.
type turn struct {
	driver              *Driver
	client              backendClient
	id, model           string
	owned               context.Context
	cancelOwned         context.CancelFunc
	done, released      chan struct{}
	result              json.RawMessage
	resultErr           error
	settle              sync.Once
	broker              *relay.Broker
	socket              *relay.Socket
	compat, reuseCompat [32]byte
	policyCompat        [32]byte
	plan                history.Plan
	pendingHistory      history.Snapshot
	lastIDs             []string
	started             time.Time
	recreations         int
	sessionState        string
	multiplier          *float64
	effort              kirofeature.Status
	metadata            kirofeature.TurnMetadata
	inputEstimate       status.InputEstimate
	previousEstimate    status.InputEstimate
	visibleOutput       status.VisibleOutput
	progressTools       map[[32]byte]struct{}
}
type round struct {
	turn              *turn
	mu                sync.Mutex
	terminal, success bool
	batch             relay.Batch
	text              strings.Builder
	once              sync.Once
}

func (r *round) Model() string { return r.turn.model }
func (r *round) Next(ctx context.Context) (inference.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal {
		return inference.Event{}, io.EOF
	}
	t := r.turn
	for {
		if ctx.Err() != nil {
			return inference.Event{}, ctx.Err()
		}
		if err := t.client.Err(); err != nil {
			return inference.Event{}, err
		}
		if t.owned.Err() != nil {
			return inference.Event{}, t.owned.Err()
		}
		if t.broker != nil && t.broker.Err() != nil {
			return inference.Event{}, t.broker.Err()
		}
		if r.batch.Number != 0 {
			r.terminal = true
			r.success = true
			return inference.Event{Kind: inference.End, StopReason: "tool_use"}, nil
		}
		event, available, err := t.client.TryNext()
		if err != nil {
			return inference.Event{}, err
		}
		if available {
			out, err := r.notification(event)
			if err != nil || out.Kind != 0 {
				return out, err
			}
			continue
		}
		select {
		case <-t.done:
			// The final reply may have arrived after the earlier empty TryNext. Its preceding
			// notifications are now all queued and must be drained before exposing completion.
			late, available, err := t.client.TryNext()
			if err != nil {
				return inference.Event{}, err
			}
			if available {
				out, err := r.notification(late)
				if err != nil || out.Kind != 0 {
					return out, err
				}
				continue
			}
			if t.resultErr != nil {
				return inference.Event{}, t.resultErr
			}
			if t.broker != nil && t.broker.Stats().Pending > 0 {
				return inference.Event{}, acp.ErrProtocol
			}
			fields, err := ndjson.Object(t.result)
			var stop string
			if err != nil || !strictString(fields["stopReason"], &stop) {
				return inference.Event{}, acp.ErrProtocol
			}
			switch stop {
			case "end_turn", "max_tokens", "refusal", "max_turn_requests":
				if stop == "max_turn_requests" {
					stop = "pause_turn"
				}
				r.terminal = true
				r.success = true
				return inference.Event{Kind: inference.End, StopReason: stop}, nil
			case "cancelled":
				return inference.Event{}, context.Canceled
			default:
				return inference.Event{}, acp.ErrProtocol
			}
		default:
		}
		var relayReady, relayDone <-chan struct{}
		if t.broker != nil {
			if t.broker.Stats().Queued > 0 {
				batch, err := r.sealTools()
				if err != nil {
					return inference.Event{}, err
				}
				r.batch = batch
				uses := make([]anthropic.ToolUse, len(batch.Calls))
				for i, call := range batch.Calls {
					uses[i] = anthropic.ToolUse{ID: call.ID, Name: call.Name, Input: call.Input}
				}
				if t.driver.estimator != nil {
					t.visibleOutput.AddTools(uses)
				}
				return inference.Event{Kind: inference.Tools, Tools: uses}, nil
			}
			relayReady = t.broker.Ready()
			relayDone = t.broker.Done()
		}
		select {
		case <-ctx.Done():
			return inference.Event{}, ctx.Err()
		case <-t.owned.Done():
			return inference.Event{}, t.owned.Err()
		case <-t.client.Activity():
		case <-t.done:
		case <-relayReady:
		case <-relayDone:
		}
	}
}

func (r *round) sealTools() (relay.Batch, error) {
	t := r.turn
	d := t.driver
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current != t || d.closed || d.state != Prompting {
		return relay.Batch{}, context.Canceled
	}
	batch, err := t.broker.Seal()
	if err != nil {
		return relay.Batch{}, err
	}
	content, ids, err := assistantContent(r.text.String(), batch.Calls)
	if err != nil {
		return relay.Batch{}, acp.ErrProtocol
	}
	pending, err := d.hasher.Complete(t.plan, content)
	if err != nil {
		return relay.Batch{}, acp.ErrProtocol
	}
	// Retain only a candidate until Finish confirms delivery. An intervening abort can report its
	// terminal error to the exact history/IDs without admitting results or committing idle history.
	t.pendingHistory = pending
	t.lastIDs = ids
	return batch, nil
}

func (r *round) notification(event acp.Notification) (inference.Event, error) {
	t := r.turn
	if event.SessionID != "" && event.SessionID != t.id {
		return inference.Event{}, acp.ErrProtocol
	}
	if event.Method != "session/update" {
		if event.Method == "_kiro.dev/commands/available" && event.SessionID == t.id {
			_ = t.driver.effort.Advertise(event.Params)
		}
		if event.Method == "_kiro.dev/metadata" && event.SessionID == t.id {
			t.metadata.Apply(event.Params)
		}
		return inference.Event{}, nil
	}
	if event.SessionID != t.id {
		return inference.Event{}, acp.ErrProtocol
	}
	fields, err := ndjson.Object(event.Params)
	if err != nil {
		return inference.Event{}, acp.ErrProtocol
	}
	update, err := ndjson.Object(fields["update"])
	var kind string
	if err != nil || !strictString(update["sessionUpdate"], &kind) {
		return inference.Event{}, acp.ErrProtocol
	}
	if kind != "agent_message_chunk" {
		if t.progress(kind, update) {
			return inference.Event{Kind: inference.Progress}, nil
		}
		return inference.Event{}, nil
	}
	content, err := ndjson.Object(update["content"])
	var typ, text string
	if err != nil || !strictString(content["type"], &typ) || typ != "text" || !strictString(content["text"], &text) {
		return inference.Event{}, acp.ErrProtocol
	}
	if text == "" {
		return inference.Event{}, nil
	}
	if r.text.Len()+len(text) > 16<<20 {
		return inference.Event{}, acp.ErrFrameTooLarge
	}
	r.text.WriteString(text)
	t.visibleOutput.AddText(text)
	return inference.Event{Kind: inference.Text, Text: text}, nil
}
func (r *round) Finish() {
	r.mu.Lock()
	success := r.success
	batch := r.batch
	text := r.text.String()
	r.mu.Unlock()
	if !success {
		r.Cancel()
		return
	}
	r.once.Do(func() {
		t := r.turn
		if batch.Number == 0 {
			t.complete(text)
			return
		}
		d := t.driver
		d.mu.Lock()
		if d.current != t || d.closed {
			d.mu.Unlock()
			t.abort(context.Canceled)
			return
		}
		d.state = WaitingTools
		err := t.broker.Delivered(batch.Number)
		d.mu.Unlock()
		if err != nil {
			t.abort(err)
		}
	})
}
func (r *round) Cancel() { r.once.Do(func() { r.turn.abort(context.Canceled) }) }
func (t *turn) watch() {
	var relayDone <-chan struct{}
	if t.broker != nil {
		relayDone = t.broker.Done()
	}
	select {
	case <-t.released:
		return
	case <-t.owned.Done():
		t.abort(t.owned.Err())
	case <-relayDone:
		t.abort(t.broker.Err())
	}
}
func (t *turn) complete(text string) {
	snapshot, err := t.driver.hasher.Complete(t.plan, []anthropic.Block{{Type: "text", Text: text}})
	if err != nil {
		t.abort(acp.ErrProtocol)
		return
	}
	t.settle.Do(func() {
		close(t.released)
		t.cancelOwned()
		d := t.driver
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.current == t {
			d.snapshot = snapshot
			d.reuseCompat = t.reuseCompat
			d.current = nil
			d.initialUsed = true
			if !d.closed {
				d.persistIdle(snapshot, t.reuseCompat)
				d.state = Idle
				if d.cfg.Metrics != nil {
					record := status.TurnRecord{Scope: d.cfg.MetricsScope, Model: t.model, Multiplier: t.multiplier, SessionState: t.sessionState, ElapsedMS: time.Since(t.started).Milliseconds(), Effort: t.effort, Metadata: t.metadata.Snapshot()}
					if estimate, ok := t.inputEstimate.Summary(t.previousEstimate, t.visibleOutput); ok {
						record.Estimate = &estimate
					}
					d.cfg.Metrics.Push(record)
					d.lastEstimate = t.inputEstimate
				}
				if lease, ok := t.client.(*acppool.Lease); ok {
					_ = lease.SetIdle(true)
				}
			}
		}
	})
}
func (t *turn) abort(reason error) {
	t.settle.Do(func() {
		reason = t.abortReason(reason)
		close(t.released)
		t.cancelOwned()
		cleanupErr := closeRelay(t.socket, t.broker)
		cleanupErr = errors.Join(cleanupErr, t.client.Close())
		t.driver.noteCleanup(cleanupErr)
		<-t.done
		// Relay disconnection may race the correlated account-expiry response during group cleanup.
		// A confirmed backend auth class takes precedence over that secondary cancellation.
		if errors.Is(t.client.Err(), acp.ErrAuthentication) || errors.Is(t.resultErr, acp.ErrAuthentication) {
			reason = acp.ErrAuthentication
		}
		d := t.driver
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.current != t {
			return
		}
		if !d.closed && len(t.lastIDs) > 0 {
			d.outcome = &terminalOutcome{compat: t.compat, ids: append([]string(nil), t.lastIDs...), pending: t.pendingHistory, err: reason, expires: time.Now().Add(5 * time.Minute)}
		}
		d.current = nil
		d.snapshot = history.Snapshot{}
		d.client = nil
		d.socket = nil
		d.broker = nil
		if !d.closed {
			d.state = Unstarted
		}
	})
}

func (t *turn) abortReason(reason error) error {
	if errors.Is(reason, acp.ErrAuthentication) {
		return reason
	}
	// A recorded tool deadline is the more specific failure; otherwise preserve an expired turn.
	// Prompt completion after relay expiry may already have been reclassified as a protocol error.
	// Inspect before cleanup cancels the owner and closes a still-healthy relay.
	if t.broker != nil && errors.Is(t.broker.Err(), relay.ErrTimeout) {
		return relay.ErrTimeout
	}
	if errors.Is(t.owned.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return reason
}
