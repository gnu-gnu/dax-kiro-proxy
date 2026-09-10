package interop_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func TestClaudeNativeRestartThroughGatewayAndACP(t *testing.T) {
	observeNativeRestart(t, false)
}

func TestKiroLiveNativeRestartThroughGatewayAndACP(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("two native text turns require explicit Kiro credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" {
		t.Fatal("pinned Kiro path required")
	}
	observeNativeRestart(t, true)
}

type nativeRestartBackend struct {
	manager          *session.Manager
	models           *catalog.Catalog
	stage            int
	seed, identity   string
	foreignSeeds     []string
	starts           atomic.Int32
	mu               sync.Mutex
	text             string
	ends             int
	failed           bool
	observeProcess   func() error
	beforeFirstEvent func(context.Context) error
}

func (b *nativeRestartBackend) Models(context.Context) ([]inference.Model, error) {
	return b.models.List(), nil
}
func (b *nativeRestartBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if b.starts.Add(1) != 1 || len(r.Tools) != 0 || !nativeHistoryID(r.Identity.Session) || b.stage == 1 && r.Identity.Session != b.identity {
		return nil, inference.ErrRequest
	}
	if !nativeRestartHistoryMatches(r, b.stage, b.seed, b.foreignSeeds) {
		return nil, inference.ErrRequest
	}
	if b.stage == 0 {
		b.identity = r.Identity.Session
	}
	turn, err := b.manager.Start(ctx, r)
	if err != nil {
		return nil, err
	}
	return &nativeRestartTurn{Turn: turn, owner: b}, nil
}

type nativeRestartTurn struct {
	inference.Turn
	owner    *nativeRestartBackend
	observed bool
}

func (t *nativeRestartTurn) Next(ctx context.Context) (inference.Event, error) {
	event, err := t.Turn.Next(ctx)
	if err != nil {
		return event, err
	}
	if !t.observed {
		if err := t.owner.observeProcess(); err != nil {
			t.Cancel()
			return inference.Event{}, err
		}
		t.observed = true
		if t.owner.beforeFirstEvent != nil {
			if err := t.owner.beforeFirstEvent(ctx); err != nil {
				t.Cancel()
				return inference.Event{}, err
			}
		}
	}
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	switch event.Kind {
	case inference.Text:
		if len(t.owner.text)+len(event.Text) > 8192 || t.owner.ends != 0 {
			t.owner.failed = true
		} else {
			t.owner.text += event.Text
		}
	case inference.End:
		t.owner.ends++
		t.owner.failed = t.owner.failed || event.StopReason != "end_turn" || t.owner.ends != 1
	default:
		t.owner.failed = true
	}
	if t.owner.failed {
		return inference.Event{}, inference.ErrRequest
	}
	return event, nil
}

func observeNativeRestart(t *testing.T, live bool) {
	observeNativeRestartPlan(t, live, nil)
}

func observeNativeRestartPlan(t *testing.T, live bool, plan *concurrentHistoryPlan) {
	t.Helper()
	if plan != nil {
		if live {
			t.Fatal("concurrent history observation requires independent ACP")
		}
		defer func() {
			if t.Failed() {
				plan.shared.abort()
			}
		}()
	}
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set pinned Claude for native restart observation")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-native-restart-")
	if err != nil {
		t.Fatal("restart root")
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: 60 * time.Second, MaxProcesses: 1, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("restart runner")
	}
	defer runner.Close()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	if plan != nil {
		home, project = plan.shared.home, plan.shared.project
	} else {
		for _, path := range []string{home, filepath.Join(home, ".claude"), project} {
			if os.Mkdir(path, 0700) != nil {
				t.Fatal("restart directory")
			}
		}
	}
	settings, global := filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json")
	if plan == nil && (os.WriteFile(settings, []byte(`{"disableAllHooks":true,"autoMemoryEnabled":false,"permissions":{"deny":["Read","Write","Edit","Bash"]}}`), 0600) != nil || os.WriteFile(global, []byte(`{}`), 0600) != nil) {
		t.Fatal("restart sources")
	}
	beforeSettings, beforeGlobal := fileFingerprint(t, settings), fileFingerprint(t, global)
	version, err := runner.Run(ctx, childproc.Command{Executable: client, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, Args: []string{"--version"}})
	if err != nil || !launcher.CompatibleClientOutput(version.Stdout) {
		t.Fatal("unverified native client")
	}
	relay := buildRelayObserver(t)
	proxy := filepath.Join(filepath.Dir(relay), "owned-relay")
	fake := filepath.Join(root, "history-peer")
	if !live {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal("fixture source")
		}
		env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
		for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
			if v := os.Getenv(key); v != "" {
				env = append(env, key+"="+v)
			}
		}
		if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", fake, "./testdata/historypeer"}}); err != nil {
			t.Fatal("build independent history peer")
		}
	}
	seed := rand.Text()
	if plan != nil {
		seed = plan.shared.seeds[plan.lane]
	}
	prompts := [2]string{"Remember SeedQuestion_91 " + seed + " for later. Reply only with the concatenation of ArchiveReady and _91 without spaces.", "ContinuedQuestion_97: reply only with the exact token remembered in the earlier question, a space, and the concatenation of ArchiveResumed and _97 without spaces."}
	var id string
	var groups [2]int
	var endpoints, profiles [2]string
	var tokens [2]gateway.Tokens
	for stage := range 2 {
		stageRoot := filepath.Join(root, strconv.Itoa(stage))
		backend := filepath.Join(stageRoot, "backend")
		worker := filepath.Join(stageRoot, "worker")
		for _, path := range []string{stageRoot, backend, worker} {
			if os.Mkdir(path, 0700) != nil {
				t.Fatal("restart stage directory")
			}
		}
		models, _ := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent native restart"}}, "fixture-backend")
		backendModel := "fixture-backend"
		pidPath := filepath.Join(stageRoot, "peer-pid")
		process := acp.Config{Executable: fake, Args: []string{strconv.Itoa(stage), pidPath}, Directory: backend, ClientInfo: acp.Info{Name: "independent-native-restart", Version: "1"}, Limits: acp.Limits{RequestTimeout: 45 * time.Second}}
		var execution launcher.KiroExecution
		if live {
			models, execution = prepareLiveKiroProbe(t, ctx, runner, os.Getenv("DAX_INTEROP_KIRO_BINARY"), stageRoot, backend, filepath.Join(stageRoot, "kiro-preflight"))
			process, backendModel = execution.Process, "auto"
			process.Limits.RequestTimeout = 45 * time.Second
		}
		model, err := models.ClientID(backendModel)
		if err != nil {
			t.Fatal("restart model")
		}
		validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: worker})
		if err != nil {
			t.Fatal("restart validator")
		}
		defer validator.Close()
		pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 1, SessionsPerProcess: 1, MaxIdle: 1, SetupTimeout: 20 * time.Second})
		if err != nil {
			t.Fatal("restart pool")
		}
		defer pool.Close()
		var prepared, cleaned atomic.Int32
		var ownedPaths []string
		cfg := session.Config{Process: process, Pool: pool, InitialModel: backendModel, Validator: validator, RelayExecutable: relay, SetupTimeout: 20 * time.Second, TurnTimeout: 45 * time.Second, MaxRecreations: 1}
		if live {
			cfg.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
				if prepared.Add(1) != 1 {
					return session.LaunchResources{}, inference.ErrRequest
				}
				owned, err := execution.Prepare(ctx, input)
				ownedPaths = append(ownedPaths, owned.Directory, input.RelayConfig)
				if owned.Cleanup != nil {
					cleanup := owned.Cleanup
					owned.Cleanup = func() error { e := cleanup(); cleaned.Add(1); return e }
				}
				return owned, err
			}
		}
		manager, err := session.NewManager(session.ManagerConfig{Session: cfg, ProfileScope: "independent-native-restart", MaxSessions: 1})
		if err != nil {
			t.Fatal("restart manager")
		}
		defer manager.Close()
		guard := &nativeRestartBackend{manager: manager, models: models, stage: stage, seed: seed, identity: id}
		clientPIDPath := filepath.Join(stageRoot, "client-pid")
		if plan != nil {
			guard.foreignSeeds = []string{plan.shared.seeds[1-plan.lane]}
			guard.beforeFirstEvent = func(ctx context.Context) error {
				data, err := readDenialArtifact(stageRoot, "client-pid", 32)
				pid, parseErr := strconv.Atoi(string(data))
				if err != nil || parseErr != nil {
					return errors.New("concurrent native client identity missing")
				}
				return plan.shared.rounds[stage].wait(ctx, historyOverlapWitness{lane: plan.lane, identity: guard.identity, client: pid, acp: groups[stage]})
			}
		}
		guard.observeProcess = func() error {
			pid := 0
			if live {
				records, e := relayProcessRecords(relay)
				if e != nil || len(records) != stage+1 {
					return errors.New("native restart relay identity missing")
				}
				pid = records[stage].pid
			} else {
				data, e := os.ReadFile(pidPath)
				if e != nil || len(data) > 20 {
					return errors.New("native restart peer identity missing")
				}
				pid, _ = strconv.Atoi(string(data))
			}
			group, e := syscall.Getpgid(pid)
			if e != nil || group <= 1 || group == syscall.Getpgrp() || stage == 1 && group == groups[0] {
				return errors.New("native restart group invalid")
			}
			groups[stage] = group
			return nil
		}
		tokens[stage], err = gateway.NewTokens()
		if err != nil {
			t.Fatal("restart token")
		}
		server, err := gateway.StartServer(ctx, gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, Gateway: gateway.Config{Tokens: tokens[stage], Backend: guard, TurnTimeout: 45 * time.Second, FirstEventTimeout: 30 * time.Second, WriteTimeout: time.Minute}})
		if err != nil {
			t.Fatal("restart gateway")
		}
		defer server.Close()
		endpoints[stage] = server.URL()
		profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: stageRoot, Home: home, Project: project, UserSettings: settings, Executable: client, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL(), ModelToken: tokens[stage].Model, KeepHistory: true, ResumeSession: id, Environment: []string{"PATH=/usr/bin:/bin", "TERM=dumb"}})
		if err != nil {
			t.Fatal("restart profile")
		}
		defer profile.Close()
		profiles[stage] = profile.Path()
		command := profile.Command()
		command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--system-prompt", "Follow the user's text-only instruction. Do not use tools.", prompts[stage])
		command.Environment = append(command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
		if plan != nil {
			wrapper := filepath.Join(stageRoot, "observe-client.sh")
			body := "#!/bin/sh\nset -eu\numask 077\nprintf '%s' \"$$\" > " + probeShellQuote(clientPIDPath) + "\nexec " + probeShellQuote(client) + " \"$@\"\n"
			if os.WriteFile(wrapper, []byte(body), 0700) != nil {
				t.Fatal("concurrent native client observer")
			}
			command.Executable = "/bin/sh"
			command.Args = append([]string{wrapper}, command.Args...)
		}
		result, runErr := runner.Run(ctx, command)
		var response struct {
			Type, Subtype, Result string
			SessionID             string `json:"session_id"`
			IsError               bool   `json:"is_error"`
		}
		decoded := json.Unmarshal(result.Stdout, &response) == nil
		marker := "ArchiveReady_91"
		if stage == 1 {
			marker = "ArchiveResumed_97"
		}
		guard.mu.Lock()
		passed := runErr == nil && result.ExitCode == 0 && decoded && response.Type == "result" && response.Subtype == "success" && !response.IsError && nativeHistoryID(response.SessionID) && strings.Count(response.Result, marker) == 1 && guard.starts.Load() == 1 && guard.ends == 1 && !guard.failed && strings.Count(guard.text, marker) == 1
		if stage == 1 {
			passed = passed && response.SessionID == id && strings.Count(response.Result, seed) == 1 && strings.Count(guard.text, seed) == 1
		}
		guard.mu.Unlock()
		if stage == 0 {
			id = response.SessionID
		}
		serverErr, managerErr, poolErr := server.Close(), manager.Close(), pool.Close()
		validator.Close()
		profileCloseErr := profile.Close()
		closeOK := serverErr == nil && managerErr == nil && poolErr == nil && profileCloseErr == nil
		if live {
			closeOK = closeOK && prepared.Load() == 1 && cleaned.Load() == 1
		}
		for _, path := range ownedPaths {
			_, e := os.Lstat(path)
			closeOK = closeOK && os.IsNotExist(e)
		}
		_, profileErr := os.Lstat(profile.Path())
		gone := groups[stage] > 1 && errors.Is(syscall.Kill(-groups[stage], 0), syscall.ESRCH) && result.PID > 1 && errors.Is(syscall.Kill(result.PID, 0), syscall.ESRCH) && os.IsNotExist(profileErr) && runner.Active() == 0 && pool.Stats().Processes == 0
		connection, dialErr := net.DialTimeout("tcp", strings.TrimPrefix(server.URL(), "http://"), time.Second)
		if connection != nil {
			connection.Close()
		}
		gone = gone && dialErr != nil
		t.Logf("live=%v stage=%d completion=%v admitted_requests=%d backend_end_turns=%d cleanup=%v processes_profile_listener_gone=%v", live, stage+1, passed, guard.starts.Load(), guard.ends, closeOK, gone)
		if !passed || !closeOK || !gone {
			t.Fatal("native restart stage failed; next stage not dispatched")
		}
		if os.RemoveAll(stageRoot) != nil {
			t.Fatal("restart stage cleanup")
		}
		if plan != nil {
			plan.shared.record(stage, plan.lane, historyOwnership{identity: id, client: result.PID, acp: groups[stage], profile: profiles[stage], endpoint: endpoints[stage], token: tokens[stage].Model})
			if stage == 0 {
				if err := plan.shared.joinInitial(ctx, plan.lane); err != nil {
					t.Fatal("concurrent initial owners did not join before resume")
				}
			}
		}
	}
	if beforeSettings != fileFingerprint(t, settings) || beforeGlobal != fileFingerprint(t, global) || groups[0] == groups[1] || profiles[0] == profiles[1] || endpoints[0] == endpoints[1] || tokens[0].Model == tokens[1].Model {
		t.Fatal("native restart ownership/source isolation failed")
	}
}
