//go:build darwin || linux

package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/schemacheck"
)

type batchHTTPProcess struct {
	pid, peer int
	config    string
}

func (p batchHTTPProcess) gone() bool {
	_, a := os.Lstat(p.config)
	_, b := os.Lstat(filepath.Dir(p.config))
	return p.pid > 1 && p.peer > 1 && errors.Is(syscall.Kill(-p.pid, 0), syscall.ESRCH) && errors.Is(syscall.Kill(p.peer, 0), syscall.ESRCH) && errors.Is(a, os.ErrNotExist) && errors.Is(b, os.ErrNotExist)
}

// Gate only the consumer's first Next until three real MCP calls are queued. This selects a
// deterministic batch boundary without changing driver/relay transitions or fabricating calls.
type batchHTTPGate struct {
	*Manager
	mu     sync.Mutex
	owners map[string][]batchHTTPProcess
}

func (g *batchHTTPGate) Start(ctx context.Context, request *anthropic.Request) (inference.Turn, error) {
	turn, err := g.Manager.Start(ctx, request)
	if err != nil {
		return nil, err
	}
	d := turn.(*managedTurn).binding.driver
	d.mu.Lock()
	broker, socket, client := d.broker, d.socket, d.client
	d.mu.Unlock()
	if broker == nil || socket == nil || client == nil {
		turn.Cancel()
		return nil, errors.New("owned batch setup missing")
	}
	peer, joined := socket.PeerPID()
	p := batchHTTPProcess{client.PID(), peer, socket.ConfigPath()}
	group, groupErr := syscall.Getpgid(peer)
	if !joined || peer <= 1 || peer == p.pid || groupErr != nil || group != p.pid {
		turn.Cancel()
		return nil, errors.New("owned batch attachment missing")
	}
	g.mu.Lock()
	prior := g.owners[request.Identity.Session]
	if len(prior) == 0 || prior[len(prior)-1].pid != p.pid {
		g.owners[request.Identity.Session] = append(prior, p)
	}
	g.mu.Unlock()
	results, err := request.LatestToolResults()
	if err != nil {
		turn.Cancel()
		return nil, err
	}
	if len(results) == 0 {
		ready, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for broker.Stats().Queued != 3 {
			select {
			case <-ready.Done():
				turn.Cancel()
				return nil, errors.New("owned complete batch readiness deadline")
			case <-broker.Done():
				turn.Cancel()
				return nil, errors.New("owned batch retired before readiness")
			case <-tick.C:
			}
		}
	}
	return turn, nil
}

type batchHTTPState struct {
	id, nonce string
	messages  []any
	uses      []anthropic.ResponseBlock
}

var batchHTTPTools = []json.RawMessage{json.RawMessage(`{"name":"batch_action","input_schema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}`)}

const batchHTTPBytes = 64 << 10

func batchHTTPPost(ctx context.Context, client *http.Client, url, token, model string, state batchHTTPState, streaming bool, expected int) (anthropic.Response, error) {
	payload, err := json.Marshal(map[string]any{"model": model, "max_tokens": 1024, "stream": streaming, "messages": state.messages, "tools": batchHTTPTools})
	if err != nil || len(payload) > batchHTTPBytes {
		return anthropic.Response{}, errors.New("owned batch request bound")
	}
	decoded, err := anthropic.DecodeRequest(payload)
	if err != nil || decoded.ValidateControls() != nil {
		return anthropic.Response{}, errors.New("owned batch input is not valid Messages data")
	}
	r, err := http.NewRequestWithContext(ctx, "POST", url+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return anthropic.Response{}, errors.New("owned batch HTTP construction")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", token)
	r.Header.Set("x-claude-code-session-id", state.id)
	response, err := client.Do(r)
	if err != nil {
		return anthropic.Response{}, errors.New("owned batch HTTP exchange")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, batchHTTPBytes+1))
	if err != nil || len(body) > batchHTTPBytes || response.StatusCode != expected {
		return anthropic.Response{}, fmt.Errorf("owned batch HTTP status or bound: %d", response.StatusCode)
	}
	if expected != 200 {
		return anthropic.Response{}, nil
	}
	if streaming {
		return decodeBatchSSE(body)
	}
	var result anthropic.Response
	if json.Unmarshal(body, &result) != nil || result.Type != "message" || result.StopReason == nil || len(result.Content) == 0 || len(result.Content) > 3 {
		return result, errors.New("owned buffered batch shape")
	}
	return result, nil
}

