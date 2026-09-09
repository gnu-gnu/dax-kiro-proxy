// Package kirofeature contains optional, version-sensitive Kiro extension adapters.
package kirofeature

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/ndjson"
)

type Capability string

const (
	Unknown     Capability = "unknown"
	Current     Capability = "current"
	Unsupported Capability = "unsupported"
	Unavailable Capability = "unavailable"
	Configured  Capability = "configured-initial"
)

type Pair struct{ Model, Effort string }
type Status struct {
	State     Capability `json:"state"`
	Model     string     `json:"model,omitempty"`
	Requested string     `json:"requested,omitempty"`
	Applied   string     `json:"applied,omitempty"`
	Rejected  bool       `json:"rejected,omitempty"`
	Reason    string     `json:"reason,omitempty"`
}
type Caller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}
type Effort struct {
	mu                    sync.Mutex
	statusMu              sync.RWMutex
	advertised, available bool
	knownUnsupported      map[Pair]bool
	rejected              map[Pair]bool
	applied               Pair
	status                Status
}

func NewEffort(unsupported []Pair) *Effort {
	c := &Effort{knownUnsupported: make(map[Pair]bool), rejected: make(map[Pair]bool), status: Status{State: Unknown}}
	for _, pair := range unsupported {
		if len(c.knownUnsupported) >= 1280 {
			break
		}
		pair.Effort = Normalize(pair.Effort)
		if len(pair.Model) > 0 && len(pair.Model) <= 256 && pair.Effort != "" {
			c.knownUnsupported[pair] = true
		}
	}
	return c
}
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "low", "medium", "high", "xhigh", "max":
		return value
	default:
		return ""
	}
}
func (c *Effort) Advertise(raw []byte) error {
	if len(raw) > 64<<10 {
		return errors.New("invalid Kiro command advertisement")
	}
	fields, err := ndjson.Object(raw)
	if err != nil {
		return errors.New("invalid Kiro command advertisement")
	}
	var commands []json.RawMessage
	items := fields["commands"]
	if len(items) == 0 || items[0] != '[' || json.Unmarshal(items, &commands) != nil || len(commands) > 128 {
		return errors.New("invalid Kiro command advertisement")
	}
	available := false
	for _, command := range commands {
		obj, err := ndjson.Object(command)
		var name string
		if err != nil || !stringField(obj["name"], &name) || len(name) > 256 {
			return errors.New("invalid Kiro command advertisement")
		}
		if name == "/effort" || name == "effort" {
			available = true
		}
	}
	c.mu.Lock()
	c.advertised = true
	c.available = available
	c.mu.Unlock()
	return nil
}
func (c *Effort) ModelChanged() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applied = Pair{}
	c.recordStatus(Status{State: Unknown})
}
func (c *Effort) ProcessChanged() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advertised = false
	c.available = false
	c.applied = Pair{}
	c.recordStatus(Status{State: Unknown})
}
func (c *Effort) Status() Status        { c.statusMu.RLock(); defer c.statusMu.RUnlock(); return c.status }
func (c *Effort) recordStatus(s Status) { c.statusMu.Lock(); c.status = s; c.statusMu.Unlock() }
func (c *Effort) Sync(ctx context.Context, rpc Caller, session, model, requested string) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value := Normalize(requested)
	pair := Pair{model, value}
	s := Status{State: Unknown, Model: model, Requested: value}
	if c.applied.Model == model {
		s.Applied = c.applied.Effort
	}
	save := func() (Status, error) { c.recordStatus(s); return s, nil }
	if model == "" || len(model) > 256 || session == "" || len(session) > 1024 {
		return s, acp.ErrParameters
	}
	if value == "" {
		return save()
	}
	if model == "auto" {
		s.State = Unsupported
		s.Reason = "automatic model selection"
		return save()
	}
	if c.knownUnsupported[pair] {
		s.State = Unsupported
		s.Reason = "known unsupported model/effort pair"
		return save()
	}
	if !c.advertised {
		s.Reason = "command availability unknown"
		return save()
	}
	if !c.available {
		s.State = Unavailable
		s.Reason = "effort command not advertised"
		return save()
	}
	if c.applied == pair {
		s.State = Current
		return save()
	}
	if c.rejected[pair] {
		s.Rejected = true
		s.Reason = "previous probe rejected"
		return save()
	}
	if len(c.rejected) >= 1280 {
		s.Reason = "capability probe budget reached"
		return save()
	}
	raw, err := rpc.Call(ctx, "_kiro.dev/commands/execute", struct {
		Session string `json:"sessionId"`
		Command struct {
			Name string `json:"command"`
			Args struct {
				Value string `json:"value"`
			} `json:"args"`
		} `json:"command"`
	}{Session: session, Command: struct {
		Name string `json:"command"`
		Args struct {
			Value string `json:"value"`
		} `json:"args"`
	}{"effort", struct {
		Value string `json:"value"`
	}{value}}})
	if err != nil {
		var remote *acp.RemoteError
		if !errors.As(err, &remote) {
			return s, err
		}
		s.Rejected = true
		s.Reason = "effort command rejected"
		c.rejected[pair] = true
		return save()
	}
	fields, err := ndjson.Object(raw)
	if err != nil || string(fields["success"]) != "true" {
		s.Rejected = true
		s.Reason = "effort result did not confirm success"
		c.rejected[pair] = true
		return save()
	}
	c.applied = pair
	s.State = Current
	s.Applied = value
	return save()
}
func stringField(raw json.RawMessage, out *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, out) == nil
}
