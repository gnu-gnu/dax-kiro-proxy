// Package session owns ACP session and turn lifetimes independently of HTTP response encoding.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/projection"
)

var ErrBusy = errors.New("session has an active response")

type State string

const (
	Unstarted State = "unstarted"
	Starting  State = "starting"
	Idle      State = "idle"
	Prompting State = "prompting"
	Closed    State = "closed"
)

type Config struct {
	Process      acp.Config
	TurnTimeout  time.Duration
	SetupTimeout time.Duration
}
type Driver struct {
	cfg         Config
	mu          sync.Mutex
	state       State
	client      *acp.Client
	current     *turn
	closed      bool
	setupCancel context.CancelFunc
	setupDone   chan struct{}
	closeOnce   sync.Once
	closeErr    error
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
	// The ACP turn may outlive an individual HTTP tool handoff. Short setup calls use their own ctx.
	cfg.Process.Limits.RequestTimeout = cfg.TurnTimeout
	return &Driver{cfg: cfg, state: Unstarted}, nil
}
func (d *Driver) State() State { d.mu.Lock(); defer d.mu.Unlock(); return d.state }

func (d *Driver) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	prompt, err := projection.Full(r)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, acp.ErrClosed
	}
	if d.state == Starting || d.state == Prompting {
		d.mu.Unlock()
		return nil, ErrBusy
	}
	previous := d.client
	d.client = nil
	d.state = Starting
	setup, cancel := context.WithTimeout(ctx, d.cfg.SetupTimeout)
	d.setupCancel = cancel
	done := make(chan struct{})
	d.setupDone = done
	d.mu.Unlock()
	defer func() { cancel(); close(done) }()
	if previous != nil {
		_ = previous.Close()
	}
	client, err := acp.Start(setup, d.cfg.Process)
	if err != nil {
		d.failedStart(nil)
		return nil, err
	}
	raw, err := client.Call(setup, "session/new", struct {
		CWD string `json:"cwd"`
		MCP []any  `json:"mcpServers"`
	}{d.cfg.Process.Directory, []any{}})
	if err != nil {
		d.failedStart(client)
		return nil, err
	}
	fields, err := ndjson.Object(raw)
	var id string
	if err != nil || !strictString(fields["sessionId"], &id) || len(id) == 0 || len(id) > 1024 {
		d.failedStart(client)
		return nil, acp.ErrProtocol
	}
	// Notifications emitted during setup are owned by this new session, not a prompt response.
	for {
		event, ok, err := client.TryNext()
		if err != nil {
			d.failedStart(client)
			return nil, err
		}
		if !ok {
			break
		}
		if event.SessionID != "" && event.SessionID != id {
			d.failedStart(client)
			return nil, acp.ErrProtocol
		}
	}
	owned, stop := context.WithTimeout(context.Background(), d.cfg.TurnTimeout)
	response, signal := context.WithCancel(context.Background())
	t := &turn{driver: d, client: client, id: id, model: r.Model, owned: owned, cancelOwned: stop, response: response, signalResponse: signal, done: make(chan struct{})}
	d.mu.Lock()
	if d.closed || setup.Err() != nil {
		d.mu.Unlock()
		stop()
		signal()
		d.failedStart(client)
		if setup.Err() != nil {
			return nil, setup.Err()
		}
		return nil, acp.ErrClosed
	}
	d.client = client
	d.current = t
	d.state = Prompting
	d.mu.Unlock()
	go func() {
		t.result, t.resultErr = client.Call(owned, "session/prompt", struct {
			Session string            `json:"sessionId"`
			Prompt  []projection.Text `json:"prompt"`
		}{id, prompt})
		close(t.done)
		signal()
	}()
	return t, nil
}

