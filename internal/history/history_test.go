package history_test

import (
	"encoding/json"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/history"
)

func transcript(t *testing.T, messages string) *anthropic.Request {
	t.Helper()
	r, err := anthropic.DecodeRequest([]byte(`{"model":"claude-dax-fixture","max_tokens":128,"messages":` + messages + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func hasher() *history.Hasher { return history.New([32]byte{17}) }
func complete(t *testing.T, h *history.Hasher, s history.Snapshot, r *anthropic.Request, reply string) history.Snapshot {
	t.Helper()
	p, err := h.Plan(s, r)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Complete(p, []anthropic.Block{{Type: "text", Text: reply}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestThreeTurnsAndProvenTruncationSendOnlyNewContent(t *testing.T) {
	h := hasher()
	first := transcript(t, `[{"role":"user","content":"one"}]`)
	s := complete(t, h, history.Snapshot{}, first, "reply one")
	second := transcript(t, `[{"role":"user","content":[{"text":"one","type":"text","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":"reply one"},{"role":"user","content":"two"}]`)
	p, err := h.Plan(s, second)
	if err != nil || p.Mode != history.Extend || p.Start != 2 {
		t.Fatal("second turn did not extend after the delivered assistant")
	}
	s = complete(t, h, s, second, "reply two")
	third := transcript(t, `[{"role":"assistant","content":"reply one"},{"role":"user","content":"two"},{"role":"assistant","content":"reply two"},{"role":"user","content":"three"}]`)
	p, err = h.Plan(s, third)
	if err != nil || p.Mode != history.Extend || p.Start != 3 {
		t.Fatal("proven truncated history did not preserve continuity")
	}
	s = complete(t, h, s, third, "reply three")
	if len(s.Pairs) != 3 || len(s.Nodes) != 6 {
		t.Fatal("truncation discarded committed history anchors")
	}
	encoded, _ := json.Marshal(s)
	var shape map[string]any
	_ = json.Unmarshal(encoded, &shape)
	if string(encoded) == "" || len(shape) != 2 {
		t.Fatal("unexpected snapshot shape")
	}
}
func TestDuplicateDivergenceAndUnprovenAssistantOnlyOverlap(t *testing.T) {
	h := hasher()
	first := transcript(t, `[{"role":"user","content":"one"}]`)
	s := complete(t, h, history.Snapshot{}, first, "reply one")
	for _, tc := range []struct {
		body string
		mode history.Mode
	}{
		{`[{"role":"user","content":"one"}]`, history.Duplicate},
		{`[{"role":"user","content":"changed"},{"role":"assistant","content":"reply one"},{"role":"user","content":"two"}]`, history.Diverged},
		{`[{"role":"user","content":"one"},{"role":"assistant","content":"altered reply"},{"role":"user","content":"two"}]`, history.Diverged},
		{`[{"role":"assistant","content":"reply one"},{"role":"user","content":"two"}]`, history.Diverged},
	} {
		p, err := h.Plan(s, transcript(t, tc.body))
		if err != nil || p.Mode != tc.mode {
			t.Fatalf("unexpected history decision: %v %v", p.Mode, err)
		}
	}
}
func TestCanonicalizationPreservesArgumentNumbersAndSystemContext(t *testing.T) {
	h := hasher()
	r := transcript(t, `[{"role":"user","content":"one"},{"role":"system","content":"runtime A"}]`)
	s := complete(t, h, history.Snapshot{}, r, "reply")
	changed := transcript(t, `[{"role":"user","content":"one"},{"role":"system","content":"runtime B"},{"role":"assistant","content":"reply"},{"role":"user","content":"two"}]`)
	p, err := h.Plan(s, changed)
	if err != nil || p.Mode != history.Diverged {
		t.Fatal("changed committed system content silently reused")
	}
	block := func(raw string) anthropic.Block { return anthropic.Block{Type: "tool_use", Raw: json.RawMessage(raw)} }
	a, err := h.Message(anthropic.Message{Role: "assistant", Content: []anthropic.Block{block(`{"type":"tool_use","id":"u","name":"action","input":{"n":9007199254740993,"cache_control":"argument"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Message(anthropic.Message{Role: "assistant", Content: []anthropic.Block{block(`{"input":{"cache_control":"argument","n":9007199254740993},"name":"action","id":"u","type":"tool_use","cache_control":{"type":"ephemeral"}}`)}})
	if err != nil || a != b {
		t.Fatal("key order or block cache hint changed meaning")
	}
	c, _ := h.Message(anthropic.Message{Role: "assistant", Content: []anthropic.Block{block(`{"type":"tool_use","id":"u","name":"action","input":{"n":9007199254740992,"cache_control":"argument"}}`)}})
	if a == c {
		t.Fatal("large argument numbers collapsed")
	}
	d, _ := history.New([32]byte{18}).Message(anthropic.Message{Role: "assistant", Content: []anthropic.Block{block(`{"type":"tool_use","id":"u","name":"action","input":{"n":9007199254740993,"cache_control":"argument"}}`)}})
	if a == d {
		t.Fatal("different digest scopes matched")
	}
}
