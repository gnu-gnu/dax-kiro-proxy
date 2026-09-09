//go:build darwin || linux

package session_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/session"
)

const churnWorkers = 8
const churnBytes = 16 << 10

type churnConversation struct {
	id, input, reply string
	observation      observedPrompt
}

type churnClient struct {
	client     *http.Client
	url, token string
}

func (c churnClient) request(ctx context.Context, state churnConversation, hold string) (*http.Response, error) {
	messages := []map[string]string{{"role": "user", "content": state.input}}
	if hold != "" {
		messages = append(messages, map[string]string{"role": "assistant", "content": state.reply}, map[string]string{"role": "user", "content": hold})
	}
	body, err := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 1024, "stream": hold != "", "messages": messages})
	if err != nil || len(body) > churnBytes {
		return nil, errors.New("churn request bound")
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("churn request construction")
	}
	r.Header.Set("x-api-key", c.token)
	r.Header.Set("x-claude-code-session-id", state.id)
	r.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(r)
	if err != nil {
		return nil, errors.New("churn HTTP request")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("churn HTTP status %d", response.StatusCode)
	}
	return response, nil
}

func (c churnClient) complete(ctx context.Context, state churnConversation) (churnConversation, error) {
	response, err := c.request(ctx, state, "")
	if err != nil {
		return state, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, churnBytes+1))
	var reply struct {
		Type       string
		StopReason string `json:"stop_reason"`
		Content    []struct{ Type, Text string }
	}
	if err != nil || len(body) > churnBytes || json.Unmarshal(body, &reply) != nil || reply.Type != "message" || reply.StopReason != "end_turn" || len(reply.Content) != 1 || reply.Content[0].Type != "text" {
		return state, errors.New("churn buffered response shape")
	}
	state.reply = reply.Content[0].Text
	if json.Unmarshal([]byte(state.reply), &state.observation) != nil || state.observation.PID <= 1 || state.observation.Session == "" || state.observation.Count != 1 || state.observation.Loaded || len(state.observation.Prompt) == 0 || state.observation.Prompt[len(state.observation.Prompt)-1].Text != state.input {
		return state, errors.New("churn initial process observation")
	}
	return state, nil
}

func checkChurnDelta(state churnConversation, text, hold string) error {
	var got observedPrompt
	if len(text) > churnBytes || json.Unmarshal([]byte(text), &got) != nil || got.PID != state.observation.PID || got.Session != state.observation.Session || got.Count != 2 || got.Loaded || len(got.Prompt) != 1 || got.Prompt[0].Text != hold {
		return errors.New("churn continuation changed ownership or replayed history")
	}
	return nil
}

func (c churnClient) stream(ctx context.Context, state churnConversation, hold string, ready chan<- struct{}) error {
	response, err := c.request(ctx, state, hold)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("churn SSE media type")
	}
	scan := bufio.NewScanner(io.LimitReader(response.Body, churnBytes+1))
	scan.Buffer(make([]byte, 4096), churnBytes)
	var text strings.Builder
	started, bytesRead := false, 0
	for scan.Scan() {
		line := scan.Text()
		bytesRead += len(line) + 1
		if bytesRead > churnBytes {
			return errors.New("churn SSE byte bound")
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type  string
			Delta struct {
				Type, Text string
				StopReason *string `json:"stop_reason"`
			}
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			return errors.New("churn SSE JSON")
		}
		switch event.Type {
		case "message_start", "content_block_start", "ping":
		case "content_block_delta":
			if started || event.Delta.Type != "text_delta" || text.Len()+len(event.Delta.Text) > churnBytes {
				return errors.New("churn extra or oversized text")
			}
			text.WriteString(event.Delta.Text)
			if json.Valid([]byte(text.String())) {
				if err := checkChurnDelta(state, text.String(), hold); err != nil {
					return err
				}
				started = true
				ready <- struct{}{}
			}
		default:
			return errors.New("held churn stream completed or failed before cancellation")
		}
	}
	if !started || ctx.Err() != context.Canceled {
		return errors.New("churn stream ended without observed caller cancellation")
	}
	return nil
}

func churnAwait(ctx context.Context, check func() bool) error {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if check() {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("churn episode deadline")
		case <-timer.C:
			return errors.New("churn state failed to settle")
		case <-tick.C:
		}
	}
}