func (d *Driver) failedStart(client *acp.Client) {
	if client != nil {
		_ = client.Close()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.client = nil
	d.current = nil
	if !d.closed {
		d.state = Unstarted
	}
}

// Model discovery and selection are supplied by the catalog phase; this driver never invents one.
func (d *Driver) Models(context.Context) ([]inference.Model, error) {
	return nil, errors.New("model discovery is not configured")
}

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
		d.mu.Unlock()
		if current != nil {
			current.Cancel()
		}
		// Finish may have won its single-flight race against Cancel; Close still owns the process.
		if client != nil {
			d.closeErr = client.Close()
		}
		if setup != nil {
			<-setup
		}
	})
	return d.closeErr
}

type turn struct {
	driver            *Driver
	client            *acp.Client
	id, model         string
	owned             context.Context
	cancelOwned       context.CancelFunc
	response          context.Context
	signalResponse    context.CancelFunc
	done              chan struct{}
	result            json.RawMessage
	resultErr         error
	nextMu            sync.Mutex
	terminal, success bool
	settle            sync.Once
}

func (t *turn) Model() string { return t.model }
func (t *turn) Next(ctx context.Context) (inference.Event, error) {
	t.nextMu.Lock()
	defer t.nextMu.Unlock()
	if t.terminal {
		return inference.Event{}, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return inference.Event{}, err
		}
		var event acp.Notification
		var err error
		select {
		case <-t.done:
			var ok bool
			event, ok, err = t.client.TryNext()
			if err != nil {
				return inference.Event{}, err
			}
			if !ok {
				t.terminal = true
				if t.resultErr != nil {
					return inference.Event{}, t.resultErr
				}
				obj, err := ndjson.Object(t.result)
				var stop string
				if err != nil || !strictString(obj["stopReason"], &stop) {
					return inference.Event{}, acp.ErrProtocol
				}
				switch stop {
				case "end_turn", "max_tokens", "refusal":
					t.success = true
					return inference.Event{Kind: inference.End, StopReason: stop}, nil
				case "cancelled":
					return inference.Event{}, context.Canceled
				default:
					return inference.Event{}, acp.ErrProtocol
				}
			}
		default:
			wait, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(t.response, cancel)
			event, err = t.client.Next(wait)
			stop()
			cancel()
			if err != nil {
				select {
				case <-t.done:
					continue
				default:
					return inference.Event{}, err
				}
			}
		}
		if event.SessionID != "" && event.SessionID != t.id {
			return inference.Event{}, acp.ErrProtocol
		}
		if event.Method != "session/update" {
			continue
		}
		if event.SessionID != t.id {
			return inference.Event{}, acp.ErrProtocol
		}
		fields, err := ndjson.Object(event.Params)
		if err != nil {
			return inference.Event{}, acp.ErrProtocol
		}
		update, err := ndjson.Object(fields["update"])
		if err != nil {
			return inference.Event{}, acp.ErrProtocol
		}
		var kind string
		if !strictString(update["sessionUpdate"], &kind) {
			return inference.Event{}, acp.ErrProtocol
		}
		if kind != "agent_message_chunk" {
			continue
		}
		content, err := ndjson.Object(update["content"])
		var typ, text string
		if err != nil || !strictString(content["type"], &typ) || typ != "text" || !strictString(content["text"], &text) {
			return inference.Event{}, acp.ErrProtocol
		}
		if text == "" {
			continue
		}
		return inference.Event{Kind: inference.Text, Text: text}, nil
	}
}
func (t *turn) Finish() {
	t.nextMu.Lock()
	success := t.success
	t.nextMu.Unlock()
	if !success {
		t.Cancel()
		return
	}
	t.settle.Do(func() { t.cancelOwned(); t.release(false) })
}
func (t *turn) Cancel() {
	t.settle.Do(func() { t.cancelOwned(); _ = t.client.Close(); <-t.done; t.release(true) })
}
func (t *turn) release(discard bool) {
	d := t.driver
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current != t {
		return
	}
	d.current = nil
	if discard {
		d.client = nil
	}
	if !d.closed {
		if discard {
			d.state = Unstarted
		} else {
			d.state = Idle
		}
	}
}
func strictString(raw json.RawMessage, out *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, out) == nil
}
