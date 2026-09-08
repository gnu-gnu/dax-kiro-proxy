package session_test

import (
	"context"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/session"
	"dax-kiro-proxy/internal/sessionstore"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentLoadDiscardReplayAndInvalidateBeforePrompt(t *testing.T) {
	for _, mode := range []string{"pool-normal", "pool-load-null", "pool-load-fail", "pool-load-mismatch", "pool-no-load"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private")
			store, err := sessionstore.New(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg := managerConfig(t, mode)
			cfg.Persistence = store
			cfg.BackendVersion = "synthetic-1"
			cfg.Session.Process.Args = append(cfg.Session.Process.Args, path)
			m, err := session.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			r := mainRequest(t, "persisted-client")
			first, reply := managerTurn(t, m, r)
			other, err := session.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = other.Start(context.Background(), r); !errors.Is(err, session.ErrBusy) {
				t.Fatal("second manager claimed live persistent backend")
			}
			other.Close()
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			r.Messages = append(r.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: reply}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "new input after restart"}}})
			m, err = session.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			got, _ := managerTurn(t, m, r)
			if got.PID == first.PID {
				t.Fatal("restart did not launch independent process")
			}
			wantLoad := mode == "pool-normal" || mode == "pool-load-null"
			if got.Loaded != wantLoad {
				t.Fatal("load or single fresh recovery did not follow capability/result")
			}
			if wantLoad {
				if got.Session != first.Session || len(got.Prompt) != 1 || got.Prompt[0].Text != "new input after restart" {
					t.Fatal("loaded turn repeated committed history")
				}
			} else if len(got.Prompt) <= 1 {
				t.Fatal("failed load omitted the safe full context")
			}
		})
	}
}
func TestTitleAndFallbackIdentityNeverPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	store, err := sessionstore.New(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := managerConfig(t, "pool-normal")
	cfg.Persistence = store
	cfg.BackendVersion = "synthetic-1"
	m, err := session.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r := mainRequest(t, "")
	managerTurn(t, m, r)
	title := mainRequest(t, "explicit-title-client")
	title.System = []anthropic.Block{{Type: "text", Text: "Create a short title for this conversation."}}
	title.Extra["thinking"] = json.RawMessage(`{"type":"disabled"}`)
	title.Extra["output_config"] = json.RawMessage(`{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}}`)
	managerTurn(t, m, title)
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Fatal("unstable fallback identity persisted")
		}
	}
}
