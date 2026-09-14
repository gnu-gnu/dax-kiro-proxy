package launcher

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/websearch"
)

func TestKiroSearchProfileReadOnly(t *testing.T)        { observeKiroSearch(t, "readonly") }
func TestKiroSearchBootstrapHooksReadOnly(t *testing.T) { observeKiroSearch(t, "bootstrap") }
func TestKiroLiveSearchConversion(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("requires explicit per-run model credit approval")
	}
	observeKiroSearch(t, "search")
}
func TestKiroLiveSearchDeniedObservation(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("requires explicit approval for one denied-search observation")
	}
	// Retain the production conversion and independent hook-witness checks.
	observeKiroSearch(t, "denied")
}
func TestKiroLiveSearchConversionAndBudgetGate(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("requires explicit approval for at most two model prompts")
	}
	if !t.Run("allowed", func(t *testing.T) { observeKiroSearch(t, "search") }) {
		return
	}
	t.Run("exhausted", func(t *testing.T) { observeKiroSearch(t, "denied") })
}
func observeKiroSearch(t *testing.T, mode string) {
	live, bootstrap, denied := mode == "search" || mode == "denied", mode == "bootstrap", mode == "denied"
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("explicit installed Kiro path required")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("absolute Kiro path required")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-search-observation-")
	if err != nil {
		t.Fatal("owned root failed")
	}
	removeRoot := true
	defer func() {
		if removeRoot {
			_ = os.RemoveAll(root)
		}
	}()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("home unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute+30*time.Second)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10, MaxProcesses: 1})
	if err != nil {
		t.Fatal("bounded runner failed")
	}
	defer runner.Close()
	info := KiroInfo{Executable: executable, Helper: filepath.Join(filepath.Dir(executable), "kiro-cli-chat")}
	for _, binary := range []string{info.Executable, info.Helper} {
		result, err := runner.Run(ctx, childproc.Command{Executable: binary, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, Args: []string{"--version"}})
		version, valid := KiroVersionFromOutput(filepath.Base(binary), result.Stdout)
		if err != nil || !valid || info.Version != "" && version != info.Version {
			t.Fatal("search observation requires matching admitted main/helper")
		}
		info.Version = version
	}
	t.Logf("kiro_version=%s measured_version=%t", info.Version, info.Version == SupportedKiroVersion)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("source directory unavailable")
	}
	proxy := filepath.Join(root, "proxy")
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "CGO_ENABLED=0", "GOCACHE=" + os.Getenv("GOCACHE"), "GOMODCACHE=" + os.Getenv("GOMODCACHE")}
	if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", proxy, "../../cmd/dax-kiro-proxy"}}); err != nil {
		t.Fatal("candidate helper build failed")
	}
	var observer *searchShapePeer
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("cannot prepare private denial witness")
	}
	denialToken := "owned-search-hook-denial-" + hex.EncodeToString(nonce[:])
	open, err := newKiroSearchOpener(KiroSearchConfig{Installation: info, Home: home, RuntimeParent: root, ProxyExecutable: proxy}, func(ctx context.Context, cfg acp.Config) (websearch.Peer, error) {
		if bootstrap {
			if err := prepareSearchBootstrapProbe(cfg.Directory, proxy); err != nil {
				return nil, err
			}
		}
		if live {
			if err := markSearchHookInvocation(cfg.Directory, denialToken); err != nil {
				return nil, err
			}
		}
		peer, err := acp.Start(ctx, cfg)
		if err != nil {
			return nil, err
		}
		observer = &searchShapePeer{Client: peer, shapes: make(map[string]bool), directory: cfg.Directory, denialToken: denialToken}
		return observer, nil
	})
	if err != nil {
		t.Fatal("search preparation unavailable")
	}
	cat, _ := catalog.New([]catalog.Backend{{ID: "auto"}}, "auto")
	model, _ := cat.ClientID("auto")
	body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 512, "tools": []any{map[string]any{"type": "web_search_20250305", "name": "web_search", "max_uses": 1}}, "messages": []any{map[string]string{"role": "user", "content": "Use web_search exactly once to find the official Agent Client Protocol documentation website. Return its title and URL. Do not use any other tool."}}})
	r, err := anthropic.DecodeRequest(body)
	if err != nil {
		t.Fatal("invalid owned search request")
	}
	session, prepareErr := open(ctx, r, anthropic.SearchSpec{MaxUses: 1})
	if observer != nil {
		defer func() {
			if observer.Close() != nil {
				removeRoot = false
			}
		}()
	}
	if prepareErr != nil {
		if errors.Is(prepareErr, acp.ErrCleanup) {
			removeRoot = false
		}
		t.Logf("search_prepare=false protocol_failure=%t authentication=%t timeout=%t cleanup_failure=%t", errors.Is(prepareErr, acp.ErrProtocol), errors.Is(prepareErr, acp.ErrAuthentication), errors.Is(prepareErr, context.DeadlineExceeded) || errors.Is(prepareErr, acp.ErrTimeout), errors.Is(prepareErr, acp.ErrCleanup))
		if observer != nil {
			observer.report(t)
		}
		t.Fatal("search preparation failed")
	}
	defer func() {
		if session.Close() != nil {
			removeRoot = false
		}
	}()
	if denied {
		if !websearch.TakeBudget(filepath.Join(filepath.Dir(observer.directory), "budget"), 1) {
			t.Fatal("cannot prepare exhausted-budget control")
		}
	}
	searches, links, terminals, searchErrors := 0, 0, 0, 0
	var runErr error
	if live {
		runErr = session.Run(ctx, func(e inference.Event) bool {
			if e.Kind == inference.Search {
				searches += len(e.Searches)
				for _, x := range e.Searches {
					links += len(x.Results)
					if x.ErrorCode != "" {
						searchErrors++
					}
				}
			}
			if e.Kind == inference.End {
				terminals++
			}
			return true
		})
	}
	// Join the process before reading its hook ledger, but retain the owned profile until
	// after that read. A conversion failure must not erase the execution evidence.
	peerCloseErr := observer.Close()
	budgetCount, budgetErr := 0, peerCloseErr
	if peerCloseErr == nil {
		budgetCount, budgetErr = websearch.BudgetCount(filepath.Join(filepath.Dir(observer.directory), "budget"), 1)
	}
	if bootstrap {
		present := func(name string) bool {
			info, err := os.Lstat(filepath.Join(observer.directory, name))
			return err == nil && info.Mode().IsRegular() && info.Size() == 0
		}
		count, err := websearch.BudgetCount(filepath.Join(observer.directory, "bootstrap-budget"), 1)
		t.Logf("bootstrap_probe=true embedded_hook_marker=%t file_hook_marker=%t bootstrap_budget_slots=%d bootstrap_budget_error=%t model_prompts=0", present("embedded-bootstrap"), present("file-bootstrap"), count, err != nil)
	}
	hookMarker, markerErr := os.Lstat(filepath.Join(observer.directory, "hook-invoked"))
	hookInvoked := markerErr == nil && hookMarker.Mode().IsRegular() && hookMarker.Size() == 0
	stats := observer.toolStats()
	closeErr := errors.Join(peerCloseErr, session.Close())
	if closeErr != nil {
		removeRoot = false
	}
	joined := observer != nil && errors.Is(syscall.Kill(-observer.PID(), 0), syscall.ESRCH)
	removed := false
	if observer != nil {
		_, err := os.Lstat(observer.directory)
		removed = os.IsNotExist(err)
		observer.report(t)
	}
	t.Logf("live=%t exhausted_control=%t hook_invoked=%t converted_searches=%d search_errors=%d links=%d terminal=%d budget_slots=%d budget_error=%t run_error=%t protocol_failure=%t joined=%t profile_removed=%t cleanup_failure=%t", live, denied, hookInvoked, searches, searchErrors, links, terminals, budgetCount, budgetErr != nil, runErr != nil, errors.Is(runErr, acp.ErrProtocol), joined, removed, closeErr != nil)
	valid := !live || searches == 1 && terminals == 1 && budgetCount == 1 && hookInvoked && observer.unowned == 0
	if live && denied {
		valid = valid && searchErrors == 1 && links == 0 && observer.denialCallVerified()
	}
	if live && !denied {
		valid = valid && searchErrors == 0 && links > 0 && stats.calls == 1 && stats.failed == 0 && stats.completed == 1 && stats.denied == 0 && observer.orphans == 0
	}
	if runErr != nil || closeErr != nil || budgetErr != nil || !joined || !removed || !live && budgetCount != 0 || !valid {
		t.Fatal("finite search control failed")
	}
}

