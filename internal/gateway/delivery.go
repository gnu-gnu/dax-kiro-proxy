package gateway

import (
	"context"
	"time"
)

// Pending terminal deliveries are bounded by model admission. Register before terminal bytes can
// reach a client, then release after Finish/Cancel has settled the owned turn and its diagnostics.
func (h *Handler) beginDelivery() func() {
	done := make(chan struct{})
	h.deliveryMu.Lock()
	if h.deliveries == nil {
		h.deliveries = make(map[chan struct{}]struct{})
	}
	h.deliveries[done] = struct{}{}
	h.deliveryMu.Unlock()
	return func() {
		h.deliveryMu.Lock()
		if _, pending := h.deliveries[done]; pending {
			delete(h.deliveries, done)
			close(done)
		}
		h.deliveryMu.Unlock()
	}
}

// Wait only for the terminal writes already registered at this observation. New model work cannot
// extend this wait. No goroutine, poll, persistent request identity or model-side action is created.
func (h *Handler) awaitDeliveries(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	h.deliveryMu.Lock()
	before := make([]chan struct{}, 0, len(h.deliveries))
	for done := range h.deliveries {
		before = append(before, done)
	}
	h.deliveryMu.Unlock()
	for _, done := range before {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}
