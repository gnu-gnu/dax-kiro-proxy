package privatefs_test

import (
	"dax-kiro-proxy/internal/privatefs"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExclusiveLeaseAndDurableInvalidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	d, err := privatefs.New(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.TryLock("session.lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.TryLock("session.lock"); !errors.Is(err, privatefs.ErrLocked) {
		t.Fatal("two owners acquired one record")
	}
	if err := d.Write("session.json", []byte(`{"state":"idle"}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveSync("session.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read("session.json", 1024); !os.IsNotExist(err) {
		t.Fatal("invalidated record remained readable")
	}
	if err := a.Check(); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a.Close()
	b, err := d.TryLock("session.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := os.Rename(filepath.Join(path, "session.lock"), filepath.Join(path, "replaced.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "session.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if b.Check() == nil {
		t.Fatal("replaced ownership file remained trusted")
	}
}
func TestLeaseRefusesLinksAndUnsafeFileModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	d, err := privatefs.New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "target"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(path, "link.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TryLock("link.lock"); err == nil {
		t.Fatal("symlink lock accepted")
	}
	if err := os.WriteFile(filepath.Join(path, "wide.lock"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(path, "wide.lock"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TryLock("wide.lock"); err == nil {
		t.Fatal("non-private lock accepted")
	}
}
