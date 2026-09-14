package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/websearch"
)

type searchPolicyPeer struct {
	dir     string
	closed  bool
	cleanup bool
}

func (p *searchPolicyPeer) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	switch method {
	case "session/new":
		return json.RawMessage(`{"sessionId":"owned-policy","models":{"currentModelId":"fixture","availableModels":[{"modelId":"fixture"}]}}`), nil
	case "_kiro.dev/commands/execute":
		return json.RawMessage(`{"success":true,"data":{"tools":[{"name":"web_search"}]}}`), nil
	}
	return nil, acp.ErrProtocol
}
func (p *searchPolicyPeer) Next(context.Context) (acp.Notification, error) {
	return acp.Notification{Method: "_kiro.dev/commands/available", SessionID: "owned-policy", Params: json.RawMessage(`{"sessionId":"owned-policy","commands":[{"name":"tools"}]}`)}, nil
}
func (p *searchPolicyPeer) TryNext() (acp.Notification, bool, error) {
	return acp.Notification{}, false, nil
}
func (p *searchPolicyPeer) Activity() <-chan struct{} { return nil }
func (p *searchPolicyPeer) Close() error {
	p.closed = true
	if _, err := os.Stat(p.dir); err != nil {
		return acp.ErrCleanup
	}
	if p.cleanup {
		return acp.ErrCleanup
	}
	return nil
}

func TestKiroSearchUsesOwnedRestrictedProfile(t *testing.T) {
	usage, _ := usageLifecycleConfig(t)
	proxy := filepath.Join(filepath.Dir(usage.Installation.Executable), "proxy")
	if os.WriteFile(proxy, []byte("#!/bin/sh\nexit 2\n"), 0700) != nil {
		t.Fatal("fixture helper")
	}
	var peers []*searchPolicyPeer
	open, err := newKiroSearchOpener(KiroSearchConfig{Installation: usage.Installation, Home: usage.Home, RuntimeParent: usage.RuntimeParent, ProxyExecutable: proxy}, func(_ context.Context, cfg acp.Config) (websearch.Peer, error) {
		var agent struct {
			Tools, AllowedTools, Resources []string
			MCP                            map[string]any `json:"mcpServers"`
			Hooks                          map[string][]map[string]any
			IncludeMCP                     bool `json:"includeMcpJson"`
		}
		raw, err := os.ReadFile(filepath.Join(cfg.Directory, ".kiro", "agents", "dax-web-search.json"))
		if err != nil || json.Unmarshal(raw, &agent) != nil || !reflect.DeepEqual(agent.Tools, []string{"web_search"}) || !reflect.DeepEqual(agent.AllowedTools, agent.Tools) || agent.Resources == nil || len(agent.Resources) != 0 || agent.MCP == nil || len(agent.MCP) != 0 || agent.IncludeMCP || len(agent.Hooks) != 1 || len(agent.Hooks["preToolUse"]) != 1 {
			t.Fatal("search profile expanded execution authority")
		}
		hook := agent.Hooks["preToolUse"][0]
		if _, filtered := hook["matcher"]; filtered || !strings.Contains(hook["command"].(string), "web-search-budget") {
			t.Fatal("missing execution budget hook")
		}
		if cfg.Directory == usage.Home || !strings.HasPrefix(cfg.Directory, usage.RuntimeParent+"/") {
			t.Fatal("search reused user workspace")
		}
		env := map[string]string{}
		for _, entry := range cfg.Environment {
			k, v, _ := strings.Cut(entry, "=")
			env[k] = v
		}
		if len(env) != 6 || env["HOME"] != usage.Home || !strings.HasPrefix(env["KIRO_HOME"], usage.RuntimeParent+"/") {
			t.Fatal("search inherited ambient configuration")
		}
		suppression, _ := os.ReadFile(filepath.Join(env["KIRO_HOME"], "settings", "cli.json"))
		if string(suppression) != `{"chat.disableInheritingDefaultResources":true}` {
			t.Fatal("search inherited default resources")
		}
		p := &searchPolicyPeer{dir: cfg.Directory}
		peers = append(peers, p)
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := anthropic.DecodeRequest([]byte(`{"model":"fixture","max_tokens":32,"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],"messages":[{"role":"user","content":"synthetic search"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := catalog.New([]catalog.Backend{{ID: "fixture"}}, "fixture")
	r.Model, _ = cat.ClientID("fixture")
	for range 2 {
		s, err := open(t.Context(), r, anthropic.SearchSpec{MaxUses: 1})
		if err != nil {
			t.Fatal(err)
		}
		if s.Close() != nil || s.Close() != nil {
			t.Fatal("search cleanup")
		}
	}
	if len(peers) != 2 || peers[0].dir == peers[1].dir || !peers[0].closed || !peers[1].closed {
		t.Fatal("search owner reused")
	}
	for _, p := range peers {
		if _, err := os.Stat(p.dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("search owner retained")
		}
	}
}
