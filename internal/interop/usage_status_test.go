package interop_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/status"
)

func usageScreenMatches(screen string, snapshot status.UsageSnapshot) bool {
	if !snapshot.Available || snapshot.Refreshing || snapshot.Data.Used == nil || snapshot.Data.Limit == nil || snapshot.Data.Remaining != nil || snapshot.State != "current" && snapshot.State != "refresh_failed" {
		return false
	}
	flat := " " + strings.Join(strings.Fields(strings.NewReplacer("|", " ", ",", " ").Replace(strings.ToLower(screen))), " ") + " "
	for _, s := range []string{"kiro last status-fixture", fmt.Sprintf("%g credits used", *snapshot.Data.Used), fmt.Sprintf("%g credit limit", *snapshot.Data.Limit)} {
		if !strings.Contains(flat, " "+s+" ") {
			return false
		}
	}
	return !strings.Contains(flat, " credits left ") && (!snapshot.Stale || strings.Contains(flat, " (stale) "))
}

func TestUsageScreenRequiresCurrentReportedValues(t *testing.T) {
	used, limit := 17.25, 120.0
	valid := status.UsageSnapshot{Available: true, State: "current", Data: status.UsageData{Used: &used, Limit: &limit}}
	screen := "Kiro last status-fixture | effort unknown | 17.25 credits used, 120 credit limit"
	for _, tc := range []struct {
		name, screen string
		view         status.UsageSnapshot
		want         bool
	}{
		{"complete", screen, valid, true},
		{"partial", strings.Replace(screen, "120 credit limit", "", 1), valid, false},
		{"wrong-amount", strings.Replace(screen, "17.25", "117.25", 1), valid, false},
		{"wrong-model", strings.Replace(screen, "status-fixture", "other", 1), valid, false},
		{"erased", "\n\n", valid, false},
		{"cold", screen, status.UsageSnapshot{State: "unavailable"}, false},
		{"refreshing", screen, status.UsageSnapshot{Available: true, Refreshing: true, State: "current", Data: valid.Data}, false},
		{"unexpected-state", screen, status.UsageSnapshot{Available: true, State: "unsupported", Data: valid.Data}, false},
		{"inferred-remaining", screen + " 102.75 credits left", valid, false},
		{"stale-unmarked", screen, status.UsageSnapshot{Available: true, State: "refresh_failed", Stale: true, Data: valid.Data}, false},
		{"stale-marked", screen + " (stale)", status.UsageSnapshot{Available: true, State: "refresh_failed", Stale: true, Data: valid.Data}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if usageScreenMatches(tc.screen, tc.view) != tc.want {
				t.Fatal("usage rendering verdict changed")
			}
		})
	}
}

func TestClaudeUsageStatusWithBoundedRefresh(t *testing.T) {
	for _, mode := range []string{"complete", "failed-refresh", "cancel-held"} {
		t.Run(mode, func(t *testing.T) { observeClaudeUsageStatus(t, &usageStatusObservation{mode: mode}) })
	}
}

func TestKiroUsageVisibleInClaudeStatus(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Skip("set both pinned CLI paths for one usage refresh and native status rendering; no model prompt")
	}
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "0" {
		t.Fatal("native usage UI observation requires credit opt-in disabled")
	}
	observeClaudeUsageStatus(t, &usageStatusObservation{mode: "native"})
}

type usageStatusObservation struct {
	mode                             string
	cache                            *status.UsageCache
	ready, release                   chan struct{}
	releaseOnce, readyOnce           sync.Once
	mu                               sync.Mutex
	coldSeen, currentSeen, staleSeen bool
	fetches                          atomic.Int32
	offset                           atomic.Int64
	nativeRoot                       string
	sourceSettings                   string
	sourceBefore                     usageSettingsFingerprint
}

type usageSettingsFingerprint struct {
	present bool
	mode    os.FileMode
	sum     [32]byte
}

