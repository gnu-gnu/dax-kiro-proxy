package launcher_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/toolregistry"
)

// Schema-worker behavior is independently covered by schemacheck; these fixtures exercise only
// alias-derived agent configuration. They never authorize or perform a tool call.
type agentFixtureValidator struct{}

func (agentFixtureValidator) Check(context.Context, []byte) error            { return nil }
func (agentFixtureValidator) Validate(context.Context, []byte, []byte) error { return nil }

func agentPrivateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(t.TempDir(), "agent-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCandidateAgentEnumeratesOnlyItsValidatedRelayAliases(t *testing.T) {
	for _, names := range [][]string{nil, {"Read", "Bash"}} {
		raw := []json.RawMessage{}
		for _, name := range names {
			declaration, _ := json.Marshal(map[string]any{"name": name, "input_schema": map[string]any{"type": "object"}})
			raw = append(raw, declaration)
		}
		registry, err := toolregistry.Build(t.Context(), raw, nil, agentFixtureValidator{})
		if err != nil {
			t.Fatal(err)
		}
		directory := agentPrivateDir(t)
		cfg := launcher.AgentConfig{Directory: directory, Registry: registry, RelayExecutable: "/owned/relay", RelayConfig: "/owned/relay-control.json"}
		agent, err := launcher.WriteCandidateAgent(cfg)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(agent.Path)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Name           string
			Tools, Allowed []string `json:"-"`
			MCP            map[string]struct {
				Command string
				Args    []string
				Env     map[string]string
			} `json:"mcpServers"`
			Resources  []string
			Hooks      map[string]any
			IncludeMCP bool `json:"includeMcpJson"`
		}
		if json.Unmarshal(data, &got) != nil {
			t.Fatal("invalid candidate agent JSON")
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(data, &fields)
		_ = json.Unmarshal(fields["tools"], &got.Tools)
		_ = json.Unmarshal(fields["allowedTools"], &got.Allowed)
		expected := []string{}
		for _, tool := range registry.Tools() {
			expected = append(expected, "@dax_session/"+tool.Alias)
		}
		if got.Name != agent.Name || !reflect.DeepEqual(got.Tools, expected) || !reflect.DeepEqual(got.Allowed, expected) || got.IncludeMCP || len(got.Resources) != 0 || len(got.Hooks) != 0 {
			t.Fatal("candidate agent expands its declared authority")
		}
		if len(got.MCP) != 1 || got.MCP["dax_session"].Command != cfg.RelayExecutable || !reflect.DeepEqual(got.MCP["dax_session"].Args, []string{"relay", "--config", cfg.RelayConfig}) || len(got.MCP["dax_session"].Env) != 0 {
			t.Fatal("candidate agent contains another MCP command or environment")
		}
		if strings.Contains(string(data), `"*"`) || strings.Contains(string(data), "execute_bash") || len(agent.PolicyDigest) != 64 || agent.ExecutionVerified {
			t.Fatal("candidate agent claims unproved execution restrictions")
		}
		if !strings.HasPrefix(agent.Path, filepath.Join(directory, ".kiro", "agents")+string(os.PathSeparator)) {
			t.Fatal("candidate agent escapes its owned directory")
		}
		for _, path := range []string{filepath.Dir(agent.Path), agent.Path} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatal("candidate profile is not private")
			}
		}
		again, err := launcher.WriteCandidateAgent(cfg)
		if err != nil || again.Name != agent.Name || again.PolicyDigest != agent.PolicyDigest {
			t.Fatal("same candidate policy is unstable")
		}
	}
}

func TestCandidateAgentRefusesLinksAndConflictingOwnedFiles(t *testing.T) {
	registry, err := toolregistry.Build(t.Context(), nil, nil, agentFixtureValidator{})
	if err != nil {
		t.Fatal(err)
	}
	root, outside := agentPrivateDir(t), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".kiro")); err != nil {
		t.Fatal(err)
	}
	cfg := launcher.AgentConfig{Directory: root, Registry: registry, RelayExecutable: "/owned/relay", RelayConfig: "/owned/config"}
	if _, err := launcher.WriteCandidateAgent(cfg); !errors.Is(err, launcher.ErrRuntime) {
		t.Fatal("candidate profile traversed a directory link")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatal("candidate profile changed outside data")
	}
	cfg.Directory = agentPrivateDir(t)
	agent, err := launcher.WriteCandidateAgent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent.Path, []byte(`{"changed":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.WriteCandidateAgent(cfg); !errors.Is(err, launcher.ErrRuntime) {
		t.Fatal("candidate profile silently overwrote a different policy")
	}
	if data, err := os.ReadFile(agent.Path); err != nil || string(data) != `{"changed":true}` {
		t.Fatal("conflicting policy was changed")
	}
}
