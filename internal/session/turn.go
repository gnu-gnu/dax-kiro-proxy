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
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/relay"
)

// A turn owns one ACP prompt. Each round owns exactly one HTTP response, including a tool handoff.
type turn struct {
	driver                     *Driver
	client                     *acp.Client
	id, model                  string
	owned                      context.Context
	cancelOwned                context.CancelFunc
	done, released             chan struct{}
	result                     json.RawMessage
	resultErr                  error
	settle                     sync.Once
	broker                     *relay.Broker
	socket                     *relay.Socket
	compat, history, assistant [32]byte
	messageCount               int
	lastIDs                    []string
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
			text, visible, err := t.notification(event)
			if err != nil {
				return inference.Event{}, err
			}
			if !visible {
				continue
			}
			if r.text.Len()+len(text) > 16<<20 {
				return inference.Event{}, acp.ErrFrameTooLarge
			}
			r.text.WriteString(text)
			return inference.Event{Kind: inference.Text, Text: text}, nil
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
				text, visible, err := t.notification(late)
				if err != nil {
					return inference.Event{}, err
				}
				if !visible {
					continue
				}
				if r.text.Len()+len(text) > 16<<20 {
					return inference.Event{}, acp.ErrFrameTooLarge
				}
				r.text.WriteString(text)
				return inference.Event{Kind: inference.Text, Text: text}, nil
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
			case "end_turn", "max_tokens", "refusal":
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
				batch, err := t.broker.Seal()
				if err != nil {
					return inference.Event{}, err
				}
				r.batch = batch
				uses := make([]anthropic.ToolUse, len(batch.Calls))
				for i, call := range batch.Calls {
					uses[i] = anthropic.ToolUse{ID: call.ID, Name: call.Name, Input: call.Input}
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
func (t *turn) notification(event acp.Notification) (string, bool, error) {
	if event.SessionID != "" && event.SessionID != t.id {
		return "", false, acp.ErrProtocol
	}
	if event.Method != "session/update" {
		if event.Method == "_kiro.dev/commands/available" && event.SessionID == t.id {
			_ = t.driver.effort.Advertise(event.Params)
		}
		return "", false, nil
	}
	if event.SessionID != t.id {
		return "", false, acp.ErrProtocol
	}
	fields, err := ndjson.Object(event.Params)
	if err != nil {
		return "", false, acp.ErrProtocol
	}
	update, err := ndjson.Object(fields["update"])
	var kind string
	if err != nil || !strictString(update["sessionUpdate"], &kind) {
		return "", false, acp.ErrProtocol
	}
	if kind != "agent_message_chunk" {
		return "", false, nil
	}
	content, err := ndjson.Object(update["content"])
	var typ, text string
	if err != nil || !strictString(content["type"], &typ) || typ != "text" || !strictString(content["text"], &text) {
		return "", false, acp.ErrProtocol
	}
	return text, text != "", nil
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
			t.complete()
			return
		}
		assistant, ids, err := assistantDigest(text, batch.Calls)
		if err != nil {
			t.abort(acp.ErrProtocol)
			return
		}
		d := t.driver
		d.mu.Lock()
		if d.current != t || d.closed {
			d.mu.Unlock()
			t.abort(context.Canceled)
			return
		}
		t.assistant = assistant
		t.lastIDs = ids
		d.state = WaitingTools
		err = t.broker.Delivered(batch.Number)
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
func (t *turn) complete() {
	t.settle.Do(func() {
		close(t.released)
		t.cancelOwned()
		d := t.driver
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.current == t {
			d.current = nil
			d.initialUsed = true
			if !d.closed {
				d.state = Idle
			}
		}
	})
}
func (t *turn) abort(reason error) {
	t.settle.Do(func() {
		close(t.released)
		t.cancelOwned()
		if t.socket != nil {
			t.socket.Close()
		} else if t.broker != nil {
			t.broker.Close()
		}
		_ = t.client.Close()
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
		if d.state == WaitingTools {
			d.outcome = &terminalOutcome{compat: t.compat, ids: append([]string(nil), t.lastIDs...), err: reason, expires: time.Now().Add(5 * time.Minute)}
		}
		d.current = nil
		d.client = nil
		d.socket = nil
		d.broker = nil
		if !d.closed {
			d.state = Unstarted
		}
	})
}