func fingerprintUsageSettings(t *testing.T, path string) usageSettingsFingerprint {
	t.Helper()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return usageSettingsFingerprint{}
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		t.Fatal("cannot safely fingerprint usage source settings")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("cannot open usage source settings")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		t.Fatal("usage source settings changed during open")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		t.Fatal("cannot fingerprint bounded usage source settings")
	}
	return usageSettingsFingerprint{true, info.Mode(), sha256.Sum256(raw)}
}

func (o *usageStatusObservation) prepare(t *testing.T) {
	t.Helper()
	o.ready, o.release = make(chan struct{}), make(chan struct{})
	var err error
	if o.mode == "native" {
		o.prepareNative(t)
		return
	}
	o.cache, err = status.NewUsageCache(status.UsageConfig{TTL: time.Minute, Timeout: 15 * time.Second, Now: func() time.Time { return time.Unix(1700000000, 0).Add(time.Duration(o.offset.Load())) }, Fetch: func(ctx context.Context) (status.UsageData, error) {
		n := o.fetches.Add(1)
		if n > 1 {
			return status.UsageData{}, errors.New("independent usage refresh failure")
		}
		select {
		case <-o.release:
		case <-ctx.Done():
			return status.UsageData{}, ctx.Err()
		}
		used, limit := 17.25, 120.0
		return status.UsageData{Used: &used, Limit: &limit}, nil
	}})
	if err != nil {
		t.Fatal("cannot prepare synthetic usage cache")
	}
}

func (o *usageStatusObservation) observe(screen string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	flat := strings.ToLower(strings.Join(strings.Fields(screen), " "))
	if strings.Contains(flat, "kiro last status-fixture") && strings.Contains(flat, "usage unavailable") {
		o.coldSeen = true
		if o.mode == "cancel-held" {
			o.readyOnce.Do(func() { close(o.ready) })
		} else {
			o.releaseOnce.Do(func() { close(o.release) })
		}
	}
	// The gateway's first status read owns refresh admission. Observing setup screens must
	// not start a query before the client has requested and rendered the cold view.
	if !o.coldSeen {
		return ""
	}
	// Advancing the fixture clock must leave the next refresh to the next UI status request.
	// Until that request renders the stale view, observer reads could accidentally admit it.
	if o.mode == "failed-refresh" && o.currentSeen && !strings.Contains(flat, "(stale)") {
		return ""
	}
	snapshot := o.cache.Read()
	if o.coldSeen && usageScreenMatches(screen, snapshot) {
		if snapshot.State == "current" && !snapshot.Stale {
			o.currentSeen = true
			if o.mode == "failed-refresh" {
				o.offset.Store(int64(61 * time.Second))
			} else {
				o.readyOnce.Do(func() { close(o.ready) })
			}
		}
		if o.currentSeen && snapshot.State == "refresh_failed" && snapshot.Stale {
			o.staleSeen = true
			o.readyOnce.Do(func() { close(o.ready) })
		}
	}
	return ""
}

func (o *usageStatusObservation) check(t *testing.T) {
	t.Helper()
	if o.mode == "cancel-held" {
		before := o.cache.Read()
		if !before.Refreshing || before.Available {
			t.Error("held refresh ended before owner cancellation")
		}
	}
	if o.cache.Close() != nil {
		t.Fatal("usage owner cleanup failed")
	}
	o.mu.Lock()
	cold, current, stale := o.coldSeen, o.currentSeen, o.staleSeen
	o.mu.Unlock()
	wantFetches := int32(1)
	if o.mode == "failed-refresh" {
		wantFetches = 2
	}
	if !cold || o.mode != "cancel-held" && !current || o.mode == "failed-refresh" && !stale || o.mode != "native" && o.fetches.Load() != wantFetches {
		t.Error("native usage display phases or bounded fetches missing")
	}
	snapshot := o.cache.Read()
	if snapshot.Refreshing {
		t.Error("closed usage cache admitted refresh")
	}
	if o.mode == "cancel-held" && snapshot.Available {
		t.Error("cancelled held query invented data")
	}
	t.Logf("usage_mode=%s, cold_view_visible=%v, current_view_visible=%v, retained_stale_view_visible=%v, synthetic_fetches=%d, refresh_joined=true", o.mode, cold, current, stale, o.fetches.Load())
}

