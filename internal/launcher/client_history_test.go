package launcher_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/launcher"
)

func TestNativeHistorySurvivesPrivateProfileCleanup(t *testing.T) {
	cfg := profileConfig(t)
	cfg.KeepHistory = true
	const id = "197326ab-1597-4268-a129-426853197ace"
	var paths [2]string
	for i := range 2 {
		if i == 1 {
			cfg.ResumeSession = id
		}
		p, err := launcher.PrepareClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		paths[i] = p.Path()
		data := filepath.Join(envMap(p.Command().Environment)["CLAUDE_CONFIG_DIR"], "projects", "owned-marker")
		if i == 0 {
			if err := os.WriteFile(data, []byte("client-owned data"), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			if b, err := os.ReadFile(data); err != nil || string(b) != "client-owned data" {
				t.Fatal("fresh profile lost native data")
			}
			args := p.Command().Args
			if len(args) != 6 || args[4] != "--resume" || args[5] != id {
				t.Fatal("native resume argument missing")
			}
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if paths[0] == paths[1] {
		t.Fatal("private profile reused")
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("private profile retained")
		}
	}
	if b, err := os.ReadFile(filepath.Join(cfg.Home, ".claude", "projects", "owned-marker")); err != nil || string(b) != "client-owned data" {
		t.Fatal("cleanup removed shared data")
	}
	for _, name := range []string{"settings.json", ".claude.json"} {
		if _, err := os.Lstat(filepath.Join(cfg.Home, ".claude", name)); !os.IsNotExist(err) {
			t.Fatal("persistent configuration created")
		}
	}
}

func TestNativeHistoryRequiresExplicitRetention(t *testing.T) {
	cfg := profileConfig(t)
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := os.Lstat(filepath.Join(cfg.Home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("ordinary launch created native history root")
	}
	for _, id := range []string{"197326ab-1597-4268-a129-426853197ace", "--help", "../outside", strings.Repeat("a", 37), "197326ab-1597-4268-a129-426853197acg"} {
		cfg.ResumeSession = id
		if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrConfig) {
			if p != nil {
				p.Close()
			}
			t.Fatal("resume without retention accepted")
		}
	}
	cfg.KeepHistory = true
	for _, id := range []string{"--help", "../outside", strings.Repeat("a", 37), "197326ab-1597-4268-a129-426853197acg", "197326ab-1597-4268-a129-426853197a--"} {
		cfg.ResumeSession = id
		if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrConfig) {
			if p != nil {
				p.Close()
			}
			t.Fatal("invalid native session accepted")
		}
	}
}

func TestNativeHistoryRejectsUnsafeSourceRoots(t *testing.T) {
	for _, name := range []string{".claude", "projects"} {
		for _, kind := range []string{"symlink", "file", "writable"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				cfg := profileConfig(t)
				cfg.KeepHistory = true
				path := filepath.Join(cfg.Home, ".claude")
				if name == "projects" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(path, name)
				}
				outside := t.TempDir()
				marker := filepath.Join(outside, "keep")
				writeSettings(t, marker, []byte("keep"))
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(outside, path)
				case "file":
					err = os.WriteFile(path, []byte("keep"), 0600)
				case "writable":
					err = os.Mkdir(path, 0700)
					if err == nil {
						err = os.Chmod(path, 0777)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrSettings) {
					if p != nil {
						p.Close()
					}
					t.Fatal("unsafe history source accepted")
				}
				if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
					t.Fatal("outside source changed")
				}
				entries, err := filepath.Glob(filepath.Join(cfg.RuntimeParent, "dax-runtime-*"))
				if err != nil || len(entries) != 0 {
					t.Fatal("rejected profile survived")
				}
			})
		}
	}
}
