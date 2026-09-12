//go:build darwin || linux

package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

type nativeToolLane struct {
	Identity, Seed, Directory string
	Requests, Issued, Results int
	Pending                   string
	IDs                       map[string]bool
}

type nativeToolLedger struct {
	Rounds int
	Lanes  [2]nativeToolLane
}

func nativeToolRounds(raw string) (int, error) {
	if raw == "" {
		return 8, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 8 || n > 128 {
		return 0, errors.New("DAX_INTEROP_TOOL_SOAK_ROUNDS must be between 8 and 128")
	}
	return n, nil
}

func nativeToolPath(lane nativeToolLane, round int) string {
	return filepath.Join(lane.Directory, fmt.Sprintf("read-%03d", round))
}

func nativeToolCanary(lane nativeToolLane, round int) string {
	return lane.Seed + "_VALUE_" + strconv.Itoa(round) + "_END"
}

// Caller holds the ledger lock. A rejected request leaves the outstanding call intact.
func (l *nativeToolLedger) request(r *anthropic.Request) (int, error) {
	if r == nil || r.Identity.Agent != "" || r.Identity.ParentAgent != "" {
		return 0, errors.New("owned native request identity")
	}
	lane := -1
	for i := range l.Lanes {
		if r.Identity.Session == l.Lanes[i].Identity {
			lane = i
		}
	}
	if lane < 0 {
		return 0, errors.New("foreign native session")
	}
	s := &l.Lanes[lane]
	if s.Requests >= l.Rounds+1 || s.Requests != s.Results+1 && s.Requests != 0 {
		return 0, errors.New("owned native request count")
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > 16<<20 || !strings.Contains(string(encoded), s.Seed) || strings.Contains(string(encoded), l.Lanes[1-lane].Seed) {
		return 0, errors.New("native conversation content crossed owners")
	}
	results, err := r.LatestToolResults()
	if err != nil {
		return 0, errors.New("owned native result shape")
	}
	if s.Requests == 0 {
		if len(results) != 0 || s.Pending != "" {
			return 0, errors.New("initial native request carried a result")
		}
	} else {
		if len(results) != 1 || s.Pending == "" || s.Results+1 != s.Issued || results[0].ID != s.Pending || results[0].IsError != (lane == 1) {
			return 0, errors.New("native result lost its exact pending owner or status")
		}
		var body strings.Builder
		for _, block := range results[0].Content {
			if block.Type != "text" || body.Len()+len(block.Text) > 64<<10 {
				return 0, errors.New("native result content bound")
			}
			body.WriteString(block.Text)
		}
		if strings.Contains(body.String(), l.Lanes[1-lane].Seed) ||
			lane == 0 && strings.Count(body.String(), nativeToolCanary(*s, s.Issued)) != 1 ||
			lane == 1 && (strings.Contains(body.String(), s.Seed) || !strings.Contains(body.String(), "independent native refusal")) {
			return 0, errors.New("native result content lost or crossed owners")
		}
		s.Results++
		s.Pending = ""
	}
	s.Requests++
	return lane, nil
}

func (l *nativeToolLedger) tools(lane int, calls []anthropic.ToolUse) (int, error) {
	s := &l.Lanes[lane]
	if len(calls) != 1 || calls[0].Name != "Read" || !calls[0].Valid() || s.Pending != "" || s.Issued != s.Results || s.Requests != s.Results+1 || s.Issued >= l.Rounds || s.IDs[calls[0].ID] || l.Lanes[1-lane].IDs[calls[0].ID] {
		return 0, errors.New("native tool call identity or admission")
	}
	var fields map[string]json.RawMessage
	var path string
	if json.Unmarshal(calls[0].Input, &fields) != nil || len(fields) != 1 || json.Unmarshal(fields["file_path"], &path) != nil || path != nativeToolPath(*s, s.Issued+1) {
		return 0, errors.New("native Read escaped its exact owned input")
	}
	s.Issued++
	s.Pending = calls[0].ID
	s.IDs[calls[0].ID] = true
	return s.Issued, nil
}

type nativeToolResources struct {
	FDs, Goroutines int
	Heap            uint64
}

func observeNativeToolResources() (nativeToolResources, error) {
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	path := "/dev/fd"
	if runtime.GOOS == "linux" {
		path = "/proc/self/fd"
	}
	dir, err := os.Open(path)
	if err != nil {
		return nativeToolResources{}, errors.New("owned descriptor observation unavailable")
	}
	entries, readErr := dir.ReadDir(4097)
	closeErr := dir.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) > 4096 {
		return nativeToolResources{}, errors.New("owned descriptor observation exceeded its bound")
	}
	return nativeToolResources{len(entries), runtime.NumGoroutine(), memory.HeapAlloc}, nil
}

func nativeToolResourcesWithin(base, now nativeToolResources) bool {
	return now.FDs <= base.FDs+4 && now.Goroutines <= base.Goroutines+16 && now.Heap <= base.Heap+(16<<20)
}

func nativeToolLedgerFixture() nativeToolLedger {
	return nativeToolLedger{Rounds: 8, Lanes: [2]nativeToolLane{
		{Identity: "owned-session-a", Seed: "OWNED_ALPHA", Directory: "/owned/a", IDs: map[string]bool{}},
		{Identity: "owned-session-b", Seed: "OWNED_BRAVO", Directory: "/owned/b", IDs: map[string]bool{}},
	}}
}

func nativeToolRequestFixture(l *nativeToolLedger, lane int, result *anthropic.ToolResult) *anthropic.Request {
	r := &anthropic.Request{Identity: anthropic.ClientIdentity{Session: l.Lanes[lane].Identity}, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: l.Lanes[lane].Seed}}}}}
	if result != nil {
		var content []map[string]string
		for _, block := range result.Content {
			content = append(content, map[string]string{"type": block.Type, "text": block.Text})
		}
		raw, _ := json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": result.ID, "is_error": result.IsError, "content": content})
		r.Messages = append(r.Messages, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "tool_result", Raw: raw}}})
	}
	return r
}

