package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"time"
	"unicode/utf8"
)

var identifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,47}$`)
var integerID = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,17})$`)

func member(s string) bool { return identifier.MatchString(s) }

func requestKey(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	var s string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil && len(s) <= 96 {
		return "string:" + s, true
	}
	if integerID.Match(raw) {
		return "number:" + string(raw), true
	}
	return "", false
}

func serve(ctx context.Context, input io.ReadCloser, output io.Writer, tool string, delay time.Duration, mark func(string) bool) bool {
	return serveWithin(ctx, input, output, tool, delay, 10*time.Second, mark)
}

func serveWithin(ctx context.Context, input io.ReadCloser, output io.Writer, tool string, delay, maximum time.Duration, mark func(string) bool) bool {
	if !member(tool) || delay <= 0 || delay > maximum || maximum != 10*time.Second && maximum != 135*time.Second {
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	frames := make(chan []byte, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(frames)
		s := bufio.NewScanner(input)
		s.Buffer(make([]byte, 4096), 64<<10)
		for n := 0; n < 32; n++ {
			if !s.Scan() {
				if s.Err() != nil {
					select {
					case frames <- nil:
					case <-ctx.Done():
					}
				}
				return
			}
			select {
			case frames <- bytes.Clone(s.Bytes()):
			case <-ctx.Done():
				return
			}
		}
		select {
		case frames <- nil:
		case <-ctx.Done():
		}
	}()
	defer func() { cancel(); _ = input.Close(); <-readDone }()
	seen := map[string]bool{}
	handshake, ready, listed, called, cancelled := false, false, false, false, false
	callKey := ""
	var pending map[string]any
	var timer *time.Timer
	var due <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	send := func(v any, event string) bool {
		return json.NewEncoder(output).Encode(v) == nil && mark(event)
	}
	for {
		select {
		case <-ctx.Done():
			return mark("lifetime_closed")
		case <-due:
			kind := "call_sent"
			if cancelled {
				kind = "late_call_sent"
			}
			if !send(pending, kind) {
				return false
			}
			pending, due = nil, nil
		case raw, ok := <-frames:
			if !ok {
				return mark("input_closed")
			}
			var q struct {
				RPC    string          `json:"jsonrpc"`
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if raw == nil || !utf8.Valid(raw) || json.Unmarshal(raw, &q) != nil || q.RPC != "2.0" {
				return false
			}
			if len(q.ID) == 0 {
				switch q.Method {
				case "notifications/initialized":
					if !handshake || ready || !mark("initialized_notice") {
						return false
					}
					ready = true
				case "notifications/cancelled":
					var p struct {
						ID json.RawMessage `json:"requestId"`
					}
					if json.Unmarshal(q.Params, &p) != nil {
						return false
					}
					key, valid := requestKey(p.ID)
					if !valid {
						return false
					}
					kind := "foreign_cancel"
					if called && key == callKey {
						kind = "call_cancelled"
						if pending == nil {
							kind = "after_result_cancel"
						} else if cancelled {
							kind = "repeated_cancel"
						}
						cancelled = true
					}
					if !mark(kind) {
						return false
					}
				default:
					return false
				}
				continue
			}
			key, valid := requestKey(q.ID)
			if !valid || seen[key] || len(seen) >= 16 {
				return false
			}
			seen[key] = true
			reply := map[string]any{"jsonrpc": "2.0", "id": q.ID}
			switch q.Method {
			case "initialize":
				var p struct {
					Version string `json:"protocolVersion"`
				}
				if handshake || json.Unmarshal(q.Params, &p) != nil || len(p.Version) == 0 || len(p.Version) > 32 {
					return false
				}
				handshake = true
				reply["result"] = map[string]any{"protocolVersion": p.Version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "independent-timing-peer", "version": "1"}}
				if !send(reply, "initialize_sent") {
					return false
				}
			case "ping":
				reply["result"] = map[string]any{}
				if !send(reply, "ping_sent") {
					return false
				}
			case "tools/list":
				if !ready || listed {
					return false
				}
				listed = true
				reply["result"] = map[string]any{"tools": []any{map[string]any{"name": tool, "description": "Independent delayed result; performs no file, shell or network action.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}}}
				if !send(reply, "list_sent") {
					return false
				}
			case "tools/call":
				var p struct {
					Name      string
					Arguments map[string]json.RawMessage
				}
				if !listed || called || json.Unmarshal(q.Params, &p) != nil || p.Name != tool || p.Arguments == nil || len(p.Arguments) != 0 {
					_ = mark("call_rejected")
					return false
				}
				called, callKey = true, key
				if !mark("call_received") {
					return false
				}
				reply["result"] = map[string]any{"isError": false, "content": []any{map[string]string{"type": "text", "text": "Owned delayed result is available."}}}
				pending = reply
				timer = time.NewTimer(delay)
				due = timer.C
			default:
				return false
			}
		}
	}
}
