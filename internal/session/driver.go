// Package session owns ACP session and turn lifetimes independently of HTTP response encoding.
package session

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/projection"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/sessionstore"
	"dax-kiro-proxy/internal/toolregistry"
)

var ErrBusy = inference.ErrBusy

type State string

const (
	Unstarted    State = "unstarted"
	Starting     State = "starting"
	Idle         State = "idle"
	Prompting    State = "prompting"
	WaitingTools State = "waiting-for-tools"
	Closed       State = "closed"
)

type Config struct {
	Process            acp.Config
	TurnTimeout        time.Duration
	SetupTimeout       time.Duration
	InitialModel       string
	InitialEffort      string
	UnsupportedEfforts []kirofeature.Pair
	Validator          toolregistry.Validator
	RelayExecutable    string
	RelayLimits        relay.Limits
	HistoryKey         [32]byte
	Pool               *acppool.Pool
	PoolScope          string
	Persistence        *sessionstore.Lease
	PersistenceProfile string
	PersistenceLaunch  string
	PersistenceTTL     time.Duration
}
type Driver struct {
	cfg                 Config
	mu                  sync.Mutex
	state               State
	client              backendClient
	current             *turn
	closed              bool
	setupCancel         context.CancelFunc
	setupDone           chan struct{}
	closeOnce           sync.Once
	closeErr            error
	modelState          catalog.Session
	fresh               bool
	initialUsed         bool
	effort              *kirofeature.Effort
	broker              *relay.Broker
	socket              *relay.Socket
	registryFingerprint string
	outcome             *terminalOutcome
	hasher              *history.Hasher
	snapshot            history.Snapshot
	reuseCompat         [32]byte
	processCompat       [32]byte
	startGate           sync.Mutex
	processWatch        sync.WaitGroup
	persistenceFailed   bool
}

func New(cfg Config) (*Driver, error) {
	if cfg.TurnTimeout == 0 {
		cfg.TurnTimeout = 10 * time.Minute
	}
	if cfg.SetupTimeout == 0 {
		cfg.SetupTimeout = 30 * time.Second
	}
	if cfg.TurnTimeout <= 0 || cfg.TurnTimeout > time.Hour || cfg.SetupTimeout <= 0 || cfg.SetupTimeout > time.Minute {
		return nil, acp.ErrParameters
	}
	if len(cfg.InitialModel) > 256 || cfg.InitialEffort != "" && kirofeature.Normalize(cfg.InitialEffort) == "" || len(cfg.UnsupportedEfforts) > 1280 {
		return nil, acp.ErrParameters
	}
	if (cfg.Validator != nil) != (cfg.RelayExecutable != "") || cfg.RelayExecutable != "" && !filepath.IsAbs(cfg.RelayExecutable) {
		return nil, acp.ErrParameters
	}
	if len(cfg.PoolScope) > 128 {
		return nil, acp.ErrParameters
	}
	if cfg.Persistence != nil {
		if cfg.Pool == nil || len(cfg.PersistenceProfile) != 64 || len(cfg.PersistenceLaunch) != 64 {
			return nil, acp.ErrParameters
		}
		if cfg.PersistenceTTL == 0 {
			cfg.PersistenceTTL = time.Hour
		}
		if cfg.PersistenceTTL <= 0 || cfg.PersistenceTTL > 24*time.Hour {
			return nil, acp.ErrParameters
		}
	}
	// The ACP turn may outlive an individual HTTP tool handoff. Short setup calls use their own ctx.
	cfg.Process.Limits.RequestTimeout = max(cfg.TurnTimeout, cfg.SetupTimeout)
	if cfg.Pool != nil && !cfg.Pool.MatchesProcess(cfg.Process) {
		return nil, acp.ErrParameters
	}
	if cfg.HistoryKey == ([32]byte{}) {
		if _, err := rand.Read(cfg.HistoryKey[:]); err != nil {
			return nil, err
		}
	}
	return &Driver{cfg: cfg, state: Unstarted, effort: kirofeature.NewEffort(cfg.UnsupportedEfforts), hasher: history.New(cfg.HistoryKey)}, nil
}
func (d *Driver) State() State { d.mu.Lock(); defer d.mu.Unlock(); return d.state }

