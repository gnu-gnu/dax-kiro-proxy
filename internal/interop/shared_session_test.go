package interop_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
)

var sharedHistoryMarkers = [7]string{"CommonRequest_241", "CommonReply_241", "LeftRequest_251", "LeftReply_251", "RightRequest_257", "RightReply_257", "InspectRequest_263"}

type sharedHistoryProjection struct {
	Counts [7]int
	Order  []int
	Seeds  int
}

// Only fixed authored markers and their public message roles/order are retained, never raw text.
func projectSharedHistory(r *anthropic.Request, seed string) (sharedHistoryProjection, error) {
	var p sharedHistoryProjection
	if r == nil || seed == "" || len(r.Tools) != 0 {
		return p, inference.ErrRequest
	}
	for _, b := range r.System {
		for _, marker := range sharedHistoryMarkers {
			if strings.Contains(b.Text, marker) {
				return p, inference.ErrRequest
			}
		}
	}
	for _, m := range r.Messages {
		for _, b := range m.Content {
			if b.Type != "text" {
				return p, inference.ErrRequest
			}
			if m.Role == "user" {
				p.Seeds += strings.Count(b.Text, seed)
			}
			var hits []struct{ at, kind int }
			for i, marker := range sharedHistoryMarkers {
				n := strings.Count(b.Text, marker)
				if n == 0 {
					continue
				}
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				if n != 1 || m.Role != role || p.Counts[i] != 0 {
					return p, inference.ErrRequest
				}
				p.Counts[i]++
				hits = append(hits, struct{ at, kind int }{strings.Index(b.Text, marker), i})
			}
			sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
			for _, hit := range hits {
				p.Order = append(p.Order, hit.kind)
			}
		}
	}
	if p.Seeds != 1 {
		return p, inference.ErrRequest
	}
	return p, nil
}

func (p sharedHistoryProjection) readbackClass() string {
	if len(p.Order) < 5 || p.Order[0] != 0 || p.Order[1] != 1 || p.Order[len(p.Order)-1] != 6 {
		return "invalid"
	}
	for _, answer := range []int{3, 5} {
		if p.Counts[answer] != 0 && (p.Counts[answer-1] != 1 || slices.Index(p.Order, answer-1) > slices.Index(p.Order, answer)) {
			return "invalid"
		}
	}
	switch {
	case p.Counts[3] == 1 && p.Counts[5] == 1:
		return "both"
	case p.Counts[3] == 1:
		return "left"
	case p.Counts[5] == 1:
		return "right"
	default:
		return "invalid"
	}
}

func TestSharedSessionProjectionSeparatesStoredAndSelectedBranches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []int
		class string
	}{
		{"sequential-left-right", []int{0, 1, 2, 3, 4, 5, 6}, "both"},
		{"sequential-right-left", []int{0, 1, 4, 5, 2, 3, 6}, "both"},
		{"selected-left", []int{0, 1, 2, 3, 6}, "left"},
		{"selected-right", []int{0, 1, 4, 5, 6}, "right"},
		{"unanswered-left", []int{0, 1, 2, 4, 5, 6}, "right"},
		{"orphan-answer", []int{0, 1, 3, 4, 5, 6}, "invalid"},
		{"reversed-pair", []int{0, 1, 3, 2, 6}, "invalid"},
		{"missing-base", []int{2, 3, 4, 5, 6}, "invalid"},
		{"missing-inspection", []int{0, 1, 2, 3, 4, 5}, "invalid"},
		{"no-completion", []int{0, 1, 2, 4, 6}, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r anthropic.Request
			for _, kind := range tc.order {
				role := "user"
				if kind%2 == 1 {
					role = "assistant"
				}
				text := sharedHistoryMarkers[kind]
				if kind == 0 {
					text += " own-seed"
				}
				r.Messages = append(r.Messages, anthropic.Message{Role: role, Content: []anthropic.Block{{Type: "text", Text: text}}})
			}
			p, err := projectSharedHistory(&r, "own-seed")
			if tc.name == "missing-base" {
				if err == nil {
					t.Fatal("missing base seed accepted")
				}
				return
			}
			if err != nil || p.readbackClass() != tc.class {
				t.Fatal("stored branch was confused with selected model context")
			}
		})
	}
	for _, mode := range []string{"duplicate", "wrong-role", "system-marker"} {
		t.Run(mode, func(t *testing.T) {
			r := &anthropic.Request{Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: sharedHistoryMarkers[0] + " own-seed"}}}}}
			switch mode {
			case "duplicate":
				r.Messages = append(r.Messages, r.Messages[0])
			case "wrong-role":
				r.Messages[0].Role = "assistant"
			case "system-marker":
				r.System = []anthropic.Block{{Type: "text", Text: sharedHistoryMarkers[2]}}
			}
			if _, err := projectSharedHistory(r, "own-seed"); err == nil {
				t.Fatal("invalid marker provenance accepted")
			}
		})
	}
}

