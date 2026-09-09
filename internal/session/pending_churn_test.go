//go:build darwin || linux

package session_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

type pendingObservation struct {
	observedPrompt
	RelayPID    int
	RelayConfig string
}

type pendingHistory struct {
	id, first, question string
	blocks              []anthropic.ResponseBlock
	old, fresh          pendingObservation
}

func pendingHTTP(ctx context.Context, client churnClient, state pendingHistory, resultID string, expectedStatus int) (anthropic.Response, error) {
	messages := []map[string]any{{"role": "user", "content": state.first}}
	if resultID != "" {
		blocks := append([]anthropic.ResponseBlock(nil), state.blocks...)
		if len(blocks) != 2 {
			return anthropic.Response{}, errors.New("pending churn missing original handoff")
		}
		// The foreign-owner control is internally paired valid Messages data. Rejection must
		// preserve the real owner's history/batch rather than rely on an unmatched wire ID.
		blocks[1].ID = resultID
		messages = append(messages, map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": resultID, "is_error": true, "content": "owned synthetic denial"}, {"type": "text", "text": state.question}}})
	}
	payload, _ := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 1024, "messages": messages, "tools": []any{map[string]any{"name": "client_action", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]string{"type": "integer"}}, "required": []string{"n"}}}}})
	if len(payload) > churnBytes {
		return anthropic.Response{}, errors.New("pending churn request bound")
	}
	if expectedStatus == http.StatusBadRequest {
		decoded, err := anthropic.DecodeRequest(payload)
		if err != nil || decoded.ValidateControls() != nil {
			return anthropic.Response{}, errors.New("foreign-owner control is not valid Messages data")
		}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, client.url+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return anthropic.Response{}, errors.New("pending churn request construction")
	}
	r.Header.Set("x-api-key", client.token)
	r.Header.Set("x-claude-code-session-id", state.id)
	r.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(r)
	if err != nil {
		return anthropic.Response{}, errors.New("pending churn HTTP exchange")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, churnBytes+1))
	if err != nil || len(body) > churnBytes || response.StatusCode != expectedStatus {
		return anthropic.Response{}, fmt.Errorf("pending churn response status/bound: %d", response.StatusCode)
	}
	if expectedStatus != http.StatusOK {
		return anthropic.Response{}, nil
	}
	var result anthropic.Response
	if json.Unmarshal(body, &result) != nil || result.Type != "message" || result.StopReason == nil || len(result.Content) == 0 || len(result.Content) > 2 {
		return result, errors.New("pending churn response shape")
	}
	return result, nil
}

func decodePendingObservation(block anthropic.ResponseBlock) (pendingObservation, error) {
	var got pendingObservation
	if block.Type != "text" || block.Text == nil || len(*block.Text) > churnBytes || json.Unmarshal([]byte(*block.Text), &got) != nil || got.PID <= 1 || got.RelayPID <= 1 || got.PID == got.RelayPID || got.Count != 1 || got.Loaded || got.Session == "" || len(got.RelayConfig) > 256 || !filepath.IsAbs(got.RelayConfig) || filepath.Base(got.RelayConfig) != "relay.json" || !strings.HasPrefix(filepath.Base(filepath.Dir(got.RelayConfig)), "dax-r-") {
		return got, errors.New("pending churn ownership observation")
	}
	return got, nil
}

func pendingOwnershipPresent(p pendingObservation) bool {
	group, err := syscall.Getpgid(p.PID)
	relayGroup, relayErr := syscall.Getpgid(p.RelayPID)
	file, fileErr := os.Lstat(p.RelayConfig)
	return err == nil && relayErr == nil && group == p.PID && relayGroup == p.PID && fileErr == nil && file.Mode().IsRegular() && file.Mode().Perm() == 0600
}

func pendingOwnershipGone(p pendingObservation) bool {
	_, configErr := os.Lstat(p.RelayConfig)
	_, directoryErr := os.Lstat(filepath.Dir(p.RelayConfig))
	return errors.Is(syscall.Kill(p.PID, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-p.PID, 0), syscall.ESRCH) && errors.Is(syscall.Kill(p.RelayPID, 0), syscall.ESRCH) && os.IsNotExist(configErr) && os.IsNotExist(directoryErr)
}

