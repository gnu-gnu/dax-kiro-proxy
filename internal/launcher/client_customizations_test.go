package launcher_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"dax-kiro-proxy/internal/launcher"
)

func TestPersonalAssetsSnapshotPreservesSourceAndNativeScope(t *testing.T) {
	cfg := profileConfig(t)
	base := filepath.Join(cfg.Home, ".claude")
	original := map[string]string{
		"skills/owned/SKILL.md":          "---\nname: owned\n---\nIndependent skill.\n",
		"skills/owned/scripts/helper.sh": "#!/bin/sh\nprintf 'independent fixture\\n'\n",
		"skills/owned/reference.bin":     string([]byte{0, 1, 255}),
		"commands/nested/owned.md":       "Independent command.\n",
		"commands/quotes '$()\n.md":      "---\r\nname: 독립\r\n---\r\nNo normalization",
		"agents/nested/owned.md":         "---\nname: owned\n---\nIndependent agent.\n",
		"output-styles/owned.md":         "---\nname: Owned output\nkeep-coding-instructions: true\n---\nIndependent style.\n",
	}
	for path, data := range original {
		path = filepath.Join(base, path)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil {
			t.Fatal("cannot prepare owned asset directory")
		}
		writeSettings(t, path, []byte(data))
		if strings.HasSuffix(path, ".sh") && os.Chmod(path, 0755) != nil {
			t.Fatal("cannot set owned executable mode")
		}
	}
	// Data outside the selected trees must not be migrated into private client state.
	writeSettings(t, filepath.Join(base, "unrelated-secret"), []byte("independent excluded sentinel"))
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for path, data := range original {
		copyPath := filepath.Join(p.Path(), "client", path)
		copy, err := os.ReadFile(copyPath)
		if err != nil || !bytes.Equal(copy, []byte(data)) {
			t.Fatal("personal asset not retained at native user scope")
		}
		info, err := os.Lstat(copyPath)
		want := os.FileMode(0600)
		if strings.HasSuffix(path, ".sh") {
			want = 0700
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != want {
			t.Fatal("snapshot type or permission mismatch")
		}
		if os.WriteFile(copyPath, []byte("independent private mutation"), 0600) != nil {
			t.Fatal("cannot mutate private copy")
		}
		if source, err := os.ReadFile(filepath.Join(base, path)); err != nil || !bytes.Equal(source, []byte(data)) {
			t.Fatal("private mutation reached source")
		}
	}
	if _, err := os.Lstat(filepath.Join(p.Path(), "client", "unrelated-secret")); !os.IsNotExist(err) {
		t.Fatal("unrelated asset copied")
	}
	for _, arg := range p.Command().Args {
		if arg == "--add-dir" || arg == "--agents" || arg == "--plugin-dir" {
			t.Fatal("native source scope replaced by command-line customization")
		}
	}
	if p.Close() != nil || p.Close() != nil {
		t.Fatal("snapshot cleanup failed")
	}
	for path, data := range original {
		if source, err := os.ReadFile(filepath.Join(base, path)); err != nil || !bytes.Equal(source, []byte(data)) {
			t.Fatal("cleanup changed source")
		}
	}
}

func TestPersonalAssetsRejectUnsafeAndOversizedSources(t *testing.T) {
	for _, treeName := range []string{"skills", "rules", "output-styles"} {
		t.Run(treeName, func(t *testing.T) {
			for _, kind := range []string{"root-link", "tree-link", "file-link", "hard-link", "fifo", "writable-directory", "writable-file", "large-file", "total-bytes", "entries", "depth", "tree-file"} {
				t.Run(kind, func(t *testing.T) {
					cfg := profileConfig(t)
					base := filepath.Join(cfg.Home, ".claude")
					tree := filepath.Join(base, treeName)
					if os.MkdirAll(tree, 0700) != nil {
						t.Fatal("cannot create independent source")
					}
					path := filepath.Join(tree, "owned")
					writeSettings(t, path, []byte("independent control"))
					switch kind {
					case "root-link", "tree-link":
						link := base
						if kind == "tree-link" {
							link = tree
						}
						if os.Rename(link, link+"-target") != nil || os.Symlink(link+"-target", link) != nil {
							t.Fatal("cannot create source directory link")
						}
					case "file-link":
						if os.Rename(path, path+"-target") != nil || os.Symlink(path+"-target", path) != nil {
							t.Fatal("cannot create source file link")
						}
					case "hard-link":
						if os.Link(path, path+"-other") != nil {
							t.Fatal("cannot create owned hard link")
						}
					case "fifo":
						if os.Remove(path) != nil || syscall.Mkfifo(path, 0600) != nil {
							t.Fatal("cannot create independent FIFO")
						}
					case "writable-directory":
						if os.Chmod(tree, 0777) != nil {
							t.Fatal("cannot set source directory mode")
						}
					case "writable-file":
						if os.Chmod(path, 0666) != nil {
							t.Fatal("cannot set source file mode")
						}
					case "large-file":
						if os.Truncate(path, launcher.MaxClientAssetBytes+1) != nil {
							t.Fatal("cannot create oversize sparse fixture")
						}
					case "total-bytes":
						for i := 0; i <= launcher.MaxClientAssetsBytes/launcher.MaxClientAssetBytes; i++ {
							p := filepath.Join(tree, fmt.Sprintf("f-%04d", i))
							writeSettings(t, p, nil)
							if os.Truncate(p, launcher.MaxClientAssetBytes) != nil {
								t.Fatal("cannot size independent aggregate fixture")
							}
						}
					case "entries":
						for i := 0; i < launcher.MaxClientAssetEntries; i++ {
							writeSettings(t, filepath.Join(tree, fmt.Sprintf("f-%04d", i)), nil)
						}
					case "depth":
						deep := tree
						for i := 0; i < launcher.MaxClientAssetDepth; i++ {
							deep = filepath.Join(deep, "nested")
						}
						if os.MkdirAll(deep, 0700) != nil {
							t.Fatal("cannot create deep independent tree")
						}
					case "tree-file":
						if os.RemoveAll(tree) != nil {
							t.Fatal("cannot remove owned tree")
						}
						writeSettings(t, tree, []byte("not a directory"))
					}
					p, err := launcher.PrepareClient(cfg)
					if p != nil {
						p.Close()
					}
					if !errors.Is(err, launcher.ErrSettings) {
						t.Fatal("unsafe or unbounded personal source accepted")
					}
					entries, err := filepath.Glob(filepath.Join(cfg.RuntimeParent, "dax-runtime-*"))
					if err != nil || len(entries) != 0 {
						t.Fatal("rejected source left runtime artifacts")
					}
				})
			}
		})
	}
}

func TestPersonalAssetsRemainAbsentAndRefreshOnlyOnPreparation(t *testing.T) {
	cfg := profileConfig(t)
	first, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	base := filepath.Join(cfg.Home, ".claude")
	if _, err := os.Lstat(base); !os.IsNotExist(err) {
		t.Fatal("preparation created source configuration")
	}
	path := filepath.Join(base, "commands", "owned.md")
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		t.Fatal("cannot create owned command source")
	}
	writeSettings(t, path, []byte("first independent revision"))
	second, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	writeSettings(t, path, []byte("second independent revision"))
	if _, err := os.Lstat(filepath.Join(first.Path(), "client", "commands")); !os.IsNotExist(err) {
		t.Fatal("absent source appeared in an older profile")
	}
	if data, err := os.ReadFile(filepath.Join(second.Path(), "client", "commands", "owned.md")); err != nil || string(data) != "first independent revision" {
		t.Fatal("running profile followed a source change")
	}
	third, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if data, err := os.ReadFile(filepath.Join(third.Path(), "client", "commands", "owned.md")); err != nil || string(data) != "second independent revision" {
		t.Fatal("fresh profile missed source revision")
	}
}

