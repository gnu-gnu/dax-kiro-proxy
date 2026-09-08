// Package sessionstore holds metadata-only idle snapshots under exclusive lifetime leases.
package sessionstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/privatefs"
)

const MaxRecordBytes = 2 << 20

var ErrRecord = errors.New("invalid or incompatible session record")

type Store struct {
	dir *privatefs.Dir
	key [32]byte
}
type Record struct {
	Version       int               `json:"version"`
	Key           string            `json:"key"`
	Profile       string            `json:"profile"`
	Compatibility string            `json:"compatibility"`
	Launch        string            `json:"launch"`
	SessionID     string            `json:"session_id"`
	CurrentModel  string            `json:"current_model"`
	Selector      string            `json:"selector,omitempty"`
	Models        []catalog.Backend `json:"models"`
	History       history.Snapshot  `json:"history"`
	Updated       time.Time         `json:"updated"`
	Expires       time.Time         `json:"expires"`
}
type Lease struct {
	mu        sync.Mutex
	store     *Store
	key, name string
	lock      *privatefs.Lock
	closed    bool
}

func New(path string) (*Store, error) {
	dir, err := privatefs.New(path)
	if err != nil {
		return nil, err
	}
	lock, err := dir.TryLock("key.lock")
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	s := &Store{dir: dir}
	key, err := dir.Read("history-key.bin", 32)
	if errors.Is(err, os.ErrNotExist) {
		if _, err = rand.Read(s.key[:]); err != nil {
			return nil, err
		}
		if err = dir.Write("history-key.bin", s.key[:]); err != nil {
			return nil, err
		}
	} else if err != nil || len(key) != 32 {
		return nil, ErrRecord
	} else {
		copy(s.key[:], key)
	}
	if s.key == ([32]byte{}) {
		return nil, ErrRecord
	}
	return s, nil
}
func (s *Store) Key() [32]byte { return s.key }
func digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
func (s *Store) Claim(key string) (*Lease, error) {
	if !digest(key) {
		return nil, ErrRecord
	}
	// A fixed 256-slot cache bounds both record files and retained lock inodes. Collisions lose cache
	// locality, never ownership: the full key must match and a live colliding owner returns busy.
	prefix := "s-" + key[:2]
	lock, err := s.dir.TryLock(prefix + ".lock")
	if err != nil {
		return nil, err
	}
	return &Lease{store: s, key: key, name: prefix + ".json", lock: lock}, nil
}
func (l *Lease) Key() string { return l.key }
func (l *Lease) check() error {
	if l.closed {
		return privatefs.ErrFile
	}
	return l.lock.Check()
}
func (l *Lease) Read() (*Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.check(); err != nil {
		return nil, err
	}
	data, err := l.store.dir.Read(l.name, MaxRecordBytes)
	if err != nil {
		return nil, err
	}
	fields, err := ndjson.Object(data)
	if err != nil {
		return nil, ErrRecord
	}
	for key := range fields {
		switch key {
		case "version", "key", "profile", "compatibility", "launch", "session_id", "current_model", "selector", "models", "history", "updated", "expires":
		default:
			return nil, ErrRecord
		}
	}
	var record Record
	if json.Unmarshal(data, &record) != nil || !record.Valid() {
		return nil, ErrRecord
	}
	if record.Key != l.key || !time.Now().Before(record.Expires) {
		return nil, os.ErrNotExist
	}
	return &record, nil
}
func (l *Lease) Save(record Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.check(); err != nil {
		return err
	}
	if record.Key != l.key || !record.Valid() {
		return ErrRecord
	}
	data, err := json.Marshal(record)
	if err != nil || len(data) > MaxRecordBytes {
		return ErrRecord
	}
	return l.store.dir.Write(l.name, data)
}
func (l *Lease) Invalidate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.check(); err != nil {
		return err
	}
	return l.store.dir.RemoveSync(l.name)
}
func (l *Lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.lock.Close()
}
func (r Record) Valid() bool {
	if r.Version != 1 || !digest(r.Key) || !digest(r.Profile) || !digest(r.Compatibility) || !digest(r.Launch) || r.SessionID == "" || len(r.SessionID) > 1024 || len(r.Selector) > 256 || r.CurrentModel == "" || !r.History.Valid() {
		return false
	}
	if r.Updated.IsZero() || r.Updated.After(time.Now().Add(time.Minute)) || !r.Expires.After(r.Updated) || r.Expires.Sub(r.Updated) > 24*time.Hour {
		return false
	}
	_, err := catalog.New(r.Models, r.CurrentModel)
	return err == nil
}
