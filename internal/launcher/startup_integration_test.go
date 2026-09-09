package launcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/session"
)

type startupBinaries struct{ proxy, kiro, client, acp string }

func buildStartupBinaries(t *testing.T) startupBinaries {
	t.Helper()
	dir := t.TempDir()
	bins := startupBinaries{filepath.Join(dir, "proxy"), filepath.Join(dir, "kiro-cli"), filepath.Join(dir, "claude"), filepath.Join(dir, "acp")}
	for _, build := range [][2]string{{bins.proxy, "../../cmd/dax-kiro-proxy"}, {bins.kiro, "./testdata/preflight"}, {bins.client, "./testdata/client"}, {bins.acp, "../acp/testdata/fake"}} {
		cmd := exec.Command("go", "build", "-o", build[0], build[1])
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("independent build failed: %v\n%s", err, output)
		}
	}
	if err := os.Link(bins.kiro, filepath.Join(dir, "kiro-cli-chat")); err != nil {
		t.Fatal(err)
	}
	return bins
}
func configureStartupBinaries(t *testing.T, opts *LaunchOptions, bins startupBinaries) {
	t.Helper()
	opts.ProxyExecutable, opts.KiroExecutable, opts.ClientExecutable = bins.proxy, bins.kiro, bins.client
	opts.UserSettings = filepath.Join(opts.Home, "source-settings.json")
	if err := os.WriteFile(opts.UserSettings, []byte(`{"permissions":{"defaultMode":"manual"},"hooks":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestStartupOwnedCompositionWithIndependentExecutables(t *testing.T) {
	bins := buildStartupBinaries(t)
	for _, mode := range []string{"catalog-text", "tools-complete", "tools-exit", "tools-hold"} {
		t.Run(mode, func(t *testing.T) {
			opts := startupOptions(t)
			configureStartupBinaries(t, &opts, bins)
			opts.Environment = []string{"PATH=/usr/bin:/bin", "TERM=" + mode}
			source, _ := os.ReadFile(opts.UserSettings)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			services := productionStartup()
			var prepared, cleaned atomic.Int32
			services.policy = func(ctx context.Context, options LaunchOptions, info KiroInfo) (launchPolicy, error) {
				backendMode := "chat"
				if strings.HasPrefix(mode, "tools-") {
					backendMode = "chat-tools-launch"
				}
				return launchPolicy{process: acp.Config{Executable: bins.acp, Directory: options.Project, Args: []string{backendMode}, ClientInfo: acp.Info{Name: "dax-independent-startup", Version: "1"}}, prepare: func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
					root, err := os.MkdirTemp(options.RuntimeParent, "dax-fixture-policy-")
					if err != nil {
						return session.LaunchResources{}, err
					}
					prepared.Add(1)
					owned := session.LaunchResources{Directory: root, RelayAtLaunch: true, Cleanup: func() error { cleaned.Add(1); return os.RemoveAll(root) }}
					if backendMode == "chat-tools-launch" {
						path := filepath.Join(root, "relay.json")
						data, _ := json.Marshal(map[string]any{"name": "independent-startup-relay", "command": input.RelayExecutable, "args": []string{"relay", "--config", input.RelayConfig}, "env": []any{}})
						owned.Args = []string{path}
						if err = os.WriteFile(path, data, 0600); err != nil {
							return owned, err
						}
					}
					return owned, ctx.Err()
				}}, ctx.Err()
			}
			var observed *ClientRunConfig
			services.client = func(ctx context.Context, cfg ClientRunConfig) (ClientRunResult, error) {
				observed = &cfg
				return RunClient(ctx, cfg)
			}
			stdin, send, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			output, stdout, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range []*os.File{stdin, send, output, stdout, null} {
				t.Cleanup(func() { file.Close() })
			}
			type outcome struct {
				result LaunchResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := start(ctx, opts, childproc.AttachedIO{Stdin: stdin, Stdout: stdout, Stderr: null}, false, services)
				done <- outcome{result, err}
			}()
			output.SetReadDeadline(time.Now().Add(10 * time.Second))
			line, readErr := bufio.NewReaderSize(output, 4096).ReadBytes('\n')
			var observation struct {
				ClientPID                int `json:"clientPID"`
				Endpoint, Runtime, State string
			}
			if readErr != nil || len(line) > 4096 || json.Unmarshal(line, &observation) != nil || observation.ClientPID <= 0 {
				cancel()
				select {
				case got := <-done:
					t.Fatalf("no independent client response: read=%v run=%v exit=%d", readErr, got.err, got.result.Client.ExitCode)
				case <-time.After(10 * time.Second):
					t.Fatal("startup failed to join")
				}
			}
			if mode == "tools-hold" {
				cancel()
				cancel()
			}
			var got outcome
			select {
			case got = <-done:
			case <-time.After(10 * time.Second):
				cancel()
				t.Fatal("startup did not join")
			}
			if mode == "tools-hold" {
				if !errors.Is(got.err, context.Canceled) {
					t.Fatal("lost cancellation", got.err)
				}
			} else if got.err != nil || got.result.Client.ExitCode != 0 {
				t.Fatal("independent runtime failed", got.err, got.result.Client.ExitCode)
			}
			if !got.result.Startup.LaunchAvailable || got.result.Startup.ClientInitialization != "unverified" || prepared.Load() != 1 || cleaned.Load() != 1 {
				t.Fatal("incomplete lifecycle or false readiness", prepared.Load(), cleaned.Load())
			}
			if observed == nil || observed.Server.Gateway.Metrics == nil {
				t.Fatal("startup did not connect metrics")
			}
			if _, err := observed.Models.Models(context.Background()); err == nil {
				t.Fatal("transferred catalog remained open")
			}
			if syscall.Kill(-observation.ClientPID, 0) != syscall.ESRCH {
				t.Fatal("client process group survived")
			}
			endpoint, _ := url.Parse(observation.Endpoint)
			if conn, err := net.DialTimeout("tcp", endpoint.Host, 100*time.Millisecond); err == nil {
				conn.Close()
				t.Fatal("gateway listener survived")
			}
			entries, _ := os.ReadDir(opts.RuntimeParent)
			if len(entries) != 0 {
				t.Fatal("startup or policy artifacts survived")
			}
			after, _ := os.ReadFile(opts.UserSettings)
			if string(after) != string(source) {
				t.Fatal("source settings changed")
			}
			if _, err := stdin.Stat(); err != nil {
				t.Fatal("caller stdin closed")
			}
			preference, err := os.ReadFile(filepath.Join(opts.StateDirectory, "models", "last-model.json"))
			wantSaved := mode == "catalog-text" || mode == "tools-complete"
			if wantSaved && (err != nil || len(preference) == 0) || !wantSaved && !errors.Is(err, os.ErrNotExist) {
				t.Fatal("wrong delivered-model preference state", err)
			}
		})
	}
}

func TestStartupCommandBoundaryReportsDevelopmentPolicy(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("development execution is measured only on macOS arm64")
	}
	bins := buildStartupBinaries(t)
	t.Run("separate-command-budgets", func(t *testing.T) {
		opts := startupOptions(t)
		if err := os.WriteFile(filepath.Join(opts.Home, "preflight-slow"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		base, err := childproc.New(childproc.Config{Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		r := &startupCommandRunner{inner: base, ordinary: 50 * time.Millisecond, catalog: time.Second}
		defer r.Close()
		command := childproc.Command{Executable: bins.kiro, Directory: opts.Project, Environment: []string{"HOME=" + opts.Home}, Args: []string{"--version"}}
		if _, err := r.Run(t.Context(), command); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("ordinary CLI deadline extended", err)
		}
		command.Args = []string{"chat", "--list-models", "--format", "json"}
		result, err := r.Run(t.Context(), command)
		if err != nil || result.ExitCode != 0 {
			t.Fatal("catalog inherited short version deadline", err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		if _, err := r.Run(ctx, command); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("catalog extended caller deadline", err)
		}
		if base.Active() != 0 {
			t.Fatal("timed out CLI remained active")
		}
	})
	for _, command := range []string{"doctor", "models", "run"} {
		t.Run(command, func(t *testing.T) {
			opts := startupOptions(t)
			configureStartupBinaries(t, &opts, bins)
			r, err := childproc.New(childproc.Config{Timeout: 15 * time.Second, MaxOutputBytes: 64 << 10})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			args := []string{command, "--kiro", bins.kiro, "--client", bins.client, "--state-dir", opts.StateDirectory, "--runtime-dir", opts.RuntimeParent, "--settings", opts.UserSettings}
			if command != "run" {
				args = append(args, "--json")
			}
			commandSpec := childproc.Command{Executable: bins.proxy, Directory: opts.Project, Environment: []string{"HOME=" + opts.Home, "PATH=/usr/bin:/bin", "TERM=tools-complete"}, Args: args}
			if command == "run" {
				// The real command owns a foreground terminal. A pipe-only invocation cannot
				// establish that lifecycle; script provides an owned PTY without UI automation.
				marker, wrapper := filepath.Join(opts.Home, "owned-proxy-pid"), filepath.Join(opts.Home, "owned-proxy.sh")
				quoted := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
				if os.WriteFile(wrapper, []byte("#!/bin/sh\numask 077\nprintf '%s' \"$$\" > "+quoted+"\nexec \"$@\"\n"), 0700) != nil {
					t.Fatal("cannot prepare owned terminal launcher")
				}
				t.Cleanup(func() {
					raw, err := os.ReadFile(marker)
					pid, parseErr := strconv.Atoi(string(raw))
					if err != nil || parseErr != nil || pid <= 1 {
						t.Error("terminal launcher owner was not observed")
						return
					}
					if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
						_ = syscall.Kill(-pid, syscall.SIGKILL)
						t.Error("terminal launcher group survived")
					}
				})
				commandSpec.Executable = "/usr/bin/script"
				commandSpec.Args = append([]string{"-q", os.DevNull, "/bin/sh", wrapper, bins.proxy}, args...)
			}
			result, err := r.Run(t.Context(), commandSpec)
			if command == "run" {
				if err != nil || result.ExitCode != 0 {
					labels, _ := os.ReadFile(filepath.Join(opts.Home, "preflight-observations"))
					t.Fatalf("prepared run did not complete the independent client tool round trip: %v, exit=%d, fixed_fixture_labels=%q", err, result.ExitCode, labels)
				}
				observed := false
				for _, line := range strings.Split(string(result.Stdout), "\n") {
					// PTYs may echo their initial EOF marker before the fixture's JSON line.
					start := strings.IndexByte(line, '{')
					if start < 0 {
						continue
					}
					var observation struct {
						ClientPID int `json:"clientPID"`
						State     string
					}
					if json.Unmarshal([]byte(strings.TrimSpace(line[start:])), &observation) == nil && observation.ClientPID > 1 && observation.State == "tools-complete" {
						observed = true
						if !errors.Is(syscall.Kill(-observation.ClientPID, 0), syscall.ESRCH) {
							_ = syscall.Kill(-observation.ClientPID, syscall.SIGKILL)
							t.Error("compiled run left the client process group")
						}
					}
				}
				if !observed {
					labels, _ := os.ReadFile(filepath.Join(opts.Home, "preflight-observations"))
					t.Fatalf("terminal wrapper exited without a client completion; fixed_fixture_labels=%q, output_bytes=%d, client_marker=%v", labels, len(result.Stdout), strings.Contains(string(result.Stdout), `"clientPID"`))
				}
			} else if err != nil || result.ExitCode != 0 {
				t.Fatal("diagnostic command failed", err, result.ExitCode)
			}
			if command == "doctor" {
				var report StartupReport
				if json.Unmarshal(result.Stdout, &report) != nil || report.Policy != "verified" || !report.LaunchAvailable || len(report.Models) != 1 {
					t.Fatal("doctor misreported launch readiness")
				}
			}
			if command == "models" {
				var list struct{ Data []json.RawMessage }
				if json.Unmarshal(result.Stdout, &list) != nil || len(list.Data) != 1 {
					t.Fatal("catalog command did not list models")
				}
			}
			observations, err := os.ReadFile(filepath.Join(opts.Home, "preflight-observations"))
			wantCalls := 6
			if command == "run" {
				wantCalls++
			}
			if err != nil || strings.Count(string(observations), "\n") != wantCalls {
				t.Fatal("incorrect public preflight commands", err, string(observations))
			}
			entries, _ := os.ReadDir(opts.RuntimeParent)
			if len(entries) != 0 {
				t.Fatal("command left startup root")
			}
			file, err := os.Open(opts.UserSettings)
			if err != nil {
				t.Fatal(err)
			}
			bytes, _ := io.ReadAll(file)
			file.Close()
			if string(bytes) != `{"permissions":{"defaultMode":"manual"},"hooks":{}}` {
				t.Fatal("settings changed")
			}
		})
	}
}
