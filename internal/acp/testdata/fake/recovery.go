package main

import (
	"encoding/json"
	"strings"
)

func freshRecoveryPrompt(raw []json.RawMessage, oldPath string) bool {
	if len(raw) < 6 || len(raw) > 8 || oldPath == "" {
		return false
	}
	parts := make([]string, len(raw))
	found := false
	for i, item := range raw {
		var part struct{ Type, Text string }
		if json.Unmarshal(item, &part) != nil || part.Type != "text" || strings.Contains(part.Text, oldPath) {
			return false
		}
		parts[i] = part.Text
		if part.Text == "Do not request any tool. Reply with a brief acknowledgement of this new independent request." {
			found = true
		}
	}
	var context struct {
		System  []string
		History []json.RawMessage
	}
	if !found || json.Unmarshal([]byte(parts[1]), &context) != nil || len(context.System) == 0 || len(context.History) != 0 || parts[2] != "Current user content follows." || parts[len(parts)-2] != "Client system update follows as JSON." {
		return false
	}
	var update struct {
		Role    string
		Content []string
	}
	return json.Unmarshal([]byte(parts[len(parts)-1]), &update) == nil && update.Role == "system" && len(update.Content) > 0
}
