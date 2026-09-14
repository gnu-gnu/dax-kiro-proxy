package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/projection"
)

type Peer interface {
	kirofeature.UsagePeer
	TryNext() (acp.Notification, bool, error)
	Activity() <-chan struct{}
	Close() error
}
type ACPsession struct {
	peer              Peer
	id, model, budget string
	limit             int
	prompt            []projection.Text
}

// Prepare makes no model request. The caller owns peer cleanup on every preparation error.
func Prepare(ctx context.Context, peer Peer, directory, budget string, r *anthropic.Request, spec anthropic.SearchSpec) (*ACPsession, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if peer == nil {
		return nil, inference.ErrRequest
	}
	prompt, err := ValidateRequest(r, spec)
	if err != nil {
		return nil, err
	}
	if count, err := BudgetCount(budget, spec.MaxUses); err != nil || count != 0 {
		return nil, acp.ErrProtocol
	}
	raw, err := peer.Call(ctx, "session/new", map[string]any{"cwd": directory, "mcpServers": []any{}})
	if err != nil {
		return nil, err
	}
	info, err := catalog.DecodeSession(raw)
	if err != nil || strings.ContainsAny(info.ID, "\x00\r\n") {
		return nil, acp.ErrProtocol
	}
	selected, err := info.Catalog.Resolve(r.Model)
	if err != nil {
		return nil, errors.Join(inference.ErrRequest, err)
	}
	advertised, err := kirofeature.SearchInventory(ctx, peer, info.ID)
	if err != nil {
		return nil, err
	}
	if selected.ID != info.Catalog.Current() {
		method := "session/set_model"
		params := map[string]string{"sessionId": info.ID, "modelId": selected.ID}
		if info.ConfigID != "" {
			method = "session/set_config_option"
			params = map[string]string{"sessionId": info.ID, "configId": info.ConfigID, "value": selected.ID}
		}
		raw, err = peer.Call(ctx, method, params)
		if err != nil {
			return nil, err
		}
		fields, err := ndjson.Object(raw)
		if err != nil {
			return nil, acp.ErrProtocol
		}
		if _, present := fields["models"]; present || info.ConfigID != "" {
			models, selector, err := catalog.DecodeModels(fields)
			if err != nil || selector != info.ConfigID || models.Current() != selected.ID {
				return nil, acp.ErrProtocol
			}
		}
	}
	effort := kirofeature.NewEffort(nil)
	if effort.Advertise(advertised) != nil {
		return nil, acp.ErrProtocol
	}
	if _, err := effort.Sync(ctx, peer, info.ID, selected.ID, r.Effort); err != nil {
		return nil, err
	}
	model, err := info.Catalog.ClientID(selected.ID)
	if err != nil {
		return nil, acp.ErrProtocol
	}
	return &ACPsession{peer: peer, id: info.ID, model: model, budget: budget, limit: spec.MaxUses, prompt: prompt}, nil
}
func (s *ACPsession) Model() string { return s.model }
func (s *ACPsession) Close() error  { return s.peer.Close() }
func (s *ACPsession) Run(ctx context.Context, emit func(inference.Event) bool) error {
	ctx, cancel := context.WithCancel(ctx)
	var result json.RawMessage
	var promptErr error
	done := make(chan struct{})
	go func() {
		result, promptErr = s.peer.Call(ctx, "session/prompt", map[string]any{"sessionId": s.id, "prompt": s.prompt})
		close(done)
	}()
	defer func() { cancel(); <-done }()
	c := newCollector(s.id, s.limit)
	completed := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, ok, err := s.peer.TryNext()
		if err != nil {
			return err
		}
		if ok {
			if err := c.observe(n); err != nil {
				return err
			}
			if c.progress && !emit(inference.Event{Kind: inference.Progress}) {
				return context.Canceled
			}
			continue
		}
		if completed {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.peer.Activity():
		case <-done:
			completed = true
		}
	}
	if promptErr != nil {
		return promptErr
	}
	fields, err := ndjson.Object(result)
	var stop string
	if err != nil || json.Unmarshal(fields["stopReason"], &stop) != nil || stop != "end_turn" {
		return acp.ErrProtocol
	}
	exchanges, text, err := c.finish()
	if err != nil {
		return err
	}
	count, err := BudgetCount(s.budget, s.limit)
	if err != nil || count != len(exchanges) {
		return acp.ErrProtocol
	}
	if !emit(inference.Event{Kind: inference.Search, Searches: exchanges}) {
		return context.Canceled
	}
	if text != "" && !emit(inference.Event{Kind: inference.Text, Text: text}) {
		return context.Canceled
	}
	if !emit(inference.Event{Kind: inference.End, StopReason: "end_turn"}) {
		return context.Canceled
	}
	return nil
}
