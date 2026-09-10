package interop_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

const denialSystemPrompt = "Independent single-tool protocol exercise. Request the listed Read tool exactly once. Do not use other tools, inspect configuration, or retry after denial. Finish with a brief acknowledgement of the denial."

func denialUserPrompt(filename string) string {
	return "Use Read once for " + filename + ". If the client refuses, do not try another route; acknowledge the refusal and finish."
}

func TestClaudeOnePromptDenialProbeWithFakeACP(t *testing.T) {
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for an owned fake-ACP denial control; no external inference")
	}
	runOnePromptDenialProbe(t, client, "")
}

// This is a separately opted-in interoperability test, never an option of the product's run command.
// Its actual Kiro request consumes account credits. See LIVE_KIRO_TEST_PLAN.md before enabling it.
func TestKiroLiveOnePromptClientDenial(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("credit-consuming Kiro test requires explicit DAX_INTEROP_KIRO_CREDIT_OPT_IN=1")
	}
	client, kiro := os.Getenv("DAX_INTEROP_CLAUDE_BINARY"), os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if client == "" || kiro == "" {
		t.Fatal("the opted-in test requires both pinned executable paths")
	}
	runOnePromptDenialProbe(t, client, kiro)
}

func runOnePromptDenialProbe(t *testing.T, clientExecutable, kiroExecutable string) {
	t.Helper()
	runClientToolProbe(t, clientExecutable, kiroExecutable, "")
}

