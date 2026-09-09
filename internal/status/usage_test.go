package status

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func amount(n float64) *float64 { return &n }

func TestUsageCloseJoinsFetchBeforeReportingOwnerFailure(t *testing.T) {
	entered, ended := make(chan struct{}), make(chan struct{})
	var closes atomic.Int32
	failure := errors.New("independent-cleanup-failure")
	c, err := NewUsageCache(UsageConfig{Fetch: func(ctx context.Context) (UsageData, error) {
		close(entered)
		<-ctx.Done()
		close(ended)
		return UsageData{}, ctx.Err()
	}, Close: func() error {
		select {
		case <-ended:
		default:
			t.Error("owner closed before fetch joined")
		}
		closes.Add(1)
		return failure
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.Read()
	<-entered
	var callers sync.WaitGroup
	for range 20 {
		callers.Go(func() {
			if !errors.Is(c.Close(), failure) {
				t.Error("cleanup failure lost")
			}
		})
	}
	callers.Wait()
	if closes.Load() != 1 || c.Read().Refreshing {
		t.Fatal("cleanup repeated or refresh still admitted")
	}
}
func awaitUsage(t *testing.T, c *UsageCache, accept func(UsageSnapshot) bool) UsageSnapshot {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		s := c.Read()
		if accept(s) {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("usage refresh did not settle")
	return UsageSnapshot{}
}
func TestUsageReadsCoalesceWithoutWaitingAndRetainLastGoodData(t *testing.T) {
	var offset atomic.Int64
	now := func() time.Time { return time.Unix(1700000000, 0).Add(time.Duration(offset.Load())) }
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c, err := NewUsageCache(UsageConfig{Now: now, Fetch: func(ctx context.Context) (UsageData, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
				return UsageData{Used: amount(12), Limit: amount(100)}, nil
			case <-ctx.Done():
				return UsageData{}, ctx.Err()
			}
		}
		return UsageData{}, errors.New("private-credential-sentinel")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	read := make(chan UsageSnapshot, 1)
	go func() { read <- c.Read() }()
	select {
	case first := <-read:
		if first.Available || !first.Refreshing {
			t.Fatal("cold read invented usage or did not schedule refresh")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("status read waited for subprocess data")
	}
	<-entered
	var readers sync.WaitGroup
	for range 100 {
		readers.Go(func() { c.Read() })
	}
	readers.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent status polls multiplied refreshes")
	}
	close(release)
	fresh := awaitUsage(t, c, func(s UsageSnapshot) bool { return s.Available && !s.Refreshing })
	if fresh.Data.Used == nil || *fresh.Data.Used != 12 || fresh.Stale {
		t.Fatal("fresh usage lost")
	}
	*fresh.Data.Used = 999
	if *c.Read().Data.Used != 12 {
		t.Fatal("caller mutated retained usage")
	}
	offset.Store(int64(59 * time.Second))
	c.Read()
	if calls.Load() != 1 {
		t.Fatal("refresh ignored 60-second TTL")
	}
	offset.Store(int64(61 * time.Second))
	stale := c.Read()
	if !stale.Available || !stale.Stale || !stale.Refreshing {
		t.Fatal("stale data did not return immediately during refresh")
	}
	failed := awaitUsage(t, c, func(s UsageSnapshot) bool { return !s.Refreshing })
	if !failed.Available || *failed.Data.Used != 12 || failed.State != "refresh_failed" {
		t.Fatal("failed refresh discarded last good usage")
	}
	raw, _ := json.Marshal(failed)
	if strings.Contains(string(raw), "private-credential-sentinel") {
		t.Fatal("refresh error leaked into status")
	}
	for range 100 {
		c.Read()
	}
	if calls.Load() != 2 {
		t.Fatal("failed refresh ignored backoff")
	}
}
func TestUsageCloseCancelsAndJoinsOwnedRefresh(t *testing.T) {
	entered, ended := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c, err := NewUsageCache(UsageConfig{Fetch: func(ctx context.Context) (UsageData, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(ended)
		return UsageData{}, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.Read()
	<-entered
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("usage shutdown did not join canceled refresh")
	}
	select {
	case <-ended:
	default:
		t.Fatal("fetch outlived cache close")
	}
	c.Close()
	c.Read()
	if calls.Load() != 1 {
		t.Fatal("closed cache restarted a refresh")
	}
}
func TestUnsupportedAndInvalidUsageRemainUnavailable(t *testing.T) {
	c, err := NewUsageCache(UsageConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if s := c.Read(); s.Available || s.Refreshing || s.State != "unsupported" {
		t.Fatal("missing usage adapter invented a balance")
	}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		c, err := NewUsageCache(UsageConfig{Fetch: func(context.Context) (UsageData, error) { return UsageData{Used: amount(bad)}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		c.Read()
		s := awaitUsage(t, c, func(s UsageSnapshot) bool { return !s.Refreshing })
		c.Close()
		if s.Available || s.State != "refresh_failed" {
			t.Fatal("invalid account amount was published")
		}
	}
}
