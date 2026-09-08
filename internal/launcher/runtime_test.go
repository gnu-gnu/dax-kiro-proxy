package launcher_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
	"dax-kiro-proxy/internal/status"
)

var runtimeClient, runtimeACP, runtimeProxy string

func TestMain(m *testing.M) {
	if childproc.IsTerminalReclaimer(os.Args[1:]) {
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "dax-launcher-fixture-")
	if err != nil {
		panic(err)
	}
	runtimeClient, runtimeACP, runtimeProxy = filepath.Join(dir, "client"), filepath.Join(dir, "fake-acp"), filepath.Join(dir, "dax-kiro-proxy")
	for _, build := range [][2]string{{runtimeClient, "./testdata/client"}, {runtimeACP, "../acp/testdata/fake"}, {runtimeProxy, "../../cmd/dax-kiro-proxy"}} {
		cmd := exec.Command("go", "build", "-o", build[0], build[1])
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if cmd.Run() != nil {
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type runtimeObservation struct {
	PID      int    `json:"clientPID"`
	Endpoint string `json:"endpoint"`
	Runtime  string `json:"runtime"`
	State    string `json:"state"`
}

type observedOwner struct {
	*session.Manager
	closes        atomic.Int32
	starts        atomic.Int32
	lists         atomic.Int32
	profileParent string
	profileStayed atomic.Bool
	models        *launcher.ModelState
	modelsStayed  atomic.Bool
	closeError    error
}

func (b *observedOwner) Start(ctx context.Context, request *anthropic.Request) (inference.Turn, error) {
	b.starts.Add(1)
	return b.Manager.Start(ctx, request)
}

func (b *observedOwner) Models(ctx context.Context) ([]inference.Model, error) {
	b.lists.Add(1)
	return b.Manager.Models(ctx)
}

func (b *observedOwner) Close() error {
	b.closes.Add(1)
	err := b.Manager.Close()
	if b.models != nil {
		_, modelErr := b.models.Models(context.Background())
		b.modelsStayed.Store(modelErr == nil)
	}
	if b.starts.Load() > 0 {
		entries, readErr := os.ReadDir(b.profileParent)
		if readErr == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "dax-runtime-") {
					if _, err := os.Stat(filepath.Join(b.profileParent, entry.Name(), "host-settings.json")); err == nil {
						b.profileStayed.Store(true)
					}
				}
			}
		}
	}
	return errors.Join(err, b.closeError)
}