func TestNativeToolSoakLedgerRejectsCrossedAndRepeatedWork(t *testing.T) {
	for _, kind := range []string{"valid", "foreign-session", "foreign-call", "wrong-status", "wrong-text", "wrong-round", "foreign-context", "duplicate-result", "replayed-result", "wrong-path", "foreign-issued-id"} {
		t.Run(kind, func(t *testing.T) {
			l := nativeToolLedgerFixture()
			for lane := range 2 {
				if _, err := l.request(nativeToolRequestFixture(&l, lane, nil)); err != nil {
					t.Fatal("initial owned request rejected")
				}
				input, _ := json.Marshal(map[string]string{"file_path": nativeToolPath(l.Lanes[lane], 1)})
				if _, err := l.tools(lane, []anthropic.ToolUse{{ID: fmt.Sprintf("toolu_owned_%d", lane), Name: "Read", Input: input}}); err != nil {
					t.Fatal("initial owned tool rejected")
				}
			}
			result := anthropic.ToolResult{ID: l.Lanes[0].Pending, Content: []anthropic.Block{{Type: "text", Text: nativeToolCanary(l.Lanes[0], 1)}}}
			r := nativeToolRequestFixture(&l, 0, &result)
			switch kind {
			case "foreign-session":
				r.Identity.Session = l.Lanes[1].Identity
			case "foreign-call":
				result.ID = l.Lanes[1].Pending
				r = nativeToolRequestFixture(&l, 0, &result)
			case "wrong-status":
				result.IsError = true
				r = nativeToolRequestFixture(&l, 0, &result)
			case "wrong-text":
				result.Content[0].Text = "unrelated result"
				r = nativeToolRequestFixture(&l, 0, &result)
			case "wrong-round":
				result.Content[0].Text = nativeToolCanary(l.Lanes[0], 10)
				r = nativeToolRequestFixture(&l, 0, &result)
			case "foreign-context":
				r.System = []anthropic.Block{{Type: "text", Text: l.Lanes[1].Seed}}
			case "duplicate-result":
				r.Messages[1].Content = append(r.Messages[1].Content, r.Messages[1].Content[0])
			case "replayed-result", "wrong-path", "foreign-issued-id":
				if _, err := l.request(r); err != nil {
					t.Fatal("owned result control rejected")
				}
			}
			before := l.Lanes[0]
			var err error
			if kind == "wrong-path" || kind == "foreign-issued-id" {
				path, id := nativeToolPath(l.Lanes[0], 2), "toolu_new"
				if kind == "wrong-path" {
					path = nativeToolPath(l.Lanes[1], 2)
				} else {
					id = l.Lanes[1].Pending
				}
				input, _ := json.Marshal(map[string]string{"file_path": path})
				_, err = l.tools(0, []anthropic.ToolUse{{ID: id, Name: "Read", Input: input}})
			} else {
				_, err = l.request(r)
			}
			if kind == "valid" {
				if err != nil || l.Lanes[0].Results != 1 || l.Lanes[0].Pending != "" {
					t.Fatal("exact owned result did not settle")
				}
			} else if err == nil || l.Lanes[0].Pending != before.Pending || l.Lanes[0].Requests != before.Requests || l.Lanes[0].Results != before.Results || l.Lanes[0].Issued != before.Issued {
				t.Fatal("rejected native work consumed pending ownership")
			}
			if l.Lanes[1].Results != 0 || l.Lanes[1].Pending != "toolu_owned_1" {
				t.Fatal("rejected native work changed the sibling")
			}
		})
	}
}

