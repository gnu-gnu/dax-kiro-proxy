package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/sessionstore"
	"dax-kiro-proxy/internal/toolregistry"
)

type backendClient interface {
	PID() int
	Call(context.Context, string, any) (json.RawMessage, error)
	TryNext() (acp.Notification, bool, error)
	Activity() <-chan struct{}
	Done() <-chan struct{}
	Capabilities() acp.Capabilities
	Err() error
	Close() error
}

type prepared struct {
	driver  *Driver
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	client  backendClient
	info    catalog.Session
	current string
	reused  bool
	loaded  bool
}

func (p *prepared) finishSetup() { p.cancel(); close(p.done) }

func (d *Driver) prepare(ctx context.Context, reuse bool, registry *toolregistry.Registry, processStamp [32]byte, resume *sessionstore.Record) (*prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if registry == nil && d.cfg.Validator != nil {
		var err error
		registry, err = toolregistry.Build(ctx, nil, nil, d.cfg.Validator)
		if err != nil {
			return nil, err
		}
	}
	fingerprint := ""
	if registry != nil {
		fingerprint = registry.Fingerprint()
	}
	d.mu.Lock()
	if d.closed || d.cleanupErr != nil {
		err := errors.Join(acp.ErrClosed, d.cleanupErr)
		d.mu.Unlock()
		return nil, err
	}
	if d.state == Starting || d.state == Prompting || d.state == WaitingTools {
		d.mu.Unlock()
		return nil, ErrBusy
	}
	setup, cancel := context.WithTimeout(ctx, d.cfg.SetupTimeout)
	p := &prepared{driver: d, ctx: setup, cancel: cancel, done: make(chan struct{})}
	d.setupCancel = cancel
	d.setupDone = p.done
	d.state = Starting
	previous := d.client
	previousSocket, previousBroker := d.socket, d.broker
	if reuse && previous != nil && previous.Err() == nil && fingerprint == d.registryFingerprint && (d.cfg.Pool == nil || processStamp == d.processCompat) {
		p.client = previous
		p.info = d.modelState
		p.current = p.info.Catalog.Current()
		p.reused = true
	}
	if p.client == nil {
		d.client = nil
		d.socket = nil
		d.broker = nil
	}
	d.mu.Unlock()
	fail := func(err error) (*prepared, error) {
		d.failedStart(p.client)
		p.finishSetup()
		return nil, errors.Join(err, d.cleanupFailure())
	}
	if lease, ok := p.client.(*acppool.Lease); ok {
		if lease.SetIdle(false) != nil {
			p.client = nil
			p.reused = false
		}
	}
	if p.client == nil {
		cleanupErr := closeRelay(previousSocket, previousBroker)
		if previous != nil {
			if cleanupErr != nil {
				cleanupErr = errors.Join(cleanupErr, previous.Close())
			} else {
				cleanupErr = releaseIdle(previous)
			}
		}
		if cleanupErr != nil {
			d.noteCleanup(cleanupErr)
			return fail(cleanupErr)
		}
		var err error
		mcp := []any{}
		relayConfig := ""
		if registry != nil {
			broker, err := relay.NewBroker(registry, d.cfg.RelayLimits)
			if err != nil {
				return fail(err)
			}
			socket, err := relay.Listen(broker, relay.SocketConfig{AttachTimeout: d.cfg.SetupTimeout, SetupContext: setup})
			if err != nil {
				broker.Close()
				return fail(err)
			}
			d.mu.Lock()
			d.broker = broker
			d.socket = socket
			d.mu.Unlock()
			relayConfig = socket.ConfigPath()
			mcp = append(mcp, map[string]any{"name": "dax_session", "command": d.cfg.RelayExecutable, "args": []string{"relay", "--config", socket.ConfigPath()}, "env": []any{}})
		}
		if d.cfg.Pool != nil {
			scope := sha256.Sum256(append([]byte(d.cfg.PoolScope+"\x00"), processStamp[:]...))
			var lease *acppool.Lease
			if d.cfg.PrepareLaunch != nil {
				lease, err = d.cfg.Pool.AcquirePrepared(setup, hex.EncodeToString(scope[:]), func(ctx context.Context) (acppool.PreparedProcess, error) {
					resources, err := d.cfg.PrepareLaunch(ctx, LaunchInput{Registry: registry, RelayExecutable: d.cfg.RelayExecutable, RelayConfig: relayConfig})
					process := d.cfg.Process
					process.Args = append(slices.Clone(process.Args), resources.Args...)
					if resources.Directory != "" {
						process.Directory = resources.Directory
					}
					if resources.RelayAtLaunch {
						mcp = []any{}
					}
					return acppool.PreparedProcess{Config: process, Cleanup: resources.Cleanup}, err
				})
			} else {
				lease, err = d.cfg.Pool.Acquire(setup, hex.EncodeToString(scope[:]))
			}
			if err == nil {
				p.client = lease
			}
		} else {
			var client *acp.Client
			client, err = acp.Start(setup, d.cfg.Process)
			if err == nil {
				p.client = client
			}
		}
		if err != nil {
			return fail(err)
		}
		d.mu.Lock()
		socket := d.socket
		d.mu.Unlock()
		if socket != nil {
			if err := socket.BindProcess(p.client.PID()); err != nil {
				return fail(err)
			}
		}
		params := struct {
			CWD string `json:"cwd"`
			MCP []any  `json:"mcpServers"`
		}{d.cfg.Process.Directory, mcp}
		var raw json.RawMessage
		if lease, ok := p.client.(*acppool.Lease); ok {
			id := ""
			if resume != nil {
				id = resume.SessionID
			}
			raw, err = lease.Create(setup, params, id)
		} else {
			raw, err = p.client.Call(setup, "session/new", params)
		}
		if err != nil {
			return fail(err)
		}
		if resume != nil {
			p.info, err = loadedSession(raw, resume)
			p.loaded = err == nil
		} else {
			p.info, err = catalog.DecodeSession(raw)
		}
		if err != nil {
			return fail(acp.ErrProtocol)
		}
		p.current = p.info.Catalog.Current()
		if p.loaded {
			p.current = ""
		} // Confirm the selected model again when a load omits model state.
		d.effort.ProcessChanged()
	}
	if err := p.drain(); err != nil {
		return fail(err)
	}
	if p.loaded {
		if err := p.selectModel(resume.CurrentModel); err != nil {
			return fail(err)
		}
	}
	d.mu.Lock()
	if d.closed || p.ctx.Err() != nil {
		d.mu.Unlock()
		return fail(acp.ErrClosed)
	}
	d.client = p.client
	d.modelState = p.info
	d.registryFingerprint = fingerprint
	d.processCompat = processStamp
	if !p.reused {
		d.processWatch.Add(1)
	}
	d.mu.Unlock()
	if !p.reused {
		go d.watchIdleProcess(p.client)
	}
	return p, nil
}

