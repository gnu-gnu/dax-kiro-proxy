package session

import (
	"encoding/json"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/sessionstore"
)

func (d *Driver) resumeRecord(r *anthropic.Request, stamp [32]byte) (*sessionstore.Record, error) {
	if d.cfg.Persistence == nil {
		return nil, nil
	}
	d.mu.Lock()
	live := d.client != nil && d.client.Err() == nil
	d.mu.Unlock()
	if live {
		return nil, nil
	}
	record, err := d.cfg.Persistence.Read()
	if err != nil {
		return nil, nil
	} // Invalidate must still succeed before any backend mutation.
	compat, _ := d.hasher.Digest("compatibility", stamp)
	if record.Profile != d.cfg.PersistenceProfile || record.Launch != d.cfg.PersistenceLaunch || record.Compatibility != compat {
		return nil, nil
	}
	plan, err := d.hasher.Plan(record.History, r)
	if err != nil || plan.Mode != history.Extend || plan.Start != len(record.History.Nodes) {
		return nil, nil
	}
	return record, nil
}
func loadedSession(raw json.RawMessage, record *sessionstore.Record) (catalog.Session, error) {
	fields, err := ndjson.Object(raw)
	if string(raw) == "null" {
		fields = map[string]json.RawMessage{}
		err = nil
	}
	if err != nil {
		return catalog.Session{}, catalog.ErrCatalog
	}
	info := catalog.Session{ID: record.SessionID, ConfigID: record.Selector}
	if id, present := fields["sessionId"]; present {
		var returned string
		if !strictString(id, &returned) || returned != record.SessionID {
			return catalog.Session{}, catalog.ErrCatalog
		}
	}
	_, models := fields["models"]
	_, options := fields["configOptions"]
	if models || options {
		info.Catalog, info.ConfigID, err = catalog.DecodeModels(fields)
		if err != nil || info.Catalog.Current() != record.CurrentModel || info.ConfigID != record.Selector {
			return catalog.Session{}, catalog.ErrCatalog
		}
	} else {
		info.Catalog, err = catalog.New(record.Models, record.CurrentModel)
	}
	return info, err
}

// Called while the driver's idle transition is locked, after successful HTTP completion only.
func (d *Driver) persistIdle(snapshot history.Snapshot, stamp [32]byte) {
	if d.cfg.Persistence == nil {
		return
	}
	compat, _ := d.hasher.Digest("compatibility", stamp)
	now := time.Now()
	record := sessionstore.Record{Version: 1, Key: d.cfg.Persistence.Key(), Profile: d.cfg.PersistenceProfile, Compatibility: compat, Launch: d.cfg.PersistenceLaunch, SessionID: d.modelState.ID, CurrentModel: d.modelState.Catalog.Current(), Selector: d.modelState.ConfigID, Models: d.modelState.Catalog.Models(), History: snapshot, Updated: now, Expires: now.Add(d.cfg.PersistenceTTL)}
	d.persistenceFailed = d.cfg.Persistence.Save(record) != nil
}
