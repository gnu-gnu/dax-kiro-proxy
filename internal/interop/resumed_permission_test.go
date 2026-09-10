package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/privatefs"
)

func TestClaudeResumedInteractiveToolPolicyWithFakeACP(t *testing.T) {
	for _, policy := range []string{"ui-allow-bash", "ui-deny-bash", "ui-hook-bash"} {
		if !t.Run(policy, func(t *testing.T) { observeToolHistory(t, false, "allow-bash", "", policy) }) {
			return
		}
	}
}

// This observes delivery of the current public handoff, not the manager's private session state.
func (b *toolRestartBackend) resumedHandoffPending(identity string, interrupted bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return nativeHistoryID(identity) && b.identity == identity && b.stage == 1 && b.followup && b.interrupted == interrupted && b.abandoned == interrupted && !b.failed &&
		b.starts == 1 && b.historyChecks == 1 && b.uses == 1 && b.handoffs == 1 && b.results == 0 && b.ends == 0 &&
		b.expect != nil && b.issued.Valid() && b.expect.matches(b.issued) && b.previous.use.ID != "" && b.issued.ID != b.previous.use.ID
}

func (b *toolRestartBackend) resumedOperationCompleted(identity string, interrupted bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return nativeHistoryID(identity) && b.identity == identity && b.stage == 1 && b.followup && b.interrupted == interrupted && b.abandoned == interrupted && !b.failed &&
		b.starts == 2 && b.historyChecks == 2 && b.uses == 1 && b.handoffs == 1 && b.results == 1 && b.ends == 1 &&
		b.expect != nil && b.pair.failed == b.expect.IsError && strings.Count(b.text, "ToolArchiveResumed_137") == 1
}

func TestResumedPermissionRequiresCurrentDeliveredHandoff(t *testing.T) {
	const identity = "e617c3b8-a732-4e19-84e5-91fb249d00d8"
	for _, interrupted := range []bool{false, true} {
		for _, kind := range []string{"current", "other-session", "initial-stage", "no-followup", "wrong-history-mode", "unproven-history-kind", "failed", "old-id", "wrong-command", "no-history", "no-handoff", "no-use", "already-returned", "extra-request", "ended"} {
			t.Run("interrupted="+strconv.FormatBool(interrupted)+"/"+kind, func(t *testing.T) {
				expect := &toolEffectExpectation{Tool: "Bash", Input: json.RawMessage(`{"command":"printf fresh >> /owned/fresh"}`)}
				b := &toolRestartBackend{identity: identity, stage: 1, followup: true, interrupted: interrupted, abandoned: interrupted, starts: 1, historyChecks: 1, uses: 1, handoffs: 1, expect: expect,
					previous: completedToolPair{use: anthropic.ToolUse{ID: "old-owned-call"}}, issued: anthropic.ToolUse{ID: "fresh-owned-call", Name: "Bash", Input: expect.Input}}
				switch kind {
				case "other-session":
					b.identity = "0271486e-7fa0-47ca-9225-c32a67dd597c"
				case "initial-stage":
					b.stage = 0
				case "no-followup":
					b.followup = false
				case "wrong-history-mode":
					b.interrupted = !interrupted
				case "unproven-history-kind":
					b.abandoned = !interrupted
				case "failed":
					b.failed = true
				case "old-id":
					b.issued.ID = b.previous.use.ID
				case "wrong-command":
					b.issued.Input = json.RawMessage(`{"command":"printf old >> /owned/old"}`)
				case "no-history":
					b.historyChecks = 0
				case "no-handoff":
					b.handoffs = 0
				case "no-use":
					b.uses = 0
				case "already-returned":
					b.results = 1
				case "extra-request":
					b.starts = 2
				case "ended":
					b.ends = 1
				}
				if b.resumedHandoffPending(identity, interrupted) != (kind == "current") {
					t.Fatal("permission input admitted without the current exact handoff")
				}
			})
		}
	}
}

func TestResumedPermissionTitlesDoNotConsumeHistory(t *testing.T) {
	b := &toolRestartBackend{allowTitles: true, identity: "owned-original-identity", stage: 1, followup: true}
	r := &anthropic.Request{Model: "fixture-model", System: []anthropic.Block{{Type: "text", Text: "Generate a conversation title."}}, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "Independent title input"}}}}, Extra: map[string]json.RawMessage{"output_config": json.RawMessage(`{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}}`)}}
	for range 2 {
		turn, err := b.Start(t.Context(), r)
		if err != nil {
			t.Fatal("bounded title rejected")
		}
		turn.Finish()
	}
	if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("title budget exceeded")
	}
	if b.starts != 0 || b.historyChecks != 0 || b.handoffs != 0 || b.results != 0 || b.identity != "owned-original-identity" || b.failed {
		t.Fatal("title changed main history evidence")
	}
}