func decodeBatchSSE(data []byte) (anthropic.Response, error) {
	result := anthropic.Response{Type: "message"}
	var inputs []string
	var closed []bool
	stops := 0
	for _, packet := range bytes.Split(bytes.TrimSpace(data), []byte("\n\n")) {
		var raw []byte
		for _, line := range bytes.Split(packet, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data: ")) {
				raw = line[6:]
			}
		}
		var e struct {
			Type  string
			Index int
			Block anthropic.ResponseBlock `json:"content_block"`
			Delta struct {
				Type, Text string
				Partial    string `json:"partial_json"`
				Stop       string `json:"stop_reason"`
			}
		}
		if json.Unmarshal(raw, &e) != nil {
			return result, errors.New("owned SSE batch frame")
		}
		switch e.Type {
		case "message_start", "ping":
		case "content_block_start":
			if e.Index != len(result.Content) || len(result.Content) == 3 {
				return result, errors.New("owned SSE block count")
			}
			result.Content = append(result.Content, e.Block)
			inputs = append(inputs, "")
			closed = append(closed, false)
		case "content_block_delta", "content_block_stop":
			if e.Index < 0 || e.Index >= len(result.Content) || closed[e.Index] {
				return result, errors.New("owned SSE block order")
			}
			b := &result.Content[e.Index]
			if e.Type == "content_block_stop" {
				closed[e.Index] = true
				if b.Type == "tool_use" {
					if !json.Valid([]byte(inputs[e.Index])) {
						return result, errors.New("owned SSE tool input")
					}
					b.Input = json.RawMessage(inputs[e.Index])
				}
			} else if e.Delta.Type == "text_delta" && b.Type == "text" && b.Text != nil {
				*b.Text += e.Delta.Text
			} else if e.Delta.Type == "input_json_delta" && b.Type == "tool_use" {
				inputs[e.Index] += e.Delta.Partial
			} else {
				return result, errors.New("owned SSE delta kind")
			}
		case "message_delta":
			if e.Delta.Stop == "" || result.StopReason != nil {
				return result, errors.New("owned SSE stop reason")
			}
			result.StopReason = &e.Delta.Stop
		case "message_stop":
			stops++
		default:
			return result, errors.New("owned SSE unexpected event")
		}
	}
	if stops != 1 || result.StopReason == nil || len(closed) == 0 {
		return result, errors.New("owned SSE completion missing")
	}
	for _, done := range closed {
		if !done {
			return result, errors.New("owned SSE unfinished block")
		}
	}
	return result, nil
}

func batchHTTPHandoff(state *batchHTTPState, result anthropic.Response) error {
	if result.StopReason == nil || *result.StopReason != "tool_use" || len(result.Content) != 3 {
		return errors.New("owned HTTP response did not deliver a three-call batch")
	}
	seen := map[int]bool{}
	ids := map[string]bool{}
	for _, b := range result.Content {
		var input struct{ N int }
		if b.Type != "tool_use" || b.Name != "batch_action" || b.ID == "" || ids[b.ID] || json.Unmarshal(b.Input, &input) != nil || input.N < 1 || input.N > 3 || seen[input.N] {
			return errors.New("owned HTTP tool identity or input changed")
		}
		seen[input.N], ids[b.ID] = true, true
	}
	state.uses = result.Content
	state.messages = append(state.messages, map[string]any{"role": "assistant", "content": state.uses})
	return nil
}
func batchHTTPResults(state batchHTTPState, recovery string) batchHTTPState {
	blocks := make([]any, 0, 4)
	for i := len(state.uses) - 1; i >= 0; i-- {
		var input struct{ N int }
		_ = json.Unmarshal(state.uses[i].Input, &input)
		blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": state.uses[i].ID, "content": fmt.Sprintf("%s_%d", state.nonce, input.N), "is_error": recovery != "" || input.N == 2})
	}
	if recovery != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": recovery})
	}
	state.messages = append(append([]any(nil), state.messages...), map[string]any{"role": "user", "content": blocks})
	return state
}

type batchHTTPObservation struct {
	PID, RelayPID, PromptCount, Calls, Errors int
	RelayConfig, Digest                       string
	Prompt                                    []batchHTTPPart
}
type batchHTTPPart struct{ Type, Text string }

func batchHTTPObserved(result anthropic.Response) (batchHTTPObservation, error) {
	var o batchHTTPObservation
	if result.StopReason == nil || *result.StopReason != "end_turn" || len(result.Content) != 1 || result.Content[0].Text == nil || len(*result.Content[0].Text) > batchHTTPBytes || json.Unmarshal([]byte(*result.Content[0].Text), &o) != nil || o.PID <= 1 || o.RelayPID <= 1 || o.PID == o.RelayPID || !filepath.IsAbs(o.RelayConfig) || len(o.RelayConfig) > 256 {
		return o, errors.New("owned final batch observation missing")
	}
	return o, nil
}

