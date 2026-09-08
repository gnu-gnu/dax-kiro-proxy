package status

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
)

func estimateRequest() *anthropic.Request {
	return &anthropic.Request{System: []anthropic.Block{{Type: "text", Text: "system context"}}, Tools: []json.RawMessage{json.RawMessage(`{"name":"inspect","description":"describe","input_schema":{"type":"object"}}`)}, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "한국어 and ASCII text"}}}}}
}
func TestTokenEstimatesCacheWorkAndExcludeMediaAndHiddenThinking(t *testing.T) {
	e := NewEstimator([32]byte{7})
	r := estimateRequest()
	base, ok := e.Measure(r)
	if !ok {
		t.Fatal("valid request estimate unavailable")
	}
	before := e.Stats()
	for range 20 {
		repeated, ok := e.Measure(r)
		if !ok || repeated.Tokens() != base.Tokens() {
			t.Fatal("nondeterministic estimate")
		}
	}
	if e.Stats().Computations != before.Computations {
		t.Fatal("repeated requests reparse cached input")
	}
	for _, kind := range []string{"image", "document", "thinking", "redacted_thinking"} {
		r.Messages[0].Content = append(r.Messages[0].Content, anthropic.Block{Type: kind, Raw: json.RawMessage(`{"type":"` + kind + `","data":"` + strings.Repeat("private-content", 100) + `"}`)})
	}
	withExcluded, ok := e.Measure(r)
	if !ok || withExcluded.Tokens() != base.Tokens() {
		t.Fatal("excluded media/thinking inflated input estimate")
	}
	var visible VisibleOutput
	visible.AddText("abcd")
	visible.AddText("efgh")
	visible.AddTools([]anthropic.ToolUse{{ID: "toolu-synthetic", Name: "inspect", Input: json.RawMessage(`{ "n": 1 }`)}})
	result, ok := withExcluded.Summary(InputEstimate{}, visible)
	if !ok || !result.Approximate || result.Method != "utf8-bytes/4-v1" || !result.MediaExcluded || !result.HiddenThinkingExcluded || result.VisibleOutputTokens != 4 || result.LogicalPrefixTokens != 0 {
		t.Fatal("incorrect approximation or output accounting")
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"private-content", "system context", "cache_read_input_tokens", "provider"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("estimate contains content or provider claims")
		}
	}
}
func TestLogicalPrefixAndBoundedEstimateCache(t *testing.T) {
	e := NewEstimator([32]byte{8})
	r := estimateRequest()
	base, _ := e.Measure(r)
	r.Messages = append(r.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: "visible reply"}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "tool_result", Raw: json.RawMessage(`{"type":"tool_result","tool_use_id":"synthetic","content":[{"type":"text","text":"new context"},{"type":"image","source":{"data":"ignored"}}]}`)}}})
	next, ok := e.Measure(r)
	result, valid := next.Summary(base, VisibleOutput{})
	if !ok || !valid || result.LogicalPrefixTokens != base.Tokens() || result.InputContextTokens <= base.Tokens() {
		t.Fatal("logical ordered prefix estimate missing")
	}
	for i := range 300 {
		r.Messages[0].Content[0].Text = fmt.Sprintf("independent input %d", i)
		if _, ok := e.Measure(r); !ok {
			t.Fatal("bounded cache rejected ordinary input")
		}
	}
	if e.Stats().Entries > 128 {
		t.Fatal("estimate cache grew beyond its bound")
	}
	r.Messages[0].Content = []anthropic.Block{{Type: "tool_use", Raw: json.RawMessage(`{"type":"tool_use","name":"inspect","input":{"n":9007199254740993}}`)}}
	exact, _ := e.Measure(r)
	r.Messages[0].Content[0].Raw = json.RawMessage(`{"input": { "n": 9007199254740993 },"name":"inspect","type":"tool_use"}`)
	formatted, ok := e.Measure(r)
	if !ok || exact.Tokens() != formatted.Tokens() {
		t.Fatal("JSON formatting changed the token estimate")
	}
}
