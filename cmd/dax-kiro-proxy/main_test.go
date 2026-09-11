package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
)

func fixtureOptions() launcher.LaunchOptions {
	return launcher.LaunchOptions{Home: "/owned/home", Project: "/owned/project", ProxyExecutable: "/owned/bin/proxy", RuntimeParent: "/owned/tmp", StateDirectory: "/owned/state", UserSettings: "/owned/home/.claude/settings.json", Environment: []string{"PATH=/owned/bin"}}
}

func fixtureReport() launcher.StartupReport {
	return launcher.StartupReport{KiroVersion: launcher.SupportedKiroVersion, KiroVersionMeasured: true, ClientVersion: launcher.SupportedClientVersion, ClientVersionMeasured: true, Login: "verified", Policy: "unverified", SelectedModel: "claude-dax-fixture-0000000000000000", ClientInitialization: "unverified", Models: []inference.Model{{ID: "claude-dax-fixture-0000000000000000", Object: "model", Name: "Independent model"}}, Phases: []launcher.PhaseTiming{{Name: "login_check", Milliseconds: 7}}}
}

func fixtureServices() commandServices {
	return commandServices{defaults: func() (launcher.LaunchOptions, error) { return fixtureOptions(), nil }, inspect: func(context.Context, launcher.LaunchOptions) (launcher.StartupReport, error) {
		return fixtureReport(), nil
	}, run: func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error) {
		return launcher.LaunchResult{Startup: fixtureReport()}, launcher.ErrPolicyUnverified
	}}
}

func invoke(t *testing.T, ctx context.Context, args []string, services commandServices) (int, string, string) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := execute(ctx, args, childproc.AttachedIO{}, &out, &diagnostics, services)
	return code, out.String(), diagnostics.String()
}

func TestHelpVersionAndExactInternalDispatch(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"version"}, {"--version"}, {"doctor", "--help"}, {"models", "--help"}, {"run", "--help"}, {"internal-terminal-reclaim"}} {
		services := fixtureServices()
		services.defaults = func() (launcher.LaunchOptions, error) {
			t.Fatal("non-launch command resolved defaults")
			return launcher.LaunchOptions{}, nil
		}
		code, _, _ := invoke(t, t.Context(), args, services)
		if code != 0 {
			t.Fatalf("command %q exited %d", args, code)
		}
	}
	for _, command := range []string{"schema-worker", "relay", "statusline", "model-notice", "turn-metrics"} {
		called := false
		services := commandServices{schema: func() error { called = true; return nil }, relay: func(_ context.Context, path string) error { called = path == "/owned/config"; return nil }}
		services.statusline = func(_ context.Context, path string) (string, error) { called = path == "/owned/config"; return "", nil }
		services.notice = func(_ context.Context, path string) (string, error) { called = path == "/owned/config"; return "", nil }
		services.metrics = func(_ context.Context, path string) (string, error) { called = path == "/owned/config"; return "", nil }
		args := []string{command}
		if command == "relay" || command == "statusline" || command == "model-notice" || command == "turn-metrics" {
			args = append(args, "--config", "/owned/config")
		}
		code, out, diagnostics := invoke(t, t.Context(), args, services)
		if code != 0 || !called || out != "" || diagnostics != "" {
			t.Fatal("internal dispatch changed", code, called, out, diagnostics)
		}
		called = false
		args = append(args, "unexpected")
		code, _, _ = invoke(t, t.Context(), args, services)
		if code != 2 || called {
			t.Fatal("nonexact internal invocation admitted", code, called)
		}
	}
	code, _, _ := invoke(t, t.Context(), []string{"internal-terminal-reclaim", "extra"}, commandServices{})
	if code != 2 {
		t.Fatal("nonexact reclaim admitted")
	}
}

