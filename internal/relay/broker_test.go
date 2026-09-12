package relay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/toolregistry"
)

type integerFixture struct{}

func (integerFixture) Check(context.Context, []byte) error { return nil }
func (integerFixture) Validate(_ context.Context, _ []byte, args []byte) error {
	var fields struct {
		N int `json:"n"`
	}
	if json.Unmarshal(args, &fields) != nil || fields.N != 1 {
		return errors.New("fixture invalid")
	}
	return nil
}
func fixtureBroker(t *testing.T, adjust func(*Limits)) (*Broker, Credentials, string) {
	t.Helper()
	r, err := toolregistry.Build(context.Background(), []json.RawMessage{json.RawMessage(`{"name":"client_action","input_schema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}`)}, nil, integerFixture{})
	if err != nil {
		t.Fatal(err)
	}
	l := Limits{ToolTimeout: time.Second}
	if adjust != nil {
		adjust(&l)
	}
	b, err := NewBroker(r, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	if err := b.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	return b, b.Credentials(), r.Tools()[0].Alias
}

func TestCallsRequireAnActiveTurnAndCannotOutliveItsCompletion(t *testing.T) {
	b, c, alias := fixtureBroker(t, nil)
	if err := b.EndTurn(); err != nil {
		t.Fatal(err)
	}
	call := Call{Version: 1, Owner: c.Owner, Secret: c.Secret, ID: "idle-attempt", Alias: alias, Arguments: json.RawMessage(`{"n":1}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := b.Call(ctx, call); !errors.Is(err, ErrCall) {
		t.Fatal("idle relay accepted a tool request")
	}
	if s := b.Stats(); s.Pending != 0 || s.Seen != 0 {
		t.Fatal("idle request reserved tool state")
	}
	if err := b.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	done := callFixture(b, c, alias, "during-turn")
	waitQueued(t, b, 1)
	if err := b.EndTurn(); err == nil {
		t.Fatal("prompt completed while a client tool was suspended")
	}
	select {
	case out := <-done:
		if !out.value.IsError {
			t.Fatal("unfinished tool did not receive an error")
		}
	case <-time.After(time.Second):
		t.Fatal("unfinished tool leaked beyond the turn")
	}
	if err := b.BeginTurn(); err == nil {
		t.Fatal("ambiguous relay state was reused")
	}
}

type resultOutcome struct {
	value ToolResult
	err   error
}

func callFixture(b *Broker, c Credentials, alias, id string) <-chan resultOutcome {
	done := make(chan resultOutcome, 1)
	go func() {
		v, err := b.Call(context.Background(), Call{Version: 1, Owner: c.Owner, Secret: c.Secret, ID: id, Alias: alias, Arguments: json.RawMessage(`{"n":1}`)})
		done <- resultOutcome{v, err}
	}()
	return done
}
func waitQueued(t *testing.T, b *Broker, n int) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for b.Stats().Queued < n && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if b.Stats().Queued < n {
		t.Fatal("call not queued")
	}
}
func noResult(t *testing.T, done <-chan resultOutcome) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("unresolved tool reached Kiro")
	default:
	}
}

func TestAbandonDeliveredResultsValidatesBeforeRetiringWithoutForwarding(t *testing.T) {
	b, c, alias := fixtureBroker(t, nil)
	one := callFixture(b, c, alias, "retired-call")
	waitQueued(t, b, 1)
	batch, err := b.Seal()
	if err != nil {
		t.Fatal(err)
	}
	result := Result{ID: batch.Calls[0].ID, ToolResult: ToolResult{Content: []Content{{Type: "text", Text: "synthetic returned content"}}}}
	if err := b.Abandon(c.Owner, []Result{result}); !errors.Is(err, ErrResults) {
		t.Fatal("undelivered batch was abandoned")
	}
	if err := b.Delivered(batch.Number); err != nil {
		t.Fatal(err)
	}
	oversized := result
	oversized.Content = []Content{{Type: "text", Text: strings.Repeat("x", MaxFrameBytes)}}
	for _, invalid := range []struct {
		owner   string
		results []Result
	}{
		{"wrong-owner", []Result{result}}, {c.Owner, nil}, {c.Owner, []Result{result, result}}, {c.Owner, []Result{{ID: "wrong-id", ToolResult: result.ToolResult}}}, {c.Owner, []Result{oversized}},
	} {
		if err := b.Abandon(invalid.owner, invalid.results); !errors.Is(err, ErrResults) {
			t.Fatal("invalid retirement accepted")
		}
		if b.Err() != nil || b.Stats().Sealed != 1 {
			t.Fatal("invalid retirement changed ownership")
		}
		noResult(t, one)
	}
	// A later, undelivered call belongs to the old prompt and must also be cancelled.
	two := callFixture(b, c, alias, "queued-call")
	waitQueued(t, b, 1)
	if err := b.Abandon(c.Owner, []Result{result}); err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan resultOutcome{one, two} {
		select {
		case got := <-done:
			raw, _ := json.Marshal(got.value)
			if !got.value.IsError || strings.Contains(string(raw), "synthetic returned content") {
				t.Fatal("client result was forwarded into retired work")
			}
		case <-time.After(time.Second):
			t.Fatal("abandoned call remained suspended")
		}
	}
	if b.Err() == nil || b.Stats().Pending != 0 || b.Abandon(c.Owner, []Result{result}) == nil || b.Resolve(c.Owner, []Result{result}) == nil {
		t.Fatal("retired ownership was reusable")
	}
}
func TestSealedBatchOwnershipAndAtomicResults(t *testing.T) {
	b, c, alias := fixtureBroker(t, nil)
	one := callFixture(b, c, alias, "call-a")
	two := callFixture(b, c, alias, "call-b")
	waitQueued(t, b, 2)
	batch, err := b.Seal()
	if err != nil || len(batch.Calls) != 2 {
		t.Fatal("batch not sealed")
	}
	results := []Result{{ID: batch.Calls[0].ID, ToolResult: ToolResult{Content: []Content{{Type: "text", Text: "synthetic output"}}}}, {ID: batch.Calls[1].ID, ToolResult: ToolResult{Content: []Content{{Type: "image", Data: "AQID", MIMEType: "image/png"}}, IsError: true}}}
	if err := b.Resolve(c.Owner, results); err == nil {
		t.Fatal("results accepted before successful HTTP handoff")
	}
	if err := b.Delivered(batch.Number); err != nil {
		t.Fatal(err)
	}
	three := callFixture(b, c, alias, "call-c")
	waitQueued(t, b, 1)
	for _, invalid := range [][]Result{results[:1], append(append([]Result{}, results...), results[0]), {results[0], results[0]}, {{ID: "toolu_orphan", ToolResult: results[0].ToolResult}, results[1]}} {
		if err := b.Resolve(c.Owner, invalid); err == nil {
			t.Fatal("nonexact result set accepted")
		}
		noResult(t, one)
		noResult(t, two)
	}
	if err := b.Resolve("foreign-owner", results); err == nil {
		t.Fatal("cross-session result accepted")
	}
	if err := b.Resolve(c.Owner, results); err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan resultOutcome{one, two} {
		select {
		case out := <-done:
			if out.err != nil || len(out.value.Content) != 1 {
				t.Fatal("matching result lost")
			}
		case <-time.After(time.Second):
			t.Fatal("relay remained suspended")
		}
	}
	if err := b.Resolve(c.Owner, results); err == nil {
		t.Fatal("duplicate results accepted")
	}
	noResult(t, three)
	next, err := b.Seal()
	if err != nil || len(next.Calls) != 1 || next.Number == batch.Number {
		t.Fatal("late call joined closed batch")
	}
	if _, err := b.Call(context.Background(), Call{Version: 1, Owner: c.Owner, Secret: c.Secret, ID: "call-a", Alias: alias, Arguments: json.RawMessage(`{"n":1}`)}); err == nil {
		t.Fatal("relay call ID reused after completion")
	}
}
func TestRelayRejectsInvalidCallsAndBoundsAdmission(t *testing.T) {
	b, c, alias := fixtureBroker(t, func(l *Limits) { l.Pending = 1 })
	valid := Call{Version: 1, Owner: c.Owner, Secret: c.Secret, ID: "one", Alias: alias, Arguments: json.RawMessage(`{"n":1}`)}
	for _, change := range []func(*Call){func(v *Call) { v.Secret = "wrong" }, func(v *Call) { v.Owner = "foreign" }, func(v *Call) { v.Alias = "unknown" }, func(v *Call) { v.Arguments = json.RawMessage(`[]`) }, func(v *Call) { v.Arguments = json.RawMessage(`{"n":"bad"}`) }} {
		v := valid
		change(&v)
		if _, err := b.Call(context.Background(), v); err == nil {
			t.Fatal("invalid relay call accepted")
		}
	}
	done := callFixture(b, c, alias, "admitted")
	waitQueued(t, b, 1)
	if _, err := b.Call(context.Background(), valid); !errors.Is(err, ErrCapacity) {
		t.Fatalf("pending limit: %v", err)
	}
	batch, _ := b.Seal()
	_ = b.Delivered(batch.Number)
	oversize := []Result{{ID: batch.Calls[0].ID, ToolResult: ToolResult{Content: []Content{{Type: "text", Text: strings.Repeat("x", MaxFrameBytes)}}}}}
	if err := b.Resolve(c.Owner, oversize); err == nil {
		t.Fatal("encoded result limit ignored")
	}
	noResult(t, done)
	if err := b.Resolve(c.Owner, []Result{{ID: batch.Calls[0].ID, ToolResult: ToolResult{Content: []Content{{Type: "text", Text: "valid"}}}}}); err != nil {
		t.Fatal("invalid result consumed pending ID")
	}
}
func TestToolTimeoutAndRepeatedCancellation(t *testing.T) {
	b, c, alias := fixtureBroker(t, func(l *Limits) { l.ToolTimeout = 40 * time.Millisecond })
	done := callFixture(b, c, alias, "timeout")
	waitQueued(t, b, 1)
	batch, _ := b.Seal()
	_ = b.Delivered(batch.Number)
	select {
	case out := <-done:
		if out.err != nil || !out.value.IsError {
			t.Fatal("timeout did not resolve tool error")
		}
	case <-time.After(time.Second):
		t.Fatal("tool wait leaked")
	}
	if !errors.Is(b.Err(), ErrTimeout) {
		t.Fatal("timeout did not discard session relay")
	}
	if err := b.Resolve(c.Owner, []Result{{ID: batch.Calls[0].ID}}); err == nil {
		t.Fatal("late result accepted")
	}
	var closes sync.WaitGroup
	for range 8 {
		closes.Go(b.Close)
	}
	closes.Wait()
	if b.Stats().Pending != 0 {
		t.Fatal("pending calls retained")
	}
}

func TestResolvedCallCannotFailANewerBatch(t *testing.T) {
	b, c, alias := fixtureBroker(t, nil)
	one := callFixture(b, c, alias, "resolved")
	waitQueued(t, b, 1)
	batch, _ := b.Seal()
	_ = b.Delivered(batch.Number)
	b.mu.Lock()
	expired := b.pending[batch.Calls[0].ID]
	b.mu.Unlock()
	if err := b.Resolve(c.Owner, []Result{{ID: batch.Calls[0].ID}}); err != nil {
		t.Fatal(err)
	}
	<-one
	two := callFixture(b, c, alias, "newer")
	waitQueued(t, b, 1)
	b.failPending(batch.Calls[0].ID, expired, context.Canceled)
	b.expire(batch.Calls[0].ID, expired)
	if b.Err() != nil {
		t.Fatal("a completed call's cancellation or timer discarded a newer batch")
	}
	noResult(t, two)
}