func runtimeConfig(t *testing.T, mode string) (launcher.ClientRunConfig, *observedOwner, *os.File, *os.File) {
	t.Helper()
	profile := profileConfig(t)
	profile.Executable, profile.GatewayURL, profile.ModelToken = runtimeClient, "", ""
	profile.Environment = []string{"PATH=/usr/bin:/bin", "TERM=" + mode, "AWS_ACCESS_KEY_ID=independent-secret-sentinel", "CLAUDE_CODE_USE_BEDROCK=1"}
	hash := sha256.Sum256([]byte("fixture-backend"))
	profile.Model = fmt.Sprintf("claude-dax-fixture-backend-%x", hash[:8])
	writeSettings(t, profile.UserSettings, []byte(`{"permissions":{"defaultMode":"manual"},"hooks":{},"env":{"NOTE":"independent-setting"}}`))
	worker, err := schemacheck.New(schemacheck.Config{Executable: runtimeProxy, Directory: profile.Project})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	backendMode := "chat"
	if strings.HasPrefix(mode, "tools-") {
		backendMode = "chat-tools"
	}
	metrics := status.NewTurnQueue()
	manager, err := session.NewManager(session.ManagerConfig{ProfileScope: "independent-launcher", Metrics: metrics, Session: session.Config{Process: acp.Config{Executable: runtimeACP, Directory: profile.Project, Args: []string{backendMode}, ClientInfo: acp.Info{Name: "dax-launcher-fixture", Version: "1"}, Limits: acp.Limits{GracePeriod: 20 * time.Millisecond, TermPeriod: 20 * time.Millisecond, KillPeriod: time.Second}}, SetupTimeout: 3 * time.Second, TurnTimeout: 8 * time.Second, Validator: worker, RelayExecutable: runtimeProxy}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	owner := &observedOwner{Manager: manager, profileParent: profile.RuntimeParent}
	input, send, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output, receive, err := os.Pipe()
	if err != nil {
		input.Close()
		send.Close()
		t.Fatal(err)
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{input, send, output, receive, null} {
		t.Cleanup(func() { file.Close() })
	}
	cfg := launcher.ClientRunConfig{Backend: owner, Schema: worker, Client: profile,
		Server:   gateway.ServerConfig{Gateway: gateway.Config{Metrics: metrics, FirstEventTimeout: 4 * time.Second, TurnTimeout: 8 * time.Second}, ShutdownTimeout: 2 * time.Second, JoinTimeout: time.Second},
		Attached: childproc.AttachedConfig{Lifetime: 15 * time.Second, GracePeriod: 20 * time.Millisecond, TermPeriod: 20 * time.Millisecond, KillPeriod: time.Second},
		IO:       childproc.AttachedIO{Stdin: input, Stdout: receive, Stderr: null}}
	return cfg, owner, output, send
}

func readRuntimeObservation(t *testing.T, output *os.File) runtimeObservation {
	t.Helper()
	if err := output.SetReadDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReaderSize(output, 4096).ReadBytes('\n')
	var result runtimeObservation
	if err != nil || len(line) > 4096 || json.Unmarshal(line, &result) != nil || result.PID <= 0 || result.Runtime == "" {
		t.Fatal("independent client did not report bounded readiness", err)
	}
	return result
}

func assertRuntimeGone(t *testing.T, cfg launcher.ClientRunConfig, owner *observedOwner, seen runtimeObservation, before []byte) {
	t.Helper()
	if owner.closes.Load() != 1 || owner.Stats().Processes != 0 || owner.Stats().Sessions != 0 || !owner.profileStayed.Load() {
		t.Fatal("launcher did not own final backend cleanup")
	}
	if !errors.Is(syscall.Kill(-seen.PID, 0), syscall.ESRCH) {
		t.Fatal("attached client group survived runtime completion")
	}
	if _, err := os.Stat(seen.Runtime); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("client profile survived runtime completion")
	}
	u, err := url.Parse(seen.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("gateway still accepts after client runtime completion")
	}
	after, err := os.ReadFile(cfg.Client.UserSettings)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("client source settings changed during coordinated runtime")
	}
	for _, file := range []*os.File{cfg.IO.Stdin, cfg.IO.Stdout, cfg.IO.Stderr} {
		if _, err := file.Stat(); err != nil {
			t.Fatal("launcher closed a caller-owned descriptor")
		}
	}
	if err := cfg.Schema.Check(t.Context(), []byte(`{"type":"object"}`)); err == nil {
		t.Fatal("launcher left schema worker admission open")
	}
}

func TestClientRuntimeJoinsNormalAndSuspendedToolSessions(t *testing.T) {
	for _, mode := range []string{"catalog-text", "tools-complete", "tools-exit", "tools-hold"} {
		t.Run(mode, func(t *testing.T) {
			cfg, owner, output, _ := runtimeConfig(t, mode)
			modelCfg, data := runtimeModelConfig(t, cfg.Client.RuntimeParent)
			cfg.Models, _ = launcher.PrepareModels(t.Context(), modelCfg)
			if cfg.Models == nil {
				t.Fatal("cannot prepare owned model catalog")
			}
			t.Cleanup(cfg.Models.Close)
			owner.models, cfg.Client.Model = cfg.Models, ""
			before, _ := os.ReadFile(cfg.Client.UserSettings)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type outcome struct {
				result launcher.ClientRunResult
				err    error
			}
			finished := make(chan outcome, 1)
			go func() { result, err := launcher.RunClient(ctx, cfg); finished <- outcome{result, err} }()
			seen := readRuntimeObservation(t, output)
			if mode == "tools-hold" {
				if owner.Stats().Busy != 1 {
					t.Fatal("fixture lost its suspended ACP prompt before cancellation")
				}
				for range 8 {
					cancel()
				}
			}
			select {
			case got := <-finished:
				if mode == "tools-hold" {
					if !errors.Is(got.err, context.Canceled) {
						t.Fatal("canceled client runtime lost its cause", got.err)
					}
				} else if got.err != nil || got.result.ExitCode != 0 {
					t.Fatal("normal client completion failed", got.err)
				}
				if got.result.ClientPID != seen.PID || got.result.GatewayTime <= 0 || got.result.ProfileTime <= 0 || got.result.LaunchTime <= 0 || got.result.CleanupTime <= 0 {
					t.Fatal("missing literal process/timing observation")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("launcher shutdown did not join finite cleanup")
			}
			assertRuntimeGone(t, cfg, owner, seen, before)
			if _, err := cfg.Models.Models(t.Context()); err == nil || !owner.modelsStayed.Load() || owner.lists.Load() != 0 {
				t.Fatal("runtime did not serve and close its prepared catalog after backend cleanup")
			}
			last, found, err := catalog.LoadLastModel(modelCfg.Cache.Directory, modelCfg.Cache.Identity, data)
			wantSaved := mode == "catalog-text" || mode == "tools-complete"
			if err != nil || found != wantSaved || found && last != "fixture-backend" || cfg.Models.SaveFailed() {
				t.Fatal("runtime did not persist exactly the final delivered model")
			}
		})
	}
}

func runtimeModelConfig(t *testing.T, parent string) (launcher.ModelConfig, *catalog.Catalog) {
	t.Helper()
	data, err := catalog.New([]catalog.Backend{{ID: "fixture-backend"}, {ID: "another-independent-model"}}, "fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	cfg := launcher.ModelConfig{Cache: catalog.CacheConfig{Directory: filepath.Join(parent, "model-cache"), Identity: catalog.Identity{Executable: runtimeACP, Version: "independent-fixture", ProfileDigest: strings.Repeat("4", 64), AgentDigest: strings.Repeat("5", 64), CapabilitiesDigest: strings.Repeat("6", 64)}}, Interactive: true, Discover: func(context.Context) (*catalog.Catalog, error) { return data, nil }}
	return cfg, data
}

func TestClientRuntimeClosesPreparedModelsOnConfigurationFailure(t *testing.T) {
	cfg, owner, _, _ := runtimeConfig(t, "text")
	modelCfg, _ := runtimeModelConfig(t, cfg.Client.RuntimeParent)
	var err error
	cfg.Models, err = launcher.PrepareModels(t.Context(), modelCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cfg.Models.Close)
	// A caller-supplied model conflicts with the prepared model authority.
	if _, err := launcher.RunClient(t.Context(), cfg); !errors.Is(err, launcher.ErrConfig) {
		t.Fatal("conflicting model authorities were accepted")
	}
	if owner.closes.Load() != 1 || owner.starts.Load() != 0 {
		t.Fatal("invalid model configuration did not close its unstarted backend")
	}
	if _, err := cfg.Models.Models(t.Context()); err == nil {
		t.Fatal("failed startup left prepared model discovery open")
	}
}

func TestClientRuntimeStartupFailureStillClosesItsOwners(t *testing.T) {
	for _, mode := range []string{"canceled", "settings", "exec", "conflicting-authority", "conflicting-startup-model", "limits"} {
		t.Run(mode, func(t *testing.T) {
			cfg, owner, _, _ := runtimeConfig(t, "text")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "canceled":
				cancel()
			case "settings":
				writeSettings(t, cfg.Client.UserSettings, []byte(`{"broken"`))
			case "exec":
				cfg.Client.Executable = filepath.Join(cfg.Client.Home, "absent-independent-client")
			case "conflicting-authority":
				cfg.Client.ModelToken = "independent-secret-sentinel"
			case "conflicting-startup-model":
				cfg.Server.Gateway.LaunchModel = "claude-dax-different-launch"
			case "limits":
				cfg.Attached.Lifetime = -time.Second
			}
			result, err := launcher.RunClient(ctx, cfg)
			if mode == "conflicting-startup-model" && !errors.Is(err, launcher.ErrConfig) {
				t.Fatal("startup notice accepted a separate model authority")
			}
			if err == nil || result.ClientPID != 0 || result.ExitCode != -1 || owner.closes.Load() != 1 || owner.Stats().Processes != 0 {
				t.Fatal("startup failure skipped owned cleanup", err)
			}
			if strings.Contains(err.Error(), "independent-secret-sentinel") {
				t.Fatal("runtime error exposed launch input")
			}
			entries, err := os.ReadDir(cfg.Client.RuntimeParent)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "dax-runtime-") {
					t.Fatal("failed startup left a private profile")
				}
			}
			if err := cfg.Schema.Check(t.Context(), []byte(`{"type":"object"}`)); err == nil {
				t.Fatal("failed startup left schema admission open")
			}
		})
	}
}

