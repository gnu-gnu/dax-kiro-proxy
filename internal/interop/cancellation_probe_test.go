package interop_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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

type cancellationProbeBackend struct {
	guard     *onePromptBackend
	starts    atomic.Int32
	closes    atomic.Int32
	delivered atomic.Int32
	closeFn   func() error
}

func (b *cancellationProbeBackend) Models(ctx context.Context) ([]inference.Model, error) {
	return b.guard.Models(ctx)
}

func (b *cancellationProbeBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	if b.starts.Add(1) != 1 {
		return nil, inference.ErrRequest
	}
	turn, err := b.guard.Start(ctx, r)
	if err != nil {
		return nil, err
	}
	return &cancellationProbeTurn{Turn: turn, owner: b}, nil
}

type cancellationProbeTurn struct {
	inference.Turn
	owner *cancellationProbeBackend
}

func (t *cancellationProbeTurn) Finish() {
	t.Turn.Finish()
	t.owner.delivered.Add(1)
}

func (b *cancellationProbeBackend) Close() error {
	b.closes.Add(1)
	return b.closeFn()
}

func TestClaudeLauncherCancellationWithFakeACP(t *testing.T) {
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for owned launcher cancellation; no external inference")
	}
	runLauncherCancellationProbe(t, client, "")
}

func TestKiroLiveLauncherCancellation(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("credit-consuming cancellation test requires explicit DAX_INTEROP_KIRO_CREDIT_OPT_IN=1")
	}
	client, kiro := os.Getenv("DAX_INTEROP_CLAUDE_BINARY"), os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if client == "" || kiro == "" {
		t.Fatal("live cancellation requires both pinned executables")
	}
	runLauncherCancellationProbe(t, client, kiro)
}