func (o *usageStatusObservation) prepareNative(t *testing.T) {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "dax-native-usage-ui-")
	if err != nil {
		t.Fatal("cannot prepare native usage observer root")
	}
	o.nativeRoot = root
	executable, home := os.Getenv("DAX_INTEROP_KIRO_BINARY"), os.Getenv("HOME")
	o.sourceSettings = filepath.Join(home, ".kiro", "settings", "cli.json")
	o.sourceBefore = fingerprintUsageSettings(t, o.sourceSettings)
	bin := filepath.Join(root, "bin")
	if os.Mkdir(bin, 0700) != nil {
		t.Fatal("cannot create usage observer directory")
	}
	marker := filepath.Join(root, "groups")
	wrapped := filepath.Join(bin, "kiro-cli")
	// The wrapper records only a fixed command class and its owned PID, then replaces itself
	// with the unmodified installed CLI. No request or response content is inspected or saved.
	script := "#!/bin/sh\nset -eu\numask 077\ncase \"$1\" in --version) kind=version;; whoami) kind=identity;; acp) kind=acp;; *) exit 78;; esac\nprintf '%s %s\\n' \"$kind\" \"$$\" >> " + probeShellQuote(marker) + "\nexec " + probeShellQuote(executable) + " \"$@\"\n"
	if os.WriteFile(wrapped, []byte(script), 0700) != nil || os.Symlink(filepath.Join(filepath.Dir(executable), "kiro-cli-chat"), filepath.Join(bin, "kiro-cli-chat")) != nil {
		t.Fatal("cannot prepare read-only CLI observer")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal("cannot create usage identity scope")
	}
	info, err := launcher.CheckKiro(t.Context(), runner, launcher.KiroConfig{Executable: wrapped, Home: home, Directory: root, ScopeKey: key})
	if err != nil {
		t.Fatal("read-only native usage identity check failed")
	}
	parent := filepath.Join(root, "refresh")
	if os.Mkdir(parent, 0700) != nil {
		t.Fatal("cannot prepare isolated refresh parent")
	}
	o.cache, err = launcher.NewKiroUsageCache(launcher.KiroUsageConfig{Installation: info, Home: home, RuntimeParent: parent, ScopeKey: key})
	if err != nil {
		t.Fatal("cannot prepare native account usage cache")
	}
}

func (o *usageStatusObservation) close(t *testing.T) {
	t.Helper()
	if o.cache != nil && o.cache.Close() != nil {
		t.Error("usage cleanup failed; observation root preserved")
		return
	}
	if o.nativeRoot == "" {
		return
	}
	unchanged := fingerprintUsageSettings(t, o.sourceSettings) == o.sourceBefore
	if !unchanged {
		t.Error("native usage changed source settings")
	}
	f, err := os.Open(filepath.Join(o.nativeRoot, "groups"))
	if err != nil {
		t.Error("native usage ownership record unavailable; root preserved")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1025))
	f.Close()
	if err != nil || len(raw) > 1024 {
		t.Error("native usage ownership record unavailable; root preserved")
		return
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Error("invalid usage owner record; root preserved")
			return
		}
		pid, err := strconv.Atoi(parts[1])
		if err != nil || pid < 2 || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			t.Error("usage group remains; root preserved")
			return
		}
		counts[parts[0]]++
	}
	// The adjacent unmodified helper is checked directly, so its two version commands are
	// not wrapper records. The wrapped main has two versions, two identities and one ACP run.
	if counts["version"] != 2 || counts["identity"] != 2 || counts["acp"] != 1 || len(counts) != 3 {
		t.Error("native usage command budget changed")
	}
	entries, err := os.ReadDir(filepath.Join(o.nativeRoot, "refresh"))
	if err != nil || len(entries) != 0 {
		t.Error("usage refresh files remain; root preserved")
		return
	}
	if os.RemoveAll(o.nativeRoot) != nil {
		t.Error("cannot remove joined usage observation root")
	} else {
		t.Logf("native_usage_groups_joined=true, usage_root_removed=true, source_settings_unchanged=%v, model_prompts=0", unchanged)
	}
}
