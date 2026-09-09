//go:build darwin

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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
