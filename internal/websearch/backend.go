// Package websearch owns isolated, finite native search turns outside ordinary conversation state.
package websearch

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

type OwnedBackend interface {
	inference.Backend
	Close() error
}
type Session interface {
	Model() string
	Run(context.Context, func(inference.Event) bool) error
	Close() error
}
type Open func(context.Context, *anthropic.Request, anthropic.SearchSpec) (Session, error)
type Backend struct {
	main       OwnedBackend
	open       Open
	mu         sync.Mutex
	active     map[*turn]bool
	closed     bool
	cleanupErr error
	once       sync.Once
}

func New(main OwnedBackend, open Open) (*Backend, error) {
	if main == nil || open == nil {
		return nil, inference.ErrRequest
	}
	return &Backend{main: main, open: open, active: make(map[*turn]bool)}, nil
}
func (b *Backend) Models(ctx context.Context) ([]inference.Model, error) { return b.main.Models(ctx) }
func (b *Backend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if r == nil {
		return nil, inference.ErrRequest
	}
	spec, matched, err := anthropic.SearchDeclaration(r.Tools)
	if err != nil {
		return nil, errors.Join(inference.ErrRequest, err)
	}
	if matched {
		if _, err := ValidateRequest(r, spec); err != nil {
			return nil, err
		}
	}
	b.mu.Lock()
	if b.closed || b.cleanupErr != nil {
		b.mu.Unlock()
		return nil, acp.ErrClosed
	}
	if !matched {
		b.mu.Unlock()
		return b.main.Start(ctx, r)
	}
	if len(b.active) >= 2 {
		b.mu.Unlock()
		return nil, acp.ErrOverloaded
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	t := &turn{owner: b, ctx: ctx, cancel: cancel, events: make(chan inference.Event, 8), done: make(chan struct{})}
	b.active[t] = true
	b.mu.Unlock()
	session, err := b.open(ctx, r, spec)
	if err == nil && session == nil {
		err = acp.ErrProtocol
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if session != nil {
			if closeErr := session.Close(); closeErr != nil {
				err = errors.Join(err, acp.ErrCleanup, closeErr)
			}
		}
		t.complete(err)
		t.Cancel()
		return nil, err
	}
	t.model = session.Model()
	go func() {
		var terminal *inference.Event
		err := session.Run(ctx, func(e inference.Event) bool {
			if terminal != nil {
				return false
			}
			if e.Kind == inference.End {
				terminal = &e
				return true
			}
			return t.emit(e)
		})
		if closeErr := session.Close(); closeErr != nil {
			err = errors.Join(err, acp.ErrCleanup, closeErr)
		}
		if err == nil {
			if terminal == nil {
				err = acp.ErrProtocol
			} else if !t.emit(*terminal) {
				err = ctx.Err()
			}
		}
		t.complete(err)
	}()
	return t, nil
}
func (b *Backend) Close() error {
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		active := make([]*turn, 0, len(b.active))
		for t := range b.active {
			active = append(active, t)
		}
		b.mu.Unlock()
		for _, t := range active {
			t.cancel()
		}
		for _, t := range active {
			t.Cancel()
		}
		err := b.main.Close()
		b.mu.Lock()
		b.cleanupErr = errors.Join(b.cleanupErr, err)
		b.mu.Unlock()
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cleanupErr
}

type turn struct {
	owner  *Backend
	model  string
	ctx    context.Context
	cancel context.CancelFunc
	events chan inference.Event
	done   chan struct{}
	err    error // published by done
}

func (t *turn) Model() string { return t.model }
func (t *turn) emit(e inference.Event) bool {
	if t.ctx.Err() != nil {
		return false
	}
	select {
	case t.events <- e:
		return true
	case <-t.ctx.Done():
		return false
	}
}
func (t *turn) complete(err error) {
	t.err = err
	t.owner.mu.Lock()
	if errors.Is(err, acp.ErrCleanup) {
		t.owner.cleanupErr = errors.Join(t.owner.cleanupErr, acp.ErrCleanup)
	}
	close(t.done)
	// Capacity is held until the response is delivered or canceled, including completed runs.
	t.owner.mu.Unlock()
}
func (t *turn) Next(ctx context.Context) (inference.Event, error) {
	if err := ctx.Err(); err != nil {
		return inference.Event{}, err
	}
	select {
	case e := <-t.events:
		return e, nil
	default:
	}
	select {
	case e := <-t.events:
		return e, nil
	case <-ctx.Done():
		return inference.Event{}, ctx.Err()
	case <-t.done:
		select {
		case e := <-t.events:
			return e, nil
		default:
		}
		if t.err != nil {
			return inference.Event{}, t.err
		}
		return inference.Event{}, io.EOF
	}
}
func (t *turn) Finish() { t.Cancel() }
func (t *turn) Cancel() {
	t.cancel()
	<-t.done
	t.owner.mu.Lock()
	delete(t.owner.active, t)
	t.owner.mu.Unlock()
}
