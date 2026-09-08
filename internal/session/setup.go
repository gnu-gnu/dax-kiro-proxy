package session

import (
	"context"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/ndjson"
)

type prepared struct {
	driver  *Driver
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	client  *acp.Client
	info    catalog.Session
	current string
}

func (p *prepared) finishSetup() { p.cancel(); close(p.done) }

func (d *Driver) prepare(ctx context.Context, reuse bool) (*prepared, error) {
	if err := ctx.Err(); err != nil {
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
	setup, cancel := context.WithTimeout(ctx, d.cfg.SetupTimeout)
	p := &prepared{driver: d, ctx: setup, cancel: cancel, done: make(chan struct{})}
	d.setupCancel = cancel
	d.setupDone = p.done
	d.state = Starting
	previous := d.client
	if reuse && d.fresh && previous != nil {
		p.client = previous
		p.info = d.modelState
		p.current = p.info.Catalog.Current()
	}
	if p.client == nil {
		d.client = nil
	}
	d.mu.Unlock()
	fail := func(err error) (*prepared, error) { d.failedStart(p.client); p.finishSetup(); return nil, err }
	if p.client == nil {
		if previous != nil {
			if err := previous.Close(); err != nil {
				return fail(err)
			}
		}
		var err error
		p.client, err = acp.Start(setup, d.cfg.Process)
		if err != nil {
			return fail(err)
		}
		raw, err := p.client.Call(setup, "session/new", struct {
			CWD string `json:"cwd"`
			MCP []any  `json:"mcpServers"`
		}{d.cfg.Process.Directory, []any{}})
		if err != nil {
			return fail(err)
		}
		p.info, err = catalog.DecodeSession(raw)
		if err != nil {
			return fail(acp.ErrProtocol)
		}
		p.current = p.info.Catalog.Current()
		d.effort.ProcessChanged()
	}
	if err := p.drain(); err != nil {
		return fail(err)
	}
	d.mu.Lock()
	if d.closed || p.ctx.Err() != nil {
		d.mu.Unlock()
		return fail(acp.ErrClosed)
	}
	d.client = p.client
	d.modelState = p.info
	d.mu.Unlock()
	return p, nil
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
