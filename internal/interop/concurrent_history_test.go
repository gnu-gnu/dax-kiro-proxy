package interop_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
)

func nativeRestartHistoryMatches(r *anthropic.Request, stage int, seed string, foreign []string) bool {
	if r == nil || stage < 0 || stage > 1 || seed == "" {
		return false
	}
	clean := func(text string) bool {
		if strings.Contains(text, "NeverSubmitted_99") {
			return false
		}
		for _, other := range foreign {
			if other == "" || strings.Contains(text, other) {
				return false
			}
		}
		return true
	}
	for _, block := range r.System {
		if !clean(block.Text) {
			return false
		}
	}
	oldUser, oldAnswer, newUser, ownSeed := 0, 0, 0, 0
	oldAt, answerAt, newAt, position := -1, -1, -1, 0
	for _, message := range r.Messages {
		for _, block := range message.Content {
			if block.Type != "text" || !clean(block.Text) {
				return false
			}
			if message.Role == "user" {
				n := strings.Count(block.Text, "SeedQuestion_91")
				oldUser += n
				if n > 0 {
					oldAt = position
				}
				n = strings.Count(block.Text, "ContinuedQuestion_97")
				newUser += n
				if n > 0 {
					newAt = position
				}
				ownSeed += strings.Count(block.Text, seed)
			}
			if message.Role == "assistant" {
				n := strings.Count(block.Text, "ArchiveReady_91")
				oldAnswer += n
				if n > 0 {
					answerAt = position
				}
			}
			position++
		}
	}
	return oldUser == 1 && ownSeed == 1 && oldAnswer == stage && newUser == stage && (stage == 0 || oldAt < answerAt && answerAt < newAt)
}

func TestConcurrentNativeHistoryRejectsMixedOrReorderedContext(t *testing.T) {
	const own, other = "OwnedSeed_211", "ForeignSeed_223"
	for _, tc := range []struct {
		name   string
		stage  int
		mutate func(*anthropic.Request)
		want   bool
	}{
		{"fresh", 0, func(r *anthropic.Request) { r.Messages = r.Messages[:1] }, true},
		{"resumed", 1, func(*anthropic.Request) {}, true},
		{"foreign-user", 1, func(r *anthropic.Request) { r.Messages[0].Content[0].Text += other }, false},
		{"foreign-answer", 1, func(r *anthropic.Request) { r.Messages[1].Content[0].Text += other }, false},
		{"foreign-system", 1, func(r *anthropic.Request) { r.System = []anthropic.Block{{Type: "text", Text: other}} }, false},
		{"wrong-seed", 1, func(r *anthropic.Request) { r.Messages[0].Content[0].Text = "SeedQuestion_91 " + other }, false},
		{"duplicate", 1, func(r *anthropic.Request) { r.Messages = append(r.Messages, r.Messages[0]) }, false},
		{"truncated", 1, func(r *anthropic.Request) { r.Messages = r.Messages[1:] }, false},
		{"reordered", 1, func(r *anthropic.Request) { r.Messages[0], r.Messages[1] = r.Messages[1], r.Messages[0] }, false},
		{"invented", 1, func(r *anthropic.Request) { r.Messages[2].Content[0].Text += " NeverSubmitted_99" }, false},
		{"nontext", 1, func(r *anthropic.Request) { r.Messages[1].Content[0].Type = "tool_use" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &anthropic.Request{Messages: []anthropic.Message{
				{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "SeedQuestion_91 " + own}}},
				{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: "ArchiveReady_91"}}},
				{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "ContinuedQuestion_97"}}},
			}}
			tc.mutate(r)
			if nativeRestartHistoryMatches(r, tc.stage, own, []string{other}) != tc.want {
				t.Fatal("mixed, missing or reordered native history admitted")
			}
		})
	}
}

type historyOverlapWitness struct {
	lane        int
	identity    string
	client, acp int
}
type historyOverlapRound struct {
	mu        sync.Mutex
	witnesses [2]historyOverlapWitness
	seen      [2]bool
	count     int
	done      chan struct{}
	once      sync.Once
	err       error
	alive     func(int) bool
}

