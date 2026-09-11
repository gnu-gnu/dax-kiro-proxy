package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/status"
)

type statusProbeBackend struct {
	catalog       *catalog.Catalog
	starts, lists atomic.Int32
}

func (b *statusProbeBackend) Models(context.Context) ([]inference.Model, error) {
	b.lists.Add(1)
	if b.catalog == nil {
		return nil, inference.ErrRequest
	}
	return b.catalog.List(), nil
}
func (b *statusProbeBackend) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	b.starts.Add(1)
	return nil, inference.ErrRequest
}

// This starts the installed client's UI in a disposable terminal with its empty project marked
// trusted through the documented per-project config key. Only first-run theme, the owned local
// API key and introductory notes receive input. No login, user prompt or tool input is submitted.
func TestClaudeStatuslineRefreshWithoutModelTurn(t *testing.T) {
	observeClaudeStatusUI(t, nil, false)
}

func observeClaudeStatusUI(t *testing.T, startup *startupObservation, completion bool) {
	observeClaudeStatusCase(t, startup, completion, nil)
}

func observeClaudeStatusCase(t *testing.T, startup *startupObservation, completion bool, existing *existingStatusObservation) {
	observeClaudeStatusUsageCase(t, startup, completion, existing, nil)
}

func observeClaudeUsageStatus(t *testing.T, usage *usageStatusObservation) {
	observeClaudeStatusUsageCase(t, nil, false, nil, usage)
}

