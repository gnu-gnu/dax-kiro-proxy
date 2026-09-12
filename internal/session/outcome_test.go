package session

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/toolregistry"
)

type outcomePeer struct {
	backendClient
	err         error
	authOnClose bool
	done        chan struct{}
	closeOnce   sync.Once
}

func (p *outcomePeer) Err() error { return p.err }
func (p *outcomePeer) TryNext() (acp.Notification, bool, error) {
	return acp.Notification{}, false, nil
}
func (p *outcomePeer) Close() error {
	if p.authOnClose {
		p.err = acp.ErrAuthentication
	}
	if p.done != nil {
		p.closeOnce.Do(func() { close(p.done) })
	}
	return nil
}

func TestSealedOutcomeRequiresCompleteBatchAndExpires(t *testing.T) {
	request := &anthropic.Request{Model: "claude-dax-fixture", Tools: []json.RawMessage{json.RawMessage(`{"name":"client_action","input_schema":{"type":"object"}}`)},
		Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "two independent fixture requests"}}}}}
	registry, err := toolregistry.Build(t.Context(), request.Tools, nil, outcomeValidator{})
	if err != nil {
		t.Fatal("registry fixture preparation failed")
	}
	broker, err := relay.NewBroker(registry, relay.Limits{ToolTimeout: time.Minute})
	if err != nil || broker.BeginTurn() != nil {
		t.Fatal("broker fixture preparation failed")
	}
	t.Cleanup(broker.Close)
	done := make(chan struct{})
	owned, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Driver{state: Prompting, hasher: history.New([32]byte{2})}
	plan, err := d.hasher.Plan(history.Snapshot{}, request)
	if err != nil {
		t.Fatal("history fixture preparation failed")
	}
	stamp, err := compatibility(request, registry)
	if err != nil {
		t.Fatal("policy fixture preparation failed")
	}
	tr := &turn{driver: d, client: &outcomePeer{done: done}, owned: owned, cancelOwned: cancel, done: done,
		released: make(chan struct{}), broker: broker, plan: plan, compat: stamp}
	d.current = tr
	r := &round{turn: tr}
	t.Cleanup(r.Cancel)
	var calls sync.WaitGroup
	for _, id := range []string{"first", "second"} {
		calls.Go(func() {
			c := broker.Credentials()
			result, err := broker.Call(context.Background(), relay.Call{Version: 1, Owner: c.Owner, Secret: c.Secret, ID: id, Alias: registry.Tools()[0].Alias, Arguments: json.RawMessage(`{}`)})
			if err != nil || !result.IsError {
				t.Error("cancelled fixture call did not receive a tool error")
			}
		})
	}
	joined := make(chan struct{})
	go func() { calls.Wait(); close(joined) }()
	t.Cleanup(func() {
		r.Cancel()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("fixture batch cleanup did not join")
		}
	})
	until := time.Now().Add(time.Second)
	for broker.Stats().Queued != 2 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if broker.Stats().Queued != 2 {
		t.Fatal("complete fixture batch was not queued")
	}
	event, err := r.Next(t.Context())
	if err != nil || event.Kind != inference.Tools || len(event.Tools) != 2 || d.state != Prompting || len(d.snapshot.Nodes) != 0 {
		t.Fatal("sealed candidate committed or lost its complete batch")
	}
	next := *request
	next.Messages = append([]anthropic.Message(nil), request.Messages...)
	assistant := anthropic.Message{Role: "assistant"}
	results := anthropic.Message{Role: "user"}
	for _, tool := range event.Tools {
		raw, _ := json.Marshal(tool.Block())
		assistant.Content = append(assistant.Content, anthropic.Block{Type: "tool_use", Raw: raw})
		raw, _ = json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": tool.ID, "is_error": true, "content": "fixture refusal"})
		results.Content = append(results.Content, anthropic.Block{Type: "tool_result", Raw: raw})
	}
	next.Messages = append(next.Messages, assistant, results)
	complete, err := next.LatestToolResults()
	if err != nil {
		t.Fatal("result fixture encoding failed")
	}
	if _, err := d.resume(t.Context(), &next, registry, complete); !errors.Is(err, inference.ErrBusy) {
		t.Fatal("sealed candidate admitted results before delivery")
	}
	// Cancellation immediately after Seal, before even exposing the tool-use End event.
	r.Cancel()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("cancelled batch did not join")
	}
	if d.outcome == nil || !d.outcome.pending.Valid() || len(d.outcome.ids) != 2 || len(d.snapshot.Nodes) != 0 {
		t.Fatal("sealed cancellation lost its bounded uncommitted history")
	}
	for range 2 {
		if _, err := d.resume(t.Context(), &next, registry, complete); !errors.Is(err, context.Canceled) {
			t.Fatal("complete retired batch lost its cancellation outcome")
		}
	}
	partial := next
	partial.Messages = append([]anthropic.Message(nil), next.Messages...)
	partial.Messages[2].Content = results.Content[:1]
	if _, err := d.resume(t.Context(), &partial, registry, complete[:1]); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("partial retired batch was accepted")
	}
	if !d.outcome.expires.After(time.Now().Add(4*time.Minute)) || d.outcome.expires.After(time.Now().Add(5*time.Minute)) {
		t.Fatal("retired outcome exceeded its declared lifetime")
	}
	d.outcome.expires = time.Now().Add(-time.Second)
	if _, err := d.resume(t.Context(), &next, registry, complete); !errors.Is(err, inference.ErrRequest) || d.outcome != nil {
		t.Fatal("expired outcome remained available")
	}
}

