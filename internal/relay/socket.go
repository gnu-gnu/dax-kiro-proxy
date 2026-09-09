package relay

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/schemawire"
	"dax-kiro-proxy/internal/toolregistry"
)

type SocketConfig struct {
	BaseDirectory             string
	Connections               int
	ReadTimeout, WriteTimeout time.Duration
}
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client descriptions keep their complete 8 KiB allowance; relay attribution has its own bound.
const maxMCPDescriptionBytes = 8192 + 256

type ChildConfig struct {
	Version       int       `json:"version"`
	SupervisorPID int       `json:"supervisorPid"`
	Socket        string    `json:"socket"`
	Owner         string    `json:"owner"`
	Secret        string    `json:"secret"`
	TimeoutMillis int64     `json:"timeoutMillis"`
	Tools         []MCPTool `json:"tools"`
}
type Socket struct {
	broker              *Broker
	cfg                 SocketConfig
	directory, path     string
	listener            *net.UnixListener
	mu                  sync.Mutex
	closed              bool
	connections         map[*net.UnixConn]bool
	wg                  sync.WaitGroup
	closeOnce           sync.Once
	closing, groupReady chan struct{}
	group, peer         int
	joined              bool
	closeErr            error
}

func Listen(b *Broker, cfg SocketConfig) (*Socket, error) {
	if cfg.BaseDirectory == "" {
		cfg.BaseDirectory = "/private/tmp"
	}
	if cfg.Connections == 0 {
		cfg.Connections = 65
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 5 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	if b == nil || !filepath.IsAbs(cfg.BaseDirectory) || cfg.Connections < 1 || cfg.Connections > 65 || cfg.ReadTimeout <= 0 || cfg.ReadTimeout > 10*time.Second || cfg.WriteTimeout <= 0 || cfg.WriteTimeout > 10*time.Second {
		return nil, ErrCall
	}
	directory, err := os.MkdirTemp(cfg.BaseDirectory, "dax-r-")
	if err != nil {
		return nil, ErrCall
	}
	path := filepath.Join(directory, "control.sock")
	fail := func() { _ = os.RemoveAll(directory) }
	// Keep the path below the most restrictive supported Unix-domain address capacity.
	if len(path) > 100 {
		fail()
		return nil, ErrCall
	}
	store, err := privatefs.New(directory)
	if err != nil {
		fail()
		return nil, ErrCall
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		fail()
		return nil, ErrCall
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		fail()
		return nil, ErrCall
	}
	child := ChildConfig{Version: 2, SupervisorPID: os.Getpid(), Socket: path, Owner: b.credentials.Owner, Secret: b.credentials.Secret, TimeoutMillis: (b.limits.ToolTimeout + 15*time.Second).Milliseconds(), Tools: make([]MCPTool, 0)}
	for _, tool := range b.registry.Tools() {
		description := "Client tool name: \"" + tool.Name + "\". This relay requests execution by the client; client permissions and hooks decide whether it runs.\n\n" + tool.Description
		child.Tools = append(child.Tools, MCPTool{Name: tool.Alias, Description: description, InputSchema: tool.Schema})
	}
	encoded, err := json.Marshal(child)
	if err != nil || store.Write("relay.json", encoded) != nil {
		_ = listener.Close()
		fail()
		return nil, ErrCall
	}
	s := &Socket{broker: b, cfg: cfg, directory: directory, path: path, listener: listener, connections: make(map[*net.UnixConn]bool), closing: make(chan struct{}), groupReady: make(chan struct{})}
	s.wg.Add(1)
	go s.accept()
	go func() { <-b.Done(); s.Close() }()
	return s, nil
}
func (s *Socket) Directory() string  { return s.directory }
func (s *Socket) Path() string       { return s.path }
func (s *Socket) ConfigPath() string { return filepath.Join(s.directory, "relay.json") }
func (s *Socket) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed || len(s.connections) >= s.cfg.Connections {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.connections[conn] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serve(conn)
	}
}
func (s *Socket) serve(conn *net.UnixConn) {
	defer func() { _ = conn.Close(); s.mu.Lock(); delete(s.connections, conn); s.mu.Unlock(); s.wg.Done() }()
	if !s.setDeadline(conn, s.cfg.ReadTimeout, false) {
		return
	}
	raw, err := ReadFrame(conn)
	if err != nil {
		return
	}
	fields, err := ndjson.Object(raw)
	if err != nil {
		return
	}
	if _, present := fields["operation"]; present {
		s.attach(conn, fields)
		return
	}
	call, err := decodeCall(raw)
	if err != nil {
		return
	}
	s.mu.Lock()
	group, peer, joined := s.group, s.peer, s.joined
	s.mu.Unlock()
	if group != 0 {
		pid, err := socketPeerPID(conn)
		if err != nil || !joined || pid != peer || !processInGroup(pid, group) {
			return
		}
	}
	if !s.setDeadline(conn, s.broker.limits.ToolTimeout+15*time.Second, false) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitorDone := make(chan struct{})
	go func() { defer close(monitorDone); var one [1]byte; _, _ = conn.Read(one[:]); cancel() }()
	defer func() { _ = conn.Close(); <-monitorDone }()
	value, err := s.broker.Call(ctx, call)
	response := map[string]any{"version": 1, "callId": call.ID}
	if err != nil {
		response["error"] = "relay_request_rejected"
	} else {
		response["result"] = value
	}
	if !s.setDeadline(conn, s.cfg.WriteTimeout, true) {
		return
	}
	_ = WriteFrame(conn, response)
}

