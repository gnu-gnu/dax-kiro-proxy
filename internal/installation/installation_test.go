package installation_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"dax-kiro-proxy/internal/installation"
)

const executableName = "dax-kiro-proxy"

func fixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "home", ".local", "bin")
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("Independent first executable bytes.\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return root, bin, source
}

func installed(t *testing.T, bin string) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(filepath.Join(bin, executableName))
	if err != nil {
		t.Fatal("installed entry point does not resolve")
	}
	return path
}

type entry struct {
	Mode os.FileMode
	Data [32]byte
	Link string
}

func snapshot(t *testing.T, root string) map[string]entry {
	t.Helper()
	out := make(map[string]entry)
	err := filepath.WalkDir(root, func(path string, item fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		value := entry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value.Data = sha256.Sum256(data)
		} else if info.Mode()&os.ModeSymlink != 0 {
			value.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		out[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal("cannot inspect owned installation fixture")
	}
	return out
}

func TestInstallReplaceUninstallPreserveOtherFiles(t *testing.T) {
	root, bin, source := fixture(t)
	protected := []string{"home/.claude/settings.json", "home/.claude.json", "home/.kiro/settings/cli.json", "home/.dax-kiro-proxy/preferences", "project/.claude/settings.local.json", "home/.local/bin/unrelated"}
	for _, name := range protected {
		path := filepath.Join(root, name)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, []byte("Independent protected settings."), 0600) != nil {
			t.Fatal("cannot prepare protected source")
		}
	}
	before := snapshot(t, root)
	if err := installation.Install(t.Context(), bin, source, false); err != nil {
		t.Fatal(err)
	}
	first := installed(t, bin)
	data, err := os.ReadFile(first)
	if err != nil || !bytes.Equal(data, []byte("Independent first executable bytes.\n")) {
		t.Fatal("installed bytes differ")
	}
	if info, err := os.Stat(first); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("installed executable mode differs")
	}
	for _, notice := range []string{"runtime/go-bsd.txt", "runtime/jsonschema-apache-2.0.txt", "runtime/unicode-cldr-32.txt", "runtime/unicode-v3.txt", "reference/json-schema-spec-current.txt"} {
		path := filepath.Join(filepath.Dir(first), "notices", notice)
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm() != 0600 {
			t.Fatal("installed notice is absent or unsafe")
		}
	}
	if err := installation.Install(t.Context(), bin, source, false); !errors.Is(err, installation.ErrExists) {
		t.Fatal("existing installation replaced without force", err)
	}
	if os.WriteFile(source, []byte("Independent replacement executable bytes.\n"), 0700) != nil {
		t.Fatal("cannot revise owned source")
	}
	if err := installation.Install(t.Context(), bin, source, true); err != nil {
		t.Fatal(err)
	}
	second := installed(t, bin)
	if first == second {
		t.Fatal("replacement modified executable in place")
	}
	if _, err := os.Lstat(filepath.Dir(first)); !os.IsNotExist(err) {
		t.Fatal("retired generation remains")
	}
	if data, err := os.ReadFile(second); err != nil || string(data) != "Independent replacement executable bytes.\n" {
		t.Fatal("replacement bytes differ")
	}
	if err := installation.Uninstall(t.Context(), bin); err != nil {
		t.Fatal(err)
	}
	if err := installation.Uninstall(t.Context(), bin); err != nil {
		t.Fatal("repeated uninstall failed", err)
	}
	if _, err := os.Lstat(filepath.Join(bin, executableName)); !os.IsNotExist(err) {
		t.Fatal("public executable remains")
	}
	if _, err := os.Lstat(filepath.Join(bin, ".dax-kiro-proxy-install")); !os.IsNotExist(err) {
		t.Fatal("private installation artifacts remain")
	}
	after := snapshot(t, root)
	for _, name := range protected {
		if before[name] != after[name] {
			t.Fatal("installation changed protected settings or unrelated executable")
		}
	}
}

func TestExecutionLeasesPreventReplacementAndUninstall(t *testing.T) {
	_, bin, source := fixture(t)
	if err := installation.Install(t.Context(), bin, source, false); err != nil {
		t.Fatal(err)
	}
	path := installed(t, bin)
	first, err := installation.Lease(path)
	if err != nil || first == nil {
		t.Fatal("cannot lease installed executable", err)
	}
	defer first.Close()
	second, err := installation.Lease(path)
	if err != nil || second == nil {
		t.Fatal("shared execution lease refused", err)
	}
	defer second.Close()
	before := snapshot(t, bin)
	for _, operation := range []func() error{
		func() error { return installation.Install(t.Context(), bin, source, true) },
		func() error { return installation.Uninstall(t.Context(), bin) },
	} {
		if err := operation(); !errors.Is(err, installation.ErrBusy) {
			t.Fatal("active executable mutation admitted", err)
		}
	}
	if !reflect.DeepEqual(before, snapshot(t, bin)) {
		t.Fatal("busy operation changed installation")
	}
	if first.Close() != nil || first.Close() != nil {
		t.Fatal("repeated lease close failed")
	}
	if err := installation.Uninstall(t.Context(), bin); !errors.Is(err, installation.ErrBusy) {
		t.Fatal("one remaining execution lease did not block removal")
	}
	if second.Close() != nil || installation.Uninstall(t.Context(), bin) != nil {
		t.Fatal("idle installation could not be removed")
	}
}

