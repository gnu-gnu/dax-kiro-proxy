package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func fixturePluginMarker(path string) string {
	if !filepath.IsAbs(path) {
		os.Exit(94)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		os.Exit(94)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 64 {
		os.Exit(94)
	}
	data, err := io.ReadAll(io.LimitReader(f, 65))
	if err != nil || len(data) < 16 || len(data) > 64 {
		os.Exit(94)
	}
	for _, ch := range data {
		if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
			os.Exit(94)
		}
	}
	return string(data)
}

// Inspect only this project's projection and the owned tool result. The installed client's
// instructions and wait text are neither interpreted as instructions nor persisted by this peer.
func pluginWaitHistory(parts []json.RawMessage, stage int) bool {
	var texts []string
	for _, raw := range parts {
		var part struct{ Type, Text string }
		if json.Unmarshal(raw, &part) != nil || part.Type != "text" {
			return false
		}
		texts = append(texts, part.Text)
	}
	if stage == 0 {
		return len(texts) > 0
	}
	if stage != 1 || len(texts) != 7 || texts[2] != "Current user content follows." {
		return false
	}
	var prior struct {
		History []struct {
			Role    string
			Content []string
		}
	}
	if json.Unmarshal([]byte(texts[1]), &prior) != nil || len(prior.History) != 3 || prior.History[0].Role != "user" || prior.History[1].Role != "system" || prior.History[2].Role != "assistant" || len(prior.History[2].Content) != 1 {
		return false
	}
	var use struct {
		Type, ID, Name string
		Input          map[string]any
	}
	if json.Unmarshal([]byte(prior.History[2].Content[0]), &use) != nil || use.Type != "tool_use" || use.ID == "" || use.Input == nil || len(use.Input) != 0 || use.Name != "WaitForMcpServers" || texts[3] != "Client tool result follows as JSON." {
		return false
	}
	var result struct {
		Type    string
		ID      string `json:"tool_use_id"`
		IsError bool   `json:"is_error"`
		Content json.RawMessage
	}
	if json.Unmarshal([]byte(texts[4]), &result) != nil || result.Type != "tool_result" || result.ID != use.ID || result.IsError {
		return false
	}
	var content []struct{ Type, Text string }
	var text string
	if len(result.Content) > 0 && result.Content[0] == '"' && json.Unmarshal(result.Content, &text) == nil {
		content = append(content, struct{ Type, Text string }{"text", text})
	} else if json.Unmarshal(result.Content, &content) != nil {
		return false
	}
	if len(content) != 1 || content[0].Type != "text" {
		return false
	}
	var update struct {
		Role    string
		Content []string
	}
	return texts[len(texts)-2] == "Client system update follows as JSON." && json.Unmarshal([]byte(texts[len(texts)-1]), &update) == nil && update.Role == "system" && len(update.Content) == 1 && len(update.Content[0]) > 0
}
