package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
	"dax-kiro-proxy/internal/toolregistry"
)

// This observation retains only fixed counters and flags, never client descriptions, schemas,
// system instructions, user content or field names supplied by a request.
type defaultClientShape struct {
	Requests, Tools, MaxDescriptionBytes, UnknownToolFields, Deferred, TypedServerTools int
	Stream, Thinking, Context, OutputConfig, Read, Write, Bash                          bool
}

func (s *defaultClientShape) observe(body []byte) {
	s.Requests++
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return
	}
	s.Stream = string(fields["stream"]) == "true"
	s.Thinking = fields["thinking"] != nil
	s.Context = fields["context_management"] != nil
	s.OutputConfig = fields["output_config"] != nil
	var declarations []map[string]json.RawMessage
	if json.Unmarshal(fields["tools"], &declarations) != nil || len(declarations) > 128 {
		return
	}
	s.Tools = len(declarations)
	for _, tool := range declarations {
		var name, description, kind string
		_ = json.Unmarshal(tool["name"], &name)
		_ = json.Unmarshal(tool["description"], &description)
		_ = json.Unmarshal(tool["type"], &kind)
		s.Read = s.Read || name == "Read"
		s.Write = s.Write || name == "Write"
		s.Bash = s.Bash || name == "Bash"
		s.MaxDescriptionBytes = max(s.MaxDescriptionBytes, len(description))
		if kind != "" && kind != "custom" {
			s.TypedServerTools++
		}
		if string(tool["defer_loading"]) == "true" {
			s.Deferred++
		}
		for key := range tool {
			switch key {
			case "name", "description", "input_schema", "type", "strict", "cache_control", "input_examples", "defer_loading", "allowed_callers":
			default:
				s.UnknownToolFields++
			}
		}
	}
}

type defaultClientBackend struct {
	*session.Driver
	catalog    *catalog.Catalog
	limit      int32
	starts     atomic.Int32
	failed     atomic.Bool
	mu         sync.Mutex
	first      *anthropic.Request
	comparison defaultClientComparison
}

type defaultClientComparison struct {
	Seen, IdentitySame, ModelSame, EffortSame, SystemSame, MetadataSame, ToolsSame, PrefixSame    bool
	SystemChanges, Messages, LatestUser, LatestBlocks, Results, ErrorResults, TrailingSystems     int
	RequestError, RegistryError                                                                   bool
	TrailingSame, AssistantTextExact, ReadInputExact, ResultIDMatches                             bool
	TrailingTextSame, TrailingPrefixSame                                                          bool
	OldStandingBlocks, NewStandingBlocks, ChangedStandingText, OldStandingBytes, NewStandingBytes int
	AssistantBlocks, AssistantTools, CallerFields, OtherUseFields                                 int
}