func runLauncherCancellationProbe(t *testing.T, client, kiro string) {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "dax-cancel-probe-")
	if err != nil {
		t.Fatal("cannot prepare owned cancellation root")
	}
	defer os.RemoveAll(root)
	ctx, stop := context.WithTimeout(t.Context(), 3*time.Minute)
	defer stop()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal("cannot create cancellation fixture runner")
	}
	defer runner.Close()
	home, project, backend, worker, configuration, profiles := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "backend"), filepath.Join(root, "schema"), filepath.Join(root, "kiro-home"), filepath.Join(root, "profiles")
	for _, dir := range []string{home, project, backend, worker, configuration, profiles} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot prepare private cancellation directories")
		}
	}
	versionContext, endVersion := context.WithTimeout(ctx, 5*time.Second)
	version, err := runner.Run(versionContext, childproc.Command{Executable: client, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "DISABLE_AUTOUPDATER=1"}, Args: []string{"--version"}})
	endVersion()
	if err != nil || !launcher.CompatibleClientOutput(version.Stdout) {
		t.Fatal("cancellation client version is unverified")
	}
	hook := buildCancellationHook(t, ctx, runner, root)
	hookRecord, clientRecord := filepath.Join(root, "hook-process"), filepath.Join(root, "client-process")
	filename, canary := filepath.Join(project, "read-fixture"), "IndependentCancellationCanary"+rand.Text()
	settings, mcp := filepath.Join(home, "settings.json"), filepath.Join(root, "empty-mcp.json")
	raw, _ := json.Marshal(map[string]any{"permissions": map[string]string{"defaultMode": "manual"}, "hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": probeShellQuote(hook) + " " + probeShellQuote(hookRecord), "timeout": 35}}}}}})
	for path, data := range map[string][]byte{filename: []byte(canary), settings: raw, mcp: []byte(`{"mcpServers":{}}`)} {
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot write owned cancellation fixtures")
		}
	}
	beforeSettings := fileFingerprint(t, settings)
	relay := buildRelayObserver(t)
	proxy := filepath.Join(filepath.Dir(relay), "owned-relay")
	models, _ := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent cancellation backend"}}, "fixture-backend")
	process := acp.Config{Directory: backend, ClientInfo: acp.Info{Name: "independent-cancellation-probe", Version: "1"}, Limits: acp.Limits{RequestTimeout: 45 * time.Second}}
	var execution launcher.KiroExecution
	backendModel := "fixture-backend"
	if kiro == "" {
		process.Executable = buildDenialACPFixture(t, ctx, runner, root)
		process.Args = []string{"chat-tools-client-launch", filename}
	} else {
		models, execution = prepareLiveKiroProbe(t, ctx, runner, kiro, root, backend, configuration)
		process, backendModel = execution.Process, "auto"
		process.Limits.RequestTimeout = 45 * time.Second
	}
	model, err := models.ClientID(backendModel)
	if err != nil {
		t.Fatal("cancellation model is absent; no fallback")
	}
	validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: worker})
	if err != nil {
		t.Fatal("cannot prepare cancellation validator")
	}
	defer validator.Close()
	pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 1, SessionsPerProcess: 1, MaxIdle: 1, SetupTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare cancellation process pool")
	}
	defer pool.Close()
	var prepared, cleaned atomic.Int32
	var group atomic.Int32
	var artifactMu sync.Mutex
	var artifacts []string
	cfg := session.Config{Process: process, Pool: pool, InitialModel: backendModel, Validator: validator, RelayExecutable: relay, SetupTimeout: 20 * time.Second, TurnTimeout: 45 * time.Second, MaxRecreations: 1}
	cfg.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		if prepared.Add(1) != 1 {
			return session.LaunchResources{}, errors.New("cancellation launch budget exceeded")
		}
		var owned session.LaunchResources
		var err error
		if kiro != "" {
			owned, err = execution.Prepare(ctx, input)
		} else {
			owned.Directory, err = os.MkdirTemp(root, "owned-agent-")
			if err != nil {
				return owned, errors.New("cannot prepare cancellation agent")
			}
			owned.RelayAtLaunch = true
			owned.Cleanup = func() error { return os.RemoveAll(owned.Directory) }
			manifest := filepath.Join(owned.Directory, "independent-relay.json")
			data, _ := json.Marshal(map[string]any{"name": "independent-cancellation-relay", "command": input.RelayExecutable, "args": []string{"relay", "--config", input.RelayConfig}, "env": []any{}})
			err = os.WriteFile(manifest, data, 0600)
			owned.Args = []string{manifest}
		}
		artifactMu.Lock()
		artifacts = append(artifacts, owned.Directory, input.RelayConfig)
		artifactMu.Unlock()
		if owned.Cleanup != nil {
			cleanup := owned.Cleanup
			owned.Cleanup = func() error { defer cleaned.Add(1); return cleanup() }
		}
		return owned, err
	}
	driver, err := session.New(cfg)
	if err != nil {
		t.Fatal("cannot prepare cancellation driver")
	}
	defer driver.Close()
	guard := &onePromptBackend{driver: driver, models: models, readPath: filename, canary: canary, observeUse: func() error {
		records, err := relayProcessRecords(relay)
		if err != nil || len(records) != 1 {
			return errors.New("cancellation relay not observed")
		}
		pgid, err := syscall.Getpgid(records[0].pid)
		if err != nil || pgid <= 1 || pgid == syscall.Getpgrp() {
			return errors.New("cancellation process group not observed")
		}
		group.Store(int32(pgid))
		return nil
	}}
	owner := &cancellationProbeBackend{guard: guard, closeFn: func() error { return errors.Join(driver.Close(), pool.Close()) }}
	wrapper := filepath.Join(root, "client.sh")
	script := "#!/bin/sh\nset -euC\numask 077\nexport CLAUDE_CODE_DISABLE_THINKING=1 CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1\nprintf '%s' \"$$\" > " + probeShellQuote(clientRecord) + "\nexec " + probeShellQuote(client) + " \"$@\" --print --output-format json --no-session-persistence --strict-mcp-config --mcp-config " + probeShellQuote(mcp) + " --tools Read --system-prompt " + probeShellQuote(denialSystemPrompt) + " " + probeShellQuote(denialUserPrompt(filename)) + "\n"
	if os.WriteFile(wrapper, []byte(script), 0700) != nil {
		t.Fatal("cannot prepare owned cancellation client wrapper")
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal("cannot attach cancellation descriptors")
	}
	defer null.Close()
	runCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	type outcome struct {
		result launcher.ClientRunResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := launcher.RunClient(runCtx, launcher.ClientRunConfig{Backend: owner, Schema: validator,
			Client:   launcher.ClientConfig{RuntimeParent: profiles, Home: home, Project: project, UserSettings: settings, Executable: wrapper, Version: launcher.SupportedClientVersion, Model: model, Environment: []string{"PATH=/usr/bin:/bin", "TERM=dumb"}},
			Server:   gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, Gateway: gateway.Config{TurnTimeout: 45 * time.Second, FirstEventTimeout: 20 * time.Second, WriteTimeout: time.Minute}},
			Attached: childproc.AttachedConfig{Lifetime: time.Minute}, IO: childproc.AttachedIO{Stdin: null, Stdout: null, Stderr: null}})
		done <- outcome{result, err}
	}()
	var got outcome
	var hookPID, hookGroup, clientPID int
	ready, returned := false, false
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case got = <-done:
			returned = true
			break wait
		case <-runCtx.Done():
			break wait
		case <-tick.C:
			data, err := readDenialArtifact(root, filepath.Base(hookRecord), 64)
			values := strings.Fields(string(data))
			if err != nil || len(values) != 2 {
				continue
			}
			hookPID, _ = strconv.Atoi(values[0])
			hookGroup, _ = strconv.Atoi(values[1])
			currentHookGroup, groupErr := syscall.Getpgid(hookPID)
			clientData, err := readDenialArtifact(root, filepath.Base(clientRecord), 32)
			clientPID, _ = strconv.Atoi(string(clientData))
			stats := guard.snapshot()
			ready = err == nil && groupErr == nil && currentHookGroup == hookGroup && hookPID > 1 && hookGroup > 1 && hookGroup != syscall.Getpgrp() && clientPID > 1 && syscall.Kill(hookPID, 0) == nil && syscall.Kill(clientPID, 0) == nil && group.Load() > 1 && driver.State() == session.WaitingTools && stats.Starts == 1 && stats.Uses == 1 && stats.Results == 0 && stats.Completions == 0 && owner.delivered.Load() == 1
			if ready {
				break wait
			}
		}
	}
	started := time.Now()
	for range 8 {
		cancel()
	}
	if !returned {
		select {
		case got = <-done:
			returned = true
		case <-time.After(8 * time.Second):
			t.Error("launcher cancellation did not join within its observation bound")
		}
	}
	latency := time.Since(started)
	stats := guard.snapshot()
	clientGone := clientPID > 1 && errors.Is(syscall.Kill(clientPID, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-clientPID, 0), syscall.ESRCH)
	hookGone := hookPID > 1 && errors.Is(syscall.Kill(hookPID, 0), syscall.ESRCH) && hookGroup > 1 && errors.Is(syscall.Kill(-hookGroup, 0), syscall.ESRCH)
	groupGone := group.Load() > 1 && errors.Is(syscall.Kill(-int(group.Load()), 0), syscall.ESRCH)
	records, recordErr := relayProcessRecords(relay)
	relayGone := recordErr == nil && len(records) == 1 && errors.Is(syscall.Kill(records[0].pid, 0), syscall.ESRCH)
	artifactMu.Lock()
	artifactsGone := len(artifacts) == 2
	for _, path := range artifacts {
		_, err := os.Lstat(path)
		artifactsGone = artifactsGone && errors.Is(err, os.ErrNotExist)
	}
	artifactMu.Unlock()
	remaining, profileErr := os.ReadDir(profiles)
	canaryAfter, canaryErr := readDenialArtifact(project, filepath.Base(filename), 128)
	sourcesOK := canaryErr == nil && string(canaryAfter) == canary && fileFingerprint(t, settings) == beforeSettings
	closed := returned && errors.Is(got.err, context.Canceled) && !errors.Is(got.err, launcher.ErrRunCleanup) && !errors.Is(got.err, childproc.ErrIO) && !errors.Is(got.err, childproc.ErrTerminal) && got.result.ClientPID == clientPID && owner.closes.Load() == 1 && driver.State() == session.Closed && pool.Stats().Processes == 0 && prepared.Load() == 1 && cleaned.Load() == 1
	t.Logf("client_shape=%+v, launcher_returned=%v, canceled=%v, deadline=%v, cleanup_error=%v, client_exit_error=%v, client_pid_observed=%v, hook_pid_observed=%v", stats.LastShape, returned, errors.Is(got.err, context.Canceled), errors.Is(got.err, context.DeadlineExceeded), errors.Is(got.err, launcher.ErrRunCleanup), errors.Is(got.err, childproc.ErrExit), clientPID > 1, hookPID > 1)
	t.Logf("delivered_http_handoffs=%d, launcher_backend_close_calls=%d", owner.delivered.Load(), owner.closes.Load())
	t.Logf("live_kiro=%v, pending_hook_and_tool_observed=%v, main_requests=%d, exposed_tools=%d, results=%d, completions=%d, prepared=%d, cleaned=%d, launcher_closed=%v, cancel_join_ms=%d, client_gone=%v, hook_gone=%v, acp_group_gone=%v, relay_gone=%v, artifacts_gone=%v, profiles_gone=%v, sources_unchanged=%v, canary_in_model_output=%v", kiro != "", ready, owner.starts.Load(), stats.Uses, stats.Results, stats.Completions, prepared.Load(), cleaned.Load(), closed, latency.Milliseconds(), clientGone, hookGone, groupGone, relayGone, artifactsGone, profileErr == nil && len(remaining) == 0, sourcesOK, stats.CanaryObserved)
	// Preserve failure before any last-resort cleanup of directly observed owned processes.
	if !ready || !closed || !clientGone || !hookGone || !groupGone || !relayGone || !artifactsGone || profileErr != nil || len(remaining) != 0 || !sourcesOK || stats.CanaryObserved || owner.starts.Load() != 1 || stats.Results != 0 || stats.Completions != 0 {
		t.Error("suspended client cancellation or joined cleanup was not established")
	}
	for _, pid := range []int{hookPID, clientPID} {
		if pid > 1 && syscall.Kill(pid, 0) == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			until := time.Now().Add(time.Second)
			for time.Now().Before(until) && syscall.Kill(pid, 0) == nil {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	if recordErr == nil {
		for _, record := range records {
			if syscall.Kill(record.pid, 0) == nil {
				_ = syscall.Kill(record.pid, syscall.SIGKILL)
			}
		}
	}
}

func buildCancellationHook(t *testing.T, ctx context.Context, runner *childproc.Runner, root string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot locate owned hook fixture")
	}
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	executable := filepath.Join(root, "held-hook")
	if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", executable, "./testdata/heldhook"}}); err != nil {
		t.Fatal("cannot build independent held hook")
	}
	return executable
}
