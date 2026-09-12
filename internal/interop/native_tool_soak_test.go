//go:build darwin || linux

package interop_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

type nativeToolWitness struct {
	PID, Peer, Lane, Prompts, Calls, Results int
	Config                                   string
}
type nativeToolRound struct {
	Seen    [2]bool
	Release chan struct{}
}
type nativeToolBackend struct {
	manager                       *session.Manager
	models                        *catalog.Catalog
	mu                            sync.Mutex
	ledger                        nativeToolLedger
	path                          string
	rounds                        []nativeToolRound
	owners                        [2]nativeToolWitness
	clients                       [2]int
	delivered, finished, canceled [2]int
	finalReady                    chan struct{}
	samples                       []nativeToolResources
	started, ended                time.Time
	failure                       error
	cancel                        context.CancelFunc
	live                          func(int, int) bool
}

func (b *nativeToolBackend) Models(context.Context) ([]inference.Model, error) {
	return b.models.List(), nil
}
func (b *nativeToolBackend) fail(err error) error {
	b.mu.Lock()
	if b.failure == nil {
		b.failure = err
	}
	b.mu.Unlock()
	b.cancel()
	return inference.ErrRequest
}
func (b *nativeToolBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	b.mu.Lock()
	lane, err := b.ledger.request(r)
	b.mu.Unlock()
	if err != nil {
		return nil, b.fail(err)
	}
	turn, err := b.manager.Start(ctx, r)
	if err != nil {
		return nil, b.fail(errors.New("owned manager did not admit exact native continuation"))
	}
	return &nativeToolTurn{Turn: turn, backend: b, lane: lane}, nil
}