func TestClientRuntimeJoinsUsageAndReportsSanitizedCleanupFailure(t *testing.T) {
	cfg, owner, output, _ := runtimeConfig(t, "text")
	owner.closeError = errors.New("independent-secret-sentinel from cleanup")
	entered, joined := make(chan struct{}), make(chan struct{})
	usage, err := status.NewUsageCache(status.UsageConfig{Fetch: func(ctx context.Context) (status.UsageData, error) {
		close(entered)
		<-ctx.Done()
		close(joined)
		return status.UsageData{}, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	cfg.Server.Gateway.Usage = usage
	usage.Read()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("usage fixture did not start")
	}
	result, err := launcher.RunClient(t.Context(), cfg)
	if !errors.Is(err, launcher.ErrRunCleanup) || strings.Contains(err.Error(), "independent-secret-sentinel") || result.ExitCode != 0 {
		t.Fatal("runtime hid or disclosed the wrong cleanup failure", err)
	}
	seen := readRuntimeObservation(t, output)
	if _, err := os.Stat(seen.Runtime); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backend cleanup error prevented profile cleanup")
	}
	select {
	case <-joined:
	default:
		t.Fatal("runtime left an asynchronous usage refresh")
	}
	if _, err := owner.Models(t.Context()); !errors.Is(err, acp.ErrClosed) {
		t.Fatal("backend stayed open after cleanup failure")
	}
}

var _ inference.Backend = (*observedOwner)(nil)
