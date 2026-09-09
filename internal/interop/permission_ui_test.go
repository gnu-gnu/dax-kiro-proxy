package interop_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/session"
)

func TestPermissionProbeTitleCannotConsumeToolTurn(t *testing.T) {
	b, f := newDenialBackendFixture()
	b.allowTitles = true
	r := &anthropic.Request{Model: "fixture-model", System: []anthropic.Block{{Type: "text", Text: "Generate a conversation title."}}, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "Independent title input"}}}}, Extra: map[string]json.RawMessage{"output_config": json.RawMessage(`{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}}`)}}
	for range 2 {
		if _, err := b.Start(t.Context(), r); err != nil {
			t.Fatal("bounded title was rejected")
		}
	}
	if _, err := b.Start(t.Context(), r); err == nil {
		t.Fatal("title budget was not enforced")
	}
	if f.starts != 0 || b.attempts.Load() != 0 || b.snapshot().Titles != 2 {
		t.Fatal("title contaminated main-turn evidence")
	}
	if _, err := b.Start(t.Context(), denialRequestFixture("", "", false)); err != nil || f.starts != 1 {
		t.Fatal("title consumed the permitted tool turn")
	}
}

func TestPermissionScreenRequiresOwnedOperationAndOneTimeChoice(t *testing.T) {
	const screen = "Create file\neffect-fixture\n1 owned client effect\nDo you want to create effect-fixture?\n❯ 1. Yes\n2. Yes, allow all edits\n3. No\n"
	for _, c := range []struct {
		name, screen string
		pending      bool
		want         string
	}{
		{"exact", screen, true, "\r"},
		{"not-pending", screen, false, ""},
		{"wrong-file", strings.ReplaceAll(screen, "effect-fixture", "another-file"), true, ""},
		{"wrong-content", strings.ReplaceAll(screen, "owned client effect", "different content"), true, ""},
		{"persistent-choice", strings.ReplaceAll(screen, "❯ 1. Yes", "1. Yes\n❯ 2. Yes, allow all edits"), true, ""},
		{"no-question", strings.ReplaceAll(screen, "Do you want to create effect-fixture?", ""), true, ""},
		{"unselected", strings.ReplaceAll(screen, "❯ 1. Yes", "1. Yes"), true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &permissionScreenProbe{tool: "Write", file: "effect-fixture", content: ownedEffectText}
			if got := p.next(c.screen, c.pending); got != c.want {
				t.Fatal("permission screen accepted an unverified action or rejected its control")
			}
			if c.want != "" && p.next(c.screen, c.pending) != "" {
				t.Fatal("stale screen caused a repeated approval")
			}
		})
	}
}

func TestPermissionScreenWaitsAndDenialRequiresNewSelectedFrames(t *testing.T) {
	const base = "Create file\neffect-fixture\n1 owned client effect\nDo you want to create effect-fixture?\n"
	const yes = base + "❯ 1. Yes\n2. Yes, allow all edits\n3. No\n"
	clock := time.Unix(1, 0)
	p := &permissionScreenProbe{tool: "Write", file: "effect-fixture", content: ownedEffectText, delay: time.Second, noDecision: true, now: func() time.Time { return clock }}
	if p.next(yes, true) != "" || p.held.Load() {
		t.Fatal("no-input window was not observed")
	}
	clock = clock.Add(time.Second)
	if p.next(yes, true) != "" || !p.held.Load() || p.stage != 0 {
		t.Fatal("no-input control typed a decision or missed the held prompt")
	}
	p = &permissionScreenProbe{tool: "Write", file: "effect-fixture", content: ownedEffectText, deny: true}
	for _, c := range []struct{ screen, keys string }{
		{yes, "\x1b[B"}, {yes, ""},
		{base + "1. Yes\n❯ 2. Yes, allow all edits\n3. No\n", "\x1b[B"},
		{base + "1. Yes\n2. Yes, allow all edits\n❯ 3. No\n", "\t"},
		{base + "1. Yes\n2. Yes, allow all edits\n❯ 3. No\n", ""},
		{base + "❯ 3. No\nTell Claude the reason:\n", clientDenialReason},
		{base + "❯ 3. No\nTell Claude the reason:\n", ""},
		{base + "❯ 3. No\nTell Claude the reason: " + clientDenialReason + "\n", "\r"},
	} {
		if p.next(c.screen, true) != c.keys {
			t.Fatal("denial input did not require the current selected option and comment echo")
		}
	}
	if p.stage != 4 {
		t.Fatal("comment denial was not submitted exactly once")
	}
	erased := statusTerminalScreen([]byte(yes + "\x1b[2J\x1b[HUnrelated screen"))
	p = &permissionScreenProbe{tool: "Write", file: "effect-fixture", content: ownedEffectText}
	if p.next(erased, true) != "" {
		t.Fatal("erased permission menu authorized input")
	}
}

