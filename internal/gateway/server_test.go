package gateway_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
)

func ownedServer(t *testing.T, ctx context.Context, backend inference.Backend, change func(*gateway.ServerConfig)) *gateway.Server {
	t.Helper()
	cfg := gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: backend}, ShutdownTimeout: time.Second, JoinTimeout: time.Second}
	if change != nil {
		change(&cfg)
	}
	s, err := gateway.StartServer(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func serverCount(t *testing.T, s *gateway.Server, connections, handlers int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats := s.Stats()
		if stats.Connections == connections && stats.Handlers == handlers {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("server lifecycle counts did not converge: %+v", s.Stats())
}
func directClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 2 * time.Second}
}
func serverRequest(t *testing.T, client *http.Client, s *gateway.Server, token string, stream bool) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), "POST", s.URL()+"/v1/messages", strings.NewReader(message(stream)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("x-api-key", token)
	r.Header.Set("Content-Type", "application/json")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
func TestOwnedServerPreservesGatewayAuthenticationAndStreaming(t *testing.T) {
	backend := &fakeBackend{turn: normal()}
	s := ownedServer(t, t.Context(), backend, nil)
	if !strings.HasPrefix(s.URL(), "http://127.0.0.1:") {
		t.Fatal("gateway did not bind loopback")
	}
	client := directClient(t)
	denied := serverRequest(t, client, s, tokens.UI, false)
	_, _ = io.Copy(io.Discard, denied.Body)
	denied.Body.Close()
	if denied.StatusCode != 401 || backend.starts.Load() != 0 {
		t.Fatal("UI credential reached model execution")
	}
	response := serverRequest(t, client, s, tokens.Model, true)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal("owned server text request failed", err)
	}
	names, _ := events(t, string(data))
	if strings.Join(names, ",") != "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop" {
		t.Fatal("server wrapper changed SSE events")
	}
	serverCount(t, s, 0, 0)
}
func TestOwnedServerBoundsConnectionsBeforeParsing(t *testing.T) {
	s := ownedServer(t, t.Context(), &fakeBackend{turn: normal()}, func(cfg *gateway.ServerConfig) { cfg.MaxConnections = 2; cfg.HeaderTimeout = time.Second })
	first, err := net.DialTimeout("tcp", s.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.DialTimeout("tcp", s.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	serverCount(t, s, 2, 0)
	extra, err := net.DialTimeout("tcp", s.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	_ = extra.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var one [1]byte
	_, err = extra.Read(one[:])
	var timed net.Error
	if err == nil || errors.As(err, &timed) && timed.Timeout() {
		t.Fatal("excess socket was left open or blocked admission")
	}
	if s.Stats().Connections > 2 {
		t.Fatal("socket capacity exceeded")
	}
	first.Close()
	serverCount(t, s, 1, 0)
	response, err := directClient(t).Get(s.URL() + "/health")
	if err != nil {
		t.Fatal("released socket capacity was not reusable", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("reused capacity failed health check")
	}
	second.Close()
	serverCount(t, s, 0, 0)
}
func TestOwnedServerExpiresPartialHeadersAndIdleKeepalive(t *testing.T) {
	s := ownedServer(t, t.Context(), &fakeBackend{turn: normal()}, func(cfg *gateway.ServerConfig) {
		cfg.HeaderTimeout = 80 * time.Millisecond
		cfg.IdleTimeout = 80 * time.Millisecond
	})
	for _, complete := range []bool{false, true} {
		conn, err := net.DialTimeout("tcp", s.Address(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if complete {
			_, err = io.WriteString(conn, "GET /health HTTP/1.1\r\nHost: independent-fixture\r\n\r\n")
			if err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			response, responseErr := http.ReadResponse(reader, nil)
			if responseErr != nil {
				t.Fatal(responseErr)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			_, err = reader.ReadByte()
		} else {
			_, _ = io.WriteString(conn, "GET /health HTTP/1.1\r\nHost:")
			var data [4096]byte
			for {
				_, err = conn.Read(data[:])
				if err != nil {
					break
				}
			}
		}
		conn.Close()
		var timed net.Error
		if err == nil || errors.As(err, &timed) && timed.Timeout() {
			t.Fatal("header/idle socket did not expire before test deadline")
		}
	}
	serverCount(t, s, 0, 0)
}
func TestOwnedServerCancellationJoinsStreamingHandlerAndRepeatedClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	turn := &fakeTurn{steps: []step{{event: normal().steps[0].event}, {wait: true}}}
	s := ownedServer(t, ctx, &fakeBackend{turn: turn}, nil)
	response := serverRequest(t, directClient(t), s, tokens.Model, true)
	serverCount(t, s, 1, 1)
	cancel()
	var joined sync.WaitGroup
	errorsFound := make(chan error, 8)
	for range 8 {
		joined.Go(func() { errorsFound <- s.Close() })
	}
	joined.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	response.Body.Close()
	if turn.canceled.Load() != 1 || turn.finished.Load() != 0 {
		t.Fatal("shutdown reused or abandoned a model turn")
	}
	serverCount(t, s, 0, 0)
	if !s.Stats().Closing {
		t.Fatal("server did not stop admission")
	}
	if conn, err := net.DialTimeout("tcp", s.Address(), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("closed listener accepted a connection")
	}
}
func TestOwnedServerForcesStalledNewConnectionsClosed(t *testing.T) {
	s := ownedServer(t, t.Context(), &fakeBackend{turn: normal()}, func(cfg *gateway.ServerConfig) {
		cfg.HeaderTimeout = 30 * time.Second
		cfg.ShutdownTimeout = 30 * time.Millisecond
	})
	conn, err := net.DialTimeout("tcp", s.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	serverCount(t, s, 1, 0)
	started := time.Now()
	if err := s.Close(); err != nil {
		t.Fatal("forced socket shutdown failed", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("new connection stalled server cleanup")
	}
	serverCount(t, s, 0, 0)
}
func TestOwnedServerRejectsInvalidConfigurationBeforeListening(t *testing.T) {
	base := gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: &fakeBackend{turn: normal()}}}
	for _, mutate := range []func(*gateway.ServerConfig){
		func(c *gateway.ServerConfig) { c.MaxConnections = -1 },
		func(c *gateway.ServerConfig) { c.MaxConnections = 1025 },
		func(c *gateway.ServerConfig) { c.HeaderTimeout = -time.Second },
		func(c *gateway.ServerConfig) { c.JoinTimeout = time.Minute },
		func(c *gateway.ServerConfig) { c.Gateway.Tokens.Model = "secret-sentinel" },
		func(c *gateway.ServerConfig) { c.Address = "0.0.0.0:0" },
	} {
		cfg := base
		mutate(&cfg)
		s, err := gateway.StartServer(t.Context(), cfg)
		if err == nil {
			s.Close()
			t.Fatal("invalid server configuration accepted")
		}
		if strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("server configuration error disclosed a token")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if s, err := gateway.StartServer(ctx, base); !errors.Is(err, context.Canceled) {
		if s != nil {
			s.Close()
		}
		t.Fatal("canceled startup listened")
	}
}

func TestOwnedServerBoundsHeadersAndUnauthorizedBodyDrain(t *testing.T) {
	b := &fakeBackend{turn: normal()}
	s := ownedServer(t, t.Context(), b, func(c *gateway.ServerConfig) { c.Gateway.ReadTimeout = 80 * time.Millisecond })
	r, _ := http.NewRequestWithContext(t.Context(), "GET", s.URL()+"/health", nil)
	r.Header.Set("X-Independent-Fixture", strings.Repeat("h", 128<<10))
	response, err := directClient(t).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatal("oversized headers reached the handler")
	}
	conn, err := net.DialTimeout("tcp", s.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, err = io.WriteString(conn, "POST /v1/messages HTTP/1.1\r\nHost: independent-fixture\r\nContent-Length: 64\r\nConnection: close\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, conn)
	var timed net.Error
	if errors.As(err, &timed) && timed.Timeout() {
		t.Fatal("unauthorized body drain retained a connection")
	}
	if b.starts.Load() != 0 {
		t.Fatal("malformed/unauthorized traffic invoked the backend")
	}
	serverCount(t, s, 0, 0)
}

type stalledCatalog struct {
	*fakeBackend
	entered, release chan struct{}
}

func (b *stalledCatalog) Models(context.Context) ([]inference.Model, error) {
	close(b.entered)
	<-b.release
	return nil, nil
}
func TestOwnedServerReportsHandlerThatDoesNotHonorCancellation(t *testing.T) {
	b := &stalledCatalog{fakeBackend: &fakeBackend{turn: normal()}, entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(b.release) })
	s, err := gateway.StartServer(t.Context(), gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: b}, ShutdownTimeout: 20 * time.Millisecond, JoinTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	finished := make(chan struct{})
	client := directClient(t)
	go func() {
		defer close(finished)
		r, _ := http.NewRequestWithContext(t.Context(), "GET", s.URL()+"/v1/models", nil)
		r.Header.Set("x-api-key", tokens.Model)
		if response, err := client.Do(r); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("independent blocked handler did not start")
	}
	started := time.Now()
	if err := s.Close(); !errors.Is(err, gateway.ErrServerCleanup) {
		t.Fatal("incomplete handler cleanup reported success", err)
	}
	if time.Since(started) > time.Second || s.Stats().Handlers != 1 {
		t.Fatal("uncooperative handler violated bounded shutdown reporting")
	}
	if !errors.Is(s.Close(), gateway.ErrServerCleanup) {
		t.Fatal("repeated Close lost the recorded cleanup failure")
	}
	release.Do(func() { close(b.release) })
	serverCount(t, s, 0, 0)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("fixture HTTP caller did not finish")
	}
}
