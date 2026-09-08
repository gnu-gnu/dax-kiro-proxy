package privatefs

import (
	"errors"
	"os"
	"sync"
)

var ErrLocked = errors.New("private record already has an owner")

type Lock struct {
	mu   sync.Mutex
	dir  *Dir
	name string
	file *os.File
}

func (d *Dir) TryLock(name string) (*Lock, error) {
	if !platformSupported || !validName(name) {
		return nil, ErrFile
	}
	r, err := d.root()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.OpenFile(name, lockFlags, 0600)
	if err != nil {
		return nil, ErrFile
	}
	fail := func(err error) (*Lock, error) { f.Close(); return nil, err }
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownerOnly(info) || info.Size() != 0 {
		return fail(ErrFile)
	}
	if err = lockExclusive(f); err != nil {
		return fail(err)
	}
	l := &Lock{dir: d, name: name, file: f}
	if err = l.Check(); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
func (l *Lock) Check() error { l.mu.Lock(); defer l.mu.Unlock(); return l.check() }
func (l *Lock) check() error {
	if l.file == nil {
		return ErrFile
	}
	r, err := l.dir.root()
	if err != nil {
		return err
	}
	defer r.Close()
	current, err := r.Lstat(l.name)
	if err != nil || !current.Mode().IsRegular() || !ownerOnly(current) {
		return ErrFile
	}
	opened, err := l.file.Stat()
	if err != nil || !os.SameFile(current, opened) {
		return ErrFile
	}
	return nil
}
func (l *Lock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// RemoveSync makes the disappearance durable before a backend mutation can start.
func (d *Dir) RemoveSync(name string) error {
	if !validName(name) {
		return ErrFile
	}
	r, err := d.root()
	if err != nil {
		return err
	}
	defer r.Close()
	if err := checkExisting(r, name); err != nil {
		return err
	}
	if err = r.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrFile
	}
	f, err := r.Open(".")
	if err != nil {
		return ErrFile
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrFile
	}
	return nil
}