func observeClaudeStatusUsageCase(t *testing.T, startup *startupObservation, completion bool, existing *existingStatusObservation, usage *usageStatusObservation) {
	t.Helper()
	clientExecutable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if clientExecutable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for an owned terminal/status probe; no inference")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-status-ui-")
	if err != nil {
		t.Fatal("cannot create owned UI observation root")
	}
	defer os.RemoveAll(root)
	var usageCache *status.UsageCache
	if usage != nil {
		if startup != nil || completion || existing != nil {
			t.Fatal("usage observation requires its own UI episode")
		}
		defer usage.close(t)
		usage.prepare(t)
		usageCache = usage.cache
	}
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, dir := range []string{home, project} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot create owned UI directories")
		}
	}
	settings, emptyMCP := filepath.Join(home, "settings.json"), filepath.Join(root, "mcp.json")
	if os.WriteFile(settings, []byte(`{"permissions":{"defaultMode":"manual"},"hooks":{}}`), 0600) != nil || os.WriteFile(emptyMCP, []byte(`{"mcpServers":{}}`), 0600) != nil {
		t.Fatal("cannot write owned UI settings")
	}
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 256 << 10})
	if err != nil {
		t.Fatal("cannot create bounded UI runner")
	}
	defer runner.Close()
	terminalOwner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 20 * time.Second})
	if err != nil {
		t.Fatal("cannot create bounded terminal owner")
	}
	defer terminalOwner.Close()
	versionContext, stopVersion := context.WithTimeout(t.Context(), 5*time.Second)
	version, err := runner.Run(versionContext, childproc.Command{Executable: clientExecutable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1"}})
	stopVersion()
	if err != nil || !launcher.CompatibleClientOutput(version.Stdout) {
		t.Fatal("pinned client version check failed")
	}
	models, _ := catalog.New([]catalog.Backend{{ID: "status-fixture", Name: "Independent status model"}}, "status-fixture")
	model, _ := models.ClientID("status-fixture")
	backend := &statusProbeBackend{catalog: models}
	queue := status.NewTurnQueue()
	if !completion && !queue.Push(status.TurnRecord{Scope: strings.Repeat("a", 64), Model: model, SessionState: "created", ElapsedMS: 1500, Effort: kirofeature.Status{State: kirofeature.Unknown}}) {
		t.Fatal("cannot prepare synthetic status record")
	}
	tokens, _ := gateway.NewTokens()
	var modelBackend inference.Backend = backend
	completionBackend := &completionDisplayBackend{statusProbeBackend: backend, queue: queue}
	if completion {
		modelBackend = completionBackend
	}
	handler, err := gateway.New(gateway.Config{Tokens: tokens, Backend: modelBackend, Metrics: queue, LaunchModel: model, Usage: usageCache})
	if err != nil {
		t.Fatal("cannot prepare local status gateway")
	}
	var mu sync.Mutex
	var calls int
	var previous time.Time
	var interval time.Duration
	ready := make(chan struct{})
	var completed sync.Once
	var messageRequests atomic.Int32
	var noticeRequests atomic.Int32
	var metricRequests atomic.Int32
	var secondInput atomic.Bool
	metricsShown := make(chan struct{})
	var metricsRendered sync.Once
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if existing != nil && existing.handle(w, r, func() { completed.Do(func() { close(ready) }) }) {
			return
		}
		if startup != nil && startup.handle(w, r) {
			return
		}
		if r.URL.Path == "/v1/messages" || r.URL.Path == "/messages" {
			messageRequests.Add(1)
		}
		if r.Method == "POST" && r.URL.Path == "/dax-kiro-proxy/hooks/model-capabilities" && r.Header.Get("x-api-key") == tokens.UI {
			noticeRequests.Add(1)
		}
		if r.Method == "POST" && r.URL.Path == "/dax-kiro-proxy/hooks/turn-metrics" && r.Header.Get("x-api-key") == tokens.UI {
			metricRequests.Add(1)
		}
		if r.Method == "GET" && r.URL.Path == "/dax-kiro-proxy/status/usage" && r.Header.Get("x-api-key") == tokens.UI {
			if startup != nil {
				startup.record(startupStatusRequest)
			}
			mu.Lock()
			now := time.Now()
			calls++
			if !previous.IsZero() && now.Sub(previous) >= 4*time.Second && now.Sub(previous) <= 8*time.Second {
				interval = now.Sub(previous)
				completed.Do(func() { close(ready) })
			}
			previous = now
			mu.Unlock()
		}
		handler.ServeHTTP(w, r)
	}))
	server.Config.ReadHeaderTimeout, server.Config.ReadTimeout, server.Config.WriteTimeout, server.Config.IdleTimeout = time.Second, 2*time.Second, 2*time.Second, 3*time.Second
	server.Config.MaxHeaderBytes = 8 << 10
	server.Start()
	defer server.Close()
	if startup != nil {
		startup.prepare(t, root, settings, server.URL)
		defer startup.checkChildren(t, root)
	}
	if existing != nil {
		existing.prepare(t, settings, project, server.URL)
	}
	before := fileFingerprint(t, settings)
	proxy := filepath.Join(filepath.Dir(buildRelayObserver(t)), "owned-relay")
	// https://code.claude.com/docs/en/permissions#project-allow-rules-and-workspace-trust
	// The owned global file carries documented trust for this empty project and the completed
	// onboarding state that the projection carries (D115); no imported user config is involved.
	trusted, encodeErr := json.Marshal(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{project: map[string]bool{"hasTrustDialogAccepted": true}}})
	if encodeErr != nil || os.WriteFile(filepath.Join(home, ".claude.json"), trusted, 0600) != nil {
		t.Fatal("cannot prepare documented trust and onboarding state for the owned empty project")
	}
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: clientExecutable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, UIToken: tokens.UI, StatusExecutable: proxy, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=xterm-256color", "COLUMNS=160", "LINES=40"}})
	if err != nil {
		t.Fatal("cannot prepare isolated status client")
	}
	defer profile.Close()
	command := profile.Command()
	command.Args = append(command.Args, "--tools", "", "--strict-mcp-config", "--mcp-config", emptyMCP)
	command.Environment = append(command.Environment, "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1")
	if completion {
		command.Environment = append(command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
		command.Args = append(command.Args, "--system-prompt", "Independent local rendering exercise.", "First independent local question.")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	pidPath, wrapper := filepath.Join(root, "client.pid"), filepath.Join(root, "record-client.sh")
	script := "#!/bin/sh\nset -euC\numask 077\nprintf '%s' \"$$\" > " + quote(pidPath) + "\n/bin/stty rows 40 cols 160\nexec " + quote(clientExecutable) + " \"$@\"\n"
	if os.WriteFile(wrapper, []byte(script), 0700) != nil {
		t.Fatal("cannot prepare owned terminal process observer")
	}
	command.Executable = "/usr/bin/script"
	command.Args = append([]string{"-q", os.DevNull, "/bin/sh", wrapper}, command.Args...)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if startup != nil {
		joined := make(chan struct{})
		go func() { defer close(joined); startup.control(ctx) }()
		defer func() { cancel(); <-joined }()
	}
	type outcome struct {
		result       childproc.Result
		setupAnswers int
		err          error
	}
	finished := make(chan outcome, 1)
	go func() {
		var observe func(string)
		if startup != nil {
			observe = startup.observe
		}
		var nextInput func(string) string
		if completion {
			nextInput = func(plain string) string {
				if strings.Contains(plain, "kiro turn#2 status-fixture") && strings.Contains(plain, "kiro last status-fixture") {
					metricsRendered.Do(func() { close(metricsShown) })
				}
				if strings.Contains(plain, "kiro turn#1 status-fixture") && secondInput.CompareAndSwap(false, true) {
					return "Second independent local question.\r"
				}
				return ""
			}
		}
		if usage != nil {
			nextInput = usage.observe
		}
		result, answers, err := runObservedTerminal(ctx, terminalOwner, command, observe, nextInput, usage != nil)
		finished <- outcome{result, answers, err}
	}()
	var got outcome
	ranToExit := false
	select {
	case <-ready:
		// Let the completed local response reach the terminal before closing its owned client.
		time.Sleep(100 * time.Millisecond)
	case got = <-finished:
		ranToExit = true
	case <-ctx.Done():
	}
	if existing != nil && existing.scope == "project_event_only" && !ranToExit {
		quiet := time.NewTimer(7 * time.Second)
		select {
		case <-quiet.C:
		case got = <-finished:
			ranToExit = true
		case <-ctx.Done():
		}
		quiet.Stop()
	}
	if completion && !ranToExit {
		select {
		case <-metricsShown:
		case got = <-finished:
			ranToExit = true
		case <-ctx.Done():
		}
	}
	if usage != nil && !ranToExit {
		select {
		case <-usage.ready:
		case got = <-finished:
			ranToExit = true
		case <-ctx.Done():
		}
	}
	store, openErr := privatefs.Open(root)
	var raw []byte
	if openErr == nil {
		raw, openErr = store.Read("client.pid", 32)
	}
	pid, parseErr := strconv.Atoi(string(raw))
	group, groupErr := syscall.Getpgid(pid)
	owned := openErr == nil && parseErr == nil && pid > 1 && groupErr == nil && group == pid && group != syscall.Getpgrp()
	if owned {
		_ = syscall.Kill(-group, syscall.SIGTERM)
		limit := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(limit) && !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			time.Sleep(10 * time.Millisecond)
		}
		if !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
	}
	cancel()
	if !ranToExit {
		got = <-finished
	}
	runner.Close()
	terminalOwner.Close()
	if usage != nil {
		usage.check(t)
		if profile.Close() != nil {
			t.Error("usage UI profile cleanup failed")
		}
		if _, err := os.Lstat(profile.Path()); !errors.Is(err, os.ErrNotExist) {
			t.Error("usage UI profile remains after close")
		}
	}
	limit := time.Now().Add(time.Second)
	for owned && time.Now().Before(limit) && !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
		time.Sleep(10 * time.Millisecond)
	}
	groupGone := owned && errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
	text := terminalEscapes.ReplaceAllString(string(got.result.Stdout), " ")
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	visible := strings.Contains(normalized, "kiro last status-fixture")
	if completion {
		visible = visible || strings.Contains(strings.ToLower(statusTerminalScreen(got.result.Stdout)), "kiro last status-fixture")
	}
	noticeVisible := strings.Contains(normalized, "kiro launch status-fixture.")
	metricsVisible := strings.Contains(normalized, "kiro turn#1 status-fixture") && strings.Contains(normalized, "kiro turn#2 status-fixture")
	markers := map[string]bool{}
	for _, marker := range []string{"welcome", "theme", "log in", "login", "trust", "security notes", "claude can make mistakes", "enter to continue", "custom api", "both anthropic_auth_token", "terminal", "continue", "model", "error"} {
		markers[marker] = strings.Contains(normalized, marker)
	}
	markers["owned_directory"] = statusProjectVisible(normalized, project)
	markers["selected_trust_choice"] = strings.Contains(strings.ReplaceAll(normalized, " ", ""), "❯yes,itrustthisfolder")
	mu.Lock()
	count, elapsed := calls, interval
	mu.Unlock()
	if existing != nil {
		count, elapsed, visible = existing.check(t, normalized, count)
	}
	t.Logf("status_requests=%d, refresh_interval_ms=%d, status_model_visible=%v, model_requests=%d, model_starts=%d, catalog_requests=%d, terminal_exit=%d, terminal_bytes=%d, setup_stage=%d, setup_markers=%v, client_pid_recorded=%v, client_pid_gone=%v, client_group_gone=%v, runner_active=%d, terminal_active=%d", count, elapsed.Milliseconds(), visible, messageRequests.Load(), backend.starts.Load(), backend.lists.Load(), got.result.ExitCode, len(got.result.Stdout), got.setupAnswers, markers, openErr == nil && parseErr == nil && pid > 1, pid > 1 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH), groupGone, runner.Active(), terminalOwner.Active())
	t.Logf("startup_notice_requests=%d, startup_notice_visible=%v", noticeRequests.Load(), noticeVisible)
	if noticeRequests.Load() != 1 || !noticeVisible {
		t.Error("installed client did not display exactly one startup model notice")
	}
	wantModels, wantMetrics := int32(0), int32(0)
	if completion {
		wantModels, wantMetrics = 2+completionBackend.titleTurns.Load(), 2
	}
	t.Logf("completion_control=%v, metric_requests=%d, main_turns=%d, title_turns=%d, both_completion_notices_visible=%v, second_user_input=%v, unexpected_model_input=%v", completion, metricRequests.Load(), completionBackend.mainTurns.Load(), completionBackend.titleTurns.Load(), metricsVisible, secondInput.Load(), completionBackend.badInput.Load())
	if completion {
		completionBackend.mu.Lock()
		t.Logf("completion_input_observations=%+v, status_unavailable_visible=%v, last_prefix_visible=%v", completionBackend.observations, strings.Contains(normalized, "status unavailable"), strings.Contains(normalized, "kiro last"))
		completionBackend.mu.Unlock()
		compact := strings.Join(strings.Fields(normalized), "")
		t.Logf("compact_last_visible=%v, last_word_count=%d, empty_turn_count=%d, usage_unavailable_count=%d, normalized_effort_count=%d", strings.Contains(compact, "kirolaststatus-fixture"), strings.Count(normalized, "last"), strings.Count(normalized, "no completed turn"), strings.Count(normalized, "usage unavailable"), strings.Count(normalized, "effort unknown"))
	}
	if completion && (!metricsVisible || !secondInput.Load() || completionBackend.badInput.Load() || completionBackend.mainTurns.Load() != 2 || completionBackend.titleTurns.Load() < 1 || completionBackend.titleTurns.Load() > 2 || len(queue.Drain().Records) != 0) {
		t.Error("interactive completion notice or later input contract failed")
	}
	minimumCalls := 2
	if existing != nil && existing.scope == "project_event_only" {
		minimumCalls = 1
	}
	if got.setupAnswers != 0 || markers["theme"] || markers["custom api"] || markers["both anthropic_auth_token"] || count < minimumCalls || count > 10 || elapsed == 0 || !visible || messageRequests.Load() != wantModels || backend.starts.Load() != wantModels || metricRequests.Load() != wantMetrics || !groupGone || runner.Active() != 0 || terminalOwner.Active() != 0 || errors.Is(got.err, childproc.ErrCleanup) || errors.Is(got.err, childproc.ErrIO) || errors.Is(got.err, childproc.ErrOutputLimit) || fileFingerprint(t, settings) != before {
		t.Fatal("installed client status refresh or terminal cleanup was not established")
	}
	if startup != nil {
		startup.check(t)
	}
}