func (d *Driver) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if !d.startGate.TryLock() {
		return nil, ErrBusy
	}
	defer d.startGate.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r == nil || !r.ClientContent() {
		return nil, inference.ErrRequest
	}
	disabled, err := r.ToolPolicy()
	if err != nil {
		return nil, inference.ErrRequest
	}
	d.mu.Lock()
	busy := d.state == Starting || d.state == Prompting
	d.mu.Unlock()
	if busy {
		return nil, ErrBusy
	}
	var registry *toolregistry.Registry
	if d.cfg.Validator != nil {
		registry, err = toolregistry.Build(ctx, r.Tools, nil, d.cfg.Validator)
		if err != nil {
			return nil, errors.Join(inference.ErrRequest, err)
		}
		if disabled {
			registry, err = toolregistry.Build(ctx, nil, nil, d.cfg.Validator)
			if err != nil {
				return nil, err
			}
		}
	} else if len(r.Tools) > 0 {
		return nil, inference.ErrRequest
	}
	results, err := r.LatestToolResults()
	if err != nil {
		return nil, inference.ErrRequest
	}
	d.mu.Lock()
	waiting := d.state == WaitingTools
	d.mu.Unlock()
	if waiting || len(results) > 0 {
		return d.resume(ctx, r, registry, results)
	}
	stamp, err := reusableCompatibility(r, registry)
	if err != nil {
		return nil, inference.ErrRequest
	}
	d.mu.Lock()
	snapshot := d.snapshot
	if stamp != d.reuseCompat {
		snapshot = history.Snapshot{}
	}
	fresh := d.fresh
	d.mu.Unlock()
	plan, err := d.hasher.Plan(snapshot, r)
	if err != nil {
		return nil, inference.ErrRequest
	}
	processStamp, err := processCompatibility(r, registry)
	if err != nil {
		return nil, inference.ErrRequest
	}
	resume, err := d.resumeRecord(r, stamp)
	if err != nil {
		return nil, err
	}
	if resume != nil {
		plan, err = d.hasher.Plan(resume.History, r)
		if err != nil {
			return nil, err
		}
	}
	// A durable invalidation precedes session/load, model/effort changes, and every new prompt.
	if d.cfg.Persistence != nil {
		if err = d.cfg.Persistence.Invalidate(); err != nil {
			return nil, err
		}
	}
	p, err := d.prepare(ctx, fresh || plan.Mode == history.Extend, registry, processStamp, resume)
	if err != nil && resume != nil && ctx.Err() == nil && !errors.Is(err, acp.ErrAuthentication) && !errors.Is(err, acp.ErrOverloaded) {
		p, err = d.prepare(ctx, false, registry, processStamp, nil)
	}
	if err != nil {
		return nil, err
	}
	defer p.finishSetup()
	var prompt []projection.Text
	if plan.Mode == history.Extend && (p.reused || p.loaded) {
		prompt, err = projection.Delta(r, plan.Start)
	} else {
		plan, err = d.hasher.Plan(history.Snapshot{}, r)
		if err == nil {
			prompt, err = projection.Full(r)
		}
	}
	if err != nil {
		d.failedStart(p.client)
		return nil, err
	}
	d.mu.Lock()
	if resume != nil {
		d.initialUsed = true
	}
	first := !d.initialUsed
	d.mu.Unlock()
	var selected catalog.Backend
	if first && d.cfg.InitialModel != "" {
		selected, err = p.info.Catalog.Backend(d.cfg.InitialModel)
	} else {
		selected, err = p.info.Catalog.Resolve(r.Model)
	}
	if err != nil {
		d.failedStart(p.client)
		return nil, errors.Join(inference.ErrRequest, err)
	}
	if err = p.selectModel(selected.ID); err != nil {
		d.failedStart(p.client)
		return nil, err
	}
	requested := r.Effort
	if first && d.cfg.InitialEffort != "" {
		requested = d.cfg.InitialEffort
	}
	if err = p.drain(); err != nil {
		d.failedStart(p.client)
		return nil, err
	}
	if _, err = d.effort.Sync(p.ctx, p.client, p.info.ID, selected.ID, requested); err != nil {
		d.failedStart(p.client)
		return nil, err
	}
	client, id := p.client, p.info.ID
	modelID, err := p.info.Catalog.ClientID(selected.ID)
	if err != nil {
		d.failedStart(client)
		return nil, acp.ErrProtocol
	}
	deadline := time.Now().Add(d.cfg.TurnTimeout)
	if requestDeadline, ok := ctx.Deadline(); ok && requestDeadline.Before(deadline) {
		deadline = requestDeadline
	}
	// Preserve the first HTTP request's total deadline across successful handoffs, without inheriting
	// its cancellation when the response handler returns. Standalone setup retains its own timeout.
	owned, stop := context.WithDeadline(context.Background(), deadline)
	d.mu.Lock()
	broker, socket := d.broker, d.socket
	d.mu.Unlock()
	t := &turn{driver: d, client: client, id: id, model: modelID, owned: owned, cancelOwned: stop, done: make(chan struct{}), released: make(chan struct{}), broker: broker, socket: socket, plan: plan, reuseCompat: stamp}
	t.compat, err = compatibility(r, registry)
	if err != nil {
		stop()
		d.failedStart(client)
		return nil, inference.ErrRequest
	}
	round := &round{turn: t}
	d.mu.Lock()
	if d.closed || p.ctx.Err() != nil {
		d.mu.Unlock()
		stop()
		d.failedStart(client)
		if p.ctx.Err() != nil {
			return nil, p.ctx.Err()
		}
		return nil, acp.ErrClosed
	}
	d.client = client
	d.current = t
	d.state = Prompting
	d.fresh = false
	d.mu.Unlock()
	go func() {
		t.result, t.resultErr = client.Call(owned, "session/prompt", struct {
			Session string            `json:"sessionId"`
			Prompt  []projection.Text `json:"prompt"`
		}{id, prompt})
		close(t.done)
		if t.resultErr != nil {
			t.abort(t.resultErr)
		}
	}()
	go t.watch()
	return round, nil
}

