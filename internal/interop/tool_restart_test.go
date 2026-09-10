package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func TestClaudeCompletedToolNativeResumeWithFakeACP(t *testing.T) {
	for _, kind := range []string{"allow-write", "allow-bash"} {
		if !t.Run(kind, func(t *testing.T) { observeCompletedToolRestart(t, false, kind) }) {
			return
		}
	}
}

func TestKiroLiveCompletedToolNativeResume(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("completed-tool resume requires explicit Kiro credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" {
		t.Fatal("pinned Kiro path required")
	}
	for _, kind := range []string{"allow-write", "allow-bash"} {
		if !t.Run(kind, func(t *testing.T) { observeCompletedToolRestart(t, true, kind) }) {
			return
		}
	}
}

func toolRestartEffectsOnce(e *clientEffectProbe) bool {
	for _, path := range []string{e.pre, e.post} {
		data, err := readDenialArtifact(filepath.Dir(path), filepath.Base(path), 64)
		if err != nil || string(data) != "observed\n" {
			return false
		}
	}
	want := ownedEffectText
	if e.expect.Tool == "Bash" {
		want += "\n"
	}
	data, err := readDenialArtifact(filepath.Dir(e.path), filepath.Base(e.path), 128)
	return err == nil && string(data) == want
}

func observeCompletedToolRestart(t *testing.T, live bool, kind string) {
	observeNativeToolHistory(t, live, kind, "")
}

func observeNativeToolHistory(t *testing.T, live bool, kind, holdMode string) {
	observeToolHistory(t, live, kind, holdMode, "")
}

func observeToolHistory(t *testing.T, live bool, kind, holdMode, followPolicy string) {
	t.Helper()
	interactiveFollow := strings.HasPrefix(followPolicy, "ui-")
	if interactiveFollow && live {
		t.Fatal("interactive resume currently uses independent ACP only")
	}
	if followPolicy != "" && (kind != "allow-bash" || holdMode != "" && holdMode != "interrupt" && holdMode != "interrupt-preface") {
		t.Fatal("resumed policy requires an owned completed or interrupted first Bash operation")
	}
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set pinned Claude for native tool resume")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-tool-history-")
	if err != nil {
		t.Fatal("tool history root")
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute, MaxProcesses: 1, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("tool history runner")
	}
	defer runner.Close()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, path := range []string{home, filepath.Join(home, ".claude"), project} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("tool history directory")
		}
	}
	settings, global := filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json")
	if os.WriteFile(global, []byte(`{}`), 0600) != nil {
		t.Fatal("tool history source")
	}
	effect := prepareClientEffect(t, root, project, "", "", kind, settings)
	if kind == "allow-bash" {
		// Appending makes a repeated successful native command visible even if it returns no text.
		effect.expect.Input, _ = json.Marshal(map[string]string{"command": "printf '%s\\n' " + probeShellQuote(ownedEffectText) + " >> " + probeShellQuote(effect.path)})
		manifest, _ := json.Marshal(map[string]any{"input": effect.expect.Input, "isError": false, "requiredText": ""})
		if os.WriteFile(effect.manifest, manifest, 0600) != nil {
			t.Fatal("append expectation")
		}
	}
	for _, entry := range []struct{ event, marker string }{{"PreToolUse", effect.pre}, {"PostToolUse", effect.post}} {
		body := "#!/bin/sh\nset -eu\numask 077\nprintf '%s\\n' observed >> " + probeShellQuote(entry.marker) + "\nprintf '%s' '{}'\n"
		if os.WriteFile(filepath.Join(root, entry.event+"-effect.sh"), []byte(body), 0700) != nil {
			t.Fatal("counting native hook")
		}
	}
	var held *heldRestart
	if holdMode != "" {
		held = prepareHeldRestart(t, ctx, runner, root, settings, effect, holdMode)
	}
	var followEffect *clientEffectProbe
	var followSettings string
	var followFingerprint [32]byte
	if followPolicy != "" {
		followEffect, followSettings = prepareFollowupEffect(t, root, project, followPolicy)
		followFingerprint = fileFingerprint(t, followSettings)
		if held != nil && held.interrupted {
			manifest, _ := json.Marshal(map[string]any{"input": followEffect.expect.Input, "isError": followEffect.expect.IsError, "requiredText": followEffect.expect.RequiredText, "followupEffect": true, "interrupted": true, "preface": holdMode == "interrupt-preface"})
			if os.WriteFile(followEffect.manifest, manifest, 0600) != nil {
				t.Fatal("interrupted follow-up expectation")
			}
		}
	}
	beforeSettings, beforeGlobal := fileFingerprint(t, settings), fileFingerprint(t, global)
	version, err := runner.Run(ctx, childproc.Command{Executable: client, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, Args: []string{"--version"}})
	if err != nil || !launcher.CompatibleClientOutput(version.Stdout) {
		t.Fatal("unverified client")
	}
	relay := buildRelayObserver(t)
	proxy := filepath.Join(filepath.Dir(relay), "owned-relay")
	fake := ""
	if !live {
		fake = buildDenialACPFixture(t, ctx, runner, root)
	}
	system := "Follow only the current user instruction. Client tool effects require a new explicit operation in that instruction. Never repeat completed operations from conversation history. For a requested operation, use the one declared client tool exactly once with the supplied JSON arguments, without extra fields, native tools, delegation or retry. A Bash description may be one line of at most 256 bytes; do not change its command. After success, follow the requested text-only reply."
	prompts := [2]string{
		"EffectQuestion_131: Request " + effect.expect.Tool + " once with exactly this argument object: " + string(effect.expect.Input) + ". After success, reply only with the concatenation of ToolArchiveReady and _131 without spaces.",
		"EffectFollow_137: The earlier operation is complete. Continue from the prior conversation without any new tool request. Reply only with the concatenation of ToolArchiveResumed and _137 without spaces.",
	}
	var id string
	if held != nil {
		id = held.id
	}
	interrupted := held != nil && held.interrupted
	if interrupted {
		prompts[1] = pendingRestartQuestion
	}
	if followEffect != nil {
		previousState := "The earlier operation is complete."
		if interrupted {
			previousState = "The earlier operation was interrupted before execution. It must remain unexecuted."
		}
		prompts[1] = "EffectFollow_137: " + previousState + " Request Bash once for this separate new operation with exactly this argument object: " + string(followEffect.expect.Input) + ". Never repeat the earlier operation. After the client returns success or refusal, do not retry or request any other tool. Reply only with the concatenation of ToolArchiveResumed and _137 without spaces."
	}
	var previous completedToolPair
	var groups [2]int
	var profiles, endpoints [2]string
	var tokens [2]gateway.Tokens
	for stage := range 2 {
		currentEffect, currentSettings := effect, settings
		followup := stage == 1 && followEffect != nil
		if followup {
			currentEffect, currentSettings = followEffect, followSettings
		}
		stageRoot := filepath.Join(root, strconv.Itoa(stage))
		backend, worker := filepath.Join(stageRoot, "backend"), filepath.Join(stageRoot, "worker")
		for _, path := range []string{stageRoot, backend, worker} {
			if os.Mkdir(path, 0700) != nil {
				t.Fatal("tool restart stage")
			}
		}
		models, _ := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent tool resume"}}, "fixture-backend")
		backendModel := "fixture-backend"
		process := acp.Config{Executable: fake, Args: []string{"native-tool-history", strconv.Itoa(stage), currentEffect.manifest}, Directory: backend, ClientInfo: acp.Info{Name: "independent-tool-resume", Version: "1"}, Limits: acp.Limits{RequestTimeout: 45 * time.Second}}
		var execution launcher.KiroExecution
		if live {
			models, execution = prepareLiveKiroProbe(t, ctx, runner, os.Getenv("DAX_INTEROP_KIRO_BINARY"), stageRoot, backend, filepath.Join(stageRoot, "kiro-preflight"))
			process, backendModel = execution.Process, "auto"
			process.Limits.RequestTimeout = 45 * time.Second
		}
		model, err := models.ClientID(backendModel)
		if err != nil {
			t.Fatal("tool restart model")
		}
		validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: worker})
		if err != nil {
			t.Fatal("tool restart validator")
		}
		defer validator.Close()
		pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 1, SessionsPerProcess: 1, MaxIdle: 1, SetupTimeout: 20 * time.Second})
		if err != nil {
			t.Fatal("tool restart pool")
		}
		defer pool.Close()
		var prepared, cleaned atomic.Int32
		var artifacts []string
		cfg := session.Config{Process: process, Pool: pool, InitialModel: backendModel, Validator: validator, RelayExecutable: relay, SetupTimeout: 20 * time.Second, TurnTimeout: 45 * time.Second, MaxRecreations: 1}
		cfg.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
			if prepared.Add(1) != 1 {
				return session.LaunchResources{}, inference.ErrRequest
			}
			var owned session.LaunchResources
			var err error
			if live {
				owned, err = execution.Prepare(ctx, input)
			} else {
				owned.Directory, err = os.MkdirTemp(stageRoot, "agent-")
				if err != nil {
					return owned, err
				}
				owned.RelayAtLaunch = true
				directory := owned.Directory
				owned.Cleanup = func() error { return os.RemoveAll(directory) }
				manifest := filepath.Join(directory, "public-mcp.json")
				data, _ := json.Marshal(map[string]any{"name": "independent-tool-history-relay", "command": input.RelayExecutable, "args": []string{"relay", "--config", input.RelayConfig}, "env": []any{}})
				err = os.WriteFile(manifest, data, 0600)
				owned.Args = []string{manifest}
			}
			artifacts = append(artifacts, owned.Directory, input.RelayConfig)
			if owned.Cleanup != nil {
				closeOwned := owned.Cleanup
				owned.Cleanup = func() error { e := closeOwned(); cleaned.Add(1); return e }
			}
			return owned, err
		}
		manager, err := session.NewManager(session.ManagerConfig{Session: cfg, ProfileScope: "independent-tool-history", MaxSessions: 1})
		if err != nil {
			t.Fatal("tool restart manager")
		}
		defer manager.Close()
		guard := &toolRestartBackend{backend: manager, models: models, stage: stage, identity: id, expect: currentEffect.expect, previous: previous, beforeUse: currentEffect.beforeUse, interrupted: interrupted, question: prompts[0], followup: followup, followQuestion: prompts[1]}
		interactive := followup && interactiveFollow
		guard.allowTitles = interactive
		oldSafe := func() bool {
			if interrupted {
				return held.effectsAbsent(effect) && held.hookGone() && held.lateChecked
			}
			return toolRestartEffectsOnce(effect)
		}
		if followup {
			guard.beforeUse = func() bool {
				return effect != followEffect && oldSafe() && followEffect.beforeUse()
			}
		}
		guard.observeProcess = func() error {
			records, err := relayProcessRecords(relay)
			if err != nil || len(records) != stage+1 {
				return errors.New("tool restart process observation")
			}
			group, err := syscall.Getpgid(records[stage].pid)
			if err != nil || group <= 1 || group == syscall.Getpgrp() || stage == 1 && group == groups[0] || groups[stage] != 0 && group != groups[stage] {
				return errors.New("tool restart process ownership")
			}
			groups[stage] = group
			return nil
		}
		tokens[stage], err = gateway.NewTokens()
		if err != nil {
			t.Fatal("tool restart tokens")
		}
		server, err := gateway.StartServer(ctx, gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, Gateway: gateway.Config{Tokens: tokens[stage], Backend: guard, TurnTimeout: 45 * time.Second, FirstEventTimeout: 30 * time.Second, WriteTimeout: time.Minute}})
		if err != nil {
			t.Fatal("tool restart server")
		}
		defer server.Close()
		endpoints[stage] = server.URL()
		resumeID := id
		if stage == 0 {
			resumeID = ""
		}
		profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: stageRoot, Home: home, Project: project, UserSettings: currentSettings, Executable: client, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL(), ModelToken: tokens[stage].Model, KeepHistory: true, ResumeSession: resumeID, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
		if err != nil {
			t.Fatal("tool restart profile")
		}
		defer profile.Close()
		profiles[stage] = profile.Path()
		command := profile.Command()
		if held != nil && stage == 0 {
			command.Args = append(command.Args, "--session-id", id)
		}
		if !interactive {
			command.Args = append(command.Args, "--print", "--output-format", "json")
		}
		command.Args = append(command.Args, "--tools", effect.expect.Tool, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--system-prompt", system, prompts[stage])
		command.Environment = append(command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
		var result childproc.Result
		var runErr error
		uiPassed := false
		if interactive {
			result, uiPassed = runResumedToolTerminal(t, ctx, stageRoot, project, id, profile, command, followEffect, guard, interrupted, oldSafe)
		} else if held != nil && stage == 0 {
			result, runErr = runHeldRestartClient(t, ctx, runner, command, guard, held, effect)
		} else {
			result, runErr = runner.Run(ctx, command)
		}
		var response struct {
			Type, Subtype, Result string
			SessionID             string `json:"session_id"`
			IsError               bool   `json:"is_error"`
		}
		decoded := json.Unmarshal(result.Stdout, &response) == nil
		marker := "ToolArchiveReady_131"
		if stage == 1 {
			marker = "ToolArchiveResumed_137"
		}
		guard.mu.Lock()
		wantResults := 1
		if interrupted && guard.abandoned && !followup {
			wantResults = 0
		}
		wantStarts, wantUses := 2-stage, 1-stage
		if followup {
			wantStarts, wantUses = 2, 1
		}
		clientPassed := runErr == nil && result.ExitCode == 0 && decoded && response.Type == "result" && response.Subtype == "success" && !response.IsError && nativeHistoryID(response.SessionID) && response.SessionID == guard.identity && strings.Count(response.Result, marker) == 1
		if interactive {
			clientPassed = uiPassed && nativeHistoryID(id) && guard.identity == id
		}
		passed := clientPassed && strings.Count(guard.text, marker) == 1 && !guard.failed && guard.starts == wantStarts && guard.uses == wantUses && guard.results == wantResults && guard.ends == 1
		if followup {
			passed = passed && guard.handoffs == 1 && guard.historyChecks == 2 && guard.pair.failed == followEffect.expect.IsError
		}
		if interrupted && stage == 0 {
			passed = held.ready && errors.Is(runErr, context.Canceled) && !guard.failed && guard.starts == 1 && guard.uses == 1 && guard.handoffs == 1 && guard.results == 0 && guard.ends == 0 && !strings.Contains(guard.text, marker)
			previous = completedToolPair{use: guard.issued, input: canonicalToolObject(guard.issued.Input), text: guard.text}
		} else if !interactive {
			id, previous = response.SessionID, guard.pair
		}
		guard.mu.Unlock()
		serverErr, managerErr, poolErr := server.Close(), manager.Close(), pool.Close()
		validator.Close()
		profileErr := profile.Close()
		clean := serverErr == nil && managerErr == nil && poolErr == nil && profileErr == nil && prepared.Load() == 1 && cleaned.Load() == 1
		for _, path := range append(artifacts, profile.Path()) {
			_, err := os.Lstat(path)
			clean = clean && os.IsNotExist(err)
		}
		gone := groups[stage] > 1 && errors.Is(syscall.Kill(-groups[stage], 0), syscall.ESRCH) && result.PID > 1 && errors.Is(syscall.Kill(result.PID, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) && runner.Active() == 0 && pool.Stats().Processes == 0
		records, recordErr := relayProcessRecords(relay)
		gone = gone && recordErr == nil && len(records) == stage+1
		for _, record := range records {
			gone = gone && errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH)
		}
		connection, dialErr := net.DialTimeout("tcp", strings.TrimPrefix(server.URL(), "http://"), time.Second)
		if connection != nil {
			connection.Close()
		}
		gone = gone && dialErr != nil
		effectOnce := toolRestartEffectsOnce(effect)
		if held != nil {
			gone = gone && held.hookGone()
			if interrupted {
				effectOnce = held.effectsAbsent(effect)
				if stage == 1 {
					effectOnce = effectOnce && held.lateChecked
				}
			}
			if interrupted && stage == 0 && passed && clean && gone && effectOnce {
				effectOnce = held.lateRelease(ctx, effect)
			}
			t.Logf("held_mode=%s held_hook_observed=%v delivered_handoffs=%d old_hook_gone=%v initial_join_ms=%d", holdMode, held.ready, guard.handoffs, held.hookGone(), held.join.Milliseconds())
			t.Logf("abandoned_native_history=%v retained_tool_pairs=%d original_partial_text_bytes=%d resumed_assistant_text_bytes=%d native_noncompletion_placeholder=%v late_release_checked=%v", guard.abandoned, guard.results, len(guard.previous.text), guard.placeholder.AssistantBytes, guard.placeholder.NoResponseRequested, held.lateChecked)
		}
		if followEffect != nil {
			oldSafe := effectOnce
			if followup {
				effectOnce = effectOnce && followupEffectMatches(followEffect)
			} else {
				effectOnce = effectOnce && followEffect.beforeUse()
			}
			t.Logf("resumed_policy=%s old_effect_safe=%v history_checks=%d fresh_result_failed=%v", followPolicy, oldSafe, guard.historyChecks, guard.pair.failed)
		}
		sources := fileFingerprint(t, settings) == beforeSettings && fileFingerprint(t, global) == beforeGlobal
		if followEffect != nil {
			sources = sources && fileFingerprint(t, followSettings) == followFingerprint
		}
		if !passed {
			t.Logf("public_request_shape=%+v", guard.shape)
			if interrupted {
				t.Logf("interrupted_history_shape=%+v native_interruption=%+v block_kinds=%v native_placeholder=%+v", guard.interruptionShape, guard.nativeInterruption, guard.blockKinds, guard.placeholder)
			}
		}
		if !live {
			data, readErr := readDenialArtifact(filepath.Dir(currentEffect.manifest), "peer-stage-"+strconv.Itoa(stage), 64)
			fields := strings.Fields(string(data))
			step := -1
			if readErr == nil && len(fields) == 2 {
				step, _ = strconv.Atoi(fields[1])
			}
			t.Logf("independent_peer_stage=%d", step)
		}
		t.Logf("live=%v tool=%s stage=%d expected_outcome=%v requests=%d tool_handoffs=%d matched_pairs=%d end_turns=%d expected_effect_and_hooks=%v cleanup=%v processes_profile_listener_gone=%v sources_unchanged=%v guard_failed=%v prepared=%d relay_records=%d group_observed=%v client_exit=%d", live, effect.expect.Tool, stage+1, passed, guard.starts, guard.uses, guard.results, guard.ends, effectOnce, clean, gone, sources, guard.failed, prepared.Load(), len(records), groups[stage] > 1, result.ExitCode)
		if !passed || !effectOnce || !clean || !gone || !sources {
			t.Fatal("completed-tool native restart failed; next stage not dispatched")
		}
		if os.RemoveAll(stageRoot) != nil {
			t.Fatal("tool restart stage cleanup")
		}
	}
	if groups[0] == groups[1] || profiles[0] == profiles[1] || endpoints[0] == endpoints[1] || tokens[0].Model == tokens[1].Model {
		t.Fatal("tool restart ownership reused")
	}
}

func TestToolRestartEffectWitnessRejectsRepeatedEffects(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private counter fixture")
	}
	e := &clientEffectProbe{pre: filepath.Join(root, "pre"), post: filepath.Join(root, "post"), path: filepath.Join(root, "effect"), expect: &toolEffectExpectation{Tool: "Bash"}}
	for _, path := range []string{e.pre, e.post} {
		if os.WriteFile(path, []byte("observed\n"), 0600) != nil {
			t.Fatal("counter fixture")
		}
	}
	if os.WriteFile(e.path, []byte(ownedEffectText+"\n"), 0600) != nil || !toolRestartEffectsOnce(e) {
		t.Fatal("single effect rejected")
	}
	for _, path := range []string{e.pre, e.post, e.path} {
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("counter fixture read")
		}
		if os.WriteFile(path, append(append([]byte{}, original...), original...), 0600) != nil || toolRestartEffectsOnce(e) {
			t.Fatal("repeated hook or effect accepted")
		}
		if os.WriteFile(path, original, 0600) != nil {
			t.Fatal("counter fixture restore")
		}
	}
}
