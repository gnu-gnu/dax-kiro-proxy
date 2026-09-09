package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
)

type completedToolPair struct {
	use           anthropic.ToolUse
	input, result []byte
}

// Observe only the public Messages contract. Native transcript bytes are never decoded here.
func completedPair(r *anthropic.Request, resume bool) (completedToolPair, error) {
	var pair completedToolPair
	uses, results, old, next, answer := 0, 0, 0, 0, 0
	bad := errors.New("completed tool history mismatch")
	if r == nil || len(r.Messages) > 16 || !r.ClientContent() {
		return pair, bad
	}
	for index, message := range r.Messages {
		for _, block := range message.Content {
			if len(block.Raw) > 64<<10 || len(block.Text) > 64<<10 {
				return pair, bad
			}
			switch block.Type {
			case "text":
				if strings.Contains(block.Text, "UnsentEffect_139") {
					return pair, bad
				}
				if message.Role == "user" {
					old += strings.Count(block.Text, "EffectQuestion_131")
					n := strings.Count(block.Text, "EffectFollow_137")
					if n != 0 && (results != 1 || index != r.LatestUserIndex()) {
						return pair, bad
					}
					next += n
				}
				if message.Role == "assistant" {
					n := strings.Count(block.Text, "ToolArchiveReady_131")
					if n != 0 && (results != 1 || next != 0) {
						return pair, bad
					}
					answer += n
				}
			case "tool_use":
				uses++
				if uses != 1 || results != 0 || old != 1 || next != 0 || message.Role != "assistant" {
					return pair, bad
				}
				if json.Unmarshal(block.Raw, &pair.use) != nil || !pair.use.Valid() {
					return pair, bad
				}
				pair.input = canonicalToolObject(pair.use.Input)
				if pair.input == nil {
					return pair, bad
				}
			case "tool_result":
				results++
				value, err := anthropic.DecodeToolResult(block.Raw)
				if err != nil || uses != 1 || results != 1 || next != 0 || message.Role != "user" || value.ID != pair.use.ID || value.IsError || len(value.Content) == 0 {
					return pair, bad
				}
				var content []string
				for _, part := range value.Content {
					if part.Type != "text" || len(part.Text) > 64<<10 {
						return pair, bad
					}
					content = append(content, part.Text)
				}
				pair.result, _ = json.Marshal(content)
			default:
				return pair, bad
			}
		}
	}
	if uses != 1 || results != 1 || old != 1 || next != btoi(resume) || answer != btoi(resume) {
		return pair, bad
	}
	return pair, nil
}

func canonicalToolObject(raw []byte) []byte {
	if len(raw) > 64<<10 {
		return nil
	}
	if _, err := ndjson.Object(raw); err != nil {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return nil
	}
	result, _ := json.Marshal(value)
	return result
}

func sameCompletedPair(a, b completedToolPair) bool {
	return a.use.ID != "" && a.use.ID == b.use.ID && a.use.Name == b.use.Name && bytes.Equal(a.input, b.input) && bytes.Equal(a.result, b.result)
}

type toolRestartBackend struct {
	shape struct {
		Tools, Messages, InitialMarkers, OtherBlocks int
		ValidIdentity                                bool
	}
	models                      *catalog.Catalog
	backend                     inference.Backend
	stage                       int
	identity                    string
	expect                      *toolEffectExpectation
	previous                    completedToolPair
	observeProcess              func() error
	beforeUse                   func() bool
	mu                          sync.Mutex
	starts, uses, results, ends int
	failed                      bool
	text                        string
	pair                        completedToolPair
	issued                      anthropic.ToolUse
}

func (b *toolRestartBackend) Models(ctx context.Context) ([]inference.Model, error) {
	return b.models.List(), nil
}
func (b *toolRestartBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	b.mu.Lock()
	b.starts++
	n := b.starts
	if r != nil {
		b.shape.Tools, b.shape.Messages, b.shape.ValidIdentity = len(r.Tools), len(r.Messages), nativeHistoryID(r.Identity.Session)
		b.shape.InitialMarkers, b.shape.OtherBlocks = 0, 0
		for _, message := range r.Messages {
			for _, block := range message.Content {
				if message.Role == "user" && block.Type == "text" {
					b.shape.InitialMarkers += strings.Count(block.Text, "EffectQuestion_131")
				} else {
					b.shape.OtherBlocks++
				}
			}
		}
	}
	valid := r != nil && nativeHistoryID(r.Identity.Session) && len(r.Tools) == 1 && n <= 2-b.stage
	if valid && b.identity != "" {
		valid = r.Identity.Session == b.identity
	}
	if valid && b.stage == 0 && n == 1 {
		b.identity = r.Identity.Session
		valid = len(r.Messages) >= 1 && len(r.Messages) <= 2 && r.Messages[0].Role == "user" && r.LatestUserIndex() == 0
		count := 0
		for index, message := range r.Messages {
			valid = valid && (index == 0 || message.Role == "system")
			for _, block := range message.Content {
				valid = valid && block.Type == "text" && !strings.Contains(block.Text, "EffectFollow_137") && !strings.Contains(block.Text, "UnsentEffect_139")
				if index == 0 {
					count += strings.Count(block.Text, "EffectQuestion_131")
				}
			}
		}
		valid = valid && count == 1
	} else if valid {
		pair, err := completedPair(r, b.stage == 1)
		valid = err == nil && b.expect.matches(pair.use)
		if b.stage == 0 {
			valid = valid && b.uses == 1 && pair.use.ID == b.issued.ID && bytes.Equal(pair.input, canonicalToolObject(b.issued.Input))
		} else {
			valid = valid && sameCompletedPair(pair, b.previous)
		}
		if valid {
			b.pair = pair
			b.results++
		}
	}
	if !valid {
		b.failed = true
	}
	b.mu.Unlock()
	if !valid {
		return nil, inference.ErrRequest
	}
	turn, err := b.backend.Start(ctx, r)
	if err != nil {
		return nil, err
	}
	return &toolRestartTurn{Turn: turn, owner: b, request: n}, nil
}