func (d *Driver) failedStart(client backendClient) {
	d.mu.Lock()
	socket, broker := d.socket, d.broker
	d.socket = nil
	d.broker = nil
	d.mu.Unlock()
	if socket != nil {
		socket.Close()
	} else if broker != nil {
		broker.Close()
	}
	if client != nil {
		_ = client.Close()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.client = nil
	d.current = nil
	d.snapshot = history.Snapshot{}
	if !d.closed {
		d.state = Unstarted
	}
}

func (d *Driver) Models(ctx context.Context) ([]inference.Model, error) {
	d.mu.Lock()
	data := d.modelState.Catalog
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return nil, acp.ErrClosed
	}
	if data == nil {
		var err error
		data, err = d.RefreshModels(ctx)
		if err != nil {
			return nil, err
		}
	}
	return data.List(), nil
}

func (d *Driver) RefreshModels(ctx context.Context) (*catalog.Catalog, error) {
	p, err := d.prepare(ctx, false, nil, [32]byte{}, nil)
	if err != nil {
		return nil, err
	}
	defer p.finishSetup()
	d.mu.Lock()
	if d.closed || p.ctx.Err() != nil {
		d.mu.Unlock()
		d.failedStart(p.client)
		return nil, acp.ErrClosed
	}
	d.state = Idle
	d.fresh = true
	d.mu.Unlock()
	if lease, ok := p.client.(*acppool.Lease); ok {
		_ = lease.SetIdle(true)
	}
	return p.info.Catalog, nil
}
func (d *Driver) EffortStatus() kirofeature.Status { return d.effort.Status() }

func (d *Driver) Close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.state = Closed
		if d.setupCancel != nil {
			d.setupCancel()
		}
		setup := d.setupDone
		current, client := d.current, d.client
		socket, broker := d.socket, d.broker
		d.mu.Unlock()
		if current != nil {
			current.abort(context.Canceled)
		}
		// Finish may have won its single-flight race against Cancel; Close still owns the process.
		if client != nil {
			d.closeErr = client.Close()
		}
		if socket != nil {
			socket.Close()
		} else if broker != nil {
			broker.Close()
		}
		if setup != nil {
			<-setup
		}
		d.processWatch.Wait()
		if d.cfg.Persistence != nil {
			d.closeErr = errors.Join(d.closeErr, d.cfg.Persistence.Close())
		}
	})
	return d.closeErr
}

func strictString(raw json.RawMessage, out *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, out) == nil
}
