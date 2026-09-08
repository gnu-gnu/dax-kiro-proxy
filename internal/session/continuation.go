package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/toolregistry"
)

type terminalOutcome struct {
	compat  [32]byte
	ids     []string
	err     error
	expires time.Time
}

func (d *Driver) resume(ctx context.Context, r *anthropic.Request, registry *toolregistry.Registry, results []anthropic.ToolResult) (inference.Turn, error) {
	stamp, err := compatibility(r, registry)
	if err != nil {
		return nil, inference.ErrRequest
	}
	converted, err := relayResults(results)
	if err != nil {
		return nil, inference.ErrRequest
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if d.state == Prompting || d.state == Starting {
		return nil, ErrBusy
	}
	if d.outcome != nil && time.Now().After(d.outcome.expires) {
		d.outcome = nil
	}
	if d.state != WaitingTools || d.current == nil {
		if d.outcome != nil && d.outcome.compat == stamp && sameResultIDs(d.outcome.ids, results) {
			err := d.outcome.err
			d.outcome = nil
			return nil, err
		}
		return nil, inference.ErrRequest
	}
	t := d.current
	if len(results) == 0 {
		return nil, ErrBusy
	}
	if t.compat != stamp || len(r.Messages) != t.messageCount+2 || len(r.Messages[len(r.Messages)-1].Content) != len(results) {
		return nil, inference.ErrRequest
	}
	prefix, err := historyDigest(r.Messages[:t.messageCount])
	if err != nil || prefix != t.history {
		return nil, inference.ErrRequest
	}
	assistant, err := historyDigest(r.Messages[t.messageCount : t.messageCount+1])
	if err != nil || assistant != t.assistant {
		return nil, inference.ErrRequest
	}
	history, err := historyDigest(r.Messages)
	if err != nil {
		return nil, inference.ErrRequest
	}
	if err := t.broker.Resolve(t.broker.Credentials().Owner, converted); err != nil {
		return nil, inference.ErrRequest
	}
	t.history = history
	t.messageCount = len(r.Messages)
	t.lastIDs = nil
	d.state = Prompting
	return &round{turn: t}, nil
}
func sameResultIDs(ids []string, results []anthropic.ToolResult) bool {
	if len(ids) == 0 || len(ids) != len(results) {
		return false
	}
	set := make(map[string]bool)
	for _, id := range ids {
		set[id] = true
	}
	for _, r := range results {
		if !set[r.ID] {
			return false
		}
		delete(set, r.ID)
	}
	return len(set) == 0
}
func relayResults(input []anthropic.ToolResult) ([]relay.Result, error) {
	if len(input) > 64 {
		return nil, inference.ErrRequest
	}
	output := make([]relay.Result, 0, len(input))
	for _, result := range input {
		value := relay.Result{ID: result.ID, ToolResult: relay.ToolResult{IsError: result.IsError, Content: make([]relay.Content, 0, len(result.Content))}}
		for _, block := range result.Content {
			switch block.Type {
			case "text":
				value.Content = append(value.Content, relay.Content{Type: "text", Text: block.Text})
			case "image":
				fields, err := ndjson.Object(block.Raw)
				if err != nil {
					return nil, inference.ErrRequest
				}
				source, err := ndjson.Object(fields["source"])
				var kind, data, mime string
				if err != nil || !strictString(source["type"], &kind) {
					return nil, inference.ErrRequest
				}
				if kind == "base64" {
					if !strictString(source["data"], &data) || !strictString(source["media_type"], &mime) {
						return nil, inference.ErrRequest
					}
					value.Content = append(value.Content, relay.Content{Type: "image", Data: data, MIMEType: mime})
				} else {
					value.Content = append(value.Content, relay.Content{Type: "text", Text: string(block.Raw)})
				}
			default:
				value.Content = append(value.Content, relay.Content{Type: "text", Text: string(block.Raw)})
			}
		}
		output = append(output, value)
	}
	return output, nil
}
func compatibility(r *anthropic.Request, registry *toolregistry.Registry) ([32]byte, error) {
	fingerprint := ""
	if registry != nil {
		fingerprint = registry.Fingerprint()
	}
	system := make([]string, len(r.System))
	for i, b := range r.System {
		system[i] = b.Text
	}
	metadata := json.RawMessage(`{}`)
	if raw, ok := r.Extra["metadata"]; ok {
		value, err := jsoncanon.Object(raw)
		if err != nil {
			return [32]byte{}, err
		}
		metadata = value
	}
	disabled, err := r.ToolPolicy()
	if err != nil {
		return [32]byte{}, err
	}
	encoded, err := json.Marshal(struct {
		Version                 int
		Identity                anthropic.ClientIdentity
		Model, Effort, Registry string
		System                  []string
		Metadata                json.RawMessage
		ToolsDisabled           bool
	}{2, r.Identity, r.Model, r.Effort, fingerprint, system, metadata, disabled})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}
func historyDigest(messages []anthropic.Message) ([32]byte, error) {
	type item struct {
		Role    string
		Content []json.RawMessage
	}
	normalized := make([]item, 0, len(messages))
	for _, m := range messages {
		entry := item{Role: m.Role, Content: make([]json.RawMessage, 0, len(m.Content))}
		for _, b := range m.Content {
			var raw []byte
			var err error
			if b.Type == "text" {
				raw, err = json.Marshal(map[string]any{"type": "text", "text": b.Text})
			} else {
				raw, err = jsoncanon.Object(b.Raw)
			}
			if err != nil {
				return [32]byte{}, err
			}
			entry.Content = append(entry.Content, raw)
		}
		normalized = append(normalized, entry)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil || len(encoded) > anthropic.MaxBodyBytes {
		return [32]byte{}, inference.ErrRequest
	}
	return sha256.Sum256(encoded), nil
}
func assistantDigest(text string, calls []relay.Use) ([32]byte, []string, error) {
	m := anthropic.Message{Role: "assistant"}
	ids := make([]string, 0, len(calls))
	if text != "" {
		m.Content = append(m.Content, anthropic.Block{Type: "text", Text: text})
	}
	for _, call := range calls {
		raw, err := json.Marshal(anthropic.ToolUse{ID: call.ID, Name: call.Name, Input: call.Input}.Block())
		if err != nil {
			return [32]byte{}, nil, err
		}
		m.Content = append(m.Content, anthropic.Block{Type: "tool_use", Raw: raw})
		ids = append(ids, call.ID)
	}
	h, err := historyDigest([]anthropic.Message{m})
	return h, ids, err
}
