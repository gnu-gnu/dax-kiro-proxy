package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/session"
)

const clientDenialReason = "independent fixture denial"

type denialDriver interface {
	Start(context.Context, *anthropic.Request) (inference.Turn, error)
	State() session.State
}

type denialProbeStats struct {
	AfterInterruption                           interruptedRequestShape
	Starts, Uses, Results, Denials, Completions int
	CanaryObserved                              bool
	LastShape                                   probeRequestShape
	Titles                                      int
}

type interruptedRequestShape struct {
	Messages, LatestUser, Uses, Results, MatchingResults, Errors, ResultMessage int
	LatestTypes                                                                 []string
	NextQuestion, InterruptionText                                              bool
}

func inspectInterruptedRequest(r *anthropic.Request, id string) interruptedRequestShape {
	s := interruptedRequestShape{Messages: len(r.Messages), LatestUser: r.LatestUserIndex(), ResultMessage: -1}
	for i, message := range r.Messages {
		for _, block := range message.Content {
			if i == s.LatestUser && len(s.LatestTypes) < 8 {
				kind := block.Type
				if kind != "text" && kind != "tool_result" {
					kind = "other"
				}
				s.LatestTypes = append(s.LatestTypes, kind)
			}
			if block.Type == "tool_use" {
				s.Uses++
			}
			if block.Type == "text" && i == s.LatestUser && strings.Contains(block.Text, "Next independent question.") {
				s.NextQuestion = true
			}
			if block.Type != "tool_result" {
				continue
			}
			result, err := anthropic.DecodeToolResult(block.Raw)
			if err != nil {
				continue
			}
			s.Results++
			s.ResultMessage = i
			if result.ID == id {
				s.MatchingResults++
			}
			if result.IsError {
				s.Errors++
			}
			for _, content := range result.Content {
				if content.Type == "text" && strings.Contains(strings.ToLower(content.Text), "interrupted") {
					s.InterruptionText = true
				}
			}
		}
	}
	return s
}

