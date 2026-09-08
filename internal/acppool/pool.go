// Package acppool routes independent sessions through bounded compatible ACP processes.
package acppool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/ndjson"
)

var ErrCleanup = errors.New("ACP pool cleanup failed")

type Config struct {
	Process                                                           acp.Config
	MaxProcesses, SessionsPerProcess, MaxIdle, EventQueue, EventBytes int
	IdleTTL, SetupTimeout                                             time.Duration
}
type Stats struct{ Processes, Sessions, Busy, Idle int }

// PreparedProcess transfers one process's launch artifacts to the pool. Preparation must honor its
// setup context, and Cleanup must finish within its own finite bound without relying on that context.
// The pool calls Cleanup once even when preparation or process startup fails, after any child group
// and router have joined. A failed preparation must return its partial ownership along with the error.
type PreparedProcess struct {
	Config  acp.Config
	Cleanup func() error
}

type PrepareProcess func(context.Context) (PreparedProcess, error)

type Pool struct {
	cfg        Config
	mu         sync.Mutex
	groups     map[*group]struct{}
	closed     bool
	closeOnce  sync.Once
	closeErr   error
	cleanupErr error
}
type group struct {
	pool                  *Pool
	scope                 string
	client                *acp.Client
	ready, failed, closed chan struct{}
	routerDone            chan struct{}
	cancel                context.CancelFunc
	err, closeErr         error
	leases                map[*Lease]struct{}
	routes                map[string]*Lease
	allocated, busy       int
	idleAt                time.Time
	creating              *Lease
	createGate            chan struct{}
	barriers              chan chan struct{}
	dedicated             bool
	cleanup               func() error
}
type Lease struct {
	g             *group
	id, candidate string
	loading, idle bool
	err           error
	queue         []acp.Notification
	bytes         int
	activity      chan struct{}
	done          chan struct{}
	ended         bool
}

// Ordinary acquisitions use the pool's immutable configuration. Explicit prepared acquisitions
// reserve a separate process and never share either an ordinary or another prepared launch.
func (p *Pool) MatchesProcess(cfg acp.Config) bool { return reflect.DeepEqual(p.cfg.Process, cfg) }

func New(cfg Config) (*Pool, error) {
	for _, p := range []struct {
		v        *int
		def, max int
	}{{&cfg.MaxProcesses, 4, 16}, {&cfg.SessionsPerProcess, 2, 8}, {&cfg.MaxIdle, 2, 16}, {&cfg.EventQueue, 64, 1024}, {&cfg.EventBytes, 16 << 20, 64 << 20}} {
		if *p.v == 0 {
			*p.v = p.def
		}
		if *p.v < 1 || *p.v > p.max {
			return nil, acp.ErrParameters
		}
	}
	if cfg.IdleTTL == 0 {
		cfg.IdleTTL = 5 * time.Minute
	}
	if cfg.SetupTimeout == 0 {
		cfg.SetupTimeout = 30 * time.Second
	}
	if cfg.IdleTTL <= 0 || cfg.IdleTTL > time.Hour || cfg.SetupTimeout <= 0 || cfg.SetupTimeout > time.Minute {
		return nil, acp.ErrParameters
	}
	cfg.MaxIdle = min(cfg.MaxIdle, cfg.MaxProcesses)
	cfg.Process.Args = slices.Clone(cfg.Process.Args)
	cfg.Process.Environment = slices.Clone(cfg.Process.Environment)
	return &Pool{cfg: cfg, groups: make(map[*group]struct{})}, nil
}
func (p *Pool) Acquire(ctx context.Context, scope string) (*Lease, error) {
	return p.acquire(ctx, scope, true, nil)
}

// AcquirePrepared reserves capacity before calling prepare. Its first session is its only lifetime
// allocation, regardless of SessionsPerProcess; keeping that session idle still permits turn reuse.
func (p *Pool) AcquirePrepared(ctx context.Context, scope string, prepare PrepareProcess) (*Lease, error) {
	if prepare == nil {
		return nil, acp.ErrParameters
	}
	return p.acquire(ctx, scope, true, prepare)
}

