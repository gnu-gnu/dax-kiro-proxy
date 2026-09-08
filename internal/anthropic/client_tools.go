package anthropic

import (
	"dax-kiro-proxy/internal/ndjson"
)

type ToolResult struct {
	ID      string
	Content []Block
	IsError bool
}

func DecodeToolResult(raw []byte) (ToolResult, error) {
	fields, err := ndjson.Object(raw)
	var result ToolResult
	if err != nil || string(fields["type"]) != `"tool_result"` || !stringField(fields["tool_use_id"], &result.ID) || len(result.ID) == 0 || len(result.ID) > 96 {
		return ToolResult{}, ErrRequest
	}
	if flag, ok := fields["is_error"]; ok {
		if string(flag) != "true" && string(flag) != "false" {
			return ToolResult{}, ErrRequest
		}
		result.IsError = string(flag) == "true"
	}
	if raw, ok := fields["content"]; ok {
		result.Content, err = content(raw)
		if err != nil {
			return ToolResult{}, ErrRequest
		}
	}
	return result, nil
}

// ClientContent validates the currently supported top-level message blocks. Result payloads can
// contain unsupported content; the relay converts that payload to bounded text without effects.
func (r *Request) ClientContent() bool {
	for _, message := range r.Messages {
		for _, block := range message.Content {
			switch block.Type {
			case "text":
			case "tool_use":
				fields, err := ndjson.Object(block.Raw)
				var t ToolUse
				if err != nil || message.Role != "assistant" || !stringField(fields["id"], &t.ID) || !stringField(fields["name"], &t.Name) {
					return false
				}
				t.Input = fields["input"]
				if !t.Valid() {
					return false
				}
			case "tool_result":
				if message.Role != "user" {
					return false
				}
				if _, err := DecodeToolResult(block.Raw); err != nil {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// ToolPolicy accepts only choices the current adapter can enforce. Required/specific tools and
// disabled parallel use need an explicit adapter before being accepted.
func (r *Request) ToolPolicy() (disabled bool, err error) {
	raw, present := r.Extra["tool_choice"]
	if !present {
		return false, nil
	}
	fields, err := ndjson.Object(raw)
	var kind string
	if err != nil || !stringField(fields["type"], &kind) {
		return false, ErrRequest
	}
	for key, value := range fields {
		switch key {
		case "type":
		case "disable_parallel_tool_use":
			if string(value) != "false" {
				return false, ErrRequest
			}
		default:
			return false, ErrRequest
		}
	}
	switch kind {
	case "auto":
		return false, nil
	case "none":
		return true, nil
	default:
		return false, ErrRequest
	}
}

func (r *Request) LatestToolResults() ([]ToolResult, error) {
	latest := r.LatestUserIndex()
	if latest < 0 {
		return nil, ErrRequest
	}
	var results []ToolResult
	for _, b := range r.Messages[latest].Content {
		if b.Type == "tool_result" {
			v, err := DecodeToolResult(b.Raw)
			if err != nil {
				return nil, err
			}
			results = append(results, v)
		}
	}
	return results, nil
}