type permissionScreenProbe struct {
	lastInputScreen              [32]byte
	deny, noDecision             bool
	held                         atomic.Bool
	delay                        time.Duration
	ready                        time.Time
	now                          func() time.Time
	lastChoice                   int
	tool, file, content, command string
	stage                        int
	seen, unexpectedEffect       bool
	completed                    atomic.Bool
	markers                      map[string]bool
}

var oneTimeYes = regexp.MustCompile(`(?im)^\s*❯\s*(?:1\.\s*)?yes\s*$`)
var selectedPermission = regexp.MustCompile(`(?im)^\s*❯\s*(\d)\.\s*([^\n]+)`)

func (p *permissionScreenProbe) next(screen string, pending bool) string {
	frame := sha256.Sum256([]byte(screen))
	lower := strings.ToLower(screen)
	if p.markers == nil {
		p.markers = map[string]bool{}
	}
	for _, marker := range []string{"create file", "do you want", "effect-fixture", "owned client effect", "yes", "no", "allow", "permission", "error", "custom api", "welcome", "trust", "independent client effect complete", "400", "401", "404", "thinking", "context_management", "stop_sequences", "tools", "invalid", "unsupported", "max_tokens", "output_config", "model", "temperature", "comment", "tell claude", "instead", "why", "tab", "do you want to proceed"} {
		p.markers[marker] = p.markers[marker] || strings.Contains(lower, marker)
	}
	if strings.Contains(lower, "independent client effect complete") {
		p.completed.Store(true)
	}
	operation := strings.Contains(lower, p.file) && strings.Contains(lower, p.content)
	if p.tool == "Write" {
		operation = operation && strings.Contains(lower, "create file") && strings.Contains(lower, "do you want to create")
	}
	if p.tool == "Bash" {
		operation = operation && strings.Contains(lower, p.command) && strings.Contains(lower, "do you want to proceed")
	}
	if !pending || !operation {
		p.ready = time.Time{}
		return ""
	}
	if p.stage != 0 && frame == p.lastInputScreen {
		return ""
	}
	if p.stage == 0 {
		if !oneTimeYes.MatchString(screen) {
			return ""
		}
		p.seen = true
		now := time.Now()
		if p.now != nil {
			now = p.now()
		}
		if p.ready.IsZero() {
			p.ready = now
		}
		if now.Sub(p.ready) < p.delay {
			return ""
		}
		if p.noDecision {
			p.held.Store(true)
			return ""
		}
		p.stage = 1
		p.lastInputScreen = frame
		if p.deny {
			p.lastChoice = 1
			return "\x1b[B"
		}
		return "\r"
	}
	if !p.deny {
		return ""
	}
	choice := selectedPermission.FindStringSubmatch(screen)
	if len(choice) != 3 {
		return ""
	}
	number, _ := strconv.Atoi(choice[1])
	isNo := strings.HasPrefix(strings.ToLower(strings.TrimSpace(choice[2])), "no")
	if p.stage == 1 {
		if isNo {
			p.stage = 2
			p.lastInputScreen = frame
			return "\t"
		}
		if number > p.lastChoice && number < 4 {
			p.lastChoice = number
			p.lastInputScreen = frame
			return "\x1b[B"
		}
	}
	if p.stage == 2 && isNo && strings.Contains(lower, "tell claude") {
		p.stage = 3
		p.lastInputScreen = frame
		return clientDenialReason
	}
	if p.stage == 3 && isNo && strings.Contains(lower, clientDenialReason) {
		p.stage = 4
		p.lastInputScreen = frame
		return "\r"
	}
	return ""
}

func TestClaudeInteractiveToolPermissionsWithFakeACP(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for an owned permission UI test; no external inference")
	}
	for _, kind := range []string{"ui-hold-write", "ui-allow-write", "ui-allow-bash", "ui-deny-write", "ui-deny-bash"} {
		t.Run(kind, func(t *testing.T) { runClientToolProbe(t, executable, "", kind) })
	}
}

