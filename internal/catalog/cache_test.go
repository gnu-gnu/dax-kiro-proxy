package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/catalog"
)

func identity() catalog.Identity {
	return catalog.Identity{Executable: "/synthetic/kiro-cli", Version: "fixture-1", ProfileDigest: strings.Repeat("a", 64), AgentDigest: strings.Repeat("b", 64), CapabilitiesDigest: strings.Repeat("c", 64)}
}
func modelCatalog(t *testing.T, id string) *catalog.Catalog {
	t.Helper()
	c, err := catalog.New([]catalog.Backend{{ID: id}}, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestCatalogCacheCoalescesAndUsesCompatibleDiskState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	cfg := catalog.CacheConfig{Directory: dir, Identity: identity()}
	cache, err := catalog.NewCache(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	data := modelCatalog(t, "model.a")
	discover := func(ctx context.Context) (*catalog.Catalog, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return data, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			snapshot, err := cache.Get(context.Background(), discover)
			if err != nil || snapshot.Stale || snapshot.Catalog.Current() != "model.a" {
				t.Error("coalesced discovery failed")
			}
		})
	}
	<-started
	close(release)
	readers.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent discovery was duplicated")
	}
	other, err := catalog.NewCache(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Get(context.Background(), func(context.Context) (*catalog.Catalog, error) {
		t.Error("fresh compatible cache was rediscovered")
		return nil, errors.New("unexpected")
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "catalog.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("catalog not owner-only")
	}
	cfg.Identity.Version = "fixture-2"
	different, err := catalog.NewCache(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer different.Close()
	if snapshot, err := different.Get(context.Background(), func(context.Context) (*catalog.Catalog, error) { return modelCatalog(t, "model.b"), nil }); err != nil || snapshot.Catalog.Current() != "model.b" {
		t.Fatal("identity mismatch reused incompatible catalog")
	}
}

func TestStaleCatalogReturnsImmediatelyAndFailedRefreshPreservesData(t *testing.T) {
	var seconds atomic.Int64
	seconds.Store(1_780_000_000)
	now := func() time.Time { return time.Unix(seconds.Load(), 0) }
	cache, err := catalog.NewCache(catalog.CacheConfig{Directory: filepath.Join(t.TempDir(), "catalog"), Identity: identity(), TTL: time.Minute, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	data := modelCatalog(t, "model.a")
	if _, err := cache.Get(context.Background(), func(context.Context) (*catalog.Catalog, error) { return data, nil }); err != nil {
		t.Fatal(err)
	}
	seconds.Add(61)
	started := make(chan struct{})
	var calls atomic.Int32
	refresh := func(ctx context.Context) (*catalog.Catalog, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	snapshot, err := cache.Get(context.Background(), refresh)
	if err != nil || !snapshot.Stale || snapshot.Catalog.Current() != "model.a" || time.Since(start) > 50*time.Millisecond {
		t.Fatal("stale read waited for refresh")
	}
	<-started
	for range 8 {
		snapshot, err = cache.Get(context.Background(), refresh)
		if err != nil || !snapshot.Stale {
			t.Fatal("refresh removed last good data")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("stale refresh was not coalesced")
	}
	cache.Close()
	if _, err := cache.Get(context.Background(), refresh); err == nil {
		t.Fatal("closed cache admitted work")
	}
}

func TestLastInteractiveModelIsSeparateAndStrictlyValidated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	catalogue := modelCatalog(t, "model.a")
	scope := identity()
	if err := catalog.SaveLastModel(dir, scope, "model.a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "last-model.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("background request persisted model")
	}
	if err := catalog.SaveLastModel(dir, scope, "model.a", true); err != nil {
		t.Fatal(err)
	}
	id, ok, err := catalog.LoadLastModel(dir, scope, catalogue)
	if err != nil || !ok || id != "model.a" {
		t.Fatal("last model not restored")
	}
	other := modelCatalog(t, "model.b")
	if _, ok, err := catalog.LoadLastModel(dir, scope, other); err != nil || ok {
		t.Fatal("unavailable last model restored")
	}
	var record map[string]any
	data, err := os.ReadFile(filepath.Join(dir, "last-model.json"))
	if err != nil || json.Unmarshal(data, &record) != nil {
		t.Fatal("invalid last model record")
	}
	if len(record) != 3 {
		t.Fatal("unexpected conversation data in last model record")
	}
	scope.ProfileDigest = strings.Repeat("d", 64)
	if _, ok, err := catalog.LoadLastModel(dir, scope, catalogue); err != nil || ok {
		t.Fatal("last model crossed profile scope")
	}
}

func TestLastModelPreferenceSurvivesRemovalOfLaunchOverrides(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	current := modelCatalog(t, "model.a")
	initial := identity()
	initial.InitialModel, initial.InitialEffort = "model.a", "high"
	if err := catalog.SaveLastModel(dir, initial, "model.a", true); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := catalog.LoadLastModel(dir, identity(), current); err != nil || !ok || got != "model.a" {
		t.Fatal("removing one-launch overrides discarded the last interactive preference")
	}
	firstDigest, _ := initial.Digest()
	secondDigest, _ := identity().Digest()
	if firstDigest == secondDigest {
		t.Fatal("catalog discovery identity must still include launch overrides")
	}
	changed := identity()
	changed.AgentDigest = strings.Repeat("d", 64)
	if _, ok, err := catalog.LoadLastModel(dir, changed, current); err != nil || ok {
		t.Fatal("last model preference crossed agent policy identity")
	}
	legacy, _ := json.Marshal(map[string]any{"version": 1, "identity": secondDigest, "model": "model.a"})
	if os.WriteFile(filepath.Join(dir, "last-model.json"), legacy, 0600) != nil {
		t.Fatal("cannot write owned legacy fixture")
	}
	if _, ok, err := catalog.LoadLastModel(dir, identity(), current); err != nil || ok {
		t.Fatal("old preference identity was silently reinterpreted")
	}
}

func TestFailedCatalogRefreshRetainsLastGoodDataAndBacksOff(t *testing.T) {
	var seconds atomic.Int64
	seconds.Store(1_780_000_000)
	now := func() time.Time { return time.Unix(seconds.Load(), 0) }
	cache, err := catalog.NewCache(catalog.CacheConfig{Directory: filepath.Join(t.TempDir(), "catalog"), Identity: identity(), TTL: time.Minute, Now: now, RefreshTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	data := modelCatalog(t, "model.a")
	if _, err := cache.Get(context.Background(), func(context.Context) (*catalog.Catalog, error) { return data, nil }); err != nil {
		t.Fatal(err)
	}
	seconds.Add(61)
	var calls atomic.Int32
	done := make(chan struct{})
	failure := func(context.Context) (*catalog.Catalog, error) {
		calls.Add(1)
		close(done)
		return nil, errors.New("synthetic catalog failure")
	}
	snapshot, err := cache.Get(context.Background(), failure)
	if err != nil || !snapshot.Stale {
		t.Fatal("stale catalog lost before refresh")
	}
	<-done
	for range 8 {
		snapshot, err = cache.Get(context.Background(), failure)
		if err != nil || snapshot.Catalog.Current() != "model.a" || !snapshot.Stale {
			t.Fatal("failed refresh discarded last good data")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("failed refresh did not back off")
	}
}
