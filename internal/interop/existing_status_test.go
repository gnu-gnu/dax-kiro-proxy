package interop_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaudeExistingStatuslinePrecedence(t *testing.T) {
	for _, scope := range []string{"user", "project", "local", "project_only", "project_event_only"} {
		t.Run(scope, func(t *testing.T) {
			observeClaudeStatusCase(t, nil, false, &existingStatusObservation{scope: scope})
		})
	}
}

// These status commands only print fixed labels and acknowledge their execution to the owned
// loopback observer. They never save client stdin or invoke a model. Native scope resolution
// belongs to the installed client, including the choice between simultaneous declarations.
type existingStatusObservation struct {
	mu              sync.Mutex
	scope, expected string
	counts          map[string]int
	previous        time.Time
	first           time.Time
	lateCalls       int
	interval        time.Duration
	sources         map[string][32]byte
}

func (o *existingStatusObservation) prepare(t *testing.T, user, project, endpoint string) {
	t.Helper()
	o.expected = o.scope
	if strings.HasPrefix(o.scope, "project_") {
		o.expected = "project"
	}
	o.counts, o.sources = make(map[string]int), make(map[string][32]byte)
	if os.Mkdir(filepath.Join(project, ".claude"), 0700) != nil {
		t.Fatal("cannot create owned project settings directory")
	}
	for _, scope := range []string{"user", "project", "local"} {
		if (scope == "user" && strings.HasPrefix(o.scope, "project_")) || (scope == "project" && o.scope == "user") || (scope == "local" && o.scope != "local") {
			continue
		}
		path := user
		if scope == "project" {
			path = filepath.Join(project, ".claude", "settings.json")
		}
		if scope == "local" {
			path = filepath.Join(project, ".claude", "settings.local.json")
		}
		command := "/usr/bin/curl -fsS --max-time 1 --noproxy '*' --output /dev/null " + probeShellQuote(endpoint+"/owned-status/"+scope) + "; printf '%s\\n' " + probeShellQuote("independent status "+scope)
		status := map[string]any{"type": "command", "command": command, "refreshInterval": 1}
		if o.scope == "project_event_only" {
			delete(status, "refreshInterval")
		}
		data, err := json.Marshal(map[string]any{"statusLine": status})
		if err != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot write owned status setting")
		}
		o.sources[path] = fileFingerprint(t, path)
	}
}

func (o *existingStatusObservation) handle(w http.ResponseWriter, r *http.Request, ready func()) bool {
	name, ok := strings.CutPrefix(r.URL.Path, "/owned-status/")
	if !ok {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.Method != "GET" || (name != "user" && name != "project" && name != "local") || o.counts[name] >= 32 {
		w.WriteHeader(400)
		return true
	}
	o.counts[name]++
	if name == o.expected {
		now := time.Now()
		if o.first.IsZero() {
			o.first = now
		}
		if o.scope == "project_event_only" {
			if now.Sub(o.first) > 2*time.Second {
				o.lateCalls++
			}
			ready()
		}
		if !o.previous.IsZero() && now.Sub(o.previous) >= 700*time.Millisecond && now.Sub(o.previous) <= 2*time.Second {
			o.interval = now.Sub(o.previous)
			ready()
		}
		o.previous = now
	}
	w.WriteHeader(200)
	return true
}

func (o *existingStatusObservation) check(t *testing.T, screen string, productCalls int) (int, time.Duration, bool) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	visible := strings.Contains(screen, "independent status "+o.expected)
	if o.scope == "project_event_only" {
		o.interval = time.Since(o.first)
		t.Logf("event_only_observation_ms=%d, late_calls=%d", o.interval.Milliseconds(), o.lateCalls)
		if o.first.IsZero() || o.interval < 6*time.Second || o.lateCalls != 0 {
			t.Error("product refresh altered an event-only status command")
		}
	}
	for path, before := range o.sources {
		if fileFingerprint(t, path) != before {
			t.Error("source status settings changed")
		}
	}
	for name, count := range o.counts {
		if name != o.expected && count != 0 {
			t.Error("lower-priority status command executed")
		}
	}
	t.Logf("expected_status=%s, owned_status_calls=%v, owned_refresh_ms=%d, owned_status_visible=%v, product_status_calls=%d", o.expected, o.counts, o.interval.Milliseconds(), visible, productCalls)
	if productCalls != 0 || strings.Contains(screen, "kiro last status-fixture") {
		t.Error("product status displaced an existing client status")
	}
	return o.counts[o.expected], o.interval, visible
}
