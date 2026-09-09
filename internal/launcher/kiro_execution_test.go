package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/session"
	"dax-kiro-proxy/internal/toolregistry"
)

type executionFixtureValidator struct{}

func (executionFixtureValidator) Check(context.Context, []byte) error            { return nil }
func (executionFixtureValidator) Validate(context.Context, []byte, []byte) error { return nil }

func TestPreparedKiroExecutionOwnsConfigurationAndExactRelay(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("the current development policy is measured on macOS arm64")
	}
	opts := startupOptions(t)
	info := identityFixture()
	info.Executable, info.Helper, info.Version = filepath.Join(filepath.Dir(opts.ProxyExecutable), "kiro-cli"), filepath.Join(filepath.Dir(opts.ProxyExecutable), "kiro-cli-chat"), SupportedKiroVersion
	execution, err := PrepareKiroExecution(t.Context(), KiroExecutionConfig{Installation: info, Home: opts.Home, Project: opts.Project, RuntimeDirectory: opts.RuntimeParent})
	if err != nil || execution.Prepare == nil {
		t.Fatal("verified development configuration was not prepared", err)
	}
	if execution.Process.Executable != info.Executable || execution.Process.Directory != opts.Project || !reflect.DeepEqual(execution.Process.Args, []string{"acp", "--agent-engine", "v2"}) || execution.Process.Auth == nil {
		t.Fatal("pinned process, workspace or authentication adapter was lost")
	}
	env := map[string]string{}
	for _, field := range execution.Process.Environment {
		key, value, _ := strings.Cut(field, "=")
		if _, duplicate := env[key]; duplicate {
			t.Fatal("duplicate process environment")
		}
		env[key] = value
	}
	if len(env) != 6 || env["HOME"] != opts.Home || !strings.HasPrefix(env["KIRO_HOME"], opts.RuntimeParent+"/") || !strings.HasPrefix(env["TMPDIR"], opts.RuntimeParent+"/") {
		t.Fatal("configuration or credentials inherited from the parent environment")
	}
	settings := filepath.Join(env["KIRO_HOME"], "settings", "cli.json")
	data, err := os.ReadFile(settings)
	if err != nil || string(data) != `{"chat.disableInheritingDefaultResources":true}` {
		t.Fatal("default-resource suppression missing")
	}
	for _, path := range []string{env["KIRO_HOME"], filepath.Dir(settings), env["TMPDIR"]} {
		stat, err := os.Lstat(path)
		if err != nil || !stat.IsDir() || stat.Mode().Perm() != 0700 {
			t.Fatal("execution directory is not private")
		}
	}
	for _, tool := range []string{"", "Read", "Bash"} {
		var declarations []json.RawMessage
		if tool != "" {
			declarations = []json.RawMessage{json.RawMessage(`{"name":"` + tool + `","input_schema":{"type":"object"}}`)}
		}
		registry, err := toolregistry.Build(t.Context(), declarations, nil, executionFixtureValidator{})
		if err != nil {
			t.Fatal(err)
		}
		input := session.LaunchInput{Registry: registry, RelayExecutable: opts.ProxyExecutable, RelayConfig: filepath.Join(opts.RuntimeParent, "owned-relay.json")}
		owned, err := execution.Prepare(t.Context(), input)
		if err != nil || owned.Cleanup == nil || !owned.RelayAtLaunch || len(owned.Args) != 2 || owned.Args[0] != "--agent" || owned.Directory == opts.Project {
			t.Fatal("launch did not isolate its exact agent", err)
		}
		path := filepath.Join(owned.Directory, ".kiro", "agents", owned.Args[1]+".json")
		data, err := os.ReadFile(path)
		var agent struct {
			Tools, AllowedTools, Resources []string
			IncludeMcpJson                 bool
			Hooks                          map[string]any
			MCPServers                     map[string]struct {
				Command string
				Args    []string
				Env     map[string]string
			}
		}
		if err != nil || json.Unmarshal(data, &agent) != nil || len(agent.Tools) != len(declarations) || !reflect.DeepEqual(agent.Tools, agent.AllowedTools) || len(agent.Resources) != 0 || len(agent.Hooks) != 0 || agent.IncludeMcpJson || len(agent.MCPServers) != 1 {
			t.Fatal("agent broadened execution or resource authority")
		}
		if tool != "" && agent.Tools[0] != "@dax_session/"+registry.Tools()[0].Alias {
			t.Fatal("tool aliases crossed a registry")
		}
		relay := agent.MCPServers["dax_session"]
		if relay.Command != input.RelayExecutable || !reflect.DeepEqual(relay.Args, []string{"relay", "--config", input.RelayConfig}) || len(relay.Env) != 0 {
			t.Fatal("relay ownership was not preserved")
		}
		if owned.Cleanup() != nil || owned.Cleanup() != nil {
			t.Fatal("prepared cleanup was not repeatable")
		}
		if _, err := os.Lstat(owned.Directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("prepared agent survived cleanup")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := execution.Prepare(ctx, session.LaunchInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled preparation created a new owner")
	}
	if os.WriteFile(settings, []byte(`{}`), 0600) != nil {
		t.Fatal("cannot change owned negative control")
	}
	registry, err := toolregistry.Build(t.Context(), nil, nil, executionFixtureValidator{})
	if err != nil {
		t.Fatal(err)
	}
	valid := session.LaunchInput{Registry: registry, RelayExecutable: opts.ProxyExecutable, RelayConfig: filepath.Join(opts.RuntimeParent, "owned-relay.json")}
	if _, err := execution.Prepare(t.Context(), valid); !errors.Is(err, ErrRuntime) {
		t.Fatal("changed suppression settings admitted a process")
	}
}

func TestPreparedKiroExecutionRejectsUnknownPolicyWithoutArtifacts(t *testing.T) {
	for _, version := range []string{"", "2.21.1", "2.21.3"} {
		opts := startupOptions(t)
		info := identityFixture()
		info.Version = version
		if _, err := PrepareKiroExecution(t.Context(), KiroExecutionConfig{Installation: info, Home: opts.Home, Project: opts.Project, RuntimeDirectory: opts.RuntimeParent}); !errors.Is(err, ErrPolicyUnverified) {
			t.Fatal("unknown version acquired the development policy")
		}
		entries, _ := os.ReadDir(opts.RuntimeParent)
		if len(entries) != 0 {
			t.Fatal("rejected policy created runtime state")
		}
	}
}
