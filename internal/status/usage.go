// Package status owns bounded local diagnostics, never provider-billed token accounting.
package status

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"
)

// UsageData is a normalized internal view, not an asserted Kiro command JSON schema. A versioned
// adapter must independently verify the non-model command and field mapping before supplying it.
type UsageData struct {
	Used      *float64 `json:"used_credits,omitempty"`
	Limit     *float64 `json:"limit_credits,omitempty"`
	Remaining *float64 `json:"remaining_credits,omitempty"`
}
type UsageSnapshot struct {
	Available  bool       `json:"available"`
	Refreshing bool       `json:"refreshing"`
	Stale      bool       `json:"stale"`
	State      string     `json:"state"`
	Data       UsageData  `json:"data"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}
type UsageConfig struct {
	// Fetch must honor cancellation and own bounded subprocess cleanup. Nil means unsupported.
	Fetch func(context.Context) (UsageData, error)
	// Close runs once after Fetch joins, including when no refresh ever started.
	Close        func() error
	TTL, Timeout time.Duration
	Now          func() time.Time
}
type UsageCache struct {
	cfg                           UsageConfig
	mu                            sync.Mutex
	data                          UsageData
	updated, next                 time.Time
	available, refreshing, closed bool
	state                         string
	ctx                           context.Context
	cancel                        context.CancelFunc
	workers                       sync.WaitGroup
	closeOnce                     sync.Once
	closeErr                      error
}

func NewUsageCache(cfg UsageConfig) (*UsageCache, error) {
	if cfg.TTL == 0 {
		cfg.TTL = time.Minute
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TTL <= 0 || cfg.TTL > time.Hour || cfg.Timeout <= 0 || cfg.Timeout > 30*time.Second {
		return nil, errors.New("invalid usage refresh limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := "unavailable"
	if cfg.Fetch == nil {
		state = "unsupported"
	}
	return &UsageCache{cfg: cfg, ctx: ctx, cancel: cancel, state: state}, nil
}

// Read never waits for Fetch. Refresh and failure backoff share one TTL-bounded admission slot.
func (c *UsageCache) Read() UsageSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.cfg.Now()
	stale := c.available && !now.Before(c.updated.Add(c.cfg.TTL))
	if !c.closed && c.cfg.Fetch != nil && !c.refreshing && (!c.available || stale) && !now.Before(c.next) {
		c.refreshing = true
		c.next = now.Add(c.cfg.TTL)
		c.workers.Add(1)
		go c.refresh()
	}
	snapshot := UsageSnapshot{Available: c.available, Refreshing: c.refreshing, Stale: stale, State: c.state, Data: cloneUsage(c.data)}
	if c.available {
		updated := c.updated
		snapshot.UpdatedAt = &updated
	}
	return snapshot
}
func (c *UsageCache) refresh() {
	defer c.workers.Done()
	ctx, stop := context.WithTimeout(c.ctx, c.cfg.Timeout)
	defer stop()
	data, err := c.cfg.Fetch(ctx)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	valid := validUsage(data)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	if c.closed {
		return
	}
	now := c.cfg.Now()
	c.next = now.Add(c.cfg.TTL)
	if err != nil || !valid {
		c.state = "refresh_failed"
		return
	}
	c.data = cloneUsage(data)
	c.updated = now
	c.available = true
	c.state = "current"
}
func (c *UsageCache) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		c.mu.Unlock()
		c.workers.Wait()
		if c.cfg.Close != nil {
			c.closeErr = c.cfg.Close()
		}
	})
	return c.closeErr
}
func validUsage(data UsageData) bool {
	any := false
	for _, n := range []*float64{data.Used, data.Limit, data.Remaining} {
		if n != nil {
			any = true
			if math.IsNaN(*n) || math.IsInf(*n, 0) || *n < 0 || *n > 1e12 {
				return false
			}
		}
	}
	return any
}
func cloneUsage(data UsageData) UsageData {
	copyAmount := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		n := *p
		return &n
	}
	return UsageData{Used: copyAmount(data.Used), Limit: copyAmount(data.Limit), Remaining: copyAmount(data.Remaining)}
}