func runResumedToolTerminal(t *testing.T, parent context.Context, root, project, identity string, profile *launcher.ClientProfile, command childproc.Command, effect *clientEffectProbe, backend *toolRestartBackend, interrupted bool, oldSafe func() bool) (childproc.Result, bool) {
	t.Helper()
	const lifetime = 25 * time.Second
	ctx, cancel := context.WithTimeout(parent, lifetime)
	defer cancel()
	owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: lifetime})
	if err != nil {
		t.Fatal("resumed permission terminal owner")
	}
	defer owner.Close()
	state, err := privatefs.Open(filepath.Join(profile.Path(), "client"))
	trusted, encodeErr := json.Marshal(map[string]any{"projects": map[string]any{project: map[string]bool{"hasTrustDialogAccepted": true}}})
	if err != nil || encodeErr != nil || state.Write(".claude.json", trusted) != nil {
		t.Fatal("owned resumed project trust")
	}
	for i, entry := range command.Environment {
		if strings.HasPrefix(entry, "TERM=") {
			command.Environment[i] = "TERM=xterm-256color"
		}
	}
	command.Environment = append(command.Environment, "COLUMNS=160", "LINES=40")
	pidPath, wrapper := filepath.Join(root, "resumed-client.pid"), filepath.Join(root, "resumed-client.sh")
	script := "#!/bin/sh\nset -euC\numask 077\nprintf '%s' \"$$\" > " + probeShellQuote(pidPath) + "\n/bin/stty rows 40 cols 160\nexec " + probeShellQuote(command.Executable) + " \"$@\"\n"
	if os.WriteFile(wrapper, []byte(script), 0700) != nil {
		t.Fatal("owned resumed terminal wrapper")
	}
	command.Executable = "/usr/bin/script"
	command.Args = append([]string{"-q", os.DevNull, "/bin/sh", wrapper}, command.Args...)
	var input struct{ Command string }
	if json.Unmarshal(effect.expect.Input, &input) != nil || input.Command == "" {
		t.Fatal("resumed terminal command expectation")
	}
	hookVeto := effect.kind == "ui-hook-bash"
	p := &permissionScreenProbe{tool: "Bash", file: filepath.Base(effect.path), content: ownedEffectText, command: input.Command, deny: effect.expect.IsError, delay: 500 * time.Millisecond}
	var visibleCompletion atomic.Bool
	menuSeen, oldChanged := false, false
	type outcome struct {
		result  childproc.Result
		answers int
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		result, answers, err := runObservedTerminal(ctx, owner, command, nil, func(screen string) string {
			pending := backend.resumedHandoffPending(identity, interrupted)
			if oldSafe == nil || !oldSafe() {
				oldChanged = true
			}
			lower := strings.ToLower(screen)
			menuSeen = menuSeen || strings.Contains(lower, strings.ToLower(input.Command)) && strings.Contains(lower, "do you want to proceed")
			if pending && (p.stage == 0 || p.deny) {
				for _, path := range []string{effect.path, effect.post} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						p.unexpectedEffect = true
					}
				}
			}
			if backend.resumedOperationCompleted(identity, interrupted) && strings.Contains(screen, "ToolArchiveResumed_137") {
				visibleCompletion.Store(true)
			}
			return p.next(screen, pending && !hookVeto && !p.unexpectedEffect && !oldChanged)
		}, true)
		done <- outcome{result, answers, err}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	pid, group := 0, 0
	owned, completed, exited := false, false, false
	var got outcome
wait:
	for {
		select {
		case got = <-done:
			exited = true
			break wait
		case <-ctx.Done():
			break wait
		case <-tick.C:
			if !owned {
				raw, readErr := readDenialArtifact(root, filepath.Base(pidPath), 32)
				if readErr == nil {
					var parseErr, groupErr error
					pid, parseErr = strconv.Atoi(string(raw))
					if parseErr == nil && pid > 1 {
						group, groupErr = syscall.Getpgid(pid)
						owned = groupErr == nil && group == pid && group != syscall.Getpgrp()
					}
				}
			}
			if owned && visibleCompletion.Load() && backend.resumedOperationCompleted(identity, interrupted) {
				completed = true
				break wait
			}
		}
	}
	if owned {
		_ = syscall.Kill(-group, syscall.SIGTERM)
		until := time.Now().Add(250 * time.Millisecond)
		for time.Now().Before(until) && !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			time.Sleep(10 * time.Millisecond)
		}
		if !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
	}
	cancel()
	if !exited {
		got = <-done
	}
	owner.Close()
	until := time.Now().Add(time.Second)
	for owned && time.Now().Before(until) && !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
		time.Sleep(10 * time.Millisecond)
	}
	groupGone := owned && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
	_, localSettingsErr := os.Lstat(filepath.Join(project, ".claude", "settings.local.json"))
	noSavedRule := os.IsNotExist(localSettingsErr)
	wantStage := 1
	if p.deny {
		wantStage = 4
	}
	decision := p.seen && menuSeen && p.stage == wantStage
	if hookVeto {
		decision = !p.seen && !menuSeen && p.stage == 0
	}
	valid := completed && decision && !p.unexpectedEffect && !oldChanged && groupGone && noSavedRule && owner.Active() == 0 && backend.titleAttempts.Load() <= 2 &&
		!errors.Is(got.err, childproc.ErrCleanup) && !errors.Is(got.err, childproc.ErrIO) && !errors.Is(got.err, childproc.ErrOutputLimit)
	t.Logf("resumed_permission_menu=%v decision_stage=%d hook_veto=%v visible_completion=%v old_effect_changed=%v new_effect_before_approval=%v native_group_gone=%v project_rule_absent=%v setup_stage=%d terminal_bytes=%d auxiliary_titles=%d markers=%v", menuSeen, p.stage, hookVeto, completed, oldChanged, p.unexpectedEffect, groupGone, noSavedRule, got.answers, len(got.result.Stdout), backend.titleAttempts.Load(), p.markers)
	return got.result, valid
}