func TestPersonalRulesKeepOriginalPathsWithoutCleanupWrites(t *testing.T) {
	cfg := profileConfig(t)
	base := filepath.Join(cfg.Home, ".claude")
	if os.MkdirAll(filepath.Join(base, "rules"), 0700) != nil {
		t.Fatal("cannot create owned rule directory")
	}
	writeSettings(t, filepath.Join(base, "CLAUDE.md"), []byte("@../independent-context.md\n"))
	writeSettings(t, filepath.Join(base, "rules", "conditional.md"), []byte("---\npaths: [\"owned/*.go\"]\n---\nIndependent condition.\n"))
	writeSettings(t, filepath.Join(cfg.Home, "independent-context.md"), []byte("independent outside import"))
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, name := range []string{"rules"} {
		target, err := os.Readlink(filepath.Join(p.Path(), "client", name))
		if err != nil || target != filepath.Join(base, name) {
			t.Fatal("instruction source path was relocated or omitted")
		}
	}
	if _, err := os.Lstat(filepath.Join(p.Path(), "client", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("unverified personal memory adapter was activated")
	}
	if p.Close() != nil || p.Close() != nil {
		t.Fatal("instruction reference cleanup failed")
	}
	if data, err := os.ReadFile(filepath.Join(base, "CLAUDE.md")); err != nil || string(data) != "@../independent-context.md\n" {
		t.Fatal("instruction source changed")
	}
	if data, err := os.ReadFile(filepath.Join(base, "rules", "conditional.md")); err != nil || string(data) != "---\npaths: [\"owned/*.go\"]\n---\nIndependent condition.\n" {
		t.Fatal("rule source changed or removed through reference")
	}
}