func runClientToolProbe(t *testing.T, clientExecutable, kiroExecutable, effectKind string) {
	t.Helper()
	fullClient := effectKind == "default-read-denial"
	if fullClient {
		effectKind = ""
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-denial-probe-")
	if err != nil {
		t.Fatal("cannot create owned denial-probe root")
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal("cannot create bounded probe runner")
	}
	defer runner.Close()
	home, project, backend, worker := filepath.Join(root, "client-home"), filepath.Join(root, "client-project"), filepath.Join(root, "backend"), filepath.Join(root, "schema")
	configuration, scratch := filepath.Join(root, "kiro-home"), filepath.Join(root, "tmp")
	for _, dir := range []string{home, project, backend, worker, configuration, filepath.Join(configuration, "settings"), scratch} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot create owned probe directories")
		}
	}
	canary := "IndependentCanary" + rand.Text()
	filename, hookMarker := filepath.Join(project, "denied-fixture"), filepath.Join(root, "hook-denied")
	if os.WriteFile(filename, []byte(canary), 0600) != nil {
		t.Fatal("cannot write synthetic canary")
	}
	hook := filepath.Join(root, "deny-read.sh")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	denial, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": clientDenialReason}})
	script := "#!/bin/sh\nset -eu\numask 077\nprintf '%s\\n' denied > " + quote(hookMarker) + "\nprintf '%s' " + quote(string(denial)) + "\n"
	if os.WriteFile(hook, []byte(script), 0700) != nil {
		t.Fatal("cannot write owned denying client hook")
	}
	settings := filepath.Join(home, "settings.json")
	raw, _ := json.Marshal(map[string]any{"permissions": map[string]string{"defaultMode": "manual"}, "hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": quote(hook), "timeout": 2}}}}}})
	if os.WriteFile(settings, raw, 0600) != nil {
		t.Fatal("cannot write owned client settings")
	}
	var effect *clientEffectProbe
	var effectSystem, effectUser string
	if effectKind != "" {
		effect = prepareClientEffect(t, root, project, filename, canary, effectKind, settings)
		if kiroExecutable != "" {
			effectSystem, effectUser, err = liveEffectPrompt(effect)
			if err != nil {
				t.Fatal("invalid live tool experiment before process startup")
			}
		}
	}
	launchBudget := int32(1)
	if fullClient || effect != nil && effect.recoverNext {
		launchBudget = 2
	}
	emptyMCP := filepath.Join(root, "client-mcp.json")
	if os.WriteFile(emptyMCP, []byte(`{"mcpServers":{}}`), 0600) != nil {
		t.Fatal("cannot write empty strict client MCP configuration")
	}
	settingsBefore := fileFingerprint(t, settings)
	versionContext, stopVersion := context.WithTimeout(ctx, 5*time.Second)
	version, err := runner.Run(versionContext, childproc.Command{Executable: clientExecutable, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1"}, Args: []string{"--version"}})
	stopVersion()
	if err != nil || !launcher.CompatibleClientOutput(version.Stdout) {
		t.Fatal("client version preflight failed")
	}
	relayExecutable := buildRelayObserver(t)
	proxyExecutable := filepath.Join(filepath.Dir(relayExecutable), "owned-relay")
	models, _ := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent denial control"}}, "fixture-backend")
	process := acp.Config{Directory: backend, ClientInfo: acp.Info{Name: "dax-owned-denial-probe", Version: "1"}, Limits: acp.Limits{RequestTimeout: 45 * time.Second}}
	var execution launcher.KiroExecution
	if kiroExecutable == "" {
		process.Executable = buildDenialACPFixture(t, ctx, runner, root)
		process.Args = []string{"chat-tools-client-launch", filename}
		if fullClient {
			process.Args = []string{"chat-tools-default-client-launch", filename, filepath.Join(backend, "owned-processes")}
		}
		if effect != nil {
			process.Args = []string{"chat-tools-effect-launch", effect.manifest}
			if effect.recoverNext {
				process.Args[0] = "chat-tools-effect-restart-launch"
			}
		}
	} else {
		models, execution = prepareLiveKiroProbe(t, ctx, runner, kiroExecutable, root, backend, configuration)
		process = execution.Process
		process.Limits.RequestTimeout = 45 * time.Second
	}
	backendModel := "fixture-backend"
	if kiroExecutable != "" {
		backendModel = "auto"
	}
	model, err := models.ClientID(backendModel)
	if err != nil {
		t.Fatal("the exact probe model is absent; no alternate model is selected")
	}
	validator, err := schemacheck.New(schemacheck.Config{Executable: proxyExecutable, Directory: worker})
	if err != nil {
		t.Fatal("cannot prepare owned schema validator")
	}
	defer validator.Close()
	pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 1, SessionsPerProcess: 1, MaxIdle: 1, SetupTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare bounded ACP pool")
	}
	defer pool.Close()
	config := session.Config{Process: process, Pool: pool, InitialModel: backendModel, Validator: validator, RelayExecutable: relayExecutable, SetupTimeout: 20 * time.Second, TurnTimeout: 45 * time.Second}
	if fullClient {
		config.MaxRecreations = 1
	}
	var launchPath, relayConfig string
	var artifacts []string
	var relayGroup atomic.Int32
	var retiredBeforeRestart atomic.Bool
	var preparedCount, cleanupCount atomic.Int32
	config.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		n := preparedCount.Add(1)
		if n > launchBudget {
			return session.LaunchResources{}, errors.New("probe launch budget exhausted")
		}
		if n == 2 {
			records, recordErr := relayProcessRecords(relayExecutable)
			if recordErr != nil || len(records) != 1 || cleanupCount.Load() != 1 || !errors.Is(syscall.Kill(records[0].pid, 0), syscall.ESRCH) || relayGroup.Load() <= 1 || !errors.Is(syscall.Kill(-int(relayGroup.Load()), 0), syscall.ESRCH) {
				return session.LaunchResources{}, errors.New("old process was not retired before recovery")
			}
			for _, path := range artifacts {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					return session.LaunchResources{}, errors.New("old runtime survived recovery")
				}
			}
			retiredBeforeRestart.Store(true)
		}
		if kiroExecutable != "" {
			owned, err := execution.Prepare(ctx, input)
			if owned.Cleanup != nil {
				cleanup := owned.Cleanup
				owned.Cleanup = func() error { cleanupCount.Add(1); return cleanup() }
			}
			launchPath, relayConfig = owned.Directory, input.RelayConfig
			artifacts = append(artifacts, launchPath, relayConfig)
			return owned, err
		}
		directory, createErr := os.MkdirTemp(root, "owned-agent-")
		if createErr != nil {
			return session.LaunchResources{}, errors.New("cannot prepare owned probe agent")
		}
		launchPath = directory
		relayConfig = input.RelayConfig
		artifacts = append(artifacts, launchPath, relayConfig)
		owned := session.LaunchResources{Directory: launchPath, RelayAtLaunch: true, Cleanup: func() error { cleanupCount.Add(1); return os.RemoveAll(directory) }}
		manifest := filepath.Join(launchPath, "independent-relay.json")
		data, _ := json.Marshal(map[string]any{"name": "independent-probe-relay", "command": input.RelayExecutable, "args": []string{"relay", "--config", input.RelayConfig}, "env": []any{}})
		if os.WriteFile(manifest, data, 0600) != nil {
			return owned, errors.New("cannot prepare independent launch manifest")
		}
		owned.Args = []string{manifest}
		return owned, ctx.Err()
	}
	driver, err := session.New(config)
	if err != nil {
		t.Fatal("cannot prepare owned session driver")
	}
	defer driver.Close()
	b := &onePromptBackend{driver: driver, models: models, readPath: filename, canary: canary, observeUse: func() error {
		if fullClient {
			data, err := readDenialArtifact(project, filepath.Base(filename), 128)
			_, markerErr := os.Lstat(hookMarker)
			if err != nil || string(data) != canary || !errors.Is(markerErr, os.ErrNotExist) {
				return errors.New("default-client handoff preceded by unexpected activity")
			}
		}
		if effect != nil && !effect.beforeUse() {
			return errors.New("client handoff already has an effect or tool activity")
		}
		records, err := relayProcessRecords(relayExecutable)
		if err != nil || len(records) != 1 {
			return errors.New("one owned relay process was not observed")
		}
		group, err := syscall.Getpgid(records[0].pid)
		if err != nil || group <= 1 || group == syscall.Getpgrp() {
			return errors.New("owned relay group was not observed")
		}
		relayGroup.Store(int32(group))
		return nil
	}}
	if effect != nil {
		b.effect = effect.expect
		b.allowTitles = effect.interactive
		b.inspectResume = effect.inspectNext
		b.recoverResume = effect.recoverNext
	}
	if fullClient {
		input, _ := json.Marshal(map[string]string{"file_path": filename})
		b.fullClient = true
		b.effect = &toolEffectExpectation{Tool: "Read", Input: input, IsError: true, RequiredText: clientDenialReason}
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("cannot allocate local probe credentials")
	}
	server, err := gateway.StartServer(ctx, gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, Gateway: gateway.Config{Tokens: tokens, Backend: b, TurnTimeout: 45 * time.Second, FirstEventTimeout: 20 * time.Second, WriteTimeout: time.Minute}})
	if err != nil {
		t.Fatal("cannot prepare local probe gateway")
	}
	defer server.Close()
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: clientExecutable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL(), ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		t.Fatal("cannot prepare isolated probe client")
	}
	defer profile.Close()
	command := profile.Command()
	if !fullClient {
		command.Environment = append(command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
	}
	tool, systemText, userText := "Read", denialSystemPrompt, denialUserPrompt(filename)
	if effect != nil {
		tool, systemText, userText = effect.expect.Tool, "Independent client permission exercise.", "Perform the single declared operation, accept the client result, then finish."
		if kiroExecutable != "" {
			systemText, userText = effectSystem, effectUser
		}
	}
	command.Args = append(command.Args, "--strict-mcp-config", "--mcp-config", emptyMCP)
	if !fullClient {
		command.Args = append(command.Args, "--tools", tool)
	}
	if effect == nil || !effect.interactive {
		command.Args = append(command.Args, "--print", "--output-format", "json", "--no-session-persistence")
	}
	if fullClient {
		command.Args = append(command.Args, denialSystemPrompt+"\n"+userText)
	} else {
		command.Args = append(command.Args, "--system-prompt", systemText, userText)
	}
	clientContext, stop := context.WithTimeout(ctx, time.Minute)
	var result childproc.Result
	var runErr error
	var clientOK bool
	if effect != nil && effect.interactive {
		result, clientOK = runClientEffectTerminal(t, clientContext, root, project, profile, command, effect, b)
	} else {
		result, runErr = runner.Run(clientContext, command)
		clientOK = runErr == nil && result.ExitCode == 0
	}
	stop()
	state := driver.State()
	finalGroup := 0
	if fullClient {
		var completion struct {
			Result  string
			IsError bool `json:"is_error"`
		}
		clientOK = clientOK && json.Unmarshal(result.Stdout, &completion) == nil && !completion.IsError && strings.TrimSpace(completion.Result) != ""
		records, err := relayProcessRecords(relayExecutable)
		if err == nil && len(records) == 2 {
			finalGroup, _ = syscall.Getpgid(records[1].pid)
		}
	}
	serverErr := server.Close()
	closeErr := driver.Close()
	poolErr := pool.Close()
	stats := b.snapshot()
	records, recordErr := relayProcessRecords(relayExecutable)
	relayGone := recordErr == nil && len(records) == int(launchBudget)
	for _, record := range records {
		if !errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH) {
			relayGone = false
			_ = syscall.Kill(record.pid, syscall.SIGKILL)
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && !errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH) {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	groupGone := relayGroup.Load() > 1 && errors.Is(syscall.Kill(-int(relayGroup.Load()), 0), syscall.ESRCH)
	if fullClient {
		groupGone = groupGone && finalGroup > 1 && finalGroup != int(relayGroup.Load()) && errors.Is(syscall.Kill(-finalGroup, 0), syscall.ESRCH)
	}
	canaryAfter, canaryErr := readDenialArtifact(project, filepath.Base(filename), 128)
	marker, markerErr := readDenialArtifact(root, filepath.Base(hookMarker), 32)
	canaryUnchanged := canaryErr == nil && string(canaryAfter) == canary
	hookDenied := markerErr == nil && string(marker) == "denied\n"
	policyOK, wantDenials := hookDenied, 1
	if effect != nil {
		policyOK, wantDenials = effect.check(t), btoi(effect.expect.IsError)
	}
	if bytes.Contains(result.Stdout, []byte(canary)) {
		stats.CanaryObserved = true
	}
	for _, path := range artifacts {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Error("prepared artifact cleanup was not established")
		}
	}
	if preparedCount.Load() != launchBudget || cleanupCount.Load() != launchBudget || launchBudget == 2 && !retiredBeforeRestart.Load() {
		t.Error("prepared launch budget or joined retirement was not established")
	}
	t.Logf("acp_launches=%d, old_runtime_retired_before_recovery=%v", preparedCount.Load(), retiredBeforeRestart.Load())
	if fullClient {
		t.Logf("default_client=true, shape=%+v, exact_arguments=%+v, final_group_observed=%v", stats.LastShape, stats.Arguments, finalGroup > 1)
		t.Logf("default_continuation=%+v", stats.Continuation)
		c := stats.Continuation
		if stats.LastShape.Tools < 3 || !stats.LastShape.Thinking || !stats.LastShape.Context || !c.Seen || !c.PrefixSame || !c.IdentitySame || !c.ModelSame || !c.EffortSame || !c.SystemSame || !c.MetadataSame || !c.ToolsSame || c.TrailingSystems != 1 || c.TrailingSame {
			t.Error("default-client changed-standing-instruction shape was not established")
		}
	}
	if effect != nil {
		t.Logf("tool_argument_comparison=%+v, handoff_group_observed=%v", stats.Arguments, relayGroup.Load() > 1)
	}
	t.Logf("live_kiro=%v, initial_request_budget=1, accepted_backend_requests=%d, exposed_tool_calls=%d, matched_results=%d, matched_denials=%d, final_completions=%d, policy_effect_verified=%v, canary_unchanged=%v, canary_in_model_output=%v, relay_gone=%v, observed_group_gone=%v, client_exit=%d, client_output_bytes=%d", kiroExecutable != "", stats.Starts, stats.Uses, stats.Results, stats.Denials, stats.Completions, policyOK, canaryUnchanged, stats.CanaryObserved, relayGone, groupGone, result.ExitCode, len(result.Stdout))
	serverState := server.Stats()
	wantState, wantStarts, wantResults, wantCompletions := session.Idle, 2, 1, 1
	if effect != nil && effect.noDecision {
		wantState, wantStarts, wantResults, wantCompletions = session.WaitingTools, 1, 0, 0
	}
	if effect != nil && effect.bare {
		wantState, wantStarts, wantResults, wantCompletions, wantDenials = session.Unstarted, 1, 0, 0, 0
		if effect.inspectNext {
			wantState = session.WaitingTools
			if effect.recoverNext {
				wantState, wantStarts, wantResults, wantCompletions, wantDenials = session.Idle, 2, 1, 1, 1
			}
		}
	}
	if !clientOK || state != wantState || serverErr != nil || serverState.Connections != 0 || serverState.Handlers != 0 || closeErr != nil || poolErr != nil || pool.Stats().Processes != 0 || stats.Starts != wantStarts || stats.Uses != 1 || stats.Results != wantResults || stats.Denials != wantDenials || stats.Completions != wantCompletions || !policyOK || !canaryUnchanged || stats.CanaryObserved || !relayGone || !groupGone {
		t.Fatal("the bounded client-denial path or its cleanup was not established")
	}
	if fileFingerprint(t, settings) != settingsBefore || profile.Close() != nil {
		t.Fatal("probe source settings changed or private client cleanup failed")
	}
}

func readDenialArtifact(directory, name string, limit int) ([]byte, error) {
	store, err := privatefs.New(directory)
	if err != nil {
		return nil, err
	}
	return store.Read(name, limit)
}

func buildDenialACPFixture(t *testing.T, ctx context.Context, runner *childproc.Runner, root string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot identify independent fixture source")
	}
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	executable := filepath.Join(root, "independent-acp")
	if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", executable, "../acp/testdata/fake"}}); err != nil {
		t.Fatal("cannot build independent ACP peer")
	}
	return executable
}