func TestInterruptedRequestObservationIsBoundedAndDoesNotDispatch(t *testing.T) {
	b, f := newDenialBackendFixture()
	b.inspectResume = true
	first, err := b.Start(t.Context(), denialRequestFixture("", "", false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Next(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := denialRequestFixture("fixture-use", "private-denial-content", true)
	r.Messages[0].Content = append(r.Messages[0].Content, anthropic.Block{Type: "text", Text: "Next independent question."})
	for range 20 {
		r.Messages[0].Content = append(r.Messages[0].Content, anthropic.Block{Type: "private-type-content"})
	}
	if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("observation dispatched a second prompt")
	}
	s := b.snapshot().AfterInterruption
	raw, _ := json.Marshal(s)
	if f.starts != 1 || !b.inspected.Load() || s.Results != 1 || s.MatchingResults != 1 || s.Errors != 1 || !s.NextQuestion || len(s.LatestTypes) != 8 || strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "fixture-use") {
		t.Fatal("observation exceeded its bounds or retained content")
	}
	if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 1 {
		t.Fatal("observation budget allowed a retry")
	}
}

type probeRequestShape struct {
	Tools, Messages                    int
	Thinking, Context, EndConversation bool
}

// This test-only adapter admits one initial request and one matching result continuation.
// A second ordinary request cannot start another model turn, including after timeout or failure.
type onePromptBackend struct {
	inspectResume    bool
	recoverResume    bool
	inspected        atomic.Bool
	driver           denialDriver
	models           *catalog.Catalog
	readPath, canary string
	observeUse       func() error
	effect           *toolEffectExpectation
	allowTitles      bool
	titleAttempts    atomic.Int32
	attempts         atomic.Int32
	mu               sync.Mutex
	callID, tail     string
	stats            denialProbeStats
}

func (b *onePromptBackend) Models(context.Context) ([]inference.Model, error) {
	return b.models.List(), nil
}

func (b *onePromptBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if r != nil && b.allowTitles && requestfamily.Classify(r) == requestfamily.Title {
		if b.titleAttempts.Add(1) > 2 {
			return nil, inference.ErrRequest
		}
		b.mu.Lock()
		b.stats.Titles++
		b.mu.Unlock()
		return &completionDisplayTurn{model: r.Model, text: `{"title":"Owned permission exercise"}`}, nil
	}
	n := b.attempts.Add(1)
	if n == 2 && r != nil && b.inspectResume {
		b.mu.Lock()
		b.stats.AfterInterruption = inspectInterruptedRequest(r, b.callID)
		b.mu.Unlock()
		b.inspected.Store(true)
		if !b.recoverResume {
			return nil, inference.ErrRequest
		}
	}
	if r != nil {
		shape := probeRequestShape{Tools: len(r.Tools), Messages: len(r.Messages), Thinking: r.Extra["thinking"] != nil, Context: r.Extra["context_management"] != nil}
		for _, raw := range r.Tools {
			var tool struct{ Name string }
			if json.Unmarshal(raw, &tool) == nil && tool.Name == "EndConversation" {
				shape.EndConversation = true
			}
		}
		b.mu.Lock()
		b.stats.LastShape = shape
		b.mu.Unlock()
	}
	if n > 2 || r == nil || len(r.Tools) != 1 {
		return nil, inference.ErrRequest
	}
	for _, field := range []string{"thinking", "context_management"} {
		if _, present := r.Extra[field]; present {
			return nil, inference.ErrRequest
		}
	}
	tool, err := ndjson.Object(r.Tools[0])
	expectedTool := "Read"
	if b.effect != nil {
		expectedTool = b.effect.Tool
	}
	if err != nil || string(tool["name"]) != `"`+expectedTool+`"` {
		return nil, inference.ErrRequest
	}
	results, err := r.LatestToolResults()
	if err != nil {
		return nil, inference.ErrRequest
	}
	if n == 1 {
		if len(results) != 0 || b.driver.State() != session.Unstarted {
			return nil, inference.ErrRequest
		}
	} else {
		b.mu.Lock()
		id := b.callID
		b.mu.Unlock()
		wantError := b.effect == nil || b.effect.IsError
		if len(results) != 1 || id == "" || results[0].ID != id || results[0].IsError != wantError || b.driver.State() != session.WaitingTools {
			return nil, inference.ErrRequest
		}
		if b.effect != nil {
			b.mu.Lock()
			valid := b.effect.acceptResult(b, results[0].Content)
			b.mu.Unlock()
			if !valid {
				return nil, inference.ErrRequest
			}
		} else {
			denied := false
			for _, block := range results[0].Content {
				if block.Type != "text" {
					return nil, inference.ErrRequest
				}
				b.mu.Lock()
				leaked := b.observeCanary(block.Text)
				b.mu.Unlock()
				if leaked {
					return nil, inference.ErrRequest
				}
				denied = denied || strings.Contains(block.Text, clientDenialReason)
			}
			if !denied {
				return nil, inference.ErrRequest
			}
		}
	}
	b.mu.Lock()
	b.stats.Starts++
	b.mu.Unlock()
	turn, err := b.driver.Start(ctx, r)
	if err != nil {
		return nil, err
	}
	if n == 2 {
		b.mu.Lock()
		b.stats.Results++
		if results[0].IsError {
			b.stats.Denials++
		}
		b.mu.Unlock()
	}
	return &denialProbeTurn{Turn: turn, owner: b}, nil
}

func (b *onePromptBackend) snapshot() denialProbeStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats
}

// The caller holds mu. Retain only a copied boundary fragment, never a complete output chunk.
func (b *onePromptBackend) observeCanary(text string) bool {
	fragment := b.tail + text
	if b.canary != "" && strings.Contains(fragment, b.canary) {
		b.stats.CanaryObserved = true
	}
	keep := min(len(fragment), max(0, len(b.canary)-1))
	b.tail = strings.Clone(fragment[len(fragment)-keep:])
	return b.stats.CanaryObserved
}

type denialProbeTurn struct {
	inference.Turn
	owner *onePromptBackend
}

