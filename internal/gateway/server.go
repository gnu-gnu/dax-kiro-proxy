package gateway

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	ErrServerParameters = errors.New("invalid gateway server configuration")
	ErrServerBind       = errors.New("cannot bind the gateway listener")
	ErrServerServe      = errors.New("gateway listener stopped unexpectedly")
	ErrServerCleanup    = errors.New("gateway shutdown did not finish within its limits")
)

type ServerConfig struct {
	Gateway                                                  Config
	Address                                                  string
	UnsafeNetwork                                            bool
	MaxConnections                                           int
	HeaderTimeout, IdleTimeout, ShutdownTimeout, JoinTimeout time.Duration
}
type ServerStats struct {
	Connections, Handlers int
	Closing               bool
}
type Server struct {
	cfg                                   ServerConfig
	http                                  *http.Server
	listener                              *boundedListener
	mu                                    sync.Mutex
	handlers                              int
	connectionTasks                       int
	closing                               bool
	cancel                                context.CancelFunc
	closeRequested, served, done, changed chan struct{}
	closeOnce                             sync.Once
	serveErr, closeErr                    error
}

func StartServer(ctx context.Context, cfg ServerConfig) (*Server, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if cfg.MaxConnections == 0 {
		cfg.MaxConnections = 64
	}
	if cfg.HeaderTimeout == 0 {
		cfg.HeaderTimeout = 5 * time.Second
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Second
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if cfg.JoinTimeout == 0 {
		cfg.JoinTimeout = time.Second
	}
	if cfg.MaxConnections < 1 || cfg.MaxConnections > 1024 || cfg.HeaderTimeout <= 0 || cfg.HeaderTimeout > time.Minute || cfg.IdleTimeout <= 0 || cfg.IdleTimeout > time.Hour || cfg.ShutdownTimeout <= 0 || cfg.ShutdownTimeout > 10*time.Second || cfg.JoinTimeout <= 0 || cfg.JoinTimeout > 5*time.Second || len(cfg.Address) > 4096 {
		return nil, ErrServerParameters
	}
	handler, err := New(cfg.Gateway)
	if err != nil {
		return nil, ErrServerParameters
	}
	setup, stop := context.WithTimeout(ctx, 5*time.Second)
	listener, err := listen(setup, cfg.Address, cfg.UnsafeNetwork)
	stop()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrServerBind
	}
	if ctx.Err() != nil {
		listener.Close()
		return nil, ctx.Err()
	}
	base, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, cancel: cancel, closeRequested: make(chan struct{}), served: make(chan struct{}), done: make(chan struct{}), changed: make(chan struct{}, 1)}
	s.listener = &boundedListener{Listener: listener, limit: cfg.MaxConnections, connections: make(map[*ownedConnection]struct{}), changed: s.signal}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	s.http = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w = &deadlineResponse{ResponseWriter: w, timeout: handler.cfg.WriteTimeout}
			s.mu.Lock()
			s.handlers++
			closing := s.closing
			s.mu.Unlock()
			defer func() { s.mu.Lock(); s.handlers--; s.mu.Unlock(); s.signal() }()
			if closing {
				w.Header().Set("Connection", "close")
				writeError(w, 503, "api_error", "Gateway is shutting down")
				return
			}
			handler.ServeHTTP(w, r)
		}),
		ConnState: func(_ net.Conn, state http.ConnState) {
			s.mu.Lock()
			switch state {
			case http.StateNew:
				s.connectionTasks++
			case http.StateClosed, http.StateHijacked:
				s.connectionTasks--
			}
			s.mu.Unlock()
			s.signal()
		},
		BaseContext:       func(net.Listener) context.Context { return base },
		ReadHeaderTimeout: cfg.HeaderTimeout, ReadTimeout: handler.cfg.ReadTimeout,
		WriteTimeout: handler.cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
		MaxHeaderBytes: 64 << 10, Protocols: protocols,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		err := s.http.Serve(s.listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			s.serveErr = ErrServerServe
		}
		close(s.served)
		s.signal()
	}()
	go s.run(ctx)
	return s, nil
}
func (s *Server) Address() string       { return s.listener.Addr().String() }
func (s *Server) URL() string           { return "http://" + s.Address() }
func (s *Server) Done() <-chan struct{} { return s.done }
func (s *Server) Wait() error           { <-s.done; return s.closeErr }
func (s *Server) Close() error {
	s.closeOnce.Do(func() { close(s.closeRequested) })
	return s.Wait()
}
func (s *Server) Stats() ServerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listener.mu.Lock()
	defer s.listener.mu.Unlock()
	return ServerStats{Connections: len(s.listener.connections), Handlers: s.handlers, Closing: s.closing}
}
func (s *Server) signal() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}
func (s *Server) quiescent() bool {
	select {
	case <-s.served:
	default:
		return false
	}
	stats := s.Stats()
	s.mu.Lock()
	defer s.mu.Unlock()
	return stats.Connections == 0 && stats.Handlers == 0 && s.connectionTasks == 0
}
func (s *Server) run(parent context.Context) {
	select {
	case <-parent.Done():
	case <-s.closeRequested:
	case <-s.served:
	}
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.cancel()
	// The model context is canceled before HTTP draining. Shutdown has its own context, then all
	// remaining sockets are forced closed and in-flight handler completion is checked separately.
	shield, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	err := s.http.Shutdown(shield)
	cancel()
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		s.closeErr = ErrServerCleanup
	}
	if err := s.http.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		s.closeErr = ErrServerCleanup
	}
	s.listener.closeConnections()
	deadline := time.NewTimer(s.cfg.JoinTimeout)
	defer deadline.Stop()
	for !s.quiescent() {
		select {
		case <-s.changed:
		case <-deadline.C:
			s.closeErr = errors.Join(s.closeErr, ErrServerCleanup)
			close(s.done)
			return
		}
	}
	s.closeErr = errors.Join(s.closeErr, s.serveErr)
	close(s.done)
}