func (p *Pool) acquire(ctx context.Context, scope string, recycle bool, prepare PrepareProcess) (*Lease, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if scope == "" || len(scope) > 256 {
		return nil, acp.ErrParameters
	}
	p.mu.Lock()
	p.pruneLocked()
	if p.cleanupErr != nil {
		err := p.cleanupErr
		p.mu.Unlock()
		return nil, err
	}
	if p.closed {
		p.mu.Unlock()
		return nil, acp.ErrClosed
	}
	var g *group
	for candidate := range p.groups {
		if prepare == nil && !candidate.dedicated && candidate.scope == scope && candidate.client != nil && candidate.err == nil && candidate.client.Err() == nil && candidate.allocated < p.cfg.SessionsPerProcess {
			g = candidate
			break
		}
	}
	if g == nil {
		if len(p.groups) >= p.cfg.MaxProcesses {
			var oldest *group
			if recycle {
				for candidate := range p.groups {
					if candidate.err == nil && candidate.busy == 0 && (oldest == nil || candidate.idleAt.Before(oldest.idleAt)) {
						oldest = candidate
					}
				}
			}
			if oldest != nil {
				oldest.retireLocked(acp.ErrClosed)
				p.mu.Unlock()
				select {
				case <-oldest.closed:
					return p.acquire(ctx, scope, false, prepare)
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			p.mu.Unlock()
			return nil, acp.ErrOverloaded
		}
		setup, cancel := context.WithTimeout(ctx, p.cfg.SetupTimeout)
		g = &group{pool: p, scope: scope, ready: make(chan struct{}), failed: make(chan struct{}), closed: make(chan struct{}), routerDone: make(chan struct{}), cancel: cancel, leases: make(map[*Lease]struct{}), routes: make(map[string]*Lease), createGate: make(chan struct{}, 1), barriers: make(chan chan struct{}, p.cfg.SessionsPerProcess)}
		g.dedicated = prepare != nil
		p.groups[g] = struct{}{}
		l := g.newLeaseLocked()
		p.mu.Unlock()
		launch := PreparedProcess{Config: p.cfg.Process}
		err := setup.Err()
		if err == nil && prepare != nil {
			launch, err = prepare(setup)
		}
		var client *acp.Client
		if err == nil {
			client, err = acp.Start(setup, launch.Config)
		}
		cancel()
		p.mu.Lock()
		g.client = client
		g.cleanup = launch.Cleanup
		close(g.ready)
		if err != nil {
			g.retireLocked(err)
		}
		retired := g.err
		if retired == nil {
			go g.route()
		} else {
			close(g.routerDone)
		}
		p.mu.Unlock()
		if retired != nil {
			<-g.closed
			return nil, errors.Join(retired, g.closeErr)
		}
		return l, nil
	}
	l := g.newLeaseLocked()
	p.mu.Unlock()
	return l, nil
}
func (g *group) newLeaseLocked() *Lease {
	l := &Lease{g: g, activity: make(chan struct{}, 1), done: make(chan struct{})}
	g.leases[l] = struct{}{}
	g.allocated++
	g.busy++
	g.idleAt = time.Time{}
	return l
}
func (l *Lease) signal() {
	select {
	case l.activity <- struct{}{}:
	default:
	}
}
func (l *Lease) ID() string                     { p := l.g.pool; p.mu.Lock(); defer p.mu.Unlock(); return l.id }
func (l *Lease) PID() int                       { return l.g.client.PID() }
func (l *Lease) Capabilities() acp.Capabilities { return l.g.client.Capabilities() }
func (l *Lease) Activity() <-chan struct{}      { return l.activity }
func (l *Lease) Done() <-chan struct{}          { return l.done }
func (l *Lease) endLocked() {
	if !l.ended {
		l.ended = true
		close(l.done)
	}
}
func (l *Lease) Err() error { p := l.g.pool; p.mu.Lock(); defer p.mu.Unlock(); return l.errLocked() }
func (l *Lease) errLocked() error {
	if l.err != nil {
		return l.err
	}
	if l.g.err != nil {
		return l.g.err
	}
	return l.g.client.Err()
}
func (l *Lease) TryNext() (acp.Notification, bool, error) {
	p := l.g.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := l.errLocked(); err != nil {
		return acp.Notification{}, false, err
	}
	if len(l.queue) == 0 {
		return acp.Notification{}, false, nil
	}
	n := l.queue[0]
	l.queue[0] = acp.Notification{}
	l.queue = l.queue[1:]
	l.bytes -= eventBytes(n)
	return n, true, nil
}
func eventBytes(n acp.Notification) int { return len(n.Method) + len(n.SessionID) + len(n.Params) }
func (g *group) retireLocked(reason error) {
	if g.err != nil {
		return
	}
	if reason == nil {
		reason = acp.ErrClosed
	}
	g.err = reason
	g.cancel()
	close(g.failed)
	for l := range g.leases {
		l.endLocked()
		l.queue = nil
		l.bytes = 0
		l.signal()
	}
	go func() {
		<-g.ready
		if g.client != nil {
			g.closeErr = g.client.Close()
		}
		<-g.routerDone
		if g.cleanup != nil {
			g.closeErr = errors.Join(g.closeErr, g.cleanup())
		}
		p := g.pool
		p.mu.Lock()
		// Retain only the first failure and stop further admission. Removing a retired group cannot
		// erase evidence of its unfinished cleanup or allow repeated failed artifact accumulation.
		if g.closeErr != nil && p.cleanupErr == nil {
			p.cleanupErr = errors.Join(ErrCleanup, g.closeErr)
		}
		delete(p.groups, g)
		p.mu.Unlock()
		close(g.closed)
	}()
}
func (l *Lease) Close() error {
	g := l.g
	p := g.pool
	p.mu.Lock()
	g.retireLocked(acp.ErrClosed)
	p.mu.Unlock()
	<-g.closed
	return g.closeErr
}

// ReleaseIdle disposes one idle binding without interrupting siblings. Backend allocation is never
// reused: ACP has no required session deletion, so lifetime allocation still counts against the cap.
func (l *Lease) ReleaseIdle() error {
	g := l.g
	p := g.pool
	p.mu.Lock()
	if l.err != nil {
		retired := g.err != nil
		p.mu.Unlock()
		if retired {
			<-g.closed
			return g.closeErr
		}
		return nil
	}
	if !l.idle && g.err == nil {
		p.mu.Unlock()
		return acp.ErrParameters
	}
	l.err = acp.ErrClosed
	l.endLocked()
	l.queue = nil
	l.bytes = 0
	l.signal()
	delete(g.leases, l)
	if l.id != "" {
		g.routes[l.id] = nil
	}
	if len(g.leases) == 0 && (g.dedicated || g.allocated >= p.cfg.SessionsPerProcess) {
		g.retireLocked(acp.ErrClosed)
	}
	retired := g.err != nil
	p.mu.Unlock()
	if retired {
		<-g.closed
		return g.closeErr
	}
	return nil
}
func (l *Lease) SetIdle(idle bool) error {
	g := l.g
	p := g.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := l.errLocked(); err != nil {
		return err
	}
	if l.idle == idle {
		return nil
	}
	l.idle = idle
	if idle {
		g.busy--
		if g.busy == 0 {
			g.idleAt = time.Now()
		}
	} else {
		g.busy++
		g.idleAt = time.Time{}
	}
	p.pruneLocked()
	return l.errLocked()
}
func (p *Pool) Prune() { p.mu.Lock(); p.pruneLocked(); p.mu.Unlock() }
func (p *Pool) pruneLocked() {
	var idle []*group
	now := time.Now()
	for g := range p.groups {
		if g.err == nil && g.busy == 0 && !g.idleAt.IsZero() {
			if now.Sub(g.idleAt) >= p.cfg.IdleTTL {
				g.retireLocked(acp.ErrClosed)
			} else {
				idle = append(idle, g)
			}
		}
	}
	for len(idle) > p.cfg.MaxIdle {
		oldest := 0
		for i := range idle {
			if idle[i].idleAt.Before(idle[oldest].idleAt) {
				oldest = i
			}
		}
		idle[oldest].retireLocked(acp.ErrClosed)
		idle = append(idle[:oldest], idle[oldest+1:]...)
	}
}
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := Stats{Processes: len(p.groups)}
	for g := range p.groups {
		s.Sessions += len(g.leases)
		s.Busy += g.busy
		if g.busy == 0 && g.err == nil {
			s.Idle++
		}
	}
	return s
}
func (p *Pool) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		groups := make([]*group, 0, len(p.groups))
		for g := range p.groups {
			groups = append(groups, g)
			g.retireLocked(acp.ErrClosed)
		}
		p.mu.Unlock()
		for _, g := range groups {
			<-g.closed
		}
		p.mu.Lock()
		p.closeErr = p.cleanupErr
		p.mu.Unlock()
	})
	return p.closeErr
}
func (l *Lease) barrier(ctx context.Context) error {
	g := l.g
	ack := make(chan struct{})
	select {
	case g.barriers <- ack:
	case <-g.failed:
		return l.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ack:
		return l.Err()
	case <-g.failed:
		return l.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (l *Lease) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method == "session/new" || method == "session/load" {
		return nil, acp.ErrParameters
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, acp.ErrParameters
	}
	fields, err := ndjson.Object(encoded)
	var id string
	if err != nil || json.Unmarshal(fields["sessionId"], &id) != nil || id == "" || id != l.ID() {
		return nil, acp.ErrParameters
	}
	if err = l.Err(); err != nil {
		return nil, err
	}
	raw, err := l.g.client.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if err = l.barrier(ctx); err != nil {
		p := l.g.pool
		p.mu.Lock()
		l.g.retireLocked(err)
		p.mu.Unlock()
		return nil, err
	}
	return raw, nil
}
func (l *Lease) Create(ctx context.Context, params any, loadID string) (json.RawMessage, error) {
	g := l.g
	p := g.pool
	select {
	case g.createGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.failed:
		return nil, l.Err()
	}
	defer func() { <-g.createGate }()
	p.mu.Lock()
	if err := l.errLocked(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if l.id != "" || g.creating != nil {
		p.mu.Unlock()
		return nil, acp.ErrParameters
	}
	l.loading = loadID != ""
	l.candidate = loadID
	g.creating = l
	p.mu.Unlock()
	fail := func(err error) (json.RawMessage, error) {
		p.mu.Lock()
		g.creating = nil
		g.retireLocked(err)
		p.mu.Unlock()
		return nil, err
	}
	method := "session/new"
	if loadID != "" {
		if !l.Capabilities().LoadSession {
			return fail(acp.ErrParameters)
		}
		method = "session/load"
		encoded, err := json.Marshal(params)
		if err != nil {
			return fail(acp.ErrParameters)
		}
		fields, err := ndjson.Object(encoded)
		if err != nil {
			return fail(acp.ErrParameters)
		}
		fields["sessionId"], _ = json.Marshal(loadID)
		params = fields
	}
	raw, err := g.client.Call(ctx, method, params)
	if err != nil {
		return fail(err)
	}
	if err = l.barrier(ctx); err != nil {
		return fail(err)
	}
	fields, err := ndjson.Object(raw)
	if loadID != "" && string(raw) == "null" {
		fields = map[string]json.RawMessage{}
		err = nil
	}
	if err != nil {
		return fail(acp.ErrProtocol)
	}
	id := loadID
	if value, present := fields["sessionId"]; present {
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &id) != nil {
			return fail(acp.ErrProtocol)
		}
	}
	if id == "" || len(id) > 1024 || loadID != "" && id != loadID {
		return fail(acp.ErrProtocol)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err = l.errLocked(); err != nil {
		g.creating = nil
		return nil, err
	}
	_, duplicate := g.routes[id]
	if duplicate || l.candidate != "" && l.candidate != id {
		g.creating = nil
		g.retireLocked(acp.ErrProtocol)
		return nil, acp.ErrProtocol
	}
	l.id = id
	l.loading = false
	g.routes[id] = l
	g.creating = nil
	return raw, nil
}
func (g *group) route() {
	defer close(g.routerDone)
	drain := func() bool {
		for {
			n, ok, err := g.client.TryNext()
			if err != nil {
				g.pool.mu.Lock()
				g.retireLocked(err)
				g.pool.mu.Unlock()
				return false
			}
			if !ok {
				return true
			}
			if !g.deliver(n) {
				return false
			}
		}
	}
	for {
		if !drain() {
			return
		}
		select {
		case <-g.failed:
			return
		case <-g.client.Activity():
		case ack := <-g.barriers:
			if !drain() {
				return
			}
			close(ack)
		}
	}
}
func (g *group) deliver(n acp.Notification) bool {
	p := g.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if g.err != nil {
		return false
	}
	if n.SessionID == "" {
		if n.Method == "session/update" {
			g.retireLocked(acp.ErrProtocol)
			return false
		}
		return true
	}
	l, known := g.routes[n.SessionID]
	if known && l == nil {
		return true
	}
	if !known {
		l = g.creating
		if l == nil || l.candidate != "" && l.candidate != n.SessionID {
			g.retireLocked(acp.ErrProtocol)
			return false
		}
		l.candidate = n.SessionID
	}
	if l.loading {
		return true
	}
	size := eventBytes(n)
	if len(l.queue) >= p.cfg.EventQueue || size > p.cfg.EventBytes-l.bytes {
		g.retireLocked(acp.ErrOverloaded)
		return false
	}
	l.queue = append(l.queue, n)
	l.bytes += size
	l.signal()
	return true
}
