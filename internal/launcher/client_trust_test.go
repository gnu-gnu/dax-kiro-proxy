package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/ndjson"
)

func TestInsertProjectTrustSplicesOneKeyIntoUnchangedBytes(t *testing.T) {
	const key = "/owned/project"
	const flag = `"hasTrustDialogAccepted":true`
	for _, c := range []struct {
		name, raw, fragment string
		changed             bool
	}{
		{"empty document", `{}`, `"projects":{"/owned/project":{` + flag + `}}`, true},
		{"no projects key", `{"numStartups": 3, "mcpServers": {"a": {"command": "/x"}}}`, `"projects":{"/owned/project":{` + flag + `}},`, true},
		{"empty projects", `{"projects": {}}`, `"/owned/project":{` + flag + `}`, true},
		{"other project only", "{\n  \"projects\": {\n    \"/owned/other\": {\n      \"hasTrustDialogAccepted\": true,\n      \"history\": [\"{not json\", \"]\"]\n    }\n  }\n}\n", `"/owned/project":{` + flag + `},`, true},
		{"entry without key", `{"projects": {"/owned/project": {"allowedTools": ["Read"], "nested": {"x": [1, {"y": "}"}]}}}}`, flag + `,`, true},
		{"empty entry", `{"projects": {"/owned/project": {}}}`, flag, true},
		{"false becomes true", `{"projects": {"/owned/project": {"a": 1, "hasTrustDialogAccepted": false, "b": [false]}}}`, "", true},
		{"already true", `{"projects": {"/owned/project": {"hasTrustDialogAccepted": true}}}`, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, changed, err := insertProjectTrust([]byte(c.raw), key)
			if err != nil || changed != c.changed {
				t.Fatal("unexpected outcome", err, changed)
			}
			if !changed {
				if string(out) != c.raw {
					t.Fatal("unchanged document was rewritten")
				}
				return
			}
			if c.fragment != "" {
				if strings.Replace(string(out), c.fragment, "", 1) != c.raw || strings.Count(string(out), c.fragment) != 1 {
					t.Fatalf("other bytes changed or fragment absent: %s", out)
				}
			} else if strings.Replace(string(out), `"hasTrustDialogAccepted": true`, `"hasTrustDialogAccepted": false`, 1) != c.raw {
				t.Fatalf("false was not replaced in place: %s", out)
			}
			fields, err := ndjson.Object(out)
			if err != nil {
				t.Fatal("result is not a strict object")
			}
			projects, err := ndjson.Object(fields["projects"])
			if err != nil {
				t.Fatal("projects is not an object")
			}
			entry, err := ndjson.Object(projects[key])
			if err != nil || string(entry[trustKey]) != "true" {
				t.Fatal("trust was not recorded for the project")
			}
			if _, err := clientMCPProjection(out); err != nil {
				t.Fatal("the projection would reject the written document")
			}
		})
	}
	for _, raw := range []string{`[]`, `{"projects": null}`, `{"projects": []}`, `{"projects": {"/owned/project": 5}}`, `{"projects": {"/owned/project": {"hasTrustDialogAccepted": "true"}}}`, `{"a": 1, "a": 2}`, `{"projects": {"/owned/project": {`} {
		if _, _, err := insertProjectTrust([]byte(raw), key); err == nil {
			t.Fatalf("malformed or unexpected document accepted: %s", raw)
		}
	}
}

func trustConfig(t *testing.T) ClientConfig {
	t.Helper()
	base := t.TempDir()
	home, project := filepath.Join(base, "home"), filepath.Join(base, "project")
	for _, path := range []string{home, project} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	return ClientConfig{RuntimeParent: base, Home: home, Project: project, Executable: "/fixture/claude", Version: SupportedClientVersion, Model: "claude-dax-fixture-0123456789abcdef", GatewayURL: "http://127.0.0.1:32123", ModelToken: tokens.Model, UserSettings: filepath.Join(home, "settings.json"), Environment: []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "TERM=xterm-256color"}}
}

