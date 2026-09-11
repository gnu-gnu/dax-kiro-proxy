package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
)

var errFrame = errors.New("independent terminal frame limit or shape")

type frameGuard struct {
	maxPrompts             int
	maxFrames, maxBytes    int // zero selects the fixed defaults; the soak mode scales them
	mu                     sync.Mutex
	frames, total, prompts int
	id                     json.RawMessage
	done                   bool
}

func (g *frameGuard) inspect(from string, raw []byte) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if from != "client" && from != "agent" {
		return "", errFrame
	}
	g.frames++
	g.total += len(raw)
	maxFrames, maxBytes := g.maxFrames, g.maxBytes
	if maxFrames <= 0 {
		maxFrames = 1024
	}
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	if len(raw) > 256<<10 || g.frames > maxFrames || g.total > maxBytes {
		return "", errFrame
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return "", errFrame
	}
	var method string
	if p, ok := fields["method"]; ok && json.Unmarshal(p, &method) != nil {
		return "", errFrame
	}
	if from == "client" && method == "session/prompt" {
		id := fields["id"]
		limit := max(g.maxPrompts, 1)
		if len(id) == 0 || bytes.Equal(id, []byte("null")) || g.prompts >= limit || g.prompts > 0 && !g.done {
			return "", errFrame
		}
		g.id = append([]byte(nil), id...)
		g.prompts++
		g.done = false
		return "prompt", nil
	}
	if from == "client" && method == "session/cancel" {
		return "cancel", nil
	}
	if from == "agent" && g.prompts > 0 && !g.done {
		if method == "session/update" {
			var params struct {
				Update struct {
					SessionUpdate string
					Content       struct {
						Type string
						Text string
					}
				}
			}
			if json.Unmarshal(fields["params"], &params) != nil {
				return "", errFrame
			}
			if params.Update.SessionUpdate == "agent_message_chunk" && params.Update.Content.Type == "text" && params.Update.Content.Text != "" {
				return "text", nil
			}
		}
		if method == "" && bytes.Equal(fields["id"], g.id) {
			g.done = true
			var result struct{ StopReason string }
			if len(fields["error"]) != 0 || json.Unmarshal(fields["result"], &result) != nil {
				return "failed-result", nil
			}
			if result.StopReason == "cancelled" {
				return "cancelled", nil
			}
			if result.StopReason == "end_turn" {
				return "end", nil
			}
			return "failed-result", nil
		}
	}
	return "", nil
}