type sharedSessionBackend struct {
	stage                         int
	seed, identity, model, answer string
	starts, ends                  atomic.Int32
	waiting, release              chan struct{}
	waitOnce, releaseOnce         sync.Once
	mu                            sync.Mutex
	projection                    sharedHistoryProjection
}

func (b *sharedSessionBackend) Models(context.Context) ([]inference.Model, error) {
	return []inference.Model{{ID: b.model, Name: "Independent shared session"}}, nil
}
func (b *sharedSessionBackend) Start(_ context.Context, r *anthropic.Request) (inference.Turn, error) {
	if b.starts.Add(1) != 1 || r.Model != b.model || !nativeHistoryID(r.Identity.Session) || b.identity != "" && r.Identity.Session != b.identity {
		return nil, inference.ErrRequest
	}
	p, err := projectSharedHistory(r, b.seed)
	if err != nil || p.Counts[0] != 1 {
		return nil, inference.ErrRequest
	}
	if b.stage == 0 {
		if !slices.Equal(p.Order, []int{0}) {
			return nil, inference.ErrRequest
		}
	} else {
		if len(p.Order) < 3 || p.Order[0] != 0 || p.Order[1] != 1 {
			return nil, inference.ErrRequest
		}
		if b.stage == 3 {
			if p.readbackClass() == "invalid" {
				return nil, inference.ErrRequest
			}
		} else {
			kind := 2 * b.stage
			if p.Counts[kind] != 1 || p.Counts[kind+1] != 0 || p.Counts[6] != 0 {
				return nil, inference.ErrRequest
			}
		}
	}
	b.mu.Lock()
	b.projection = p
	b.mu.Unlock()
	return &sharedSessionTurn{completionDisplayTurn: &completionDisplayTurn{model: b.model, text: b.answer}, owner: b, cancelled: make(chan struct{})}, nil
}
func (b *sharedSessionBackend) permit() { b.releaseOnce.Do(func() { close(b.release) }) }

type sharedSessionTurn struct {
	*completionDisplayTurn
	owner     *sharedSessionBackend
	cancelled chan struct{}
	once      sync.Once
	started   bool
}

func (t *sharedSessionTurn) Next(ctx context.Context) (inference.Event, error) {
	if !t.started {
		t.owner.waitOnce.Do(func() { close(t.owner.waiting) })
		select {
		case <-ctx.Done():
			return inference.Event{}, ctx.Err()
		case <-t.cancelled:
			return inference.Event{}, context.Canceled
		case <-t.owner.release:
		}
		t.started = true
	}
	e, err := t.completionDisplayTurn.Next(ctx)
	if e.Kind == inference.End {
		t.owner.ends.Add(1)
	}
	return e, err
}
func (t *sharedSessionTurn) Cancel() {
	t.once.Do(func() { close(t.cancelled) })
	t.completionDisplayTurn.Cancel()
}

type sharedSessionOutcome struct {
	result childproc.Result
	err    error
}
type sharedSessionRun struct {
	backend         *sharedSessionBackend
	server          *gateway.Server
	profile         *launcher.ClientProfile
	command         childproc.Command
	pidPath         string
	finished        chan sharedSessionOutcome
	started, joined bool
	pid             int
}
type sharedSessionEpisode struct {
	ctx                                                                 context.Context
	cancel                                                              context.CancelFunc
	runner                                                              *childproc.Runner
	root, home, project, settings, global, client, seed, identity, mode string
	runs                                                                []*sharedSessionRun
	tokens                                                              []string
	settingsBefore, globalBefore                                        [32]byte
}

