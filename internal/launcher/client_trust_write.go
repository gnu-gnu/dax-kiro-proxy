package launcher

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

const trustFile = ".claude.json"
const trustLock = trustFile + ".lock"

// Native 2.1.268 can write through a contended lock after a bounded wait. Stage and sync before
// taking its lock path, then abandon a slow publication. This coordinates cooperating writers;
// neither a lock nor a digest comparison is an atomic compare-and-swap against unlocked writers.
const trustPublishBudget = 250 * time.Millisecond

type trustWrite struct {
	root                  *os.Root
	dir, temp             string
	source, staged        os.FileInfo
	expected, replacement [32]byte
}

func stageTrustWrite(dir string, data []byte, expected [32]byte) (*trustWrite, error) {
	if len(data) > MaxSettingsBytes {
		return nil, ErrSettings
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ErrSettings
	}
	w := &trustWrite{root: root, dir: dir, expected: expected, replacement: sha256.Sum256(data)}
	ready := false
	defer func() {
		if !ready {
			w.close()
		}
	}()
	parent, err := root.Stat(".")
	if err != nil || !safeClientAssetDirectory(parent) {
		return nil, ErrSettings
	}
	w.source, err = root.Lstat(trustFile)
	if err != nil || !safeSettings(w.source) {
		return nil, ErrSettings
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, ErrSettings
	}
	w.temp = trustFile + ".dax-" + hex.EncodeToString(suffix[:])
	file, err := root.OpenFile(w.temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, w.source.Mode().Perm())
	if err != nil {
		return nil, ErrSettings
	}
	w.staged, err = file.Stat()
	if err != nil {
		file.Close()
		return nil, ErrSettings
	}
	// Creation applies the process umask; preserve the source permissions through the owned file
	// descriptor instead of changing whichever entry may later occupy the temporary pathname.
	if file.Chmod(w.source.Mode().Perm()) != nil {
		file.Close()
		return nil, ErrSettings
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return nil, ErrSettings
	}
	ready = true
	return w, nil
}

func (w *trustWrite) publish() (bool, error) {
	started := time.Now()
	// Existing locks, including links and unexpected file types, are never reclaimed or waited on.
	if w.root.Mkdir(trustLock, 0700) != nil {
		return false, nil
	}
	lock, err := w.root.Lstat(trustLock)
	if err != nil || !safeClientAssetDirectory(lock) {
		return false, ErrSettings
	}
	defer removeTrustEntry(w.root, trustLock, lock)
	if !w.sameRoot() {
		return false, nil
	}
	sourceDigest, source, sourceErr := trustFileDigest(w.root, trustFile)
	stagedDigest, staged, stagedErr := trustFileDigest(w.root, w.temp)
	if sourceErr != nil || stagedErr != nil || sourceDigest != w.expected || stagedDigest != w.replacement || !os.SameFile(source, w.source) || !os.SameFile(staged, w.staged) || source.Mode() != w.source.Mode() || staged.Mode() != source.Mode() {
		return false, nil
	}
	return w.commit(source, staged, lock, started)
}

func (w *trustWrite) commit(source, staged, lock os.FileInfo, started time.Time) (bool, error) {
	if !w.sameRoot() || !sameTrustEntry(w.root, trustFile, source) || !sameTrustEntry(w.root, w.temp, staged) || !sameTrustEntry(w.root, trustLock, lock) || time.Since(started) > trustPublishBudget {
		return false, nil
	}
	if w.root.Rename(w.temp, trustFile) != nil {
		return false, ErrSettings
	}
	return true, nil
}

func (w *trustWrite) sameRoot() bool {
	current, currentErr := os.Stat(w.dir)
	opened, openedErr := w.root.Stat(".")
	return currentErr == nil && openedErr == nil && safeClientAssetDirectory(opened) && os.SameFile(current, opened)
}

func trustFileDigest(root *os.Root, name string) ([32]byte, os.FileInfo, error) {
	file, err := root.OpenFile(name, settingsReadFlags, 0)
	if err != nil {
		return [32]byte{}, nil, ErrSettings
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !safeSettings(before) || before.Size() > MaxSettingsBytes {
		return [32]byte{}, nil, ErrSettings
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxSettingsBytes+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || !safeSettings(after) || int64(len(data)) != before.Size() || !sameTrustInfo(before, after) {
		return [32]byte{}, nil, ErrSettings
	}
	return sha256.Sum256(data), after, nil
}

func sameTrustInfo(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func sameTrustEntry(root *os.Root, name string, info os.FileInfo) bool {
	current, err := root.Lstat(name)
	return err == nil && sameTrustInfo(current, info) && (current.IsDir() && safeClientAssetDirectory(current) || !current.IsDir() && safeSettings(current))
}

func removeTrustEntry(root *os.Root, name string, info os.FileInfo) {
	current, err := root.Lstat(name)
	if err == nil && info != nil && os.SameFile(current, info) {
		_ = root.Remove(name)
	}
}

func (w *trustWrite) close() {
	removeTrustEntry(w.root, w.temp, w.staged)
	w.root.Close()
}