func TestNativeToolSoakDenialCannotExposeReadContent(t *testing.T) {
	for _, text := range []string{"independent native refusal", "OWNED_BRAVO_VALUE_1", "OWNED_ALPHA_VALUE_1", "file unavailable"} {
		l := nativeToolLedgerFixture()
		if _, err := l.request(nativeToolRequestFixture(&l, 1, nil)); err != nil {
			t.Fatal("initial denial request rejected")
		}
		input, _ := json.Marshal(map[string]string{"file_path": nativeToolPath(l.Lanes[1], 1)})
		if _, err := l.tools(1, []anthropic.ToolUse{{ID: "toolu_denial", Name: "Read", Input: input}}); err != nil {
			t.Fatal("owned denial tool rejected")
		}
		result := anthropic.ToolResult{ID: "toolu_denial", IsError: true, Content: []anthropic.Block{{Type: "text", Text: text}}}
		_, err := l.request(nativeToolRequestFixture(&l, 1, &result))
		if (err == nil) != (text == "independent native refusal") {
			t.Fatal("native denial admitted private content or lacked the owned hook reason")
		}
	}
}

func TestNativeToolSoakResourceObserverDetectsRetention(t *testing.T) {
	for _, kind := range []string{"descriptor", "goroutine", "heap"} {
		t.Run(kind, func(t *testing.T) {
			base, err := observeNativeToolResources()
			if err != nil {
				t.Fatal("resource baseline unavailable")
			}
			var files []*os.File
			var memory []byte
			var workers sync.WaitGroup
			release := make(chan struct{})
			var once sync.Once
			cleanup := func() {
				once.Do(func() {
					close(release)
					workers.Wait()
					for _, file := range files {
						_ = file.Close()
					}
				})
			}
			defer cleanup()
			switch kind {
			case "descriptor":
				for range 8 {
					file, err := os.Open(os.DevNull)
					if err != nil {
						t.Fatal("owned descriptor allocation")
					}
					files = append(files, file)
				}
			case "goroutine":
				for range 24 {
					workers.Go(func() { <-release })
				}
			case "heap":
				memory = make([]byte, 24<<20)
				for i := 0; i < len(memory); i += 4096 {
					memory[i] = byte(i / 4096)
				}
			}
			now, err := observeNativeToolResources()
			runtime.KeepAlive(memory)
			if err != nil || nativeToolResourcesWithin(base, now) {
				t.Fatal("retained resources escaped observation")
			}
			cleanup()
			memory = nil
			settled, err := observeNativeToolResources()
			if err != nil || !nativeToolResourcesWithin(base, settled) {
				t.Fatal("released resources failed to settle")
			}
		})
	}
}

func TestNativeToolSoakRoundBounds(t *testing.T) {
	for _, value := range []string{"", "8", "128", "0", "7", "129", "invalid"} {
		n, err := nativeToolRounds(value)
		valid := value == "" || value == "8" || value == "128"
		if (err == nil) != valid || valid && (n < 8 || n > 128) {
			t.Fatal("native tool round bound changed")
		}
	}
}

