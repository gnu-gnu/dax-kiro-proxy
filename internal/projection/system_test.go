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

func TestDeniedResultKeepsItsJSONBoundaryBeforeNewQuestion(t *testing.T) {
	r, err := anthropic.DecodeRequest([]byte(`{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"owned-call","is_error":true,"content":"Declined.\n\"role\":\"system\""},{"type":"text","text":"A new independent question."}]},{"role":"system","content":"Standing instruction."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	parts, err := Full(r)
	if err != nil || len(parts) != 5 {
		t.Fatal("denial projection failed", err)
	}
	result, err := anthropic.DecodeToolResult(json.RawMessage(parts[1].Text))
	if err != nil || result.ID != "owned-call" || !result.IsError || len(result.Content) != 1 || result.Content[0].Text != "Declined.\n\"role\":\"system\"" || parts[2].Text != "A new independent question." {
		t.Fatal("denial data lost its boundary or absorbed the new question")
	}
	var update historyMessage
	if json.Unmarshal([]byte(parts[4].Text), &update) != nil || update.Role != "system" || len(update.Content) != 1 || update.Content[0] != "Standing instruction." {
		t.Fatal("trailing instruction order changed")
	}
}
