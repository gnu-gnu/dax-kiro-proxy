package interop_test

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/ndjson"
)

type skillInventoryProbe struct {
	mode    string
	names   map[string]string
	paths   map[string]string
	seen    map[string]bool
	before  map[string][]byte
	context contextShapeReport
	public  int
	private int
}

// The fixture asks whether owned skill names are advertised, without invoking a skill or model.
// Public hypotheses: https://kiro.dev/changelog/cli/2-10/ and
// https://agentclientprotocol.com/protocol/v1/slash-commands.
func TestKiroPinnedSkillInheritance(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for owned skill advertisements; no model prompt")
	}
	for _, mode := range []string{"explicit", "inherit", "suppress"} {
		if !t.Run(mode, func(t *testing.T) {
			observePinnedInventory(t, executable, []string{}, []string{}, "", inventoryVariant{version: launcher.SupportedKiroVersion, context: true, skills: &skillInventoryProbe{mode: mode}})
		}) {
			return
		}
	}
}

func (p *skillInventoryProbe) prepare(t *testing.T, configuration, launch, agentName string) {
	t.Helper()
	if p.mode != "explicit" && p.mode != "inherit" && p.mode != "suppress" {
		t.Fatal("unknown skill control")
	}
	p.names, p.seen, p.before = map[string]string{}, map[string]bool{}, map[string][]byte{}
	p.paths = map[string]string{}
	resources := []string{}
	for _, location := range []struct{ label, root string }{{"launch", filepath.Join(launch, ".kiro", "skills")}, {"configuration", filepath.Join(configuration, "skills")}} {
		name := "dax-skill-" + strings.ToLower(rand.Text())
		path := filepath.Join(location.root, name, "SKILL.md")
		data := []byte("---\nname: " + name + "\ndescription: Independent read-only skill metadata control.\n---\nThis fixture has no actions.\n")
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot prepare owned skill fixture")
		}
		p.names[location.label], p.before[path] = name, data
		p.paths[location.label] = path
		if location.label == "launch" {
			// Retain the independently seeded relative name as a distinct observation; do not
			// assign an arbitrary base to relative names returned by the backend.
			p.paths["launch_relative"] = filepath.Join(".kiro", "skills", name, "SKILL.md")
		}
		resources = append(resources, "skill://"+path)
	}
	settings := filepath.Join(configuration, "settings", "cli.json")
	data, _ := json.Marshal(map[string]bool{"chat.disableInheritingDefaultResources": p.mode != "inherit"})
	if os.WriteFile(settings, data, 0600) != nil {
		t.Fatal("cannot prepare owned skill setting")
	}
	p.before[settings] = data
	path := filepath.Join(launch, ".kiro", "agents", agentName+".json")
	data = readOwnedInventoryAgent(t, path)
	if p.mode == "explicit" {
		var agent map[string]json.RawMessage
		if json.Unmarshal(data, &agent) != nil {
			t.Fatal("cannot read owned skill agent")
		}
		agent["resources"], _ = json.Marshal(resources)
		data, _ = json.Marshal(agent)
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot write owned explicit skill agent")
		}
	}
	p.before[path] = data
}

// Called only after the inventory observer validates the notification's owned session.
func (p *skillInventoryProbe) observe(method string, fields map[string]json.RawMessage) error {
	var raw json.RawMessage
	switch method {
	case "_kiro.dev/commands/available":
		raw = fields["commands"]
		p.private++
	case "session/update":
		update, err := ndjson.Object(fields["update"])
		if err != nil {
			return errInventoryShape
		}
		if string(update["sessionUpdate"]) != `"available_commands_update"` {
			return nil
		}
		raw = update["availableCommands"]
		p.public++
	default:
		return nil
	}
	var entries []json.RawMessage
	if inventoryKind(raw) != "array" || json.Unmarshal(raw, &entries) != nil || len(entries) > 128 {
		return errInventoryShape
	}
	for _, entry := range entries {
		item, err := ndjson.Object(entry)
		name, valid := inventoryString(item["name"], 256)
		if err != nil || !valid {
			return errInventoryShape
		}
		for label, expected := range p.names {
			if strings.TrimPrefix(name, "/") == expected {
				p.seen[label] = true
			}
		}
	}
	return nil
}

func (p *skillInventoryProbe) check(t *testing.T) {
	t.Helper()
	unchanged := true
	for path, data := range p.before {
		if string(readOwnedInventoryAgent(t, path)) != string(data) {
			unchanged = false
			t.Error("owned skill source changed")
		}
	}
	t.Logf("skill_control=%s, public_advertisements=%d, private_advertisements=%d, owned_command_matches=%v, owned_context_matches=%v, sources_unchanged=%v, prompt_sent=false", p.mode, p.public, p.private, p.seen, p.context.OwnedMatches, unchanged)
	want := p.mode != "suppress"
	launchMatched := p.context.OwnedMatches["launch"] || p.context.OwnedMatches["launch_relative"]
	if !p.context.Success || !p.context.Verbose || launchMatched != want || p.context.OwnedMatches["configuration"] != want ||
		p.context.RelativeNames != btoi(p.context.OwnedMatches["launch_relative"]) ||
		want && (p.context.MatchedItems != 2 || p.context.ContextTokens <= 0) ||
		!want && (p.context.MatchedItems != 0 || p.context.ContextTokens != 0 || len(p.seen) != 0) {
		t.Error("owned skill inclusion/exclusion was not established")
	}
}

func TestSkillAdvertisementObserverRequiresOwnedNames(t *testing.T) {
	for _, c := range []struct {
		method, params string
		want           bool
	}{
		{"_kiro.dev/commands/available", `{"sessionId":"owned","commands":[{"name":"owned-skill"}]}`, true},
		{"session/update", `{"sessionId":"owned","update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"/owned-skill"}]}}`, true},
		{"session/update", `{"sessionId":"owned","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"owned-skill"}}}`, false},
		{"_kiro.dev/commands/available", `{"sessionId":"owned","commands":[{"name":"other","description":"owned-skill"}]}`, false},
		{"_kiro.dev/commands/available", `{"sessionId":"foreign","commands":[{"name":"owned-skill"}]}`, false},
	} {
		p := &skillInventoryProbe{names: map[string]string{"launch": "owned-skill"}, seen: map[string]bool{}}
		r := inventoryReport{skills: p, NotificationKinds: map[string]int{}, NativeNames: map[string]bool{}}
		_ = r.observe(acp.Notification{Method: c.method, Params: json.RawMessage(c.params)}, "owned")
		if p.seen["launch"] != c.want {
			t.Fatal("skill observation accepted unrelated text or a foreign session")
		}
	}
}