func (t *denialProbeTurn) Next(ctx context.Context) (inference.Event, error) {
	event, err := t.Turn.Next(ctx)
	if err != nil {
		return event, err
	}
	b := t.owner
	b.mu.Lock()
	valid := true
	switch event.Kind {
	case inference.Text:
		valid = !b.observeCanary(event.Text)
	case inference.Tools:
		if len(event.Tools) != 1 || b.stats.Uses != 0 {
			valid = false
		} else {
			use := event.Tools[0]
			var args struct {
				File string `json:"file_path"`
			}
			callMatches := use.Name == "Read" && json.Unmarshal(use.Input, &args) == nil && args.File == b.readPath
			if b.effect != nil {
				callMatches = b.effect.matches(use)
			}
			if !callMatches || use.ID == "" || b.observeCanary(string(use.Input)) {
				valid = false
			} else {
				if b.observeUse != nil && b.observeUse() != nil {
					valid = false
				} else {
					b.callID = use.ID
					b.stats.Uses++
				}
			}
		}
	case inference.End:
		if event.StopReason == "end_turn" {
			b.stats.Completions++
		}
	}
	b.mu.Unlock()
	if !valid {
		t.Turn.Cancel()
		return inference.Event{}, inference.ErrRequest
	}
	return event, nil
}

// Owned experiments authorize a single exact argument object, not a tool-wide permission bypass.
type toolEffectExpectation struct {
	Tool                string
	Input               json.RawMessage
	IsError, ReadCanary bool
	RequiredText        string
}

func (e *toolEffectExpectation) matches(use anthropic.ToolUse) bool {
	if use.Name != e.Tool {
		return false
	}
	a, errA := ndjson.Object(e.Input)
	b, errB := ndjson.Object(use.Input)
	return errA == nil && errB == nil && reflect.DeepEqual(a, b)
}

// The caller holds the observation mutex. Only the allowed Read can return its canary; neither
// generated tool inputs nor model output can expose it. Retain only a match-length fragment.
func (e *toolEffectExpectation) acceptResult(b *onePromptBackend, content []anthropic.Block) bool {
	found, tail, size := e.RequiredText == "", "", 0
	if e.ReadCanary && (e.Tool != "Read" || e.IsError || e.RequiredText != b.canary) {
		return false
	}
	for _, block := range content {
		size += len(block.Text)
		if block.Type != "text" || size > 64<<10 {
			return false
		}
		if !e.ReadCanary && b.observeCanary(block.Text) {
			return false
		}
		joined := tail + block.Text
		found = found || strings.Contains(joined, e.RequiredText)
		keep := min(len(joined), max(0, len(e.RequiredText)-1))
		tail = strings.Clone(joined[len(joined)-keep:])
	}
	return found
}

type denialDriverFixture struct {
	mu        sync.Mutex
	state     session.State
	starts    int
	first     []inference.Event
	cancelled atomic.Int32
}

func (f *denialDriverFixture) State() session.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *denialDriverFixture) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	events := f.first
	f.state = session.WaitingTools
	if f.starts == 2 {
		events = []inference.Event{{Kind: inference.End, StopReason: "end_turn"}}
		f.state = session.Idle
	}
	return &denialTurnFixture{events: events, cancel: &f.cancelled}, nil
}

type denialTurnFixture struct {
	events []inference.Event
	cancel *atomic.Int32
}

func (*denialTurnFixture) Model() string { return "fixture-model" }
func (*denialTurnFixture) Finish()       {}
func (f *denialTurnFixture) Cancel()     { f.cancel.Add(1) }
func (f *denialTurnFixture) Next(context.Context) (inference.Event, error) {
	if len(f.events) == 0 {
		return inference.Event{}, io.EOF
	}
	event := f.events[0]
	f.events = f.events[1:]
	return event, nil
}

func denialRequestFixture(id, reason string, isError bool) *anthropic.Request {
	block := anthropic.Block{Type: "text", Text: "Independent tool request"}
	if id != "" {
		raw, _ := json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isError, "content": reason})
		block = anthropic.Block{Type: "tool_result", Raw: raw}
	}
	return &anthropic.Request{Model: "fixture-model", Tools: []json.RawMessage{json.RawMessage(`{"name":"Read","input_schema":{"type":"object"}}`)}, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{block}}}}
}