func checkPendingRecovery(state pendingHistory, next pendingObservation) error {
	if next.PID == state.old.PID || next.RelayPID == state.old.RelayPID || next.RelayConfig == state.old.RelayConfig || len(next.Prompt) != 6 || next.Prompt[5].Text != state.question {
		return errors.New("pending recovery did not create fresh full-history ownership")
	}
	var contextData struct {
		System  []string
		History []struct {
			Role    string
			Content []string
		}
	}
	if json.Unmarshal([]byte(next.Prompt[1].Text), &contextData) != nil || len(contextData.System) != 0 || len(contextData.History) != 2 || contextData.History[0].Role != "user" || len(contextData.History[0].Content) != 1 || contextData.History[0].Content[0] != state.first || contextData.History[1].Role != "assistant" || len(contextData.History[1].Content) != 2 || contextData.History[1].Content[0] != *state.blocks[0].Text {
		return errors.New("pending recovery changed original history")
	}
	var use anthropic.ResponseBlock
	var result struct {
		Type, Content string
		ID            string `json:"tool_use_id"`
		IsError       bool   `json:"is_error"`
	}
	if json.Unmarshal([]byte(contextData.History[1].Content[1]), &use) != nil || use.Type != "tool_use" || use.ID != state.blocks[1].ID || use.Name != "client_action" || !bytes.Equal(bytes.TrimSpace(use.Input), []byte(`{"n":1}`)) || json.Unmarshal([]byte(next.Prompt[4].Text), &result) != nil || result.Type != "tool_result" || result.ID != use.ID || !result.IsError || result.Content != "owned synthetic denial" {
		return errors.New("pending recovery changed tool ownership or denial")
	}
	return nil
}

func pendingRound(ctx context.Context, client churnClient, m *session.Manager, server *gateway.Server, wave int, previous *[]pendingObservation) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var states [churnWorkers]pendingHistory
	var faults [churnWorkers]error
	var tasks sync.WaitGroup
	for i := range churnWorkers {
		states[i] = pendingHistory{id: fmt.Sprintf("pending-%d-%d", wave, i), first: fmt.Sprintf("PENDING_START_%d_%d", wave, i), question: fmt.Sprintf("PENDING_RECOVER_%d_%d", wave, i)}
		tasks.Go(func() {
			r, err := pendingHTTP(ctx, client, states[i], "", http.StatusOK)
			if err != nil {
				faults[i] = err
				return
			}
			if *r.StopReason != "tool_use" || len(r.Content) != 2 || r.Content[1].Type != "tool_use" || r.Content[1].ID == "" || r.Content[1].Name != "client_action" || string(r.Content[1].Input) != `{"n":1}` {
				faults[i] = errors.New("pending churn did not deliver its exact inert tool")
				return
			}
			states[i].blocks = r.Content
			states[i].old, faults[i] = decodePendingObservation(r.Content[0])
			if faults[i] == nil && (len(states[i].old.Prompt) != 1 || states[i].old.Prompt[0].Text != states[i].first) {
				faults[i] = errors.New("pending churn original question mismatch")
			}
		})
	}
	tasks.Wait()
	identities := make(map[string]bool)
	for i := range churnWorkers {
		if faults[i] != nil {
			return faults[i]
		}
		for _, key := range []string{states[i].blocks[1].ID, strconv.Itoa(states[i].old.PID), strconv.Itoa(states[i].old.RelayPID), states[i].old.RelayConfig} {
			if identities[key] {
				return errors.New("pending churn shared distinct tool/process/relay ownership")
			}
			identities[key] = true
		}
	}
	for _, prior := range *previous {
		if !pendingOwnershipGone(prior) {
			return errors.New("next pending wave retained evicted recovery ownership")
		}
	}
	waiting := func() bool {
		pool, http := m.Stats(), server.Stats()
		if pool.Processes != churnWorkers || pool.Sessions != churnWorkers || pool.Busy != churnWorkers || http.Connections != 0 || http.Handlers != 0 {
			return false
		}
		for _, state := range states {
			if !pendingOwnershipPresent(state.old) {
				return false
			}
		}
		return true
	}
	if err := churnAwait(ctx, waiting); err != nil {
		return err
	}
	// Wrong ownership must reject without consuming any of the eight suspended batches.
	if _, err := pendingHTTP(ctx, client, states[0], states[1].blocks[1].ID, http.StatusBadRequest); err != nil {
		return err
	}
	if err := churnAwait(ctx, waiting); err != nil {
		return err
	}
	for i := range churnWorkers {
		tasks.Go(func() {
			r, err := pendingHTTP(ctx, client, states[i], states[i].blocks[1].ID, http.StatusOK)
			if err != nil {
				faults[i] = err
				return
			}
			if *r.StopReason != "end_turn" || len(r.Content) != 1 {
				faults[i] = errors.New("pending recovery replayed a tool or failed to complete")
				return
			}
			next, err := decodePendingObservation(r.Content[0])
			if err == nil {
				err = checkPendingRecovery(states[i], next)
			}
			if err == nil && (!pendingOwnershipGone(states[i].old) || !pendingOwnershipPresent(next)) {
				err = errors.New("pending recovery ownership or relay cleanup missing")
			}
			states[i].fresh, faults[i] = next, err
		})
	}
	tasks.Wait()
	for _, err := range faults {
		if err != nil {
			return err
		}
	}
	err := churnAwait(ctx, func() bool {
		if server.Stats().Handlers != 0 || server.Stats().Connections != 0 {
			return false
		}
		if stats := m.Stats(); stats.Processes != churnWorkers || stats.Sessions != churnWorkers || stats.Busy != 0 || stats.Idle != churnWorkers {
			return false
		}
		for _, state := range states {
			if !pendingOwnershipPresent(state.fresh) || !pendingOwnershipGone(state.old) {
				return false
			}
		}
		return true
	})
	if err == nil {
		*previous = (*previous)[:0]
		for _, state := range states {
			*previous = append(*previous, state.fresh)
		}
	}
	return err
}

func TestConcurrentPendingToolDenialChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded concurrent pending-tool churn")
	}
	waves := 8
	if raw := os.Getenv("DAX_FIXTURE_PENDING_WAVES"); raw != "" {
		var err error
		waves, err = strconv.Atoi(raw)
		if err != nil || waves < 8 || waves > 32 {
			t.Fatal("DAX_FIXTURE_PENDING_WAVES must be between 8 and 32")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	validator, err := schemacheck.New(schemacheck.Config{Executable: relayBinary, Directory: t.TempDir(), MaxWorkers: churnWorkers})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Close()
	cfg := managerConfig(t, "pending-churn")
	cfg.MaxSessions = churnWorkers
	cfg.Session.TurnTimeout, cfg.Session.SetupTimeout = 25*time.Second, 5*time.Second
	cfg.Session.Process.Limits.RequestTimeout = cfg.Session.TurnTimeout
	cfg.Session.Validator, cfg.Session.RelayExecutable = validator, relayBinary
	cfg.Session.RelayLimits = relay.Limits{ToolTimeout: 20 * time.Second}
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
	server, err := gateway.StartServer(ctx, gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: m, FirstEventTimeout: 6 * time.Second, TurnTimeout: 25 * time.Second, MaxActiveRequests: churnWorkers, MaxOutputBytes: churnBytes}, MaxConnections: churnWorkers * 2, ShutdownTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	}()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: churnWorkers, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, ResponseHeaderTimeout: 7 * time.Second}
	defer transport.CloseIdleConnections()
	client := churnClient{client: &http.Client{Transport: transport, Timeout: 20 * time.Second}, url: server.URL(), token: tokens.Model}
	var baseline, peak churnResources
	var previous []pendingObservation
	for wave := 0; wave < waves; wave++ {
		if err := pendingRound(ctx, client, m, server, wave, &previous); err != nil {
			t.Fatalf("pending churn wave %d: %v", wave+1, err)
		}
		now, err := measureChurnResources()
		if err != nil {
			t.Fatal(err)
		}
		if wave == 3 {
			baseline, peak = now, now
		}
		if wave >= 4 {
			if err := checkChurnResources(baseline, now); err != nil {
				t.Fatalf("pending churn resources: %v; baseline=%+v observed=%+v", err, baseline, now)
			}
			peak.FDs, peak.Goroutines, peak.Heap = max(peak.FDs, now.FDs), max(peak.Goroutines, now.Goroutines), max(peak.Heap, now.Heap)
		}
		if (wave+1)%8 == 0 {
			t.Logf("pending churn waves=%d requests=%d denied_batches=%d joined_groups=%d joined_relays=%d idle_groups=%d resources=%+v", wave+1, (wave+1)*17, (wave+1)*churnWorkers, (wave+1)*churnWorkers*2-churnWorkers, (wave+1)*churnWorkers*2-churnWorkers, churnWorkers, now)
		}
	}
	for range 4 {
		if err := errors.Join(server.Close(), m.Close(), pool.Close()); err != nil {
			t.Fatal(err)
		}
	}
	validator.Close()
	for _, ownership := range previous {
		if !pendingOwnershipGone(ownership) {
			t.Fatal("final pending churn ownership did not join")
		}
	}
	if m.Stats() != (acppool.Stats{}) || server.Stats().Handlers != 0 || server.Stats().Connections != 0 || validator.Stats().Active != 0 || validator.Stats().Idle != 0 {
		t.Fatal("final pending churn owner counts did not clear")
	}
	final, err := measureChurnResources()
	if err != nil || checkChurnResources(baseline, final) != nil {
		t.Fatal("pending churn final resource envelope")
	}
	t.Logf("pending churn complete waves=%d joined_groups=%d joined_relays=%d baseline=%+v peak=%+v final=%+v", waves, waves*churnWorkers*2, waves*churnWorkers*2, baseline, peak, final)
}
