package websearch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"dax-kiro-proxy/internal/anthropic"
)

// TakeBudget is called only by the owned Kiro preToolUse hook. Atomic exclusive creation makes
// parallel invocations consume distinct slots before any search effect. Failures never allow use.
func TakeBudget(directory string, limit int) bool {
	root, err := budgetRoot(directory, limit)
	if err != nil {
		return false
	}
	defer root.Close()
	for i := range limit {
		file, err := root.OpenFile(fmt.Sprintf("search-%d", i), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return file.Close() == nil
		}
		if !errors.Is(err, os.ErrExist) {
			return false
		}
	}
	return false
}
func BudgetCount(directory string, limit int) (int, error) {
	root, err := budgetRoot(directory, limit)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	count := 0
	for i := range limit {
		info, err := root.Lstat(fmt.Sprintf("search-%d", i))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != 0 {
			return 0, os.ErrPermission
		}
		count++
	}
	return count, nil
}
func budgetRoot(directory string, limit int) (*os.Root, error) {
	if !filepath.IsAbs(directory) || limit < 1 || limit > anthropic.MaxSearchUses {
		return nil, os.ErrPermission
	}
	private := func(info os.FileInfo) bool {
		if info == nil {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		return ok && info.IsDir() && info.Mode().Perm() == 0700 && stat.Uid == uint32(os.Geteuid())
	}
	before, err := os.Lstat(directory)
	if err != nil || !private(before) {
		return nil, os.ErrPermission
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, os.ErrPermission
	}
	after, err := root.Stat(".")
	if err != nil || !private(after) || !os.SameFile(before, after) {
		root.Close()
		return nil, os.ErrPermission
	}
	return root, nil
}
