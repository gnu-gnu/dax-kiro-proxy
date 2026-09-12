//go:build darwin || linux

package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/schemacheck"
)

// These invented launch documents exercise prepared-resource ownership, not Kiro policy.
type batchPolicyRecord struct {
	directory, marker string
	owner             batchHTTPProcess
	cleanupCalls      int
	cleaned           bool
}
type batchPolicies struct {
	mu                                sync.Mutex
	root                              string
	records                           map[string]*batchPolicyRecord
	expected                          batchHTTPProcess
	prepared, cleaned, recoveryChecks int
}

func (p *batchPolicies) prepare(ctx context.Context, in LaunchInput) (LaunchResources, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil || in.Registry == nil || len(in.Registry.Tools()) != 1 || in.RelayConfig == "" || len(p.records) >= 6 {
		return LaunchResources{}, errors.New("owned batch preparation input or capacity")
	}
	if p.expected.pid != 0 {
		old := p.records[p.expected.config]
		if old == nil || !old.cleaned || old.cleanupCalls != 1 || !p.expected.gone() {
			return LaunchResources{}, errors.New("owned recovery prepared before old ownership joined")
		}
		p.expected = batchHTTPProcess{}
		p.recoveryChecks++
	}
	dir, err := os.MkdirTemp(p.root, "owned-policy-")
	if err != nil {
		return LaunchResources{}, errors.New("owned policy directory")
	}
	r := &batchPolicyRecord{directory: dir, marker: filepath.Join(dir, "policy.json")}
	p.records[in.RelayConfig] = r
	p.prepared++
	owned := LaunchResources{Directory: dir, Args: []string{r.marker}, RelayAtLaunch: true, Cleanup: func() error {
		p.mu.Lock()
		r.cleanupCalls++
		calls, owner := r.cleanupCalls, r.owner
		p.mu.Unlock()
		// Do not serialize a replacement preparer behind the cleanup being observed.
		if calls != 1 || owner.pid <= 1 || owner.peer <= 1 || !errors.Is(syscall.Kill(-owner.pid, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(owner.peer, 0), syscall.ESRCH) {
			return errors.New("owned policy cleanup preceded group/relay join or repeated")
		}
		if os.RemoveAll(dir) != nil {
			return errors.New("owned policy cleanup")
		}
		p.mu.Lock()
		r.cleaned = true
		p.cleaned++
		p.mu.Unlock()
		return nil
	}}
	data, err := json.Marshal(map[string]any{"aliases": []string{in.Registry.Tools()[0].Alias}, "mcp": map[string]any{"name": "owned_launch_relay", "command": in.RelayExecutable, "args": []string{"relay", "--config", in.RelayConfig}, "env": []any{}}})
	if err != nil || len(data) > 4096 || os.WriteFile(r.marker, data, 0600) != nil {
		return owned, errors.New("owned policy document")
	}
	return owned, ctx.Err()
}

func (p *batchPolicies) observe(owner batchHTTPProcess) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.records[owner.config]
	if r == nil || r.cleaned || (r.owner.pid != 0 && r.owner != owner) {
		return errors.New("owned binding changed prepared policy")
	}
	file, err := os.Lstat(r.marker)
	if err != nil || !file.Mode().IsRegular() || file.Mode().Perm() != 0600 || file.Size() > 4096 {
		return errors.New("owned live policy missing or widened")
	}
	r.owner = owner
	return nil
}

func (p *batchPolicies) beforeRecovery(owner batchHTTPProcess) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.records[owner.config]
	if p.expected.pid != 0 || r == nil || r.cleaned || r.owner != owner {
		return errors.New("owned recovery lost pending policy")
	}
	p.expected = owner
	return nil
}

func (p *batchPolicies) checkpoint(waves, live int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.expected.pid != 0 || p.prepared != waves*3 || p.cleaned != waves*3-live || p.recoveryChecks != waves {
		return errors.New("owned prepared lifecycle counts")
	}
	for path, r := range p.records {
		if r.cleaned {
			_, err := os.Lstat(r.directory)
			if r.cleanupCalls != 1 || !r.owner.gone() || !errors.Is(err, os.ErrNotExist) {
				return errors.New("owned retired policy or relay survived")
			}
			delete(p.records, path)
		} else {
			file, err := os.Lstat(r.marker)
			if r.cleanupCalls != 0 || err != nil || file.Mode().Perm() != 0600 || syscall.Kill(-r.owner.pid, 0) != nil || syscall.Kill(r.owner.peer, 0) != nil {
				return errors.New("owned idle policy or process disappeared")
			}
		}
	}
	if len(p.records) != live {
		return errors.New("owned policy ledger exceeded live bound")
	}
	return nil
}

