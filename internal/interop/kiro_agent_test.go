package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/toolregistry"
)

type syntaxFixtureValidator struct{}

func (syntaxFixtureValidator) Check(context.Context, []byte) error            { return nil }
func (syntaxFixtureValidator) Validate(context.Context, []byte, []byte) error { return nil }

func TestKiroAgentValidationExitStatus(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for candidate syntax only; no model request")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-agent-syntax-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	runner, err := childproc.New(childproc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	command := childproc.Command{Executable: executable, Directory: root, Environment: []string{"HOME=" + os.Getenv("HOME"), "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + root, "TERM=dumb", "LANG=en_US.UTF-8"}, Args: []string{"--version"}}
	version, err := runner.Run(t.Context(), command)
	if err != nil || strings.TrimSpace(string(version.Stdout)) != "kiro-cli "+launcher.SupportedKiroVersion {
		t.Fatal("unverified Kiro version")
	}
	registry, err := toolregistry.Build(t.Context(), []json.RawMessage{json.RawMessage(`{"name":"syntax_only_action","input_schema":{"type":"object"}}`)}, nil, syntaxFixtureValidator{})
	if err != nil {
		t.Fatal(err)
	}
	// Syntax validation has no reason to execute an MCP program. Even if this CLI does, the named
	// public executable simply exits unsuccessfully and cannot act as a client tool or read secrets.
	noEffect := "/usr/bin/false"
	if info, err := os.Stat(noEffect); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatal("system no-effect syntax fixture is unavailable")
	}
	agent, err := launcher.WriteCandidateAgent(launcher.AgentConfig{Directory: root, Registry: registry, RelayExecutable: noEffect, RelayConfig: filepath.Join(root, "unused-control.json")})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(agent.Path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		t.Fatal("candidate JSON is invalid")
	}
	fields["tools"] = json.RawMessage(`42`)
	malformed, _ := json.Marshal(fields)
	for _, name := range []string{"kiro-cli", "kiro-cli-chat"} {
		command.Executable = filepath.Join(filepath.Dir(executable), name)
		command.Args = []string{"--version"}
		version, err := runner.Run(t.Context(), command)
		if err != nil || strings.TrimSpace(string(version.Stdout)) != name+" "+launcher.SupportedKiroVersion {
			t.Fatal("candidate probe requires both pinned public binaries")
		}
		command.Args = []string{"agent", "--help"}
		help, err := runner.Run(t.Context(), command)
		if err != nil {
			t.Fatalf("public agent help did not finish: %v", err)
		}
		commands := []string{}
		for _, subcommand := range []string{"create", "validate", "list", "edit", "delete", "show"} {
			if regexp.MustCompile(`(?m)^\s+` + subcommand + `\s`).Match(help.Stdout) {
				commands = append(commands, subcommand)
			}
		}
		t.Logf("binary=%s, advertised_agent_commands=%v", name, commands)
		results := []childproc.Result{}
		for index, candidate := range [][]byte{data, malformed} {
			if os.WriteFile(agent.Path, candidate, 0600) != nil {
				t.Fatal("cannot write owned validation fixture")
			}
			command.Args = []string{"agent", "validate", "--path", agent.Path}
			// Only this synthetic validation probe combines stderr into the runner's bounded capture.
			// Positional arguments stay quoted; no path/config text becomes shell code. Only fixed
			// markers and byte/line counts leave the test, never the captured diagnostic prose.
			observed := command
			observed.Executable = "/bin/sh"
			observed.Args = append([]string{"-c", `exec "$@" 2>&1`, "dax-validation-probe", command.Executable}, command.Args...)
			result, err := runner.Run(t.Context(), observed)
			if err != nil && (!errors.Is(err, childproc.ErrExit) || errors.Is(err, childproc.ErrCleanup) || result.ExitCode < 0) {
				t.Fatalf("candidate validation command did not finish cleanly: %v", err)
			}
			results = append(results, result)
			markers := []string{}
			for _, marker := range []string{"invalid type", "expected a sequence", "unknown field", "unknown tool", "validation failed", "validation succeeded", "successfully validated", "valid configuration", "not found", "error"} {
				if strings.Contains(strings.ToLower(string(result.Stdout)), marker) {
					markers = append(markers, marker)
				}
			}
			t.Logf("binary=%s, negative_control=%v, exit=%d, bounded_output_bytes=%d, output_shape=%v, fixed_validation_markers=%v", name, index == 1, result.ExitCode, len(result.Stdout), kiroOutputShape(result.Stdout), markers)
		}
		t.Logf("binary=%s, version=%s, exit_distinguishes_control=%v, execution_restriction_verified=%v", name, launcher.SupportedKiroVersion, results[0].ExitCode != results[1].ExitCode, agent.ExecutionVerified)
	}
}