func markSearchHookInvocation(directory, denialToken string) error {
	path := filepath.Join(directory, ".kiro", "agents", "dax-web-search.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var agent map[string]json.RawMessage
	if json.Unmarshal(raw, &agent) != nil {
		return ErrConfig
	}
	var hooks map[string][]map[string]any
	if json.Unmarshal(agent["hooks"], &hooks) != nil || len(hooks["preToolUse"]) != 1 {
		return ErrConfig
	}
	command, ok := hooks["preToolUse"][0]["command"].(string)
	if !ok {
		return ErrConfig
	}
	marker := "'" + strings.ReplaceAll(filepath.Join(directory, "hook-invoked"), "'", "'\"'\"'") + "'"
	// A subshell preserves the helper wrapper's exit without exiting this witness wrapper.
	// The token is generated privately by this test and never appears in its model prompt.
	witness := "'" + strings.ReplaceAll(denialToken, "'", "'\"'\"'") + "'"
	hooks["preToolUse"][0]["command"] = "if /usr/bin/touch " + marker + "; then if ( " + command + " ); then exit 0; else hook_code=$?; if [ \"$hook_code\" -eq 2 ]; then printf '%s\\n' " + witness + " >&2; fi; exit 2; fi; else exit 2; fi"
	agent["hooks"], err = json.Marshal(hooks)
	if err != nil {
		return err
	}
	raw, err = json.Marshal(agent)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}

// Both commands only create distinct empty markers inside the owned scratch workspace.
// The probe distinguishes documented hook formats without submitting a model prompt.
func prepareSearchBootstrapProbe(directory, proxy string) error {
	command := func(marker string) string {
		return "/usr/bin/touch '" + strings.ReplaceAll(filepath.Join(directory, marker), "'", "'\"'\"'") + "'"
	}
	path := filepath.Join(directory, ".kiro", "agents", "dax-web-search.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var agent map[string]json.RawMessage
	if json.Unmarshal(raw, &agent) != nil {
		return ErrConfig
	}
	var hooks map[string]any
	if json.Unmarshal(agent["hooks"], &hooks) != nil {
		return ErrConfig
	}
	budget := filepath.Join(directory, "bootstrap-budget")
	if os.Mkdir(budget, 0700) != nil {
		return ErrConfig
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	budgetCommand := "if " + quote(proxy) + " web-search-budget " + quote(budget) + " 1; then exit 0; else exit 2; fi"
	hooks["agentSpawn"] = []any{map[string]string{"matcher": ".*", "command": command("embedded-bootstrap") + " && " + budgetCommand}}
	agent["hooks"], err = json.Marshal(hooks)
	if err != nil {
		return err
	}
	raw, err = json.Marshal(agent)
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		return ErrConfig
	}
	dir := filepath.Join(directory, ".kiro", "hooks")
	if os.Mkdir(dir, 0700) != nil {
		return ErrConfig
	}
	raw, err = json.Marshal(map[string]any{"version": "v1", "hooks": []any{map[string]any{"name": "Owned search bootstrap observation", "trigger": "SessionStart", "action": map[string]string{"type": "command", "command": command("file-bootstrap")}}}})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "owned-bootstrap.json"), raw, 0600)
}