func batchRecoveryContentMatches(state batchHTTPState, latest []any, observed batchHTTPObservation, recovery string) bool {
	if observed.PromptCount != 1 || observed.Calls != 0 || len(observed.Prompt) != 10 || observed.Prompt[9].Text != recovery || len(state.uses) != 3 || len(latest) != 4 {
		return false
	}
	for _, part := range observed.Prompt {
		if part.Type != "text" {
			return false
		}
	}
	var history struct {
		System  []string
		History []struct {
			Role    string
			Content []string
		}
	}
	if json.Unmarshal([]byte(observed.Prompt[1].Text), &history) != nil || len(history.System) != 0 || len(history.History) != 2 || history.History[0].Role != "user" || len(history.History[0].Content) != 1 || history.History[0].Content[0] != state.nonce || history.History[1].Role != "assistant" || len(history.History[1].Content) != 3 {
		return false
	}
	for i, use := range state.uses {
		expected, _ := json.Marshal(use)
		if !bytes.Equal(expected, []byte(history.History[1].Content[i])) {
			return false
		}
	}
	for i := range 3 {
		expected, _ := json.Marshal(latest[i])
		if !bytes.Equal(expected, []byte(observed.Prompt[4+2*i].Text)) {
			return false
		}
	}
	return true
}

