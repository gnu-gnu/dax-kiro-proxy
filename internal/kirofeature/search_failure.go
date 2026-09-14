package kirofeature

import (
	"encoding/json"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
)

// StandaloneSearchFailure recognizes the self-contained failed update observed when Kiro
// 2.21.4 blocks a search before emitting its initial call. It does not infer the failure cause.
// The caller still validates the session owner, call ID, query, final identity and budget.
func StandaloneSearchFailure(fields map[string]json.RawMessage) bool {
	for key, want := range map[string]string{"sessionUpdate": "tool_call_update", "status": "failed", "kind": "search", "title": "web_search"} {
		var value string
		if !stringField(fields[key], &value) || value != want {
			return false
		}
	}
	if _, present := fields["rawOutput"]; present {
		return false
	}
	input, err := ndjson.Object(fields["rawInput"])
	var query string
	if err != nil || !stringField(input["query"], &query) || strings.TrimSpace(query) == "" || len(query) > 4096 {
		return false
	}
	raw := fields["content"]
	var parts []json.RawMessage
	if len(raw) > 64<<10 || json.Unmarshal(raw, &parts) != nil || len(parts) == 0 || len(parts) > 8 {
		return false
	}
	for _, raw := range parts {
		part, err := ndjson.Object(raw)
		var kind, text string
		if err != nil || !stringField(part["type"], &kind) || kind != "content" {
			return false
		}
		content, err := ndjson.Object(part["content"])
		if err != nil || !stringField(content["type"], &kind) || kind != "text" || !stringField(content["text"], &text) || strings.TrimSpace(text) == "" {
			return false
		}
	}
	return true
}
