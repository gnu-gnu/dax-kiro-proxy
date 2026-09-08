package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"dax-kiro-proxy/internal/ndjson"
)

type completion struct {
	result json.RawMessage
	err    error
}
type pendingCall struct {
	done    chan completion
	session string
}
type outbound struct {
	data []byte
	done chan error
}
type Client struct {
	config Config
	limits Limits

	cmd                   *exec.Cmd
	stdin, stdout, stderr *os.File

	mu              sync.Mutex
	ready           bool
	caps            Capabilities
	nextID          uint64
	pending         map[uint64]pendingCall
	err, cleanupErr error

	events     chan Notification
	activity   chan struct{}
	eventBytes int
	writes     chan outbound
	writeBytes int

	failed, done, exited, readDone, stderrDone, writeDone, stopWriter chan struct{}

	stderrTail  []string
	stderrBytes int
	truncated   uint64
}

func Start(ctx context.Context, cfg Config) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits, err := cfg.Limits.normalized()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(cfg.Executable) || !filepath.IsAbs(cfg.Directory) || cfg.ClientInfo.Name == "" || cfg.ClientInfo.Version == "" {
		return nil, ErrParameters
	}
	cmd := exec.Command(cfg.Executable, cfg.Args...)
	cmd.Dir = cfg.Directory
	cmd.Env = append([]string{}, cfg.Environment...)
	if err = configureProcess(cmd); err != nil {
		return nil, err
	}
	var opened []*os.File
	pipe := func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err == nil {
			opened = append(opened, r, w)
		}
		return r, w, err
	}
	closeAll := func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}
	inR, inW, err := pipe()
	if err != nil {
		return nil, ErrTransport
	}
	outR, outW, err := pipe()
	if err != nil {
		closeAll()
		return nil, ErrTransport
	}
	errR, errW, err := pipe()
	if err != nil {
		closeAll()
		return nil, ErrTransport
	}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err = cmd.Start(); err != nil {
		closeAll()
		return nil, ErrTransport
	}
	_ = inR.Close()
	_ = outW.Close()
	_ = errW.Close()
	c := &Client{config: cfg, limits: limits, cmd: cmd, stdin: inW, stdout: outR, stderr: errR, nextID: 1, pending: make(map[uint64]pendingCall), events: make(chan Notification, limits.EventQueue), activity: make(chan struct{}, 1), writes: make(chan outbound, limits.WriteQueue), failed: make(chan struct{}), done: make(chan struct{}), exited: make(chan struct{}), readDone: make(chan struct{}), stderrDone: make(chan struct{}), writeDone: make(chan struct{}), stopWriter: make(chan struct{})}
	go c.readLoop()
	go c.writeLoop()
	go c.stderrLoop()
	go c.waitLoop()
	result, err := c.call(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}, "clientInfo": cfg.ClientInfo}, true)
	if err == nil {
		var fields map[string]json.RawMessage
		fields, err = ndjson.Object(result)
		if err != nil || string(fields["protocolVersion"]) != "1" {
			err = ErrProtocol
		} else if capBytes, ok := fields["agentCapabilities"]; ok {
			err = json.Unmarshal(capBytes, &c.caps)
			if err != nil {
				err = ErrProtocol
			}
		}
	}
	if err != nil {
		c.retire(err)
		_ = c.Close()
		return nil, err
	}
	c.mu.Lock()
	c.ready = true
	err = c.err
	c.mu.Unlock()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
