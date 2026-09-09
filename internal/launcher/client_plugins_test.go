package launcher_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dax-kiro-proxy/internal/launcher"
)

func TestClientPluginSeedRetainsOwnedSource(t *testing.T) {
	cfg := profileConfig(t)
	seed := filepath.Join(cfg.Home, ".claude", "plugins")
	if os.MkdirAll(seed, 0700) != nil {
		t.Fatal("cannot create owned plugin fixture")
	}
	source := filepath.Join(seed, "owned-fixture")
	original := []byte("independent plugin source")
	writeSettings(t, source, original)
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if envMap(p.Command().Environment)["CLAUDE_CODE_PLUGIN_SEED_DIR"] != seed {
		t.Fatal("existing plugin source was not supplied through the read-only seed contract")
	}
	if p.Close() != nil {
		t.Fatal("plugin profile cleanup failed")
	}
	if after, err := os.ReadFile(source); err != nil || !bytes.Equal(after, original) {
		t.Fatal("plugin source changed or was removed")
	}
}

func TestClientPluginSeedAbsentOrUnsafeSource(t *testing.T) {
	for _, kind := range []string{"absent", "file", "link", "writable", "separator"} {
		t.Run(kind, func(t *testing.T) {
			cfg := profileConfig(t)
			if kind == "separator" {
				cfg.Home = filepath.Join(cfg.Home, "owned:home")
			}
			parent := filepath.Join(cfg.Home, ".claude")
			seed := filepath.Join(parent, "plugins")
			if os.MkdirAll(parent, 0700) != nil {
				t.Fatal("cannot create owned plugin fixture parent")
			}
			switch kind {
			case "file":
				writeSettings(t, seed, []byte(`{}`))
			case "link":
				if os.Symlink(parent, seed) != nil {
					t.Fatal("cannot prepare owned link")
				}
			case "writable", "separator":
				if os.Mkdir(seed, 0700) != nil {
					t.Fatal("cannot prepare owned directory")
				}
				if kind == "writable" && os.Chmod(seed, 0777) != nil {
					t.Fatal("cannot prepare unsafe mode")
				}
			}
			p, err := launcher.PrepareClient(cfg)
			if p != nil {
				defer p.Close()
			}
			if kind == "absent" {
				if err != nil || envMap(p.Command().Environment)["CLAUDE_CODE_PLUGIN_SEED_DIR"] != "" {
					t.Fatal("missing source created an unintended seed")
				}
				if _, err := os.Lstat(seed); !os.IsNotExist(err) {
					t.Fatal("missing source was created")
				}
			} else if !errors.Is(err, launcher.ErrSettings) {
				t.Fatal("unsafe or ambiguous seed source accepted")
			}
		})
	}
}
