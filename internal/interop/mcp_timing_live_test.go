package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/kiroauth"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/privatefs"
)

func TestKiroLiveMCPRequestTimeoutObservation(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("MCP timing model work requires explicit per-run credit approval")
	}
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if !filepath.IsAbs(executable) || os.Getenv("DAX_INTEROP_CLAUDE_BINARY") != "" {
		t.Fatal("the timing observation requires only the pinned Kiro executable")
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("cannot prepare private timing root")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, Timeout: time.Minute, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal("cannot prepare bounded timing builder")
	}
	defer runner.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot identify timing build root")
	}
	peer := filepath.Join(root, "owned-mcp-timing")
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	result, err := runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env, Args: []string{"build", "-o", peer, "./testdata/mcptiming"}})
	if err != nil || result.ExitCode != 0 || runner.Active() != 0 {
		t.Fatal("cannot build independent MCP timing peer")
	}
	// Two sequential episodes, no retries. A failed or inconclusive first case stops the pair.
	for _, timeout := range []int{1500, 8000} {
		if !runMCPTimingEpisode(t, runner, root, executable, peer, timeout) {
			t.Fatal("MCP timing hypothesis was not established; no subsequent prompt is authorized by this invocation")
		}
	}
}

func runMCPTimingEpisode(t *testing.T, runner *childproc.Runner, root, executable, peer string, timeout int) (passed bool) {
	t.Helper()
	base := filepath.Join(root, strconv.Itoa(timeout))
	work, configuration, scratch := filepath.Join(base, "work"), filepath.Join(base, "config"), filepath.Join(base, "tmp")
	for _, path := range []string{filepath.Join(work, ".kiro", "agents"), filepath.Join(configuration, "settings"), scratch} {
		if os.MkdirAll(path, 0700) != nil {
			t.Error("cannot prepare isolated timing directories")
			return false
		}
	}
	alias := "@owned_timing/owned_wait"
	peerConfig, _ := json.Marshal(map[string]any{"GroupFile": filepath.Join(base, "group"), "Witness": filepath.Join(base, "witness"), "Tool": "owned_wait", "DelayMilliseconds": 3000})
	agent, _ := json.Marshal(map[string]any{"name": "owned-mcp-timing", "tools": []string{alias}, "allowedTools": []string{alias}, "resources": []string{}, "hooks": map[string]any{}, "includeMcpJson": false,
		"mcpServers": map[string]any{"owned_timing": map[string]any{"command": peer, "args": []string{filepath.Join(base, "peer.json")}, "env": map[string]string{}, "timeout": timeout}}})
	sources := map[string][]byte{"peer.json": peerConfig, "work/.kiro/agents/owned-mcp-timing.json": agent, "config/settings/cli.json": []byte(`{"chat.disableInheritingDefaultResources":true}`)}
	for name, data := range sources {
		if os.WriteFile(filepath.Join(base, name), data, 0600) != nil {
			t.Error("cannot write owned timing configuration")
			return false
		}
	}
	environment := []string{"HOME=" + os.Getenv("HOME"), "KIRO_HOME=" + configuration, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "LANG=en_US.UTF-8", "TERM=dumb"}
	ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
	defer cancel()
	for _, name := range []string{"kiro-cli", "kiro-cli-chat"} {
		limit, stop := context.WithTimeout(ctx, 5*time.Second)
		result, err := runner.Run(limit, childproc.Command{Executable: filepath.Join(filepath.Dir(executable), name), Directory: work, Args: []string{"--version"}, Environment: environment})
		stop()
		version, valid := launcher.KiroVersionFromOutput(name, result.Stdout)
		if err != nil || result.ExitCode != 0 || !valid || version != "2.21.3" || result.PID <= 1 || !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
			t.Error("timing observation requires the measured Kiro 2.21.3 pair and joined preflight")
			return false
		}
	}
	setup, stopSetup := context.WithTimeout(ctx, 20*time.Second)
	defer stopSetup()
	client, err := acp.Start(setup, acp.Config{Executable: executable, Directory: work, Args: []string{"acp", "--agent", "owned-mcp-timing", "--agent-engine", "v2"}, Environment: environment,
		ClientInfo: acp.Info{Name: "independent-mcp-timing", Version: "1"}, Auth: kiroauth.Classifier{}, Limits: acp.Limits{FrameBytes: 256 << 10, EventBytes: 2 << 20, RequestTimeout: 45 * time.Second}})
	if err != nil {
		t.Errorf("timing ACP initialization failed before model work: failure=%s cleanup_failed=%v", kiroSetupFailure(err), errors.Is(err, acp.ErrCleanup))
		return false
	}
	probe := new(mcpTimingProbe)
	stage := "setup"
	defer func() {
		joined := client.Close() == nil && errors.Is(syscall.Kill(-client.PID(), 0), syscall.ESRCH)
		store, openErr := privatefs.Open(base)
		unchanged := openErr == nil
		var marks []mcpTimingMark
		if openErr == nil {
			for name, expected := range sources {
				sourceDir, dirErr := privatefs.Open(filepath.Join(base, filepath.Dir(name)))
				if dirErr != nil {
					unchanged = false
					continue
				}
				data, readErr := sourceDir.Read(filepath.Base(name), 8192)
				unchanged = unchanged && readErr == nil && bytes.Equal(data, expected)
			}
			data, readErr := store.Read("witness", 32<<10)
			if readErr == nil {
				marks, _ = timingMarks(data, client.PID())
			}
		}
		for _, mark := range marks {
			joined = joined && errors.Is(syscall.Kill(mark.PID, 0), syscall.ESRCH)
		}
		elapsed, established := timingEstablished(marks, probe, timeout == 1500)
		counts := map[string]int{}
		for _, m := range marks {
			counts[m.Kind]++
		}
		removed := false
		if joined && os.RemoveAll(base) == nil {
			_, statErr := os.Lstat(base)
			removed = errors.Is(statErr, os.ErrNotExist)
		}
		passed = passed && joined && unchanged && established && removed
		t.Logf("configured_ms=%d peer_delay_ms=3000 stage=%s prompt_sent=%v completed=%v acp_calls=%d terminal=%s terminal_after_call_ms=%d notifications=%d notification_bytes=%d peer_events=%v joined=%v config_unchanged=%v artifacts_removed=%v established=%v", timeout, stage, probe.PromptSent, probe.Completed, probe.Calls, probe.Terminal, elapsed, probe.Notifications, probe.Bytes, counts, joined, unchanged, removed, passed)
	}()
	sources["group"] = []byte(strconv.Itoa(client.PID()))
	if os.WriteFile(filepath.Join(base, "group"), sources["group"], 0600) != nil {
		return false
	}
	inventory, err := readOnlyToolsInventoryAfter(setup, client, work, 5*time.Second, inventoryPrerequisite{ObservedTools: map[string]string{"owned_timing": "owned_wait"}, WaitForAllMCP: true})
	if err != nil || !inventory.Success || inventory.DataSizes["tools"] != 1 || !inventory.ToolMatches["owned_timing"] {
		return false
	}
	var models struct{ AvailableModels []struct{ ModelID string } }
	if json.Unmarshal(inventory.sessionModels, &models) != nil || len(models.AvailableModels) > 128 {
		return false
	}
	auto := 0
	for _, model := range models.AvailableModels {
		if model.ModelID == "auto" {
			auto++
		}
	}
	if auto != 1 {
		return false
	}
	stage = "prompt"
	if probe.exercise(ctx, client, inventory.session, 45*time.Second, 4*time.Second) != nil {
		return false
	}
	stage = "observation"
	return true
}
