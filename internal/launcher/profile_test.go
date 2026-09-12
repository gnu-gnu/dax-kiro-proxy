package launcher_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

func profileConfig(t *testing.T) launcher.ClientConfig {
	t.Helper()
	base := t.TempDir()
	home, project := filepath.Join(base, "home"), filepath.Join(base, "project")
	for _, path := range []string{home, project} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	return launcher.ClientConfig{RuntimeParent: base, Home: home, Project: project,
		Executable: "/fixture/claude", Version: launcher.SupportedClientVersion, Model: "claude-dax-fixture-0123456789abcdef",
		GatewayURL: "http://127.0.0.1:32123", ModelToken: tokens.Model,
		UserSettings: filepath.Join(home, "settings.json"),
		Environment:  []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "TERM=xterm-256color"}}
}
func writeSettings(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func envMap(values []string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		key, val, _ := strings.Cut(value, "=")
		out[key] = val
	}
	return out
}
func TestClientProfileKeepsPoliciesAtUserScopeAndOwnsRouting(t *testing.T) {
	cfg := profileConfig(t)
	original := []byte(`{"permissions":{"defaultMode":"manual","deny":["Read(./private.txt)"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/fixture/hook"}]}]},"spinnerTipsEnabled":false,"env":{"TASK_COLOR":"blue","ANTHROPIC_API_KEY":"synthetic-old-key","CLAUDE_CODE_USE_VERTEX":"1"},"apiKeyHelper":"/fixture/credential-helper","model":"external-default"}`)
	writeSettings(t, cfg.UserSettings, original)
	cfg.Environment = append(cfg.Environment, "ANTHROPIC_API_KEY=synthetic-inherited-key", "CLAUDE_CODE_USE_BEDROCK=1", "AWS_ACCESS_KEY_ID=synthetic", "HTTP_PROXY=http://outside.invalid", "NODE_OPTIONS=--require=untrusted", "UNDECLARED_VALUE=not-inherited")
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	command := p.Command()
	if command.Executable != cfg.Executable || command.Directory != cfg.Project {
		t.Fatal("client working directory or executable changed")
	}
	env := envMap(command.Environment)
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "CLAUDE_CODE_USE_BEDROCK", "NODE_OPTIONS", "UNDECLARED_VALUE"} {
		if _, ok := env[key]; ok {
			t.Fatal("unapproved inherited environment reached client")
		}
	}
	if env["ANTHROPIC_API_KEY"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != cfg.ModelToken || env["ANTHROPIC_BASE_URL"] != cfg.GatewayURL || env["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] != "1" || env["DISABLE_TELEMETRY"] != "1" || env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] != "1" {
		t.Fatal("gateway routing is not host owned")
	}
	if env["HTTP_PROXY"] != "" || env["HOME"] != cfg.Home || env["TERM"] != "xterm-256color" {
		t.Fatal("unsafe proxy or lost terminal context")
	}
	profile := env["CLAUDE_CONFIG_DIR"]
	if !strings.HasPrefix(profile, p.Path()+string(os.PathSeparator)) {
		t.Fatal("client profile escapes runtime")
	}
	var user, source, overlay map[string]json.RawMessage
	data, err := os.ReadFile(filepath.Join(profile, "settings.json"))
	if err != nil || json.Unmarshal(data, &user) != nil || json.Unmarshal(original, &source) != nil {
		t.Fatal("invalid user snapshot")
	}
	for _, key := range []string{"permissions", "hooks", "spinnerTipsEnabled"} {
		var a, b any
		_ = json.Unmarshal(user[key], &a)
		_ = json.Unmarshal(source[key], &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("user policy changed during snapshot")
		}
	}
	if bytes.Contains(data, []byte("synthetic-old-key")) || bytes.Contains(data, []byte("credential-helper")) || user["model"] != nil {
		t.Fatal("old credentials or model copied into isolated settings")
	}
	data, err = os.ReadFile(p.SettingsPath())
	if err != nil || json.Unmarshal(data, &overlay) != nil {
		t.Fatal("invalid host overlay")
	}
	if overlay["permissions"] != nil || overlay["hooks"] != nil || overlay["availableModels"] != nil {
		t.Fatal("host overlay overrides permission/hook or managed model policy")
	}
	if len(command.Args) != 4 || command.Args[0] != "--settings" || command.Args[1] != p.SettingsPath() || command.Args[2] != "--model" || command.Args[3] != cfg.Model {
		t.Fatal("unexpected client flags")
	}
	for _, path := range []string{p.Path(), profile, filepath.Join(profile, "settings.json"), p.SettingsPath()} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("runtime is not owner-only")
		}
	}
	command.Environment[0] = "CHANGED=yes"
	command.Args[0] = "--safe-mode"
	if p.Command().Args[0] != "--settings" || strings.Contains(strings.Join(p.Command().Environment, "\n"), "CHANGED=yes") {
		t.Fatal("caller mutated stored launch contract")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Path()); !os.IsNotExist(err) {
		t.Fatal("runtime survived cleanup")
	}
	after, err := os.ReadFile(cfg.UserSettings)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("source settings changed")
	}
}
func TestClientProfileRejectsUnsafeSettingsAndInvalidLaunches(t *testing.T) {
	for _, kind := range []string{"duplicate", "array", "large", "bad-env", "symlink", "world-writable"} {
		t.Run(kind, func(t *testing.T) {
			cfg := profileConfig(t)
			data := []byte(`{}`)
			switch kind {
			case "duplicate":
				data = []byte(`{"hooks":{},"hooks":{}}`)
			case "array":
				data = []byte(`[]`)
			case "large":
				data = []byte(strings.Repeat(" ", 2<<20) + `{}`)
			case "bad-env":
				data = []byte(`{"env":{"KEY":false}}`)
			}
			writeSettings(t, cfg.UserSettings, data)
			if kind == "symlink" {
				other := cfg.UserSettings + ".link"
				if os.Symlink(cfg.UserSettings, other) != nil {
					t.Fatal("cannot create synthetic link")
				}
				cfg.UserSettings = other
			}
			if kind == "world-writable" {
				if os.Chmod(cfg.UserSettings, 0666) != nil {
					t.Fatal("cannot set synthetic mode")
				}
			}
			if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrSettings) {
				if p != nil {
					p.Close()
				}
				t.Fatal("unsafe settings accepted")
			}
		})
	}
	for _, kind := range []string{"version", "version-malformed", "remote", "credentials", "path", "model", "token", "duplicate-env"} {
		t.Run(kind, func(t *testing.T) {
			cfg := profileConfig(t)
			switch kind {
			case "version":
				cfg.Version = "1.0.0"
			case "version-malformed":
				cfg.Version = "2.1.263-dev"
			case "remote":
				cfg.GatewayURL = "http://gateway.invalid:32123"
			case "credentials":
				cfg.GatewayURL = "http://secret@127.0.0.1:32123"
			case "path":
				cfg.Project = "relative"
			case "model":
				cfg.Model = "sonnet"
			case "token":
				cfg.ModelToken = "short"
			case "duplicate-env":
				cfg.Environment = append(cfg.Environment, "PATH=/duplicate")
			}
			if p, err := launcher.PrepareClient(cfg); !errors.Is(err, launcher.ErrConfig) {
				if p != nil {
					p.Close()
				}
				t.Fatal("invalid launcher config accepted")
			}
		})
	}
}
func TestClientVersionOutputAdmission(t *testing.T) {
	for _, tc := range []struct {
		output  string
		version string
		ok      bool
	}{
		{launcher.SupportedClientVersion + " (Claude Code)\n", launcher.SupportedClientVersion, true},
		{"2.1.268 (Claude Code)", "2.1.268", true},
		{"2.1.267 (Claude Code)", "2.1.267", true},
		{"1.0.0 (Claude Code)", "", false},
		{"3.0.0 (Claude Code)", "", false},
		{"2.1.263", "", false},
		{"2.1.263 (Claude Code) extra", "", false},
		{"2.1.263-dev (Claude Code)", "", false},
		{strings.Repeat("2", 250) + " (Claude Code)", "", false},
	} {
		version, ok := launcher.ClientVersionFromOutput([]byte(tc.output))
		if version != tc.version || ok != tc.ok || launcher.CompatibleClientOutput([]byte(tc.output)) != tc.ok {
			t.Fatalf("output %q: got %q/%v, want %q/%v", tc.output, version, ok, tc.version, tc.ok)
		}
	}
}