func TestHTTPConcurrentMultiCallDeliveryAndRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("owned batch root mode")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, Timeout: time.Minute, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal("owned batch builder")
	}
	defer runner.Close()
	cwd, _ := os.Getwd()
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, name := range []string{"GOMODCACHE", "GOCACHE"} {
		if v := os.Getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	fake, executable := filepath.Join(root, "fake"), filepath.Join(root, "relay")
	for _, target := range []struct{ path, source string }{{fake, "../acp/testdata/fake"}, {executable, "../../cmd/dax-kiro-proxy"}} {
		if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", target.path, target.source}}); err != nil {
			t.Fatal("owned batch build failed")
		}
	}
	hash := sha256.Sum256([]byte("fixture-backend"))
	model := fmt.Sprintf("claude-dax-fixture-backend-%x", hash[:8])
	for wave := range 8 {
		t.Run(fmt.Sprintf("wave-%d", wave+1), func(t *testing.T) {
			streaming := wave%2 == 1
			validator, err := schemacheck.New(schemacheck.Config{Executable: executable, Directory: root, MaxWorkers: 2})
			if err != nil {
				t.Fatal("owned batch validator")
			}
			defer validator.Close()
			process := acp.Config{Executable: fake, Args: []string{"batch-relay"}, Directory: root, ClientInfo: acp.Info{Name: "owned-batch-http", Version: "1"}, Limits: acp.Limits{RequestTimeout: 20 * time.Second}}
			// This peer owns one session. Shared-process routing has a different multi-session peer.
			pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 2, SessionsPerProcess: 1, MaxIdle: 2, SetupTimeout: 5 * time.Second})
			if err != nil {
				t.Fatal("owned single-session batch pool")
			}
			defer pool.Close()
			m, err := NewManager(ManagerConfig{ProfileScope: "owned-batch-http", MaxSessions: 2, Session: Config{Process: process, Pool: pool, TurnTimeout: 20 * time.Second, SetupTimeout: 5 * time.Second, Validator: validator, RelayExecutable: executable}})
			if err != nil {
				t.Fatal("owned batch manager")
			}
			defer m.Close()
			g := &batchHTTPGate{Manager: m, owners: map[string][]batchHTTPProcess{}}
			tokens, err := gateway.NewTokens()
			if err != nil {
				t.Fatal("owned batch HTTP tokens")
			}
			server, err := gateway.StartServer(ctx, gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: g, FirstEventTimeout: 6 * time.Second, TurnTimeout: 20 * time.Second, MaxActiveRequests: 2, MaxOutputBytes: batchHTTPBytes}, MaxConnections: 4, ShutdownTimeout: 2 * time.Second})
			if err != nil {
				t.Fatal("owned batch HTTP listener")
			}
			defer server.Close()
			transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 2, ResponseHeaderTimeout: 8 * time.Second}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
			post := func(state batchHTTPState, status int) (anthropic.Response, error) {
				result, err := batchHTTPPost(ctx, client, server.URL(), tokens.Model, model, state, streaming, status)
				if err != nil {
					return result, err
				}
				// The next request is deliberately after complete HTTP handler/connection release.
				settle, stop := context.WithTimeout(ctx, 5*time.Second)
				defer stop()
				tick := time.NewTicker(time.Millisecond)
				defer tick.Stop()
				for server.Stats().Handlers != 0 || server.Stats().Connections != 0 {
					select {
					case <-settle.Done():
						return result, errors.New("owned HTTP response did not release its handler")
					case <-tick.C:
					}
				}
				return result, nil
			}
			var states [2]batchHTTPState
			var replies [2]anthropic.Response
			var faults [2]error
			var tasks sync.WaitGroup
			for i := range 2 {
				states[i] = batchHTTPState{id: fmt.Sprintf("batch-%d-%d", wave, i), nonce: fmt.Sprintf("BATCH_REPLY_%d_%d_0", wave, i)}
				states[i].messages = []any{map[string]string{"role": "user", "content": states[i].nonce}}
				tasks.Go(func() { replies[i], faults[i] = post(states[i], 200) })
			}
			tasks.Wait()
			for i := range 2 {
				if faults[i] != nil || batchHTTPHandoff(&states[i], replies[i]) != nil {
					t.Fatal("concurrent HTTP did not deliver both full batches")
				}
			}
			g.mu.Lock()
			firstA, firstB := g.owners[states[0].id][0], g.owners[states[1].id][0]
			g.mu.Unlock()
			if firstA.pid == firstB.pid || firstA.peer == firstB.peer {
				t.Fatal("HTTP owners shared a relay process")
			}
			bad := states[0]
			bad.uses = states[1].uses
			bad.messages = []any{states[0].messages[0], map[string]any{"role": "assistant", "content": bad.uses}}
			if _, err := post(batchHTTPResults(bad, ""), 400); err != nil {
				t.Fatal("valid foreign-ID history was not rejected")
			}
			if s := m.Stats(); s.Busy != 2 || s.Processes != 2 {
				t.Fatal("foreign result consumed a pending owner")
			}
			recovery := fmt.Sprintf("BATCH_RECOVER_%d", wave)
			resumed := batchHTTPResults(states[1], recovery)
			r, err := post(resumed, 200)
			observed, observeErr := batchHTTPObserved(r)
			if err != nil || observeErr != nil || !firstB.gone() || observed.PID == firstB.pid || observed.RelayPID == firstB.peer || observed.PromptCount != 1 || observed.Calls != 0 || len(observed.Prompt) != 10 || observed.Prompt[9].Text != recovery {
				t.Fatalf("batch recovery post_error=%v observation_ok=%v old_gone=%v new_pid=%v prompt_count=%d calls=%d prompt_parts=%d", err, observeErr == nil, firstB.gone(), observed.PID > 1 && observed.PID != firstB.pid, observed.PromptCount, observed.Calls, len(observed.Prompt))
			}
			latest := resumed.messages[2].(map[string]any)["content"].([]any)
			if !batchRecoveryContentMatches(states[1], latest, observed, recovery) {
				t.Fatal("recovery changed original history, tool/result IDs, order or content")
			}
			for round := range 2 {
				completed := batchHTTPResults(states[0], "")
				r, err := post(completed, 200)
				got, observeErr := batchHTTPObserved(r)
				digest := sha256.Sum256([]byte(strings.Join([]string{states[0].nonce + "_1", states[0].nonce + "_2", states[0].nonce + "_3"}, "\n")))
				if err != nil || observeErr != nil || got.PID != firstA.pid || got.RelayPID != firstA.peer || got.PromptCount != round+1 || got.Calls != 3 || got.Errors != 1 || got.Digest != hex.EncodeToString(digest[:]) || len(got.Prompt) != 1 || got.Prompt[0].Text != states[0].nonce {
					t.Fatal("healthy sibling lost exact results, original prompt or next-turn delta")
				}
				if round == 0 {
					states[0].nonce = fmt.Sprintf("BATCH_REPLY_%d_0_1", wave)
					states[0].messages = append(completed.messages, map[string]any{"role": "assistant", "content": r.Content}, map[string]string{"role": "user", "content": states[0].nonce})
					r, err = post(states[0], 200)
					if err != nil || batchHTTPHandoff(&states[0], r) != nil {
						t.Fatal("healthy sibling could not continue with another complete batch")
					}
				}
			}
			for range 3 {
				if errors.Join(server.Close(), m.Close(), pool.Close()) != nil {
					t.Fatal("multi-call HTTP shutdown failed")
				}
			}
			if m.Stats() != (acppool.Stats{}) || server.Stats().Handlers != 0 || server.Stats().Connections != 0 {
				t.Fatal("multi-call HTTP ownership counts survived shutdown")
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			if len(g.owners[states[0].id]) != 1 || len(g.owners[states[1].id]) != 2 {
				t.Fatal("unexpected batch process recreation")
			}
			for _, owners := range g.owners {
				for _, p := range owners {
					if !p.gone() {
						t.Fatal("recorded batch process or relay artifact survived shutdown")
					}
				}
			}
		})
		if t.Failed() {
			return
		}
	}
	t.Log("waves=8 streaming_waves=4 buffered_waves=4 calls_per_batch=3 successful_roundtrips=48 abandoned_denials=24 recorded_groups_joined=24 recorded_relays_joined=24")
}
