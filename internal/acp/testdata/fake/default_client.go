package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// The independent observer records only its own process identities. A replacement must not start
// until the previous group is gone; at most two processes belong to this synthetic exercise.
func defaultClientProcess(path string) bool {
	var previous []string
	f, err := os.Open(path)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, 1025))
		_ = f.Close()
		if readErr != nil || len(data) > 1024 {
			os.Exit(94)
		}
		previous = strings.Fields(string(data))
	} else if !os.IsNotExist(err) {
		os.Exit(94)
	}
	if len(previous) > 1 {
		os.Exit(94)
	}
	for _, value := range previous {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 1 || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			os.Exit(94)
		}
	}
	out, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(94)
	}
	_, err = out.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	if closeErr := out.Close(); err != nil || closeErr != nil {
		os.Exit(94)
	}
	return len(previous) == 1
}

// Inspect only the new project's documented projection. Never save the client's instructions or
// tool payloads. The result must refer to the historical Read request and contain the owned denial.
func defaultClientHistory(parts []json.RawMessage, target string) bool {
	var texts []string
	for _, raw := range parts {
		var p struct{ Type, Text string }
		if json.Unmarshal(raw, &p) != nil || p.Type != "text" {
			return false
		}
		texts = append(texts, p.Text)
	}
	if len(texts) != 7 || texts[2] != "Current user content follows." || texts[3] != "Client tool result follows as JSON." || texts[5] != "Client system update follows as JSON." {
		return false
	}
	var context struct {
		History []struct {
			Role    string
			Content []string
		}
	}
	var use struct {
		Type, ID, Name string
		Input          map[string]string
	}
	var result struct {
		Type    string
		ID      string `json:"tool_use_id"`
		Error   bool   `json:"is_error"`
		Content json.RawMessage
	}
	var update struct {
		Role    string
		Content []string
	}
	if json.Unmarshal([]byte(texts[1]), &context) != nil || len(context.History) != 3 {
		return false
	}
	prior := context.History[2]
	if prior.Role != "assistant" || len(prior.Content) != 2 || prior.Content[0] != "before client tool" || json.Unmarshal([]byte(prior.Content[1]), &use) != nil || use.Type != "tool_use" || use.ID == "" || use.Name != "Read" || len(use.Input) != 1 || use.Input["file_path"] != target {
		return false
	}
	if json.Unmarshal([]byte(texts[4]), &result) != nil || result.Type != "tool_result" || result.ID != use.ID || !result.Error || !strings.Contains(string(result.Content), "independent fixture denial") {
		return false
	}
	if json.Unmarshal([]byte(texts[6]), &update) != nil || update.Role != "system" || len(update.Content) != 1 || len(update.Content[0]) == 0 {
		return false
	}
	return context.History[0].Role == "user" && context.History[1].Role == "system" && len(context.History[1].Content) == 1 && context.History[1].Content[0] != update.Content[0]
}
