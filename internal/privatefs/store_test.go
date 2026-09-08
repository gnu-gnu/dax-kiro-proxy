//go:build darwin || linux

package privatefs_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/privatefs"
)

func TestOwnerOnlyAtomicFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	d, err := privatefs.New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"first", "second"} {
		if err := d.Write("record.json", []byte(data)); err != nil {
			t.Fatal(err)
		}
		got, err := d.Read("record.json", 128)
		if err != nil || string(got) != data {
			t.Fatal("atomic record contents")
		}
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{path, 0700}, {filepath.Join(path, "record.json"), 0600}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatal("incorrect private permissions")
		}
	}
	entries, _ := os.ReadDir(path)
	if len(entries) != 1 {
		t.Fatal("temporary record file retained")
	}
	if _, err := d.Read("record.json", 2); !errors.Is(err, privatefs.ErrLimit) {
		t.Fatal("read limit not enforced")
	}
	if err := d.Remove("record.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read("record.json", 128); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("record not removed")
	}
}

func TestSymlinkPermissionsAndSpecialFilesAreRejected(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "private")
	d, err := privatefs.New(path)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(parent, "unrelated")
	if err := os.WriteFile(victim, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(path, "record.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read("record.json", 128); err == nil {
		t.Fatal("followed record symlink")
	}
	if err := d.Write("record.json", []byte("changed")); err == nil {
		t.Fatal("accepted record symlink")
	}
	data, _ := os.ReadFile(victim)
	if string(data) != "unchanged" {
		t.Fatal("unrelated file modified")
	}
	for _, name := range []string{"../unrelated", "/absolute", ".", "..", "sub/record"} {
		if err := d.Write(name, []byte("x")); err == nil {
			t.Fatal("accepted nonlocal record name")
		}
	}
	if err := os.WriteFile(filepath.Join(path, "public.json"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Creation modes are filtered by the caller's umask; make the unsafe fixture explicit.
	if err := os.Chmod(filepath.Join(path, "public.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read("public.json", 128); err == nil {
		t.Fatal("accepted world-readable record")
	}
	if err := syscall.Mkfifo(filepath.Join(path, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := d.Read("pipe", 128); err == nil || time.Since(start) > time.Second {
		t.Fatal("special record blocked or was accepted")
	}
	if err := os.Symlink(path, filepath.Join(parent, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := privatefs.New(filepath.Join(parent, "alias")); err == nil {
		t.Fatal("accepted symlink directory")
	}
	if _, err := privatefs.New(filepath.Join(parent, "alias") + "/"); err == nil {
		t.Fatal("trailing separator bypassed symlink directory check")
	}
}
