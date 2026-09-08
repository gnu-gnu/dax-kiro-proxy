package session

import (
	"errors"

	"dax-kiro-proxy/internal/relay"
)

func closeRelay(socket *relay.Socket, broker *relay.Broker) error {
	if socket != nil {
		return socket.Close()
	}
	if broker != nil {
		broker.Close()
	}
	return nil
}

func (d *Driver) noteCleanup(err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	d.cleanupErr = errors.Join(d.cleanupErr, err)
	d.mu.Unlock()
}

func (d *Driver) cleanupFailure() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cleanupErr
}