var terminalEscapes = regexp.MustCompile("\\x1b\\[[0-?]*[ -/]*[@-~]|\\x1b\\][^\\x07\\x1b]*(?:\\x07|\\x1b\\\\)")

// Output stays in bounded memory. Setup input requires the exact recognized screen and selected
// item. The API key belongs solely to this test. No login, directory or tool approval is sent.
func runObservedStatusTerminal(ctx context.Context, owner *childproc.Attached, command childproc.Command, observe func(string), nextInput func(string) string) (childproc.Result, int, error) {
	return runObservedTerminal(ctx, owner, command, observe, nextInput, false)
}

// Permission tests use only the current reconstructed screen. Historical text must not authorize
// input after a menu has been erased or replaced. The original status observer retains its contract.
func runObservedTerminal(ctx context.Context, owner *childproc.Attached, command childproc.Command, observe func(string), nextInput func(string) string, currentScreen bool) (childproc.Result, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	in, input, err := os.Pipe()
	if err != nil {
		return childproc.Result{}, 0, childproc.ErrIO
	}
	defer in.Close()
	defer input.Close()
	output, out, err := os.Pipe()
	if err != nil {
		return childproc.Result{}, 0, childproc.ErrIO
	}
	defer output.Close()
	defer out.Close()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return childproc.Result{}, 0, childproc.ErrIO
	}
	defer null.Close()
	process, err := owner.Start(ctx, command, childproc.AttachedIO{Stdin: in, Stdout: out, Stderr: null})
	if err != nil {
		return childproc.Result{}, 0, err
	}
	defer process.Close()
	in.Close()
	out.Close()
	var raw []byte
	var setupAnswers, screenStart int
	var captureErr error
	captured := make(chan struct{})
	go func() {
		defer close(captured)
		buffer := make([]byte, 4096)
		for {
			if currentScreen {
				if output.SetReadDeadline(time.Now().Add(100*time.Millisecond)) != nil {
					captureErr = childproc.ErrIO
					cancel()
					return
				}
			}
			n, readErr := output.Read(buffer)
			if len(raw)+n > 256<<10 {
				captureErr = childproc.ErrOutputLimit
				cancel()
				return
			}
			raw = append(raw, buffer[:n]...)
			if nextInput != nil {
				plain := strings.ToLower(strings.Join(strings.Fields(terminalEscapes.ReplaceAllString(string(raw), " ")), " "))
				plain += " " + strings.ToLower(strings.Join(strings.Fields(statusTerminalScreen(raw)), " "))
				if currentScreen {
					plain = statusTerminalScreen(raw)
				}
				if keys := nextInput(plain); keys != "" {
					if len(keys) > 256 || input.SetWriteDeadline(time.Now().Add(200*time.Millisecond)) != nil {
						captureErr = childproc.ErrIO
						cancel()
						return
					}
					if count, err := input.Write([]byte(keys)); err != nil || count != len(keys) {
						captureErr = childproc.ErrIO
						cancel()
						return
					}
				}
			}
			if observe != nil {
				observe(strings.ToLower(strings.Join(strings.Fields(terminalEscapes.ReplaceAllString(string(raw), " ")), " ")))
			}
			if setupAnswers < 4 {
				plain := strings.ToLower(strings.Join(strings.Fields(terminalEscapes.ReplaceAllString(string(raw[screenStart:]), " ")), " "))
				keys, next := statusSetupInput(setupAnswers, plain)
				if keys != "" {
					if input.SetWriteDeadline(time.Now().Add(200*time.Millisecond)) != nil {
						captureErr = childproc.ErrIO
						cancel()
						return
					}
					if n, err := input.Write([]byte(keys)); err != nil || n != len(keys) {
						captureErr = childproc.ErrIO
						cancel()
						return
					}
					setupAnswers = next
					screenStart = len(raw)
				}
			}
			if readErr != nil {
				if currentScreen && errors.Is(readErr, os.ErrDeadlineExceeded) && ctx.Err() == nil {
					continue
				}
				return
			}
		}
	}()
	result, err := process.Wait()
	select {
	case <-captured:
	case <-time.After(100 * time.Millisecond):
		output.Close()
		<-captured
	}
	result.Stdout = raw
	return result, setupAnswers, errors.Join(err, captureErr)
}

