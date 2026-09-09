package launcher_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestClientPluginRegistrationsStayPrivateAndIndependent(t *testing.T) {
	cfg := profileConfig(t)
	seed := filepath.Join(cfg.Home, ".claude", "plugins")
	if os.MkdirAll(seed, 0700) != nil {
		t.Fatal("cannot prepare owned plugin registry")
	}
	sources := map[string][]byte{
		"installed_plugins.json":  []byte(`{"version":2,"plugins":{"owned@fixture":[{"scope":"user","installPath":"/owned/cache/plugin","version":"1"}]}}`),
		"known_marketplaces.json": []byte(`{"fixture":{"source":{"source":"directory","path":"/owned/market"},"privateFixtureField":"independent-value"}}`),
	}
	for name, data := range sources {
		writeSettings(t, filepath.Join(seed, name), data)
	}
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for name, original := range sources {
		path := filepath.Join(p.Path(), "client", "plugins", name)
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatal("native plugin registration was not retained")
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("plugin registry is not owner-only")
		}
		writeSettings(t, path, []byte(`{}`))
		if data, err := os.ReadFile(filepath.Join(seed, name)); err != nil || !bytes.Equal(data, original) {
			t.Fatal("private plugin mutation reached original registration")
		}
	}
	if p.Close() != nil {
		t.Error("plugin registry cleanup failed")
	}
}

func TestClientPluginRegistrationRejectsUnsafeSources(t *testing.T) {
	for _, name := range []string{"installed_plugins.json", "known_marketplaces.json"} {
		for _, mode := range []string{"array", "null", "malformed", "duplicate", "oversized", "link", "writable"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				cfg := profileConfig(t)
				seed := filepath.Join(cfg.Home, ".claude", "plugins")
				if os.MkdirAll(seed, 0700) != nil {
					t.Fatal("cannot prepare owned plugin registry")
				}
				path := filepath.Join(seed, name)
				data := []byte(`{}`)
				switch mode {
				case "array":
					data = []byte(`[]`)
				case "null":
					data = []byte(`null`)
				case "malformed":
					data = []byte(`{"plugins":`)
				case "duplicate":
					data = []byte(`{"plugins":{},"plugins":{}}`)
				case "oversized":
					data = []byte(strings.Repeat(" ", launcher.MaxSettingsBytes) + `{}`)
				}
				writeSettings(t, path, data)
				if mode == "link" {
					if os.Rename(path, path+".target") != nil || os.Symlink(path+".target", path) != nil {
						t.Fatal("cannot prepare owned registry link")
					}
				}
				if mode == "writable" && os.Chmod(path, 0666) != nil {
					t.Fatal("cannot prepare unsafe registry mode")
				}
				p, err := launcher.PrepareClient(cfg)
				if p != nil {
					p.Close()
				}
				if !errors.Is(err, launcher.ErrSettings) {
					t.Error("unsafe plugin registry was accepted")
				}
			})
		}
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
