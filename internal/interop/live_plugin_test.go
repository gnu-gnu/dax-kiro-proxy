package interop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func TestKiroLivePluginRegistryReplacement(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("live plugin sequence requires explicit credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Fatal("both pinned executables are required")
	}
	for _, mode := range []string{"live-wait-tool", "live-wait-tool-denied"} {
		if !t.Run(mode, func(t *testing.T) { observeClaudePluginSources(t, mode) }) {
			return
		}
	}
}

// The plugin skill's own instruction must reach the actual model: a fresh marker that only the
// expanded skill supplies has to appear in the client's final answer (D119).
func TestKiroLivePluginSkillContent(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("live plugin skill content requires explicit credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Fatal("both pinned executables are required")
	}
	observePluginAssetProfile(t, false, pluginLiveSkill, false)
}

// Plugin SessionStart and Stop hooks must surround one actual Kiro turn, with the startup context
// in the request the model receives (D119).
func TestKiroLivePluginHooksAroundTurn(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("live plugin hooks require explicit credit opt-in")
	}
	if os.Getenv("DAX_INTEROP_KIRO_BINARY") == "" || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") == "" {
		t.Fatal("both pinned executables are required")
	}
	observePluginAssetProfile(t, false, pluginLiveHooks, false)
}

// The two observed groups and prepared policies must be retired in order. This helper owns only
// new fixture roots; Kiro's original account HOME remains under Kiro's own authentication handling.
func prepareLivePluginDriver(t *testing.T, ctx context.Context, runner *childproc.Runner, root, backend string, validator *schemacheck.Pool) (*session.Driver, *catalog.Catalog, func(int) error, func()) {
	t.Helper()
	models, execution := prepareLiveKiroProbe(t, ctx, runner, os.Getenv("DAX_INTEROP_KIRO_BINARY"), root, backend, filepath.Join(root, "preflight-kiro"))
	process := execution.Process
	process.Limits.RequestTimeout = 45 * time.Second
	pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 1, SessionsPerProcess: 1, MaxIdle: 1, SetupTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal("cannot create owned plugin ACP pool")
	}
	relay := buildRelayObserver(t)
	var prepared, cleaned atomic.Int32
	var mu sync.Mutex
	var paths []string
	var groups [2]int
	config := session.Config{Process: process, Pool: pool, InitialModel: "auto", Validator: validator, RelayExecutable: relay, SetupTimeout: 20 * time.Second, TurnTimeout: 45 * time.Second, MaxRecreations: 1}
	config.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		n := prepared.Add(1)
		if n > 2 || cleaned.Load() != n-1 || input.Registry == nil {
			return session.LaunchResources{}, errors.New("plugin process budget or old cleanup failed")
		}
		wait, plugin := false, false
		for _, tool := range input.Registry.Tools() {
			wait = wait || tool.Name == "WaitForMcpServers"
			plugin = plugin || tool.Name == ownedPluginToolName
		}
		if n == 1 && (!wait || plugin) || n == 2 && !plugin {
			return session.LaunchResources{}, errors.New("unexpected plugin registry transition")
		}
		if n == 2 {
			records, err := relayProcessRecords(relay)
			mu.Lock()
			valid := err == nil && len(records) == 1 && groups[0] > 1 && errors.Is(syscall.Kill(-groups[0], 0), syscall.ESRCH)
			if len(records) == 1 {
				valid = valid && errors.Is(syscall.Kill(records[0].pid, 0), syscall.ESRCH)
			}
			for _, path := range paths {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					valid = false
				}
			}
			mu.Unlock()
			if !valid {
				return session.LaunchResources{}, errors.New("old plugin process or policy survived")
			}
		}
		owned, err := execution.Prepare(ctx, input)
		if owned.Cleanup != nil {
			cleanup := owned.Cleanup
			owned.Cleanup = func() error { err := cleanup(); cleaned.Add(1); return err }
		}
		mu.Lock()
		paths = append(paths, owned.Directory, input.RelayConfig)
		mu.Unlock()
		return owned, err
	}
	driver, err := session.New(config)
	if err != nil {
		pool.Close()
		t.Fatal("cannot create owned plugin session")
	}
	beforeUse := func(stage int) error {
		records, err := relayProcessRecords(relay)
		if err != nil || stage < 1 || stage > 2 || len(records) != stage {
			return errors.New("missing plugin relay identity")
		}
		group, err := syscall.Getpgid(records[stage-1].pid)
		if err != nil || group <= 1 || group == syscall.Getpgrp() {
			return errors.New("invalid owned plugin process group")
		}
		mu.Lock()
		groups[stage-1] = group
		mu.Unlock()
		return nil
	}
	var once sync.Once
	finish := func() {
		once.Do(func() {
			// An early failure can occur before the tool callback records a live group.
			// Observe any still-running owned relay before closing it; absence is not a leak.
			if records, err := relayProcessRecords(relay); err == nil && len(records) <= len(groups) {
				mu.Lock()
				for i, record := range records {
					if group, err := syscall.Getpgid(record.pid); err == nil && group > 1 && group != syscall.Getpgrp() && groups[i] == 0 {
						groups[i] = group
					}
				}
				mu.Unlock()
			}
			closeErr, poolErr := driver.Close(), pool.Close()
			mu.Lock()
			defer mu.Unlock()
			records, err := relayProcessRecords(relay)
			relaysGone, groupsGone, artifactsGone := err == nil, true, true
			rescueDeadline := time.Now().Add(time.Second)
			for _, record := range records {
				if !errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH) {
					relaysGone = false
					// Preserve the failed cleanup evidence and signal only the recorded owned PID.
					_ = syscall.Kill(record.pid, syscall.SIGKILL)
					for time.Now().Before(rescueDeadline) && !errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH) {
						time.Sleep(10 * time.Millisecond)
					}
				}
			}
			observedGroups := 0
			for _, group := range groups {
				if group > 1 {
					observedGroups++
					groupsGone = groupsGone && errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
				}
			}
			for _, path := range paths {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					artifactsGone = false
				}
			}
			complete := prepared.Load() == 2 && cleaned.Load() == 2 && len(records) == 2 && observedGroups == 2 && groups[0] != groups[1]
			poolEmpty, closeOK := pool.Stats().Processes == 0, closeErr == nil && poolErr == nil
			t.Logf("live_plugin_prepared=%d, cleaned=%d, relay_records=%d, relays_gone=%v, observed_groups=%d, observed_groups_gone=%v, artifacts_gone=%v, pool_empty=%v, close_ok=%v, complete_process_sequence=%v", prepared.Load(), cleaned.Load(), len(records), relaysGone, observedGroups, groupsGone, artifactsGone, poolEmpty, closeOK, complete)
			if !closeOK || !poolEmpty || prepared.Load() != cleaned.Load() || !relaysGone || !groupsGone || !artifactsGone {
				t.Error("live plugin process cleanup was not established")
			}
			if !complete {
				t.Error("live plugin did not establish two distinct prepared processes")
			}
		})
	}
	t.Cleanup(finish)
	return driver, models, beforeUse, finish
}
