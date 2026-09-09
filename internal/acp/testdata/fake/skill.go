package main

import (
	"encoding/json"
	"strings"
)

const fixtureSkillBody = "Independent skill invocation marker for client asset preservation."

func expandedSkillHistory(parts []json.RawMessage) bool {
	if len(parts) != 8 {
		return false
	}
	texts := make([]string, len(parts))
	for i, raw := range parts {
		var part struct{ Type, Text string }
		if json.Unmarshal(raw, &part) != nil || part.Type != "text" {
			return false
		}
		texts[i] = part.Text
	}
	if texts[2] != "Current user content follows." || texts[3] != "Client tool result follows as JSON." || !strings.Contains(texts[5], fixtureSkillBody) || texts[6] != "Client system update follows as JSON." {
		return false
	}
	var prior struct {
		History []struct {
			Role    string
			Content []string
		}
	}
	if json.Unmarshal([]byte(texts[1]), &prior) != nil || len(prior.History) != 3 || prior.History[0].Role != "user" || prior.History[1].Role != "system" {
		return false
	}
	last := prior.History[2]
	var use struct {
		Type, ID, Name string
		Input          map[string]string
	}
	if last.Role != "assistant" || len(last.Content) != 2 || last.Content[0] != "before client tool" || json.Unmarshal([]byte(last.Content[1]), &use) != nil || use.Type != "tool_use" || use.Name != "Skill" || use.ID == "" || len(use.Input) != 1 || use.Input["skill"] != "dax-assets:owned-skill" {
		return false
	}
	var result struct {
		Type    string
		ID      string `json:"tool_use_id"`
		Error   bool   `json:"is_error"`
		Content json.RawMessage
	}
	if json.Unmarshal([]byte(texts[4]), &result) != nil || result.Type != "tool_result" || result.ID != use.ID || result.Error || len(result.Content) > 64<<10 {
		return false
	}
	var resultText string
	if json.Unmarshal(result.Content, &resultText) != nil {
		var blocks []struct{ Type, Text string }
		if json.Unmarshal(result.Content, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
			return false
		}
		resultText = blocks[0].Text
	}
	if resultText == "" {
		return false
	}
	var update struct {
		Role    string
		Content []string
	}
	return json.Unmarshal([]byte(texts[7]), &update) == nil && update.Role == "system" && len(update.Content) == 1 && update.Content[0] != ""
}
