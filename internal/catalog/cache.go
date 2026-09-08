package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/privatefs"
)

type Identity struct {
	Executable         string `json:"executable"`
	Version            string `json:"version"`
	ProfileDigest      string `json:"profile_digest"`
	AgentDigest        string `json:"agent_digest"`
	CapabilitiesDigest string `json:"capabilities_digest"`
	InitialModel       string `json:"initial_model,omitempty"`
	InitialEffort      string `json:"initial_effort,omitempty"`
}

func (i Identity) Digest() (string, error) {
	if !filepath.IsAbs(i.Executable) || len(i.Executable) > 4096 || i.Version == "" || len(i.Version) > 256 || len(i.InitialModel) > 256 || len(i.InitialEffort) > 16 {
		return "", ErrCatalog
	}
	for _, s := range []string{i.ProfileDigest, i.AgentDigest, i.CapabilitiesDigest} {
		b, err := hex.DecodeString(s)
		if err != nil || len(b) != 32 {
			return "", ErrCatalog
		}
	}
	data, err := json.Marshal(i)
	if err != nil {
		return "", ErrCatalog
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

type CacheConfig struct {
	Directory                          string
	Identity                           Identity
	TTL, RetryInterval, RefreshTimeout time.Duration
	Now                                func() time.Time
}
type Snapshot struct {
	Catalog *Catalog
	Stale   bool
}
type Discover func(context.Context) (*Catalog, error)
type cacheRecord struct {
	Version  int       `json:"version"`
	Identity string    `json:"identity"`
	Created  int64     `json:"created"`
	Current  string    `json:"current"`
	Models   []Backend `json:"models"`
}
type Cache struct {
	cfg                  CacheConfig
	dir                  *privatefs.Dir
	identity             string
	mu                   sync.Mutex
	data                 *Catalog
	created, lastAttempt time.Time
	flight               chan struct{}
	ctx                  context.Context
	cancel               context.CancelFunc
	closed               bool
}

func NewCache(cfg CacheConfig) (*Cache, error) {
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = time.Minute
	}
	if cfg.RefreshTimeout == 0 {
		cfg.RefreshTimeout = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TTL <= 0 || cfg.TTL > 7*24*time.Hour || cfg.RetryInterval <= 0 || cfg.RetryInterval > time.Hour || cfg.RefreshTimeout <= 0 || cfg.RefreshTimeout > time.Minute {
		return nil, ErrCatalog
	}
	hash, err := cfg.Identity.Digest()
	if err != nil {
		return nil, err
	}
	dir, err := privatefs.New(cfg.Directory)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Cache{cfg: cfg, dir: dir, identity: hash, ctx: ctx, cancel: cancel}
	raw, err := dir.Read("catalog.json", MaxBytes)
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, privatefs.ErrLimit) {
		cancel()
		return nil, err
	}
	if err == nil {
		if data, created, ok := decodeRecord(raw, hash, cfg.Now()); ok {
			c.data = data
			c.created = created
		}
	}
	return c, nil
}

func (c *Cache) Get(ctx context.Context, discover Discover) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Snapshot{}, ErrCatalog
	}
	now := c.cfg.Now()
	if c.data != nil {
		stale := now.Sub(c.created) >= c.cfg.TTL || now.Before(c.created)
		if stale && c.flight == nil && now.Sub(c.lastAttempt) >= c.cfg.RetryInterval && discover != nil {
			c.start(discover, now)
		}
		result := Snapshot{c.data, stale}
		c.mu.Unlock()
		return result, nil
	}
	if c.flight == nil {
		if discover == nil || !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < c.cfg.RetryInterval {
			c.mu.Unlock()
			return Snapshot{}, ErrCatalog
		}
		c.start(discover, now)
	}
	done := c.flight
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-done:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.data == nil {
		return Snapshot{}, ErrCatalog
	}
	return Snapshot{c.data, c.cfg.Now().Sub(c.created) >= c.cfg.TTL}, nil
}

// start is single-flight under mu. A canceled reader does not cancel another reader's discovery.
func (c *Cache) start(discover Discover, now time.Time) {
	done := make(chan struct{})
	c.flight = done
	c.lastAttempt = now
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, c.cfg.RefreshTimeout)
		defer cancel()
		data, err := discover(ctx)
		created := c.cfg.Now()
		if err == nil && data == nil {
			err = ErrCatalog
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil {
			record := cacheRecord{1, c.identity, created.UnixNano(), data.Current(), data.Models()}
			raw, encodeErr := json.Marshal(record)
			if encodeErr != nil || len(raw) > MaxBytes {
				err = ErrCatalog
			} else {
				err = c.dir.Write("catalog.json", raw)
			}
		}
		c.mu.Lock()
		if !c.closed && err == nil {
			c.data = data
			c.created = created
		}
		c.flight = nil
		close(done)
		c.mu.Unlock()
	}()
}
func (c *Cache) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	done := c.flight
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

func decodeRecord(raw []byte, identity string, now time.Time) (*Catalog, time.Time, bool) {
	fields, err := ndjson.Object(raw)
	if err != nil || string(fields["version"]) != "1" {
		return nil, time.Time{}, false
	}
	var record cacheRecord
	if json.Unmarshal(raw, &record) != nil || record.Identity != identity || record.Created <= 0 {
		return nil, time.Time{}, false
	}
	created := time.Unix(0, record.Created)
	if created.After(now.Add(time.Minute)) {
		return nil, time.Time{}, false
	}
	data, err := New(record.Models, record.Current)
	if err != nil {
		return nil, time.Time{}, false
	}
	return data, created, true
}

type lastModel struct {
	Version  int    `json:"version"`
	Identity string `json:"identity"`
	Model    string `json:"model"`
}

// Preferences survive one-launch overrides, while catalog discovery retains the full identity.
// Version 2 invalidates earlier records instead of reinterpreting their narrower identity scope.
func preferenceIdentity(identity Identity) (string, error) {
	if _, err := identity.Digest(); err != nil {
		return "", err
	}
	identity.InitialModel, identity.InitialEffort = "", ""
	return identity.Digest()
}

func SaveLastModel(directory string, identity Identity, backend string, interactive bool) error {
	if !interactive {
		return nil
	}
	if !validID(backend) {
		return ErrModel
	}
	hash, err := preferenceIdentity(identity)
	if err != nil {
		return err
	}
	dir, err := privatefs.New(directory)
	if err != nil {
		return err
	}
	data, err := json.Marshal(lastModel{2, hash, backend})
	if err != nil {
		return ErrCatalog
	}
	return dir.Write("last-model.json", data)
}
func LoadLastModel(directory string, identity Identity, current *Catalog) (string, bool, error) {
	if current == nil {
		return "", false, ErrCatalog
	}
	hash, err := preferenceIdentity(identity)
	if err != nil {
		return "", false, err
	}
	dir, err := privatefs.New(directory)
	if err != nil {
		return "", false, err
	}
	data, err := dir.Read("last-model.json", 8192)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, privatefs.ErrLimit) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	fields, err := ndjson.Object(data)
	if err != nil || string(fields["version"]) != "2" {
		return "", false, nil
	}
	var record lastModel
	if json.Unmarshal(data, &record) != nil || record.Identity != hash {
		return "", false, nil
	}
	if _, err := current.Backend(record.Model); err != nil {
		return "", false, nil
	}
	return record.Model, true, nil
}
