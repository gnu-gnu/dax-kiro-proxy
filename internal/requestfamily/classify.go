// Package requestfamily separates narrow background work from the foreground conversation.
package requestfamily

import (
	"encoding/json"
	"strings"
	"unicode"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/ndjson"
)

type Kind string

const (
	Main         Kind = "main"
	Title        Kind = "title"
	ToolFollowup Kind = "tool-follow-up"
	LocalCommand Kind = "local-command"
	Resume       Kind = "resume"
	Retry        Kind = "retry"
)

func Classify(r *anthropic.Request) Kind {
	if results, err := r.LatestToolResults(); err == nil && len(results) > 0 {
		return ToolFollowup
	}
	if len(r.Tools) != 0 {
		return Main
	}
	systemBytes := 0
	for _, b := range r.System {
		systemBytes += len(b.Text)
	}
	if systemBytes > 32<<10 || len(r.Extra["thinking"]) > 1024 || len(r.Extra["output_config"]) > 64<<10 {
		return Main
	}
	// The public client's force-disable option omits thinking even on title requests. Omission
	// is accepted only alongside every schema/purpose signal below; explicit reasoning is not.
	if raw, declared := r.Extra["thinking"]; declared {
		thinking, err := ndjson.Object(raw)
		if err != nil || !stringIs(thinking["type"], "disabled") {
			return Main
		}
	}
	output, err := ndjson.Object(r.Extra["output_config"])
	if err != nil {
		return Main
	}
	format, err := ndjson.Object(output["format"])
	if err != nil || !stringIs(format["type"], "json_schema") {
		return Main
	}
	schema, err := ndjson.Object(format["schema"])
	if err != nil || !stringIs(schema["type"], "object") {
		return Main
	}
	properties, err := ndjson.Object(schema["properties"])
	if err != nil || len(properties) != 1 {
		return Main
	}
	title, err := ndjson.Object(properties["title"])
	if err != nil || !stringIs(title["type"], "string") {
		return Main
	}
	var required []string
	if json.Unmarshal(schema["required"], &required) != nil || len(required) != 1 || required[0] != "title" {
		return Main
	}
	words := map[string]bool{}
	for _, b := range r.System {
		for _, word := range strings.FieldsFunc(strings.ToLower(b.Text), func(r rune) bool { return !unicode.IsLetter(r) }) {
			words[word] = true
		}
	}
	if words["title"] && (words["conversation"] || words["session"] || words["chat"]) && (words["create"] || words["generate"] || words["write"] || words["return"]) {
		return Title
	}
	return Main
}
func stringIs(raw json.RawMessage, want string) bool {
	var value string
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &value) == nil && value == want
}

func Group(kind Kind) string {
	if kind == Title {
		return "title"
	}
	return "main"
}
