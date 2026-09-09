package session

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/projection"
	"dax-kiro-proxy/internal/toolregistry"
)

type continuationRestart struct {
	deadline, started time.Time
	count             int
}

// A changed registry, standing instruction or added client text requires a fresh ACP prompt. Validate
// the complete delivered batch and immutable prior content before revoking any old ownership.
func (d *Driver) restartForContinuation(ctx context.Context, r *anthropic.Request, registry *toolregistry.Registry, results []anthropic.ToolResult) (*continuationRestart, error) {
	i := r.LatestUserIndex()
	if i < 0 || len(results) == 0 {
		return nil, nil
	}
	additionalText := len(r.Messages[i].Content) != len(results)
	if additionalText {
		success := false
		for _, result := range results {
			success = success || !result.IsError
		}
		// All-denial/new-question recovery has its own bounded retired-outcome policy.
		if !success {
			return nil, nil
		}
		nonempty := false
		for j, block := range r.Messages[i].Content {
			if j < len(results) {
				if block.Type != "tool_result" {
					return nil, inference.ErrRequest
				}
			} else {
				if block.Type != "text" {
					return nil, inference.ErrRequest
				}
				nonempty = nonempty || strings.TrimSpace(block.Text) != ""
			}
		}
		if !nonempty {
			return nil, inference.ErrRequest
		}
	}
	suffix := r.Messages[i+1:]
	stamp, err := compatibility(r, registry)
	if err != nil {
		return nil, inference.ErrRequest
	}
	policy, err := compatibility(r, nil)
	if err != nil {
		return nil, inference.ErrRequest
	}
	d.mu.Lock()
	previous := d.current
	if d.state != WaitingTools || previous == nil {
		d.mu.Unlock()
		return nil, nil
	}
	repeated := d.repeatedSystem(previous.pendingHistory, suffix)
	if stamp == previous.compat && repeated && !additionalText {
		d.mu.Unlock()
		return nil, nil
	}
	if policy != previous.policyCompat || previous.recreations >= d.cfg.MaxRecreations {
		d.mu.Unlock()
		return nil, inference.ErrRequest
	}
	// A complete repeated sequence keeps its existing semantics; a new sequence is restricted
	// to one nonempty text-only standing message. All other changed instructions reject.
	if !repeated {
		if len(suffix) != 1 || suffix[0].Role != "system" || len(suffix[0].Content) == 0 {
			d.mu.Unlock()
			return nil, inference.ErrRequest
		}
		for _, block := range suffix[0].Content {
			if block.Type != "text" {
				d.mu.Unlock()
				return nil, inference.ErrRequest
			}
		}
	}
	pending := previous.pendingHistory
	nodes, err := d.hasher.Nodes(r.Messages[:i])
	n := len(pending.Nodes)
	if err != nil || !pending.Valid() || !slices.Equal(nodes, pending.Nodes) || (!repeated && (n < 2 || pending.Nodes[n-2].Role != "system" || (n >= 3 && pending.Nodes[n-3].Role == "system"))) || !sameResultIDs(previous.lastIDs, results) {
		d.mu.Unlock()
		return nil, inference.ErrRequest
	}
	converted, err := relayResults(results)
	if err != nil {
		d.mu.Unlock()
		return nil, inference.ErrRequest
	}
	window := &continuationRestart{started: previous.started, count: previous.recreations + 1}
	var valid bool
	window.deadline, valid = previous.owned.Deadline()
	if !valid || !time.Now().Before(window.deadline) || ctx.Err() != nil {
		d.mu.Unlock()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, context.DeadlineExceeded
	}
	// Validate the complete projection before revocation. Reserve the worst JSON escaping of the
	// maximum accepted 1024-byte session ID because the replacement ID is not known yet.
	prompt, err := projection.FullWithCapabilities(r, previous.client.Capabilities().Prompt)
	if err != nil || !d.promptFits(strings.Repeat("\x00", 1024), prompt) || previous.broker == nil {
		d.mu.Unlock()
		return nil, inference.ErrRequest
	}
	err = previous.broker.Abandon(previous.broker.Credentials().Owner, converted)
	d.mu.Unlock()
	if err != nil {
		return nil, inference.ErrRequest
	}
	previous.abort(context.Canceled)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.cleanupErr != nil {
		return nil, errors.Join(acp.ErrClosed, d.cleanupErr)
	}
	if d.outcome != nil && errors.Is(d.outcome.err, acp.ErrAuthentication) {
		d.outcome = nil
		return nil, acp.ErrAuthentication
	}
	d.outcome = nil
	return window, nil
}
