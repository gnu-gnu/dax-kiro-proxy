package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

type pluginSequenceFixture struct {
	starts   int
	canceled atomic.Int32
	turns    [][]inference.Event
}

type pluginSequenceFault struct {
	inference.Backend
	inference.Turn
	startErr, readErr error
	canceled          int
}

func (f *pluginSequenceFault) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f, nil
}
func (f *pluginSequenceFault) Next(context.Context) (inference.Event, error) {
	return inference.Event{}, f.readErr
}
func (f *pluginSequenceFault) Cancel() { f.canceled++ }

func (f *pluginSequenceFixture) Models(context.Context) ([]inference.Model, error) { return nil, nil }
func (f *pluginSequenceFixture) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	f.starts++
	if f.starts > len(f.turns) {
		return nil, inference.ErrRequest
	}
	return &denialTurnFixture{events: f.turns[f.starts-1], cancel: &f.canceled}, nil
}

func pluginSequenceRequest(stage int, denied bool) *anthropic.Request {
	r := &anthropic.Request{Model: "fixture-model", Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "Independent two-tool sequence"}}}}}
	name := "WaitForMcpServers"
	if stage > 0 {
		name = ownedPluginToolName
	}
	raw, _ := json.Marshal(map[string]any{"name": name, "input_schema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}})
	r.Tools = []json.RawMessage{raw}
	if stage > 0 {
		id, text, isError := "wait-use", "Independent completed wait", false
		if stage == 2 {
			id, text, isError = "plugin-use", "independent client asset result", denied
			if denied {
				text = clientDenialReason
			}
		}
		raw, _ := json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isError, "content": text})
		r.Messages[0].Content = []anthropic.Block{{Type: "tool_result", Raw: raw}}
	}
	return r
}

func newPluginSequenceFixture(denied bool) (*pluginSequenceGuard, *pluginSequenceFixture) {
	f := &pluginSequenceFixture{turns: [][]inference.Event{
		{{Kind: inference.Tools, Tools: []anthropic.ToolUse{{ID: "wait-use", Name: "WaitForMcpServers", Input: json.RawMessage(`{}`)}}}, {Kind: inference.End, StopReason: "tool_use"}},
		{{Kind: inference.Tools, Tools: []anthropic.ToolUse{{ID: "plugin-use", Name: ownedPluginToolName, Input: json.RawMessage(`{}`)}}}, {Kind: inference.End, StopReason: "tool_use"}},
		{{Kind: inference.Text, Text: "Independent completed sequence"}, {Kind: inference.End, StopReason: "end_turn"}},
	}}
	return &pluginSequenceGuard{Backend: f, denied: denied}, f
}

func consumePluginSequence(t *testing.T, g *pluginSequenceGuard, stage int, denied bool) error {
	t.Helper()
	turn, err := g.Start(t.Context(), pluginSequenceRequest(stage, denied))
	if err != nil {
		return err
	}
	for {
		_, err := turn.Next(t.Context())
		if errors.Is(err, io.EOF) {
			turn.Finish()
			return nil
		}
		if err != nil {
			turn.Cancel()
			return err
		}
	}
}