func TestDoctorModelsAndTimingOutputs(t *testing.T) {
	services := fixtureServices()
	code, out, diagnostics := invoke(t, t.Context(), []string{"doctor", "--json", "--timing"}, services)
	var got launcher.StartupReport
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Policy != "unverified" || got.LaunchAvailable || got.Login != "verified" {
		t.Fatal("doctor did not report unavailable launch successfully", code, out)
	}
	if !strings.Contains(diagnostics, "7 ms") || strings.Contains(out, " ms") {
		t.Fatal("timings did not stay on stderr", out, diagnostics)
	}
	code, out, diagnostics = invoke(t, t.Context(), []string{"models", "--json"}, services)
	var listing struct {
		Object string            `json:"object"`
		Data   []inference.Model `json:"data"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &listing) != nil || listing.Object != "list" || len(listing.Data) != 1 || diagnostics != "" {
		t.Fatal("invalid model listing", code, out, diagnostics)
	}
	code, out, diagnostics = invoke(t, t.Context(), []string{"models"}, services)
	if code != 0 || out != fixtureReport().Models[0].ID+"\n" || diagnostics != "" {
		t.Fatal("plain listing changed", code, out, diagnostics)
	}
	code, out, diagnostics = invoke(t, t.Context(), []string{"doctor"}, services)
	if code != 0 || !strings.Contains(out, "Launch: unavailable") || !strings.Contains(out, "Client initialization: unverified") || diagnostics != "" {
		t.Fatal("doctor concealed unverified state", code, out, diagnostics)
	}
	if !strings.Contains(out, "Kiro: "+launcher.SupportedKiroVersion) || !strings.Contains(out, "Claude Code: "+launcher.SupportedClientVersion) {
		t.Fatal("doctor omitted verified versions", out)
	}
}

func TestDoctorReportsUnmeasuredClientBuild(t *testing.T) {
	services := fixtureServices()
	services.inspect = func(context.Context, launcher.LaunchOptions) (launcher.StartupReport, error) {
		report := fixtureReport()
		report.ClientVersion, report.ClientVersionMeasured = "2.9.1", false
		return report, nil
	}
	code, out, diagnostics := invoke(t, t.Context(), []string{"doctor"}, services)
	if code != 0 || diagnostics != "" || !strings.Contains(out, "Claude Code: 2.9.1 (unmeasured; measured "+launcher.SupportedClientVersion+")") {
		t.Fatal("doctor concealed an unmeasured client build", code, out, diagnostics)
	}
	code, out, diagnostics = invoke(t, t.Context(), []string{"doctor", "--json"}, services)
	if code != 0 || diagnostics != "" || !strings.Contains(out, `"client_version":"2.9.1"`) || !strings.Contains(out, `"client_version_measured":false`) {
		t.Fatal("doctor JSON omitted measurement state", code, out, diagnostics)
	}
}

func TestDoctorReportsUnmeasuredKiroBuild(t *testing.T) {
	services := fixtureServices()
	services.inspect = func(context.Context, launcher.LaunchOptions) (launcher.StartupReport, error) {
		report := fixtureReport()
		report.KiroVersion, report.KiroVersionMeasured = "2.30.1", false
		return report, nil
	}
	code, out, diagnostics := invoke(t, t.Context(), []string{"doctor"}, services)
	if code != 0 || diagnostics != "" || !strings.Contains(out, "Kiro: 2.30.1 (unmeasured; measured "+launcher.SupportedKiroVersion+")") {
		t.Fatal("doctor concealed an unmeasured Kiro build", code, out, diagnostics)
	}
	code, out, diagnostics = invoke(t, t.Context(), []string{"doctor", "--json"}, services)
	if code != 0 || diagnostics != "" || !strings.Contains(out, `"kiro_version":"2.30.1"`) || !strings.Contains(out, `"kiro_version_measured":false`) {
		t.Fatal("doctor JSON omitted Kiro measurement state", code, out, diagnostics)
	}
}

func TestLaunchFlagsAndDescriptorOwnership(t *testing.T) {
	file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	files := childproc.AttachedIO{Stdin: file, Stdout: file, Stderr: file, Foreground: true}
	want := fixtureOptions()
	want.KiroExecutable, want.ClientExecutable = "/configured/kiro", "/configured/client"
	want.StateDirectory, want.RuntimeParent, want.UserSettings = "/configured/state", "/configured/runtime", "/configured/settings"
	want.InitialModel, want.InitialEffort, want.Interactive = "exact-model", "high", true
	want.ToolTimeout, want.TurnTimeout, want.FirstEventTimeout = 20*time.Minute, 40*time.Minute, 2*time.Minute
	services := fixtureServices()
	called := false
	services.run = func(_ context.Context, got launcher.LaunchOptions, descriptors childproc.AttachedIO) (launcher.LaunchResult, error) {
		called = true
		if !reflect.DeepEqual(got, want) || descriptors != files {
			t.Fatal("flags or descriptors changed", got)
		}
		return launcher.LaunchResult{Client: launcher.ClientRunResult{ClientPID: 42, ExitCode: 23}}, childproc.ErrExit
	}
	args := []string{"run", "--kiro", want.KiroExecutable, "--client", want.ClientExecutable, "--state-dir", want.StateDirectory, "--runtime-dir", want.RuntimeParent, "--settings", want.UserSettings, "--model", want.InitialModel, "--effort", want.InitialEffort, "--tool-timeout", "20m", "--turn-timeout", "40m", "--first-event-timeout", "2m"}
	var out, diagnostics bytes.Buffer
	if code := execute(t.Context(), args, files, &out, &diagnostics, services); code != 23 || !called || out.Len() != 0 {
		t.Fatal("client exit not preserved", code, called)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("caller descriptor closed", err)
	}
}

func TestNativeHistoryFlagsStayOnRun(t *testing.T) {
	const id = "197326ab-1597-4268-a129-426853197ace"
	for _, args := range [][]string{{"run", "--client-history"}, {"run", "--resume", id}} {
		services := fixtureServices()
		called := false
		services.run = func(_ context.Context, got launcher.LaunchOptions, _ childproc.AttachedIO) (launcher.LaunchResult, error) {
			called = true
			if !got.KeepHistory || (args[1] == "--resume" && got.ResumeSession != id) {
				t.Fatal("native history option lost")
			}
			return launcher.LaunchResult{}, nil
		}
		var out, diagnostics bytes.Buffer
		if execute(t.Context(), args, childproc.AttachedIO{}, &out, &diagnostics, services) != 0 || !called {
			t.Fatal("native history option rejected")
		}
	}
	for _, args := range [][]string{{"doctor", "--client-history"}, {"models", "--resume", id}, {"run", "--client-history=false", "--resume", id}, {"run", "--resume", ""}} {
		services := fixtureServices()
		services.defaults = func() (launcher.LaunchOptions, error) {
			t.Fatal("invalid history arguments reached startup")
			return launcher.LaunchOptions{}, nil
		}
		var out, diagnostics bytes.Buffer
		if execute(t.Context(), args, childproc.AttachedIO{}, &out, &diagnostics, services) == 0 {
			t.Fatal("invalid history option accepted")
		}
	}
}

func TestErrorsUseFixedDiagnosticsAndExitClasses(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		result launcher.ClientRunResult
		code   int
	}{
		{"policy", launcher.ErrPolicyUnverified, launcher.ClientRunResult{}, 3},
		{"config", launcher.ErrConfig, launcher.ClientRunResult{}, 2},
		{"settings", launcher.ErrSettings, launcher.ClientRunResult{}, 2},
		{"model", catalog.ErrModel, launcher.ClientRunResult{}, 2},
		{"login", launcher.ErrLoginCheck, launcher.ClientRunResult{}, 1},
		{"canceled", context.Canceled, launcher.ClientRunResult{}, 130},
		{"unknown", errors.New("private-error-sentinel"), launcher.ClientRunResult{}, 1},
		{"clean-client-exit", nil, launcher.ClientRunResult{ClientPID: 42, ExitCode: 0}, 0},
		{"client-exit", childproc.ErrExit, launcher.ClientRunResult{ClientPID: 42, ExitCode: 19}, 19},
		{"cleanup-after-success", launcher.ErrRunCleanup, launcher.ClientRunResult{ClientPID: 42, ExitCode: 0}, 1},
		{"cleanup-after-failure", errors.Join(childproc.ErrExit, launcher.ErrRunCleanup), launcher.ClientRunResult{ClientPID: 42, ExitCode: 19}, 19},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			services := fixtureServices()
			services.run = func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error) {
				return launcher.LaunchResult{Client: tc.result}, tc.err
			}
			code, out, diagnostics := invoke(t, t.Context(), []string{"run"}, services)
			if code != tc.code || out != "" || strings.Contains(diagnostics, "private-error-sentinel") {
				t.Fatal("wrong error outcome", code, out, diagnostics)
			}
			if tc.err == launcher.ErrLoginCheck && !strings.Contains(diagnostics, "kiro-cli login") {
				t.Fatal("login instruction missing")
			}
		})
	}
	services := fixtureServices()
	services.inspect = func(context.Context, launcher.LaunchOptions) (launcher.StartupReport, error) {
		return fixtureReport(), errors.New("private-error-sentinel")
	}
	code, out, diagnostics := invoke(t, t.Context(), []string{"doctor", "--json"}, services)
	if code != 1 || out != "" || strings.Contains(diagnostics, "private-error-sentinel") {
		t.Fatal("failed inspection reported success or leaked data")
	}
}

func TestInvalidArgumentsDoNotReachPreflightOrLeakValues(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown-private-sentinel"}, {"run", "--json"}, {"doctor", "--settings"}, {"models", "extra-private-sentinel"}, {"run", "--trust-unverified"}, {"run", "--model", "private\nvalue"}, {"doctor", "--kiro=" + strings.Repeat("x", 4097)}, append([]string{"run"}, make([]string, 65)...), {"run", "--settings", ""}, {"doctor", "--model="}, {"run", "--effort="}, {"models", "--kiro="}, {"run", "--client="}, {"run", "--runtime-dir="}, {"run", "--state-dir="}} {
		services := fixtureServices()
		services.defaults = func() (launcher.LaunchOptions, error) {
			t.Fatal("invalid input reached defaults")
			return launcher.LaunchOptions{}, nil
		}
		code, out, diagnostics := invoke(t, t.Context(), args, services)
		if code != 2 || out != "" || strings.Contains(diagnostics, "private") || len(diagnostics) > 1024 {
			t.Fatalf("unsafe validation for %q: %d %q %q", args, code, out, diagnostics)
		}
	}
}

func TestCatalogAndStateFailuresIdentifySafeStartupStage(t *testing.T) {
	for _, tc := range []struct {
		name, stage, action string
		err                 error
	}{
		{"prepared-models", "model catalog preparation", "doctor --timing", launcher.ErrModels},
		{"catalog", "model catalog preparation", "doctor --timing", catalog.ErrCatalog},
		{"state", "private state preparation", "--state-dir", launcher.ErrState},
	} {
		for _, command := range []string{"doctor", "models", "run"} {
			t.Run(tc.name+"-"+command, func(t *testing.T) {
				services := fixtureServices()
				err := errors.Join(tc.err, errors.New("private-stage-sentinel"))
				services.inspect = func(context.Context, launcher.LaunchOptions) (launcher.StartupReport, error) {
					return fixtureReport(), err
				}
				services.run = func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error) {
					return launcher.LaunchResult{}, err
				}
				code, out, diagnostics := invoke(t, t.Context(), []string{command}, services)
				if code != 1 || out != "" || !strings.Contains(diagnostics, tc.stage) || !strings.Contains(diagnostics, tc.action) || strings.Contains(diagnostics, "private-stage-sentinel") {
					t.Fatal("missing or unsafe stage diagnostic", code, out, diagnostics)
				}
			})
		}
	}
}

func TestCleanupFailureRemainsVisibleWithPrimaryError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{{context.Canceled, 130}, {launcher.ErrPolicyUnverified, 3}, {launcher.ErrConfig, 2}, {launcher.ErrLoginCheck, 1}} {
		services := fixtureServices()
		services.run = func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error) {
			return launcher.LaunchResult{}, errors.Join(tc.err, launcher.ErrRunCleanup, errors.New("private-cleanup-sentinel"))
		}
		code, _, diagnostics := invoke(t, t.Context(), []string{"run"}, services)
		if code != tc.code || !strings.Contains(diagnostics, "cleanup") || strings.Contains(diagnostics, "private-cleanup-sentinel") {
			t.Fatal("cleanup failure hidden or leaked", code, diagnostics)
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) { return len(data) / 2, nil }

func TestTimingsHaveBoundedFixedNamesAndCheckShortWrites(t *testing.T) {
	services := fixtureServices()
	var out bytes.Buffer
	if code := execute(t.Context(), []string{"doctor", "--timing"}, childproc.AttachedIO{}, &out, shortWriter{}, services); code == 0 || out.Len() != 0 {
		t.Fatal("short timing write reported successful inspection", code)
	}
	if err := writeTimings(io.Discard, make([]launcher.PhaseTiming, 33)); err == nil {
		t.Fatal("unbounded phase list accepted")
	}
	var diagnostics bytes.Buffer
	phases := []launcher.PhaseTiming{{Name: "private-phase-sentinel", Milliseconds: 7}, {Name: "login_check", Milliseconds: -1}, {Name: "login_check", Milliseconds: 1 << 62}}
	if err := writeTimings(&diagnostics, phases); err == nil || strings.Contains(diagnostics.String(), "private-phase-sentinel") {
		t.Fatal("invalid phase metadata accepted or leaked", diagnostics.String())
	}
}

func TestCancellationAfterClientExitKeepsCancellationStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	services := fixtureServices()
	services.run = func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error) {
		cancel()
		return launcher.LaunchResult{Client: launcher.ClientRunResult{ClientPID: 42, ExitCode: 0}}, nil
	}
	code, _, _ := invoke(t, ctx, []string{"run"}, services)
	if code != 130 {
		t.Fatal("lost cancellation", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputFailureIsUnsuccessful(t *testing.T) {
	for _, args := range [][]string{{"doctor", "--json"}, {"doctor"}, {"models", "--json"}, {"models"}} {
		var diagnostics bytes.Buffer
		if code := execute(t.Context(), args, childproc.AttachedIO{}, failingWriter{}, &diagnostics, fixtureServices()); code == 0 {
			t.Fatal("output failure reported success", args)
		}
	}
}

func TestDiagnosticsNameTheFailureClass(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		result       launcher.ClientRunResult
		code         int
		want, reject string
	}{
		{"terminal", childproc.ErrTerminalUnavailable, launcher.ClientRunResult{}, 2, "foreground terminal", "cleanup"},
		{"lifetime", errors.Join(childproc.ErrLifetime, context.DeadlineExceeded), launcher.ClientRunResult{ClientPID: 42, ExitCode: -1}, 1, "lifetime limit", "startup or client"},
		{"model", catalog.ErrModel, launcher.ClientRunResult{}, 2, "dax-kiro-proxy models", "configuration"},
		{"kiro-missing", errors.Join(launcher.ErrKiroVersion, launcher.ErrExecutableNotFound), launcher.ClientRunResult{}, 1, "kiro-cli was not found", "not supported"},
		{"client-missing", errors.Join(launcher.ErrClientVersion, launcher.ErrExecutableNotFound), launcher.ClientRunResult{}, 1, "claude was not found", "not supported"},
		{"kiro-version", &launcher.VersionError{Component: "kiro-cli", Found: "3.0.0", Expected: "major version 2", Sentinel: launcher.ErrKiroVersion}, launcher.ClientRunResult{}, 1, "kiro-cli 3.0.0 is not supported; expected major version 2", ""},
		{"client-version", &launcher.VersionError{Component: "Claude Code", Found: "1.0.0", Expected: "major version 2", Sentinel: launcher.ErrClientVersion}, launcher.ClientRunResult{}, 1, "Claude Code 1.0.0 is not supported", ""},
		{"kiro-version-unnamed", &launcher.VersionError{Component: "kiro-cli", Sentinel: launcher.ErrKiroVersion}, launcher.ClientRunResult{}, 1, "Kiro installation or version is not supported", "expected"},
		{"runtime", launcher.ErrRuntime, launcher.ClientRunResult{}, 1, "runtime directory", "startup or client"},
		{"bind", gateway.ErrServerBind, launcher.ClientRunResult{}, 1, "loopback gateway", "startup or client"},
		{"start", childproc.ErrStart, launcher.ClientRunResult{}, 1, "cannot start the Claude Code executable", "startup or client"},
		{"gateway-stopped", launcher.ErrGatewayStopped, launcher.ClientRunResult{ClientPID: 42, ExitCode: -1}, 1, "gateway stopped", "startup or client"},
		{"restore", childproc.ErrTerminal, launcher.ClientRunResult{ClientPID: 42, ExitCode: 0}, 1, "cleanup", "foreground terminal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			services := fixtureServices()
			services.run = func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error) {
				return launcher.LaunchResult{Client: tc.result}, tc.err
			}
			code, out, diagnostics := invoke(t, t.Context(), []string{"run"}, services)
			if code != tc.code || out != "" || !strings.Contains(diagnostics, tc.want) || tc.reject != "" && strings.Contains(diagnostics, tc.reject) {
				t.Fatal("diagnostic did not name the failure class", code, diagnostics)
			}
		})
	}
}

func TestTimeoutFlagsStayOnRunAndRejectInvalidValues(t *testing.T) {
	for _, args := range [][]string{{"doctor", "--tool-timeout", "1m"}, {"models", "--turn-timeout", "1m"}, {"run", "--tool-timeout", "0s"}, {"run", "--first-event-timeout", "soon"}, {"run", "--turn-timeout", ""}, {"run", "--tool-timeout"}} {
		services := fixtureServices()
		services.defaults = func() (launcher.LaunchOptions, error) {
			t.Fatal("invalid timeout arguments reached startup")
			return launcher.LaunchOptions{}, nil
		}
		var out, diagnostics bytes.Buffer
		if execute(t.Context(), args, childproc.AttachedIO{}, &out, &diagnostics, services) == 0 {
			t.Fatal("invalid timeout option accepted", args)
		}
	}
	for _, args := range [][]string{{"run", "--tool-timeout", "45s"}, {"run", "--turn-timeout", "1h"}, {"run", "--first-event-timeout", "10s"}} {
		services := fixtureServices()
		called := false
		services.run = func(_ context.Context, got launcher.LaunchOptions, _ childproc.AttachedIO) (launcher.LaunchResult, error) {
			called = true
			if got.ToolTimeout+got.TurnTimeout+got.FirstEventTimeout == 0 {
				t.Fatal("timeout option lost")
			}
			return launcher.LaunchResult{}, nil
		}
		var out, diagnostics bytes.Buffer
		if execute(t.Context(), args, childproc.AttachedIO{}, &out, &diagnostics, services) != 0 || !called {
			t.Fatal("valid timeout option rejected", args)
		}
	}
}
