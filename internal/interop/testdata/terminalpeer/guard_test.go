//go:build darwin

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFrameReceiptRequiresActiveCorrelatedPrompt(t *testing.T) {
	var g frameGuard
	for _, tc := range []struct {
		from string
		body string
		want string
	}{
		{"client", `{"id":1,"method":"initialize","params":{}}`, ""},
		{"client", `{"id":2,"method":"session/prompt","params":{}}`, "prompt"},
		{"agent", `{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"independent fixture"}}}}`, "text"},
		{"agent", `{"id":1,"result":{}}`, ""},
		{"client", `{"method":"session/cancel","params":{}}`, "cancel"},
		{"agent", `{"id":2,"result":{"stopReason":"cancelled"}}`, "cancelled"},
	} {
		kind, err := g.inspect(tc.from, []byte(tc.body))
		if err != nil || kind != tc.want {
			t.Fatalf("receipt differs: kind=%q error=%v", kind, err)
		}
	}
	if !g.done || g.prompts != 1 {
		t.Fatal("missing finished prompt")
	}
	if _, err := g.inspect("client", []byte(`{"id":3,"method":"session/prompt"}`)); err == nil {
		t.Fatal("second prompt budget admitted")
	}
}

func TestFrameReceiptRejectsUnboundedOrAmbiguousInput(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{broken`, `{"method":"session/prompt"}`, `{"id":null,"method":"session/prompt"}`} {
		var g frameGuard
		if _, err := g.inspect("client", []byte(input)); err == nil {
			t.Fatal("invalid prompt observation admitted")
		}
	}
	var g frameGuard
	if _, err := g.inspect("other", []byte(`{}`)); err == nil {
		t.Fatal("unknown direction admitted")
	}
	for range 1024 {
		if _, err := g.inspect("client", []byte(`{}`)); err != nil {
			t.Fatal("bounded control failed")
		}
	}
	if _, err := g.inspect("client", []byte(`{}`)); err == nil {
		t.Fatal("frame budget exceeded")
	}
}

func TestPromptAdmissionRemainsConsumedByReplacement(t *testing.T) {
	previous := cfg
	cfg.Root = t.TempDir()
	defer func() { cfg = previous }()
	if admitPrompt(false) != nil {
		t.Fatal("initial independent prompt not admitted")
	}
	if admitPrompt(false) == nil {
		t.Fatal("replacement process could admit another prompt")
	}
	if admitPrompt(true) != nil || admitPrompt(true) == nil {
		t.Fatal("separate auxiliary budget differs")
	}
}

func TestForwardKeepsExactFramesAndBlocksReplacementBeforeDispatch(t *testing.T) {
	previous := cfg
	cfg.Root = t.TempDir()
	defer func() { cfg = previous }()
	if os.Mkdir(filepath.Join(cfg.Root, "events"), 0700) != nil {
		t.Fatal("fixture")
	}
	raw := []byte("{\"jsonrpc\":\"2.0\", \"id\":4,\"method\":\"session/prompt\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := forward(bytes.NewReader(raw), &output, "client", new(frameGuard)); err != io.EOF || !bytes.Equal(output.Bytes(), raw) {
		t.Fatal("observer changed frame bytes")
	}
	output.Reset()
	if err := forward(bytes.NewReader(raw), &output, "client", new(frameGuard)); err == nil || output.Len() != 0 {
		t.Fatal("replacement prompt crossed guard")
	}
}

func TestFollowupAdmissionRequiresParentIntentAndOneIndependentBudget(t *testing.T) {
	previous := cfg
	cfg.Root, cfg.AllowFollowup = t.TempDir(), true
	defer func() { cfg = previous }()
	if admitObservedPrompt(false, false) != nil || admitObservedPrompt(true, false) != nil {
		t.Fatal("initial budgets")
	}
	if admitObservedPrompt(false, true) == nil || admitObservedPrompt(true, true) == nil {
		t.Fatal("new input was admitted before parent intent")
	}
	if os.WriteFile(filepath.Join(cfg.Root, "followup-allowed"), []byte("owned-new-question"), 0600) != nil {
		t.Fatal("fixture")
	}
	if admitObservedPrompt(false, true) != nil || admitObservedPrompt(true, true) != nil {
		t.Fatal("independent followup budgets")
	}
	if admitObservedPrompt(false, false) == nil || admitObservedPrompt(false, true) == nil || admitObservedPrompt(true, true) == nil {
		t.Fatal("replacement or repeated prompt crossed total budget")
	}
}

func TestSerialAuxiliaryPromptsRemainCorrelatedAndBounded(t *testing.T) {
	g := frameGuard{maxPrompts: 2}
	for _, input := range []struct{ from, raw, kind string }{
		{"client", `{"id":1,"method":"session/prompt"}`, "prompt"},
		{"agent", `{"id":1,"result":{"stopReason":"end_turn"}}`, "end"},
		{"client", `{"id":2,"method":"session/prompt"}`, "prompt"},
		{"agent", `{"id":1,"result":{"stopReason":"end_turn"}}`, ""},
		{"agent", `{"id":2,"result":{"stopReason":"end_turn"}}`, "end"},
	} {
		kind, err := g.inspect(input.from, []byte(input.raw))
		if err != nil || kind != input.kind {
			t.Fatal("serial prompt correlation differs")
		}
	}
	if _, err := g.inspect("client", []byte(`{"id":3,"method":"session/prompt"}`)); err == nil {
		t.Fatal("third prompt admitted")
	}
	g = frameGuard{maxPrompts: 2}
	_, _ = g.inspect("client", []byte(`{"id":1,"method":"session/prompt"}`))
	if _, err := g.inspect("client", []byte(`{"id":2,"method":"session/prompt"}`)); err == nil {
		t.Fatal("overlapping prompt admitted")
	}
}

func TestPromptMarkerFactsDistinguishPresentAndAbsentInputs(t *testing.T) {
	previous := cfg
	cfg.Root = t.TempDir()
	defer func() { cfg = previous; titleScope.Store(false); followScope.Store(false) }()
	if os.Mkdir(filepath.Join(cfg.Root, "events"), 0700) != nil {
		t.Fatal("fixture")
	}
	for _, body := range []string{"Independent unrelated input.", "concatenation of Ready and _47; Ready_47; concatenation of Follow and _49"} {
		raw, _ := json.Marshal(map[string]any{"method": "session/prompt", "params": map[string]any{"prompt": []any{map[string]string{"type": "text", "text": body}}}})
		notePrompt(raw)
	}
	data, err := os.ReadFile(filepath.Join(cfg.Root, "events", strconv.Itoa(os.Getpid())+".jsonl"))
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if err != nil || len(lines) != 2 {
		t.Fatal("missing fixed marker receipts")
	}
	for i, line := range lines {
		var facts map[string]json.RawMessage
		if json.Unmarshal(line, &facts) != nil {
			t.Fatal("receipt")
		}
		for _, key := range []string{"old_input", "partial_marker", "new_input", "null_marker"} {
			value, present := facts[key]
			var observed bool
			if !present || json.Unmarshal(value, &observed) != nil || observed != (i == 1 && key != "null_marker") {
				t.Fatal("marker detector did not distinguish its null control")
			}
		}
	}
}

func TestPromptFailureCannotCountAsSuccessfulCompletion(t *testing.T) {
	for _, raw := range []string{`{"id":1,"error":{"code":-32000,"message":"independent failure"}}`, `{"id":1,"result":{}}`, `{"id":1,"result":{"stopReason":"max_tokens"}}`} {
		var g frameGuard
		_, _ = g.inspect("client", []byte(`{"id":1,"method":"session/prompt"}`))
		kind, err := g.inspect("agent", []byte(raw))
		if err != nil || kind != "failed-result" {
			t.Fatal("non-success prompt result counted as completion")
		}
	}
}