func newSharedSessionEpisode(t *testing.T, mode string) *sharedSessionEpisode {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "dax-shared-session-")
	if err != nil {
		t.Fatal("shared session root")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	e := &sharedSessionEpisode{ctx: ctx, cancel: cancel, root: root, home: filepath.Join(root, "home"), project: filepath.Join(root, "project"), client: os.Getenv("DAX_INTEROP_CLAUDE_BINARY"), seed: rand.Text(), mode: mode}
	t.Cleanup(func() {
		e.cancel()
		if e.runner != nil {
			e.runner.Close()
		}
		for _, r := range e.runs {
			if r.started && !r.joined {
				<-r.finished
				r.joined = true
			}
			r.server.Close()
			r.profile.Close()
		}
		os.RemoveAll(root)
	})
	for _, dir := range []string{e.home, filepath.Join(e.home, ".claude"), e.project} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("shared session directories")
		}
	}
	e.settings, e.global = filepath.Join(e.home, ".claude", "settings.json"), filepath.Join(e.home, ".claude.json")
	if os.WriteFile(e.settings, []byte(`{"disableAllHooks":true,"autoMemoryEnabled":false,"permissions":{"deny":["Read","Write","Edit","Bash"]}}`), 0600) != nil || os.WriteFile(e.global, []byte(`{}`), 0600) != nil {
		t.Fatal("shared session settings")
	}
	e.settingsBefore, e.globalBefore = fileFingerprint(t, e.settings), fileFingerprint(t, e.global)
	e.runner, err = childproc.New(childproc.Config{MaxProcesses: 2, Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("shared session runner")
	}
	v, err := e.runner.Run(ctx, childproc.Command{Executable: e.client, Directory: root, Environment: []string{"HOME=" + e.home, "PATH=/usr/bin:/bin"}, Args: []string{"--version"}})
	if err != nil || !launcher.CompatibleClientOutput(v.Stdout) {
		t.Fatal("unverified shared session client")
	}
	return e
}

