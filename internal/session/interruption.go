package session

import (
	"context"
	"errors"
	"strings"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/toolregistry"
)

// A new user question can arrive with the client's previously withheld denial results. These
// results describe abandoned work; they must not resume the suspended prompt or absorb the new
// question into tool output. Only an exact owned batch and extending history authorize recreation.
func (d *Driver) restartAfterDenial(ctx context.Context, r *anthropic.Request, registry *toolregistry.Registry, results []anthropic.ToolResult) (bool, error) {
	i := r.LatestUserIndex()
	if len(results) == 0 || i < 0 || len(r.Messages[i].Content) == len(results) {
		return false, nil
	}
	for _, result := range results {
		if !result.IsError {
			return false, inference.ErrRequest
		}
	}
	text := false
	for j, block := range r.Messages[i].Content {
		if j < len(results) {
			if block.Type != "tool_result" {
				return false, inference.ErrRequest
			}
		} else {
			if block.Type != "text" {
				return false, inference.ErrRequest
			}
			text = text || strings.TrimSpace(block.Text) != ""
		}
	}
	if !text {
		return false, inference.ErrRequest
	}
	stamp, err := compatibility(r, registry)
	if err != nil {
		return false, inference.ErrRequest
	}
	d.mu.Lock()
	var pending history.Snapshot
	var ids []string
	var owner [32]byte
	var previous *turn
	if d.state == WaitingTools && d.current != nil {
		previous = d.current
		pending, ids, owner = previous.pendingHistory, previous.lastIDs, previous.compat
	} else if d.state == Unstarted && d.outcome != nil && time.Now().Before(d.outcome.expires) {
		pending, ids, owner = d.outcome.pending, d.outcome.ids, d.outcome.compat
	}
	plan, planErr := d.hasher.Plan(pending, r)
	valid := owner == stamp && sameResultIDs(ids, results) && planErr == nil && plan.Mode == history.Extend && plan.Start == i && d.repeatedSystem(pending, r.Messages[i+1:])
	d.mu.Unlock()
	if !valid {
		return false, inference.ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if previous != nil {
		previous.abort(context.Canceled)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.cleanupErr != nil {
		return false, errors.Join(acp.ErrClosed, d.cleanupErr)
	}
	// Start's admission lock excludes another request throughout validation and joined retirement.
	d.outcome = nil
	return true, nil
}
