package relay

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Delay evaluation of the final wait until resolution and cancellation are both ready.
// Before admission, WithCancel observes the same underlying Done channel without a wait.
type admittedWaitContext struct {
	context.Context
	broker  *Broker
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *admittedWaitContext) Done() <-chan struct{} {
	if c.broker.Stats().Queued > 0 {
		c.once.Do(func() { close(c.entered) })
		<-c.release
	}
	return c.Context.Done()
}

func TestResolvedCallCancellationPreservesNewWork(t *testing.T) {
	for _, stage := range []string{"queued", "sealed", "delivered", "next-turn"} {
		t.Run(stage, func(t *testing.T) {
			// With both select arms ready, exercise either choice repeatedly.
			for range 32 {
				b, credentials, alias := fixtureBroker(t, func(l *Limits) { l.ToolTimeout = 5 * time.Second })
				ctx, cancel := context.WithCancel(context.Background())
				wait := &admittedWaitContext{Context: ctx, broker: b, entered: make(chan struct{}), release: make(chan struct{})}
				var release sync.Once
				unblock := func() { release.Do(func() { close(wait.release) }) }
				t.Cleanup(unblock)
				t.Cleanup(cancel)
				done := make(chan resultOutcome, 1)
				go func() {
					v, err := b.Call(wait, Call{Version: 1, Owner: credentials.Owner, Secret: credentials.Secret,
						ID: "resolved", Alias: alias, Arguments: json.RawMessage(`{"n":1}`)})
					done <- resultOutcome{v, err}
				}()
				select {
				case <-wait.entered:
				case <-time.After(time.Second):
					t.Fatal("call did not reach admitted wait")
				}
				first, err := b.Seal()
				if err != nil || b.Delivered(first.Number) != nil {
					t.Fatal("first handoff failed")
				}
				committed := ToolResult{Content: []Content{{Type: "text", Text: "committed fixture result"}}}
				if err := b.Resolve(credentials.Owner, []Result{{ID: first.Calls[0].ID, ToolResult: committed}}); err != nil {
					t.Fatal("first resolution failed")
				}
				if stage == "next-turn" && (b.EndTurn() != nil || b.BeginTurn() != nil) {
					t.Fatal("next turn admission failed")
				}
				nextDone := callFixture(b, credentials, alias, "next")
				waitQueued(t, b, 1)
				var next Batch
				if stage != "queued" {
					next, err = b.Seal()
					if err != nil {
						t.Fatal("next batch seal failed")
					}
					if stage != "sealed" && b.Delivered(next.Number) != nil {
						t.Fatal("next batch delivery failed")
					}
				}
				before := b.Stats()
				cancel()
				unblock()
				if got := awaitCall(t, done); got.err != nil || !reflect.DeepEqual(got.value, committed) {
					t.Fatal("committed result was changed by late cancellation")
				}
				if b.Err() != nil || b.Stats() != before {
					t.Fatal("resolved call cancellation retired unrelated work")
				}
				noResult(t, nextDone)
				if stage == "queued" {
					next, err = b.Seal()
					if err != nil {
						t.Fatal("queued next batch was lost")
					}
				}
				if (stage == "queued" || stage == "sealed") && b.Delivered(next.Number) != nil {
					t.Fatal("next batch delivery was lost")
				}
				if err := b.Resolve(credentials.Owner, []Result{{ID: next.Calls[0].ID, ToolResult: committed}}); err != nil {
					t.Fatal("next result was rejected")
				}
				if got := awaitCall(t, nextDone); got.err != nil || !reflect.DeepEqual(got.value, committed) {
					t.Fatal("next result did not complete exactly")
				}
				if b.EndTurn() != nil || b.Stats().Pending != 0 {
					t.Fatal("completed ownership remained")
				}
				b.Close()
			}
		})
	}
}

func awaitCall(t *testing.T, done <-chan resultOutcome) resultOutcome {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(time.Second):
		t.Fatal("call did not finish")
		return resultOutcome{}
	}
}

func TestUnresolvedCallCancellationRetiresCompleteOwnership(t *testing.T) {
	for _, stage := range []string{"queued", "sealed", "delivered"} {
		t.Run(stage, func(t *testing.T) {
			b, credentials, alias := fixtureBroker(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			one := make(chan resultOutcome, 1)
			go func() {
				v, err := b.Call(ctx, Call{Version: 1, Owner: credentials.Owner, Secret: credentials.Secret,
					ID: "cancelled", Alias: alias, Arguments: json.RawMessage(`{"n":1}`)})
				one <- resultOutcome{v, err}
			}()
			two := callFixture(b, credentials, alias, "sibling")
			waitQueued(t, b, 2)
			var batch Batch
			if stage != "queued" {
				var err error
				batch, err = b.Seal()
				if err != nil || len(batch.Calls) != 2 {
					t.Fatal("complete batch seal failed")
				}
				if stage == "delivered" && b.Delivered(batch.Number) != nil {
					t.Fatal("complete batch delivery failed")
				}
			}
			cancel()
			cancel()
			for _, done := range []<-chan resultOutcome{one, two} {
				if got := awaitCall(t, done); got.err != nil || !got.value.IsError {
					t.Fatal("unresolved cancellation did not return a tool error")
				}
			}
			if !errors.Is(b.Err(), context.Canceled) || b.Stats().Pending != 0 || b.Stats().Queued != 0 || b.Stats().Sealed != 0 {
				t.Fatal("unresolved cancellation retained ownership")
			}
			if stage != "queued" && b.Resolve(credentials.Owner, []Result{{ID: batch.Calls[0].ID}, {ID: batch.Calls[1].ID}}) == nil {
				t.Fatal("retired result batch was accepted")
			}
			if b.BeginTurn() == nil {
				t.Fatal("cancelled relay was reused")
			}
		})
	}
}
