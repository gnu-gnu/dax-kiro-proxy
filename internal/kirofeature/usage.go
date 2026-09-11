package kirofeature

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/ndjson"
)

var ErrUsageUnavailable = errors.New("Kiro account usage is unavailable")

// AccountUsage contains only reported CREDIT amounts. Supplemental buckets, labels and inferred
// balances are deliberately absent. The private command mapping is measured for Kiro 2.21.3/v2 (D114; first measured on 2.21.2).
type AccountUsage struct{ Used, Limit *float64 }

type UsagePeer interface {
	Caller
	Next(context.Context) (acp.Notification, error)
}

// ReadAccountUsage requires a fresh, empty-agent process owned by the caller. It creates one
// session and issues at most one tools inspection and one usage query, never a model prompt.
// The caller must join that process before discarding either its data or its private directory.
func ReadAccountUsage(ctx context.Context, peer UsagePeer, directory string) (AccountUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return AccountUsage{}, err
	}
	if peer == nil || !filepath.IsAbs(directory) || len(directory) > 4096 || strings.ContainsAny(directory, "\x00\r\n") {
		return AccountUsage{}, ErrUsageUnavailable
	}
	raw, err := peer.Call(ctx, "session/new", map[string]any{"cwd": directory, "mcpServers": []any{}})
	if err != nil {
		return AccountUsage{}, err
	}
	fields, err := usageObject(raw)
	var session string
	if err != nil || !stringField(fields["sessionId"], &session) || session == "" || len(session) > 1024 || strings.ContainsAny(session, "\x00\r\n") {
		return AccountUsage{}, ErrUsageUnavailable
	}
	advertised := false
	bytes := 0
	for count := 0; count < 64; count++ {
		n, err := peer.Next(ctx)
		if err != nil {
			return AccountUsage{}, err
		}
		bytes += len(n.Params)
		if bytes > 1<<20 {
			return AccountUsage{}, ErrUsageUnavailable
		}
		fields, err := usageObject(n.Params)
		if err != nil {
			return AccountUsage{}, err
		}
		if owner, present := fields["sessionId"]; present {
			var got string
			if !stringField(owner, &got) || got != session {
				return AccountUsage{}, ErrUsageUnavailable
			}
		}
		if n.Method == "_kiro.dev/mcp/server_initialized" {
			return AccountUsage{}, ErrUsageUnavailable
		}
		if n.Method != "_kiro.dev/commands/available" {
			continue
		}
		var owner string
		if !stringField(fields["sessionId"], &owner) || owner != session {
			return AccountUsage{}, ErrUsageUnavailable
		}
		commands, err := usageArray(fields["commands"], 128)
		if err != nil {
			return AccountUsage{}, err
		}
		seen := map[string]bool{}
		for _, item := range commands {
			command, err := usageObject(item)
			var name string
			if err != nil || !stringField(command["name"], &name) || len(name) > 256 {
				return AccountUsage{}, ErrUsageUnavailable
			}
			name = strings.TrimPrefix(name, "/")
			if name == "" || seen[name] {
				return AccountUsage{}, ErrUsageUnavailable
			}
			seen[name] = true
		}
		if !seen["usage"] || !seen["tools"] {
			return AccountUsage{}, ErrUsageUnavailable
		}
		advertised = true
		break
	}
	if !advertised {
		return AccountUsage{}, ErrUsageUnavailable
	}
	command := func(name string) (json.RawMessage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return peer.Call(ctx, "_kiro.dev/commands/execute", map[string]any{"sessionId": session, "command": map[string]any{"command": name, "args": map[string]any{}}})
	}
	raw, err = command("tools")
	if err != nil {
		return AccountUsage{}, err
	}
	data, err := usageResult(raw)
	if err != nil {
		return AccountUsage{}, err
	}
	tools, err := usageArray(data["tools"], 0)
	if err != nil || len(tools) != 0 {
		return AccountUsage{}, ErrUsageUnavailable
	}
	raw, err = command("usage")
	if err != nil {
		return AccountUsage{}, err
	}
	return DecodeAccountUsage(raw)
}

func DecodeAccountUsage(raw []byte) (AccountUsage, error) {
	data, err := usageResult(raw)
	if err != nil {
		return AccountUsage{}, err
	}
	entries, err := usageArray(data["usageBreakdowns"], 16)
	if err != nil {
		return AccountUsage{}, err
	}
	var result AccountUsage
	for _, raw := range entries {
		entry, err := usageObject(raw)
		var kind string
		if err != nil || !stringField(entry["resourceType"], &kind) {
			return AccountUsage{}, ErrUsageUnavailable
		}
		if kind != "CREDIT" {
			continue
		}
		if result.Used != nil {
			return AccountUsage{}, ErrUsageUnavailable
		}
		result.Used, err = usageAmount(entry["used"])
		if err != nil {
			return AccountUsage{}, err
		}
		switch string(entry["hasLimit"]) {
		case "true":
			result.Limit, err = usageAmount(entry["limit"])
			if err != nil {
				return AccountUsage{}, err
			}
		case "false":
		default:
			return AccountUsage{}, ErrUsageUnavailable
		}
	}
	if result.Used == nil {
		return AccountUsage{}, ErrUsageUnavailable
	}
	return result, nil
}

func usageAmount(raw json.RawMessage) (*float64, error) {
	var n float64
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' || json.Unmarshal(raw, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1e12 {
		return nil, ErrUsageUnavailable
	}
	return &n, nil
}
func usageObject(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > 64<<10 {
		return nil, ErrUsageUnavailable
	}
	obj, err := ndjson.Object(raw)
	if err != nil || len(obj) > 128 {
		return nil, ErrUsageUnavailable
	}
	return obj, nil
}
func usageResult(raw []byte) (map[string]json.RawMessage, error) {
	fields, err := usageObject(raw)
	if err != nil || string(fields["success"]) != "true" {
		return nil, ErrUsageUnavailable
	}
	return usageObject(fields["data"])
}
func usageArray(raw []byte, limit int) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || len(values) > limit {
		return nil, ErrUsageUnavailable
	}
	return values, nil
}