func (e *sharedSessionEpisode) prepare(t *testing.T, stage int) *sharedSessionRun {
	t.Helper()
	const model = "claude-dax-shared-history"
	answer := "InspectReply_263"
	if stage < 3 {
		answer = sharedHistoryMarkers[2*stage+1]
	}
	b := &sharedSessionBackend{stage: stage, seed: e.seed, identity: e.identity, model: model, answer: answer, waiting: make(chan struct{}), release: make(chan struct{})}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("shared session credentials")
	}
	server, err := gateway.StartServer(e.ctx, gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: time.Second, IdleTimeout: 2 * time.Second, Gateway: gateway.Config{Tokens: tokens, Backend: b, TurnTimeout: 20 * time.Second, FirstEventTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second}})
	if err != nil {
		t.Fatal("shared session gateway")
	}
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: e.root, Home: e.home, Project: e.project, UserSettings: e.settings, Executable: e.client, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL(), ModelToken: tokens.Model, KeepHistory: true, ResumeSession: e.identity, Environment: []string{"PATH=/usr/bin:/bin", "TERM=dumb"}})
	if err != nil {
		server.Close()
		t.Fatal("shared session profile")
	}
	r := &sharedSessionRun{backend: b, server: server, profile: profile, command: profile.Command(), finished: make(chan sharedSessionOutcome, 1), pidPath: filepath.Join(e.root, "client-"+strconv.Itoa(stage)+".pid")}
	e.runs = append(e.runs, r)
	e.tokens = append(e.tokens, tokens.Model)
	if e.mode == "native-reference" {
		// Keep the same routing overlay but let the unmodified CLI use its ordinary owned HOME.
		// Native settings/state mutations in this disposable reference arm are observed, not adopted.
		var env []string
		for _, v := range r.command.Environment {
			if !strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") {
				env = append(env, v)
			}
		}
		r.command.Environment = env
	}
	question := sharedHistoryMarkers[2*stage] + ". Reply briefly."
	if stage == 0 {
		question = sharedHistoryMarkers[0] + " " + e.seed + ". Reply briefly."
	}
	r.command.Args = append(r.command.Args, "--print", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--system-prompt", "Follow the current text-only instruction.", question)
	r.command.Environment = append(r.command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
	wrapper := filepath.Join(e.root, "client-"+strconv.Itoa(stage)+".sh")
	body := "#!/bin/sh\nset -eu\numask 077\nprintf '%s' \"$$\" > " + probeShellQuote(r.pidPath) + "\nexec " + probeShellQuote(e.client) + " \"$@\"\n"
	if os.WriteFile(wrapper, []byte(body), 0700) != nil {
		t.Fatal("shared session process observer")
	}
	r.command.Executable = "/bin/sh"
	r.command.Args = append([]string{wrapper}, r.command.Args...)
	return r
}
func (e *sharedSessionEpisode) start(r *sharedSessionRun) {
	r.started = true
	go func() { result, err := e.runner.Run(e.ctx, r.command); r.finished <- sharedSessionOutcome{result, err} }()
}
func (e *sharedSessionEpisode) held(t *testing.T, r *sharedSessionRun) int {
	t.Helper()
	select {
	case <-r.backend.waiting:
	case got := <-r.finished:
		r.joined = true
		t.Logf("early_exit=%d, output_bytes=%d", got.result.ExitCode, len(got.result.Stdout))
		t.Fatal("shared session ended before held response")
	case <-e.ctx.Done():
		t.Fatal("held shared session deadline")
	}
	raw, err := readDenialArtifact(e.root, filepath.Base(r.pidPath), 32)
	pid, parseErr := strconv.Atoi(string(raw))
	group, groupErr := syscall.Getpgid(pid)
	if err != nil || parseErr != nil || pid < 2 || groupErr != nil || group != pid || syscall.Kill(-pid, 0) != nil || r.backend.ends.Load() != 0 {
		t.Fatal("held shared session is not a live owned client")
	}
	r.pid = pid
	return pid
}
func (e *sharedSessionEpisode) finish(t *testing.T, r *sharedSessionRun) {
	t.Helper()
	var got sharedSessionOutcome
	select {
	case got = <-r.finished:
		r.joined = true
	case <-e.ctx.Done():
		t.Fatal("shared session completion deadline")
	}
	var output struct {
		Type, Subtype, Result string
		SessionID             string `json:"session_id"`
		IsError               bool   `json:"is_error"`
		Turns                 int    `json:"num_turns"`
	}
	decoded := json.Unmarshal(got.result.Stdout, &output) == nil
	valid := got.err == nil && got.result.ExitCode == 0 && decoded && output.Type == "result" && output.Subtype == "success" && !output.IsError && output.Turns == 1 && output.Result == r.backend.answer && nativeHistoryID(output.SessionID) && r.backend.starts.Load() == 1 && r.backend.ends.Load() == 1
	if e.identity == "" {
		e.identity = output.SessionID
	} else {
		valid = valid && output.SessionID == e.identity
	}
	if !valid {
		t.Logf("stage=%d, decoded=%v, exit=%d, starts=%d, ends=%d", r.backend.stage, decoded, got.result.ExitCode, r.backend.starts.Load(), r.backend.ends.Load())
		t.Fatal("shared session response failed")
	}
	r.pid = got.result.PID
	if r.server.Close() != nil || r.profile.Close() != nil || r.pid < 2 || !errors.Is(syscall.Kill(-r.pid, 0), syscall.ESRCH) {
		t.Fatal("shared session ownership did not join")
	}
	if _, err := os.Lstat(r.profile.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("shared session profile remains")
	}
	stats := r.server.Stats()
	if stats.Handlers != 0 || stats.Connections != 0 {
		t.Fatal("shared session HTTP work remains")
	}
	c, err := net.DialTimeout("tcp", strings.TrimPrefix(r.server.URL(), "http://"), time.Second)
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("shared session listener remains")
	}
}

// Inspect only known marker/credential presence in the owned transcript, never its internal format.
func (e *sharedSessionEpisode) storedMarkers(t *testing.T) [7]bool {
	t.Helper()
	var found [7]bool
	entries, matched := 0, 0
	err := filepath.WalkDir(filepath.Join(e.home, ".claude", "projects"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 64 || d.Type()&os.ModeSymlink != 0 {
			return errors.New("shared history inventory bound")
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			return errors.New("shared history file bound")
		}
		if filepath.Base(path) != e.identity+".jsonl" {
			return nil
		}
		matched++
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		f.Close()
		if err != nil || len(raw) > 2<<20 {
			return errors.New("shared history read bound")
		}
		for i, m := range sharedHistoryMarkers {
			found[i] = bytes.Contains(raw, []byte(m))
		}
		for _, token := range e.tokens {
			if bytes.Contains(raw, []byte(token)) {
				return errors.New("routing credential in shared history")
			}
		}
		return nil
	})
	if err != nil || matched != 1 {
		t.Fatal("owned shared transcript not established")
	}
	return found
}

