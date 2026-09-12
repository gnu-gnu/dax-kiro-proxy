package session

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
)

const maxProgressTools = 4096

// These public control fields establish activity, not answer content or permission to run a tool.
// Unknown or unusable informational updates remain ignored. Their payloads are never forwarded.
func (t *turn) progress(kind string, fields map[string]json.RawMessage) bool {
	switch kind {
	case "agent_thought_chunk":
		content, err := ndjson.Object(fields["content"])
		return err == nil && progressEnum(content["type"], false, "text") && progressText(content["text"])
	case "plan":
		var entries []json.RawMessage
		if json.Unmarshal(fields["entries"], &entries) != nil || len(entries) == 0 || len(entries) > 4096 {
			return false
		}
		for _, raw := range entries {
			entry, err := ndjson.Object(raw)
			if err != nil || !progressText(entry["content"]) || !progressEnum(entry["priority"], false, "high", "medium", "low") || !progressEnum(entry["status"], false, "pending", "in_progress", "completed") {
				return false
			}
		}
		return true
	case "tool_call", "tool_call_update":
		var id string
		if !strictString(fields["toolCallId"], &id) || strings.TrimSpace(id) == "" || len(id) > 1024 {
			return false
		}
		if !progressEnum(fields["kind"], true, "read", "edit", "delete", "move", "search", "execute", "think", "fetch", "other") || !progressEnum(fields["status"], kind == "tool_call", "pending", "in_progress", "completed", "failed") {
			return false
		}
		key := sha256.Sum256([]byte(id))
		if kind == "tool_call_update" {
			if raw, present := fields["title"]; present && !progressText(raw) {
				return false
			}
			_, observed := t.progressTools[key]
			return observed
		}
		if !progressText(fields["title"]) {
			return false
		}
		if t.progressTools == nil {
			t.progressTools = make(map[[32]byte]struct{})
		}
		// Extra valid starts still count. Untracked later statuses cannot establish progress.
		if len(t.progressTools) < maxProgressTools {
			t.progressTools[key] = struct{}{}
		}
		return true
	default:
		return false
	}
}

func progressText(raw json.RawMessage) bool {
	var text string
	return strictString(raw, &text) && strings.TrimSpace(text) != ""
}

func progressEnum(raw json.RawMessage, optional bool, values ...string) bool {
	if len(raw) == 0 {
		return optional
	}
	var value string
	return strictString(raw, &value) && slices.Contains(values, value)
}
