package interop_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func TestClaudeProcessLossAndFreshRequestWithFakeACP(t *testing.T) {
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for local process-loss control; no external inference")
	}
	runProcessLossProbe(t, client, "")
}

func TestKiroLiveProcessLossAndFreshRequest(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("credit-consuming process-loss test requires explicit DAX_INTEROP_KIRO_CREDIT_OPT_IN=1")
	}
	client, kiro := os.Getenv("DAX_INTEROP_CLAUDE_BINARY"), os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if client == "" || kiro == "" {
		t.Fatal("live process loss requires both pinned executable paths")
	}
	runProcessLossProbe(t, client, kiro)
}

func runProcessLossProbe(t *testing.T, client, kiro string) {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "dax-loss-probe-")
	if err != nil {
		t.Fatal("cannot prepare process-loss root")
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("cannot prepare bounded loss runner")
	}
	defer runner.Close()
	home, project, backend, worker, configuration := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "backend"), filepath.Join(root, "worker"), filepath.Join(root, "kiro-home")
	for _, dir := range []string{home, project, backend, worker, configuration} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot prepare owned loss directories")
		}
	}
	versionCtx, stopVersion := context.WithTimeout(ctx, 5*time.Second)
	version, err := runner.Run(versionCtx, childproc.Command{Executable: client, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "DISABLE_AUTOUPDATER=1"}, Args: []string{"--version"}})
	stopVersion()
	if err != nil || !launcher.CompatibleClientOutput(version.Stdout) {
		t.Fatal("unverified client for process-loss probe")
	}
	filename, hookMarker := filepath.Join(project, "unread-fixture"), filepath.Join(root, "unexpected-client-read")
	canary := "IndependentLossCanary" + rand.Text()
	hook := filepath.Join(root, "deny-read.sh")
	denial, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": clientDenialReason}})
	script := "#!/bin/sh\nset -eu\numask 077\nprintf '%s' observed > " + probeShellQuote(hookMarker) + "\nprintf '%s' " + probeShellQuote(string(denial)) + "\n"
	if os.WriteFile(hook, []byte(script), 0700) != nil || os.WriteFile(filename, []byte(canary), 0600) != nil {
		t.Fatal("cannot prepare effect-free loss fixtures")
	}
	settings, mcp := filepath.Join(home, "settings.json"), filepath.Join(root, "empty-mcp.json")
	raw, _ := json.Marshal(map[string]any{"permissions": map[string]string{"defaultMode": "manual"}, "hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": probeShellQuote(hook), "timeout": 2}}}}}})
	if os.WriteFile(settings, raw, 0600) != nil || os.WriteFile(mcp, []byte(`{"mcpServers":{}}`), 0600) != nil {
		t.Fatal("cannot prepare loss client settings")
	}
	settingsBefore := fileFingerprint(t, settings)
	relay := buildRelayObserver(t)
	proxy := filepath.Join(filepath.Dir(relay), "owned-relay")
	models, _ := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent recovery backend"}}, "fixture-backend")
	process := acp.Config{Directory: backend, ClientInfo: acp.Info{Name: "independent-loss-probe", Version: "1"}, Limits: acp.Limits{RequestTimeout: 45 * time.Second}}
	var execution launcher.KiroExecution
	backendModel := "fixture-backend"
	if kiro == "" {
		process.Executable = buildDenialACPFixture(t, ctx, runner, root)
	} else {
		models, execution = prepareLiveKiroProbe(t, ctx, runner, kiro, root, backend, configuration)
		process, backendModel = execution.Process, "auto"
		process.Limits.RequestTimeout = 45 * time.Second
	}
	model, err := models.ClientID(backendModel)
	if err != nil {
		t.Fatal("exact loss probe model unavailable")
	}
	validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: worker})
	if err != nil {
		t.Fatal("cannot prepare loss validator")
	}
	defer validator.Close()
	pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 1, SessionsPerProcess: 1, MaxIdle: 1, SetupTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare loss pool")
	}
	defer pool.Close()
	var prepared, cleaned, firstGroup atomic.Int32
	var retiredBeforeReplacement atomic.Bool
	var artifactMu sync.Mutex
	var artifacts []string
	oldGone := func() bool {
		group := int(firstGroup.Load())
		if group <= 1 || cleaned.Load() != 1 || !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			return false
		}
		records, err := relayProcessRecords(relay)
		if err != nil || len(records) != 1 || !errors.Is(syscall.Kill(records[0].pid, 0), syscall.ESRCH) {
			return false
		}
		artifactMu.Lock()
		defer artifactMu.Unlock()
		if len(artifacts) != 2 {
			return false
		}
		for _, path := range artifacts {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return false
			}
		}
		return true
	}
	cfg := session.Config{Process: process, Pool: pool, InitialModel: backendModel, Validator: validator, RelayExecutable: relay, SetupTimeout: 20 * time.Second, TurnTimeout: 45 * time.Second, MaxRecreations: 1}
	cfg.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		n := prepared.Add(1)
		if n > 2 {
			return session.LaunchResources{}, errors.New("loss probe launch budget exceeded")
		}
		if n == 2 {
			if !oldGone() {
				return session.LaunchResources{}, errors.New("old loss resources not joined")
			}
			retiredBeforeReplacement.Store(true)
		}
		var owned session.LaunchResources
		var err error
		if kiro != "" {
			owned, err = execution.Prepare(ctx, input)
		} else {
			owned.Directory, err = os.MkdirTemp(root, "owned-agent-")
			if err != nil {
				return owned, errors.New("cannot prepare loss agent")
			}
			owned.RelayAtLaunch = true
			owned.Cleanup = func() error { return os.RemoveAll(owned.Directory) }
			manifest := filepath.Join(owned.Directory, "independent-relay.json")
			data, _ := json.Marshal(map[string]any{"name": "independent-loss-relay", "command": input.RelayExecutable, "args": []string{"relay", "--config", input.RelayConfig}, "env": []any{}})
			err = os.WriteFile(manifest, data, 0600)
			mode := "chat-tools-client-launch"
			if n == 2 {
				mode = "chat-tools-recovery-launch"
			}
			owned.Args = []string{mode, filename, manifest}
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
		t.Fatal("cannot prepare loss driver")
	}
	defer driver.Close()
	input, _ := json.Marshal(map[string]string{"file_path": filename})
	guard := &onePromptBackend{driver: driver, models: models, readPath: filename, canary: canary, effect: &toolEffectExpectation{Tool: "Read", Input: input, IsError: true}}
	b := &processLossBackend{guard: guard}
	b.lose = func(ctx context.Context) error {
		records, err := relayProcessRecords(relay)
		if err != nil || len(records) != 1 {
			return errors.New("owned loss relay unavailable")
		}
		group, err := syscall.Getpgid(records[0].pid)
		if err != nil || group <= 1 || group == syscall.Getpgrp() {
			return errors.New("owned loss group unavailable")
		}
		firstGroup.Store(int32(group))
		if syscall.Kill(-group, syscall.SIGKILL) != nil {
			return errors.New("cannot terminate owned ACP group")
		}
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if driver.State() == session.Unstarted && oldGone() && pool.Stats().Processes == 0 {
				return nil
			}
			select {
			case <-bounded.Done():
				return bounded.Err()
			case <-tick.C:
			}
		}
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("cannot allocate loss gateway credentials")
	}
	server, err := gateway.StartServer(ctx, gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, Gateway: gateway.Config{Tokens: tokens, Backend: b, TurnTimeout: 45 * time.Second, FirstEventTimeout: 20 * time.Second, WriteTimeout: time.Minute}})
	if err != nil {
		t.Fatal("cannot prepare loss gateway")
	}
	defer server.Close()
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: client, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL(), ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin", "TERM=dumb"}})
	if err != nil {
		t.Fatal("cannot prepare loss client profile")
	}
	defer profile.Close()
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		t.Fatal("cannot allocate owned session ID")
	}
	uuid[6], uuid[8] = (uuid[6]&15)|64, (uuid[8]&63)|128
	sessionID := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
	command := profile.Command()
	command.Environment = append(command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
	command.Args = append(command.Args, "--print", "--output-format", "json", "--no-session-persistence", "--session-id", sessionID, "--strict-mcp-config", "--mcp-config", mcp, "--tools", "Read", "--system-prompt", processLossSystem)
	run := func(prompt string) (childproc.Result, error) {
		c := command
		c.Args = append(append([]string(nil), command.Args...), prompt)
		bounded, stop := context.WithTimeout(ctx, time.Minute)
		defer stop()
		return runner.Run(bounded, c)
	}
	first, firstErr := run(denialUserPrompt(filename))
	var firstOutput struct {
		IsError bool `json:"is_error"`
		Result  string
	}
	firstJSON := json.Unmarshal(first.Stdout, &firstOutput) == nil
	firstClientFailed := errors.Is(firstErr, childproc.ErrExit) && !errors.Is(firstErr, childproc.ErrCleanup) && first.ExitCode != 0 && firstJSON && firstOutput.IsError
	firstClientGone := first.PID > 1 && errors.Is(syscall.Kill(-first.PID, 0), syscall.ESRCH)
	_, hookErr := os.Lstat(hookMarker)
	noHook := errors.Is(hookErr, os.ErrNotExist)
	firstOK := firstClientFailed && firstClientGone && b.failureObserved.Load() && b.starts.Load() == 1 && guard.snapshot().Uses == 1 && oldGone() && pool.Stats().Processes == 0 && noHook && !bytes.Contains(first.Stdout, []byte(canary)) && !guard.snapshot().CanaryObserved
	b.mu.Lock()
	t.Logf("process_loss_error_shape=%+v", b.lossShape)
	b.mu.Unlock()
	t.Logf("live_kiro=%v, first_exit=%d, first_json=%v, first_error_result=%v, first_stdout_bytes=%d, actual_backend_failure=%v, first_client_gone=%v, retired_before_new_request=%v, intercepted_read=%d, client_hook_absent=%v, first_phase_passed=%v", kiro != "", first.ExitCode, firstJSON, firstOutput.IsError, len(first.Stdout), b.failureObserved.Load(), firstClientGone, oldGone(), guard.snapshot().Uses, noHook, firstOK)
	if !firstOK {
		t.Fatal("process loss did not produce a clean client failure; recovery is not dispatched")
	}
	b.recoveryAllowed.Store(true)
	second, secondErr := run(processLossRecoveryPrompt)
	var secondOutput struct {
		IsError bool `json:"is_error"`
		Result  string
	}
	secondJSON := json.Unmarshal(second.Stdout, &secondOutput) == nil
	secondClientOK := secondErr == nil && second.ExitCode == 0 && secondJSON && !secondOutput.IsError && strings.TrimSpace(secondOutput.Result) != "" && !bytes.Contains(second.Stdout, []byte(canary))
	if kiro == "" {
		secondClientOK = secondClientOK && secondOutput.Result == "Independent recovery complete."
	}
	secondClientGone := second.PID > 1 && errors.Is(syscall.Kill(-second.PID, 0), syscall.ESRCH)
	records, recordErr := relayProcessRecords(relay)
	secondGroup := 0
	if recordErr == nil && len(records) == 2 {
		secondGroup, _ = syscall.Getpgid(records[1].pid)
	}
	idle := driver.State() == session.Idle
	serverErr, driverErr, poolErr := server.Close(), driver.Close(), pool.Close()
	b.mu.Lock()
	policySame := b.policySame
	t.Logf("recovery_request_shape=%+v, comparison=%+v", b.recoveryShape, b.comparison)
	t.Logf("system_comparison=%+v", b.systemShape)
	b.first = nil
	b.mu.Unlock()
	groupsGone := firstGroup.Load() > 1 && secondGroup > 1 && secondGroup != int(firstGroup.Load()) && errors.Is(syscall.Kill(-int(firstGroup.Load()), 0), syscall.ESRCH) && errors.Is(syscall.Kill(-secondGroup, 0), syscall.ESRCH)
	relaysGone := recordErr == nil && len(records) == 2
	for _, record := range records {
		relaysGone = relaysGone && errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH)
	}
	artifactMu.Lock()
	artifactsGone := len(artifacts) == 4
	for _, path := range artifacts {
		_, err := os.Lstat(path)
		artifactsGone = artifactsGone && errors.Is(err, os.ErrNotExist)
	}
	artifactMu.Unlock()
	_, hookErr = os.Lstat(hookMarker)
	canaryAfter, canaryErr := readDenialArtifact(project, filepath.Base(filename), 128)
	sourcesOK := canaryErr == nil && string(canaryAfter) == canary && fileFingerprint(t, settings) == settingsBefore && errors.Is(hookErr, os.ErrNotExist)
	profileErr := profile.Close()
	_, profileGoneErr := os.Lstat(profile.Path())
	clean := serverErr == nil && driverErr == nil && poolErr == nil && profileErr == nil && errors.Is(profileGoneErr, os.ErrNotExist) && server.Stats().Connections == 0 && server.Stats().Handlers == 0 && pool.Stats().Processes == 0
	t.Logf("fresh_client_complete=%v, fresh_json=%v, fresh_exit=%d, fresh_stdout_bytes=%d, owned_policy_preserved=%v, main_requests=%d, prepared=%d, cleaned=%d, old_cleanup_before_replacement=%v, fresh_completions=%d, model_text_events=%d, driver_idle_before_close=%v, second_client_gone=%v, acp_groups_gone=%v, relays_gone=%v, artifacts_gone=%v, sources_unchanged=%v, runtime_cleanup=%v", secondClientOK, secondJSON, second.ExitCode, len(second.Stdout), policySame, b.starts.Load(), prepared.Load(), cleaned.Load(), retiredBeforeReplacement.Load(), b.completions.Load(), b.textEvents.Load(), idle, secondClientGone, groupsGone, relaysGone, artifactsGone, sourcesOK, clean)
	if !secondClientOK || !secondClientGone || !policySame || !retiredBeforeReplacement.Load() || b.starts.Load() != 2 || b.completions.Load() != 1 || prepared.Load() != 2 || cleaned.Load() != 2 || !idle || !groupsGone || !relaysGone || !artifactsGone || !sourcesOK || !clean {
		t.Error("fresh request did not recover after observed process loss")
	}
	for _, record := range records {
		if syscall.Kill(record.pid, 0) == nil {
			_ = syscall.Kill(record.pid, syscall.SIGKILL)
		}
	}
}
