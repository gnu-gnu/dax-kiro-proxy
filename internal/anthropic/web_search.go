package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const MaxSearchUses = 8

type SearchSpec struct{ MaxUses int }

// SearchDeclaration recognizes a separate, single-use Claude Code search request. Domain and
// location restrictions need backend enforcement; accepting them as prompt hints is insufficient.
func SearchDeclaration(tools []json.RawMessage) (SearchSpec, bool, error) {
	matched := false
	for _, raw := range tools {
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &header) == nil && strings.HasPrefix(header.Type, "web_search_") {
			matched = true
		}
	}
	if !matched {
		return SearchSpec{}, false, nil
	}
	bad := func() (SearchSpec, bool, error) { return SearchSpec{}, true, ErrRequest }
	if len(tools) != 1 {
		return bad()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(tools[0], &fields) != nil {
		return bad()
	}
	for key := range fields {
		if key != "type" && key != "name" && key != "max_uses" {
			return bad()
		}
	}
	var kind, name string
	if json.Unmarshal(fields["type"], &kind) != nil || kind != "web_search_20250305" || json.Unmarshal(fields["name"], &name) != nil || name != "web_search" {
		return bad()
	}
	limit := MaxSearchUses
	if raw, ok := fields["max_uses"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &limit) != nil || limit < 1 || limit > MaxSearchUses {
			return bad()
		}
	}
	return SearchSpec{MaxUses: limit}, true, nil
}

type ServerToolUsage struct {
	WebSearchRequests int `json:"web_search_requests"`
}

// SearchResult deliberately has no provider-encrypted continuation or citation fields. Results
// are for the native client's one-shot search consumer, not Anthropic conversation replay.
type SearchResult struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}
type SearchExchange struct {
	ID, Query, ErrorCode string
	Results              []SearchResult
}

func (x SearchExchange) Valid() bool {
	if !strings.HasPrefix(x.ID, "srvtoolu_") || !(ToolUse{ID: x.ID, Name: "web_search", Input: json.RawMessage(`{}`)}).Valid() || !searchText(x.Query, 4096) || x.Query == "" || len(x.Results) > 32 {
		return false
	}
	if x.ErrorCode != "" {
		if len(x.Results) != 0 {
			return false
		}
		switch x.ErrorCode {
		case "unavailable", "invalid_tool_input", "too_many_requests", "query_too_long", "request_too_large", "max_uses_exceeded":
			return true
		default:
			return false
		}
	}
	for _, result := range x.Results {
		u, err := url.Parse(result.URL)
		if result.Type != "web_search_result" || !searchText(result.Title, 4096) || !searchText(result.URL, 8192) || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
			return false
		}
	}
	return true
}
func searchText(s string, limit int) bool {
	if len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return false
		}
	}
	return true
}
func (x SearchExchange) Blocks() []ResponseBlock {
	query, _ := json.Marshal(struct {
		Query string `json:"query"`
	}{x.Query})
	results := x.Results
	if results == nil {
		results = []SearchResult{}
	}
	content, _ := json.Marshal(results)
	if x.ErrorCode != "" {
		content, _ = json.Marshal(struct {
			Type string `json:"type"`
			Code string `json:"error_code"`
		}{"web_search_tool_result_error", x.ErrorCode})
	}
	return []ResponseBlock{{Type: "server_tool_use", ID: x.ID, Name: "web_search", Input: query}, {Type: "web_search_tool_result", ToolUseID: x.ID, Content: content}}
}
func (s *TextStream) SearchBytes(x SearchExchange) (int, error) {
	if s.ended || !x.Valid() {
		return 0, errors.New("invalid search exchange")
	}
	index := s.index
	if s.open {
		index++
	}
	packet, err := searchPacket(x, index)
	return len(packet) + 128, err
}
func (s *TextStream) Search(x SearchExchange) error {
	if s.ended || !x.Valid() || s.searches >= MaxSearchUses {
		return errors.New("invalid search exchange")
	}
	if err := s.stopBlock(); err != nil {
		return err
	}
	packet, err := searchPacket(x, s.index)
	if err != nil {
		return err
	}
	if _, err = s.writer.Write(packet); err != nil {
		return err
	}
	s.index += 2
	s.searches++
	return s.flush()
}
func searchPacket(x SearchExchange, index int) ([]byte, error) {
	blocks := x.Blocks()
	query := blocks[0].Input
	blocks[0].Input = json.RawMessage(`{}`)
	var b bytes.Buffer
	for i, block := range blocks {
		idx := index + i
		events := []streamEvent{{Type: "content_block_start", Index: &idx, Block: &block}}
		if i == 0 {
			events = append(events, streamEvent{Type: "content_block_delta", Index: &idx, Delta: struct {
				Type    string `json:"type"`
				Partial string `json:"partial_json"`
			}{"input_json_delta", string(query)}})
		}
		events = append(events, streamEvent{Type: "content_block_stop", Index: &idx})
		for _, event := range events {
			raw, err := json.Marshal(event)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", event.Type, raw)
		}
	}
	return b.Bytes(), nil
}