func runClientEffectTerminal(t *testing.T, parent context.Context, root, project string, profile *launcher.ClientProfile, command childproc.Command, effect *clientEffectProbe, backend *onePromptBackend) (childproc.Result, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 25 * time.Second})
	if err != nil {
		t.Fatal("cannot create owned permission terminal")
	}
	defer owner.Close()
	state, err := privatefs.Open(filepath.Join(profile.Path(), "client"))
	trusted, encodeErr := json.Marshal(map[string]any{"projects": map[string]any{project: map[string]bool{"hasTrustDialogAccepted": true}}})
	if err != nil || encodeErr != nil || state.Write(".claude.json", trusted) != nil {
		t.Fatal("cannot prepare owned empty-project trust")
	}
	for i, entry := range command.Environment {
		if strings.HasPrefix(entry, "TERM=") {
			command.Environment[i] = "TERM=xterm-256color"
		}
	}
	command.Environment = append(command.Environment, "COLUMNS=160", "LINES=40", "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1")
	pidPath, wrapper := filepath.Join(root, "permission-client.pid"), filepath.Join(root, "permission-client.sh")
	script := "#!/bin/sh\nset -euC\numask 077\nprintf '%s' \"$$\" > " + probeShellQuote(pidPath) + "\n/bin/stty rows 40 cols 160\nexec " + probeShellQuote(command.Executable) + " \"$@\"\n"
	if os.WriteFile(wrapper, []byte(script), 0700) != nil {
		t.Fatal("cannot prepare owned terminal wrapper")
	}
	command.Executable = "/usr/bin/script"
	command.Args = append([]string{"-q", os.DevNull, "/bin/sh", wrapper}, command.Args...)
	p := &permissionScreenProbe{tool: effect.expect.Tool, file: filepath.Base(effect.path), content: ownedEffectText, deny: effect.expect.IsError, noDecision: effect.noDecision, delay: 500 * time.Millisecond}
	if effect.noDecision {
		p.delay = time.Second
	}
	if effect.expect.Tool == "Bash" {
		var input struct{ Command string }
		_ = json.Unmarshal(effect.expect.Input, &input)
		p.command = input.Command
	}
	type outcome struct {
		result  childproc.Result
		answers int
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		result, answers, err := runObservedTerminal(ctx, owner, command, nil, func(screen string) string {
			stats := backend.snapshot()
			pending := stats.Uses == 1 && stats.Results == 0 && backend.driver.State() == session.WaitingTools
			if (p.stage == 0 || p.deny || p.noDecision) && pending {
				if _, err := os.Lstat(effect.path); !errors.Is(err, os.ErrNotExist) {
					p.unexpectedEffect = true
					pending = false
				}
			}
			return p.next(screen, pending)
		}, true)
		done <- outcome{result, answers, err}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	pid, group := 0, 0
	owned, completed, held, exited := false, false, false, false
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
			stats := backend.snapshot()
			if owned && p.completed.Load() && stats.Completions == 1 && stats.Results == 1 && backend.driver.State() == session.Idle {
				completed = true
				break wait
			}
			if owned && p.held.Load() && stats.Uses == 1 && stats.Results == 0 && backend.driver.State() == session.WaitingTools {
				held = true
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
	groupGone := owned && errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
	_, localSettingsErr := os.Lstat(filepath.Join(project, ".claude", "settings.local.json"))
	noSavedProjectRule := errors.Is(localSettingsErr, os.ErrNotExist)
	wantStage := 1
	if p.deny {
		wantStage = 4
	}
	if p.noDecision {
		wantStage = 0
	}
	valid := (completed || held) && p.seen && p.stage == wantStage && !p.unexpectedEffect && groupGone && noSavedProjectRule && owner.Active() == 0 && !errors.Is(got.err, childproc.ErrCleanup) && !errors.Is(got.err, childproc.ErrIO) && !errors.Is(got.err, childproc.ErrOutputLimit)
	t.Logf("permission_ui_seen=%v, decision_stage=%d, completed_before_shutdown=%v, effect_before_approval=%v, client_group_gone=%v, setup_stage=%d, terminal_bytes=%d, markers=%v", p.seen, p.stage, completed, p.unexpectedEffect, groupGone, got.answers, len(got.result.Stdout), p.markers)
	t.Logf("backend_attempts=%d, auxiliary_titles=%d, last_request_shape=%+v", backend.attempts.Load(), backend.snapshot().Titles, backend.snapshot().LastShape)
	t.Logf("permission_without_input=%v, pending_without_effect=%v", effect.noDecision, held)
	t.Logf("project_permission_settings_absent=%v", noSavedProjectRule)
	return got.result, valid
}
