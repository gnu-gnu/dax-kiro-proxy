package launcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/privatefs"
)

func scopeDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(t.TempDir(), "identity-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScopeKeyPersistsUnderConcurrentAdmission(t *testing.T) {
	dir := scopeDirectory(t)
	const count = 16
	keys := make([][32]byte, count)
	errs := make([]error, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range count {
		workers.Go(func() { <-start; keys[i], errs[i] = loadScopeKey(t.Context(), dir) })
	}
	close(start)
	workers.Wait()
	var selected [32]byte
	for i, err := range errs {
		if err != nil {
			if !errors.Is(err, ErrState) {
				t.Fatalf("unexpected state error: %v", err)
			}
			continue
		}
		if keys[i] == ([32]byte{}) {
			t.Fatal("zero key admitted")
		}
		if selected == ([32]byte{}) {
			selected = keys[i]
		}
		if selected != keys[i] {
			t.Fatal("concurrent callers obtained different keys")
		}
	}
	if selected == ([32]byte{}) {
		t.Fatal("no caller acquired state")
	}
	for range 3 {
		key, err := loadScopeKey(t.Context(), dir)
		if err != nil || key != selected {
			t.Fatal("stable key was not retained", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "scope-key.bin"))
	if err != nil || !bytes.Equal(data, selected[:]) {
		t.Fatal("wrong persisted key")
	}
	for _, name := range []string{"scope-key.bin", "scope-key.lock"} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Fatal("state mode is not private", name)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatal("ephemeral write artifacts remain")
	}
}

func TestScopeKeyCancellationAndHeldLock(t *testing.T) {
	dir := filepath.Join(scopeDirectory(t), "not-created")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if key, err := loadScopeKey(ctx, dir); !errors.Is(err, context.Canceled) || key != ([32]byte{}) {
		t.Fatal("cancellation was lost")
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled caller created state")
	}
	store, err := privatefs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := store.TryLock("scope-key.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	started := time.Now()
	if key, err := loadScopeKey(t.Context(), dir); !errors.Is(err, ErrState) || !errors.Is(err, privatefs.ErrLocked) || key != ([32]byte{}) {
		t.Fatal("held lock did not report explicit busy state", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("lock acquisition blocked")
	}
	if _, err := os.Lstat(filepath.Join(dir, "scope-key.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("busy caller created a key")
	}
	if _, err := loadScopeKey(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatal("busy state replaced cancellation")
	}
}

func TestScopeKeyRefusesMalformedExistingData(t *testing.T) {
	for _, data := range [][]byte{nil, make([]byte, 32), bytes.Repeat([]byte{7}, 31), bytes.Repeat([]byte{9}, 33)} {
		dir := scopeDirectory(t)
		path := filepath.Join(dir, "scope-key.bin")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if key, err := loadScopeKey(t.Context(), dir); !errors.Is(err, ErrState) || key != ([32]byte{}) {
			t.Fatal("malformed key was accepted")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, data) {
			t.Fatal("malformed state was replaced")
		}
	}
}

func TestScopeKeyRefusesUnsafeFilesAndDirectories(t *testing.T) {
	for _, name := range []string{"scope-key.bin", "scope-key.lock"} {
		for _, kind := range []string{"symlink", "hardlink", "public", "directory"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				dir := scopeDirectory(t)
				path := filepath.Join(dir, name)
				data := []byte{}
				if name == "scope-key.bin" {
					data = bytes.Repeat([]byte{37}, 32)
				}
				outside := filepath.Join(scopeDirectory(t), "owned-sentinel")
				if err := os.WriteFile(outside, data, 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(outside, path)
				case "hardlink":
					err = os.Link(outside, path)
				case "public":
					err = os.WriteFile(path, data, 0644)
					if err == nil {
						err = os.Chmod(path, 0644)
					}
				case "directory":
					err = os.Mkdir(path, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
				if key, err := loadScopeKey(t.Context(), dir); !errors.Is(err, ErrState) || key != ([32]byte{}) {
					t.Fatal("unsafe state admitted")
				}
				after, err := os.ReadFile(outside)
				if err != nil || !bytes.Equal(after, data) {
					t.Fatal("external sentinel changed")
				}
			})
		}
	}
	dir := scopeDirectory(t)
	link := filepath.Join(scopeDirectory(t), "state-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadScopeKey(t.Context(), link); !errors.Is(err, ErrState) {
		t.Fatal("directory link admitted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadScopeKey(t.Context(), dir); !errors.Is(err, ErrState) {
		t.Fatal("public directory admitted")
	}
}

func TestScopeKeyRejectsReplacedOwnership(t *testing.T) {
	for _, replacement := range []string{"directory", "lock", "key"} {
		t.Run(replacement, func(t *testing.T) {
			dir := scopeDirectory(t)
			key, err := loadScopeKey(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			root, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			store, err := privatefs.New(dir)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := store.TryLock("scope-key.lock")
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if !scopeOwnerUnchanged(dir, root, lock) {
				t.Fatal("newly acquired ownership was not valid")
			}
			path := filepath.Join(dir, "scope-key.bin")
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch replacement {
			case "directory":
				if err := os.Rename(dir, dir+"-retired"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			case "lock":
				path := filepath.Join(dir, "scope-key.lock")
				if err := os.Rename(path, path+"-retired"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "key":
				if err := os.Rename(path, path+"-retired"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, key[:], 0600); err != nil {
					t.Fatal(err)
				}
			}
			if replacement == "key" {
				after, err := os.Lstat(path)
				if err != nil || scopeFileUnchanged(before, after) {
					t.Fatal("a replacement key inode retained read ownership")
				}
			} else if scopeOwnerUnchanged(dir, root, lock) {
				t.Fatal("replaced path retained the original lock ownership")
			}
		})
	}
}

func identityFixture() KiroInfo {
	account := sha256.Sum256([]byte("independent-account"))
	return KiroInfo{Executable: "/fixture/bin/kiro-cli", Helper: "/fixture/bin/kiro-cli-chat", Version: "2.21.1", ProfileScope: hex.EncodeToString(account[:])}
}

func TestLaunchIdentitySeparatesPersistentScopesAndOneLaunchOverrides(t *testing.T) {
	key, info := [32]byte{43}, identityFixture()
	base, err := makeLaunchIdentity(key, "/fixture/home", info, "fixture-model", "high")
	if err != nil {
		t.Fatal(err)
	}
	if base.Executable != info.Executable || base.Version != info.Version || base.InitialModel != "fixture-model" || base.InitialEffort != "high" {
		t.Fatal("identity changed declared configuration")
	}
	if _, err := base.Digest(); err != nil {
		t.Fatal(err)
	}
	again, err := makeLaunchIdentity(key, "/fixture/home/", info, "fixture-model", "high")
	if err != nil || again != base {
		t.Fatal("normalized home is unstable")
	}
	for _, change := range []string{"key", "home", "account", "version", "executable"} {
		changedKey, changedInfo, home := key, info, "/fixture/home"
		switch change {
		case "key":
			changedKey[1] = 1
		case "home":
			home = "/fixture/other-home"
		case "account":
			changedInfo.ProfileScope = strings.Repeat("a", 64)
		case "version":
			changedInfo.Version = "2.21.2"
		case "executable":
			changedInfo.Executable = "/fixture/other/kiro-cli"
		}
		other, err := makeLaunchIdentity(changedKey, home, changedInfo, "fixture-model", "high")
		if err != nil {
			t.Fatal(err)
		}
		first, _ := base.Digest()
		second, _ := other.Digest()
		if first == second {
			t.Fatal("configuration change reused identity", change)
		}
		if (change == "key" || change == "home" || change == "account") && base.ProfileDigest == other.ProfileDigest {
			t.Fatal("profile identity omitted authority", change)
		}
	}
	without, err := makeLaunchIdentity(key, "/fixture/home", info, "", "")
	if err != nil || without.ProfileDigest != base.ProfileDigest || without.AgentDigest != base.AgentDigest || without.CapabilitiesDigest != base.CapabilitiesDigest {
		t.Fatal("one-launch options polluted stable policy identity")
	}
	first, _ := base.Digest()
	second, _ := without.Digest()
	if first == second {
		t.Fatal("catalog identity lost one-launch options")
	}
	models, err := catalog.New([]catalog.Backend{{ID: "fixture-model"}}, "fixture-model")
	if err != nil {
		t.Fatal(err)
	}
	dir := scopeDirectory(t)
	if err := catalog.SaveLastModel(dir, base, "fixture-model", true); err != nil {
		t.Fatal(err)
	}
	model, found, err := catalog.LoadLastModel(dir, without, models)
	if err != nil || !found || model != "fixture-model" {
		t.Fatal("preference did not survive removal of launch overrides")
	}
}

func TestLaunchIdentityRejectsInvalidAuthority(t *testing.T) {
	for _, bad := range []string{"zero-key", "relative-home", "control-home", "relative-executable", "control-executable", "empty-version", "control-version", "bad-account", "upper-account", "model-control", "bad-effort"} {
		key, info, home, model, effort := [32]byte{43}, identityFixture(), "/fixture/home", "", ""
		switch bad {
		case "zero-key":
			key = [32]byte{}
		case "relative-home":
			home = "relative"
		case "control-home":
			home = "/fixture/\nprivate"
		case "relative-executable":
			info.Executable = "kiro-cli"
		case "control-executable":
			info.Executable = "/fixture/\x00private"
		case "empty-version":
			info.Version = ""
		case "control-version":
			info.Version = "2.21.1\nprivate"
		case "bad-account":
			info.ProfileScope = "private-account"
		case "upper-account":
			info.ProfileScope = strings.Repeat("A", 64)
		case "model-control":
			model = "fixture\nprivate"
		case "bad-effort":
			effort = "unexpected"
		}
		if _, err := makeLaunchIdentity(key, home, info, model, effort); !errors.Is(err, ErrState) {
			t.Fatal("invalid authority admitted", bad)
		}
	}
}