func (c *Client) PID() int                   { return c.cmd.Process.Pid }
func (c *Client) Capabilities() Capabilities { return c.caps }
func (c *Client) Done() <-chan struct{}      { return c.done }
func (c *Client) Err() error                 { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

// Activity is a coalescing wake hint for a single TryNext consumer multiplexing another event source.
// Consumers always drain TryNext before waiting again; a hint is not itself a protocol event.
func (c *Client) Activity() <-chan struct{} { return c.activity }
func (c *Client) signalActivity() {
	select {
	case c.activity <- struct{}{}:
	default:
	}
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.call(ctx, method, params, false)
}
func (c *Client) call(ctx context.Context, method string, params any, initializing bool) (json.RawMessage, error) {
	bounded, cancel := context.WithTimeout(ctx, c.limits.RequestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == "" || len(method) > 256 {
		return nil, ErrParameters
	}
	p, err := json.Marshal(params)
	if err != nil {
		return nil, ErrParameters
	}
	if params == nil {
		p = []byte("{}")
	}
	c.mu.Lock()
	if c.err != nil {
		err = c.err
		c.mu.Unlock()
		return nil, err
	}
	if !c.ready && !initializing {
		c.mu.Unlock()
		return nil, ErrProtocol
	}
	if len(c.pending) >= c.limits.Pending {
		c.mu.Unlock()
		return nil, ErrOverloaded
	}
	if c.nextID > 1<<53-1 {
		c.mu.Unlock()
		c.retire(ErrProtocol)
		return nil, ErrProtocol
	}
	id := c.nextID
	c.nextID++
	frame, err := json.Marshal(struct {
		Version string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{"2.0", id, method, p})
	if err != nil {
		c.mu.Unlock()
		return nil, ErrParameters
	}
	if len(frame) > c.limits.FrameBytes {
		c.mu.Unlock()
		c.retire(ErrFrameTooLarge)
		return nil, ErrFrameTooLarge
	}
	if _, err = ndjson.Object(frame); err != nil || len(p) == 0 || (p[0] != '{' && p[0] != '[') {
		c.mu.Unlock()
		return nil, ErrParameters
	}
	pending := pendingCall{done: make(chan completion, 1)}
	if method == "session/prompt" {
		pending.session = sessionID(p)
	}
	c.pending[id] = pending
	err = c.enqueueLocked(outbound{data: append(frame, '\n')}, false)
	c.mu.Unlock()
	if err != nil {
		c.retire(err)
	}
	select {
	case answer := <-pending.done:
		return answer.result, answer.err
	case <-bounded.Done():
		select {
		case answer := <-pending.done:
			return answer.result, answer.err
		default:
		}
		err = bounded.Err()
		if errors.Is(err, context.DeadlineExceeded) {
			err = ErrTimeout
		}
		c.retire(err)
		return nil, err
	}
}

func (c *Client) Notify(ctx context.Context, method string, params any) error {
	return c.notify(ctx, method, params, false)
}
func (c *Client) notify(ctx context.Context, method string, params any, closing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if method == "" || len(method) > 256 {
		return ErrParameters
	}
	if params == nil {
		params = map[string]any{}
	}
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return ErrParameters
	}
	if len(frame) > c.limits.FrameBytes {
		c.retire(ErrFrameTooLarge)
		return ErrFrameTooLarge
	}
	obj, err := ndjson.Object(frame)
	if err != nil || (obj["params"][0] != '{' && obj["params"][0] != '[') {
		return ErrParameters
	}
	bounded, cancel := context.WithTimeout(ctx, c.limits.WriteTimeout)
	defer cancel()
	packet := outbound{append(frame, '\n'), make(chan error, 1)}
	if err = c.enqueue(packet, closing); err != nil {
		if !closing && errors.Is(err, ErrOverloaded) {
			c.retire(err)
		}
		return err
	}
	select {
	case err = <-packet.done:
		return err
	case <-bounded.Done():
		err = bounded.Err()
		if errors.Is(err, context.DeadlineExceeded) {
			err = ErrTimeout
		}
		if !closing {
			c.retire(err)
		}
		return err
	case <-c.writeDone:
		return ErrClosed
	}
}

func (c *Client) enqueue(packet outbound, closing bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enqueueLocked(packet, closing)
}

func (c *Client) enqueueLocked(packet outbound, closing bool) error {
	if c.err != nil && !closing {
		return c.err
	}
	if len(packet.data) > c.limits.WriteBytes-c.writeBytes {
		return ErrOverloaded
	}
	select {
	case c.writes <- packet:
		c.writeBytes += len(packet.data)
		return nil
	default:
		return ErrOverloaded
	}
}

func (c *Client) Next(ctx context.Context) (Notification, error) {
	if err := c.Err(); err != nil {
		return Notification{}, err
	}
	select {
	case event := <-c.events:
		c.mu.Lock()
		c.eventBytes -= event.size
		err := c.err
		c.mu.Unlock()
		if err != nil {
			return Notification{}, err
		}
		return event, nil
	case <-ctx.Done():
		return Notification{}, ctx.Err()
	case <-c.failed:
		return Notification{}, c.Err()
	}
}

// TryNext drains notifications already read from stdout without waiting. A single consumer can
// use it after an RPC completes to deliver preceding notifications before its terminal result.
func (c *Client) TryNext() (Notification, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return Notification{}, false, c.err
	}
	select {
	case event := <-c.events:
		c.eventBytes -= event.size
		return event, true, nil
	default:
		return Notification{}, false, nil
	}
}

func (c *Client) writeLoop() {
	defer close(c.writeDone)
	for {
		select {
		case <-c.stopWriter:
			return
		default:
		}
		var packet outbound
		select {
		case <-c.stopWriter:
			return
		case packet = <-c.writes:
		}
		c.mu.Lock()
		c.writeBytes -= len(packet.data)
		c.mu.Unlock()
		err := c.stdin.SetWriteDeadline(time.Now().Add(c.limits.WriteTimeout))
		if err == nil {
			_, err = c.stdin.Write(packet.data)
		}
		if err != nil {
			err = ErrTransport
			c.retire(err)
		}
		if packet.done != nil {
			packet.done <- err
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.readDone)
	r := ndjson.NewReader(c.stdout, c.limits.FrameBytes)
	for {
		frame, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Let bounded stderr draining classify an immediately exiting backend first.
				select {
				case <-c.stderrDone:
				case <-time.After(50 * time.Millisecond):
				}
				err = ErrTransport
			} else if errors.Is(err, ndjson.ErrTooLarge) {
				err = ErrFrameTooLarge
			} else if errors.Is(err, ndjson.ErrInvalid) {
				err = ErrProtocol
			} else {
				err = ErrTransport
			}
			c.retire(err)
			return
		}
		if err = c.receive(frame); err != nil {
			c.retire(err)
			return
		}
	}
}