func churnRound(ctx context.Context, client churnClient, m *session.Manager, server *gateway.Server, wave int) ([]int, error) {
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	var states [churnWorkers]churnConversation
	var faults [churnWorkers]error
	var tasks sync.WaitGroup
	for i := range churnWorkers {
		tasks.Go(func() {
			states[i], faults[i] = client.complete(ctx, churnConversation{id: fmt.Sprintf("churn-%d-%d", wave, i), input: fmt.Sprintf("Owned first input %d %d", wave, i)})
		})
	}
	tasks.Wait()
	var pids []int
	seen := map[int]bool{}
	for i := range churnWorkers {
		if faults[i] != nil {
			return pids, faults[i]
		}
		pid := states[i].observation.PID
		group, err := syscall.Getpgid(pid)
		if err != nil || group != pid || seen[pid] {
			return pids, errors.New("churn process ownership not independently observed")
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	if err := churnAwait(ctx, func() bool {
		s := m.Stats()
		return s.Processes == churnWorkers && s.Sessions == churnWorkers && s.Busy == 0 && server.Stats().Handlers == 0
	}); err != nil {
		return pids, err
	}
	ready := make(chan struct{}, churnWorkers)
	finished := make(chan error, churnWorkers)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i := range churnWorkers {
		tasks.Go(func() {
			finished <- client.stream(streamCtx, states[i], fmt.Sprintf("CHURN_HOLD_%d_%d", wave, i), ready)
		})
	}
	var readiness error
	for count := 0; count < churnWorkers && readiness == nil; count++ {
		select {
		case <-ready:
		case <-finished:
			readiness = errors.New("churn stream exited before the cancellation barrier")
		case <-ctx.Done():
			readiness = errors.New("churn stream readiness deadline")
		}
	}
	if readiness == nil {
		readiness = churnAwait(ctx, func() bool {
			s := m.Stats()
			h := server.Stats()
			return s.Processes == churnWorkers && s.Sessions == churnWorkers && s.Busy == churnWorkers && h.Handlers == churnWorkers && h.Connections == churnWorkers
		})
	}
	for range 8 {
		cancel()
	}
	tasks.Wait()
	close(finished)
	for err := range finished {
		readiness = errors.Join(readiness, err)
	}
	if readiness != nil {
		return pids, readiness
	}
	err := churnAwait(ctx, func() bool {
		s := m.Stats()
		h := server.Stats()
		if s != (acppool.Stats{}) || h.Connections != 0 || h.Handlers != 0 {
			return false
		}
		for _, pid := range pids {
			if !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
				return false
			}
		}
		return true
	})
	return pids, err
}

type churnResources struct {
	FDs, Goroutines int
	Heap            uint64
}

func measureChurnResources() (churnResources, error) {
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	path := "/dev/fd"
	if runtime.GOOS == "linux" {
		path = "/proc/self/fd"
	}
	dir, err := os.Open(path)
	if err != nil {
		return churnResources{}, errors.New("churn descriptor enumeration unavailable")
	}
	entries, readErr := dir.ReadDir(4097)
	closeErr := dir.Close()
	if (readErr != nil && readErr != io.EOF) || closeErr != nil || len(entries) > 4096 {
		return churnResources{}, errors.New("churn descriptor enumeration bound")
	}
	return churnResources{FDs: len(entries), Goroutines: runtime.NumGoroutine(), Heap: memory.HeapAlloc}, nil
}

func checkChurnResources(base, next churnResources) error {
	if next.FDs > base.FDs+2 || next.Goroutines > base.Goroutines+16 || next.Heap > base.Heap+(8<<20) {
		return errors.New("churn resources exceeded the fixed post-warmup envelope")
	}
	return nil
}

func TestConcurrentHTTPProcessChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded independent-process churn")
	}
	waves := 8
	if raw := os.Getenv("DAX_FIXTURE_CHURN_WAVES"); raw != "" {
		var err error
		waves, err = strconv.Atoi(raw)
		if err != nil || waves < 8 || waves > 64 {
			t.Fatal("DAX_FIXTURE_CHURN_WAVES must be between 8 and 64")
		}
	}
	ctx, stop := context.WithTimeout(t.Context(), 3*time.Minute)
	defer stop()
	cfg := managerConfig(t, "http-churn")
	cfg.MaxSessions, cfg.Session.TurnTimeout, cfg.Session.SetupTimeout = churnWorkers, 20*time.Second, 5*time.Second
	cfg.Session.Process.Limits.RequestTimeout = cfg.Session.TurnTimeout
	pool, err := acppool.New(acppool.Config{Process: cfg.Session.Process, MaxProcesses: churnWorkers, SessionsPerProcess: 1, MaxIdle: churnWorkers, SetupTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	}()
	cfg.Session.Pool = pool
	m, err := session.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	}()
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	server, err := gateway.StartServer(ctx, gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: m, FirstEventTimeout: 5 * time.Second, TurnTimeout: 20 * time.Second, MaxActiveRequests: churnWorkers, MaxOutputBytes: churnBytes}, MaxConnections: churnWorkers * 2, ShutdownTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	}()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: churnWorkers, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, ResponseHeaderTimeout: 6 * time.Second}
	defer transport.CloseIdleConnections()
	client := churnClient{client: &http.Client{Transport: transport, Timeout: 15 * time.Second}, url: server.URL(), token: tokens.Model}
	var baseline, peak churnResources
	processes := 0
	for wave := 0; wave < waves; wave++ {
		pids, err := churnRound(ctx, client, m, server, wave)
		if err != nil {
			t.Fatalf("churn wave %d: %v", wave+1, err)
		}
		processes += len(pids)
		now, err := measureChurnResources()
		if err != nil {
			t.Fatal(err)
		}
		if wave == 3 {
			baseline, peak = now, now
		}
		if wave >= 4 {
			if err := checkChurnResources(baseline, now); err != nil {
				t.Fatalf("churn wave %d: %v; baseline=%+v observed=%+v", wave+1, err, baseline, now)
			}
			peak.FDs, peak.Goroutines, peak.Heap = max(peak.FDs, now.FDs), max(peak.Goroutines, now.Goroutines), max(peak.Heap, now.Heap)
		}
		if (wave+1)%8 == 0 {
			t.Logf("churn waves=%d requests=%d joined_groups=%d settled=%+v", wave+1, (wave+1)*churnWorkers*2, processes, now)
		}
	}
	for range 4 {
		if err := errors.Join(server.Close(), m.Close(), pool.Close()); err != nil {
			t.Fatal(err)
		}
	}
	if server.Stats().Connections != 0 || server.Stats().Handlers != 0 || m.Stats() != (acppool.Stats{}) {
		t.Fatal("churn final ownership did not join")
	}
	final, err := measureChurnResources()
	if err != nil || checkChurnResources(baseline, final) != nil {
		t.Fatal("churn final resource envelope")
	}
	t.Logf("churn complete waves=%d requests=%d joined_groups=%d baseline=%+v peak=%+v final=%+v", waves, waves*churnWorkers*2, processes, baseline, peak, final)
}
