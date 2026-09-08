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

// Full is a fresh-session projection. JSON preserves role boundaries without an ambiguous
// delimiter around user-supplied historical text. The latest user blocks retain their order.
func Full(r *anthropic.Request) ([]Text, error) {
	if !r.TextOnly() || len(r.Messages) == 0 {
		return nil, anthropic.ErrRequest
	}
	var parts []Text
	if len(r.System) > 0 || len(r.Messages) > 1 {
		data := struct {
			System  []string         `json:"system"`
			History []historyMessage `json:"history"`
		}{System: []string{}, History: []historyMessage{}}
		for _, b := range r.System {
			data.System = append(data.System, b.Text)
		}
		for _, m := range r.Messages[:len(r.Messages)-1] {
			item := historyMessage{Role: m.Role, Content: []string{}}
			for _, b := range m.Content {
				item.Content = append(item.Content, b.Text)
			}
			data.History = append(data.History, item)
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, anthropic.ErrRequest
		}
		parts = append(parts, Text{"text", "Conversation context follows as JSON; preserve its role and content order."}, Text{"text", string(encoded)}, Text{"text", "Current user content follows."})
	}
	for _, b := range r.Messages[len(r.Messages)-1].Content {
		parts = append(parts, Text{"text", b.Text})
	}
	return parts, nil
}