type boundedListener struct {
	net.Listener
	mu          sync.Mutex
	limit       int
	connections map[*ownedConnection]struct{}
	closed      bool
	closeOnce   sync.Once
	closeErr    error
	changed     func()
}

func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		l.mu.Lock()
		closed := l.closed
		if closed || len(l.connections) >= l.limit {
			l.mu.Unlock()
			conn.Close()
			if closed {
				return nil, net.ErrClosed
			}
			continue
		}
		owned := &ownedConnection{Conn: conn, listener: l}
		l.connections[owned] = struct{}{}
		l.mu.Unlock()
		l.changed()
		return owned, nil
	}
}
func (l *boundedListener) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}
func (l *boundedListener) closeConnections() {
	l.mu.Lock()
	connections := make([]*ownedConnection, 0, len(l.connections))
	for conn := range l.connections {
		connections = append(connections, conn)
	}
	l.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

type ownedConnection struct {
	net.Conn
	listener *boundedListener
	once     sync.Once
	err      error
}

func (c *ownedConnection) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.listener.mu.Lock()
		delete(c.listener.connections, c)
		c.listener.mu.Unlock()
		c.listener.changed()
	})
	return c.err
}

// Per-write deadlines also cover health, auth and catalog responses. They refresh only transport
// writes; they do not extend the model's first-event or total-turn deadline.
type deadlineResponse struct {
	http.ResponseWriter
	timeout time.Duration
}

func (w *deadlineResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *deadlineResponse) deadline() error {
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(w.timeout))
}
func (w *deadlineResponse) WriteHeader(status int) {
	if w.deadline() == nil {
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *deadlineResponse) Write(data []byte) (int, error) {
	if err := w.deadline(); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(data)
}
func (w *deadlineResponse) FlushError() error {
	if err := w.deadline(); err != nil {
		return err
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
