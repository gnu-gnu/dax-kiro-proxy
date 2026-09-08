package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/privatefs"
)

// This finite experiment observes hook arrival, return and visible output separately. Neither
// a hook request nor a status render is treated as the interactive client's readiness contract.
const (
	startupFastEntered = iota
	startupHeldEntered
	startupFastReturned
	startupHeldReturned
	startupFastExited
	startupReleased
	startupStatusRequest
	startupStatusVisible
	startupFastVisible
	startupHeldVisible
	startupEventCount
)

type startupObservation struct {
	mu       sync.Mutex
	times    [startupEventCount]time.Time
	counts   [startupEventCount]int
	token    string
	root     string
	updates  chan struct{}
	release  chan struct{}
	once     sync.Once
	requests int
}

func newStartupObservation(t *testing.T) *startupObservation {
	t.Helper()
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("cannot prepare independent hook observation key")
	}
	return &startupObservation{token: tokens.UI, updates: make(chan struct{}, 1), release: make(chan struct{})}
}

func (o *startupObservation) record(event int) {
	o.mu.Lock()
	if o.times[event].IsZero() {
		o.times[event] = time.Now()
	}
	o.counts[event]++
	o.mu.Unlock()
	select {
	case o.updates <- struct{}{}:
	default:
	}
}

func (o *startupObservation) releaseHeld() {
	o.once.Do(func() { o.record(startupReleased); close(o.release) })
}

