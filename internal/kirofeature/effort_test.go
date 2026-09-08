package kirofeature_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/kirofeature"
)

type rpc struct {
	calls   int
	methods []string
	params  []json.RawMessage
	result  json.RawMessage
	err     error
}

type waitingRPC struct{ entered chan struct{} }

func (r *waitingRPC) Call(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
	close(r.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestEffortStatusDoesNotWaitForPrivateCommand(t *testing.T) {
	c := kirofeature.NewEffort(nil)
	advertise(t, c, `[{"name":"/effort"}]`)
	r := &waitingRPC{entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = c.Sync(ctx, r, "session", "synthetic-model", "high") }()
	<-r.entered
	read := make(chan struct{})
	go func() { _ = c.Status(); close(read) }()
	select {
	case <-read:
	case <-time.After(50 * time.Millisecond):
		t.Error("cached status blocked on private command")
	}
	cancel()
	<-done
	<-read
}

func (r *rpc) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	r.calls++
	r.methods = append(r.methods, method)
	b, _ := json.Marshal(params)
	r.params = append(r.params, b)
	return r.result, r.err
}
func advertise(t *testing.T, c *kirofeature.Effort, commands string) {
	t.Helper()
	if err := c.Advertise([]byte(`{"commands":` + commands + `}`)); err != nil {
		t.Fatal(err)
	}
}
func TestEffortAvailabilityAndAutomaticModel(t *testing.T) {
	c := kirofeature.NewEffort(nil)
	r := &rpc{result: json.RawMessage(`{"success":true}`)}
	s, err := c.Sync(context.Background(), r, "session", "synthetic-model", "high")
	if err != nil || s.State != kirofeature.Unknown || r.calls != 0 {
		t.Fatal("unknown command availability was probed")
	}
	advertise(t, c, `[{"name":"/help"}]`)
	s, err = c.Sync(context.Background(), r, "session", "synthetic-model", "high")
	if err != nil || s.State != kirofeature.Unavailable || r.calls != 0 {
		t.Fatal("absent effort command was dispatched")
	}
	advertise(t, c, `[{"name":"/effort"}]`)
	s, err = c.Sync(context.Background(), r, "session", "auto", "high")
	if err != nil || r.calls != 0 {
		t.Fatal("automatic model received explicit effort")
	}
}
func TestEffortSuccessAndSwitchOrdering(t *testing.T) {
	c := kirofeature.NewEffort(nil)
	advertise(t, c, `[{"name":"/effort"}]`)
	r := &rpc{result: json.RawMessage(`{"success":true}`)}
	for range 2 {
		s, err := c.Sync(context.Background(), r, "session", "synthetic-model", "high")
		if err != nil || s.State != kirofeature.Current || s.Applied != "high" {
			t.Fatal("effort not applied")
		}
	}
	if r.calls != 1 || r.methods[0] != "_kiro.dev/commands/execute" {
		t.Fatal("redundant/wrong effort request")
	}
	var params struct {
		Session string `json:"sessionId"`
		Command struct {
			Name      string   `json:"name"`
			Arguments []string `json:"arguments"`
		} `json:"command"`
	}
	_ = json.Unmarshal(r.params[0], &params)
	if params.Session != "session" || params.Command.Name != "effort" || len(params.Command.Arguments) != 1 || params.Command.Arguments[0] != "high" {
		t.Fatal("effort command contract")
	}
	c.ModelChanged()
	if c.Status().Applied != "" {
		t.Fatal("model switch retained effective effort")
	}
	_, _ = c.Sync(context.Background(), r, "session", "synthetic-model", "high")
	if r.calls != 2 {
		t.Fatal("known supported effort not reapplied after switch")
	}
}
func TestRejectedProbeIsNotRepeatedAndTransportRemainsFatal(t *testing.T) {
	for _, failure := range []struct {
		result string
		err    error
	}{{`{"success":false}`, nil}, {`null`, nil}, {``, &acp.RemoteError{Code: -32601}}} {
		c := kirofeature.NewEffort(nil)
		advertise(t, c, `[{"name":"/effort"}]`)
		r := &rpc{result: json.RawMessage(failure.result), err: failure.err}
		for range 2 {
			s, err := c.Sync(context.Background(), r, "session", "synthetic-model", "high")
			if err != nil || !s.Rejected || s.Applied != "" {
				t.Fatal("optional rejection failed the turn or claimed applied effort")
			}
			c.ModelChanged()
		}
		if r.calls != 1 {
			t.Fatal("rejected unknown pair probed repeatedly")
		}
	}
	c := kirofeature.NewEffort(nil)
	advertise(t, c, `[{"name":"/effort"}]`)
	r := &rpc{err: acp.ErrTransport}
	if _, err := c.Sync(context.Background(), r, "session", "synthetic-model", "high"); !errors.Is(err, acp.ErrTransport) {
		t.Fatal("broken transport downgraded to optional failure")
	}
	c = kirofeature.NewEffort([]kirofeature.Pair{{Model: "synthetic-model", Effort: "high"}})
	advertise(t, c, `[{"name":"/effort"}]`)
	r = &rpc{}
	s, err := c.Sync(context.Background(), r, "session", "synthetic-model", "high")
	if err != nil || s.State != kirofeature.Unsupported || r.calls != 0 {
		t.Fatal("known unsupported pair was probed")
	}
}
