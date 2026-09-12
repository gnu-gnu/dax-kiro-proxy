package launcher

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
					t.Fatal("other bytes changed or trust fragment absent")
				}
			} else if strings.Replace(string(out), `"hasTrustDialogAccepted": true`, `"hasTrustDialogAccepted": false`, 1) != c.raw {
				t.Fatal("false was not replaced in place")
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
			t.Fatalf("malformed or unexpected document accepted: bytes=%d digest=%x", len(raw), sha256.Sum256([]byte(raw)))
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
		t.Fatal("source changed beyond the one accepted key")
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

func TestPersistProjectTrustDoesNotWriteThroughAnExistingClientLock(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			cfg := trustConfig(t)
			source := filepath.Join(cfg.Home, ".claude.json")
			const seed = `{"numStartups":3}`
			if os.WriteFile(source, []byte(seed), 0600) != nil {
				t.Fatal("cannot seed settings")
			}
			p, err := PrepareClient(cfg)
			if err != nil {
				t.Fatal("cannot prepare profile")
			}
			defer p.Close()
			acceptInPrivateProfile(t, p, cfg.Project)
			lock := source + ".lock"
			switch kind {
			case "directory":
				err = os.Mkdir(lock, 0700)
			case "file":
				err = os.WriteFile(lock, nil, 0600)
			case "symlink":
				err = os.Symlink(source, lock)
			}
			if err != nil {
				t.Fatal("cannot prepare competing lock")
			}
			before, err := os.Lstat(lock)
			if err != nil {
				t.Fatal("cannot observe competing lock")
			}
			written, err := p.persistProjectTrust()
			after, readErr := os.ReadFile(source)
			lockAfter, lockErr := os.Lstat(lock)
			if written || err != nil || readErr != nil || string(after) != seed {
				t.Error("trust persistence wrote through an existing lock")
			}
			if lockErr != nil || !os.SameFile(before, lockAfter) {
				t.Error("trust persistence changed another writer's lock")
			}
		})
	}
}

func TestTrustPublicationRechecksStagedInputs(t *testing.T) {
	for _, change := range []string{"source bytes", "source identity", "source absent", "source link", "source mode", "source readable mode", "staged bytes", "staged identity", "staged readable mode", "home replaced"} {
		t.Run(change, func(t *testing.T) {
			cfg := trustConfig(t)
			source := filepath.Join(cfg.Home, trustFile)
			const seed = `{"numStartups":3}`
			if os.WriteFile(source, []byte(seed), 0600) != nil {
				t.Fatal("cannot seed settings")
			}
			data, changed, err := insertProjectTrust([]byte(seed), cfg.Project)
			if err != nil || !changed {
				t.Fatal("cannot prepare accepted trust answer")
			}
			write, err := stageTrustWrite(cfg.Home, data, sha256.Sum256([]byte(seed)))
			if err != nil {
				t.Fatal("cannot stage settings")
			}
			defer write.close()
			staged := filepath.Join(cfg.Home, write.temp)
			// Interleave another writer after staging, while all decisions still refer to the old
			// source. Each publication must reject without replacing the competing writer's state.
			switch change {
			case "source bytes":
				err = os.WriteFile(source, []byte(`{"numStartups":4}`), 0600)
			case "source identity":
				err = os.Rename(source, source+".old")
				if err == nil {
					err = os.WriteFile(source, []byte(seed), 0600)
				}
			case "source absent":
				err = os.Remove(source)
			case "source link":
				err = os.Rename(source, source+".old")
				if err == nil {
					err = os.Symlink(source+".old", source)
				}
			case "source mode":
				err = os.Chmod(source, 0666)
			case "source readable mode":
				err = os.Chmod(source, 0640)
			case "staged bytes":
				err = os.WriteFile(staged, []byte(`{"unrelated":true}`), 0600)
			case "staged identity":
				err = os.Rename(staged, staged+".old")
				if err == nil {
					err = os.WriteFile(staged, data, 0600)
				}
			case "staged readable mode":
				err = os.Chmod(staged, 0644)
			case "home replaced":
				err = os.Rename(cfg.Home, cfg.Home+"-old")
				if err == nil {
					err = os.Mkdir(cfg.Home, 0700)
				}
				if err == nil {
					err = os.WriteFile(source, []byte(seed), 0600)
				}
			}
			if err != nil {
				t.Fatal("cannot interleave competing change")
			}
			before, beforeErr := os.ReadFile(source)
			written, err := write.publish()
			after, afterErr := os.ReadFile(source)
			if err != nil || written || string(after) != string(before) || (beforeErr == nil) != (afterErr == nil) {
				t.Fatal("publication did not preserve competing state")
			}
			if _, err := write.root.Lstat(trustLock); !os.IsNotExist(err) {
				t.Fatal("publication left its lock")
			}
			if change == "staged identity" {
				write.close()
				if got, err := os.ReadFile(staged); err != nil || string(got) != string(data) {
					t.Fatal("cleanup removed the replacement of its staged file")
				}
			}
		})
	}
}

