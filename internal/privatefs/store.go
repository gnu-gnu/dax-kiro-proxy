// Package privatefs writes bounded, owner-only records inside a product-owned directory.
package privatefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var ErrFile = errors.New("record requires an owner-only regular file in a private directory")
var ErrLimit = errors.New("record exceeds byte limit")

const MaxBytes = 4 << 20

type Dir struct{ path string }

func New(path string) (*Dir, error) {
	if !platformSupported || !filepath.IsAbs(path) {
		return nil, ErrFile
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, ErrFile
	}
	return Open(path)
}

// Open requires an existing private directory. Readers of short-lived runtime files must not
// recreate a directory after its owner has removed it during shutdown.
func Open(path string) (*Dir, error) {
	if !platformSupported || !filepath.IsAbs(path) {
		return nil, ErrFile
	}
	path = filepath.Clean(path)
	d := &Dir{path: path}
	root, err := d.root()
	if err != nil {
		return nil, err
	}
	_ = root.Close()
	return d, nil
}
func (d *Dir) root() (*os.Root, error) {
	info, err := os.Lstat(d.path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !ownerOnly(info) {
		return nil, ErrFile
	}
	r, err := os.OpenRoot(d.path)
	if err != nil {
		return nil, ErrFile
	}
	info, err = r.Stat(".")
	if err != nil || !info.IsDir() || !ownerOnly(info) {
		_ = r.Close()
		return nil, ErrFile
	}
	return r, nil
}
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 96 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func (d *Dir) Read(name string, limit int) ([]byte, error) {
	if !validName(name) || limit < 1 || limit > MaxBytes {
		return nil, ErrFile
	}
	r, err := d.root()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.OpenFile(name, readFlags, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, ErrFile
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownerOnly(info) {
		return nil, ErrFile
	}
	if info.Size() > int64(limit) {
		return nil, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, ErrFile
	}
	if len(data) > limit {
		return nil, ErrLimit
	}
	return data, nil
}
func (d *Dir) Write(name string, data []byte) error {
	if !validName(name) {
		return ErrFile
	}
	if len(data) > MaxBytes {
		return ErrLimit
	}
	r, err := d.root()
	if err != nil {
		return err
	}
	defer r.Close()
	if err := checkExisting(r, name); err != nil {
		return err
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return ErrFile
	}
	temp := ".dax-tmp-" + hex.EncodeToString(entropy[:])
	f, err := r.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrFile
	}
	defer func() { _ = f.Close(); _ = r.Remove(temp) }()
	if _, err := f.Write(data); err != nil {
		return ErrFile
	}
	if err := f.Sync(); err != nil {
		return ErrFile
	}
	if err := f.Close(); err != nil {
		return ErrFile
	}
	if err := r.Rename(temp, name); err != nil {
		return ErrFile
	}
	directory, err := r.Open(".")
	if err != nil {
		return ErrFile
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return ErrFile
	}
	return nil
}
func checkExisting(r *os.Root, name string) error {
	info, err := r.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || !ownerOnly(info) {
		return ErrFile
	}
	return nil
}
func (d *Dir) Remove(name string) error {
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
	err = r.Remove(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
