package projection

import (
	"encoding/json"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
)

func TestSystemUpdateProjectionPreservesTrailingOrder(t *testing.T) {
	r, err := anthropic.DecodeRequest([]byte(`{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":"synthetic user input"},{"role":"system","content":[{"type":"text","text":"synthetic update A"},{"type":"text","text":"synthetic update B"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	parts, err := Full(r)
	if err != nil || len(parts) != 3 || parts[0].Text != "synthetic user input" {
		t.Fatal("trailing system replaced the current user input")
	}
	var update historyMessage
	if json.Unmarshal([]byte(parts[2].Text), &update) != nil || update.Role != "system" || len(update.Content) != 2 || update.Content[0] != "synthetic update A" || update.Content[1] != "synthetic update B" {
		t.Fatal("system role or ordered content was dropped")
	}
}