func compareDefaultRequests(first, next *anthropic.Request) defaultClientComparison {
	c := defaultClientComparison{Seen: true, IdentitySame: first.Identity == next.Identity, ModelSame: first.Model == next.Model, EffortSame: first.Effort == next.Effort, SystemSame: true, MetadataSame: bytes.Equal(first.Extra["metadata"], next.Extra["metadata"]), ToolsSame: reflect.DeepEqual(first.Tools, next.Tools), PrefixSame: true, Messages: len(next.Messages), LatestUser: next.LatestUserIndex()}
	if len(first.System) != len(next.System) {
		c.SystemSame = false
	}
	for i := range min(len(first.System), len(next.System)) {
		if first.System[i].Text != next.System[i].Text {
			c.SystemSame = false
			c.SystemChanges++
		}
	}
	h := history.New([32]byte{1})
	if len(first.Messages) > len(next.Messages) {
		c.PrefixSame = false
	}
	for i := range min(len(first.Messages), len(next.Messages)) {
		a, ae := h.Message(first.Messages[i])
		b, be := h.Message(next.Messages[i])
		c.PrefixSame = c.PrefixSame && ae == nil && be == nil && a == b
	}
	if c.LatestUser >= 0 {
		c.LatestBlocks = len(next.Messages[c.LatestUser].Content)
		for _, message := range next.Messages[c.LatestUser+1:] {
			if message.Role == "system" {
				c.TrailingSystems++
			}
		}
	}
	results, _ := next.LatestToolResults()
	c.Results = len(results)
	for _, result := range results {
		if result.IsError {
			c.ErrorResults++
		}
	}
	if c.TrailingSystems == 1 && len(first.Messages) > 0 {
		old, nextStanding := first.Messages[len(first.Messages)-1].Content, next.Messages[len(next.Messages)-1].Content
		c.OldStandingBlocks, c.NewStandingBlocks = len(old), len(nextStanding)
		c.TrailingTextSame, c.TrailingPrefixSame = len(old) == len(nextStanding), true
		for _, block := range old {
			c.OldStandingBytes += len(block.Text)
		}
		for _, block := range nextStanding {
			c.NewStandingBytes += len(block.Text)
		}
		for i := range min(len(old), len(nextStanding)) {
			if old[i].Type != nextStanding[i].Type || old[i].Text != nextStanding[i].Text {
				c.ChangedStandingText++
				c.TrailingTextSame, c.TrailingPrefixSame = false, false
			}
		}
		a, ae := h.Message(first.Messages[len(first.Messages)-1])
		b, be := h.Message(next.Messages[len(next.Messages)-1])
		c.TrailingSame = ae == nil && be == nil && a == b
	}
	if c.LatestUser > 0 {
		assistant := next.Messages[c.LatestUser-1]
		c.AssistantBlocks = len(assistant.Content)
		for _, block := range assistant.Content {
			if block.Type == "text" {
				c.AssistantTextExact = block.Text == "before client tool"
			}
			if block.Type != "tool_use" {
				continue
			}
			c.AssistantTools++
			var use map[string]json.RawMessage
			_ = json.Unmarshal(block.Raw, &use)
			for key := range use {
				switch key {
				case "type", "id", "name", "input", "cache_control":
				case "caller":
					c.CallerFields++
				default:
					c.OtherUseFields++
				}
			}
			var name, id string
			_ = json.Unmarshal(use["name"], &name)
			_ = json.Unmarshal(use["id"], &id)
			var input map[string]string
			_ = json.Unmarshal(use["input"], &input)
			c.ReadInputExact = name == "Read" && len(input) == 1 && filepath.Base(input["file_path"]) == "denied-fixture"
			c.ResultIDMatches = len(results) == 1 && results[0].ID == id
		}
	}
	return c
}

func (b *defaultClientBackend) Models(context.Context) ([]inference.Model, error) {
	return b.catalog.List(), nil
}

func (b *defaultClientBackend) Start(ctx context.Context, request *anthropic.Request) (inference.Turn, error) {
	if b.starts.Add(1) > b.limit {
		return nil, inference.ErrRequest
	}
	b.mu.Lock()
	if b.first == nil {
		b.first = request
	} else {
		b.comparison = compareDefaultRequests(b.first, request)
	}
	b.mu.Unlock()
	turn, err := b.Driver.Start(ctx, request)
	if err != nil {
		b.failed.Store(true)
		b.mu.Lock()
		b.comparison.RequestError = errors.Is(err, inference.ErrRequest)
		b.comparison.RegistryError = errors.Is(err, toolregistry.ErrRegistry)
		b.mu.Unlock()
	}
	return turn, err
}

func TestClaudeDefaultRequestThroughGatewayAndACP(t *testing.T) {
	t.Run("text", func(t *testing.T) { observeClaudeDefaultRequest(t, false) })
	t.Run("read-hook-denial", func(t *testing.T) { observeClaudeDefaultRequest(t, true) })
}

