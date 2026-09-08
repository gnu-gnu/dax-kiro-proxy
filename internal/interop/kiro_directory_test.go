package interop_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"dax-kiro-proxy/internal/privatefs"
)

type inventoryDirectoryProbe struct {
	// nil leaves the new session workspace without a local agent; an empty list writes one.
	projectTools []string
	agents       map[string][]byte
}

// Each case has separate owned configuration roots. The conflicting profiles expose only an
// empty inventory or the already observed native read entry. No prompt or tool call is admitted.
func TestKiroPinnedAgentDirectorySelection(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for owned launch/session directory controls; no model prompt")
	}
	for i, test := range []struct {
		name    string
		tools   []string
		listed  []string
		variant inventoryVariant
	}{
		{name: "same-directory-native", tools: []string{"fs_read"}, listed: []string{"read"}},
		{name: "split-without-session-agent", tools: []string{"fs_read"}, listed: []string{"read"}, variant: inventoryVariant{directories: &inventoryDirectoryProbe{}}},
		{name: "split-native-launch-empty-session", tools: []string{"fs_read"}, listed: []string{"read"}, variant: inventoryVariant{directories: &inventoryDirectoryProbe{projectTools: []string{}}}},
		{name: "split-empty-launch-native-session", tools: []string{}, listed: []string{}, variant: inventoryVariant{directories: &inventoryDirectoryProbe{projectTools: []string{"fs_read"}}}},
	} {
		passed := t.Run(test.name, func(t *testing.T) {
			observePinnedInventory(t, executable, test.tools, test.listed, "", test.variant)
		})
		if i == 0 && !passed {
			return
		}
	}
}

func (p *inventoryDirectoryProbe) prepare(t *testing.T, root, launch, name string) string {
	t.Helper()
	p.agents = map[string][]byte{}
	launchAgent := filepath.Join(launch, ".kiro", "agents", name+".json")
	p.agents[launchAgent] = readOwnedInventoryAgent(t, launchAgent)
	project := filepath.Join(root, "session-workspace")
	if os.Mkdir(project, 0700) != nil {
		t.Fatal("cannot create owned session workspace")
	}
	if p.projectTools != nil {
		for _, directory := range []string{filepath.Join(project, ".kiro"), filepath.Join(project, ".kiro", "agents")} {
			if os.Mkdir(directory, 0700) != nil {
				t.Fatal("cannot create owned conflicting agent directory")
			}
		}
		raw, err := json.Marshal(map[string]any{
			"name": name, "description": "Independent session-directory inventory control", "tools": p.projectTools,
			"allowedTools": []string{}, "mcpServers": map[string]any{}, "resources": []string{}, "hooks": map[string]any{}, "includeMcpJson": false,
		})
		if err != nil {
			t.Fatal("cannot encode independent conflicting agent")
		}
		filename := filepath.Join(project, ".kiro", "agents", name+".json")
		if os.WriteFile(filename, raw, 0600) != nil {
			t.Fatal("cannot write independent conflicting agent")
		}
		p.agents[filename] = raw
	}
	return project
}

func (p *inventoryDirectoryProbe) check(t *testing.T) {
	t.Helper()
	unchanged := true
	for filename, before := range p.agents {
		if !bytes.Equal(before, readOwnedInventoryAgent(t, filename)) {
			unchanged = false
			t.Error("owned inventory agent changed during the read-only observation")
		}
	}
	t.Logf("separate_session_directory=true, session_agent_present=%v, source_agent_count=%d, source_agents_unchanged=%v", p.projectTools != nil, len(p.agents), unchanged)
}

func readOwnedInventoryAgent(t *testing.T, filename string) []byte {
	t.Helper()
	store, err := privatefs.New(filepath.Dir(filename))
	if err != nil {
		t.Fatal("cannot open owned inventory agent directory")
	}
	raw, err := store.Read(filepath.Base(filename), 64<<10)
	if err != nil {
		t.Fatal("cannot read bounded owned inventory agent")
	}
	return raw
}