func (c *Client) receive(frame []byte) error {
	obj, err := ndjson.Object(frame)
	if err != nil {
		return ErrProtocol
	}
	var version string
	if json.Unmarshal(obj["jsonrpc"], &version) != nil || version != "2.0" {
		return ErrProtocol
	}
	methodRaw, hasMethod := obj["method"]
	id, hasID := obj["id"]
	result, hasResult := obj["result"]
	remoteErr, hasError := obj["error"]
	if hasMethod {
		var method string
		if json.Unmarshal(methodRaw, &method) != nil || method == "" || len(method) > 256 || hasResult || hasError {
			return ErrProtocol
		}
		params := obj["params"]
		if len(params) > 0 && params[0] != '{' && params[0] != '[' {
			return ErrProtocol
		}
		if hasID {
			if !validAgentID(id) {
				return ErrProtocol
			}
			return c.rejectAgent(id, method, params)
		}
		event := Notification{Method: method, SessionID: sessionID(params), Params: params, size: len(frame)}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.err != nil {
			return c.err
		}
		if event.size > c.limits.EventBytes-c.eventBytes {
			return ErrOverloaded
		}
		select {
		case c.events <- event:
			c.eventBytes += event.size
			c.signalActivity()
			return nil
		default:
			return ErrOverloaded
		}
	}
	if !hasID || hasResult == hasError {
		return ErrProtocol
	}
	numericID, err := strconv.ParseUint(string(id), 10, 64)
	if err != nil || numericID == 0 || numericID > 1<<53-1 {
		return ErrProtocol
	}
	c.mu.Lock()
	_, correlated := c.pending[numericID]
	c.mu.Unlock()
	if !correlated {
		return ErrProtocol
	}
	answer := completion{result: result}
	if hasError {
		fields, err := ndjson.Object(remoteErr)
		if err != nil {
			return ErrProtocol
		}
		code, codeErr := strconv.ParseInt(string(fields["code"]), 10, 32)
		var message string
		if codeErr != nil || len(fields["message"]) == 0 || fields["message"][0] != '"' || json.Unmarshal(fields["message"], &message) != nil {
			return ErrProtocol
		}
		if c.config.Auth != nil && c.config.Auth.Error(int(code), message, fields["data"]) {
			return ErrAuthentication
		}
		answer = completion{err: &RemoteError{Code: int(code)}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pending, ok := c.pending[numericID]
	if !ok {
		return ErrProtocol
	}
	delete(c.pending, numericID)
	pending.done <- answer
	return nil
}
func validAgentID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	if id[0] == '"' {
		var value string
		return json.Unmarshal(id, &value) == nil && len(value) <= 256
	}
	_, err := strconv.ParseInt(string(id), 10, 64)
	return err == nil
}
func sessionID(params json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(params, &fields) != nil {
		return ""
	}
	var id string
	if json.Unmarshal(fields["sessionId"], &id) != nil || len(id) > 1024 {
		return ""
	}
	return id
}
func (c *Client) rejectAgent(id json.RawMessage, method string, params json.RawMessage) error {
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if method == "session/request_permission" {
		outcome := map[string]any{"outcome": "cancelled"}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(params, &fields)
		var options []map[string]json.RawMessage
		_ = json.Unmarshal(fields["options"], &options)
		for _, option := range options {
			var kind, optionID string
			_ = json.Unmarshal(option["kind"], &kind)
			_ = json.Unmarshal(option["optionId"], &optionID)
			if (kind == "reject_once" || kind == "reject_always") && optionID != "" && len(optionID) <= 1024 {
				outcome = map[string]any{"outcome": "selected", "optionId": optionID}
				break
			}
		}
		response["result"] = map[string]any{"outcome": outcome}
	} else {
		response["error"] = map[string]any{"code": -32601, "message": "Agent method is disabled"}
	}
	frame, err := json.Marshal(response)
	if err != nil {
		return ErrProtocol
	}
	if len(frame) > c.limits.FrameBytes {
		return ErrFrameTooLarge
	}
	return c.enqueue(outbound{data: append(frame, '\n')}, false)
}

func (c *Client) retire(reason error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = reason
	sessions := make(map[string]struct{})
	for id, pending := range c.pending {
		if pending.session != "" {
			sessions[pending.session] = struct{}{}
		}
		pending.done <- completion{err: reason}
		delete(c.pending, id)
	}
	close(c.failed)
	c.signalActivity()
	c.mu.Unlock()
	go c.cleanup(sessions)
}
func (c *Client) waitLoop() {
	_ = c.cmd.Wait()
	close(c.exited)
	select {
	case <-c.readDone:
	case <-time.After(100 * time.Millisecond):
	}
	c.retire(ErrTransport)
}

// Close joins the independently bounded cleanup even if the initiating caller was canceled.
func (c *Client) Close() error {
	c.retire(ErrClosed)
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cleanupErr
}
func (c *Client) cleanup(sessions map[string]struct{}) {
	defer close(c.done)
	ctx, cancel := context.WithTimeout(context.Background(), c.limits.CancelTimeout)
	for session := range sessions {
		if ctx.Err() != nil {
			break
		}
		_ = c.notify(ctx, "session/cancel", map[string]string{"sessionId": session}, true)
	}
	cancel()
	close(c.stopWriter)
	_ = c.stdin.Close()
	waitGroup := func(duration time.Duration) bool {
		deadline := time.NewTimer(duration)
		defer deadline.Stop()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if !groupExists(c.PID()) {
				select {
				case <-c.exited:
					return true
				default:
				}
			}
			select {
			case <-deadline.C:
				return false
			case <-tick.C:
			}
		}
	}
	var cleanupErr error
	if !waitGroup(c.limits.GracePeriod) {
		if err := signalGroup(c.PID(), false); err != nil {
			cleanupErr = ErrTransport
		}
		if !waitGroup(c.limits.TermPeriod) {
			if err := signalGroup(c.PID(), true); err != nil {
				cleanupErr = ErrTransport
			}
			if !waitGroup(c.limits.KillPeriod) {
				cleanupErr = ErrTransport
			}
		}
	}
	_ = c.stdout.Close()
	_ = c.stderr.Close()
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	for _, done := range []<-chan struct{}{c.readDone, c.stderrDone, c.writeDone, c.exited} {
		select {
		case <-done:
		case <-deadline.C:
			cleanupErr = ErrTransport
			goto finished
		}
	}
finished:
	c.mu.Lock()
	for draining := true; draining; {
		select {
		case packet := <-c.writes:
			c.writeBytes -= len(packet.data)
			if packet.done != nil {
				packet.done <- ErrClosed
			}
		default:
			draining = false
		}
	}
	for draining := true; draining; {
		select {
		case event := <-c.events:
			c.eventBytes -= event.size
		default:
			draining = false
		}
	}
	c.cleanupErr = cleanupErr
	c.stderrTail = nil
	c.stderrBytes = 0
	c.mu.Unlock()
}