func batchChurnWaves(raw string) (int, error) {
	if raw == "" {
		return 8, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 8 || n > 1024 {
		return 0, errors.New("DAX_FIXTURE_BATCH_WAVES must be between 8 and 1024")
	}
	return n, nil
}

func TestPreparedConcurrentMultiCallResourceChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded prepared multi-call resource churn")
	}
	waves, err := batchChurnWaves(os.Getenv("DAX_FIXTURE_BATCH_WAVES"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	root, fake, executable := buildBatchHTTPPeers(t, ctx)
	policies := &batchPolicies{root: root, records: make(map[string]*batchPolicyRecord)}
	validator, err := schemacheck.New(schemacheck.Config{Executable: executable, Directory: root, MaxWorkers: 2})
	if err != nil {
		t.Fatal("owned churn validator")
	}
	defer validator.Close()
	process := acp.Config{Executable: fake, Args: []string{"batch-relay-prepared"}, Directory: root, ClientInfo: acp.Info{Name: "owned-batch-churn", Version: "1"}, Limits: acp.Limits{RequestTimeout: 20 * time.Second}}
	pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 2, SessionsPerProcess: 1, MaxIdle: 2, SetupTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal("owned churn pool")
	}
	defer func() {
		if pool.Close() != nil {
			t.Error("owned churn pool cleanup")
		}
	}()
	m, err := NewManager(ManagerConfig{ProfileScope: "owned-batch-churn", MaxSessions: 2, Session: Config{Process: process, Pool: pool, TurnTimeout: 20 * time.Second, SetupTimeout: 5 * time.Second, Validator: validator, RelayExecutable: executable, PrepareLaunch: policies.prepare}})
	if err != nil {
		t.Fatal("owned churn manager")
	}
	defer func() {
		if m.Close() != nil {
			t.Error("owned churn manager cleanup")
		}
	}()
	g := &batchHTTPGate{Manager: m, owners: make(map[string][]batchHTTPProcess), observe: policies.observe, beforeRecovery: func(p batchHTTPProcess) {
		if policies.beforeRecovery(p) != nil {
			t.Fatal("owned pending policy missing before recovery")
		}
	}}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("owned churn tokens")
	}
	server, err := gateway.StartServer(ctx, gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: g, FirstEventTimeout: 6 * time.Second, TurnTimeout: 20 * time.Second, MaxActiveRequests: 2, MaxOutputBytes: batchHTTPBytes}, MaxConnections: 4, ShutdownTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal("owned churn HTTP listener")
	}
	defer func() {
		if server.Close() != nil {
			t.Error("owned churn HTTP cleanup")
		}
	}()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 2, ResponseHeaderTimeout: 8 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	var baseline, peak batchResources
	var prior [3]batchHTTPProcess
	started := time.Now()
	for wave := range waves {
		owners := runBatchHTTPWave(t, ctx, g, server, client, tokens.Model, wave)
		if wave > 0 {
			for _, old := range prior {
				if !old.gone() {
					t.Fatal("next wave retained evicted batch ownership")
				}
			}
		}
		if policies.checkpoint(wave+1, 2) != nil {
			t.Fatal("prepared ownership did not settle after batch wave")
		}
		stats := m.Stats()
		if stats.Processes != 2 || stats.Sessions != 2 || stats.Busy != 0 || stats.Idle != 2 || server.Stats().Handlers != 0 || server.Stats().Connections != 0 {
			t.Fatal("prepared HTTP owner counts did not settle")
		}
		g.mu.Lock()
		g.owners = make(map[string][]batchHTTPProcess)
		g.mu.Unlock()
		prior = owners
		now, err := measureBatchResources()
		if err != nil {
			t.Fatal(err)
		}
		if wave == 3 {
			baseline, peak = now, now
		}
		if wave >= 4 {
			if !withinBatchResources(baseline, now) {
				t.Fatalf("batch resource envelope baseline=%+v observed=%+v", baseline, now)
			}
			peak = batchResources{max(peak.FDs, now.FDs), max(peak.Goroutines, now.Goroutines), max(peak.Heap, now.Heap)}
		}
		if (wave+1)%8 == 0 {
			t.Logf("batch_churn waves=%d completed_calls=%d abandoned_calls=%d joined_groups=%d joined_relays=%d live_policies=2 resources=%+v", wave+1, (wave+1)*6, (wave+1)*3, (wave+1)*3-2, (wave+1)*3-2, now)
		}
	}
	for range 3 {
		if errors.Join(server.Close(), m.Close(), pool.Close()) != nil {
			t.Fatal("prepared repeated final shutdown")
		}
	}
	validator.Close()
	if policies.checkpoint(waves, 0) != nil || m.Stats() != (acppool.Stats{}) || server.Stats().Handlers != 0 || server.Stats().Connections != 0 || validator.Stats().Active != 0 || validator.Stats().Idle != 0 {
		t.Fatal("prepared final ownership did not join")
	}
	for _, old := range prior {
		if !old.gone() {
			t.Fatal("final batch process or relay survived")
		}
	}
	final, err := measureBatchResources()
	if err != nil || !withinBatchResources(baseline, final) {
		t.Fatal("prepared final resource envelope")
	}
	t.Logf("batch_churn complete waves=%d joined_groups=%d joined_relays=%d prepared_cleanups=%d recovery_order_checks=%d elapsed_ms=%d baseline=%+v peak=%+v final=%+v", waves, waves*3, waves*3, waves*3, waves, time.Since(started).Milliseconds(), baseline, peak, final)
}

func TestBatchChurnWaveBounds(t *testing.T) {
	for _, value := range []string{"", "8", "1024", "7", "1025", "bad"} {
		n, err := batchChurnWaves(value)
		valid := value == "" || value == "8" || value == "1024"
		if (err == nil) != valid || (valid && (n < 8 || n > 1024)) {
			t.Fatalf("batch wave bound validity=%v", valid)
		}
	}
}