func TestMissingSettingsAndConcurrentRuntimeCleanup(t *testing.T) {
	cfg := profileConfig(t)
	p, err := launcher.PrepareClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var done sync.WaitGroup
	for range 8 {
		done.Go(func() {
			if err := p.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	done.Wait()
	if _, err := os.Stat(cfg.UserSettings); !os.IsNotExist(err) {
		t.Fatal("missing source settings were created")
	}
}

func TestRuntimeCleanupDoesNotFollowLinksOrRemoveAReplacedRoot(t *testing.T) {
	for _, replace := range []bool{false, true} {
		cfg := profileConfig(t)
		p, err := launcher.PrepareClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(cfg.Home, "keep.json")
		writeSettings(t, outside, []byte(`{"keep":true}`))
		if !replace {
			if err := os.Symlink(cfg.Home, filepath.Join(p.Path(), "external-link")); err != nil {
				t.Fatal(err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Rename(p.Path(), p.Path()+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(p.Path(), 0700); err != nil {
				t.Fatal(err)
			}
			writeSettings(t, filepath.Join(p.Path(), "replacement.json"), []byte(`{}`))
			if !errors.Is(p.Close(), launcher.ErrRuntime) {
				t.Fatal("replaced runtime was not rejected")
			}
			if _, err := os.Stat(filepath.Join(p.Path(), "replacement.json")); err != nil {
				t.Fatal("cleanup removed replacement data")
			}
		}
		if data, err := os.ReadFile(outside); err != nil || string(data) != `{"keep":true}` {
			t.Fatal("cleanup changed data outside the owned runtime")
		}
	}
}