func newDenialBackendFixture() (*onePromptBackend, *denialDriverFixture) {
	f := &denialDriverFixture{state: session.Unstarted, first: []inference.Event{
		{Kind: inference.Tools, Tools: []anthropic.ToolUse{{ID: "fixture-use", Name: "Read", Input: json.RawMessage(`{"file_path":"/independent/fixture"}`)}}},
		{Kind: inference.End, StopReason: "tool_use"},
	}}
	return &onePromptBackend{driver: f, readPath: "/independent/fixture", canary: "synthetic-canary-value"}, f
}

func TestOnePromptProbeRejectsAdditionalTurnsAndUnprovenResults(t *testing.T) {
	for _, test := range []struct {
		name, id, reason string
		isError, valid   bool
	}{
		{"matching-denial", "fixture-use", clientDenialReason, true, true},
		{"new-ordinary-turn", "", "", false, false},
		{"foreign-result", "another-use", clientDenialReason, true, false},
		{"successful-tool", "fixture-use", clientDenialReason, false, false},
		{"unproven-denial", "fixture-use", "different error", true, false},
		{"canary-in-result", "fixture-use", clientDenialReason + " synthetic-canary-value", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, f := newDenialBackendFixture()
			turn, err := b.Start(t.Context(), denialRequestFixture("", "", false))
			if err != nil {
				t.Fatal(err)
			}
			if event, err := turn.Next(t.Context()); err != nil || event.Kind != inference.Tools {
				t.Fatal("declared fixture tool was not exposed")
			}
			second, err := b.Start(t.Context(), denialRequestFixture(test.id, test.reason, test.isError))
			if test.valid {
				if err != nil || f.starts != 2 {
					t.Fatal("matching client refusal was not admitted")
				}
				if _, err := second.Next(t.Context()); err != nil || b.snapshot().Denials != 1 || b.snapshot().Completions != 1 {
					t.Fatal("matching continuation did not complete")
				}
			} else if !errors.Is(err, inference.ErrRequest) || f.starts != 1 {
				t.Fatal("unproven result reached the driver")
			}
			if test.name == "canary-in-result" && !b.snapshot().CanaryObserved {
				t.Fatal("client result leaked the canary without recording the failed check")
			}
			if _, err := b.Start(t.Context(), denialRequestFixture("", "", false)); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("exhausted observation admitted another turn")
			}
		})
	}
}

func TestOnePromptProbeBoundsConcurrentInitialRequests(t *testing.T) {
	b, f := newDenialBackendFixture()
	var work sync.WaitGroup
	for range 8 {
		work.Go(func() { _, _ = b.Start(t.Context(), denialRequestFixture("", "", false)) })
	}
	work.Wait()
	if f.starts != 1 || b.snapshot().Starts != 1 {
		t.Fatal("concurrent requests started multiple initial turns")
	}
}

func TestOnePromptProbeRejectsUnexpectedToolAndSplitCanary(t *testing.T) {
	for _, name := range []string{"tool-name", "tool-path", "multiple-tools", "split-canary"} {
		t.Run(name, func(t *testing.T) {
			b, f := newDenialBackendFixture()
			switch name {
			case "tool-name":
				f.first[0].Tools[0].Name = "Write"
			case "tool-path":
				f.first[0].Tools[0].Input = json.RawMessage(`{"file_path":"/another/fixture"}`)
			case "multiple-tools":
				f.first[0].Tools = append(f.first[0].Tools, f.first[0].Tools[0])
			case "split-canary":
				f.first = []inference.Event{{Kind: inference.Text, Text: "synthetic-can"}, {Kind: inference.Text, Text: "ary-value"}}
			}
			turn, err := b.Start(t.Context(), denialRequestFixture("", "", false))
			if err != nil {
				t.Fatal(err)
			}
			if name == "split-canary" {
				if _, err := turn.Next(t.Context()); err != nil {
					t.Fatal("first fragment failed before the canary was present")
				}
			}
			if _, err := turn.Next(t.Context()); !errors.Is(err, inference.ErrRequest) || f.cancelled.Load() != 1 {
				t.Fatal("unexpected output did not cancel the owned turn")
			}
			if name == "split-canary" && !b.snapshot().CanaryObserved {
				t.Fatal("canary observation was not retained")
			}
		})
	}
}