func observeSharedSession(t *testing.T, mode string, overlap bool, first int) sharedHistoryProjection {
	t.Helper()
	e := newSharedSessionEpisode(t, mode)
	initial := e.prepare(t, 0)
	initial.backend.permit()
	e.start(initial)
	e.finish(t, initial)
	var writers [2]*sharedSessionRun
	if overlap {
		writers[0] = e.prepare(t, 1)
		e.start(writers[0])
		leftPID := e.held(t, writers[0])
		deadline := time.Now().Add(2 * time.Second)
		for !e.storedMarkers(t)[2] {
			if time.Now().After(deadline) {
				t.Fatal("first pending question not retained before second resume")
			}
			time.Sleep(10 * time.Millisecond)
		}
		writers[1] = e.prepare(t, 2)
		e.start(writers[1])
		rightPID := e.held(t, writers[1])
		if leftPID == rightPID || syscall.Kill(-leftPID, 0) != nil || syscall.Kill(-rightPID, 0) != nil {
			t.Fatal("shared writers did not overlap")
		}
		writers[first].backend.permit()
		e.finish(t, writers[first])
		other := writers[1-first]
		e.held(t, other)
		other.backend.permit()
		e.finish(t, other)
	} else {
		for _, lane := range []int{first, 1 - first} {
			writers[lane] = e.prepare(t, lane+1)
			writers[lane].backend.permit()
			e.start(writers[lane])
			e.finish(t, writers[lane])
		}
	}
	stored := e.storedMarkers(t)
	for i := 0; i < 6; i++ {
		if !stored[i] {
			t.Fatal("completed shared transcript lost an authored marker")
		}
	}
	reader := e.prepare(t, 3)
	reader.backend.permit()
	e.start(reader)
	e.finish(t, reader)
	p := reader.backend.projection
	if !overlap {
		expected := []int{0, 1, 2, 3, 4, 5, 6}
		if first == 1 {
			expected = []int{0, 1, 4, 5, 2, 3, 6}
		}
		if !slices.Equal(p.Order, expected) {
			t.Fatal("sequential shared resume lost completed context")
		}
	}
	settingsOK := fileFingerprint(t, e.settings) == e.settingsBefore
	globalOK := fileFingerprint(t, e.global) == e.globalBefore
	if e.mode == "prepared" && (!settingsOK || !globalOK) {
		t.Fatal("prepared shared resume changed source settings")
	}
	stored = e.storedMarkers(t)
	for _, present := range stored {
		if !present {
			t.Fatal("shared transcript marker disappeared after readback")
		}
	}
	groups, profiles, endpoints, tokens := map[int]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, r := range e.runs {
		if !r.joined || groups[r.pid] || profiles[r.profile.Path()] || endpoints[r.server.URL()] || tokens[e.tokens[i]] {
			t.Fatal("shared resume reused ownership")
		}
		groups[r.pid], profiles[r.profile.Path()], endpoints[r.server.URL()], tokens[e.tokens[i]] = true, true, true, true
	}
	if e.runner.Active() != 0 {
		t.Fatal("shared resume runner remains active")
	}
	t.Logf("mode=%s, overlap=%v, first_completed=%d, writer_left_projection=%v, writer_right_projection=%v, readback_projection=%v, selected_branch_class=%s, both_completed_branches_stored=true, joined_client_groups=%d, fresh_profiles=%d, settings_unchanged=%v, global_config_unchanged=%v, credentials_in_transcript=false", mode, overlap, first, writers[0].backend.projection.Order, writers[1].backend.projection.Order, p.Order, p.readbackClass(), len(groups), len(profiles), settingsOK, globalOK)
	return p
}

func TestClaudeSharedSessionConcurrentResumeObservation(t *testing.T) {
	if os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Skip("set pinned Claude for disposable shared-session writers and local synthetic responses; no Kiro or external inference")
	}
	for _, overlap := range []bool{false, true} {
		for first := range 2 {
			name := "sequential-"
			if overlap {
				name = "overlap-"
			}
			name += strconv.Itoa(first)
			if !t.Run(name, func(t *testing.T) {
				var reference sharedHistoryProjection
				if !t.Run("native-reference", func(t *testing.T) { reference = observeSharedSession(t, "native-reference", overlap, first) }) {
					return
				}
				t.Run("prepared", func(t *testing.T) {
					prepared := observeSharedSession(t, "prepared", overlap, first)
					if reference.Counts != prepared.Counts || !slices.Equal(reference.Order, prepared.Order) {
						t.Fatal("prepared shared-session readback differs from native reference")
					}
				})
			}) {
				return
			}
		}
	}
}
