// Package anthropic owns client wire validation and response encoding.
package anthropic

import (
	"encoding/json"
	"errors"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
)

const MaxBodyBytes = 16 << 20

var ErrRequest = errors.New("invalid Messages request")

type Block struct {
	Type string
	Text string
	Raw  json.RawMessage
}
type Message struct {
	Role    string
	Content []Block
}

// ClientIdentity comes from authenticated gateway headers, never from the request body.
// Values are opaque identifiers; their format is not assumed to be a UUID.
type ClientIdentity struct {
	Session, Agent, ParentAgent string
}

type Request struct {
	Identity  ClientIdentity
	Model     string
	MaxTokens int64
	Stream    bool
	System    []Block
	Messages  []Message
	Tools     []json.RawMessage
	Effort    string
	Extra     map[string]json.RawMessage
	Warnings  []string
}

func DecodeRequest(body []byte) (*Request, error) {
	if len(body) > MaxBodyBytes {
		return nil, ErrRequest
	}
	fields, err := ndjson.Object(body)
	if err != nil {
		return nil, ErrRequest
	}
	r := &Request{Extra: make(map[string]json.RawMessage)}
	if !stringField(fields["model"], &r.Model) || len(r.Model) == 0 || len(r.Model) > 256 || strings.ContainsAny(r.Model, " \t\r\n/\\") {
		return nil, ErrRequest
	}
	if len(fields["max_tokens"]) == 0 || fields["max_tokens"][0] == 'n' || json.Unmarshal(fields["max_tokens"], &r.MaxTokens) != nil || r.MaxTokens <= 0 || r.MaxTokens > 1<<20 {
		return nil, ErrRequest
	}
	if raw, ok := fields["stream"]; ok {
		if string(raw) != "true" && string(raw) != "false" {
			return nil, ErrRequest
		}
		r.Stream = string(raw) == "true"
	}
	if raw, ok := fields["system"]; ok {
		r.System, err = content(raw)
		if err != nil {
			return nil, err
		}
		for _, b := range r.System {
			if b.Type != "text" {
				return nil, ErrRequest
			}
		}
	}
	var messages []json.RawMessage
	if json.Unmarshal(fields["messages"], &messages) != nil || len(messages) == 0 || len(messages) > 4096 {
		return nil, ErrRequest
	}
	for _, raw := range messages {
		m, err := ndjson.Object(raw)
		if err != nil {
			return nil, ErrRequest
		}
		var message Message
		if !stringField(m["role"], &message.Role) || (message.Role != "user" && message.Role != "assistant" && message.Role != "system") {
			return nil, ErrRequest
		}
		// These fields change instruction lifetime or generation policy. Unsupported semantics
		// must be rejected before they disappear from the normalized conversation.
		if _, present := m["output_config"]; present {
			return nil, ErrRequest
		}
		if raw, present := m["clear_at"]; present {
			var lifetime string
			if message.Role != "system" || !stringField(raw, &lifetime) || lifetime != "never" {
				return nil, ErrRequest
			}
		}
		message.Content, err = content(m["content"])
		if err != nil || len(message.Content) == 0 {
			return nil, ErrRequest
		}
		if message.Role == "system" {
			for _, b := range message.Content {
				if b.Type != "text" {
					return nil, ErrRequest
				}
			}
		}
		r.Messages = append(r.Messages, message)
	}
	if r.LatestUserIndex() < 0 {
		return nil, ErrRequest
	}
	if raw, ok := fields["tools"]; ok {
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &r.Tools) != nil || len(r.Tools) > 128 {
			return nil, ErrRequest
		}
		for _, tool := range r.Tools {
			if _, err := ndjson.Object(tool); err != nil {
				return nil, ErrRequest
			}
		}
	}
	if raw, ok := fields["output_config"]; ok {
		var output map[string]json.RawMessage
		if json.Unmarshal(raw, &output) != nil {
			r.Warnings = append(r.Warnings, "malformed output configuration ignored")
		} else if effort, ok := output["effort"]; ok {
			if !stringField(effort, &r.Effort) {
				r.Warnings = append(r.Warnings, "malformed effort ignored")
				r.Effort = ""
			} else {
				r.Effort = strings.ToLower(strings.TrimSpace(r.Effort))
				switch r.Effort {
				case "", "low", "medium", "high", "xhigh", "max":
				default:
					r.Effort = ""
					r.Warnings = append(r.Warnings, "unsupported effort ignored")
				}
			}
		}
	}
	for key, value := range fields {
		switch key {
		case "model", "max_tokens", "stream", "system", "messages", "tools":
		default:
			r.Extra[key] = value
		}
	}
	return r, nil
}

// LatestUserIndex excludes trailing per-message system context. Assistant prefill remains unsupported.
func (r *Request) LatestUserIndex() int {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		switch r.Messages[i].Role {
		case "system":
			continue
		case "user":
			return i
		default:
			return -1
		}
	}
	return -1
}
func content(raw json.RawMessage) ([]Block, error) {
	if len(raw) == 0 {
		return nil, ErrRequest
	}
	if raw[0] == '"' {
		var text string
		if !stringField(raw, &text) {
			return nil, ErrRequest
		}
		return []Block{{Type: "text", Text: text, Raw: raw}}, nil
	}
	if raw[0] != '[' {
		return nil, ErrRequest
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) > 4096 {
		return nil, ErrRequest
	}
	blocks := make([]Block, 0, len(values))
	for _, value := range values {
		obj, err := ndjson.Object(value)
		if err != nil {
			return nil, ErrRequest
		}
		b := Block{Raw: value}
		if !stringField(obj["type"], &b.Type) || b.Type == "" {
			return nil, ErrRequest
		}
		if b.Type == "text" && !stringField(obj["text"], &b.Text) {
			return nil, ErrRequest
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}
func stringField(raw json.RawMessage, dest *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, dest) == nil
}

// TextOnly is the initial negotiated subset; tool/media adapters extend it in subsequent phases.
func (r *Request) TextOnly() bool {
	if len(r.Tools) > 0 {
		return false
	}
	for _, m := range r.Messages {
		for _, b := range m.Content {
			if b.Type != "text" {
				return false
			}
		}
	}
	return true
}
