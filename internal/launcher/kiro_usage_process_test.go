package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

func TestKiroUsageIndependentProcessQueriesAndCancellation(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "kiro-cli")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/usage")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("independent usage peer build failed: %v %s", err, out)
	}
	if os.Link(bin, filepath.Join(filepath.Dir(bin), "kiro-cli-chat")) != nil {
		t.Fatal("cannot prepare paired fixture")
	}
	for _, mode := range []string{"complete", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			cfg, _ := usageLifecycleConfig(t)
			runner, err := childproc.New(childproc.Config{MaxProcesses: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()
			cfg.Installation, err = CheckKiro(t.Context(), runner, KiroConfig{Executable: bin, Home: cfg.Home, Directory: cfg.Home, ScopeKey: cfg.ScopeKey})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(cfg.Home, "usage-observations")
			if os.WriteFile(path, nil, 0600) != nil {
				t.Fatal("cannot reset fixture observations")
			}
			if mode == "cancel" && os.WriteFile(filepath.Join(cfg.Home, "usage-hold"), nil, 0600) != nil {
				t.Fatal("cannot hold usage response")
			}
			cache, err := NewKiroUsageCache(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			started := time.Now()
			first := cache.Read()
			if !first.Refreshing || time.Since(started) > 100*time.Millisecond {
				t.Fatal("status read blocked on usage process")
			}
			if mode == "complete" {
				got := awaitLauncherUsage(t, cache)
				if !got.Available || got.Data.Used == nil || *got.Data.Used != 17.25 || got.Data.Limit == nil || *got.Data.Limit != 120 || got.Data.Remaining != nil {
					raw, _ := os.ReadFile(path)
					t.Fatalf("independent process amounts were lost; fixed_fixture_records=%q", raw)
				}
			} else {
				until := time.Now().Add(3 * time.Second)
				for {
					raw, _ := os.ReadFile(path)
					if strings.Contains(string(raw), "usage ") {
						break
					}
					if time.Now().After(until) {
						t.Fatal("usage RPC was not reached")
					}
					time.Sleep(time.Millisecond)
				}
			}
			if cache.Close() != nil || cache.Close() != nil {
				t.Fatal("usage owner failed to join")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				parts := strings.Fields(line)
				if len(parts) != 2 {
					t.Fatal("malformed synthetic record")
				}
				counts[parts[0]]++
				pid, err := strconv.Atoi(parts[1])
				if err != nil || pid < 2 {
					t.Fatal("invalid recorded process")
				}
				if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
					t.Fatal("usage process group survived")
				}
			}
			if counts["version"] != 2 || counts["identity"] != 1 || counts["acp"] != 1 || counts["tools"] != 1 || counts["usage"] != 1 || len(counts) != 5 {
				t.Fatal("usage command budget or no-prompt invariant failed")
			}
			entries, _ := os.ReadDir(cfg.RuntimeParent)
			if len(entries) != 0 {
				t.Fatal("usage runtime survived joined shutdown")
			}
			for range 10 {
				cache.Read()
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(raw) {
				t.Fatal("closed cache started a late process")
			}
		})
	}
}