func TestConcurrentTrustPublicationsPreserveTheWinningSource(t *testing.T) {
	cfg := trustConfig(t)
	source := filepath.Join(cfg.Home, trustFile)
	const seed = `{"numStartups":3}`
	if os.WriteFile(source, []byte(seed), 0600) != nil {
		t.Fatal("cannot seed settings")
	}
	const writers = 8
	writes := make([]*trustWrite, writers)
	values := make([][]byte, writers)
	for i := range writers {
		data, changed, err := insertProjectTrust([]byte(seed), cfg.Project+strings.Repeat("x", i))
		if err != nil || !changed {
			t.Fatal("cannot prepare accepted answer")
		}
		writes[i], err = stageTrustWrite(cfg.Home, data, sha256.Sum256([]byte(seed)))
		if err != nil {
			t.Fatal("cannot stage competing answer")
		}
		defer writes[i].close()
		values[i] = data
	}
	var joined sync.WaitGroup
	start := make(chan struct{})
	published := make([]bool, writers)
	errors := make([]error, writers)
	for i := range writers {
		joined.Go(func() {
			<-start
			published[i], errors[i] = writes[i].publish()
		})
	}
	close(start)
	joined.Wait()
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("cannot read published answer")
	}
	count := 0
	for i := range writers {
		if errors[i] != nil {
			t.Fatal("publication failed unexpectedly")
		}
		if published[i] {
			count++
			if string(after) != string(values[i]) {
				t.Fatal("a later publication overwrote the winning source")
			}
		}
	}
	if count != 1 {
		t.Fatalf("want one published answer, got %d", count)
	}
	if _, err := os.Lstat(filepath.Join(cfg.Home, trustLock)); !os.IsNotExist(err) {
		t.Fatal("competing publications left a lock")
	}
}

func TestTrustWriteKeepsSourcePermissionBits(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0640, 0644} {
		cfg := trustConfig(t)
		source := filepath.Join(cfg.Home, trustFile)
		const seed = `{"numStartups":3}`
		if os.WriteFile(source, []byte(seed), 0600) != nil || os.Chmod(source, mode) != nil {
			t.Fatal("cannot seed source permissions")
		}
		p, err := PrepareClient(cfg)
		if err != nil {
			t.Fatal("cannot prepare profile")
		}
		defer p.Close()
		acceptInPrivateProfile(t, p, cfg.Project)
		written, err := p.persistProjectTrust()
		after, statErr := os.Lstat(source)
		if err != nil || !written || statErr != nil {
			t.Fatal("could not publish accepted trust answer")
		}
		if after.Mode().Perm() != mode {
			t.Errorf("permission bits changed: want=%#o got=%#o", mode, after.Mode().Perm())
		}
	}
}