func TestPluginSequenceGuardAllowsOnlyTwoOwnedCallsAndThreeRequests(t *testing.T) {
	for _, denied := range []bool{false, true} {
		g, f := newPluginSequenceFixture(denied)
		for stage := range 3 {
			if err := consumePluginSequence(t, g, stage, denied); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := g.Start(t.Context(), pluginSequenceRequest(0, denied)); !errors.Is(err, inference.ErrRequest) || f.starts != 3 {
			t.Fatal("fourth request reached backend")
		}
		if s := g.snapshot(); s.Uses != 2 || s.Results != 2 || s.Completions != 1 || s.TextBytes == 0 {
			t.Fatal("incomplete sequence counted as success")
		}
	}
}

func TestPluginSequenceGuardRejectsUnplannedEffectsAndResults(t *testing.T) {
	for _, mutate := range []func(*pluginSequenceFixture){
		func(f *pluginSequenceFixture) { f.turns[0][0].Tools[0].Name = "Bash" },
		func(f *pluginSequenceFixture) {
			f.turns[0][0].Tools[0].Input = json.RawMessage(`{"command":"unplanned"}`)
		},
		func(f *pluginSequenceFixture) {
			f.turns[0][0].Tools = append(f.turns[0][0].Tools, f.turns[0][0].Tools[0])
		},
		func(f *pluginSequenceFixture) {
			f.turns[0] = []inference.Event{{Kind: inference.End, StopReason: "end_turn"}}
		},
		func(f *pluginSequenceFixture) { f.turns[0] = nil },
	} {
		g, f := newPluginSequenceFixture(false)
		mutate(f)
		if err := consumePluginSequence(t, g, 0, false); !errors.Is(err, inference.ErrRequest) || f.canceled.Load() == 0 {
			t.Fatal("unplanned response reached client")
		}
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) {
			r.Messages[0].Content = append(r.Messages[0].Content, anthropic.Block{Type: "text", Text: "New question"})
		},
		func(r *anthropic.Request) {
			r.Messages[0].Content = append(r.Messages[0].Content, r.Messages[0].Content[0])
		},
		func(r *anthropic.Request) {
			r.Messages[0].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"foreign","content":"unowned"}`)
		},
		func(r *anthropic.Request) {
			r.Messages[0].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"wait-use","is_error":true,"content":"failed wait"}`)
		},
		func(r *anthropic.Request) { r.Tools = nil },
	} {
		g, f := newPluginSequenceFixture(false)
		if err := consumePluginSequence(t, g, 0, false); err != nil {
			t.Fatal(err)
		}
		r := pluginSequenceRequest(1, false)
		mutate(r)
		if _, err := g.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 1 {
			t.Fatal("unproven result reached backend")
		}
	}
	for _, denied := range []bool{false, true} {
		g, f := newPluginSequenceFixture(denied)
		for stage := range 2 {
			if err := consumePluginSequence(t, g, stage, denied); err != nil {
				t.Fatal(err)
			}
		}
		r := pluginSequenceRequest(2, !denied)
		if _, err := g.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 2 {
			t.Fatal("wrong plugin status reached backend")
		}
	}
}

