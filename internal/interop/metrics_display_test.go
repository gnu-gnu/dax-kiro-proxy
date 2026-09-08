package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/status"
)

// This responder handles two independent text turns and narrow title work, without Kiro or tools.
type completionDisplayBackend struct {
	*statusProbeBackend
	queue                 *status.TurnQueue
	badInput              atomic.Bool
	mainTurns, titleTurns atomic.Int32
	mu                    sync.Mutex
	observations          []completionInputObservation
}

type completionInputObservation struct {
	Number                                                             int32
	Kind                                                               requestfamily.Kind
	Stream, FixtureSystem, FirstQuestion, SecondQuestion, OutputConfig bool
	MaxTokens                                                          int64
	SystemBlocks, SystemBytes, Messages                                int
	PurposeWords                                                       []string
	OutputFormat, TitleSchema                                          bool
}

func (b *completionDisplayBackend) Start(_ context.Context, r *anthropic.Request) (inference.Turn, error) {
	n := b.starts.Add(1)
	seen := completionInputObservation{Number: n, Kind: requestfamily.Classify(r), Stream: r.Stream, OutputConfig: r.Extra["output_config"] != nil, MaxTokens: r.MaxTokens}
	seen.SystemBlocks, seen.Messages = len(r.System), len(r.Messages)
	var systemText strings.Builder
	for _, block := range r.System {
		seen.FixtureSystem = seen.FixtureSystem || strings.Contains(block.Text, "Independent local rendering exercise.")
		seen.SystemBytes += len(block.Text)
		systemText.WriteString(strings.ToLower(block.Text))
	}
	for _, word := range []string{"title", "suggest", "token", "count", "cache", "warm", "summar", "classif", "permission", "safety", "agent"} {
		if strings.Contains(systemText.String(), word) {
			seen.PurposeWords = append(seen.PurposeWords, word)
		}
	}
	var output map[string]json.RawMessage
	if json.Unmarshal(r.Extra["output_config"], &output) == nil {
		seen.OutputFormat = output["format"] != nil
		seen.TitleSchema = bytes.Contains(output["format"], []byte(`"title"`))
	}
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role != "user" {
			continue
		}
		for _, block := range r.Messages[i].Content {
			seen.FirstQuestion = seen.FirstQuestion || strings.Contains(block.Text, "First independent local question.")
			seen.SecondQuestion = seen.SecondQuestion || strings.Contains(block.Text, "Second independent local question.")
		}
		break
	}
	b.mu.Lock()
	if len(b.observations) < 8 {
		b.observations = append(b.observations, seen)
	}
	b.mu.Unlock()
	raw, err := json.Marshal(r)
	if err != nil || bytes.Contains(raw, []byte("Kiro turn#")) || r.Extra["thinking"] != nil || r.Extra["context_management"] != nil {
		b.badInput.Store(true)
	}
	if n > 4 || len(r.Tools) != 0 || b.badInput.Load() {
		return nil, inference.ErrRequest
	}
	if requestfamily.Classify(r) == requestfamily.Title {
		if b.titleTurns.Add(1) > 2 {
			return nil, inference.ErrRequest
		}
		return &completionDisplayTurn{model: r.Model, text: `{"title":"Independent rendering exercise"}`}, nil
	}
	if !seen.FixtureSystem || b.mainTurns.Add(1) > 2 {
		b.badInput.Store(true)
		return nil, inference.ErrRequest
	}
	return &completionDisplayTurn{model: r.Model, text: "Independent local response complete.", queue: b.queue}, nil
}

type completionDisplayTurn struct {
	model string
	text  string
	queue *status.TurnQueue
	step  int
	once  sync.Once
}

func (t *completionDisplayTurn) Model() string { return t.model }
func (t *completionDisplayTurn) Next(context.Context) (inference.Event, error) {
	t.step++
	switch t.step {
	case 1:
		return inference.Event{Kind: inference.Text, Text: t.text}, nil
	case 2:
		return inference.Event{Kind: inference.End, StopReason: "end_turn"}, nil
	default:
		return inference.Event{}, io.EOF
	}
}
func (t *completionDisplayTurn) Finish() {
	t.once.Do(func() {
		if t.queue != nil {
			t.queue.Push(status.TurnRecord{Scope: strings.Repeat("c", 64), Model: t.model, SessionState: "created", ElapsedMS: 1250, Effort: kirofeature.Status{State: kirofeature.Unknown}})
		}
	})
}
func (t *completionDisplayTurn) Cancel() { t.once.Do(func() {}) }

func TestClaudeCompletionMetricsVisibleAcrossTwoLocalTurns(t *testing.T) {
	observeClaudeStatusUI(t, nil, true)
}