// A handler cannot extend the shutdown deadline after Close takes ownership of its connection.
func (s *Socket) setDeadline(conn *net.UnixConn, duration time.Duration, write bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if write {
		return conn.SetWriteDeadline(time.Now().Add(duration)) == nil
	}
	return conn.SetReadDeadline(time.Now().Add(duration)) == nil
}
func (s *Socket) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.closing)
		_ = s.listener.Close()
		for conn := range s.connections {
			_ = conn.SetDeadline(time.Now().Add(time.Second))
		}
		s.mu.Unlock()
		s.broker.Close()
		s.wg.Wait()
		if !waitRelayExit(s.peer) {
			s.closeErr = ErrCleanup
		}
		if os.RemoveAll(s.directory) != nil {
			s.closeErr = ErrCleanup
		}
	})
	return s.closeErr
}

func LoadChildConfig(path string) (ChildConfig, error) {
	if !filepath.IsAbs(path) {
		return ChildConfig{}, ErrCall
	}
	store, err := privatefs.New(filepath.Dir(path))
	if err != nil {
		return ChildConfig{}, ErrCall
	}
	raw, err := store.Read(filepath.Base(path), 2<<20)
	if err != nil {
		return ChildConfig{}, ErrCall
	}
	fields, err := ndjson.Object(raw)
	if err != nil || len(fields) != 7 {
		return ChildConfig{}, ErrCall
	}
	var c ChildConfig
	if string(fields["version"]) != "2" || json.Unmarshal(fields["supervisorPid"], &c.SupervisorPID) != nil || c.SupervisorPID <= 1 || c.SupervisorPID > 1<<31-1 || !controlString(fields["socket"], &c.Socket) || !controlString(fields["owner"], &c.Owner) || !controlString(fields["secret"], &c.Secret) || json.Unmarshal(fields["timeoutMillis"], &c.TimeoutMillis) != nil || json.Unmarshal(fields["tools"], &c.Tools) != nil {
		return ChildConfig{}, ErrCall
	}
	c.Version = 2
	if !filepath.IsAbs(c.Socket) || filepath.Dir(c.Socket) != filepath.Dir(path) || len(c.Socket) > 100 || len(c.Owner) != 22 || len(c.Secret) != 43 || c.TimeoutMillis < 1 || c.TimeoutMillis > (time.Hour+15*time.Second).Milliseconds() || len(c.Tools) > toolregistry.MaxTools {
		return ChildConfig{}, ErrCall
	}
	seen := make(map[string]bool)
	for _, tool := range c.Tools {
		if !validCallID(tool.Name) || seen[tool.Name] || len(tool.Description) > maxMCPDescriptionBytes {
			return ChildConfig{}, ErrCall
		}
		seen[tool.Name] = true
		if _, err := schemawire.Schema(tool.InputSchema); err != nil {
			return ChildConfig{}, ErrCall
		}
	}
	return c, nil
}
func Exchange(ctx context.Context, c ChildConfig, call Call) (ToolResult, error) {
	if c.TimeoutMillis <= 0 || c.TimeoutMillis > (time.Hour+15*time.Second).Milliseconds() {
		return ToolResult{}, ErrCall
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMillis)*time.Millisecond)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return ToolResult{}, ErrClosed
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	if err := WriteFrame(conn, call); err != nil {
		return ToolResult{}, ErrCall
	}
	raw, err := ReadFrame(conn)
	if err != nil {
		return ToolResult{}, ErrClosed
	}
	fields, err := ndjson.Object(raw)
	var id string
	if err != nil || len(fields) != 3 || string(fields["version"]) != "1" || !controlString(fields["callId"], &id) || id != call.ID {
		return ToolResult{}, ErrCall
	}
	if _, failed := fields["error"]; failed {
		return ToolResult{}, ErrCall
	}
	var result ToolResult
	if json.Unmarshal(fields["result"], &result) != nil {
		return ToolResult{}, ErrCall
	}
	return validateResult(result)
}
