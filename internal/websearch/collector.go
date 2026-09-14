package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/ndjson"
)

type searchCall struct {
	exchange      anthropic.SearchExchange
	status        string
	input, output [32]byte
	hasOutput     bool
	content       json.RawMessage
	native        json.RawMessage
	nativeDigest  [32]byte
	hasNative     bool
}
type collector struct {
	session              string
	limit, events, bytes int
	calls                map[string]*searchCall
	order                []string
	text                 strings.Builder
	progress             bool
}

func newCollector(session string, limit int) *collector {
	return &collector{session: session, limit: limit, calls: make(map[string]*searchCall)}
}

func (c *collector) observe(n acp.Notification) error {
	c.progress = false
	c.events++
	c.bytes += len(n.Params)
	if c.events > 4096 || c.bytes > 4<<20 || n.SessionID != "" && n.SessionID != c.session {
		return acp.ErrProtocol
	}
	fields, err := ndjson.Object(n.Params)
	if err != nil {
		return acp.ErrProtocol
	}
	var owner string
	if raw, ok := fields["sessionId"]; ok {
		if json.Unmarshal(raw, &owner) != nil || owner != c.session {
			return acp.ErrProtocol
		}
	}
	if n.Method == "_kiro.dev/mcp/server_initialized" {
		return acp.ErrProtocol
	}
	if n.Method != "session/update" {
		return nil
	}
	if owner != c.session {
		return acp.ErrProtocol
	}
	update, err := ndjson.Object(fields["update"])
	if err != nil {
		return acp.ErrProtocol
	}
	var kind string
	if json.Unmarshal(update["sessionUpdate"], &kind) != nil {
		return acp.ErrProtocol
	}
	switch kind {
	case "agent_message_chunk":
		var part struct{ Type, Text string }
		if json.Unmarshal(update["content"], &part) != nil || part.Type != "text" || len(part.Text) > 1<<20-c.text.Len() {
			return acp.ErrProtocol
		}
		c.text.WriteString(part.Text)
		c.progress = part.Text != ""
		return nil
	case "agent_thought_chunk":
		var part struct{ Type, Text string }
		c.progress = json.Unmarshal(update["content"], &part) == nil && part.Type == "text" && strings.TrimSpace(part.Text) != ""
		return nil
	case "tool_call", "tool_call_update":
		err := c.tool(kind, update)
		c.progress = err == nil
		return err
	default:
		return nil
	}
}
func (c *collector) tool(kind string, fields map[string]json.RawMessage) error {
	if raw, present := fields["kind"]; present {
		var value string
		if json.Unmarshal(raw, &value) != nil || value != "search" {
			return acp.ErrProtocol
		}
	}
	if raw, present := fields["title"]; present {
		var value string
		if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" || len(value) > 4096 {
			return acp.ErrProtocol
		}
	}
	var id, status string
	if json.Unmarshal(fields["toolCallId"], &id) != nil || id == "" || len(id) > 256 {
		return acp.ErrProtocol
	}
	if raw, ok := fields["status"]; ok {
		if json.Unmarshal(raw, &status) != nil {
			return acp.ErrProtocol
		}
	}
	call := c.calls[id]
	if call == nil {
		if kind != "tool_call" && !kirofeature.StandaloneSearchFailure(fields) || len(c.calls) >= c.limit || fields["title"] == nil {
			return acp.ErrProtocol
		}
		digest := sha256.Sum256([]byte(c.session + "\x00" + id))
		call = &searchCall{exchange: anthropic.SearchExchange{ID: "srvtoolu_" + hex.EncodeToString(digest[:16]), Results: []anthropic.SearchResult{}}}
		c.calls[id] = call
		c.order = append(c.order, id)
	}
	if raw, ok := fields["rawInput"]; ok {
		input, err := ndjson.Object(raw)
		var query string
		if err != nil {
			return acp.ErrProtocol
		}
		if raw, present := input["query"]; present {
			if json.Unmarshal(raw, &query) != nil {
				return acp.ErrProtocol
			}
		}
		digest, err := canonicalDigest(raw)
		if err != nil {
			return err
		}
		frozen := call.status == "in_progress" || call.status == "completed" || call.status == "failed"
		if frozen && call.exchange.Query != "" && (query != call.exchange.Query || digest != call.input) {
			return acp.ErrProtocol
		}
		call.exchange.Query = query
		call.input = digest
	}
	provisional := call.exchange
	if provisional.Query == "" {
		provisional.Query = "pending"
	}
	if !provisional.Valid() {
		return acp.ErrProtocol
	}
	if raw, ok := fields["content"]; ok {
		digest, err := canonicalDigest(raw)
		if err != nil {
			return err
		}
		if call.status == "completed" || call.status == "failed" {
			if !call.hasOutput || digest != call.output {
				return acp.ErrProtocol
			}
		} else {
			call.content = append(json.RawMessage(nil), raw...)
			call.output = digest
			call.hasOutput = true
		}
	}
	if raw, ok := fields["rawOutput"]; ok {
		digest, err := canonicalDigest(raw)
		if err != nil {
			return err
		}
		if call.status == "completed" || call.status == "failed" {
			if !call.hasNative || digest != call.nativeDigest {
				return acp.ErrProtocol
			}
		} else {
			call.native = append(json.RawMessage(nil), raw...)
			call.nativeDigest = digest
			call.hasNative = true
		}
	}
	if status != "" {
		if call.status == "completed" || call.status == "failed" {
			if status != call.status {
				return acp.ErrProtocol
			}
		} else {
			if call.status == "in_progress" && status == "pending" {
				return acp.ErrProtocol
			}
			switch status {
			case "pending", "in_progress", "completed", "failed":
			default:
				return acp.ErrProtocol
			}
			call.status = status
			if status == "failed" {
				call.exchange.Results = nil
				call.exchange.ErrorCode = "unavailable"
			}
		}
	}
	if call.status == "completed" && (call.hasOutput || call.hasNative) {
		var results []anthropic.SearchResult
		var err error
		if call.hasNative {
			var hits []kirofeature.SearchHit
			hits, err = kirofeature.SearchResults(call.native, call.exchange.Query)
			for _, hit := range hits {
				results = append(results, anthropic.SearchResult{Type: "web_search_result", URL: hit.URL, Title: hit.Title})
			}
		} else {
			results, err = publicResults(call.content)
		}
		if err != nil {
			return err
		}
		call.exchange.Results = results
		if !call.exchange.Valid() {
			return acp.ErrProtocol
		}
	}
	return nil
}
func (c *collector) finish() ([]anthropic.SearchExchange, string, error) {
	if len(c.order) == 0 {
		return nil, "", acp.ErrProtocol
	}
	output := make([]anthropic.SearchExchange, 0, len(c.order))
	for _, id := range c.order {
		call := c.calls[id]
		if call.status != "completed" && call.status != "failed" || call.status == "completed" && !call.hasOutput && !call.hasNative || !call.exchange.Valid() {
			return nil, "", acp.ErrProtocol
		}
		output = append(output, call.exchange)
	}
	return output, c.text.String(), nil
}
func canonicalDigest(raw []byte) ([32]byte, error) {
	wrapped := append([]byte(`{"value":`), raw...)
	wrapped = append(wrapped, '}')
	canonical, err := jsoncanon.Object(wrapped)
	if err != nil {
		return [32]byte{}, acp.ErrProtocol
	}
	return sha256.Sum256(canonical), nil
}

// Public ACP resource links preserve URLs and titles without parsing generated answer prose.
// The separately observed native rawOutput shape is decoded in kirofeature.
func publicResults(raw []byte) ([]anthropic.SearchResult, error) {
	var parts []struct {
		Type    string
		Content struct{ Type, URI, Name, Title string }
	}
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &parts) != nil || len(parts) > 32 {
		return nil, acp.ErrProtocol
	}
	results := make([]anthropic.SearchResult, 0, len(parts))
	for _, part := range parts {
		if part.Type != "content" || part.Content.Type != "resource_link" {
			return nil, acp.ErrProtocol
		}
		title := part.Content.Title
		if title == "" {
			title = part.Content.Name
		}
		results = append(results, anthropic.SearchResult{Type: "web_search_result", URL: part.Content.URI, Title: title})
	}
	return results, nil
}