func readNativeToolWitness(path string) (nativeToolWitness, error) {
	var w nativeToolWitness
	file, err := os.Open(path)
	if err != nil {
		return w, errors.New("owned native witness missing")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > 4096 || json.Unmarshal(raw, &w) != nil || w.PID <= 1 || w.Peer <= 1 || w.PID == w.Peer || w.Config == "" || w.Prompts != 1 || w.Lane < 0 || w.Lane > 1 {
		return w, errors.New("owned native witness invalid")
	}
	return w, nil
}
func nativeToolLive(pid, group int) bool {
	got, err := syscall.Getpgid(pid)
	return pid > 1 && group > 1 && err == nil && got == group && syscall.Kill(pid, 0) == nil
}
func (b *nativeToolBackend) tools(ctx context.Context, lane int, calls []anthropic.ToolUse) (int, error) {
	b.mu.Lock()
	round, err := b.ledger.tools(lane, calls)
	if err == nil {
		var w nativeToolWitness
		w, err = readNativeToolWitness(filepath.Join(b.path, fmt.Sprintf("owner-%d.json", lane)))
		if err == nil && (w.Lane != lane || w.Calls != round || w.Results != round-1 || !b.live(w.PID, w.PID) || !b.live(w.Peer, w.PID)) {
			err = errors.New("native tool reached HTTP without its live ACP and relay")
		}
		old := b.owners[lane]
		if err == nil && old.PID != 0 && (old.PID != w.PID || old.Peer != w.Peer || old.Config != w.Config) {
			err = errors.New("native sequence replaced its ACP prompt owner")
		}
		if err == nil {
			b.owners[lane] = w
		}
	}
	if err != nil {
		b.mu.Unlock()
		return 0, b.fail(err)
	}
	r := &b.rounds[round-1]
	if r.Seen[lane] {
		b.mu.Unlock()
		return 0, b.fail(errors.New("native tool entered its overlap barrier twice"))
	}
	r.Seen[lane] = true
	if r.Seen[0] && r.Seen[1] {
		if round == 1 {
			b.started = time.Now()
		}
		live := b.live(b.clients[0], b.clients[0]) && b.live(b.clients[1], b.clients[1])
		for _, owner := range b.owners {
			live = live && b.live(owner.PID, owner.PID) && b.live(owner.Peer, owner.PID)
		}
		if !live || b.clients[0] == b.clients[1] || b.owners[0].PID == b.owners[1].PID || b.owners[0].Peer == b.owners[1].Peer || b.owners[0].Config == b.owners[1].Config {
			err = errors.New("native overlap lacked distinct live client and backend owners")
		} else if round >= 4 {
			var observed nativeToolResources
			observed, err = observeNativeToolResources()
			if err == nil && len(b.samples) > 0 && !nativeToolResourcesWithin(b.samples[0], observed) {
				err = errors.New("native tool sequence exceeded its warm resource envelope")
			}
			if err == nil {
				b.samples = append(b.samples, observed)
			}
		}
		if err == nil {
			if round == b.ledger.Rounds {
				b.ended = time.Now()
				close(b.finalReady)
			} else {
				close(r.Release)
			}
		}
	}
	release := r.Release
	b.mu.Unlock()
	if err != nil {
		return 0, b.fail(err)
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-release:
		return round, nil
	}
}

type nativeToolTurn struct {
	inference.Turn
	backend         *nativeToolBackend
	lane, toolRound int
	stop            string
	once            sync.Once
}

func (t *nativeToolTurn) Next(ctx context.Context) (inference.Event, error) {
	e, err := t.Turn.Next(ctx)
	if err == nil && e.Kind == inference.Tools {
		t.toolRound, err = t.backend.tools(ctx, t.lane, e.Tools)
	}
	if err == nil && e.Kind == inference.End {
		t.stop = e.StopReason
		if e.StopReason == "end_turn" {
			t.backend.mu.Lock()
			state := t.backend.ledger.Lanes[t.lane]
			complete := state.Results == t.backend.ledger.Rounds && state.Pending == "" && t.lane == 1
			t.backend.mu.Unlock()
			if !complete {
				err = t.backend.fail(errors.New("native final response preceded its exact result set"))
			}
		}
	}
	return e, err
}
func (t *nativeToolTurn) Finish() {
	t.once.Do(func() {
		t.Turn.Finish()
		t.backend.mu.Lock()
		defer t.backend.mu.Unlock()
		if t.stop == "tool_use" && t.toolRound > 0 {
			t.backend.delivered[t.lane]++
		} else if t.stop == "end_turn" {
			t.backend.finished[t.lane]++
		}
	})
}
func (t *nativeToolTurn) Cancel() {
	t.once.Do(func() { t.Turn.Cancel(); t.backend.mu.Lock(); t.backend.canceled[t.lane]++; t.backend.mu.Unlock() })
}

func nativeToolOwnersGone(w nativeToolWitness) bool {
	if !errors.Is(syscall.Kill(-w.PID, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(w.Peer, 0), syscall.ESRCH) {
		return false
	}
	_, err := os.Lstat(filepath.Dir(w.Config))
	return errors.Is(err, os.ErrNotExist)
}

func (b *nativeToolBackend) waitCanceled(ctx context.Context, lane int, gone func(nativeToolWitness) bool) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		b.mu.Lock()
		owner := b.owners[lane]
		joined := b.canceled[lane] == 1
		b.mu.Unlock()
		if joined && gone(owner) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type nativeToolCapture struct {
	data []byte
	err  error
}
type nativeToolClient struct {
	profile *launcher.ClientProfile
	owner   *childproc.Attached
	process *childproc.AttachedProcess
	output  *os.File
	done    chan nativeToolCapture
}

func (c *nativeToolClient) capture() nativeToolCapture {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case result := <-c.done:
		c.done = nil
		return result
	case <-timer.C:
		_ = c.output.Close()
		result := <-c.done
		c.done = nil
		result.err = errors.New("native output drain did not join")
		return result
	}
}

func (c *nativeToolClient) close() {
	if c.owner != nil {
		c.owner.Close()
	}
	if c.output != nil {
		_ = c.output.Close()
	}
	if c.done != nil {
		<-c.done
		c.done = nil
	}
	if c.profile != nil {
		_ = c.profile.Close()
	}
}

// One actual-client invocation, two independent native conversations, one shared gateway/manager.
// The final approved Read stays undisclosed while that client is canceled; its sibling completes.
func TestClaudeConcurrentNativeToolSoak(t *testing.T) {
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set measured Claude for concurrent native tool rounds with independent ACP; no model credits")
	}
	rounds, err := nativeToolRounds(os.Getenv("DAX_INTEROP_TOOL_SOAK_ROUNDS"))
	if err != nil {
		t.Fatal("invalid native tool round bound")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("/private/tmp", "dax-native-tool-soak-")
	if err != nil {
		t.Fatal("owned native tool root")
	}
	defer os.RemoveAll(root)
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute, MaxProcesses: 1, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal("owned native tool builder")
	}
	defer runner.Close()
	fake := buildDenialACPFixture(t, ctx, runner, root)
	executable := filepath.Join(root, "owned-relay")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("owned native source root")
	}
	buildEnv := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			buildEnv = append(buildEnv, key+"="+value)
		}
	}
	if _, err = runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: buildEnv, Args: []string{"build", "-o", executable, "../../cmd/dax-kiro-proxy"}}); err != nil {
		t.Fatal("owned native relay build")
	}
	version, err := runner.Run(ctx, childproc.Command{Executable: client, Directory: root, Environment: []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "DISABLE_AUTOUPDATER=1"}, Args: []string{"--version"}})
	measured, ok := launcher.ClientVersionFromOutput(version.Stdout)
	if err != nil || !ok || measured != launcher.SupportedClientVersion {
		t.Fatal("native tool soak requires the measured client")
	}
	models, err := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent native tools"}}, "fixture-backend")
	if err != nil {
		t.Fatal("native tool catalog")
	}
	model, _ := models.ClientID("fixture-backend")
	b := &nativeToolBackend{models: models, path: filepath.Join(root, "witness"), finalReady: make(chan struct{}), cancel: cancel, live: nativeToolLive}
	b.ledger.Rounds = rounds
	project := filepath.Join(root, "project")
	for _, dir := range []string{project, b.path} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("owned native tool directories")
		}
	}
	var homes, settings, globals [2]string
	var beforeSettings, beforeGlobals [2][32]byte
	for lane := range 2 {
		homes[lane] = filepath.Join(root, fmt.Sprintf("home-%d", lane))
		memory := filepath.Join(homes[lane], ".claude")
		dataDir := filepath.Join(project, fmt.Sprintf("lane-%d", lane))
		for _, dir := range []string{homes[lane], memory, dataDir} {
			if os.Mkdir(dir, 0700) != nil {
				t.Fatal("owned native sources")
			}
		}
		b.ledger.Lanes[lane] = nativeToolLane{Identity: fmt.Sprintf("13900000-0000-4000-8000-%012d", lane+1), Seed: "NATIVE_" + rand.Text(), Directory: dataDir, IDs: map[string]bool{}}
		settings[lane], globals[lane] = filepath.Join(memory, "settings.json"), filepath.Join(homes[lane], ".claude.json")
		policy := map[string]any{"autoMemoryEnabled": false, "permissions": map[string]any{"allow": []string{"Read"}, "deny": []string{"Write", "Edit", "Bash"}}}
		if lane == 1 {
			policy["hooks"] = map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": `printf '%s' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"independent native refusal"}}'`, "timeout": 2}}}}}
		}
		encoded, _ := json.Marshal(policy)
		if os.WriteFile(settings[lane], encoded, 0600) != nil || os.WriteFile(globals[lane], []byte(`{}`), 0600) != nil {
			t.Fatal("owned native source policy")
		}
		beforeSettings[lane], beforeGlobals[lane] = fileFingerprint(t, settings[lane]), fileFingerprint(t, globals[lane])
		for round := 1; round <= rounds; round++ {
			if os.WriteFile(nativeToolPath(b.ledger.Lanes[lane], round), []byte(nativeToolCanary(b.ledger.Lanes[lane], round)+"\n"), 0600) != nil {
				t.Fatal("owned Read canary")
			}
		}
	}
	for range rounds {
		b.rounds = append(b.rounds, nativeToolRound{Release: make(chan struct{})})
	}
	manifest := filepath.Join(root, "plan.json")
	plan, _ := json.Marshal(map[string]any{"Rounds": rounds, "Witness": b.path, "Lanes": b.ledger.Lanes})
	if os.WriteFile(manifest, plan, 0600) != nil {
		t.Fatal("owned native peer plan")
	}
	validator, err := schemacheck.New(schemacheck.Config{Executable: executable, Directory: root, MaxWorkers: 2})
	if err != nil {
		t.Fatal("native schema worker")
	}
	defer validator.Close()
	process := acp.Config{Executable: fake, Args: []string{"native-tool-soak", manifest}, Directory: project, ClientInfo: acp.Info{Name: "independent-native-soak", Version: "1"}, Limits: acp.Limits{RequestTimeout: 8 * time.Minute}}
	pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 2, SessionsPerProcess: 1, MaxIdle: 2, SetupTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal("native tool pool")
	}
	defer func() {
		if pool.Close() != nil {
			t.Error("native pool cleanup")
		}
	}()
	b.manager, err = session.NewManager(session.ManagerConfig{ProfileScope: "independent-native-tool-soak", MaxSessions: 2, Session: session.Config{Process: process, Pool: pool, Validator: validator, RelayExecutable: executable, TurnTimeout: 8 * time.Minute, RelayLimits: relay.Limits{ToolTimeout: time.Minute}, SetupTimeout: 10 * time.Second}})
	if err != nil {
		t.Fatal("native tool manager")
	}
	defer func() {
		if b.manager.Close() != nil {
			t.Error("native manager cleanup")
		}
	}()
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("native gateway credentials")
	}
	server, err := gateway.StartServer(ctx, gateway.ServerConfig{Gateway: gateway.Config{Tokens: tokens, Backend: b, TurnTimeout: 8 * time.Minute, FirstEventTimeout: 20 * time.Second, MaxActiveRequests: 4, MaxOutputBytes: 128 << 10}, MaxConnections: 8, ShutdownTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal("shared native gateway")
	}
	defer server.Close()
	baseline, err := observeNativeToolResources()
	if err != nil {
		t.Fatal("native resource baseline")
	}
	var clients [2]*nativeToolClient
	defer func() {
		for _, c := range clients {
			if c != nil {
				c.close()
			}
		}
	}()
	for lane := range 2 {
		c := &nativeToolClient{}
		clients[lane] = c
		c.profile, err = launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: homes[lane], Project: project, UserSettings: settings[lane], Executable: client, Version: measured, Model: model, GatewayURL: server.URL(), ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
		if err != nil {
			t.Fatal("native client profile")
		}
		command := c.profile.Command()
		command.Args = append(command.Args, "--print", "--output-format", "json", "--session-id", b.ledger.Lanes[lane].Identity, "--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--tools", "Read", "--system-prompt", "Independent local protocol exercise.", "Follow the owned tool sequence for "+b.ledger.Lanes[lane].Seed+" and finish.")
		c.owner, err = childproc.NewAttached(childproc.AttachedConfig{Lifetime: 8 * time.Minute})
		if err != nil {
			t.Fatal("native attached owner")
		}
		null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			t.Fatal("native owned null descriptor")
		}
		out, write, err := os.Pipe()
		if err != nil {
			null.Close()
			t.Fatal("native output pipe")
		}
		c.output, c.done = out, make(chan nativeToolCapture, 1)
		go func() {
			data, err := io.ReadAll(io.LimitReader(out, (1<<20)+1))
			if len(data) > 1<<20 {
				cancel()
				err = errors.New("native output bound")
			}
			c.done <- nativeToolCapture{data, err}
		}()
		c.process, err = c.owner.Start(ctx, command, childproc.AttachedIO{Stdin: null, Stdout: write, Stderr: null})
		write.Close()
		null.Close()
		if err != nil {
			t.Fatal("native client start")
		}
		b.mu.Lock()
		b.clients[lane] = c.process.PID()
		b.mu.Unlock()
	}
	select {
	case <-b.finalReady:
	case <-clients[0].process.Done():
		result, _ := clients[0].process.Wait()
		t.Logf("early_client_lane=0 exit=%d", result.ExitCode)
		t.Fatal("native client exited before the final joint tool wait")
	case <-clients[1].process.Done():
		result, _ := clients[1].process.Wait()
		t.Logf("early_client_lane=1 exit=%d", result.ExitCode)
		t.Fatal("native client exited before the final joint tool wait")
	case <-ctx.Done():
		b.mu.Lock()
		t.Logf("requests=%d/%d issued=%d/%d results=%d/%d observer_failed=%t", b.ledger.Lanes[0].Requests, b.ledger.Lanes[1].Requests, b.ledger.Lanes[0].Issued, b.ledger.Lanes[1].Issued, b.ledger.Lanes[0].Results, b.ledger.Lanes[1].Results, b.failure != nil)
		b.mu.Unlock()
		t.Fatal("native sequence did not reach the final joint tool wait")
	}
	interrupted := clients[0].process.Close()
	if !errors.Is(interrupted, context.Canceled) || errors.Is(interrupted, childproc.ErrCleanup) {
		t.Fatal("one native client cancellation did not join")
	}
	joinCtx, stopJoin := context.WithTimeout(ctx, 3*time.Second)
	joinErr := b.waitCanceled(joinCtx, 0, nativeToolOwnersGone)
	stopJoin()
	if joinErr != nil {
		t.Fatal("canceled native request did not finish its server-side cleanup")
	}
	b.mu.Lock()
	close(b.rounds[rounds-1].Release)
	b.mu.Unlock()
	result, err := clients[1].process.Wait()
	if err != nil || result.ExitCode != 0 {
		t.Fatal("unrelated native client did not complete after sibling cancellation")
	}
	for lane, c := range clients {
		captured := c.capture()
		if captured.err != nil {
			t.Fatal("native bounded output capture failed")
		}
		if lane == 1 {
			var response struct {
				Type, Result string
				IsError      bool `json:"is_error"`
			}
			if json.Unmarshal(captured.data, &response) != nil || response.Type != "result" || response.IsError || !strings.Contains(response.Result, "Native tool sequence complete: "+b.ledger.Lanes[1].Seed) || strings.Contains(response.Result, b.ledger.Lanes[0].Seed) {
				t.Fatal("native final answer lost its own completion or included foreign context")
			}
		}
		if !errors.Is(syscall.Kill(-c.process.PID(), 0), syscall.ESRCH) {
			t.Fatal("native client group survived")
		}
	}
	if server.Close() != nil || b.manager.Close() != nil || pool.Close() != nil {
		t.Fatal("shared native runtime cleanup")
	}
	if stats := server.Stats(); stats.Connections != 0 || stats.Handlers != 0 || !stats.Closing {
		t.Fatal("native HTTP ownership survived shutdown")
	}
	validator.Close()
	b.mu.Lock()
	if b.failure != nil || b.ledger.Lanes[0].Requests != rounds || b.ledger.Lanes[1].Requests != rounds+1 || b.ledger.Lanes[0].Results != rounds-1 || b.ledger.Lanes[1].Results != rounds || b.delivered != [2]int{rounds - 1, rounds} || b.finished != [2]int{0, 1} || b.canceled != [2]int{1, 0} || len(b.samples) != rounds-3 {
		b.mu.Unlock()
		t.Fatal("native response, result or settlement counts diverged")
	}
	owners := b.owners
	warm, last := b.samples[0], b.samples[len(b.samples)-1]
	peak := warm
	for _, sample := range b.samples {
		peak.FDs = max(peak.FDs, sample.FDs)
		peak.Goroutines = max(peak.Goroutines, sample.Goroutines)
		peak.Heap = max(peak.Heap, sample.Heap)
	}
	active := b.ended.Sub(b.started)
	b.mu.Unlock()
	for lane, c := range clients {
		w, err := readNativeToolWitness(filepath.Join(b.path, fmt.Sprintf("owner-%d.json", lane)))
		if err != nil || w.PID != owners[lane].PID || w.Peer != owners[lane].Peer || w.Calls != rounds || w.Results != rounds-1+lane || !errors.Is(syscall.Kill(-w.PID, 0), syscall.ESRCH) || !errors.Is(syscall.Kill(w.Peer, 0), syscall.ESRCH) {
			t.Fatal("final independent MCP result or cleanup witness differs")
		}
		if _, err := os.Lstat(filepath.Dir(w.Config)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native relay configuration survived")
		}
		profilePath := c.profile.Path()
		if c.profile.Close() != nil {
			t.Fatal("native profile cleanup")
		}
		c.close()
		if _, err := os.Lstat(profilePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native private profile survived")
		}
		if fileFingerprint(t, settings[lane]) != beforeSettings[lane] || fileFingerprint(t, globals[lane]) != beforeGlobals[lane] {
			t.Fatal("native source settings changed")
		}
		for round := 1; round <= rounds; round++ {
			data, err := os.ReadFile(nativeToolPath(b.ledger.Lanes[lane], round))
			if err != nil || string(data) != nativeToolCanary(b.ledger.Lanes[lane], round)+"\n" {
				t.Fatal("native Read changed its owned file")
			}
		}
	}
	settled, err := observeNativeToolResources()
	if err != nil || !nativeToolResourcesWithin(baseline, settled) {
		t.Fatal("native tool resources did not settle")
	}
	t.Logf("client=%s rounds=%d active_ms=%d concurrent_native_clients=2 shared_gateway=true shared_manager=true ACP_prompts=2 allowed_results=%d hook_refusals=%d cancelled_before_final_delivery=true sibling_completed=true sources_unchanged=true recorded_groups_joined=true http_owners_joined=true warm=%+v peak=%+v last=%+v settled=%+v", measured, rounds, active.Milliseconds(), rounds-1, rounds, warm, peak, last, settled)
}
