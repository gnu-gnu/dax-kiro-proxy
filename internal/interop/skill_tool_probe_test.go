package interop_test

import (
	"encoding/json"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
)

const ownedSkillUseID = "owned-skill-use"

type pluginAssetMode uint8

const (
	pluginAssetsOnly pluginAssetMode = iota
	pluginModelSkill
	pluginGatewaySkill
)

func TestClaudeModelSelectedPluginSkill(t *testing.T) {
	observePluginAssetMode(t, false, pluginModelSkill)
}

func inspectOwnedSkillReturn(r *anthropic.Request) (count int, matched bool) {
	return inspectSkillReturn(r, ownedSkillUseID)
}

func inspectSkillReturn(r *anthropic.Request, expectedID string) (count int, matched bool) {
	matched = expectedID != ""
	for _, message := range r.Messages {
		for _, block := range message.Content {
			if block.Type != "tool_result" {
				continue
			}
			count++
			var result struct {
				ID    string `json:"tool_use_id"`
				Error bool   `json:"is_error"`
			}
			matched = matched && message.Role == "user" && json.Unmarshal(block.Raw, &result) == nil && result.ID == expectedID && !result.Error
		}
	}
	return count, matched && count == 1
}

func observedSkillID(r *anthropic.Request) string {
	var id string
	for _, message := range r.Messages {
		for _, block := range message.Content {
			if block.Type != "tool_use" {
				continue
			}
			var use struct {
				ID, Name string
				Input    map[string]string
			}
			if id != "" || message.Role != "assistant" || json.Unmarshal(block.Raw, &use) != nil || use.ID == "" || use.Name != "Skill" || len(use.Input) != 1 || use.Input["skill"] != "dax-assets:owned-skill" {
				return ""
			}
			id = use.ID
		}
	}
	return id
}

func TestOwnedSkillReturnRequiresOneMatchingSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, raw   string
		twice, want bool
	}{
		{"success", `{"type":"tool_result","tool_use_id":"owned-skill-use","content":"Independent skill result"}`, false, true},
		{"foreign", `{"type":"tool_result","tool_use_id":"other","content":"Independent skill result"}`, false, false},
		{"refused", `{"type":"tool_result","tool_use_id":"owned-skill-use","is_error":true,"content":"Independent refusal"}`, false, false},
		{"duplicate", `{"type":"tool_result","tool_use_id":"owned-skill-use","content":"Independent skill result"}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &anthropic.Request{Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "tool_result", Raw: json.RawMessage(tc.raw)}}}, {Role: "user", Content: []anthropic.Block{{Type: "text", Text: pluginSkillBody}}}}}
			if tc.twice {
				r.Messages[0].Content = append(r.Messages[0].Content, r.Messages[0].Content[0])
			}
			if _, ok := inspectOwnedSkillReturn(r); ok != tc.want {
				t.Fatal("skill result identity or status not established")
			}
		})
	}
	if _, ok := inspectOwnedSkillReturn(&anthropic.Request{}); ok {
		t.Fatal("absent skill result accepted")
	}
}
