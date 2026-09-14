package launcher

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
)

func searchShapeNotification(update string) acp.Notification {
	return acp.Notification{Method: "session/update", Params: json.RawMessage(`{"sessionId":"owned-shape-fixture","update":` + update + `}`)}
}

func newSearchShapeFixture() *searchShapePeer {
	p := &searchShapePeer{shapes: make(map[string]bool)}
	p.bindSession(json.RawMessage(`{"sessionId":"owned-shape-fixture"}`))
	return p
}

func TestSearchShapeObservationDescendsWithoutRetainingPrivateFields(t *testing.T) {
	p := newSearchShapeFixture()
	p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call","toolCallId":"private-call-id","title":"private-display","rawInput":{"query":"private-query"}}`))
	result := searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"private-call-id","status":"completed","rawOutput":{"private-container":{"results":[{"title":"private-title","url":"https://example.org/private-result"}]},"url|uri":"private-value","private-encoded":"{\"results\":[{\"title\":\"private-encoded-title\",\"url\":\"https://example.org/private-encoded\"}]}"}}`)
	p.observe(result)
	p.observe(result)
	for _, shape := range []string{"rawOutput._.results[].title:string", "rawOutput._.results[].url:string", "rawOutput._:encoded_json", "rawOutput._.json.results[].url:string"} {
		if !p.shapes[shape] {
			t.Fatalf("missing fixed shape %s", shape)
		}
	}
	for key := range p.shapes {
		if strings.Contains(key, "private") || strings.Contains(key, "example.org") || strings.Contains(key, "url|uri") {
			t.Fatal("diagnostic retained an arbitrary name or value")
		}
	}
	stats := p.toolStats()
	if stats.calls != 1 || stats.completed != 1 || stats.failed != 0 || stats.rawOnly != 1 {
		t.Fatal("repeated updates changed correlated diagnostic counts")
	}
}

func TestSearchShapeObservationSeparatesToolAndAnswerContent(t *testing.T) {
	p := newSearchShapeFixture()
	p.observe(searchShapeNotification(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"https://example.org/answer"}}`))
	p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call","toolCallId":"owned-call","title":"Searching","status":"pending"}`))
	p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"owned-call","status":"completed","rawOutput":{"result":"synthetic text with https://example.org/result"}}`))
	if p.shapes["content:object"] || !p.shapes["rawOutput.result:contains_http"] || p.toolStats().rawOnly != 1 {
		t.Fatal("answer prose obscured the tool result carrier")
	}
}

func TestSearchShapeObservationBoundsDecodedTraversal(t *testing.T) {
	p := newSearchShapeFixture()
	value := any(map[string]string{"title": "synthetic"})
	for range 40 {
		value = map[string]any{"private-container": value}
	}
	raw, _ := json.Marshal(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "owned-call", "title": "Searching", "rawOutput": value})
	for range 4200 {
		p.observe(searchShapeNotification(string(raw)))
	}
	if p.updates != 4096 || p.nodes > 8192 || len(p.shapes) > 256 || !p.truncated {
		t.Fatal("diagnostic traversal exceeded its finite bounds")
	}
	for key := range p.shapes {
		if strings.Contains(key, "private-container") {
			t.Fatal("opaque container name escaped sanitization")
		}
	}
}

func TestSearchShapeObservationRequiresOwnedInitialCall(t *testing.T) {
	p := newSearchShapeFixture()
	p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call","toolCallId":"owned-call","title":"Searching"}`))
	completed := searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"owned-call","status":"completed","rawOutput":{"result":"synthetic"}}`)
	foreign := completed
	foreign.Params = json.RawMessage(strings.Replace(string(completed.Params), "owned-shape-fixture", "foreign-fixture", 1))
	p.observe(foreign)
	foreign = completed
	foreign.SessionID = "foreign-fixture"
	p.observe(foreign)
	orphan := completed
	orphan.Params = json.RawMessage(strings.Replace(string(completed.Params), "owned-call", "unknown-call", 1))
	p.observe(orphan)
	if s := p.toolStats(); s.calls != 1 || s.completed != 0 || s.rawOnly != 0 || p.unowned != 2 || p.orphans != 1 {
		t.Fatal("foreign or orphan updates were counted as owned completions")
	}
	p.observe(completed)
	if s := p.toolStats(); s.calls != 1 || s.completed != 1 || s.rawOnly != 1 {
		t.Fatal("owned completion failed to correlate")
	}
	p = &searchShapePeer{shapes: make(map[string]bool)}
	p.observe(completed)
	if len(p.shapes) != 0 || p.unowned != 1 {
		t.Fatal("observer accepted updates before binding session/new")
	}
}

func TestSearchShapeDenialRequiresCorrelatedOutputWitness(t *testing.T) {
	for _, mode := range []string{"generic-failure", "answer-token", "input-token", "foreign-output", "owned-output"} {
		t.Run(mode, func(t *testing.T) {
			p := newSearchShapeFixture()
			p.denialToken = "private-denial-witness"
			p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call","toolCallId":"owned-call","title":"Searching"}`))
			switch mode {
			case "answer-token":
				p.observe(searchShapeNotification(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"private-denial-witness"}}`))
			case "input-token":
				p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"owned-call","rawInput":{"query":"private-denial-witness"}}`))
			case "foreign-output":
				n := searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"owned-call","status":"failed","rawOutput":{"error":"private-denial-witness"}}`)
				n.SessionID = "foreign-fixture"
				p.observe(n)
			case "owned-output":
				p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"owned-call","rawOutput":{"error":"private-denial-witness"}}`))
			}
			p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"owned-call","status":"failed"}`))
			s := p.toolStats()
			if s.failed != 1 || (s.denied == 1) != (mode == "owned-output") {
				t.Fatal("unproven hook denial counted")
			}
			for key := range p.shapes {
				if strings.Contains(key, p.denialToken) {
					t.Fatal("private witness leaked")
				}
			}
		})
	}
}

func TestSearchShapeOrphanDiagnosticsStaySeparate(t *testing.T) {
	for _, mode := range []string{"failed-output", "completed-output", "input-only", "foreign-owner", "orphan-then-initial"} {
		t.Run(mode, func(t *testing.T) {
			p := newSearchShapeFixture()
			p.denialToken = "private-orphan-witness"
			update := `{"sessionUpdate":"tool_call_update","toolCallId":"private-call","status":"failed","rawOutput":{"private-container":{"error":"private-orphan-witness"}},"rawInput":{"query":"private-query"}}`
			if mode == "completed-output" {
				update = strings.Replace(update, `"failed"`, `"completed"`, 1)
			}
			if mode == "input-only" {
				update = `{"sessionUpdate":"tool_call_update","toolCallId":"private-call","status":"failed","rawInput":{"query":"private-orphan-witness"}}`
			}
			n := searchShapeNotification(update)
			if mode == "foreign-owner" {
				n.SessionID = "foreign-fixture"
			}
			p.observe(n)
			if mode == "orphan-then-initial" {
				p.observe(searchShapeNotification(`{"sessionUpdate":"tool_call","toolCallId":"private-call","title":"Searching","status":"failed"}`))
			}
			wantWitness := mode == "failed-output" || mode == "orphan-then-initial"
			if (p.orphanDenialUpdates == 1) != wantWitness || p.toolStats().denied != 0 || p.toolStats().completed != 0 {
				t.Fatal("orphan witness promoted into a correlated result")
			}
			if mode == "foreign-owner" {
				if p.orphans != 0 || p.unowned != 1 || len(p.shapes) != 0 {
					t.Fatal("foreign update entered orphan diagnostics")
				}
			} else if p.orphans != 1 || !p.shapes["orphan.rawInput.query:string"] || mode != "input-only" && !p.shapes["orphan.rawOutput._.error:string"] {
				t.Fatal("missing bounded orphan shape")
			}
			for key := range p.shapes {
				if strings.Contains(key, "private") {
					t.Fatal("orphan diagnostic retained private data")
				}
			}
		})
	}
}

func TestSearchShapeOrphanDiagnosticsShareTraversalBounds(t *testing.T) {
	p := newSearchShapeFixture()
	p.denialToken = "private-witness"
	update := searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"private-call","status":"failed","rawOutput":{"error":"private-witness"}}`)
	for range 4200 {
		p.observe(update)
	}
	if p.updates != 4096 || p.orphans != 4096 || p.orphanDenialUpdates != 4096 || p.nodes > 8192 || len(p.shapes) > 256 || !p.truncated || p.toolStats().calls != 0 {
		t.Fatal("orphan diagnostics exceeded bounds or created a correlated call")
	}
}

