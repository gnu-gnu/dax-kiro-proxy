package websearch

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSearchBudgetEnforcesBeforeConcurrentEffects(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture")
	}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			if TakeBudget(dir, 3) {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 3 {
		t.Fatalf("effects admitted=%d", allowed.Load())
	}
	if count, err := BudgetCount(dir, 3); err != nil || count != 3 {
		t.Fatal("budget ledger mismatch")
	}
	if TakeBudget(dir, 0) || TakeBudget(dir, 9) {
		t.Fatal("invalid limit admitted")
	}
	if os.RemoveAll(dir) != nil || TakeBudget(dir, 3) {
		t.Fatal("retired budget recreated")
	}
}
func TestSearchBudgetRejectsForeignPaths(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if os.Mkdir(private, 0700) != nil {
		t.Fatal("fixture")
	}
	alias := filepath.Join(root, "alias")
	if os.Symlink(private, alias) != nil {
		t.Fatal("fixture")
	}
	if TakeBudget(alias, 1) {
		t.Fatal("symlink budget accepted")
	}
	if os.Chmod(private, 0755) != nil {
		t.Fatal("fixture")
	}
	if TakeBudget(private, 1) {
		t.Fatal("public budget accepted")
	}
}