func TestNativeToolSoakRechecksFirstOwnerAtPairedWait(t *testing.T) {
	for _, kind := range []string{"alive", "first-acp-lost", "first-relay-lost"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			var workers sync.WaitGroup
			defer func() { cancel(); workers.Wait() }()
			live := map[[2]int]bool{{101, 101}: true, {102, 102}: true, {201, 201}: true, {202, 202}: true, {301, 201}: true, {302, 202}: true}
			b := &nativeToolBackend{ledger: nativeToolLedgerFixture(), path: t.TempDir(), clients: [2]int{101, 102}, finalReady: make(chan struct{}), cancel: cancel}
			b.live = func(pid, group int) bool { return live[[2]int{pid, group}] }
			b.ledger.Rounds = 1
			b.rounds = []nativeToolRound{{Release: make(chan struct{})}}
			var calls [2][]anthropic.ToolUse
			for lane := range 2 {
				if _, err := b.ledger.request(nativeToolRequestFixture(&b.ledger, lane, nil)); err != nil {
					t.Fatal("owned overlap request")
				}
				w := nativeToolWitness{PID: 201 + lane, Peer: 301 + lane, Lane: lane, Prompts: 1, Calls: 1, Config: fmt.Sprintf("/owned/lane-%d/relay.json", lane)}
				encoded, _ := json.Marshal(w)
				if os.WriteFile(filepath.Join(b.path, fmt.Sprintf("owner-%d.json", lane)), encoded, 0600) != nil {
					t.Fatal("owned overlap witness")
				}
				input, _ := json.Marshal(map[string]string{"file_path": nativeToolPath(b.ledger.Lanes[lane], 1)})
				calls[lane] = []anthropic.ToolUse{{ID: fmt.Sprintf("toolu_overlap_%d", lane), Name: "Read", Input: input}}
			}
			results := make(chan error, 2)
			workers.Go(func() { _, err := b.tools(ctx, 0, calls[0]); results <- err })
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				b.mu.Lock()
				arrived := b.rounds[0].Seen[0]
				b.mu.Unlock()
				if arrived {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("first owned arrival did not reach its held wait")
				case <-ticker.C:
				}
			}
			// Lose the first owner only after its per-arrival checks have passed.
			b.mu.Lock()
			if kind == "first-acp-lost" {
				live[[2]int{201, 201}] = false
			} else if kind == "first-relay-lost" {
				live[[2]int{301, 201}] = false
			}
			b.mu.Unlock()
			workers.Go(func() { _, err := b.tools(ctx, 1, calls[1]); results <- err })
			ready := false
			select {
			case <-b.finalReady:
				ready = true
			case <-results:
			case <-ctx.Done():
			}
			b.mu.Lock()
			failed := b.failure != nil
			b.mu.Unlock()
			close(b.rounds[0].Release)
			cancel()
			workers.Wait()
			if ready != (kind == "alive") || failed != (kind != "alive") {
				t.Fatal("paired wait relied on stale first-owner liveness")
			}
		})
	}
}

type nativeToolHeldCancel struct {
	inference.Turn
	entered, release chan struct{}
}

func (t *nativeToolHeldCancel) Cancel() { close(t.entered); <-t.release }

func TestNativeToolSoakWaitsForCancellationCallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	b := &nativeToolBackend{}
	held := &nativeToolHeldCancel{entered: make(chan struct{}), release: make(chan struct{})}
	turn := &nativeToolTurn{Turn: held, backend: b}
	var once sync.Once
	release := func() { once.Do(func() { close(held.release) }) }
	var workers sync.WaitGroup
	defer func() { release(); workers.Wait() }()
	workers.Go(turn.Cancel)
	select {
	case <-held.entered:
	case <-ctx.Done():
		t.Fatal("owned cancellation callback did not begin")
	}
	wait, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	err := b.waitCanceled(wait, 0, func(nativeToolWitness) bool { return true })
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("sibling could proceed while server cancellation was still joining")
	}
	release()
	workers.Wait()
	if err := b.waitCanceled(ctx, 0, func(nativeToolWitness) bool { return true }); err != nil {
		t.Fatal("completed cancellation did not release the sibling")
	}
}
