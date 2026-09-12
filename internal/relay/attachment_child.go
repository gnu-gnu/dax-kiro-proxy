package relay

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"time"

	"dax-kiro-proxy/internal/ndjson"
)

// Attachment couples the MCP reader and pending tool calls to their supervisor's connection.
type Attachment struct {
	ctx    context.Context
	cancel context.CancelFunc
	conn   *net.UnixConn
	done   chan struct{}
	stop   func() bool
	once   sync.Once
}

func Attach(ctx context.Context, config ChildConfig) (*Attachment, error) {
	if config.Version != childConfigVersion || config.SupervisorPID <= 1 || config.AttachTimeoutMillis < 1 || config.AttachTimeoutMillis > time.Minute.Milliseconds() {
		return nil, ErrCall
	}
	setup, cancelSetup := context.WithTimeout(ctx, time.Duration(config.AttachTimeoutMillis)*time.Millisecond)
	defer cancelSetup()
	var dialer net.Dialer
	connection, err := dialer.DialContext(setup, "unix", config.Socket)
	if err != nil {
		return nil, ErrClosed
	}
	conn, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, ErrCall
	}
	keep := false
	defer func() {
		if !keep {
			_ = conn.Close()
		}
	}()
	stopSetup := context.AfterFunc(setup, func() { _ = conn.Close() })
	defer stopSetup()
	deadline, _ := setup.Deadline()
	_ = conn.SetDeadline(deadline)
	pid, err := socketPeerPID(conn)
	if err != nil || pid != config.SupervisorPID {
		return nil, ErrAuth
	}
	if WriteFrame(conn, map[string]any{"version": 1, "operation": "attach", "owner": config.Owner, "secret": config.Secret}) != nil {
		return nil, ErrClosed
	}
	raw, err := ReadFrame(conn)
	if err != nil {
		return nil, ErrClosed
	}
	fields, err := ndjson.Object(raw)
	var group int32
	if err != nil || len(fields) != 3 || string(fields["version"]) != "1" || string(fields["operation"]) != `"attach"` || json.Unmarshal(fields["group"], &group) != nil || int(group) == config.SupervisorPID || !joinRelayGroup(int(group)) {
		return nil, ErrCall
	}
	if WriteFrame(conn, map[string]any{"version": 1, "operation": "joined"}) != nil {
		return nil, ErrClosed
	}
	raw, err = ReadFrame(conn)
	if err != nil {
		return nil, ErrClosed
	}
	fields, err = ndjson.Object(raw)
	if err != nil || len(fields) != 2 || string(fields["version"]) != "1" || string(fields["operation"]) != `"ready"` {
		return nil, ErrCall
	}
	if !stopSetup() || setup.Err() != nil || ctx.Err() != nil {
		return nil, ErrClosed
	}
	_ = conn.SetDeadline(time.Time{})
	lifetime, cancel := context.WithCancel(ctx)
	a := &Attachment{ctx: lifetime, cancel: cancel, conn: conn, done: make(chan struct{})}
	a.stop = context.AfterFunc(lifetime, func() { _ = conn.Close() })
	keep = true
	go func() {
		defer close(a.done)
		var one [1]byte
		_, _ = conn.Read(one[:])
		cancel()
		_ = conn.Close()
	}()
	return a, nil
}

func (a *Attachment) Context() context.Context { return a.ctx }
func (a *Attachment) Close() {
	a.once.Do(func() { a.cancel(); _ = a.conn.Close(); <-a.done; a.stop() })
}
