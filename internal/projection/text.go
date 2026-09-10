// Package projection converts validated client content into public ACP prompt parts.
package projection

import (
	"dax-kiro-proxy/internal/anthropic"
	"encoding/json"
)

type Text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type historyMessage struct {
	Role    string   `json:"role"`
	Content []string `json:"content"`
}

// Delta projects only the proven uncommitted suffix. The backend already owns the preceding
// assistant response and stable top-level system context.
func Delta(r *anthropic.Request, start int) ([]Text, error) {
	if start < 0 || start > r.LatestUserIndex() {
		return nil, anthropic.ErrRequest
	}
	copy := *r
	copy.System = nil
	copy.Messages = r.Messages[start:]
	return Full(&copy)
}

// Full is a fresh-session projection. JSON preserves role boundaries without an ambiguous
// delimiter around user-supplied historical text. The latest user blocks retain their order.
func Full(r *anthropic.Request) ([]Text, error) {
	if !r.ClientContent() || len(r.Messages) == 0 {
		return nil, anthropic.ErrRequest
	}
	for _, m := range r.Messages {
		for _, b := range m.Content {
			if b.Type == "image" || b.Type == "document" {
				return nil, anthropic.ErrRequest
			}
			if b.Type == "tool_result" {
				result, err := anthropic.DecodeToolResult(b.Raw)
				if err != nil {
					return nil, err
				}
				content, err := result.PromptContent()
				if err != nil {
					return nil, err
				}
				for _, child := range content {
					if _, ok := child.Media(); ok {
						return nil, anthropic.ErrRequest
					}
				}
			}
		}
	}
	latest := r.LatestUserIndex()
	if latest < 0 {
		return nil, anthropic.ErrRequest
	}
	var parts []Text
	if len(r.System) > 0 || latest > 0 {
		data := struct {
			System  []string         `json:"system"`
			History []historyMessage `json:"history"`
		}{System: []string{}, History: []historyMessage{}}
		for _, b := range r.System {
			data.System = append(data.System, b.Text)
		}
		for _, m := range r.Messages[:latest] {
			item := historyMessage{Role: m.Role, Content: []string{}}
			for _, b := range m.Content {
				if b.Type == "text" {
					item.Content = append(item.Content, b.Text)
				} else {
					item.Content = append(item.Content, string(b.Raw))
				}
			}
			data.History = append(data.History, item)
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, anthropic.ErrRequest
		}
		parts = append(parts, Text{"text", "Conversation context follows as JSON; preserve its role and content order."}, Text{"text", string(encoded)}, Text{"text", "Current user content follows."})
	}
	for _, b := range r.Messages[latest].Content {
		switch b.Type {
		case "text":
			parts = append(parts, Text{"text", b.Text})
		case "tool_result":
			if _, err := anthropic.DecodeToolResult(b.Raw); err != nil {
				return nil, err
			}
			parts = append(parts, Text{"text", "Client tool result follows as JSON."}, Text{"text", string(b.Raw)})
		default:
			return nil, anthropic.ErrRequest
		}
	}
	for _, message := range r.Messages[latest+1:] {
		update := historyMessage{Role: message.Role, Content: []string{}}
		for _, b := range message.Content {
			update.Content = append(update.Content, b.Text)
		}
		encoded, err := json.Marshal(update)
		if err != nil {
			return nil, anthropic.ErrRequest
		}
		parts = append(parts, Text{"text", "Client system update follows as JSON."}, Text{"text", string(encoded)})
	}
	return parts, nil
}