type toolRestartTurn struct {
	inference.Turn
	owner   *toolRestartBackend
	request int
}

func (t *toolRestartTurn) Next(ctx context.Context) (inference.Event, error) {
	event, err := t.Turn.Next(ctx)
	if err != nil {
		return event, err
	}
	b := t.owner
	b.mu.Lock()
	valid := b.observeProcess() == nil
	switch event.Kind {
	case inference.Tools:
		valid = valid && b.stage == 0 && t.request == 1 && b.uses == 0 && len(event.Tools) == 1
		if valid {
			valid = b.expect.matches(event.Tools[0]) && b.beforeUse()
			if valid {
				b.issued = event.Tools[0]
				b.uses++
			}
		}
	case inference.Text:
		valid = valid && b.ends == 0 && len(b.text)+len(event.Text) <= 16<<10
		if valid {
			b.text += event.Text
		}
	case inference.End:
		if event.StopReason == "tool_use" {
			valid = valid && b.stage == 0 && t.request == 1 && b.uses == 1
		} else {
			valid = valid && event.StopReason == "end_turn" && t.request == 2-b.stage && b.ends == 0 && b.results == 1
			if valid {
				b.ends++
			}
		}
	default:
		valid = false
	}
	if !valid {
		b.failed = true
	}
	b.mu.Unlock()
	if !valid {
		t.Cancel()
		return inference.Event{}, inference.ErrRequest
	}
	return event, nil
}

func TestCompletedToolHistoryRejectsChangedOrReorderedPairs(t *testing.T) {
	base := `{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":"EffectQuestion_131"},{"role":"assistant","content":[{"type":"tool_use","id":"owned-call","name":"Write","input":{"file_path":"/owned/fixture","content":"fixture"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"owned-call","content":"created"}]},{"role":"assistant","content":"ToolArchiveReady_131"},{"role":"user","content":"EffectFollow_137"}]}`
	decode := func(raw string) *anthropic.Request {
		r, err := anthropic.DecodeRequest([]byte(raw))
		if err != nil {
			t.Fatal("invalid independent guard fixture")
		}
		return r
	}
	want, err := completedPair(decode(base), true)
	if err != nil {
		t.Fatal("valid completed pair rejected")
	}
	for _, replacement := range [][2]string{
		{`"tool_use_id":"owned-call"`, `"tool_use_id":"foreign-call"`},
		{`"content":"created"`, `"content":"changed"`},
		{`"content":"created"`, `"content":"created","is_error":true`},
		{`"file_path":"/owned/fixture"`, `"file_path":"/other/fixture"`},
		{`"content":"fixture"`, `"content":"changed"`},
		{`"name":"Write"`, `"name":"Read"`},
		{`EffectQuestion_131`, `UnsentEffect_139`},
		{`EffectFollow_137`, `EffectFollow_137 EffectFollow_137`},
		{`ToolArchiveReady_131`, `absent`},
	} {
		pair, err := completedPair(decode(strings.Replace(base, replacement[0], replacement[1], 1)), true)
		if err == nil && sameCompletedPair(pair, want) {
			t.Fatal("changed history earned a match")
		}
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) { r.Messages[1], r.Messages[2] = r.Messages[2], r.Messages[1] },
		func(r *anthropic.Request) { r.Messages[1], r.Messages[3] = r.Messages[3], r.Messages[1] },
		func(r *anthropic.Request) {
			r.Messages[2].Content = append(r.Messages[2].Content, r.Messages[2].Content[0])
		},
		func(r *anthropic.Request) {
			r.Messages[1].Content = append(r.Messages[1].Content, r.Messages[1].Content[0])
		},
		func(r *anthropic.Request) { r.Messages[2], r.Messages[4] = r.Messages[4], r.Messages[2] },
		func(r *anthropic.Request) { r.Messages = append(r.Messages[:2], r.Messages[3:]...) },
	} {
		r := decode(base)
		mutate(r)
		if _, err := completedPair(r, true); err == nil {
			t.Fatal("missing, duplicate or reordered history was accepted")
		}
	}
	if sameCompletedPair(completedToolPair{}, completedToolPair{}) {
		t.Fatal("absent pairs matched")
	}
}

func TestCompletedToolResumeCannotExposeANewTool(t *testing.T) {
	var cancelled atomic.Int32
	call := anthropic.ToolUse{ID: "unexpected-repeat", Name: "Write", Input: json.RawMessage(`{"file_path":"/owned/fixture","content":"fixture"}`)}
	b := &toolRestartBackend{stage: 1, observeProcess: func() error { return nil }}
	turn := &toolRestartTurn{owner: b, request: 1, Turn: &denialTurnFixture{events: []inference.Event{{Kind: inference.Tools, Tools: []anthropic.ToolUse{call}}}, cancel: &cancelled}}
	if _, err := turn.Next(t.Context()); !errors.Is(err, inference.ErrRequest) || cancelled.Load() != 1 || b.uses != 0 || !b.failed {
		t.Fatal("unexpected resumed tool reached the native client")
	}
}
