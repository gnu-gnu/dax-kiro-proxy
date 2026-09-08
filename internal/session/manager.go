package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/sessionstore"
	"dax-kiro-proxy/internal/status"
)

type ManagerConfig struct {
	Session        Config
	ProfileScope   string
	Instance       string
	MaxSessions    int
	IdleTTL        time.Duration
	Persistence    *sessionstore.Store
	BackendVersion string
	Metrics        *status.TurnQueue
}
type Manager struct {
	cfg       ManagerConfig
	mu        sync.Mutex
	entries   map[string]*binding
	hasher    *history.Hasher
	pool      *acppool.Pool
	ownsPool  bool
	discovery *Driver
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	starts    sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}
type binding struct {
	driver  *Driver
	gate    chan struct{}
	users   int
	touched time.Time
	family  requestfamily.Kind
}

func NewManager(cfg ManagerConfig) (*Manager, error) {
	// A manager derives per-binding scopes; callers must not supply a shared raw driver scope.
	if cfg.Session.Metrics != nil || cfg.Session.MetricsScope != "" {
		return nil, acp.ErrParameters
	}
	// Restoring persisted launch-bound relay/profile data requires separate interoperability proof.
	if cfg.Session.PrepareLaunch != nil && cfg.Persistence != nil {
		return nil, acp.ErrParameters
	}
	if cfg.ProfileScope == "" || len(cfg.ProfileScope) > 256 || len(cfg.Instance) > 128 {
		return nil, acp.ErrParameters
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = 8
	}
	if cfg.IdleTTL == 0 {
		cfg.IdleTTL = time.Hour
	}
	if cfg.MaxSessions < 1 || cfg.MaxSessions > 128 || cfg.IdleTTL <= 0 || cfg.IdleTTL > 24*time.Hour {
		return nil, acp.ErrParameters
	}
	if cfg.Persistence != nil {
		if cfg.BackendVersion == "" || len(cfg.BackendVersion) > 128 {
			return nil, acp.ErrParameters
		}
		cfg.Session.HistoryKey = cfg.Persistence.Key()
	}
	if cfg.Instance == "" {
		var entropy [16]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return nil, err
		}
		cfg.Instance = hex.EncodeToString(entropy[:])
	}
	validated, err := normalizeConfig(cfg.Session)
	if err != nil {
		return nil, err
	}
	cfg.Session = validated
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, hasher: history.New(cfg.Session.HistoryKey), entries: make(map[string]*binding), ctx: ctx, cancel: cancel, pool: cfg.Session.Pool}
	if m.pool == nil {
		m.pool, err = acppool.New(acppool.Config{Process: cfg.Session.Process, SetupTimeout: cfg.Session.SetupTimeout})
		if err != nil {
			cancel()
			return nil, err
		}
		m.ownsPool = true
	}
	m.cfg.Session.Pool = m.pool
	if m.cfg.Session.PoolScope == "" {
		scope, _ := m.hasher.Digest("profile", cfg.ProfileScope)
		m.cfg.Session.PoolScope = scope
	}
	m.discovery, err = New(m.cfg.Session)
	if err != nil {
		cancel()
		if m.ownsPool {
			_ = m.pool.Close()
		}
		return nil, err
	}
	return m, nil
}
func (m *Manager) key(r *anthropic.Request, kind requestfamily.Kind) (string, error) {
	for _, id := range []string{r.Identity.Session, r.Identity.Agent, r.Identity.ParentAgent} {
		if len(id) > 128 {
			return "", inference.ErrRequest
		}
		for _, c := range []byte(id) {
			if c < 0x21 || c > 0x7e || c == ',' {
				return "", inference.ErrRequest
			}
		}
	}
	group := requestfamily.Group(kind)
	if r.Identity.Session != "" && kind != requestfamily.Title {
		return m.hasher.Digest("explicit-binding", struct {
			Profile, Group string
			Identity       anthropic.ClientIdentity
		}{m.cfg.ProfileScope, group, r.Identity})
	}
	var first *anthropic.Message
	for i := range r.Messages {
		if r.Messages[i].Role == "user" {
			first = &r.Messages[i]
			break
		}
	}
	if first == nil {
		return "", inference.ErrRequest
	}
	anchor, err := m.hasher.Message(*first)
	if err != nil {
		return "", err
	}
	system, err := m.hasher.Message(anthropic.Message{Role: "system", Content: r.System})
	if err != nil {
		return "", err
	}
	return m.hasher.Digest("fallback-binding", struct {
		Profile, Instance, Group string
		Identity                 anthropic.ClientIdentity
		Anchor, System           history.Node
	}{m.cfg.ProfileScope, m.cfg.Instance, group, r.Identity, anchor, system})
}
func idleBinding(e *binding) bool {
	if e.users != 0 || e.driver == nil {
		return false
	}
	state := e.driver.State()
	return state == Idle || state == Unstarted || state == Closed
}
func (m *Manager) pruneLocked() []*Driver {
	var expired []*Driver
	now := time.Now()
	for key, e := range m.entries {
		if idleBinding(e) && now.Sub(e.touched) >= m.cfg.IdleTTL {
			delete(m.entries, key)
			expired = append(expired, e.driver)
		}
	}
	return expired
}
func (m *Manager) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r == nil {
		return nil, inference.ErrRequest
	}
	kind := requestfamily.Classify(r)
	key, err := m.key(r, kind)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, acp.ErrClosed
	}
	m.starts.Add(1)
	defer m.starts.Done()
	expired := m.pruneLocked()
	e := m.entries[key]
	if e == nil && len(m.entries) >= m.cfg.MaxSessions {
		var candidate string
		var oldest time.Time
		for k, value := range m.entries {
			if idleBinding(value) && (candidate == "" || value.touched.Before(oldest)) {
				candidate = k
				oldest = value.touched
			}
		}
		if candidate != "" {
			expired = append(expired, m.entries[candidate].driver)
			delete(m.entries, candidate)
		}
	}
	if e == nil && len(m.entries) < m.cfg.MaxSessions {
		e = &binding{gate: make(chan struct{}, 1), touched: time.Now(), family: kind}
		m.entries[key] = e
	}
	if e != nil {
		e.users++
	}
	m.mu.Unlock()
	for _, d := range expired {
		_ = d.CloseIdle()
	}
	if e == nil {
		return nil, acp.ErrOverloaded
	}
	defer func() { m.mu.Lock(); e.users--; m.mu.Unlock() }()
	select {
	case e.gate <- struct{}{}:
		defer func() { <-e.gate }()
	default:
		return nil, ErrBusy
	}
	owned, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
	if e.driver == nil {
		config := m.cfg.Session
		if kind != requestfamily.Title && r.Identity.Agent == "" && r.Identity.ParentAgent == "" {
			config.Metrics = m.cfg.Metrics
			config.MetricsScope = key
		}
		// Background title work is isolated even when a client sends identical system/tool policy.
		config.PoolScope, _ = m.hasher.Digest("process-family", []string{config.PoolScope, requestfamily.Group(kind)})
		if m.cfg.Persistence != nil && r.Identity.Session != "" && kind != requestfamily.Title {
			lease, err := m.cfg.Persistence.Claim(key)
			if errors.Is(err, privatefs.ErrLocked) {
				return nil, ErrBusy
			}
			if err != nil {
				return nil, err
			}
			config.Persistence = lease
			config.PersistenceTTL = m.cfg.IdleTTL
			config.PersistenceProfile, _ = m.hasher.Digest("profile", m.cfg.ProfileScope)
			config.PersistenceLaunch, _ = m.hasher.Digest("launch", struct {
				Executable, Directory, Version, InitialModel, InitialEffort, Policy string
				Args, Environment                                                   []string
			}{config.Process.Executable, config.Process.Directory, m.cfg.BackendVersion, config.InitialModel, config.InitialEffort, config.PoolScope, config.Process.Args, config.Process.Environment})
		}
		d, err := New(config)
		if err != nil {
			if config.Persistence != nil {
				_ = config.Persistence.Close()
			}
			return nil, err
		}
		m.mu.Lock()
		e.driver = d
		m.mu.Unlock()
	}
	turn, err := e.driver.Start(owned, r)
	if err != nil {
		m.touch(e)
		return nil, err
	}
	return &managedTurn{inner: turn, manager: m, binding: e}, nil
}
func (m *Manager) touch(e *binding) { m.mu.Lock(); e.touched = time.Now(); m.mu.Unlock() }
func (m *Manager) Models(ctx context.Context) ([]inference.Model, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, acp.ErrClosed
	}
	m.starts.Add(1)
	m.mu.Unlock()
	defer m.starts.Done()
	owned, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
	return m.discovery.Models(owned)
}
func (m *Manager) Prune() {
	m.mu.Lock()
	expired := m.pruneLocked()
	m.mu.Unlock()
	for _, d := range expired {
		_ = d.CloseIdle()
	}
	m.pool.Prune()
}
func (m *Manager) Stats() acppool.Stats { return m.pool.Stats() }
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		m.mu.Unlock()
		m.starts.Wait()
		m.mu.Lock()
		drivers := make([]*Driver, 0, len(m.entries))
		for _, e := range m.entries {
			if e.driver != nil {
				drivers = append(drivers, e.driver)
			}
		}
		m.entries = make(map[string]*binding)
		m.mu.Unlock()
		for _, d := range drivers {
			m.closeErr = errors.Join(m.closeErr, d.Close())
		}
		m.closeErr = errors.Join(m.closeErr, m.discovery.Close())
		if m.ownsPool {
			m.closeErr = errors.Join(m.closeErr, m.pool.Close())
		}
	})
	return m.closeErr
}

type managedTurn struct {
	inner   inference.Turn
	manager *Manager
	binding *binding
	once    sync.Once
}

func (t *managedTurn) Model() string                                     { return t.inner.Model() }
func (t *managedTurn) Next(ctx context.Context) (inference.Event, error) { return t.inner.Next(ctx) }
func (t *managedTurn) Finish()                                           { t.once.Do(func() { t.inner.Finish(); t.manager.touch(t.binding) }) }
func (t *managedTurn) Cancel()                                           { t.once.Do(func() { t.inner.Cancel(); t.manager.touch(t.binding) }) }
