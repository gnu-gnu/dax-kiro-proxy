package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

func TestClaudeClientMCPSources(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for owned MCP configuration controls; no model calls")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-client-assets-")
	if err != nil {
		t.Fatal("cannot create owned asset root")
	}
	defer os.RemoveAll(root)
	home, project, observations := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "observations")
	for _, dir := range []string{home, filepath.Join(home, ".claude"), project, observations, filepath.Join(root, "tmp")} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot create owned asset directories")
		}
	}
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	settings := filepath.Join(home, ".claude", "settings.json")
	if os.WriteFile(settings, []byte("{\"enabledMcpjsonServers\":[\"dax-owned-project\"]}\n"), 0600) != nil {
		t.Fatal("cannot create owned source settings")
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	config := launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: launcher.SupportedClientVersion, Model: "claude-dax-asset-fixture", GatewayURL: "http://127.0.0.1:1", ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}}
	p, err := launcher.PrepareClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	prepared := p.Command()
	natural := prepared
	natural.Args = nil
	natural.Environment = nil
	for _, entry := range prepared.Environment {
		if !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") {
			natural.Environment = append(natural.Environment, entry)
		}
	}
	run := func(t *testing.T, command childproc.Command, args ...string) childproc.Result {
		t.Helper()
		command.Args = append(append([]string(nil), command.Args...), args...)
		result, err := runner.Run(ctx, command)
		if err != nil {
			t.Fatalf("owned client configuration command failed: exit=%d, stdout_bytes=%d", result.ExitCode, len(result.Stdout))
		}
		return result
	}
	version := run(t, natural, "--version")
	if strings.TrimSpace(string(version.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified client version")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	buildEnv := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(key); value != "" {
			buildEnv = append(buildEnv, key+"="+value)
		}
	}
	peer := filepath.Join(root, "client-asset-peer")
	if _, err := runner.Run(ctx, childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: buildEnv, Args: []string{"build", "-o", peer, "./testdata/clientassets"}}); err != nil {
		t.Fatal("cannot build independent MCP fixture")
	}
	for _, scope := range []string{"user", "local", "project"} {
		run(t, natural, "mcp", "add", "--scope", scope, "dax-owned-"+scope, "--", peer, scope, observations)
	}
	protected := []string{settings, filepath.Join(home, ".claude.json"), filepath.Join(project, ".mcp.json")}
	before := make([][]byte, len(protected))
	for i, path := range protected {
		before[i] = boundedAssetFile(t, path)
	}
	for _, tc := range []struct {
		name     string
		command  childproc.Command
		expected map[string]int
	}{
		{"natural", natural, map[string]int{"user": 1, "local": 1, "project": 1}},
		{"prepared", prepared, map[string]int{"user": 2, "local": 2, "project": 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "prepared" {
				fresh, err := launcher.PrepareClient(config)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if fresh.Close() != nil {
						t.Error("fresh profile cleanup failed")
					}
				}()
				tc.command = fresh.Command()
			}
			result := run(t, tc.command, "mcp", "list")
			seen := map[string]bool{}
			for _, scope := range []string{"user", "local", "project"} {
				seen[scope] = bytes.Contains(result.Stdout, []byte("dax-owned-"+scope))
				started, initialized, called := inspectClientAssetProcesses(t, filepath.Join(observations, scope))
				t.Logf("scope=%s, named=%v, starts=%d, initializations=%d, calls=%d", scope, seen[scope], started, initialized, called)
				if started != tc.expected[scope] || initialized != started || called != 0 {
					t.Error("MCP source observation did not match the expected active control")
				}
			}
			for i, path := range protected {
				after := boundedAssetFile(t, path)
				changed := !bytes.Equal(before[i], after)
				if tc.name == "natural" && i == 1 {
					t.Logf("natural_global_config_changed=%v", changed)
					before[i] = after
				} else if changed {
					t.Errorf("owned source changed: index=%d", i)
				}
			}
		})
		if t.Failed() {
			return
		}
	}
	// Disabling declarations and resolving equal names stay native client decisions. Observe
	// processes, rather than assuming a listed name means that its server actually connected.
	counts := map[string]int{"user": 2, "local": 2, "project": 2}
	checkPair := func(name string, increments map[string]int) {
		t.Helper()
		for i, path := range protected {
			before[i] = boundedAssetFile(t, path)
		}
		for _, candidate := range []bool{false, true} {
			kind := "natural"
			if candidate {
				kind = "prepared"
			}
			t.Run(name+"/"+kind, func(t *testing.T) {
				command := natural
				if candidate {
					fresh, err := launcher.PrepareClient(config)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if fresh.Close() != nil {
							t.Error("fresh policy profile cleanup failed")
						}
					}()
					command = fresh.Command()
				}
				run(t, command, "mcp", "list")
				for _, scope := range []string{"user", "local", "project"} {
					counts[scope] += increments[scope]
					started, initialized, called := inspectClientAssetProcesses(t, filepath.Join(observations, scope))
					t.Logf("scope=%s, starts=%d, initializations=%d, calls=%d", scope, started, initialized, called)
					if started != counts[scope] || initialized != started || called != 0 {
						t.Error("native MCP policy or precedence did not match the active control")
					}
				}
				for i, path := range protected {
					after := boundedAssetFile(t, path)
					if !candidate && i == 1 {
						before[i] = after
					} else if !bytes.Equal(before[i], after) {
						t.Errorf("policy source changed: index=%d", i)
					}
				}
			})
			if t.Failed() {
				t.FailNow()
			}
		}
	}
	globalPath := filepath.Join(home, ".claude.json")
	setDisabled := func(names []string) {
		t.Helper()
		var global map[string]any
		if json.Unmarshal(boundedAssetFile(t, globalPath), &global) != nil {
			t.Fatal("invalid owned global fixture")
		}
		projects, ok := global["projects"].(map[string]any)
		if !ok {
			t.Fatal("public writer did not establish local scope")
		}
		current, ok := projects[project].(map[string]any)
		if !ok {
			t.Fatal("public writer did not establish current project")
		}
		current["disabledMcpServers"] = names
		data, err := json.Marshal(global)
		if err != nil || os.WriteFile(globalPath, data, 0600) != nil {
			t.Fatal("cannot seed documented project toggle")
		}
	}
	setDisabled([]string{"dax-owned-user", "dax-owned-project"})
	checkPair("disabled", map[string]int{"local": 1})
	setDisabled([]string{})
	checkPair("reenabled", map[string]int{"user": 1, "local": 1, "project": 1})
	if os.WriteFile(settings, []byte(`{"enabledMcpjsonServers":["dax-owned-project"],"disabledMcpjsonServers":["dax-owned-project"]}`), 0600) != nil {
		t.Fatal("cannot seed documented project refusal")
	}
	checkPair("project_refused", map[string]int{"user": 1, "local": 1})
	if os.WriteFile(settings, []byte(`{"enabledMcpjsonServers":["dax-owned-project","dax-owned-shared"]}`), 0600) != nil {
		t.Fatal("cannot approve owned precedence fixture")
	}
	for _, scope := range []string{"user", "project", "local"} {
		run(t, natural, "mcp", "add", "--scope", scope, "dax-owned-shared", "--", peer, scope, observations)
	}
	checkPair("local_precedence", map[string]int{"user": 1, "local": 2, "project": 1})
	run(t, natural, "mcp", "remove", "--scope", "local", "dax-owned-shared")
	checkPair("project_precedence", map[string]int{"user": 1, "local": 1, "project": 2})
	run(t, natural, "mcp", "remove", "--scope", "project", "dax-owned-shared")
	checkPair("user_fallback", map[string]int{"user": 2, "local": 1, "project": 1})
	// Public writer commands establish the private global-file location independently. These
	// declarations are never queried or executed and cannot change the original source files.
	run(t, prepared, "mcp", "add", "--scope", "user", "dax-runtime-check", "--", "/usr/bin/false")
	run(t, prepared, "mcp", "add", "--scope", "local", "dax-runtime-local", "--", "/usr/bin/false")
	profileDir := ""
	for _, entry := range prepared.Environment {
		if value, ok := strings.CutPrefix(entry, "CLAUDE_CONFIG_DIR="); ok {
			profileDir = value
		}
	}
	var written struct {
		Servers  map[string]json.RawMessage `json:"mcpServers"`
		Projects map[string]struct {
			Servers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	if json.Unmarshal(boundedAssetFile(t, filepath.Join(profileDir, ".claude.json")), &written) != nil || written.Servers["dax-runtime-check"] == nil || written.Projects[project].Servers["dax-runtime-local"] == nil {
		t.Fatal("private MCP writer location or scope shape changed")
	}
	for i, path := range protected {
		if !bytes.Equal(before[i], boundedAssetFile(t, path)) {
			t.Error("private MCP writer modified source settings")
		}
	}
	t.Log("private_global_writer_verified=true, source_settings_unchanged=true")
	if err := p.Close(); err != nil {
		t.Error("profile cleanup failed")
	}
}

func boundedAssetFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("owned asset is missing")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		t.Fatal("owned asset exceeds observation bound")
	}
	return data
}

func inspectClientAssetProcesses(t *testing.T, path string) (started, initialized, called int) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(boundedAssetFile(t, path))), "\n") {
		var pid int
		var event string
		if n, _ := fmt.Sscanf(line, "%d %s", &pid, &event); n != 2 || pid <= 1 {
			t.Fatal("invalid owned process record")
		}
		if !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			t.Error("MCP process survived client command")
		}
		switch event {
		case "started":
			started++
		case "initialized":
			initialized++
		case "called":
			called++
		case "listed", "held":
		default:
			t.Fatal("unknown owned lifecycle label")
		}
	}
	return
}
