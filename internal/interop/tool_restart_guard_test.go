package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/requestfamily"
)

type completedToolPair struct {
	text          string
	use           anthropic.ToolUse
	input, result []byte
	failed        bool
}

// Observe only the public Messages contract. Native transcript bytes are never decoded here.
func completedPair(r *anthropic.Request, resume bool) (completedToolPair, error) {
	return recordedToolPair(r, resume, false)
}

func recordedToolPair(r *anthropic.Request, resume, interrupted bool) (completedToolPair, error) {
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
				if err != nil || uses != 1 || results != 1 || next != 0 || message.Role != "user" || value.ID != pair.use.ID || value.IsError != interrupted || len(value.Content) == 0 {
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
				pair.failed = value.IsError
			default:
				return pair, bad
			}
		}
	}
	if uses != 1 || results != 1 || old != 1 || next != btoi(resume) || answer != btoi(resume && !interrupted) {
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
	return a.use.ID != "" && a.use.ID == b.use.ID && a.use.Name == b.use.Name && bytes.Equal(a.input, b.input) && bytes.Equal(a.result, b.result) && a.failed == b.failed
}

type toolRestartBackend struct {
	allowTitles    bool
	titleAttempts  atomic.Int32
	followup       bool
	followQuestion string
	historyChecks  int
	question       string
	abandoned      bool
	historyForm    string
	placeholder    struct {
		AssistantBytes                                              int
		NoResponseRequested, NoContent, Aborted, Cancelled, Stopped bool
	}
	blockKinds         []string
	nativeInterruption struct {
		UserNotices, AssistantNotices, OtherNotices int
		PlainNotice, ToolNotice                     bool
		// NoticeText retains the client's own interruption wording (bounded), never user content.
		NoticeText string
	}
	interruptionShape struct {
		Uses, Results, ErrorResults, ResultTextBytes                  int
		HasErrorFlag, UseMatches, ResultMatches, MentionsInterruption bool
		ResultText                                                    string
	}
	interrupted bool
	handoffs    int
	shape       struct {
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
	if r != nil && b.allowTitles && requestfamily.Classify(r) == requestfamily.Title {
		if b.titleAttempts.Add(1) > 2 {
			return nil, inference.ErrRequest
		}
		return &completionDisplayTurn{model: r.Model, text: `{"title":"Owned resumed permission exercise"}`}, nil
	}
	b.mu.Lock()
	b.starts++
	n := b.starts
	if r != nil {
		b.shape.Tools, b.shape.Messages, b.shape.ValidIdentity = len(r.Tools), len(r.Messages), nativeHistoryID(r.Identity.Session)
		b.shape.InitialMarkers, b.shape.OtherBlocks = 0, 0
		for _, message := range r.Messages {
			for _, block := range message.Content {
				if b.interrupted && b.stage == 1 {
					if len(b.blockKinds) < 24 && len(message.Role) <= 16 && len(block.Type) <= 48 {
						kind := message.Role + "/" + block.Type + "#" + strconv.Itoa(len(block.Text))
						if block.Type == "text" && !strings.Contains(block.Text, "Effect") && !strings.Contains(block.Text, "ToolArchive") {
							kind += "=" + strconv.Quote(block.Text[:min(len(block.Text), 60)])
						}
						b.blockKinds = append(b.blockKinds, kind)
					}
					switch block.Type {
					case "text":
						if message.Role == "assistant" {
							b.placeholder.AssistantBytes += len(block.Text)
							b.placeholder.NoResponseRequested = b.placeholder.NoResponseRequested || strings.TrimSpace(block.Text) == "No response requested."
							b.placeholder.NoContent = b.placeholder.NoContent || strings.TrimSpace(block.Text) == "(no content)"
							lower := strings.ToLower(block.Text)
							b.placeholder.Aborted = b.placeholder.Aborted || strings.Contains(lower, "abort")
							b.placeholder.Cancelled = b.placeholder.Cancelled || strings.Contains(lower, "cancel")
							b.placeholder.Stopped = b.placeholder.Stopped || strings.Contains(lower, "stop")
						}
						if strings.Contains(strings.ToLower(block.Text), "interrupt") {
							if b.nativeInterruption.NoticeText == "" && len(block.Text) <= 96 {
								b.nativeInterruption.NoticeText = block.Text
							}
							switch message.Role {
							case "user":
								b.nativeInterruption.UserNotices++
							case "assistant":
								b.nativeInterruption.AssistantNotices++
							default:
								b.nativeInterruption.OtherNotices++
							}
						}
						b.nativeInterruption.PlainNotice = b.nativeInterruption.PlainNotice || block.Text == "[Request interrupted by user]"
						b.nativeInterruption.ToolNotice = b.nativeInterruption.ToolNotice || block.Text == "[Request interrupted by user for tool use]"
					case "tool_use":
						b.interruptionShape.Uses++
						var use anthropic.ToolUse
						// Identity is keyed on the old delivered call so a later new pair cannot overwrite it.
						if json.Unmarshal(block.Raw, &use) == nil && use.ID == b.previous.use.ID {
							b.interruptionShape.UseMatches = use.Name == b.previous.use.Name && bytes.Equal(canonicalToolObject(use.Input), b.previous.input)
						}
					case "tool_result":
						b.interruptionShape.Results++
						fields, _ := ndjson.Object(block.Raw)
						_, b.interruptionShape.HasErrorFlag = fields["is_error"]
						value, err := anthropic.DecodeToolResult(block.Raw)
						if err == nil {
							b.interruptionShape.ResultMatches = b.interruptionShape.ResultMatches || value.ID == b.previous.use.ID
							if value.IsError {
								b.interruptionShape.ErrorResults++
							}
							for _, part := range value.Content {
								if b.interruptionShape.ResultText == "" && len(part.Text) <= 96 && strings.Contains(strings.ToLower(part.Text), "interrupt") {
									b.interruptionShape.ResultText = part.Text
								}
								b.interruptionShape.ResultTextBytes += len(part.Text)
								b.interruptionShape.MentionsInterruption = b.interruptionShape.MentionsInterruption || strings.Contains(strings.ToLower(part.Text), "interrupt") || strings.Contains(strings.ToLower(part.Text), "cancel")
							}
						}
					}
				}
				if message.Role == "user" && block.Type == "text" {
					b.shape.InitialMarkers += strings.Count(block.Text, "EffectQuestion_131")
				} else {
					b.shape.OtherBlocks++
				}
			}
		}
	}
	budget := 2 - b.stage
	if b.followup {
		budget = 2
	}
	if b.interrupted && !b.followup {
		budget = 1
	}
	valid := r != nil && nativeHistoryID(r.Identity.Session) && len(r.Tools) == 1 && n <= budget
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
	} else if valid && b.followup {
		pair, err := resumedOperationPair(r, b.previous, b.issued, b.expect, b.followQuestion, n == 2)
		form := ""
		if b.interrupted {
			pair, err = resumedInterruptedOperationPair(r, b.previous, b.issued, b.expect, b.question, b.followQuestion, n == 2, &form)
		}
		valid = err == nil && (n == 1 || b.uses == 1 && b.handoffs == 1)
		if valid && b.interrupted {
			b.historyForm = form
			valid = form == "abandoned" || (form == "retained" && b.interruptionShape.UseMatches && b.interruptionShape.ResultMatches)
		}
		if valid {
			b.abandoned = b.interrupted
			b.historyChecks++
			if n == 2 {
				b.pair = pair
				b.results++
			}
		}
	} else if valid && b.interrupted && nativeInterruptedHistoryForm(r, b.question, b.previous.use.ID, b.previous.text, pendingRestartQuestion) != "" {
		b.historyForm = nativeInterruptedHistoryForm(r, b.question, b.previous.use.ID, b.previous.text, pendingRestartQuestion)
		// The retained form must also carry the exact delivered call and its own result identity.
		b.abandoned = b.historyForm == "abandoned" || (b.historyForm == "retained" && b.interruptionShape.UseMatches && b.interruptionShape.ResultMatches)
		valid = b.abandoned
	} else if valid {
		pair, err := recordedToolPair(r, b.stage == 1, b.interrupted)
		valid = err == nil && b.expect.matches(pair.use)
		if b.stage == 0 {
			valid = valid && b.uses == 1 && pair.use.ID == b.issued.ID && bytes.Equal(pair.input, canonicalToolObject(b.issued.Input))
		} else if b.interrupted {
			valid = valid && pair.use.ID == b.previous.use.ID && pair.use.Name == b.previous.use.Name && bytes.Equal(pair.input, b.previous.input) && pair.failed
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
	finished sync.Once
	owner    *toolRestartBackend
	request  int
}

func (t *toolRestartTurn) Finish() {
	t.finished.Do(func() {
		t.Turn.Finish()
		b := t.owner
		b.mu.Lock()
		if (b.stage == 0 || b.followup) && t.request == 1 && b.uses == 1 {
			b.handoffs++
		}
		b.mu.Unlock()
	})
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
		valid = valid && (b.stage == 0 || b.followup) && t.request == 1 && b.uses == 0 && len(event.Tools) == 1
		if valid {
			valid = b.expect.matches(event.Tools[0]) && b.beforeUse()
			if b.followup {
				valid = valid && event.Tools[0].ID != b.previous.use.ID && b.historyChecks == 1
			}
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
			valid = valid && (b.stage == 0 || b.followup) && t.request == 1 && b.uses == 1
		} else {
			lastRequest := 2 - b.stage
			if b.followup {
				lastRequest = 2
			}
			valid = valid && event.StopReason == "end_turn" && t.request == lastRequest && b.ends == 0 && (b.results == 1 || b.interrupted && !b.followup && b.abandoned)
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