func statusSetupInput(stage int, plain string) (string, int) {
	if strings.Contains(plain, "login") || strings.Contains(plain, "log in") {
		return "", stage
	}
	compact := strings.ReplaceAll(plain, " ", "")
	switch stage {
	case 0:
		if strings.Contains(plain, "welcome") && strings.Contains(plain, "theme") && strings.Contains(plain, "dark") && strings.Contains(plain, "light") {
			return "\r", 1
		}
	case 1:
		if strings.Contains(plain, "custom api key") && strings.Contains(plain, "do you want to use") && strings.Contains(compact, "yes❯no(recommended)") && strings.Contains(compact, "entertoconfirm") {
			return "\x1b[A", 2
		}
		// A Bearer-only environment (D115) shows no key approval; the notes follow the theme directly.
		if strings.Contains(plain, "security notes") && strings.Contains(plain, "claude can make mistakes") && strings.Contains(plain, "trust") && strings.Contains(plain, "enter to continue") {
			return "\r", 4
		}
	case 2:
		// A separate render must confirm that the arrow moved to Yes before Enter is sent.
		if strings.Contains(compact, "❯yes") {
			return "\r", 3
		}
	case 3:
		if strings.Contains(plain, "security notes") && strings.Contains(plain, "claude can make mistakes") && strings.Contains(plain, "trust") && strings.Contains(plain, "enter to continue") {
			return "\r", 4
		}
	}
	return "", stage
}