func (o *startupObservation) handle(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/startup-observation/") {
		return false
	}
	if r.Method != "POST" || r.Header.Get("X-Dax-Observation-Key") != o.token {
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65))
	if err != nil || string(raw) != "{}" {
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	event := -1
	switch r.URL.Path {
	case "/startup-observation/fast/entered":
		event = startupFastEntered
	case "/startup-observation/held/entered":
		event = startupHeldEntered
	case "/startup-observation/fast/returned":
		event = startupFastReturned
	case "/startup-observation/held/returned":
		event = startupHeldReturned
	}
	o.mu.Lock()
	o.requests++
	valid := event >= 0 && o.requests <= 8
	if valid {
		valid = o.counts[event] == 0
		if event == startupFastReturned {
			valid = valid && !o.times[startupFastEntered].IsZero()
		}
		if event == startupHeldReturned {
			valid = valid && !o.times[startupHeldEntered].IsZero() && !o.times[startupReleased].IsZero()
		}
		if valid {
			o.times[event], o.counts[event] = time.Now(), 1
		}
	}
	o.mu.Unlock()
	if !valid {
		w.WriteHeader(http.StatusConflict)
		return true
	}
	select {
	case o.updates <- struct{}{}:
	default:
	}
	if event == startupHeldEntered {
		if http.NewResponseController(w).SetWriteDeadline(time.Now().Add(8*time.Second)) != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		timer := time.NewTimer(7 * time.Second)
		defer timer.Stop()
		select {
		case <-o.release:
		case <-r.Context().Done():
			return true
		case <-timer.C:
			w.WriteHeader(http.StatusGatewayTimeout)
			return true
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, "{}")
	return true
}

func (o *startupObservation) control(ctx context.Context) {
	defer o.releaseHeld()
	for {
		o.mu.Lock()
		both := !o.times[startupFastReturned].IsZero() && !o.times[startupHeldEntered].IsZero()
		o.mu.Unlock()
		if both {
			break
		}
		select {
		case <-o.updates:
		case <-ctx.Done():
			return
		}
	}
	store, err := privatefs.Open(o.root)
	var pid int
	if err == nil {
		var raw []byte
		raw, err = store.Read("fast.pid", 32)
		if err == nil {
			pid, err = strconv.Atoi(string(raw))
		}
	}
	if err == nil && pid > 1 {
		deadline := time.NewTimer(time.Second)
		tick := time.NewTicker(10 * time.Millisecond)
		defer deadline.Stop()
		defer tick.Stop()
	exitWait:
		for {
			if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				o.record(startupFastExited)
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-deadline.C:
				break exitWait
			case <-tick.C:
			}
		}
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (o *startupObservation) observe(plain string) {
	for _, item := range []struct {
		text  string
		event int
	}{{"kiro last status-fixture", startupStatusVisible}, {"dax startup fast complete", startupFastVisible}, {"dax startup held complete", startupHeldVisible}} {
		if strings.Contains(plain, item.text) {
			o.record(item.event)
		}
	}
}

func (o *startupObservation) check(t *testing.T) {
	t.Helper()
	o.mu.Lock()
	times, counts := o.times, o.counts
	o.mu.Unlock()
	start := times[startupFastEntered]
	if times[startupHeldEntered].Before(start) {
		start = times[startupHeldEntered]
	}
	deltas := make([]int64, startupEventCount)
	for i, at := range times {
		deltas[i] = -1
		if !at.IsZero() && !start.IsZero() {
			deltas[i] = at.Sub(start).Milliseconds()
		}
	}
	t.Logf("startup_event_ms [fast_enter,held_enter,fast_return,held_return,fast_exit,release,status_request,status_visible,fast_visible,held_visible]=%v", deltas)
	for i := startupFastEntered; i <= startupHeldReturned; i++ {
		if counts[i] != 1 {
			t.Error("expected exactly one callback for each owned startup hook stage")
		}
	}
	parallel := !times[startupFastExited].IsZero() && !times[startupReleased].IsZero() && times[startupFastExited].Before(times[startupReleased]) && times[startupHeldEntered].Before(times[startupReleased])
	statusBeforeRelease := !times[startupStatusVisible].IsZero() && times[startupStatusVisible].Before(times[startupReleased])
	fastNoticeBeforeRelease := !times[startupFastVisible].IsZero() && times[startupFastVisible].Before(times[startupReleased])
	t.Logf("fast_hook_exited_while_peer_held=%v, status_visible_while_peer_held=%v, fast_notice_visible_while_peer_held=%v", parallel, statusBeforeRelease, fastNoticeBeforeRelease)
	if !parallel || times[startupReleased].Sub(times[startupFastReturned]) < 2500*time.Millisecond || times[startupHeldReturned].Before(times[startupReleased]) {
		t.Error("independent startup hook overlap was not established")
	}
	if times[startupFastVisible].IsZero() || times[startupHeldVisible].IsZero() || times[startupStatusVisible].IsZero() {
		t.Error("startup notices and client status were not all observed in the terminal")
	}
}

func (o *startupObservation) prepare(t *testing.T, root, settings, endpoint string) {
	t.Helper()
	o.root = root
	helper := buildStartupHook(t)
	hooks := make([]any, 0, 2)
	for _, kind := range []string{"fast", "held"} {
		config := writeStartupHookConfig(t, root, endpoint, o.token, kind)
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
		hooks = append(hooks, map[string]any{"type": "command", "timeout": 12, "command": "exec /usr/bin/env -i PATH=/usr/bin:/bin " + quote(helper) + " " + quote(config)})
	}
	raw, err := json.Marshal(map[string]any{"permissions": map[string]any{"defaultMode": "manual"}, "hooks": map[string]any{"SessionStart": []any{map[string]any{"matcher": "startup", "hooks": hooks}}}})
	if err != nil || os.WriteFile(settings, raw, 0600) != nil {
		t.Fatal("cannot prepare independent startup hooks")
	}
}

func (o *startupObservation) checkChildren(t *testing.T, root string) {
	t.Helper()
	store, err := privatefs.Open(root)
	if err != nil {
		t.Error("hook observation directory unavailable at cleanup")
		return
	}
	for _, kind := range []string{"fast", "held"} {
		raw, err := store.Read(kind+".pid", 32)
		pid, parseErr := strconv.Atoi(string(raw))
		if err != nil || parseErr != nil || pid <= 1 {
			t.Error("owned hook PID was not recorded")
			continue
		}
		gone := errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
		t.Logf("startup_%s_process_gone=%v", kind, gone)
		if !gone {
			t.Error("owned hook survived client shutdown")
			_ = syscall.Kill(pid, syscall.SIGKILL)
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
}

func writeStartupHookConfig(t *testing.T, root, endpoint, token, kind string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"version": 1, "endpoint": endpoint, "token": token, "kind": kind})
	path := filepath.Join(root, kind+".json")
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("cannot write private startup observation configuration")
	}
	return path
}

func buildStartupHook(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot resolve independent fixture build location")
	}
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal("cannot prepare fixture compiler owner")
	}
	defer runner.Close()
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	executable := filepath.Join(root, "startup-hook")
	// Configure only this disposable build HOME. GOTELEMETRY is not a settable environment option.
	_, err = runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"telemetry", "off"}})
	if err != nil {
		t.Fatal("cannot disable telemetry in the owned fixture build HOME")
	}
	_, err = runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", executable, "./testdata/startuphooks"}})
	if err != nil {
		t.Fatal("cannot build independent startup hook fixture")
	}
	return executable
}