type outcomeValidator struct{}

func (outcomeValidator) Check(context.Context, []byte) error            { return nil }
func (outcomeValidator) Validate(context.Context, []byte, []byte) error { return nil }

func TestAbortPreservesKnownDeadlineBeforeCleanup(t *testing.T) {
	for _, tc := range []struct {
		name              string
		owned, tool, auth bool
		reason            error
		want              error
	}{
		{"caller", false, false, false, context.Canceled, context.Canceled},
		{"owned", true, false, false, context.Canceled, context.DeadlineExceeded},
		{"tool", false, true, false, context.Canceled, relay.ErrTimeout},
		{"both", true, true, false, context.Canceled, relay.ErrTimeout},
		{"authentication", true, true, true, context.Canceled, acp.ErrAuthentication},
		{"protocol", false, false, false, acp.ErrProtocol, acp.ErrProtocol},
		{"end-after-tool-expiry", false, true, false, acp.ErrProtocol, relay.ErrTimeout},
		{"rpc-owned-expiry", true, false, false, acp.ErrTimeout, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := toolregistry.Build(t.Context(), []json.RawMessage{json.RawMessage(`{"name":"client_action","input_schema":{"type":"object"}}`)}, nil, outcomeValidator{})
			if err != nil {
				t.Fatal("registry fixture preparation failed")
			}
			limit := time.Minute
			if tc.tool {
				limit = 2 * time.Millisecond
			}
			broker, err := relay.NewBroker(registry, relay.Limits{ToolTimeout: limit})
			if err != nil || broker.BeginTurn() != nil {
				t.Fatal("relay fixture preparation failed")
			}
			t.Cleanup(broker.Close)
			callDone := make(chan struct{})
			go func() {
				c := broker.Credentials()
				_, _ = broker.Call(context.Background(), relay.Call{Version: 1, Owner: c.Owner, Secret: c.Secret, ID: "owned-call", Alias: registry.Tools()[0].Alias, Arguments: json.RawMessage(`{}`)})
				close(callDone)
			}()
			select {
			case <-broker.Ready():
			case <-time.After(time.Second):
				t.Fatal("relay fixture did not admit the call")
			}
			if tc.tool {
				select {
				case <-broker.Done():
				case <-time.After(time.Second):
					t.Fatal("relay fixture deadline did not fire")
				}
			}
			owned, cancel := context.WithCancel(context.Background())
			if tc.owned {
				cancel()
				owned, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer cancel()
			done := make(chan struct{})
			close(done)
			d := &Driver{state: WaitingTools, hasher: history.New([32]byte{1})}
			pending, err := d.hasher.Pending([]anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "owned fixture question"}}}}, []anthropic.Block{{Type: "tool_use", Raw: json.RawMessage(`{"type":"tool_use","id":"toolu_owned","name":"client_action","input":{}}`)}})
			if err != nil {
				t.Fatal("history fixture preparation failed")
			}
			tr := &turn{driver: d, client: &outcomePeer{authOnClose: tc.auth}, owned: owned, cancelOwned: cancel,
				done: done, released: make(chan struct{}), broker: broker, pendingHistory: pending, lastIDs: []string{"toolu_owned"}}
			d.current = tr
			r := &round{turn: tr}
			// Hold the watchdog out of this schedule: caller cancellation enters settlement first.
			if tc.reason == context.Canceled {
				r.Cancel()
			} else {
				if tc.name == "end-after-tool-expiry" && broker.EndTurn() == nil {
					t.Fatal("expired relay accepted prompt completion")
				}
				tr.abort(tc.reason)
			}
			var repeats sync.WaitGroup
			for range 8 {
				repeats.Go(r.Cancel)
			}
			repeats.Wait()
			if d.outcome == nil || !errors.Is(d.outcome.err, tc.want) || d.state != Unstarted {
				t.Fatal("cleanup cancellation replaced the known primary outcome")
			}
			select {
			case <-callDone:
			case <-time.After(time.Second):
				t.Fatal("retired relay call did not join")
			}
		})
	}
}