func newHistoryOverlapRound(alive func(int) bool) *historyOverlapRound {
	return &historyOverlapRound{done: make(chan struct{}), alive: alive}
}
func (r *historyOverlapRound) fail() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = errors.New("native history overlap failed")
	r.once.Do(func() { close(r.done) })
}
func (r *historyOverlapRound) register(w historyOverlapWitness) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	valid := r.err == nil && w.lane >= 0 && w.lane < 2 && nativeHistoryID(w.identity) && w.client > 1 && w.acp > 1 && w.client != w.acp
	if valid {
		valid = !r.seen[w.lane] && r.alive(w.client) && r.alive(w.acp)
	}
	for i, seen := range r.seen {
		if seen {
			old := r.witnesses[i]
			valid = valid && old.identity != w.identity && old.client != w.client && old.client != w.acp && old.acp != w.client && old.acp != w.acp
		}
	}
	if valid {
		r.seen[w.lane] = true
		r.witnesses[w.lane] = w
		r.count++
		if r.count == 2 {
			for _, old := range r.witnesses {
				valid = valid && r.alive(old.client) && r.alive(old.acp)
			}
		}
	}
	if !valid {
		r.err = errors.New("distinct live native history owners not established")
	}
	if r.err != nil || r.count == 2 {
		r.once.Do(func() { close(r.done) })
	}
	return r.err
}
func (r *historyOverlapRound) wait(ctx context.Context, w historyOverlapWitness) error {
	if err := r.register(w); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func TestConcurrentHistoryRequiresDistinctLiveOwners(t *testing.T) {
	for _, mode := range []string{"valid", "duplicate-lane", "same-session", "same-client", "cross-group", "dead-first-owner", "missing-peer", "aborted"} {
		t.Run(mode, func(t *testing.T) {
			live := map[int]bool{201: true, 202: true, 203: true, 204: true}
			r := newHistoryOverlapRound(func(pid int) bool { return live[pid] })
			a := historyOverlapWitness{0, "00000000-0000-4000-8000-000000000001", 201, 202}
			b := historyOverlapWitness{1, "00000000-0000-4000-8000-000000000002", 203, 204}
			if err := r.register(a); err != nil {
				t.Fatal(err)
			}
			select {
			case <-r.done:
				t.Fatal("one owner satisfied overlap")
			default:
			}
			switch mode {
			case "duplicate-lane":
				b.lane = 0
			case "same-session":
				b.identity = a.identity
			case "same-client":
				b.client = a.client
			case "cross-group":
				b.acp = a.client
			case "dead-first-owner":
				live[a.client] = false
			case "missing-peer":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				pending := newHistoryOverlapRound(func(pid int) bool { return live[pid] })
				if !errors.Is(pending.wait(ctx, a), context.Canceled) {
					t.Fatal("waiting for a missing peer ignored cancellation")
				}
				select {
				case <-pending.done:
					t.Fatal("missing peer completed")
				default:
					return
				}
			case "aborted":
				r.fail()
			}
			err := r.register(b)
			if (err == nil) != (mode == "valid") {
				t.Fatal("invalid concurrent ownership accepted")
			}
			select {
			case <-r.done:
			default:
				t.Fatal("completed or failed overlap did not release waiters")
			}
		})
	}
}

type historyOwnership struct {
	identity                 string
	client, acp              int
	profile, endpoint, token string
}
type concurrentHistoryPlan struct {
	shared *concurrentHistoryFixture
	lane   int
}
type concurrentHistoryFixture struct {
	home, project string
	seeds         [2]string
	rounds        [2]*historyOverlapRound
	mu            sync.Mutex
	owners        [2][2]historyOwnership
	joined        [2]bool
	joinedAll     chan struct{}
	joinedOnce    sync.Once
	err           error
}

func (f *concurrentHistoryFixture) record(stage, lane int, owner historyOwnership) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owners[stage][lane] = owner
}
func (f *concurrentHistoryFixture) abort() {
	for _, r := range f.rounds {
		r.fail()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = errors.New("concurrent native history episode failed")
	f.joinedOnce.Do(func() { close(f.joinedAll) })
}
func (f *concurrentHistoryFixture) joinInitial(ctx context.Context, lane int) error {
	f.mu.Lock()
	owner := f.owners[0][lane]
	if f.joined[lane] || owner.client < 2 || !errors.Is(syscall.Kill(-owner.client, 0), syscall.ESRCH) || owner.acp < 2 || !errors.Is(syscall.Kill(-owner.acp, 0), syscall.ESRCH) {
		f.err = errors.New("old native history owner remains")
	}
	f.joined[lane] = true
	if f.err != nil || f.joined[0] && f.joined[1] {
		f.joinedOnce.Do(func() { close(f.joinedAll) })
	}
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.joinedAll:
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func TestClaudeConcurrentNativeHistoryThroughGatewayAndACP(t *testing.T) {
	if os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Skip("set pinned Claude for two concurrent owned native histories with independent ACP; no external inference")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-concurrent-history-")
	if err != nil {
		t.Fatal("concurrent history root")
	}
	defer os.RemoveAll(root)
	f := &concurrentHistoryFixture{home: filepath.Join(root, "home"), project: filepath.Join(root, "project"), seeds: [2]string{rand.Text(), rand.Text()}, joinedAll: make(chan struct{})}
	for _, dir := range []string{f.home, filepath.Join(f.home, ".claude"), f.project} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("concurrent history sources")
		}
	}
	settings, global := filepath.Join(f.home, ".claude", "settings.json"), filepath.Join(f.home, ".claude.json")
	if os.WriteFile(settings, []byte(`{"disableAllHooks":true,"autoMemoryEnabled":false,"permissions":{"deny":["Read","Write","Edit","Bash"]}}`), 0600) != nil || os.WriteFile(global, []byte(`{}`), 0600) != nil {
		t.Fatal("concurrent history settings")
	}
	beforeSettings, beforeGlobal := fileFingerprint(t, settings), fileFingerprint(t, global)
	alive := func(pid int) bool {
		g, err := syscall.Getpgid(pid)
		return pid > 1 && err == nil && g == pid && syscall.Kill(-pid, 0) == nil
	}
	for i := range f.rounds {
		f.rounds[i] = newHistoryOverlapRound(alive)
	}
	if !t.Run("writers", func(t *testing.T) {
		for lane := range 2 {
			t.Run(strconv.Itoa(lane), func(t *testing.T) { t.Parallel(); observeNativeRestartPlan(t, false, &concurrentHistoryPlan{f, lane}) })
		}
	}) {
		return
	}
	identities, groups, profiles, endpoints, tokens := map[string]bool{}, map[int]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for stage := range 2 {
		r := f.rounds[stage]
		if r.err != nil || r.count != 2 {
			t.Fatal("concurrent history overlap incomplete")
		}
		for lane := range 2 {
			o := f.owners[stage][lane]
			w := r.witnesses[lane]
			if o.identity != w.identity || o.client != w.client || o.acp != w.acp || !f.joined[lane] {
				t.Fatal("history ownership witness mismatch")
			}
			if stage == 1 && o.identity != f.owners[0][lane].identity {
				t.Fatal("resume selected a different native history")
			}
			identities[o.identity] = true
			for _, pid := range []int{o.client, o.acp} {
				if pid < 2 || groups[pid] || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
					t.Fatal("native history group reused or retained")
				}
				groups[pid] = true
			}
			if o.profile == "" || o.endpoint == "" || o.token == "" || profiles[o.profile] || endpoints[o.endpoint] || tokens[o.token] {
				t.Fatal("concurrent history routing/profile ownership reused")
			}
			profiles[o.profile], endpoints[o.endpoint], tokens[o.token] = true, true, true
		}
	}
	if len(identities) != 2 || fileFingerprint(t, settings) != beforeSettings || fileFingerprint(t, global) != beforeGlobal {
		t.Fatal("concurrent session identities or source preservation failed")
	}
	entries, total, transcripts := 0, int64(0), 0
	err = filepath.WalkDir(filepath.Join(f.home, ".claude", "projects"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 128 || d.Type()&os.ModeSymlink != 0 {
			return errors.New("native history inventory bound")
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			return errors.New("native history file bound")
		}
		total += info.Size()
		if total > 8<<20 {
			return errors.New("native history total bound")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
		file.Close()
		if err != nil || len(raw) > 2<<20 {
			return errors.New("bounded native history read")
		}
		for token := range tokens {
			if bytes.Contains(raw, []byte(token)) {
				return errors.New("routing credential retained in native history")
			}
		}
		for lane := range 2 {
			if filepath.Base(path) == f.owners[0][lane].identity+".jsonl" {
				if !bytes.Contains(raw, []byte(f.seeds[lane])) || bytes.Contains(raw, []byte(f.seeds[1-lane])) || !bytes.Contains(raw, []byte("ArchiveResumed_97")) {
					return errors.New("native transcript markers mixed or lost")
				}
				transcripts++
			}
		}
		return nil
	})
	if err != nil || transcripts != 2 {
		t.Fatal("independent native histories did not survive concurrent writes")
	}
	t.Logf("simultaneous_native_sessions=2, overlapping_rounds=2, completed_turns=4, matched_transcripts=%d, recorded_groups_joined=%d, fresh_profiles=%d, fresh_endpoints=%d, inventory_entries=%d, inventory_bytes=%d, source_settings_unchanged=true, foreign_history_absent=true, credentials_in_history=false", transcripts, len(groups), len(profiles), len(endpoints), entries, total)
}
