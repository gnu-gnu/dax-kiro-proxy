// Package schemacheck isolates untrusted schema compilation/validation behind process deadlines.
package schemacheck

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/schemawire"
)

var ErrSchema = errors.New("tool schema is invalid or unsupported")
var ErrArguments = errors.New("tool arguments do not satisfy input_schema")
var ErrBudget = errors.New("schema worker exceeded its resource deadline")
var ErrWorker = errors.New("schema worker unavailable")
var ErrOverloaded = errors.New("schema validation capacity reached")
var ErrClosed = errors.New("schema validation pool closed")

type Config struct {
	Executable, Directory            string
	Args                             []string
	MaxWorkers                       int
	Timeout, StartupTimeout, IdleTTL time.Duration
	// QueueTimeout bounds how long a caller waits for a worker slot when every worker is busy;
	// only after it elapses is the call refused with ErrOverloaded.
	QueueTimeout time.Duration
}
type idleWorker struct {
	client *acp.Client
	since  time.Time
}
type Stats struct {
	Active, Idle int
	Started      uint64
}
type Pool struct {
	cfg       Config
	slots     chan struct{}
	mu        sync.Mutex
	idle      []idleWorker
	active    int
	started   uint64
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	jobs      sync.WaitGroup
	closeOnce sync.Once
}

func New(cfg Config) (*Pool, error) {
	if cfg.MaxWorkers == 0 {
		cfg.MaxWorkers = 2
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 500 * time.Millisecond
	}
	if cfg.StartupTimeout == 0 {
		cfg.StartupTimeout = 5 * time.Second
	}
	if cfg.IdleTTL == 0 {
		cfg.IdleTTL = 5 * time.Minute
	}
	if cfg.QueueTimeout == 0 {
		cfg.QueueTimeout = 2 * time.Second
	}
	if cfg.QueueTimeout <= 0 || cfg.QueueTimeout > 30*time.Second {
		return nil, ErrWorker
	}
	if !filepath.IsAbs(cfg.Executable) || !filepath.IsAbs(cfg.Directory) || cfg.MaxWorkers < 1 || cfg.MaxWorkers > 8 || cfg.Timeout <= 0 || cfg.Timeout > 5*time.Second || cfg.StartupTimeout <= 0 || cfg.StartupTimeout > 10*time.Second || cfg.IdleTTL <= 0 || cfg.IdleTTL > time.Hour {
		return nil, ErrWorker
	}
	if cfg.Args == nil {
		cfg.Args = []string{"schema-worker"}
	} else {
		cfg.Args = append([]string{}, cfg.Args...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{cfg: cfg, slots: make(chan struct{}, cfg.MaxWorkers), ctx: ctx, cancel: cancel}, nil
}
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{p.active, len(p.idle), p.started}
}
func (p *Pool) Check(ctx context.Context, schema []byte) error {
	return p.run(ctx, "schema/check", schema, nil)
}
func (p *Pool) Validate(ctx context.Context, schema, args []byte) error {
	return p.run(ctx, "schema/validate", schema, args)
}
func (p *Pool) run(ctx context.Context, method string, schema, args []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := schemawire.Schema(schema); err != nil {
		return ErrSchema
	}
	if method == "schema/validate" {
		if _, err := schemawire.Arguments(args); err != nil {
			return ErrArguments
		}
	}
	// Wait a bounded time for a worker slot: checks take milliseconds, so concurrent requests queue
	// instead of failing the moment both workers are busy. Closing the pool releases waiters.
	queue := time.NewTimer(p.cfg.QueueTimeout)
	defer queue.Stop()
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return ErrClosed
	case <-queue.C:
		return ErrOverloaded
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.slots
		return ErrClosed
	}
	p.active++
	p.jobs.Add(1)
	var client *acp.Client
	var expired bool
	if len(p.idle) > 0 {
		last := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		client = last.client
		expired = time.Since(last.since) >= p.cfg.IdleTTL
	}
	p.mu.Unlock()
	defer func() {
		if client != nil && client.Err() != nil {
			_ = client.Close()
			client = nil
		}
		p.mu.Lock()
		p.active--
		<-p.slots
		if client != nil && !p.closed {
			p.idle = append(p.idle, idleWorker{client, time.Now()})
			client = nil
		}
		p.mu.Unlock()
		if client != nil {
			_ = client.Close()
		}
		p.jobs.Done()
	}()
	job, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.ctx, cancel)
	defer func() { stop(); cancel() }()
	if expired || client != nil && client.Err() != nil {
		_ = client.Close()
		client = nil
	}
	if client == nil {
		startup, end := context.WithTimeout(job, p.cfg.StartupTimeout)
		var err error
		client, err = acp.Start(startup, acp.Config{Executable: p.cfg.Executable, Args: p.cfg.Args, Directory: p.cfg.Directory, Environment: []string{"GOMEMLIMIT=64MiB", "GOMAXPROCS=1"}, ClientInfo: acp.Info{Name: "dax-schema-parent", Version: "1"}, Limits: acp.Limits{RequestTimeout: max(p.cfg.StartupTimeout, p.cfg.Timeout), Pending: 1, EventQueue: 1, EventBytes: 4096, WriteQueue: 1, WriteBytes: 4 << 20, FrameBytes: 4 << 20, GracePeriod: 50 * time.Millisecond, TermPeriod: 100 * time.Millisecond, KillPeriod: time.Second}})
		end()
		if err != nil {
			return classify(err, ctx, job)
		}
		p.mu.Lock()
		p.started++
		p.mu.Unlock()
	}
	work, end := context.WithTimeout(job, p.cfg.Timeout)
	defer end()
	raw, err := client.Call(work, method, schemawire.Request{Schema: schema, Arguments: args})
	if err != nil {
		return classify(err, ctx, job)
	}
	response, decodeErr := ndjson.Object(raw)
	if decodeErr != nil || string(response["valid"]) != "true" {
		_ = client.Close()
		return ErrWorker
	}
	return nil
}
func classify(err error, caller, job context.Context) error {
	if caller.Err() != nil {
		return caller.Err()
	}
	if errors.Is(err, acp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return ErrBudget
	}
	if job.Err() != nil {
		return ErrClosed
	}
	var remote *acp.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case schemawire.SchemaCode:
			return ErrSchema
		case schemawire.ArgumentCode:
			return ErrArguments
		}
	}
	return ErrWorker
}
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.cancel()
		idle := p.idle
		p.idle = nil
		p.mu.Unlock()
		for _, entry := range idle {
			_ = entry.client.Close()
		}
		p.jobs.Wait()
	})
}