func (d *Driver) watchIdleProcess(client backendClient) {
	defer d.processWatch.Done()
	<-client.Done()
	d.mu.Lock()
	var current *turn
	if d.client == client {
		current = d.current
	}
	d.mu.Unlock()
	if current != nil {
		current.abort(client.Err())
	}
	d.mu.Lock()
	if d.client != client || d.state != Idle {
		d.mu.Unlock()
		return
	}
	socket, broker := d.socket, d.broker
	d.client = nil
	d.socket = nil
	d.broker = nil
	d.snapshot = history.Snapshot{}
	d.fresh = false
	d.state = Unstarted
	d.mu.Unlock()
	d.noteCleanup(closeRelay(socket, broker))
}

func releaseIdle(client backendClient) error {
	if lease, ok := client.(*acppool.Lease); ok {
		return lease.ReleaseIdle()
	}
	return client.Close()
}

// CloseIdle can evict an idle binding without canceling an active sibling on the same process.
func (d *Driver) CloseIdle() error {
	d.mu.Lock()
	if done := d.idleClosing; done != nil {
		d.mu.Unlock()
		<-done
		return d.idleErr
	}
	if d.closed {
		d.mu.Unlock()
		return d.Close()
	}
	if d.state == Starting || d.state == Prompting || d.state == WaitingTools {
		d.mu.Unlock()
		return ErrBusy
	}
	d.closed = true
	d.idleClosing = make(chan struct{})
	d.state = Closed
	client, socket, broker := d.client, d.socket, d.broker
	d.client = nil
	d.socket = nil
	d.broker = nil
	d.mu.Unlock()
	err := closeRelay(socket, broker)
	if client != nil {
		if err != nil {
			err = errors.Join(err, client.Close())
		} else {
			err = releaseIdle(client)
		}
	}
	d.processWatch.Wait()
	if d.cfg.Persistence != nil {
		err = errors.Join(err, d.cfg.Persistence.Close())
	}
	d.mu.Lock()
	d.cleanupErr = errors.Join(d.cleanupErr, err)
	d.idleErr = d.cleanupErr
	close(d.idleClosing)
	d.mu.Unlock()
	return d.idleErr
}

func (p *prepared) drain() error {
	for {
		event, ok, err := p.client.TryNext()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if event.SessionID != "" && event.SessionID != p.info.ID {
			return acp.ErrProtocol
		}
		if event.Method == "_kiro.dev/commands/available" && event.SessionID == p.info.ID {
			_ = p.driver.effort.Advertise(event.Params)
		}
	}
}

func (p *prepared) selectModel(model string) error {
	if p.current == model {
		return nil
	}
	method := "session/set_model"
	var params any = struct {
		Session string `json:"sessionId"`
		Model   string `json:"modelId"`
	}{p.info.ID, model}
	if p.info.ConfigID != "" {
		method = "session/set_config_option"
		params = struct {
			Session string `json:"sessionId"`
			Config  string `json:"configId"`
			Value   string `json:"value"`
		}{p.info.ID, p.info.ConfigID, model}
	}
	raw, err := p.client.Call(p.ctx, method, params)
	if err != nil {
		return err
	}
	fields, err := ndjson.Object(raw)
	if err != nil {
		return acp.ErrProtocol
	}
	if p.info.ConfigID != "" {
		data, selector, err := catalog.DecodeModels(fields)
		if err != nil || selector != p.info.ConfigID || data.Current() != model {
			return acp.ErrProtocol
		}
		p.info.Catalog = data
	} else {
		if _, present := fields["models"]; present {
			data, _, decodeErr := catalog.DecodeModels(fields)
			if decodeErr != nil || data.Current() != model {
				return acp.ErrProtocol
			}
			p.info.Catalog = data
		}
		p.info.Catalog, err = catalog.New(p.info.Catalog.Models(), model)
		if err != nil {
			return acp.ErrProtocol
		}
	}
	p.current = model
	p.driver.effort.ModelChanged()
	p.driver.mu.Lock()
	p.driver.modelState = p.info
	p.driver.mu.Unlock()
	return nil
}