func observeClaudeDefaultRequest(t *testing.T, denyRead bool) {
	t.Helper()
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for the default-request test; no model credits")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-default-client-")
	if err != nil {
		t.Fatal("cannot create owned observation root")
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: 45 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	home, project, backendDir, workerDir := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "backend"), filepath.Join(root, "worker")
	for _, dir := range []string{home, project, backendDir, workerDir} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot create owned workspaces")
		}
	}
	settings := filepath.Join(home, "settings.json")
	sourceSettings := []byte("{}\n")
	if denyRead {
		sourceSettings = []byte(`{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"printf '%s' '{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"independent fixture denial\"}}'","timeout":2}]}]}}`)
	}
	if os.WriteFile(settings, sourceSettings, 0600) != nil {
		t.Fatal("cannot create owned settings")
	}
	before := fileFingerprint(t, settings)
	version, err := runner.Run(ctx, childproc.Command{Executable: executable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	clientVersion, ok := launcher.ClientVersionFromOutput(version.Stdout)
	if err != nil || !ok {
		t.Fatal("unverified installed client version")
	}
	fake := buildDenialACPFixture(t, ctx, runner, root)
	proxy := filepath.Join(filepath.Dir(buildRelayObserver(t)), "owned-relay")
	validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: workerDir})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Close()
	fixtureArgs, answer, requests := []string{"chat"}, "birch stone", int32(1)
	processLedger := filepath.Join(backendDir, "owned-processes")
	if denyRead {
		fixtureArgs, answer, requests = []string{"chat-tools-default-client", filepath.Join(project, "denied-fixture"), processLedger}, "independent client instruction restart complete", 2
	}
	driver, err := session.New(session.Config{Process: acp.Config{Executable: fake, Args: fixtureArgs, Directory: backendDir, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, ClientInfo: acp.Info{Name: "independent-default-client", Version: "1"}}, Validator: validator, RelayExecutable: proxy, SetupTimeout: 10 * time.Second, TurnTimeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	models, err := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent default-client fixture"}}, "fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	model, err := models.ClientID("fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	backend := &defaultClientBackend{Driver: driver, catalog: models, limit: requests}
	handler, err := gateway.New(gateway.Config{Tokens: tokens, Backend: backend, TurnTimeout: 15 * time.Second, FirstEventTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var shape defaultClientShape
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/messages" {
			body, err := io.ReadAll(io.LimitReader(r.Body, anthropic.MaxBodyBytes+1))
			_ = r.Body.Close()
			if err != nil || len(body) > anthropic.MaxBodyBytes {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			mu.Lock()
			shape.observe(body)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	command := profile.Command()
	command.Args = append(command.Args, "--print", "--output-format", "json", "--no-session-persistence", "Reply with a brief greeting.")
	result, runErr := runner.Run(ctx, command)
	mu.Lock()
	observed := shape
	mu.Unlock()
	t.Logf("client=%s, shape=%+v, backend_starts=%d, backend_failed=%v, state=%s, exit=%d, stdout_bytes=%d", clientVersion, observed, backend.starts.Load(), backend.failed.Load(), driver.State(), result.ExitCode, len(result.Stdout))
	backend.mu.Lock()
	t.Logf("continuation=%+v", backend.comparison)
	backend.first = nil
	backend.mu.Unlock()
	if runErr != nil || !bytes.Contains(result.Stdout, []byte(answer)) || int32(observed.Requests) != requests || observed.Tools < 3 || !observed.Stream || !observed.Read || !observed.Write || !observed.Bash || backend.starts.Load() != requests || backend.failed.Load() || driver.State() != session.Idle {
		t.Fatal("default client request did not complete through the real gateway and fake ACP")
	}
	if fileFingerprint(t, settings) != before {
		t.Error("owned source settings changed")
	}
	if err := driver.Close(); err != nil {
		t.Error("ACP and relay cleanup did not join")
	}
	if denyRead {
		data, err := os.ReadFile(processLedger)
		pids := strings.Fields(string(data))
		if err != nil || len(pids) != 2 {
			t.Fatal("expected one joined ACP replacement")
		}
		for _, value := range pids {
			pid, err := strconv.Atoi(value)
			if err != nil || pid <= 1 || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
				t.Error("observed ACP group survived cleanup")
			}
		}
	}
	validator.Close()
	if err := profile.Close(); err != nil {
		t.Error("client runtime cleanup failed")
	}
	if _, err := os.Stat(profile.Path()); !os.IsNotExist(err) {
		t.Error("client runtime survived cleanup")
	}
}
