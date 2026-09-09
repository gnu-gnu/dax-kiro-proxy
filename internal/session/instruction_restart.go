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

type instructionRestart struct {
	deadline, started time.Time
	count             int
}

// A changed single standing system message cannot be injected into a suspended ACP prompt.
// For one exact full-history continuation, revoke the delivered batch and recreate with all supplied
// history. Repetition stays in the original prompt; ambiguous/truncated history remains rejected.
func (d *Driver) restartForInstruction(ctx context.Context, r *anthropic.Request, registry *toolregistry.Registry, results []anthropic.ToolResult) (*instructionRestart, error) {
	i := r.LatestUserIndex()
	if i < 0 || len(results) == 0 || len(r.Messages[i].Content) != len(results) || len(r.Messages) != i+2 {
		return nil, nil
	}
	suffix := r.Messages[i+1:]
	if suffix[0].Role != "system" || len(suffix[0].Content) == 0 {
		return nil, nil
	}
	for _, block := range suffix[0].Content {
		if block.Type != "text" {
			return nil, inference.ErrRequest
		}
	}
	stamp, err := compatibility(r, registry)
	if err != nil {
		return nil, inference.ErrRequest
	}
	converted, err := relayResults(results)
	if err != nil {
		return nil, inference.ErrRequest
	}
	nodes, err := d.hasher.Nodes(r.Messages[:i])
	if err != nil {
		return nil, inference.ErrRequest
	}
	d.mu.Lock()
	previous := d.current
	if d.state != WaitingTools || previous == nil || d.repeatedSystem(previous.pendingHistory, suffix) {
		d.mu.Unlock()
		return nil, nil
	}
	pending := previous.pendingHistory
	// Initially support a single standing message before the handoff and after the result.
	// Existing multi-message suffix rules are unchanged, including their order/partial safeguards.
	n := len(pending.Nodes)
	valid := pending.Valid() && n >= 2 && pending.Nodes[n-2].Role == "system" && (n < 3 || pending.Nodes[n-3].Role != "system") && slices.Equal(nodes, pending.Nodes) && stamp == previous.compat && sameResultIDs(previous.lastIDs, results) && previous.instructionRestarts == 0
	if !valid {
		d.mu.Unlock()
		return nil, inference.ErrRequest
	}
	window := &instructionRestart{started: previous.started, count: previous.instructionRestarts + 1}
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