type searchShapePeer struct {
	*acp.Client
	shapes              map[string]bool
	directory           string
	updates             int
	nodes, decodedBytes int
	truncated           bool
	calls               map[[32]byte]searchObservedCall
	orphanCalls         map[[32]byte]searchObservedCall
	session             [32]byte
	bound               bool
	unowned, orphans    int
	orphanDenialUpdates int
	denialToken         string
}

type searchObservedCall struct {
	initial, content, rawOutput bool
	status                      string
	denialSeen                  bool
}
type searchObservedStats struct{ calls, completed, failed, rawOnly, denied int }

func (p *searchShapePeer) toolStats() (s searchObservedStats) {
	return observedSearchStats(p.calls, true)
}
func observedSearchStats(calls map[[32]byte]searchObservedCall, requireInitial bool) (s searchObservedStats) {
	for _, call := range calls {
		if requireInitial && !call.initial {
			continue
		}
		s.calls++
		switch call.status {
		case "completed":
			s.completed++
			if call.rawOutput && !call.content {
				s.rawOnly++
			}
		case "failed":
			s.failed++
			if call.denialSeen {
				s.denied++
			}
		}
	}
	return
}

func (p *searchShapePeer) denialCallVerified() bool {
	s := p.toolStats()
	orphan := observedSearchStats(p.orphanCalls, false)
	if p.unowned != 0 || p.truncated {
		return false
	}
	// Either one initially announced failure, or the separately observed self-contained
	// failed-update path. Conversion must also succeed; these counters alone never admit it.
	return s.calls == 1 && s.failed == 1 && s.denied == 1 && p.orphans == 0 ||
		s.calls == 0 && orphan.calls == 1 && orphan.failed == 1 && orphan.denied == 1
}

