package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/websearch"
)

type KiroSearchConfig struct {
	Installation                         KiroInfo
	Home, RuntimeParent, ProxyExecutable string
}

// D146 records finite native output conversion and execution-hook denial on Kiro 2.21.4.
const kiroNativeSearchVerified = true

// NewKiroSearchOpener is lazy. Each admitted nested request owns its own profile, workspace,
// agent, budget and process. Ordinary conversation agents never acquire native web tools.
func NewKiroSearchOpener(cfg KiroSearchConfig) (websearch.Open, error) {
	return newKiroSearchOpener(cfg, func(ctx context.Context, cfg acp.Config) (websearch.Peer, error) { return acp.Start(ctx, cfg) })
}
func newKiroSearchOpener(cfg KiroSearchConfig, start func(context.Context, acp.Config) (websearch.Peer, error)) (websearch.Open, error) {
	if start == nil || !identityPath(cfg.Home) || !identityPath(cfg.RuntimeParent) || !identityPath(cfg.ProxyExecutable) {
		return nil, ErrConfig
	}
	if _, err := resolveLaunchExecutable(cfg.ProxyExecutable, "", ""); err != nil {
		return nil, ErrConfig
	}
	parent, err := os.Lstat(cfg.RuntimeParent)
	if err != nil || !parent.IsDir() {
		return nil, ErrRuntime
	}
	return func(ctx context.Context, r *anthropic.Request, spec anthropic.SearchSpec) (result websearch.Session, resultErr error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, err := websearch.ValidateRequest(r, spec); err != nil {
			return nil, err
		}
		current, err := os.Lstat(cfg.RuntimeParent)
		if err != nil || !os.SameFile(parent, current) {
			return nil, ErrRuntime
		}
		root, err := os.MkdirTemp(cfg.RuntimeParent, "dax-web-search-")
		if err != nil {
			return nil, ErrRuntime
		}
		original, err := os.Lstat(root)
		if err != nil {
			_ = os.RemoveAll(root)
			return nil, ErrRuntime
		}
		owner := &searchOwner{root: root, original: original}
		defer func() {
			if result == nil {
				resultErr = errors.Join(resultErr, owner.Close())
			}
		}()
		work, budget := filepath.Join(root, "work"), filepath.Join(root, "budget")
		if os.Mkdir(work, 0700) != nil || os.Mkdir(budget, 0700) != nil {
			return nil, ErrRuntime
		}
		execution, err := PrepareKiroExecution(ctx, KiroExecutionConfig{Installation: cfg.Installation, Home: cfg.Home, Project: work, RuntimeDirectory: root})
		if err != nil {
			return nil, err
		}
		agents, err := privatefs.New(filepath.Join(work, ".kiro", "agents"))
		if err != nil {
			return nil, err
		}
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
		command := fmt.Sprintf("if %s web-search-budget %s %d; then exit 0; else exit 2; fi", quote(cfg.ProxyExecutable), quote(budget), spec.MaxUses)
		agent, err := json.Marshal(map[string]any{
			"name": "dax-web-search", "description": "Isolated finite web search",
			"prompt": "Use web_search for the supplied search request. Return source links from actual tool results. Do not request local tools. Search calls are bounded by the execution hook.",
			"tools":  []string{"web_search"}, "allowedTools": []string{"web_search"}, "mcpServers": map[string]any{}, "resources": []any{}, "includeMcpJson": false,
			"hooks": map[string]any{"preToolUse": []any{map[string]any{"command": command}}},
		})
		if err != nil || agents.Write("dax-web-search.json", agent) != nil {
			return nil, ErrRuntime
		}
		process := execution.Process
		process.Args = append(process.Args, "--agent", "dax-web-search")
		process.Limits = acp.Limits{FrameBytes: 1 << 20, EventQueue: 64, EventBytes: 4 << 20, Pending: 4, WriteQueue: 4, WriteBytes: 1 << 20, RequestTimeout: 3 * time.Minute}
		owner.peer, err = start(ctx, process)
		if err != nil {
			if errors.Is(err, acp.ErrCleanup) {
				owner.cleanupErr = acp.ErrCleanup
			}
			return nil, err
		}
		if owner.peer == nil {
			return nil, ErrRuntime
		}
		owner.session, err = websearch.Prepare(ctx, owner.peer, work, budget, r, spec)
		if err != nil {
			return nil, err
		}
		return owner, nil
	}, nil
}

type searchOwner struct {
	root       string
	original   os.FileInfo
	peer       websearch.Peer
	session    *websearch.ACPsession
	once       sync.Once
	cleanupErr error
}

func (s *searchOwner) Model() string { return s.session.Model() }
func (s *searchOwner) Run(ctx context.Context, emit func(inference.Event) bool) error {
	return s.session.Run(ctx, emit)
}
func (s *searchOwner) Close() error {
	s.once.Do(func() {
		if s.peer != nil && s.peer.Close() != nil {
			s.cleanupErr = acp.ErrCleanup
		}
		if s.cleanupErr != nil {
			return
		}
		current, err := os.Lstat(s.root)
		if err != nil || !current.IsDir() || current.Mode().Perm() != 0700 || !os.SameFile(s.original, current) || os.RemoveAll(s.root) != nil {
			s.cleanupErr = acp.ErrCleanup
		}
	})
	return s.cleanupErr
}