func acceptInPrivateProfile(t *testing.T, p *ClientProfile, key string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"projects": map[string]any{key: map[string]any{"hasTrustDialogAccepted": true}}, "numStartups": 1})
	if err := os.WriteFile(filepath.Join(p.Path(), "client", ".claude.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPersistProjectTrustWritesOnlyTheAcceptedAnswer(t *testing.T) {
	original := "{\n  \"numStartups\": 3,\n  \"hasCompletedOnboarding\": true,\n  \"projects\": {\n    \"/owned/other\": {\n      \"hasTrustDialogAccepted\": true,\n      \"allowedTools\": []\n    }\n  },\n  \"mcpServers\": {}\n}\n"
	cfg := trustConfig(t)
	source := filepath.Join(cfg.Home, ".claude.json")
	if err := os.WriteFile(source, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if written, err := p.persistProjectTrust(); err != nil || written {
		t.Fatal("a launch without an accepted dialog wrote the source")
	}
	// The client may record the resolved path rather than the launch path.
	resolved, err := filepath.EvalSymlinks(cfg.Project)
	if err != nil {
		t.Fatal(err)
	}
	acceptInPrivateProfile(t, p, resolved)
	written, err := p.persistProjectTrust()
	if err != nil || !written {
		t.Fatal("accepted answer was not written back", err, written)
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(resolved)
	fragment := string(encoded) + `:{"hasTrustDialogAccepted":true},`
	if strings.Replace(string(after), fragment, "", 1) != original || strings.Count(string(after), fragment) != 1 {
		t.Fatalf("source changed beyond the one accepted key: %s", after)
	}
	if info, err := os.Lstat(source); err != nil || info.Mode().Perm() != 0600 || !info.Mode().IsRegular() {
		t.Fatal("source mode or identity changed")
	}
	if entries, _ := os.ReadDir(cfg.Home); len(entries) != 2 {
		t.Fatal("temporary file left beside the source")
	}
	if written, err := p.persistProjectTrust(); err != nil || written {
		t.Fatal("a second call rewrote the source")
	}
	if _, err := clientMCPProjection(after); err != nil {
		t.Fatal("the next launch would reject the written source")
	}
}

func TestPersistProjectTrustSkipsChangedAbsentOrClosedSources(t *testing.T) {
	// Changed since launch: the answer is dropped and the source keeps its new bytes.
	cfg := trustConfig(t)
	source := filepath.Join(cfg.Home, ".claude.json")
	if err := os.WriteFile(source, []byte(`{"numStartups": 1}`), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	acceptInPrivateProfile(t, p, cfg.Project)
	if err := os.WriteFile(source, []byte(`{"numStartups": 2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if written, err := p.persistProjectTrust(); err != nil || written {
		t.Fatal("a source changed during the launch was rewritten")
	}
	if after, _ := os.ReadFile(source); string(after) != `{"numStartups": 2}` {
		t.Fatal("changed source was not left alone")
	}
	// Absent at launch: nothing is created.
	cfg = trustConfig(t)
	p2, err := PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	acceptInPrivateProfile(t, p2, cfg.Project)
	if written, err := p2.persistProjectTrust(); err != nil || written {
		t.Fatal("an absent source was created")
	}
	if _, err := os.Lstat(filepath.Join(cfg.Home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("source file appeared")
	}
	// Already recorded at the source (projected trust): nothing is written.
	cfg = trustConfig(t)
	source = filepath.Join(cfg.Home, ".claude.json")
	trusted, _ := json.Marshal(map[string]any{"projects": map[string]any{cfg.Project: map[string]any{"hasTrustDialogAccepted": true}}})
	if err := os.WriteFile(source, trusted, 0600); err != nil {
		t.Fatal(err)
	}
	p3, err := PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	acceptInPrivateProfile(t, p3, cfg.Project)
	if written, err := p3.persistProjectTrust(); err != nil || written {
		t.Fatal("an already trusted project was rewritten")
	}
	if after, _ := os.ReadFile(source); string(after) != string(trusted) {
		t.Fatal("trusted source changed")
	}
	if err := p3.Close(); err != nil {
		t.Fatal(err)
	}
	if written, err := p3.persistProjectTrust(); err != nil || written {
		t.Fatal("a closed profile wrote the source")
	}
}
