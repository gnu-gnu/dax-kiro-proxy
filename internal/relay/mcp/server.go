// Package mcp is the execution-free stdio surface of a session relay. It exposes no filesystem,
// shell, sampling, resource retrieval or general-purpose process API.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/relay"
)

const ProtocolVersion = "2025-06-18"

type server struct {
	ctx     context.Context
	cancel  context.CancelFunc
	config  relay.ChildConfig
	output  *os.File
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]context.CancelFunc
	active  int
	jobs    sync.WaitGroup
}

func Run(ctx context.Context, input, output *os.File, config relay.ChildConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &server{ctx: ctx, cancel: cancel, config: config, output: output, pending: make(map[string]context.CancelFunc)}
	stop := context.AfterFunc(ctx, func() { _ = input.Close(); _ = output.Close() })
	defer stop()
	defer func() { cancel(); _ = input.Close(); s.jobs.Wait() }()
	reader := ndjson.NewReader(input, 8<<20)
	state := 0
	for {
		raw, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return relay.ErrCall
		}
		fields, err := ndjson.Object(raw)
		var protocol, method string
		if err != nil || !readString(fields["jsonrpc"], &protocol) || protocol != "2.0" || !readString(fields["method"], &method) || method == "" {
			return relay.ErrCall
		}
		if _, ok := fields["result"]; ok {
			return relay.ErrCall
		}
		if _, ok := fields["error"]; ok {
			return relay.ErrCall
		}
		id, request := fields["id"]
		if !request {
			switch method {
			case "notifications/initialized":
				if state == 1 {
					state = 2
				}
			case "notifications/cancelled":
				params, e := ndjson.Object(fields["params"])
				key, e2 := idKey(params["requestId"])
				if e == nil && e2 == nil {
					s.mu.Lock()
					if cancel := s.pending[key]; cancel != nil {
						cancel()
					}
					s.mu.Unlock()
				}
			}
			continue
		}
		key, err := idKey(id)
		if err != nil {
			return relay.ErrCall
		}
		s.mu.Lock()
		duplicate := s.pending[key] != nil
		s.mu.Unlock()
		if duplicate {
			return relay.ErrCall
		}
		params := map[string]json.RawMessage{}
		if v, ok := fields["params"]; ok {
			params, err = ndjson.Object(v)
			if err != nil {
				s.failure(id, -32602)
				continue
			}
		}
		if method == "initialize" {
			if state != 0 {
				s.failure(id, -32600)
				continue
			}
			var version, name, clientVersion string
			caps, e := ndjson.Object(params["capabilities"])
			info, e2 := ndjson.Object(params["clientInfo"])
			if !readString(params["protocolVersion"], &version) || version == "" || len(version) > 32 || e != nil || caps == nil || e2 != nil || !readString(info["name"], &name) || !readString(info["version"], &clientVersion) || len(name) == 0 || len(name) > 256 || len(clientVersion) == 0 || len(clientVersion) > 256 {
				s.failure(id, -32602)
				continue
			}
			state = 1
			s.success(id, map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "dax-kiro-proxy-relay", "version": "1"}})
			continue
		}
		if state == 0 || state != 2 && method != "ping" {
			s.failure(id, -32002)
			continue
		}
		switch method {
		case "ping":
			s.success(id, map[string]any{})
		case "tools/list":
			if _, cursor := params["cursor"]; cursor {
				s.failure(id, -32602)
				continue
			}
			s.success(id, map[string]any{"tools": config.Tools})
		case "tools/call":
			var name string
			if !readString(params["name"], &name) {
				s.failure(id, -32602)
				continue
			}
			known := false
			for _, tool := range config.Tools {
				if tool.Name == name {
					known = true
					break
				}
			}
			args, present := params["arguments"]
			if !present {
				args = json.RawMessage(`{}`)
			}
			if _, err := ndjson.Object(args); err != nil || !known {
				s.failure(id, -32602)
				continue
			}
			s.mu.Lock()
			if s.active >= 64 {
				s.mu.Unlock()
				s.failure(id, -32000)
				continue
			}
			callCtx, end := context.WithCancel(ctx)
			s.pending[key] = end
			s.active++
			s.jobs.Add(1)
			s.mu.Unlock()
			go func(id json.RawMessage, key, name string, args json.RawMessage) {
				defer s.jobs.Done()
				defer end()
				callID, err := relay.NewCallID()
				var result relay.ToolResult
				if err == nil {
					result, err = relay.Exchange(callCtx, config, relay.Call{Version: 1, Owner: config.Owner, Secret: config.Secret, ID: callID, Alias: name, Arguments: args})
				}
				s.mu.Lock()
				delete(s.pending, key)
				s.mu.Unlock()
				if err != nil {
					s.failure(id, -32000)
				} else {
					s.success(id, result)
				}
				s.mu.Lock()
				s.active--
				s.mu.Unlock()
			}(id, key, name, args)
		default:
			s.failure(id, -32601)
		}
		if ctx.Err() != nil {
			return relay.ErrClosed
		}
	}
}
func (s *server) success(id json.RawMessage, result any) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func (s *server) failure(id json.RawMessage, code int) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": "Relay request rejected"}})
}
func (s *server) write(value any) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 8<<20 {
		s.cancel()
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	// The watchdog closes owned pipes even on platforms where file deadlines are unsupported.
	watch := time.AfterFunc(5*time.Second, s.cancel)
	_, err = s.output.Write(append(raw, '\n'))
	watch.Stop()
	if err != nil {
		s.cancel()
	}
}
func readString(raw []byte, dest *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, dest) == nil
}
func idKey(raw []byte) (string, error) {
	var value string
	if readString(raw, &value) {
		if value == "" || len(value) > 128 {
			return "", relay.ErrCall
		}
		return "s:" + value, nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || n < -(1<<53-1) || n > 1<<53-1 {
		return "", relay.ErrCall
	}
	return "n:" + strconv.FormatInt(n, 10), nil
}
