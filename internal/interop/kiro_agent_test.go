package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	command.Args = []string{"agent", "validate", "--path", agent.Path}
	valid, err := runner.Run(t.Context(), command)
	if err != nil || valid.ExitCode != 0 {
		t.Fatalf("literal relay alias candidate syntax failed: %v", err)
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
	if os.WriteFile(agent.Path, malformed, 0600) != nil {
		t.Fatal("cannot write negative syntax control")
	}
	command.Args = []string{"agent", "validate", "--path", agent.Path}
	invalid, err := runner.Run(t.Context(), command)
	if err != nil && (!errors.Is(err, childproc.ErrExit) || errors.Is(err, childproc.ErrCleanup) || invalid.ExitCode <= 0) {
		t.Fatalf("negative syntax observation did not finish cleanly: %v", err)
	}
	t.Logf("version=%s, candidate_exit=%d, invalid_tool_list_exit=%d, exit_distinguishes_control=%v, execution_restriction_verified=%v", launcher.SupportedKiroVersion, valid.ExitCode, invalid.ExitCode, invalid.ExitCode != valid.ExitCode, agent.ExecutionVerified)
}