func TestPluginSequenceGuardRequiresDeliveredHandoff(t *testing.T) {
	g, f := newPluginSequenceFixture(false)
	turn, err := g.Start(t.Context(), pluginSequenceRequest(0, false))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := turn.Next(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Start(t.Context(), pluginSequenceRequest(1, false)); !errors.Is(err, inference.ErrBusy) || f.starts != 1 {
		t.Fatal("undelivered handoff consumed a result")
	}
	turn.Finish()
	if err := consumePluginSequence(t, g, 1, false); err != nil {
		t.Fatal("delivery did not admit the same result", err)
	}
}

func TestPluginSequenceMarkerSeparatesPromptToolsAndFinalText(t *testing.T) {
	const marker = "OWNEDFINALMARKER"
	for _, stage := range []string{"valid", "split_final", "prompt", "early_text", "split_early", "wait_result", "missing_final"} {
		t.Run(stage, func(t *testing.T) {
			g, f := newPluginSequenceFixture(false)
			g.finalMarker = marker
			f.turns[2][0].Text = marker
			if stage == "split_final" {
				f.turns[2] = []inference.Event{{Kind: inference.Text, Text: marker[:7]}, {Kind: inference.Text, Text: marker[7:]}, {Kind: inference.End, StopReason: "end_turn"}}
			}
			if stage == "early_text" {
				f.turns[0] = append([]inference.Event{{Kind: inference.Text, Text: marker}}, f.turns[0]...)
			}
			if stage == "split_early" {
				f.turns[0] = append([]inference.Event{{Kind: inference.Text, Text: marker[:7]}, {Kind: inference.Text, Text: marker[7:]}}, f.turns[0]...)
			}
			if stage == "missing_final" {
				f.turns[2][0].Text = "Another final wording"
			}
			if stage == "prompt" {
				r := pluginSequenceRequest(0, false)
				r.Messages[0].Content[0].Text = marker
				if _, err := g.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 0 {
					t.Fatal("literal prompt marker admitted")
				}
				return
			}
			if err := consumePluginSequence(t, g, 0, false); stage == "early_text" || stage == "split_early" {
				if !errors.Is(err, inference.ErrRequest) {
					t.Fatal("early model marker accepted")
				}
				return
			} else if err != nil {
				t.Fatal(err)
			}
			if stage == "wait_result" {
				r := pluginSequenceRequest(1, false)
				r.Messages[0].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"wait-use","content":"OWNEDFINALMARKER"}`)
				if _, err := g.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 1 {
					t.Fatal("tool marker counted as a final response")
				}
				return
			}
			if err := consumePluginSequence(t, g, 1, false); err != nil {
				t.Fatal(err)
			}
			err := consumePluginSequence(t, g, 2, false)
			if stage == "valid" || stage == "split_final" {
				if err != nil || !g.snapshot().FinalMarker {
					t.Fatal("final marker not established", err)
				}
			} else if !errors.Is(err, inference.ErrRequest) || g.snapshot().FinalMarker {
				t.Fatal("other wording passed the marker condition")
			}
		})
	}
}

func TestPluginSequenceFailureReportsOnlyFixedCategories(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*pluginSequenceFixture)
		want   string
	}{
		{"wrong_tool", func(f *pluginSequenceFixture) { f.turns[0][0].Tools[0].Name = "private-unexpected-name" }, "tool_name"},
		{"wrong_input", func(f *pluginSequenceFixture) {
			f.turns[0][0].Tools[0].Input = json.RawMessage(`{"private":"payload"}`)
		}, "tool_arguments"},
		{"early_end", func(f *pluginSequenceFixture) {
			f.turns[0] = []inference.Event{{Kind: inference.End, StopReason: "end_turn"}}
		}, "end_condition"},
		{"missing_end", func(f *pluginSequenceFixture) { f.turns[0] = nil }, "missing_end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, f := newPluginSequenceFixture(false)
			tc.change(f)
			if err := consumePluginSequence(t, g, 0, false); !errors.Is(err, inference.ErrRequest) || g.snapshot().Failure != tc.want {
				t.Fatal("missing fixed validation category")
			}
		})
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("private process output"), "other"},
		{errors.Join(errors.New("private context"), acp.ErrAuthentication), "authentication"},
		{acp.ErrTransport, "transport"}, {acp.ErrProtocol, "protocol"},
		{context.DeadlineExceeded, "deadline"}, {context.Canceled, "canceled"},
	} {
		if pluginSequenceErrorClass(tc.err) != tc.want {
			t.Fatal("error classification exposed text or lost a known class")
		}
	}
	for _, atStart := range []bool{true, false} {
		f := &pluginSequenceFault{}
		if atStart {
			f.startErr = acp.ErrTransport
		} else {
			f.readErr = acp.ErrTransport
		}
		g := &pluginSequenceGuard{Backend: f}
		turn, err := g.Start(t.Context(), pluginSequenceRequest(0, false))
		if atStart {
			if !errors.Is(err, acp.ErrTransport) || g.snapshot().Failure != "backend_start" || g.snapshot().StartError != "transport" {
				t.Fatal("backend start was not distinguished")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if _, err = turn.Next(t.Context()); !errors.Is(err, acp.ErrTransport) {
				t.Fatal("backend read error lost")
			}
			turn.Cancel()
			if g.snapshot().Failure != "backend_read" || g.snapshot().ReadError != "transport" || f.canceled != 1 {
				t.Fatal("read cancellation category lost")
			}
		}
	}
}

func TestPluginSequenceFinalChallengeComesFromThePluginResult(t *testing.T) {
	const suffix = "RESULTCHALLENGE"
	for _, denied := range []bool{false, true} {
		g, f := newPluginSequenceFixture(denied)
		g.resultSuffix = suffix
		g.finalMarker = "PROMPTPREFIX" + suffix
		f.turns[2][0].Text = g.finalMarker
		for stage := range 2 {
			if err := consumePluginSequence(t, g, stage, denied); err != nil {
				t.Fatal(err)
			}
		}
		r := pluginSequenceRequest(2, denied)
		text := "independent client asset result; Y=" + suffix
		if denied {
			text = clientDenialReason + "; Y=" + suffix
		}
		raw, _ := json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": "plugin-use", "is_error": denied, "content": text})
		r.Messages[0].Content[0].Raw = raw
		turn, err := g.Start(t.Context(), r)
		if err != nil {
			t.Fatal("matching result challenge rejected", err)
		}
		for range 2 {
			if _, err := turn.Next(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		turn.Finish()
		if !g.snapshot().FinalMarker || g.snapshot().Failed {
			t.Fatal("final result challenge not established")
		}
	}
	for _, stage := range []int{0, 1, 2} {
		g, _ := newPluginSequenceFixture(false)
		g.resultSuffix, g.finalMarker = suffix, "PROMPTPREFIX"+suffix
		for n := 0; n < stage; n++ {
			if err := consumePluginSequence(t, g, n, false); err != nil {
				t.Fatal(err)
			}
		}
		r := pluginSequenceRequest(stage, false)
		if stage == 0 {
			r.Messages[0].Content[0].Text = suffix
		}
		if stage == 1 {
			r.Messages[0].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"wait-use","content":"RESULTCHALLENGE"}`)
		}
		if _, err := g.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) {
			t.Fatal("early or missing result challenge accepted")
		}
	}
}
