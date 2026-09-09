package launcher

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"dax-kiro-proxy/internal/status"
)

type usageObservedProcess struct {
	usageProcess
	calls *int
}

func (p usageObservedProcess) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	*p.calls++
	if *p.calls > 3 || *p.calls == 1 && method != "session/new" || *p.calls > 1 && method != "_kiro.dev/commands/execute" {
		return nil, errors.New("usage observation rejected unexpected RPC")
	}
	if *p.calls > 1 {
		raw, _ := json.Marshal(params)
		var body struct {
			Command struct {
				Command string
				Args    map[string]any
			}
		}
		want := "tools"
		if *p.calls == 3 {
			want = "usage"
		}
		if json.Unmarshal(raw, &body) != nil || body.Command.Command != want || body.Command.Args == nil || len(body.Command.Args) != 0 {
			return nil, errors.New("usage observation rejected command")
		}
	}
	return p.usageProcess.Call(ctx, method, params)
}

func TestKiroPinnedUsageCache(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for one read-only usage refresh; no model prompt")
	}
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "0" {
		t.Fatal("this usage observation requires model-credit opt-in disabled")
	}
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".kiro", "settings", "cli.json")
	fingerprint := func() (bool, [32]byte, os.FileMode) {
		info, err := os.Lstat(settings)
		if errors.Is(err, os.ErrNotExist) {
			return false, [32]byte{}, 0
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			t.Fatal("cannot safely fingerprint source settings")
		}
		raw, err := os.ReadFile(settings)
		if err != nil || len(raw) > 1<<20 {
			t.Fatal("cannot fingerprint source settings")
		}
		return true, sha256.Sum256(raw), info.Mode()
	}
	existed, before, mode := fingerprint()
	defer func() {
		exists, after, currentMode := fingerprint()
		unchanged := existed == exists && before == after && mode == currentMode
		t.Logf("usage_source_settings_unchanged=%v", unchanged)
		if !unchanged {
			t.Error("usage observation changed source settings")
		}
	}()
	root, err := os.MkdirTemp("/private/tmp", "dax-usage-observation-")
	if err != nil {
		t.Fatal("cannot create usage observation root")
	}
	var cache *status.UsageCache
	var groups []int
	defer func() {
		if cache != nil && cache.Close() != nil {
			t.Error("usage cleanup failed; observation root preserved")
			return
		}
		for _, pid := range groups {
			if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
				t.Error("usage group survived; observation root preserved")
				return
			}
		}
		if os.RemoveAll(root) != nil {
			t.Error("usage observation root cleanup failed")
		} else {
			t.Log("usage_recorded_groups_joined=true, observation_root_removed=true")
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal("cannot create ephemeral scope key")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	info, err := CheckKiro(ctx, runner, KiroConfig{Executable: executable, Home: home, Directory: root, ScopeKey: key})
	if err != nil {
		t.Fatal("read-only usage preflight failed")
	}
	services := productionUsageServices()
	baseStart := services.start
	starts, calls := 0, 0
	services.start = func(ctx context.Context, cfg acp.Config) (usageProcess, error) {
		starts++
		if starts != 1 {
			return nil, errors.New("usage refresh process budget exceeded")
		}
		p, err := baseStart(ctx, cfg)
		if err != nil {
			return nil, err
		}
		groups = append(groups, p.(*acp.Client).PID())
		return usageObservedProcess{usageProcess: p, calls: &calls}, nil
	}
	cache, err = newKiroUsageCache(KiroUsageConfig{Installation: info, Home: home, RuntimeParent: root, ScopeKey: key}, services)
	if err != nil {
		t.Fatal("cannot prepare usage cache")
	}
	started := time.Now()
	first := cache.Read()
	if !first.Refreshing || time.Since(started) > 100*time.Millisecond {
		t.Fatal("cold status read waited")
	}
	var snapshot status.UsageSnapshot
	for {
		select {
		case <-ctx.Done():
			t.Fatal("usage observation deadline exceeded")
		case <-time.After(10 * time.Millisecond):
		}
		snapshot = cache.Read()
		if !snapshot.Refreshing {
			break
		}
	}
	if cache.Close() != nil {
		t.Fatal("usage cache cleanup failed")
	}
	if !snapshot.Available || snapshot.State != "current" || snapshot.Data.Used == nil || snapshot.Data.Remaining != nil || starts != 1 || calls != 3 {
		t.Fatal("actual usage refresh did not establish the bounded data path")
	}
	for range 20 {
		cache.Read()
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("actual usage refresh retained private files")
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), "usageBreakdowns") || strings.Contains(string(raw), "planName") {
		t.Fatal("native private payload escaped the normalized view")
	}
	t.Logf("usage_available=true, used_present=true, limit_present=%v, inferred_remaining=false, refresh_processes=%d, refresh_rpcs=%d, model_prompts=0, elapsed_ms=%d", snapshot.Data.Limit != nil, starts, calls, time.Since(started).Milliseconds())
}
