//go:build darwin || linux

package interop_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/toolregistry"
)

type batchReply struct {
	raw json.RawMessage
	err error
}
type batchOwner struct {
	client  *acp.Client
	broker  *relay.Broker
	socket  *relay.Socket
	session string
	peer    int
	batch   relay.Batch
	results []relay.Result
	done    chan batchReply
	nonce   string
	prompts int
}

func openBatchOwner(t *testing.T, ctx context.Context, fake, executable string) *batchOwner {
	t.Helper()
	registry, err := toolregistry.Build(ctx, []json.RawMessage{json.RawMessage(`{"name":"batch_action","input_schema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}`)}, nil, syntaxFixtureValidator{})
	if err != nil {
		t.Fatal("cannot prepare owned batch registry")
	}
	b, err := relay.NewBroker(registry, relay.Limits{ToolTimeout: 15 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare owned batch broker")
	}
	o := &batchOwner{broker: b}
	t.Cleanup(func() {
		if !o.close() {
			t.Error("batch owner cleanup did not join")
		}
	})
	o.socket, err = relay.Listen(b, relay.SocketConfig{})
	if err != nil {
		t.Fatal("cannot prepare owned batch socket")
	}
	work := t.TempDir()
	setup, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	o.client, err = acp.Start(setup, acp.Config{Executable: fake, Args: []string{"batch-relay"}, Directory: work, ClientInfo: acp.Info{Name: "independent-batch-control", Version: "1"}, Limits: acp.Limits{RequestTimeout: 15 * time.Second}})
	if err != nil || o.socket.BindProcess(o.client.PID()) != nil {
		t.Fatal("cannot start and bind owned batch process")
	}
	raw, err := o.client.Call(setup, "session/new", map[string]any{"cwd": work, "mcpServers": []any{map[string]any{"name": "dax_session", "command": executable, "args": []string{"relay", "--config", o.socket.ConfigPath()}, "env": []any{}}}})
	var result struct{ SessionID string }
	if err != nil || json.Unmarshal(raw, &result) != nil || result.SessionID == "" {
		t.Fatal("cannot create owned batch session")
	}
	o.session = result.SessionID
	peer, joined := o.socket.PeerPID()
	group, groupErr := syscall.Getpgid(peer)
	if !joined || peer <= 1 || peer == o.client.PID() || groupErr != nil || group != o.client.PID() {
		t.Fatal("batch relay group was not independently observed")
	}
	o.peer = peer
	return o
}

func (o *batchOwner) close() bool {
	joined := true
	if o.client != nil {
		joined = o.client.Close() == nil && errors.Is(syscall.Kill(-o.client.PID(), 0), syscall.ESRCH)
	}
	if o.socket != nil {
		joined = o.socket.Close() == nil && joined
		_, fileErr := os.Lstat(o.socket.ConfigPath())
		_, dirErr := os.Lstat(filepath.Dir(o.socket.ConfigPath()))
		joined = joined && errors.Is(fileErr, os.ErrNotExist) && errors.Is(dirErr, os.ErrNotExist)
	}
	if o.broker != nil {
		o.broker.Close()
		joined = joined && o.broker.Stats().Pending == 0
	}
	if o.peer > 1 {
		joined = joined && errors.Is(syscall.Kill(o.peer, 0), syscall.ESRCH)
	}
	return joined
}

func (o *batchOwner) start(t *testing.T, ctx context.Context, nonce string) {
	t.Helper()
	if o.broker.BeginTurn() != nil {
		t.Fatal("cannot begin owned batch turn")
	}
	o.nonce, o.prompts = nonce, o.prompts+1
	o.done = make(chan batchReply, 1)
	go func() {
		raw, err := o.client.Call(ctx, "session/prompt", map[string]any{"sessionId": o.session, "prompt": []any{map[string]string{"type": "text", "text": nonce}}})
		o.done <- batchReply{raw, err}
	}()
}

func (o *batchOwner) seal(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for o.broker.Stats().Queued != 3 {
		select {
		case <-ctx.Done():
			t.Fatal("batch readiness context expired")
		case <-deadline.C:
			t.Fatal("three concurrent calls did not become ready")
		case <-o.done:
			t.Fatal("ACP prompt ended before the complete batch was delivered")
		case <-tick.C:
		}
	}
	batch, err := o.broker.Seal()
	if err != nil || len(batch.Calls) != 3 || o.broker.Delivered(batch.Number) != nil {
		t.Fatal("complete three-call batch was not delivered")
	}
	o.batch, o.results = batch, nil
	seen := map[int]bool{}
	for _, call := range batch.Calls {
		var args struct{ N int }
		if call.Name != "batch_action" || json.Unmarshal(call.Input, &args) != nil || args.N < 1 || args.N > 3 || seen[args.N] {
			t.Fatal("batch changed an exact independent input")
		}
		seen[args.N] = true
		o.results = append(o.results, relay.Result{ID: call.ID, ToolResult: relay.ToolResult{IsError: args.N == 2, Content: []relay.Content{{Type: "text", Text: fmt.Sprintf("%s_%d", o.nonce, args.N)}}}})
	}
}

func (o *batchOwner) reject(t *testing.T, owner string, results []relay.Result) {
	t.Helper()
	before := o.broker.Stats()
	if !errors.Is(o.broker.Resolve(owner, results), relay.ErrResults) || o.broker.Stats() != before || o.broker.Err() != nil {
		t.Fatal("invalid batch consumed or retired the real pending owner")
	}
	select {
	case <-o.done:
		t.Fatal("invalid results reached the suspended ACP prompt")
	default:
	}
}

func (o *batchOwner) finish(t *testing.T, ctx context.Context) {
	t.Helper()
	// Client return order need not match queue order or the MCP peer's request order.
	reversed := append([]relay.Result(nil), o.results...)
	for a, b := 0, len(reversed)-1; a < b; a, b = a+1, b-1 {
		reversed[a], reversed[b] = reversed[b], reversed[a]
	}
	if o.broker.Resolve(o.broker.Credentials().Owner, reversed) != nil {
		t.Fatal("complete mixed results were not accepted")
	}
	var response batchReply
	select {
	case response = <-o.done:
	case <-ctx.Done():
		t.Fatal("batch response did not finish")
	}
	var result struct {
		StopReason string
		Observed   struct {
			PID, RelayPID, PromptCount, Calls, Errors int
			Digest                                    string
		}
	}
	expected := sha256.Sum256([]byte(strings.Join([]string{o.nonce + "_1", o.nonce + "_2", o.nonce + "_3"}, "\n")))
	if response.err != nil || len(response.raw) > 1024 || json.Unmarshal(response.raw, &result) != nil || result.StopReason != "end_turn" || result.Observed.PID != o.client.PID() || result.Observed.RelayPID != o.peer || result.Observed.PromptCount != o.prompts || result.Observed.Calls != 3 || result.Observed.Errors != 1 || result.Observed.Digest != hex.EncodeToString(expected[:]) {
		t.Fatal("MCP request correlation, refusal or exact result content was lost")
	}
	if o.broker.EndTurn() != nil || o.broker.Stats().Pending != 0 {
		t.Fatal("completed batch retained pending calls")
	}
	o.reject(t, o.broker.Credentials().Owner, o.results)
}

func TestConcurrentMultiCallRelayOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("cannot prepare private batch build directory")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, Timeout: time.Minute, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal("cannot prepare bounded batch builder")
	}
	defer runner.Close()
	fake := buildDenialACPFixture(t, ctx, runner, root)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot identify batch build root")
	}
	executable := filepath.Join(root, "owned-relay")
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", executable, "../../cmd/dax-kiro-proxy"}}); err != nil {
		t.Fatal("cannot build owned relay executable")
	}
	for wave := range 8 {
		t.Run(fmt.Sprintf("wave-%d", wave+1), func(t *testing.T) {
			a, b := openBatchOwner(t, ctx, fake, executable), openBatchOwner(t, ctx, fake, executable)
			if a.client.PID() == b.client.PID() || a.peer == b.peer || a.socket.ConfigPath() == b.socket.ConfigPath() {
				t.Fatal("distinct batches shared process or configuration ownership")
			}
			for round := range 2 {
				a.start(t, ctx, fmt.Sprintf("BATCH_REPLY_%d_A_%d", wave, round))
				b.start(t, ctx, fmt.Sprintf("BATCH_REPLY_%d_B_%d", wave, round))
				a.seal(t, ctx)
				b.seal(t, ctx)
				a.reject(t, a.broker.Credentials().Owner, b.results)
				a.reject(t, b.broker.Credentials().Owner, a.results)
				a.reject(t, a.broker.Credentials().Owner, a.results[:2])
				if round == 0 {
					b.finish(t, ctx)
				} else {
					before := a.broker.Stats()
					if !b.close() || a.broker.Stats() != before || a.broker.Err() != nil {
						t.Fatal("closing one pending owner damaged its healthy sibling or retained a process")
					}
					select {
					case reply := <-b.done:
						if reply.err == nil {
							t.Fatal("cancelled batch completed successfully")
						}
					case <-ctx.Done():
						t.Fatal("cancelled ACP call did not join")
					}
				}
				a.finish(t, ctx)
			}
			a.start(t, ctx, fmt.Sprintf("BATCH_REPLY_%d_A_2", wave))
			a.seal(t, ctx)
			a.finish(t, ctx)
			for range 3 {
				if !a.close() || !b.close() {
					t.Fatal("repeated batch cleanup did not join")
				}
			}
		})
		if t.Failed() {
			return
		}
	}
	t.Log("waves=8 owners=16 calls_per_batch=3 completed_calls=96 cancelled_calls=24 recorded_acp_groups_joined=16 recorded_relays_joined=16")
}