func statusProjectVisible(plain, project string) bool {
	for _, field := range strings.Fields(plain) {
		if field == strings.ToLower(project) {
			return true
		}
	}
	return false
}

func TestStatusUISetupInputRequiresRecognizedScreens(t *testing.T) {
	for _, c := range []struct {
		stage      int
		text, keys string
		next       int
	}{
		{0, "welcome theme dark light", "\r", 1},
		{1, "custom api key do you want to use yes ❯ no (recommended) enter to confirm", "\x1b[A", 2},
		{2, "❯ yes no (recommended)", "\r", 3},
		{1, "security notes claude can make mistakes trust enter to continue", "\r", 4},
		{0, "security notes claude can make mistakes trust enter to continue", "", 0},
		{0, "theme dark light", "", 0},
		{1, "❯ yes", "", 1},
		{2, "❯ no (recommended)", "", 2},
		{2, "login ❯ yes", "", 2},
		{3, "❯ yes", "", 3},
		{3, "security notes claude can make mistakes trust enter to continue", "\r", 4},
		{3, "enter to continue", "", 3},
		{4, "/owned-status-project ❯ no yes, i trust this folder", "", 4},
		{4, "/owned-status-project-other ❯ no yes, i trust this folder", "", 4},
		{4, "no ❯ yes, i trust this folder", "", 4},
	} {
		if keys, next := statusSetupInput(c.stage, c.text); keys != c.keys || next != c.next {
			t.Fatal("UI observer accepted an unexpected setup action")
		}
	}
}
