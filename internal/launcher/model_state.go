package launcher

import (
	"context"
	"errors"
	"sync"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/requestfamily"
)

var ErrModels = errors.New("launcher model catalog or preference is unavailable")

type ModelSource string

const (
	ModelConfigured ModelSource = "configured"
	ModelLastUsed   ModelSource = "last-used"
	ModelDefault    ModelSource = "kiro-default"
)

type ModelConfig struct {
	Cache       catalog.CacheConfig
	Discover    catalog.Discover
	Interactive bool
}
type ModelSelection struct {
	Backend, Client string
	Source          ModelSource
	Stale           bool
	LoadTime        time.Duration
}
type ModelState struct {
	cfg        ModelConfig
	cache      *catalog.Cache
	selection  ModelSelection
	mu         sync.Mutex
	closed     bool
	saveFailed bool
	closeOnce  sync.Once
}

// PrepareModels loads a compatible catalog and chooses an exact startup alias before HTTP/client
// launch. The caller supplies a complete stable identity and bounded read-only discovery; this stage
// does not establish account or execution-policy validity. Close, or transfer ownership to RunClient.
func PrepareModels(ctx context.Context, cfg ModelConfig) (*ModelState, error) {
	started := time.Now()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	cache, err := catalog.NewCache(cfg.Cache)
	if err != nil {
		return nil, ErrModels
	}
	keep := false
	defer func() {
		if !keep {
			cache.Close()
		}
	}()
	snapshot, err := cache.Get(ctx, cfg.Discover)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrModels
	}
	selection := ModelSelection{Backend: cfg.Cache.Identity.InitialModel, Source: ModelConfigured, Stale: snapshot.Stale}
	if selection.Backend == "" {
		if cfg.Interactive {
			last, found, err := catalog.LoadLastModel(cfg.Cache.Directory, cfg.Cache.Identity, snapshot.Catalog)
			if err != nil {
				return nil, ErrModels
			}
			if found {
				selection.Backend, selection.Source = last, ModelLastUsed
			}
		}
		if selection.Backend == "" {
			selection.Backend, selection.Source = snapshot.Catalog.Current(), ModelDefault
		}
	}
	selection.Client, err = snapshot.Catalog.ClientID(selection.Backend)
	if err != nil {
		return nil, catalog.ErrModel
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	selection.LoadTime = time.Since(started)
	keep = true
	return &ModelState{cfg: cfg, cache: cache, selection: selection}, nil
}

func (m *ModelState) Selection() ModelSelection { return m.selection }
func (m *ModelState) SaveFailed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveFailed
}
func (m *ModelState) ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && m.cache != nil && m.selection.Client != ""
}
func (m *ModelState) Models(ctx context.Context) ([]inference.Model, error) {
	if !m.ready() {
		return nil, ErrModels
	}
	snapshot, err := m.cache.Get(ctx, m.cfg.Discover)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrModels
	}
	return snapshot.Catalog.List(), nil
}
func (m *ModelState) recordUsed(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.cfg.Interactive {
		return
	}
	// No new discovery or model call belongs on the response-delivery path. The cache already holds
	// compatible data. If it cannot identify the actual backend model, do not persist a guess.
	snapshot, err := m.cache.Get(context.Background(), nil)
	if err == nil {
		var model catalog.Backend
		model, err = snapshot.Catalog.Resolve(clientID)
		if err == nil {
			err = catalog.SaveLastModel(m.cfg.Cache.Directory, m.cfg.Cache.Identity, model.ID, true)
		}
	}
	if err != nil {
		m.saveFailed = true
	}
}
func (m *ModelState) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		if m.cache != nil {
			m.cache.Close()
		}
	})
}

// The runtime owns inner independently; this wrapper owns no additional inference resources.
type catalogBackend struct {
	inner  inference.Backend
	models *ModelState
}

func (b *catalogBackend) Models(ctx context.Context) ([]inference.Model, error) {
	return b.models.Models(ctx)
}
func (b *catalogBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if r == nil {
		return nil, inference.ErrRequest
	}
	if !b.models.ready() {
		return nil, ErrModels
	}
	kind := requestfamily.Classify(r)
	foreground := r.Identity.Agent == "" && r.Identity.ParentAgent == "" && (kind == requestfamily.Main || kind == requestfamily.ToolFollowup || kind == requestfamily.Resume || kind == requestfamily.Retry)
	turn, err := b.inner.Start(ctx, r)
	if err != nil || turn == nil {
		return turn, err
	}
	return &modelTurn{inner: turn, models: b.models, foreground: foreground}, nil
}

type modelTurn struct {
	inner      inference.Turn
	models     *ModelState
	foreground bool
	mu         sync.Mutex
	terminal   bool
	closed     bool
	once       sync.Once
}

func (t *modelTurn) Model() string { return t.inner.Model() }
func (t *modelTurn) Next(ctx context.Context) (inference.Event, error) {
	event, err := t.inner.Next(ctx)
	t.mu.Lock()
	if !t.closed {
		t.terminal = err == nil && event.Kind == inference.End && (event.StopReason == "end_turn" || event.StopReason == "max_tokens" || event.StopReason == "refusal")
	}
	t.mu.Unlock()
	return event, err
}
func (t *modelTurn) Finish() {
	t.once.Do(func() {
		t.mu.Lock()
		remember := !t.closed && t.foreground && t.terminal
		t.closed = true
		t.mu.Unlock()
		model := t.inner.Model()
		t.inner.Finish()
		if remember {
			t.models.recordUsed(model)
		}
	})
}
func (t *modelTurn) Cancel() {
	t.once.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		t.inner.Cancel()
	})
}
