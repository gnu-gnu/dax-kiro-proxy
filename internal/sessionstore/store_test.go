package sessionstore_test

import (
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/sessionstore"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func record(t *testing.T, key string) sessionstore.Record {
	t.Helper()
	h := history.New([32]byte{2})
	r := &anthropic.Request{Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "secret synthetic prompt"}}}}}
	plan, err := h.Plan(history.Snapshot{}, r)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := h.Complete(plan, []anthropic.Block{{Type: "text", Text: "private synthetic reply"}})
	if err != nil {
		t.Fatal(err)
	}
	return sessionstore.Record{Version: 1, Key: key, Profile: strings.Repeat("1", 64), Compatibility: strings.Repeat("2", 64), Launch: strings.Repeat("3", 64), SessionID: "stored-fixture", CurrentModel: "fixture", Models: []catalog.Backend{{ID: "fixture"}}, History: snapshot, Updated: time.Now(), Expires: time.Now().Add(time.Hour)}
}
func TestMetadataOnlyRecordsStableKeyAndHeldOwnership(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	store, err := sessionstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	lease, err := store.Claim(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Save(record(t, key)); err != nil {
		t.Fatal(err)
	}
	again, err := sessionstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.Key() != again.Key() {
		t.Fatal("digest key changed across restart")
	}
	if _, err := again.Claim(key); !errors.Is(err, privatefs.ErrLocked) {
		t.Fatal("live record had two owners")
	}
	for _, name := range []string{"s-aa.json"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "secret synthetic") || strings.Contains(string(body), "private synthetic") {
			t.Fatal("conversation text persisted")
		}
		var shape map[string]any
		if json.Unmarshal(body, &shape) != nil {
			t.Fatal("invalid record")
		}
	}
	lease.Close()
	loaded, err := again.Claim(key)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	got, err := loaded.Read()
	if err != nil || got.SessionID != "stored-fixture" {
		t.Fatal("record did not survive restart")
	}
	if err := loaded.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if _, err = loaded.Read(); !os.IsNotExist(err) {
		t.Fatal("mutating ownership left resumable idle data")
	}
}
func TestFixedSlotsBoundStorageAndCollisionCannotCrossOwnership(t *testing.T) {
	store, err := sessionstore.New(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	a := strings.Repeat("a", 64)
	b := "aa" + strings.Repeat("b", 62)
	l, err := store.Claim(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Save(record(t, a)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(b); !errors.Is(err, privatefs.ErrLocked) {
		t.Fatal("slot collision bypassed live lease")
	}
	l.Close()
	other, err := store.Claim(b)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.Read(); !os.IsNotExist(err) {
		t.Fatal("hash slot collision resumed another key")
	}
	bad := record(t, b)
	bad.History.Nodes[0].Digest = hex.EncodeToString([]byte("short"))
	if other.Save(bad) == nil {
		t.Fatal("invalid history accepted")
	}
}
