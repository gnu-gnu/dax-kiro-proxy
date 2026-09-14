package anthropic

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchDeclaration(t *testing.T) {
	for _, tc := range []struct {
		raw          string
		match, valid bool
		limit        int
	}{
		{`[]`, false, true, 0},
		{`[{"name":"WebSearch","input_schema":{"type":"object"}}]`, false, true, 0},
		{`[{"type":"web_search_20250305","name":"web_search","max_uses":8}]`, true, true, 8},
		{`[{"type":"web_search_20250305","name":"web_search"}]`, true, true, 8},
		{`[{"type":"web_search_20250305","name":"web_search","max_uses":0}]`, true, false, 0},
		{`[{"type":"web_search_20250305","name":"web_search","max_uses":9}]`, true, false, 0},
		{`[{"type":"web_search_20250305","name":"web_search","max_uses":1.5}]`, true, false, 0},
		{`[{"type":"web_search_20250305","name":"web_search","allowed_domains":["example.org"]}]`, true, false, 0},
		{`[{"type":"web_search_20250305","name":"web_search","user_location":{}}]`, true, false, 0},
		{`[{"type":"web_search_20250305","name":"WebSearch"}]`, true, false, 0},
		{`[{"type":"web_search_20260209","name":"web_search"}]`, true, false, 0},
		{`[{"type":"web_search_20250305","name":"web_search"},{"name":"Bash"}]`, true, false, 0},
	} {
		var tools []json.RawMessage
		if json.Unmarshal([]byte(tc.raw), &tools) != nil {
			t.Fatal("invalid independent declaration fixture")
		}
		spec, match, err := SearchDeclaration(tools)
		if match != tc.match || (err == nil) != tc.valid || err == nil && match && spec.MaxUses != tc.limit {
			t.Fatalf("declaration match=%t valid=%t limit=%d", match, err == nil, spec.MaxUses)
		}
	}
}

func TestSearchResponsePackets(t *testing.T) {
	x := SearchExchange{ID: "srvtoolu_fixture", Query: "synthetic query", Results: []SearchResult{{Type: "web_search_result", URL: "https://example.org/reference", Title: "Independent reference"}}}
	if !x.Valid() {
		t.Fatal("valid fixture rejected")
	}
	blocks := x.Blocks()
	if len(blocks) != 2 || blocks[0].Type != "server_tool_use" || blocks[1].Type != "web_search_tool_result" || blocks[1].ToolUseID != x.ID {
		t.Fatal("incorrect search correlation")
	}
	var output bytes.Buffer
	s, err := BeginStream(&output, func() error { return nil }, "msg_fixture", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.SearchBytes(x)
	if err != nil {
		t.Fatal(err)
	}
	before := output.Len()
	if err := s.Search(x); err != nil {
		t.Fatal(err)
	}
	if output.Len()-before > n {
		t.Fatal("search escaped output reservation")
	}
	if err := s.End("end_turn"); err != nil {
		t.Fatal(err)
	}
	var starts []ResponseBlock
	for _, line := range strings.Split(output.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type  string        `json:"type"`
			Block ResponseBlock `json:"content_block"`
			Usage Usage         `json:"usage"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			t.Fatal("invalid SSE JSON")
		}
		if event.Type == "content_block_start" {
			starts = append(starts, event.Block)
		}
		if event.Type == "message_delta" && (event.Usage.ServerTools == nil || event.Usage.ServerTools.WebSearchRequests != 1) {
			t.Fatal("missing actual search count")
		}
	}
	if len(starts) != 2 || starts[1].ToolUseID != x.ID || !bytes.Equal(starts[1].Content, blocks[1].Content) {
		t.Fatal("stream result differs from complete response")
	}
	if bytes.Contains(output.Bytes(), []byte("encrypted_content")) {
		t.Fatal("provider continuation must not be invented")
	}
}

func TestSearchResultValidation(t *testing.T) {
	base := SearchExchange{ID: "srvtoolu_fixture", Query: "synthetic", Results: []SearchResult{}}
	for _, change := range []func(*SearchExchange){
		func(x *SearchExchange) { x.ID = "toolu_client" },
		func(x *SearchExchange) { x.Query = "" },
		func(x *SearchExchange) {
			x.Results = []SearchResult{{Type: "web_search_result", URL: "file:///etc/passwd", Title: "no"}}
		},
		func(x *SearchExchange) {
			x.Results = []SearchResult{{Type: "web_search_result", URL: "https://secret@example.org/", Title: "no"}}
		},
		func(x *SearchExchange) { x.ErrorCode = "made_up_error" },
		func(x *SearchExchange) {
			x.ErrorCode = "unavailable"
			x.Results = []SearchResult{{Type: "web_search_result", URL: "https://example.org", Title: "no"}}
		},
	} {
		x := base
		change(&x)
		if x.Valid() {
			t.Fatal("invalid result accepted")
		}
	}
	if !base.Valid() {
		t.Fatal("empty successful search rejected")
	}
	base.ErrorCode = "max_uses_exceeded"
	if !base.Valid() {
		t.Fatal("documented tool error rejected")
	}
}