func (p *searchShapePeer) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := p.Client.Call(ctx, method, params)
	if method == "session/new" && err == nil {
		p.bindSession(raw)
	}
	return raw, err
}
func (p *searchShapePeer) bindSession(raw json.RawMessage) {
	fields, err := ndjson.Object(raw)
	var id string
	if err == nil && json.Unmarshal(fields["sessionId"], &id) == nil && id != "" {
		p.session, p.bound = sha256.Sum256([]byte(id)), true
	}
}

func (p *searchShapePeer) TryNext() (acp.Notification, bool, error) {
	n, ok, err := p.Client.TryNext()
	if ok {
		p.observe(n)
	}
	return n, ok, err
}
func (p *searchShapePeer) Next(ctx context.Context) (acp.Notification, error) {
	n, err := p.Client.Next(ctx)
	if err == nil {
		p.observe(n)
	}
	return n, err
}
func (p *searchShapePeer) observe(n acp.Notification) {
	if p.updates >= 4096 {
		p.truncated = true
		return
	}
	p.updates++
	if len(n.Params) > 1<<20 {
		p.truncated = true
		return
	}
	if n.Method != "session/update" {
		return
	}
	fields, err := ndjson.Object(n.Params)
	if err != nil {
		return
	}
	var owner string
	if !p.bound || json.Unmarshal(fields["sessionId"], &owner) != nil || owner == "" || sha256.Sum256([]byte(owner)) != p.session || n.SessionID != "" && sha256.Sum256([]byte(n.SessionID)) != p.session {
		p.unowned++
		return
	}
	update, err := ndjson.Object(fields["update"])
	if err != nil {
		return
	}
	var kind, id, status string
	if json.Unmarshal(update["sessionUpdate"], &kind) != nil || kind != "tool_call" && kind != "tool_call_update" {
		return
	}
	if json.Unmarshal(update["toolCallId"], &id) == nil && id != "" && len(id) <= 256 {
		if p.calls == nil {
			p.calls = make(map[[32]byte]searchObservedCall)
		}
		digest := sha256.Sum256([]byte(id))
		call, present := p.calls[digest]
		if !present && kind != "tool_call" {
			p.orphans++
			_ = json.Unmarshal(update["status"], &status)
			witness := p.hasDenialWitness(update)
			if status == "failed" && witness {
				p.orphanDenialUpdates++
			}
			if p.orphanCalls == nil {
				p.orphanCalls = make(map[[32]byte]searchObservedCall)
			}
			prior, exists := p.orphanCalls[digest]
			if exists || len(p.orphanCalls) < 16 {
				prior.status = status
				prior.denialSeen = prior.denialSeen || witness
				p.orphanCalls[digest] = prior
			} else {
				p.truncated = true
			}
			p.observeToolShape("orphan.", update)
			return
		}
		if present || len(p.calls) < 16 {
			call.initial = call.initial || kind == "tool_call"
			call.content = call.content || update["content"] != nil
			call.rawOutput = call.rawOutput || update["rawOutput"] != nil
			call.denialSeen = call.denialSeen || p.hasDenialWitness(update)
			if json.Unmarshal(update["status"], &status) == nil {
				switch status {
				case "pending", "in_progress", "completed", "failed":
					call.status = status
				}
			}
			p.calls[digest] = call
		} else {
			p.truncated = true
		}
	}
	p.observeToolShape("", update)
}
func (p *searchShapePeer) hasDenialWitness(update map[string]json.RawMessage) bool {
	if p.denialToken == "" {
		return false
	}
	return strings.Contains(string(update["rawOutput"]), p.denialToken) || strings.Contains(string(update["content"]), p.denialToken)
}
func (p *searchShapePeer) observeToolShape(prefix string, update map[string]json.RawMessage) {
	for _, key := range []string{"rawInput", "rawOutput", "content", "_meta"} {
		if raw, ok := update[key]; ok {
			p.shape(prefix+key, raw, 0)
		}
	}
	for key, allowed := range map[string][]string{"title": {"web_search"}, "kind": {"search", "fetch", "other"}, "status": {"pending", "in_progress", "completed", "failed"}} {
		var value string
		if json.Unmarshal(update[key], &value) != nil {
			continue
		}
		known := false
		for _, item := range allowed {
			if value == item {
				p.mark(prefix + key + "=" + item)
				known = true
			}
		}
		if !known {
			p.mark(prefix + key + "=other_value")
		}
	}
}
func (p *searchShapePeer) shape(path string, raw json.RawMessage, depth int) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	p.walkShape(path, value, depth)
}
func (p *searchShapePeer) mark(shape string) {
	if p.shapes[shape] {
		return
	}
	if len(p.shapes) >= 256 {
		p.truncated = true
		return
	}
	p.shapes[shape] = true
}
func (p *searchShapePeer) walkShape(path string, value any, depth int) {
	if depth > 8 || p.nodes >= 8192 {
		p.truncated = true
		return
	}
	p.nodes++
	switch v := value.(type) {
	case map[string]any:
		p.mark(path + ":object")
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if p.nodes >= 8192 {
				p.truncated = true
				break
			}
			label := "_"
			switch key {
			case "results", "content", "json", "text", "type", "title", "url", "uri", "name", "snippet", "body", "query", "search_results", "searchResults", "result", "output", "metadata", "toolName", "tool_name", "data", "value", "response", "message", "error", "success", "status", "items", "sources", "links", "format", "mimeType", "kind", "isError", "is_error", "toolOutput", "tool_output":
				label = key
			}
			// Opaque names never enter retained diagnostics; still inspect their child shape.
			p.walkShape(path+"."+label, v[key], depth+1)
		}
	case []any:
		p.mark(path + ":array")
		if len(v) == 0 {
			p.mark(path + ":empty")
		}
		for i, child := range v {
			if i >= 8 || p.nodes >= 8192 {
				p.truncated = true
				break
			}
			p.walkShape(path+"[]", child, depth+1)
		}
	case string:
		p.mark(path + ":string")
		if strings.Contains(v, "https://") || strings.Contains(v, "http://") {
			p.mark(path + ":contains_http")
		}
		encoded := strings.TrimSpace(v)
		if !strings.HasPrefix(encoded, "{") && !strings.HasPrefix(encoded, "[") {
			return
		}
		if len(encoded) > 512<<10 || len(encoded) > 2<<20-p.decodedBytes {
			p.truncated = true
			return
		}
		p.decodedBytes += len(encoded)
		wrapped := append([]byte(`{"value":`), encoded...)
		wrapped = append(wrapped, '}')
		if _, err := ndjson.Object(wrapped); err != nil {
			return
		}
		var nested any
		if json.Unmarshal([]byte(encoded), &nested) == nil {
			p.mark(path + ":encoded_json")
			p.walkShape(path+".json", nested, depth+1)
		}
	case float64:
		p.mark(path + ":number")
	case bool:
		p.mark(path + ":boolean")
	case nil:
		p.mark(path + ":null")
	}
}
func (p *searchShapePeer) report(t *testing.T) {
	keys := make([]string, 0, len(p.shapes))
	for key := range p.shapes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	s := p.toolStats()
	orphan := observedSearchStats(p.orphanCalls, false)
	t.Logf("observed_updates=%d tool_calls=%d completed_calls=%d failed_calls=%d hook_denial_witnesses=%d completed_raw_only=%d unowned_updates=%d orphan_updates=%d orphan_denial_updates=%d orphan_calls=%d orphan_failed_calls=%d orphan_denial_calls=%d diagnostic_truncated=%t known_shapes=%v", p.updates, s.calls, s.completed, s.failed, s.denied, s.rawOnly, p.unowned, p.orphans, p.orphanDenialUpdates, orphan.calls, orphan.failed, orphan.denied, p.truncated, keys)
}
