package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

// Observe the pinned helper's writer/list distinction using a completely synthetic HOME. The
// workspace-file cases are negative controls: absence from this list cannot prove non-inheritance
// during ACP session creation. No real account HOME, --force, connect or session is involved.
// Both disabled and enabled controls use false, so an unexpected server launch has no effect.
func TestKiroOwnedMCPWriterInventoryObservation(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		state := "disabled"
		if !disabled {
			state = "enabled"
		}
		t.Run(state, func(t *testing.T) {
			for _, agentScoped := range []bool{false, true} {
				target := "workspace-file"
				if agentScoped {
					target = "named-agent"
				}
				t.Run(target, func(t *testing.T) { observeOwnedMCPWriter(t, disabled, agentScoped) })
			}
		})
	}
}

func observeOwnedMCPWriter(t *testing.T, disabled, agentScoped bool) {
	t.Helper()
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for a fully owned effect-free MCP writer observation")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("MCP writer observation requires an absolute executable")
	}
	root := t.TempDir()
	home, work, configuration, scratch := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "config"), filepath.Join(root, "tmp")
	for _, directory := range []string{home, work, configuration, scratch} {
		if os.Mkdir(directory, 0700) != nil {
			t.Fatal("cannot create owned MCP writer directories")
		}
	}
	const agentName = "dax-mcp-owned-agent"
	if agentScoped {
		agentDirectory := filepath.Join(work, ".kiro", "agents")
		data, marshalErr := json.Marshal(map[string]any{"name": agentName, "tools": []string{}, "allowedTools": []string{}, "mcpServers": map[string]any{}, "resources": []string{}, "hooks": map[string]any{}})
		if marshalErr != nil || os.MkdirAll(agentDirectory, 0700) != nil || os.WriteFile(filepath.Join(agentDirectory, agentName+".json"), data, 0600) != nil {
			t.Fatal("cannot create independent empty MCP target agent")
		}
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, MaxOutputBytes: 64 << 10, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal("cannot create bounded MCP writer runner")
	}
	t.Cleanup(func() {
		runner.Close()
		if runner.Active() != 0 {
			t.Error("MCP writer process ownership was not released")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	env := []string{"HOME=" + home, "KIRO_HOME=" + configuration, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"}
	run := func(label, binary string, args []string, limit time.Duration) (childproc.Result, error) {
		t.Helper()
		commandContext, commandCancel := context.WithTimeout(ctx, limit)
		defer commandCancel()
		result, runErr := runner.Run(commandContext, childproc.Command{Executable: "/bin/sh", Directory: work, Environment: env,
			Args: append([]string{"-c", `exec "$@" 2>&1`, "dax-mcp-owned-writer", binary}, args...)})
		groupGone := result.PID == 0 || errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
		cleanupJoined := groupGone && runner.Active() == 0 && !errors.Is(runErr, childproc.ErrCleanup)
		authenticationMarker := strings.Contains(strings.ToLower(string(result.Stdout)), "not logged in")
		t.Logf("command=%s, synthetic_home=true, exit=%d, output_bytes=%d, authentication_marker=%v, failure=%s, cleanup_joined=%v", label, result.ExitCode, len(result.Stdout), authenticationMarker, kiroHomeProbeFailure(runErr), cleanupJoined)
		if !cleanupJoined {
			t.Fatal("MCP writer process group survived cleanup")
		}
		return result, runErr
	}
	for _, binary := range []struct{ label, path, name string }{
		{"main-version", executable, "kiro-cli"},
		{"helper-version", filepath.Join(filepath.Dir(executable), "kiro-cli-chat"), "kiro-cli-chat"},
	} {
		result, runErr := run(binary.label, binary.path, []string{"--version"}, 5*time.Second)
		if runErr != nil || !launcher.CompatibleKiroOutput(binary.name, result.Stdout) {
			t.Fatal("MCP writer observation requires both pinned binaries")
		}
	}
	selectedBinary := filepath.Join(filepath.Dir(executable), "kiro-cli-chat")
	help, helpErr := run("mcp-add-help", selectedBinary, []string{"mcp", "add", "--help"}, 5*time.Second)
	if helpErr != nil {
		t.Fatal("MCP add help did not complete")
	}
	for _, flag := range []string{"--scope", "--name", "--command", "--disabled", "--agent"} {
		if !strings.Contains(string(help.Stdout), flag) {
			t.Fatal("public MCP add help did not advertise the required disabled-server options")
		}
	}
	const marker = "dax-mcp-owned-writer"
	arguments := []string{"mcp", "add", "--name", marker, "--command", "/usr/bin/false"}
	expectedPath := "work/.kiro/settings/mcp.json"
	if agentScoped {
		arguments = append(arguments, "--agent", agentName)
		expectedPath = "work/.kiro/agents/" + agentName + ".json"
	} else {
		arguments = append(arguments, "--scope", "workspace")
	}
	if disabled {
		arguments = append(arguments, "--disabled")
	}
	result, runErr := run("mcp-add-workspace", selectedBinary, arguments, 15*time.Second)
	matches := inspectOwnedMCPWriterFiles(t, root, marker, expectedPath, disabled)
	if runErr != nil || result.ExitCode != 0 || matches != 1 {
		t.Fatal("public MCP writer did not establish one owned configuration location")
	}
	listed, listErr := run("mcp-list-workspace", selectedBinary, []string{"mcp", "list", "workspace"}, 15*time.Second)
	markerPresent := regexp.MustCompile(`(^|[^a-zA-Z0-9_-])` + marker + `([^a-zA-Z0-9_-]|$)`).Match(listed.Stdout)
	lower := strings.ToLower(string(listed.Stdout))
	t.Logf("agent_scoped=%v, disabled=%v, own_marker_present=%v, own_agent_present=%v, no_servers_marker=%v, workspace_marker=%v, warning_marker=%v, error_marker=%v", agentScoped, disabled, markerPresent, strings.Contains(lower, agentName), strings.Contains(lower, "no mcp servers"), strings.Contains(lower, "workspace"), strings.Contains(lower, "warning"), strings.Contains(lower, "error"))
	if listErr != nil || listed.ExitCode != 0 || markerPresent != agentScoped || strings.Contains(lower, agentName) != agentScoped {
		t.Fatal("public MCP inventory changed its observed named-agent versus workspace-file distinction")
	}
	t.Logf("owned_writer_location_verified=true, own_server_listed=%v, inherited_mcp_exclusion_verified=false, execution_restriction_verified=false, session_created=false, prompt_sent=false", markerPresent)
}

// Inspect only files newly produced inside this probe. The rooted, nonfollowing, finite read never
// searches an installed client's source or a real user directory, and reports only matching paths.
func inspectOwnedMCPWriterFiles(t *testing.T, directory, marker, expectedPath string, disabled bool) int {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal("cannot inspect owned MCP writer root")
	}
	defer root.Close()
	visited, jsonFiles, totalBytes, matches := 0, 0, 0, 0
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > 256 || strings.Count(path, "/") > 10 {
			return errors.New("owned MCP writer tree exceeded its observation bound")
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".json") {
			return nil
		}
		jsonFiles++
		if jsonFiles > 32 {
			return errors.New("owned MCP JSON count exceeded its observation bound")
		}
		file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return errors.New("owned MCP JSON is not a bounded regular file")
		}
		data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
		totalBytes += len(data)
		if err != nil || len(data) > 64<<10 || totalBytes > 1<<20 {
			return errors.New("owned MCP JSON bytes exceeded the observation bound")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil {
			return nil
		}
		var servers map[string]json.RawMessage
		if json.Unmarshal(fields["mcpServers"], &servers) != nil || servers[marker] == nil {
			return nil
		}
		if len(path) > 256 || !regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`).MatchString(path) {
			return errors.New("owned MCP configuration path cannot be safely reported")
		}
		var server map[string]json.RawMessage
		valid := json.Unmarshal(servers[marker], &server) == nil
		var storedDisabled bool
		if raw, present := server["disabled"]; present {
			valid = valid && (string(raw) == "true" || string(raw) == "false") && json.Unmarshal(raw, &storedDisabled) == nil
		}
		valid = valid && path == expectedPath && storedDisabled == disabled && string(server["command"]) == `"/usr/bin/false"`
		t.Logf("owned_relative_path=%s, file_bytes=%d, disabled_field_present=%v, disabled_value=%v, declared_state_and_false_command_confirmed=%v", path, len(data), server["disabled"] != nil, storedDisabled, valid)
		if valid {
			matches++
		}
		return nil
	})
	if err != nil {
		t.Fatal("owned MCP writer inspection failed within its path/file/byte bounds")
	}
	t.Logf("owned_entries=%d, json_files=%d, json_bytes=%d, matching_configuration_files=%d", visited, jsonFiles, totalBytes, matches)
	return matches
}

// Both documented mcp.json locations are independently seeded negative controls. The pinned list
// did not display any of them; passing this observation does not prove their exclusion in ACP:
// https://kiro.dev/docs/mcp/configuration/
// https://kiro.dev/docs/mcp/registry/
// No status/connect, agent, ACP, prompt, login or setter command is used. Every authored server's
// executable is false, so an unexpected invocation cannot perform a client tool.
func TestKiroOwnedMCPFileInventoryObservation(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for owned MCP configuration inventory; no session or prompt")
	}
	home := os.Getenv("HOME")
	if !filepath.IsAbs(executable) || !filepath.IsAbs(home) {
		t.Fatal("MCP inventory requires absolute executable and account HOME")
	}
	root := t.TempDir()
	work, first, second, scratch := filepath.Join(root, "work"), filepath.Join(root, "first"), filepath.Join(root, "second"), filepath.Join(root, "tmp")
	for _, directory := range []string{work, first, second, scratch} {
		if os.Mkdir(directory, 0700) != nil {
			t.Fatal("cannot create owned MCP inventory directories")
		}
	}
	markers := []string{"dax-mcp-a-flat", "dax-mcp-a-settings", "dax-mcp-b-flat", "dax-mcp-b-settings", "dax-mcp-project-flat", "dax-mcp-project-settings"}
	for scope, directory := range []string{first, second, filepath.Join(work, ".kiro")} {
		if os.MkdirAll(filepath.Join(directory, "settings"), 0700) != nil {
			t.Fatal("cannot create owned MCP configuration directory")
		}
		for location, relative := range []string{"mcp.json", "settings/mcp.json"} {
			data, err := json.Marshal(map[string]any{"mcpServers": map[string]any{markers[2*scope+location]: map[string]any{
				"command": "/usr/bin/false", "args": []string{}, "env": map[string]string{},
			}}})
			if err != nil || os.WriteFile(filepath.Join(directory, relative), data, 0600) != nil {
				t.Fatal("cannot write independent MCP inventory fixture")
			}
		}
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, MaxOutputBytes: 64 << 10, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal("cannot create bounded MCP inventory runner")
	}
	t.Cleanup(func() {
		runner.Close()
		if runner.Active() != 0 {
			t.Error("MCP inventory process ownership was not released")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Second)
	defer cancel()
	environment := []string{"HOME=" + home, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"}
	run := func(label, binary, directory string, args []string, inventory bool) childproc.Result {
		t.Helper()
		limit := 5 * time.Second
		if inventory {
			if len(args) != 3 || args[0] != "mcp" || args[1] != "list" || args[2] != "global" && args[2] != "workspace" {
				t.Fatal("MCP inventory command escaped its read-only scope allowlist")
			}
			limit = 15 * time.Second
		}
		commandContext, commandCancel := context.WithTimeout(ctx, limit)
		defer commandCancel()
		// Capture only this finite command's bounded streams. All paths remain positional
		// arguments to a fixed shell program and are never evaluated as shell syntax.
		command := childproc.Command{Executable: "/bin/sh", Directory: work, Environment: append(append([]string(nil), environment...), "KIRO_HOME="+directory),
			Args: append([]string{"-c", `exec "$@" 2>&1`, "dax-mcp-inventory", binary}, args...)}
		result, runErr := runner.Run(commandContext, command)
		groupGone := result.PID == 0 || errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
		cleanupJoined := groupGone && runner.Active() == 0 && !errors.Is(runErr, childproc.ErrCleanup)
		t.Logf("command=%s, exit=%d, output_bytes=%d, failure=%s, cleanup_joined=%v", label, result.ExitCode, len(result.Stdout), kiroHomeProbeFailure(runErr), cleanupJoined)
		if !cleanupJoined {
			t.Fatal("MCP inventory process group survived cleanup")
		}
		if runErr != nil || result.ExitCode != 0 {
			t.Fatal("MCP inventory command did not complete; scope discovery remains unverified")
		}
		return result
	}
	for _, binary := range []struct{ label, path, name string }{
		{"main-version", executable, "kiro-cli"},
		{"helper-version", filepath.Join(filepath.Dir(executable), "kiro-cli-chat"), "kiro-cli-chat"},
	} {
		result := run(binary.label, binary.path, first, []string{"--version"}, false)
		if !launcher.CompatibleKiroOutput(binary.name, result.Stdout) {
			t.Fatal("MCP inventory requires both pinned public binaries")
		}
	}
	help := run("mcp-list-help", executable, first, []string{"mcp", "list", "--help"}, false)
	scopes := strings.Contains(string(help.Stdout), "[SCOPE]") && strings.Contains(string(help.Stdout), "default, workspace, global")
	if !scopes {
		t.Fatal("public MCP list help did not advertise the expected scopes")
	}
	for _, selected := range []struct {
		label, directory, scope string
	}{
		{"first-global", first, "global"},
		{"second-global", second, "global"},
		{"first-workspace", first, "workspace"},
		{"second-workspace", second, "workspace"},
	} {
		result := run(selected.label, executable, selected.directory, []string{"mcp", "list", selected.scope}, true)
		present := make([]bool, len(markers))
		anyPresent := false
		for index, marker := range markers {
			present[index] = regexp.MustCompile(`(^|[^a-zA-Z0-9_-])` + regexp.QuoteMeta(marker) + `([^a-zA-Z0-9_-]|$)`).Match(result.Stdout)
			anyPresent = anyPresent || present[index]
		}
		t.Logf("command=%s, a_flat=%v, a_settings=%v, b_flat=%v, b_settings=%v, project_flat=%v, project_settings=%v", selected.label, present[0], present[1], present[2], present[3], present[4], present[5])
		if anyPresent {
			t.Error("public MCP inventory changed its observed omission of these standalone file markers")
		}
	}
	t.Log("owned_mcp_scope_distinction_verified=false, exclusive_inventory_verified=false, agent_merge_verified=false, execution_restriction_verified=false, session_created=false, prompt_sent=false")
}
