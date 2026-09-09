package launcher_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/launcher"
)

func TestClientMCPStateKeepsNativeScopesWithoutProviderState(t *testing.T) {
	cfg := profileConfig(t)
	source := []byte(`{"apiKey":"unrelated-provider-secret","oauthAccount":{"token":"unrelated-oauth-secret"},"mcpServers":{"owned":{"command":"/fixture/mcp","env":{"MCP_ACCESS_TOKEN":"owned-mcp-credential"}}},"disabledMcpServers":["inactive"],"projects":{"/fixture/project":{"mcpServers":{"local":{"type":"http","url":"https://mcp.invalid/endpoint","headers":{"Authorization":"owned-mcp-header"}}},"enabledMcpjsonServers":["approved"],"disabledMcpjsonServers":["refused"],"enabledMcpServers":["owned-opt-in"],"enableAllProjectMcpServers":false,"hasTrustDialogAccepted":true,"lastPrompt":"private-conversation-must-not-copy"}},"model":"unrelated-provider-model"}`)
	path := filepath.Join(cfg.Home, ".claude.json")
	writeSettings(t, path, source)
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	profile := envMap(p.Command().Environment)["CLAUDE_CONFIG_DIR"]
	data, err := os.ReadFile(filepath.Join(profile, ".claude.json"))
	if err != nil {
		t.Fatal("client MCP runtime state missing")
	}
	var actual, original map[string]json.RawMessage
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal(source, &original) != nil {
		t.Fatal("invalid projected state")
	}
	if len(actual) != 3 || !bytes.Equal(actual["mcpServers"], original["mcpServers"]) || !bytes.Equal(actual["disabledMcpServers"], original["disabledMcpServers"]) {
		t.Fatal("user MCP scope was changed or unrelated state copied")
	}
	for _, forbidden := range [][]byte{[]byte("unrelated-"), []byte("private-conversation")} {
		if bytes.Contains(data, forbidden) {
			t.Fatal("provider or conversation state copied")
		}
	}
	for _, required := range [][]byte{[]byte("owned-mcp-credential"), []byte("owned-mcp-header"), []byte(`"hasTrustDialogAccepted":true`), []byte(`"enableAllProjectMcpServers":false`), []byte(`"disabledMcpjsonServers":["refused"]`), []byte(`"enabledMcpServers":["owned-opt-in"]`)} {
		if !bytes.Contains(data, required) {
			t.Fatal("MCP declaration or existing decision disappeared")
		}
	}
	if info, err := os.Stat(filepath.Join(profile, ".claude.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("runtime MCP credentials are not private")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, source) {
		t.Fatal("MCP source state changed")
	}
}

func TestClientMCPStateRejectsUnsafeOrAmbiguousSource(t *testing.T) {
	for _, source := range []string{`[]`, `{"mcpServers":[]}`, `{"projects":null}`, `{"projects":{"/fixture":false}}`, `{"disabledMcpServers":true}`, `{"disabledMcpServers":[null]}`, `{"enableAllProjectMcpServers":"true"}`, `{"mcpServers":{},"mcpServers":{}}`, `{"futureMcpPolicy":true}`, strings.Repeat(" ", launcher.MaxSettingsBytes) + `{}`} {
		cfg := profileConfig(t)
		writeSettings(t, filepath.Join(cfg.Home, ".claude.json"), []byte(source))
		if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrSettings) {
			if p != nil {
				p.Close()
			}
			t.Fatal("ambiguous MCP state accepted")
		}
		entries, err := os.ReadDir(cfg.RuntimeParent)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "dax-runtime-") {
				t.Fatal("invalid MCP source created runtime artifacts")
			}
		}
	}
	for _, kind := range []string{"link", "permissions"} {
		cfg := profileConfig(t)
		path := filepath.Join(cfg.Home, ".claude.json")
		writeSettings(t, path, []byte(`{}`))
		if kind == "link" {
			if os.Rename(path, path+".source") != nil || os.Symlink(path+".source", path) != nil {
				t.Fatal("cannot prepare unsafe fixture")
			}
		} else if os.Chmod(path, 0666) != nil {
			t.Fatal("cannot prepare unsafe mode")
		}
		if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrSettings) {
			if p != nil {
				p.Close()
			}
			t.Fatal("unsafe MCP source accepted")
		}
	}
}