func TestStartupHookObserverProtocol(t *testing.T) {
	helper := buildStartupHook(t)
	o := newStartupObservation(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !o.handle(w, r) {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	server.Config.ReadHeaderTimeout, server.Config.ReadTimeout, server.Config.WriteTimeout, server.Config.IdleTimeout = time.Second, 2*time.Second, 8*time.Second, time.Second
	server.Config.MaxHeaderBytes = 4096
	server.Start()
	defer server.Close()
	defer o.releaseHeld()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("cannot restrict test configuration directory")
	}
	runner, err := childproc.New(childproc.Config{Timeout: 10 * time.Second, MaxOutputBytes: 256})
	if err != nil {
		t.Fatal("cannot prepare hook process owner")
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	type result struct {
		kind   string
		result childproc.Result
		err    error
	}
	finished := make(chan result, 2)
	for _, kind := range []string{"fast", "held"} {
		config := writeStartupHookConfig(t, root, server.URL, o.token, kind)
		go func() {
			out, err := runner.Run(ctx, childproc.Command{Executable: helper, Args: []string{config}, Directory: root, Environment: []string{"PATH=/usr/bin:/bin"}})
			finished <- result{kind, out, err}
		}()
	}
	first := <-finished
	if first.kind != "fast" || first.err != nil || first.result.ExitCode != 0 || string(first.result.Stdout) != "{\"systemMessage\":\"Dax startup fast complete\"}\n" {
		t.Error("fast hook failed to complete while the held hook was suspended")
	}
	// Do not release an unstarted peer and then mistake sequential execution for overlap.
	waiting := false
awaitHeld:
	for {
		o.mu.Lock()
		waiting = !o.times[startupHeldEntered].IsZero() && o.times[startupHeldReturned].IsZero()
		o.mu.Unlock()
		if waiting {
			break
		}
		select {
		case <-o.updates:
		case <-ctx.Done():
			break awaitHeld
		}
	}
	if !waiting {
		t.Error("held hook did not reach its suspended callback")
	}
	o.releaseHeld()
	second := <-finished
	if second.kind != "held" || second.err != nil || second.result.ExitCode != 0 || string(second.result.Stdout) != "{\"systemMessage\":\"Dax startup held complete\"}\n" {
		t.Error("held hook did not complete after explicit release")
	}
	runner.Close()
	if runner.Active() != 0 {
		t.Error("hook command ownership survived cleanup")
	}
	o.checkChildren(t, root)
}

func TestStartupObserverRejectsInvalidEvidence(t *testing.T) {
	for _, c := range []struct {
		name, method, path, body string
		wrongKey, duplicate      bool
		status                   int
	}{
		{"wrong key", "POST", "/fast/entered", "{}", true, false, 401},
		{"wrong method", "GET", "/fast/entered", "{}", false, false, 401},
		{"injected body", "POST", "/fast/entered", `{"pid":123}`, false, false, 400},
		{"oversize body", "POST", "/fast/entered", strings.Repeat("x", 65), false, false, 400},
		{"unknown event", "POST", "/not-a-stage", "{}", false, false, 409},
		{"return before entry", "POST", "/fast/returned", "{}", false, false, 409},
		{"return before release", "POST", "/held/returned", "{}", false, false, 409},
		{"repeated entry", "POST", "/fast/entered", "{}", false, true, 409},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := newStartupObservation(t)
			if c.path == "/held/returned" {
				// Entry alone must not permit the second stage while its response remains held.
				o.record(startupHeldEntered)
			}
			req := func() *http.Request {
				r := httptest.NewRequest(c.method, "/startup-observation"+c.path, strings.NewReader(c.body))
				key := o.token
				if c.wrongKey {
					key = strings.Repeat("x", 43)
				}
				r.Header.Set("X-Dax-Observation-Key", key)
				return r
			}
			if c.duplicate {
				first := httptest.NewRecorder()
				if !o.handle(first, req()) || first.Code != 200 {
					t.Fatal("independent positive event was not recorded")
				}
			}
			before := o.times
			out := httptest.NewRecorder()
			if !o.handle(out, req()) || out.Code != c.status || o.times != before {
				t.Fatal("invalid callback changed the startup evidence")
			}
		})
	}
}

func TestClaudeStartupHookOrderingWithoutModelTurn(t *testing.T) {
	observeClaudeStatusUI(t, newStartupObservation(t))
}
