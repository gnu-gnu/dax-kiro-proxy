package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/status"
	"dax-kiro-proxy/internal/toolregistry"
)

type terminalOutcome struct {
	compat  [32]byte
	ids     []string
	pending history.Snapshot
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
	var estimated status.InputEstimate
	if d.estimator != nil {
		estimated, _ = d.estimator.Measure(r)
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
		// The retired outcome answers every identical resubmission the same way until it expires or a
		// new turn starts; consuming it on first read turned a client retry into a request error.
		if d.outcome != nil && d.outcome.compat == stamp && sameResultIDs(d.outcome.ids, results) {
			return nil, d.outcome.err
		}
		return nil, inference.ErrRequest
	}
	t := d.current
	if len(results) == 0 {
		return nil, ErrBusy
	}
	if t.compat != stamp {
		return nil, inference.ErrRequest
	}
	plan, err := d.hasher.Plan(t.pendingHistory, r)
	if err != nil || plan.Mode != history.Extend || plan.Start != r.LatestUserIndex() || len(r.Messages[plan.Start].Content) != len(results) {
		return nil, inference.ErrRequest
	}
	suffix := r.Messages[plan.Start+1:]
	if !d.repeatedSystem(t.pendingHistory, suffix) && !rotatedStanding(t.pendingHistory, suffix) {
		return nil, inference.ErrRequest
	}
	if err := t.broker.Resolve(t.broker.Credentials().Owner, converted); err != nil {
		return nil, inference.ErrRequest
	}
	t.plan = plan
	t.inputEstimate = estimated
	t.pendingHistory = history.Snapshot{}
	t.lastIDs = nil
	d.state = Prompting
	return &round{turn: t}, nil
}

// rotatedStanding reports a result-only continuation whose trailing standing message replaced a
// one-message standing sequence with one nonempty text-only system message. The pending backend
// turn keeps the instruction it was given; the new message is recorded in the history so the next
// prompt carries it, instead of recreating the session for every rotated reminder (D123).
func rotatedStanding(pending history.Snapshot, suffix []anthropic.Message) bool {
	n := len(pending.Nodes)
	if len(suffix) != 1 || suffix[0].Role != "system" || len(suffix[0].Content) == 0 || n < 2 || pending.Nodes[n-2].Role != "system" || n >= 3 && pending.Nodes[n-3].Role == "system" {
		return false
	}
	for _, block := range suffix[0].Content {
		if block.Type != "text" {
			return false
		}
	}
	return true
}

// A repeated, complete standing instruction sequence is already present in the owned ACP prompt.
// Record the client's repeated anchors without injecting content into tool output or starting a
// second prompt. New instructions require a new ordinary turn and cannot alter a suspended call.
func (d *Driver) repeatedSystem(pending history.Snapshot, suffix []anthropic.Message) bool {
	if len(suffix) == 0 {
		return true
	}
	start := len(pending.Nodes) - 1 - len(suffix)
	if start < 0 || start > 0 && pending.Nodes[start-1].Role == "system" {
		return false
	}
	for i, message := range suffix {
		if message.Role != "system" {
			return false
		}
		for _, block := range message.Content {
			if block.Type != "text" {
				return false
			}
		}
		node, err := d.hasher.Message(message)
		if err != nil || node != pending.Nodes[start+i] {
			return false
		}
	}
	return true
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
func reusableCompatibility(r *anthropic.Request, registry *toolregistry.Registry) ([32]byte, error) {
	copy := *r
	copy.Model = ""
	copy.Effort = ""
	return compatibility(&copy, registry)
}

func processCompatibility(r *anthropic.Request, registry *toolregistry.Registry) ([32]byte, error) {
	copy := *r
	copy.Identity = anthropic.ClientIdentity{}
	copy.Extra = nil
	return reusableCompatibility(&copy, registry)
}
func assistantContent(text string, calls []relay.Use) ([]anthropic.Block, []string, error) {
	m := anthropic.Message{Role: "assistant"}
	ids := make([]string, 0, len(calls))
	if text != "" {
		m.Content = append(m.Content, anthropic.Block{Type: "text", Text: text})
	}
	for _, call := range calls {
		raw, err := json.Marshal(anthropic.ToolUse{ID: call.ID, Name: call.Name, Input: call.Input}.Block())
		if err != nil {
			return nil, nil, err
		}
		m.Content = append(m.Content, anthropic.Block{Type: "tool_use", Raw: raw})
		ids = append(ids, call.ID)
	}
	return m.Content, ids, nil
}
