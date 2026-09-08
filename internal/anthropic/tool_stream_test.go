package anthropic

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOrderedTextAndToolStream(t *testing.T) {
	var output bytes.Buffer
	s, err := BeginStream(&output, func() error { return nil }, "msg_fixture", "claude-dax-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Text("before tools"); err != nil {
		t.Fatal(err)
	}
	tools := []ToolUse{{ID: "toolu_one", Name: "client_one", Input: json.RawMessage(`{"n":9007199254740993}`)}, {ID: "toolu_two", Name: "client_two", Input: json.RawMessage(`{"text":"한 줄\n다음"}`)}}
	for _, tool := range tools {
		if err := s.Tool(tool); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.End("tool_use"); err != nil {
		t.Fatal(err)
	}
	var events []string
	var indices []int
	var args []string
	for _, packet := range strings.Split(strings.TrimSpace(output.String()), "\n\n") {
		lines := strings.Split(packet, "\n")
		var event struct {
			Type  string
			Index int
			Delta struct {
				Type    string
				Partial string `json:"partial_json"`
			}
			Block struct{ Type, ID, Name string } `json:"content_block"`
		}
		if len(lines) != 2 || json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &event) != nil {
			t.Fatal("invalid SSE event")
		}
		events = append(events, event.Type)
		if event.Type == "content_block_start" {
			indices = append(indices, event.Index)
		}
		if event.Delta.Type == "input_json_delta" {
			args = append(args, event.Delta.Partial)
		}
	}
	want := "message_start,content_block_start,content_block_delta,content_block_stop,content_block_start,content_block_delta,content_block_stop,content_block_start,content_block_delta,content_block_stop,message_delta,message_stop"
	if strings.Join(events, ",") != want || len(indices) != 3 || indices[0] != 0 || indices[1] != 1 || indices[2] != 2 {
		t.Fatal("mixed block order is invalid")
	}
	if len(args) != 2 || args[0] != string(tools[0].Input) || args[1] != string(tools[1].Input) {
		t.Fatal("complete tool input lost or rounded")
	}
	before := output.Len()
	if err := s.Tool(tools[0]); err == nil || output.Len() != before {
		t.Fatal("tool emitted after message_stop")
	}
}
func TestInvalidToolInputCannotStartAPartialBlock(t *testing.T) {
	var output bytes.Buffer
	s, _ := BeginStream(&output, func() error { return nil }, "msg_fixture", "claude-dax-fixture")
	before := output.Len()
	for _, input := range []json.RawMessage{json.RawMessage(`{"n":`), json.RawMessage(`[]`), json.RawMessage(`{"n":1,"n":2}`)} {
		if s.Tool(ToolUse{ID: "toolu_one", Name: "client_tool", Input: input}) == nil || output.Len() != before {
			t.Fatal("invalid JSON exposed as a tool block")
		}
	}
}
