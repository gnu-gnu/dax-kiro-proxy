package acp

import (
	"dax-kiro-proxy/internal/ndjson"
	"encoding/json"
)

func decodeCapabilities(raw []byte) (Capabilities, error) {
	var caps Capabilities
	fields, err := ndjson.Object(raw)
	if err != nil {
		return caps, ErrProtocol
	}
	if err = capabilityBool(fields, "loadSession", &caps.LoadSession); err != nil {
		return Capabilities{}, err
	}
	for _, group := range []struct {
		name   string
		values map[string]*bool
	}{
		{"promptCapabilities", map[string]*bool{"image": &caps.Prompt.Image, "audio": &caps.Prompt.Audio, "embeddedContext": &caps.Prompt.EmbeddedContext}},
		{"mcpCapabilities", map[string]*bool{"http": &caps.MCP.HTTP, "sse": &caps.MCP.SSE}},
	} {
		value, ok := fields[group.name]
		if !ok {
			continue
		}
		nested, err := ndjson.Object(value)
		if err != nil {
			return Capabilities{}, ErrProtocol
		}
		for name, dest := range group.values {
			if err = capabilityBool(nested, name, dest); err != nil {
				return Capabilities{}, err
			}
		}
	}
	return caps, nil
}
func capabilityBool(fields map[string]json.RawMessage, name string, dest *bool) error {
	value, ok := fields[name]
	if !ok {
		return nil
	}
	switch string(value) {
	case "true":
		*dest = true
	case "false":
		*dest = false
	default:
		return ErrProtocol
	}
	return nil
}
