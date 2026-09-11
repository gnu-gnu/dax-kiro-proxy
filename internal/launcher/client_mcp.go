package launcher

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
)

// Retain native client MCP scopes in a private mutable global file. Server declarations remain
// opaque to the launcher; the client validates, connects and applies precedence/permissions.
// The reviewed completed-onboarding flag is carried too (D115). Provider sign-in, model defaults,
// conversation, the client's theme record and other unrelated mutable state are never copied.
func clientMCPState(home string) ([]byte, error) {
	raw, err := readSettings(filepath.Join(home, ".claude.json"))
	if err != nil {
		return nil, err
	}
	return clientMCPProjection(raw)
}

// clientMCPProjection projects one already-read global document.
func clientMCPProjection(raw []byte) ([]byte, error) {
	fields, err := ndjson.Object(raw)
	if err != nil {
		return nil, ErrSettings
	}
	selected, err := clientMCPFields(fields, false)
	if err != nil {
		return nil, err
	}
	if rawProjects, ok := fields["projects"]; ok {
		projects, err := ndjson.Object(rawProjects)
		if err != nil {
			return nil, ErrSettings
		}
		retained := make(map[string]map[string]json.RawMessage)
		for path, rawProject := range projects {
			project, err := ndjson.Object(rawProject)
			if err != nil {
				return nil, ErrSettings
			}
			values, err := clientMCPFields(project, true)
			if err != nil {
				return nil, err
			}
			if len(values) != 0 {
				retained[path] = values
			}
		}
		if len(retained) != 0 {
			selected["projects"], _ = json.Marshal(retained)
		}
	}
	result, err := json.Marshal(selected)
	if err != nil || len(result) > MaxSettingsBytes {
		return nil, ErrSettings
	}
	return result, nil
}

func clientMCPFields(fields map[string]json.RawMessage, project bool) (map[string]json.RawMessage, error) {
	selected := make(map[string]json.RawMessage)
	for key, value := range fields {
		switch key {
		case "mcpServers":
			servers, err := ndjson.Object(value)
			if err != nil {
				return nil, ErrSettings
			}
			for _, server := range servers {
				if _, err := ndjson.Object(server); err != nil {
					return nil, ErrSettings
				}
			}
		case "enabledMcpjsonServers", "disabledMcpjsonServers", "enabledMcpServers", "disabledMcpServers", "mcpContextUris":
			var entries []json.RawMessage
			if len(value) == 0 || value[0] != '[' || json.Unmarshal(value, &entries) != nil {
				return nil, ErrSettings
			}
			for _, entry := range entries {
				if len(entry) == 0 || entry[0] != '"' {
					return nil, ErrSettings
				}
			}
		case "enableAllProjectMcpServers":
			if string(value) != "true" && string(value) != "false" {
				return nil, ErrSettings
			}
		case "claudeAiMcpEverConnected":
			// Reviewed: a claude.ai connector breadcrumb, not a server declaration or an
			// execution policy. Excluding it cannot broaden execution, and it is unrelated
			// account state that must stay out of the private client root.
			continue
		case "hasTrustDialogAccepted":
			if !project {
				continue
			}
			if string(value) != "true" && string(value) != "false" {
				return nil, ErrSettings
			}
		case "hasCompletedOnboarding":
			// Reviewed (D115): whether the client's first-run onboarding was completed. Carrying it
			// keeps the private profile from repeating the theme dialog; it grants nothing.
			if project {
				continue
			}
			if string(value) != "true" && string(value) != "false" {
				return nil, ErrSettings
			}
		default:
			// Dropping an unrecognized MCP policy might broaden execution. Require a reviewed
			// mapping instead of silently omitting it; unrelated client state stays private.
			if strings.Contains(strings.ToLower(key), "mcp") {
				return nil, ErrSettings
			}
			continue
		}
		selected[key] = value
	}
	return selected, nil
}
