package interop_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/toolregistry"
)

type scopeRelay struct {
	name, executable, alias string
	broker                  *relay.Broker
	socket                  *relay.Socket
}

type mcpScopeProbe struct {
	mode    string
	relays  []scopeRelay
	started int
}

// Every declared server is this repository's effect-free relay with closed broker admission.
// These read-only controls observe startup and listing; they never ask a model to invoke a tool.
func TestKiroPinnedMCPFileScopeObservation(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for owned MCP scope controls; no model prompt or tool call")
	}
	for _, mode := range []string{"explicit", "default", "flag-false", "candidate"} {
		probe := &mcpScopeProbe{mode: mode}
		if !t.Run(mode, func(t *testing.T) {
			for _, name := range []string{"dax_scope_global_flat", "dax_scope_global_settings", "dax_scope_project_flat", "dax_scope_project_settings"} {
				probe.relays = append(probe.relays, scopeRelay{name: name, executable: buildRelayObserver(t)})
			}
			observePinnedInventory(t, executable, nil, nil, buildRelayObserver(t), inventoryVariant{sources: probe})
		}) {
			return
		}
		if mode == "default" && probe.started == 0 {
			t.Log("no seeded standalone source activated; exclusion comparisons remain unverified and were not run")
			return
		}
	}
}

func (p *mcpScopeProbe) prepare(t *testing.T, ctx context.Context, root, configuration, cwd, agentPath string, required *inventoryPrerequisite) []string {
	t.Helper()
	store, err := privatefs.New(filepath.Dir(agentPath))
	if err != nil {
		t.Fatal("cannot open owned scope agent directory")
	}
	raw, err := store.Read(filepath.Base(agentPath), 64<<10)
	if err != nil {
		t.Fatal("cannot read owned scope agent")
	}
	var fields map[string]json.RawMessage
	var servers map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(fields["mcpServers"], &servers) != nil {
		t.Fatal("invalid owned scope agent")
	}
	refs := []string{"@dax_session/" + required.Alias}
	required.ObservedTools = map[string]string{"dax_session": required.Alias}
	required.WaitForAllMCP = p.mode == "explicit"
	required.SettleWindow = time.Second
	paths := []string{filepath.Join(configuration, "mcp.json"), filepath.Join(configuration, "settings", "mcp.json"), filepath.Join(cwd, ".kiro", "mcp.json"), filepath.Join(cwd, ".kiro", "settings", "mcp.json")}
	for i := range p.relays {
		item := &p.relays[i]
		tool, err := json.Marshal(map[string]any{"name": "Scope" + rand.Text(), "description": "Independent effect-free scope marker", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}})
		if err != nil {
			t.Fatal("cannot encode synthetic scope tool")
		}
		registry, err := toolregistry.Build(ctx, []json.RawMessage{tool}, nil, syntaxFixtureValidator{})
		if err != nil {
			t.Fatal("cannot build independent scope registry")
		}
		item.alias = registry.Tools()[0].Alias
		item.broker, err = relay.NewBroker(registry, relay.Limits{ToolTimeout: 5 * time.Second})
		if err != nil {
			t.Fatal("cannot create effect-free scope broker")
		}
		item.socket, err = relay.Listen(item.broker, relay.SocketConfig{BaseDirectory: root})
		if err != nil {
			t.Fatal("cannot create private scope control socket")
		}
		definition, err := json.Marshal(map[string]any{"command": item.executable, "args": []string{"relay", "--config", item.socket.ConfigPath()}, "env": map[string]string{}, "disabled": false})
		if err != nil {
			t.Fatal("cannot encode owned scope server")
		}
		data, err := json.Marshal(map[string]any{"mcpServers": map[string]json.RawMessage{item.name: definition}})
		if err != nil || os.MkdirAll(filepath.Dir(paths[i]), 0700) != nil || os.WriteFile(paths[i], data, 0600) != nil {
			t.Fatal("cannot write owned scope configuration")
		}
		if p.mode == "explicit" {
			servers[item.name] = definition
		}
		if p.mode != "candidate" {
			refs = append(refs, "@"+item.name+"/"+item.alias)
		}
		required.ObservedTools[item.name] = item.alias
	}
	fields["tools"], _ = json.Marshal(refs)
	fields["allowedTools"], _ = json.Marshal(refs)
	fields["mcpServers"], _ = json.Marshal(servers)
	if p.mode == "default" {
		delete(fields, "includeMcpJson")
	} else {
		fields["includeMcpJson"] = json.RawMessage(`false`)
	}
	encoded, err := json.Marshal(fields)
	if err != nil || len(encoded) > 64<<10 || store.Write(filepath.Base(agentPath), encoded) != nil {
		t.Fatal("cannot write owned scope candidate")
	}
	return refs
}

func (p *mcpScopeProbe) bind(t *testing.T, group int) {
	t.Helper()
	for _, item := range p.relays {
		if item.socket.BindProcess(group) != nil {
			t.Fatal("cannot bind an owned scope relay to ACP")
		}
	}
}

func (p *mcpScopeProbe) close(t *testing.T) {
	t.Helper()
	for _, item := range p.relays {
		if item.broker != nil {
			item.broker.Close()
		}
		if item.socket != nil && item.socket.Close() != nil {
			t.Error("scope relay cleanup failed")
		}
	}
}

func (p *mcpScopeProbe) check(t *testing.T, group int, report inventoryReport) {
	t.Helper()
	for _, item := range p.relays {
		_, err := os.Lstat(filepath.Join(filepath.Dir(item.executable), "relay-processes.txt"))
		started := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error("scope launch observation is unavailable")
		}
		peer, verified := item.socket.PeerPID()
		if started {
			p.started++
			if !verified {
				t.Error("started scope relay did not establish owned membership")
			}
			checkRelayCleanupWithAttachment(t, item.executable, group, peer)
		} else if verified || peer != 0 {
			t.Error("scope peer has no independent launch record")
		}
		stats := item.broker.Stats()
		if stats.Pending != 0 || stats.Queued != 0 || stats.Sealed != 0 {
			t.Error("scope probe retained tool work")
		}
		t.Logf("scope_mode=%s, source=%s, process_started=%v, attached=%v, mcp_notifications=%d, alias_listed=%v", p.mode, item.name, started, verified, report.MCPMatches[item.name], report.ToolMatches[item.name])
		if p.mode == "explicit" && (!started || !verified || report.MCPMatches[item.name] == 0 || !report.ToolMatches[item.name]) {
			t.Error("explicit scope positive control did not activate")
		}
		if p.mode == "candidate" && (started || report.MCPMatches[item.name] != 0 || report.ToolMatches[item.name]) {
			t.Error("candidate did not exclude an inherited scope server")
		}
	}
	p.close(t)
}

func (p *mcpScopeProbe) toolCount(t *testing.T, report inventoryReport) int {
	t.Helper()
	count := 0
	for _, matched := range report.ToolMatches {
		if matched {
			count++
		}
	}
	if p.mode == "explicit" && count != 5 {
		t.Error("explicit scope tools were not all enumerated")
	}
	return count
}