func TestUnmanagedOrChangedFilesAreNeverOverwrittenOrRemoved(t *testing.T) {
	for _, kind := range []string{"public-file", "public-link", "manager-unknown", "binary-changed", "binary-link", "binary-hardlink", "unexpected-file", "manifest-corrupt", "manifest-duplicate", "current-escape", "marker-missing", "lock-missing", "lock-mode", "manager-mode", "notice-changed", "notice-directory-link", "incomplete-stage", "too-many-generations"} {
		t.Run(kind, func(t *testing.T) {
			root, bin, source := fixture(t)
			if os.MkdirAll(bin, 0700) != nil {
				t.Fatal("cannot prepare bin directory")
			}
			public := filepath.Join(bin, executableName)
			manager := filepath.Join(bin, ".dax-kiro-proxy-install")
			var path string
			if kind != "public-file" && kind != "public-link" && kind != "manager-unknown" {
				if err := installation.Install(t.Context(), bin, source, false); err != nil {
					t.Fatal(err)
				}
				path = installed(t, bin)
			}
			switch kind {
			case "public-file":
				if os.WriteFile(public, []byte("Unrelated owned executable."), 0700) != nil {
					t.Fatal("fixture")
				}
			case "public-link":
				if os.Symlink(source, public) != nil {
					t.Fatal("fixture")
				}
			case "manager-unknown":
				if os.Mkdir(manager, 0700) != nil || os.WriteFile(filepath.Join(manager, "unrelated"), []byte("Unrelated owned data."), 0600) != nil {
					t.Fatal("fixture")
				}
			case "binary-changed":
				if os.WriteFile(path, []byte("Unrelated replacement."), 0700) != nil {
					t.Fatal("fixture")
				}
			case "binary-link":
				if os.Remove(path) != nil || os.Symlink(source, path) != nil {
					t.Fatal("fixture")
				}
			case "binary-hardlink":
				if os.Link(path, filepath.Join(root, "retained-link")) != nil {
					t.Fatal("fixture")
				}
			case "unexpected-file":
				if os.WriteFile(filepath.Join(filepath.Dir(path), "unrelated"), []byte("Keep this file."), 0600) != nil {
					t.Fatal("fixture")
				}
			case "manifest-corrupt":
				if os.WriteFile(filepath.Join(filepath.Dir(path), "manifest.json"), []byte("{"), 0600) != nil {
					t.Fatal("fixture")
				}
			case "manifest-duplicate":
				manifest := filepath.Join(filepath.Dir(path), "manifest.json")
				data, err := os.ReadFile(manifest)
				if err != nil || len(data) < 1 {
					t.Fatal("fixture")
				}
				data = append([]byte("{\"format\":1,"), data[1:]...)
				if os.WriteFile(manifest, data, 0600) != nil {
					t.Fatal("fixture")
				}
			case "marker-missing", "lock-missing":
				name := "format.json"
				if kind == "lock-missing" {
					name = "lock"
				}
				if os.Remove(filepath.Join(manager, name)) != nil {
					t.Fatal("fixture")
				}
			case "lock-mode":
				if os.Chmod(filepath.Join(manager, "lock"), 0666) != nil {
					t.Fatal("fixture")
				}
			case "manager-mode":
				if os.Chmod(manager, 0755) != nil {
					t.Fatal("fixture")
				}
			case "notice-changed":
				if os.WriteFile(filepath.Join(filepath.Dir(path), "notices/runtime/go-bsd.txt"), []byte("Changed notice."), 0600) != nil {
					t.Fatal("fixture")
				}
			case "notice-directory-link":
				dir := filepath.Join(filepath.Dir(path), "notices/runtime")
				if os.Rename(dir, filepath.Join(root, "retained-notices")) != nil || os.Symlink(filepath.Join(root, "retained-notices"), dir) != nil {
					t.Fatal("fixture")
				}
			case "incomplete-stage":
				if os.Mkdir(filepath.Join(manager, "staging-00000000000000000000000000000000"), 0700) != nil {
					t.Fatal("fixture")
				}
			case "too-many-generations":
				for _, id := range []string{"00000000000000000000000000000000", "11111111111111111111111111111111", "22222222222222222222222222222222", "33333333333333333333333333333333", "44444444444444444444444444444444"} {
					if os.Mkdir(filepath.Join(manager, "generation-"+id), 0700) != nil {
						t.Fatal("fixture")
					}
				}
			case "current-escape":
				if os.Remove(filepath.Join(manager, "current")) != nil || os.Symlink(root, filepath.Join(manager, "current")) != nil {
					t.Fatal("fixture")
				}
			}
			before := snapshot(t, root)
			if installation.Install(t.Context(), bin, source, true) == nil || installation.Uninstall(t.Context(), bin) == nil {
				t.Fatal("unmanaged installation accepted")
			}
			if !reflect.DeepEqual(before, snapshot(t, root)) {
				t.Fatal("failed operation changed unowned or modified files")
			}
		})
	}
}

func TestInvalidSourceAndCancellationDoNotCreateInstallation(t *testing.T) {
	for _, kind := range []string{"empty", "large", "link", "fifo", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			root, bin, source := fixture(t)
			ctx := t.Context()
			switch kind {
			case "empty":
				if os.Truncate(source, 0) != nil {
					t.Fatal("fixture")
				}
			case "large":
				if os.Truncate(source, installation.MaxExecutableBytes+1) != nil {
					t.Fatal("fixture")
				}
			case "link":
				if os.Rename(source, source+"-target") != nil || os.Symlink(source+"-target", source) != nil {
					t.Fatal("fixture")
				}
			case "fifo":
				if os.Remove(source) != nil || syscall.Mkfifo(source, 0600) != nil {
					t.Fatal("fixture")
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := installation.Install(ctx, bin, source, false); err == nil {
				t.Fatal("invalid source accepted")
			}
			if _, err := os.Lstat(filepath.Join(root, "home")); !os.IsNotExist(err) {
				t.Fatal("rejected preparation created destination")
			}
		})
	}
}