func TestSearchShapeStandaloneDenialCountsCallsNotUpdates(t *testing.T) {
	p := newSearchShapeFixture()
	p.denialToken = "private-witness"
	n := searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"private-call","status":"failed","content":[{"type":"content","content":{"type":"text","text":"private-witness"}}]}`)
	p.observe(n)
	p.observe(n)
	if s := observedSearchStats(p.orphanCalls, false); s.calls != 1 || s.failed != 1 || s.denied != 1 || p.orphanDenialUpdates != 2 || !p.denialCallVerified() || p.toolStats().calls != 0 {
		t.Fatal("duplicate denial updates did not retain one independent call")
	}
	// Generic failure, input witness, extra call, a foreign owner, truncation or later initial
	// notification cannot independently qualify as the one verified standalone denial.
	for _, mode := range []string{"generic", "input", "extra", "foreign", "truncated", "later-initial"} {
		q := newSearchShapeFixture()
		q.denialToken = p.denialToken
		candidate := n
		switch mode {
		case "generic":
			candidate.Params = json.RawMessage(strings.Replace(string(n.Params), "private-witness", "generic failure", 1))
		case "input":
			candidate = searchShapeNotification(`{"sessionUpdate":"tool_call_update","toolCallId":"private-call","status":"failed","rawInput":{"query":"private-witness"}}`)
		}
		q.observe(candidate)
		switch mode {
		case "extra":
			candidate.Params = json.RawMessage(strings.Replace(string(n.Params), "private-call", "second-call", 1))
			q.observe(candidate)
		case "foreign":
			candidate.SessionID = "foreign"
			q.observe(candidate)
		case "truncated":
			q.truncated = true
		case "later-initial":
			q.observe(searchShapeNotification(`{"sessionUpdate":"tool_call","toolCallId":"private-call","status":"failed"}`))
		}
		if q.denialCallVerified() {
			t.Fatal("unproven or ambiguous standalone denial accepted")
		}
	}
}

func TestSearchHookWitnessPreservesShellExitAndDenial(t *testing.T) {
	runner, err := childproc.New(childproc.Config{Timeout: 2 * time.Second, MaxProcesses: 1, MaxOutputBytes: 4096})
	if err != nil {
		t.Fatal("bounded witness runner")
	}
	defer runner.Close()
	for _, tc := range []struct {
		command string
		code    int
	}{{"exit 0", 0}, {"exit 2", 2}} {
		dir := t.TempDir()
		agents := filepath.Join(dir, ".kiro", "agents")
		if os.MkdirAll(agents, 0700) != nil {
			t.Fatal("owned witness directory")
		}
		path := filepath.Join(agents, "dax-web-search.json")
		raw, _ := json.Marshal(map[string]any{"hooks": map[string]any{"preToolUse": []any{map[string]string{"command": tc.command}}}})
		if os.WriteFile(path, raw, 0600) != nil || markSearchHookInvocation(dir, "owned-fixture-denial") != nil {
			t.Fatal("witness preparation")
		}
		raw, err := os.ReadFile(path)
		var agent struct {
			Hooks map[string][]struct{ Command string }
		}
		if err != nil || json.Unmarshal(raw, &agent) != nil {
			t.Fatal("prepared witness")
		}
		result, runErr := runner.Run(t.Context(), childproc.Command{Executable: "/bin/sh", Directory: dir, Environment: []string{"PATH=/usr/bin:/bin"}, Args: []string{"-c", `exec /bin/sh -c "$1" 2>&1`, "owned-witness", agent.Hooks["preToolUse"][0].Command}})
		want := ""
		if tc.code == 2 {
			want = "owned-fixture-denial\n"
		}
		marker, markerErr := os.Lstat(filepath.Join(dir, "hook-invoked"))
		if result.ExitCode != tc.code || tc.code == 0 && runErr != nil || tc.code == 2 && !errors.Is(runErr, childproc.ErrExit) || string(result.Stdout) != want || markerErr != nil || !marker.Mode().IsRegular() || marker.Size() != 0 || runner.Active() != 0 || !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
			t.Fatal("witness changed exit or omitted denial")
		}
	}
}
