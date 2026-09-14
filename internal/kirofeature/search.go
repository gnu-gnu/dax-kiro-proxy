package kirofeature

import (
	"context"
	"encoding/json"
	"strings"

	"dax-kiro-proxy/internal/acp"
)

// SearchInventory admits exactly one built-in search tool in a fresh isolated session. The
// private inventory is a policy check, never a replacement for public ACP client capabilities.
func SearchInventory(ctx context.Context, peer UsagePeer, session string) (json.RawMessage, error) {
	var advertisement json.RawMessage
	bytes := 0
	for i := 0; i < 64; i++ {
		n, err := peer.Next(ctx)
		if err != nil {
			return nil, err
		}
		bytes += len(n.Params)
		if bytes > 1<<20 || n.SessionID != "" && n.SessionID != session || n.Method == "_kiro.dev/mcp/server_initialized" {
			return nil, acp.ErrProtocol
		}
		fields, err := usageObject(n.Params)
		if err != nil {
			return nil, acp.ErrProtocol
		}
		if raw, ok := fields["sessionId"]; ok {
			var owner string
			if !stringField(raw, &owner) || owner != session {
				return nil, acp.ErrProtocol
			}
		}
		if n.Method != "_kiro.dev/commands/available" {
			continue
		}
		var owner string
		if !stringField(fields["sessionId"], &owner) || owner != session {
			return nil, acp.ErrProtocol
		}
		commands, err := usageArray(fields["commands"], 128)
		if err != nil {
			return nil, acp.ErrProtocol
		}
		found := false
		for _, raw := range commands {
			command, err := usageObject(raw)
			var name string
			if err != nil || !stringField(command["name"], &name) {
				return nil, acp.ErrProtocol
			}
			found = found || strings.TrimPrefix(name, "/") == "tools"
		}
		if !found {
			return nil, acp.ErrProtocol
		}
		advertisement = n.Params
		break
	}
	if advertisement == nil {
		return nil, acp.ErrProtocol
	}
	raw, err := peer.Call(ctx, "_kiro.dev/commands/execute", map[string]any{"sessionId": session, "command": map[string]any{"command": "tools", "args": map[string]any{}}})
	if err != nil {
		return nil, err
	}
	data, err := usageResult(raw)
	if err != nil {
		return nil, acp.ErrProtocol
	}
	items, err := usageArray(data["tools"], 1)
	if err != nil || len(items) != 1 {
		return nil, acp.ErrProtocol
	}
	tool, err := usageObject(items[0])
	var name string
	if err != nil || !stringField(tool["name"], &name) || name != "web_search" {
		return nil, acp.ErrProtocol
	}
	return advertisement, nil
}
