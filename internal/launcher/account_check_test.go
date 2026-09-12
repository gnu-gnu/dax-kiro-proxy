package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

type accountFailureRunner struct {
	*startupRunnerFixture
	failure error
	exit    int
	cancel  context.CancelFunc
}

func (r *accountFailureRunner) Run(ctx context.Context, command childproc.Command) (childproc.Result, error) {
	result, err := r.startupRunnerFixture.Run(ctx, command)
	if strings.Join(command.Args, " ") == "whoami --format json" {
		if r.cancel != nil {
			r.cancel()
		}
		result.ExitCode = r.exit
		return result, r.failure
	}
	return result, err
}

func TestAccountCheckPreservesRunnerFailures(t *testing.T) {
	private := errors.New("private-account-failure-sentinel")
	for _, tc := range []struct {
		name           string
		failure        error
		exit           int
		callerCancel   bool
		login, cleanup bool
	}{
		{"deadline", context.DeadlineExceeded, -1, false, false, false},
		{"deadline-cleanup", errors.Join(context.DeadlineExceeded, childproc.ErrCleanup), -1, false, false, true},
		{"runner-cancel", context.Canceled, -1, false, false, false},
		{"caller-cancel-cleanup", childproc.ErrCleanup, -1, true, false, true},
		{"launch", childproc.ErrStart, -1, false, false, false},
		{"output-limit-and-exit", errors.Join(childproc.ErrOutputLimit, childproc.ErrExit), 1, false, false, false},
		{"pipe", childproc.ErrIO, 0, false, false, false},
		{"closed", childproc.ErrClosed, -1, false, false, false},
		{"busy", childproc.ErrBusy, -1, false, false, false},
		{"unknown", private, 0, false, false, false},
		{"cleanup", childproc.ErrCleanup, 0, false, false, true},
		{"nonzero", childproc.ErrExit, 1, false, true, false},
		{"nonzero-cleanup", errors.Join(childproc.ErrExit, childproc.ErrCleanup), 1, false, true, true},
		{"nonzero-without-error", nil, 1, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := &accountFailureRunner{startupRunnerFixture: new(startupRunnerFixture), failure: tc.failure, exit: tc.exit}
			if tc.callerCancel {
				r.cancel = cancel
			}
			cfg := KiroConfig{Executable: "/fixture/bin/kiro-cli", Home: t.TempDir(), Directory: t.TempDir(), ScopeKey: [32]byte{31}}
			info, err := CheckKiro(ctx, r, cfg)
			if err == nil || info != (KiroInfo{}) || len(r.calls) != 3 {
				t.Fatal("failed account command published identity or changed command sequence")
			}
			for _, cause := range []error{context.Canceled, context.DeadlineExceeded, childproc.ErrStart, childproc.ErrOutputLimit, childproc.ErrIO, childproc.ErrClosed, childproc.ErrBusy, childproc.ErrExit, private} {
				want := errors.Is(tc.failure, cause) || tc.callerCancel && cause == context.Canceled
				if errors.Is(err, cause) != want {
					t.Fatal("account failure lost or invented an execution cause")
				}
			}
			if errors.Is(err, ErrLoginCheck) != tc.login || errors.Is(err, childproc.ErrCleanup) != tc.cleanup || strings.Contains(err.Error(), "private-account") {
				t.Fatal("account failure misclassified login, hid cleanup or exposed private text")
			}
		})
	}
}

func TestAccountFailureStopsStartupBeforePolicy(t *testing.T) {
	for _, inspect := range []bool{true, false} {
		opts := startupOptions(t)
		r := &accountFailureRunner{startupRunnerFixture: new(startupRunnerFixture), exit: -1, failure: errors.Join(context.DeadlineExceeded, childproc.ErrCleanup)}
		services := startupServicesFor(r.startupRunnerFixture)
		services.runner = func() (startupRunner, error) { return r, nil }
		services.policy = func(context.Context, LaunchOptions, KiroInfo) (launchPolicy, error) {
			t.Fatal("account failure reached execution policy")
			return launchPolicy{}, nil
		}
		got, err := start(t.Context(), opts, childproc.AttachedIO{}, inspect, services)
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, childproc.ErrCleanup) || errors.Is(err, ErrLoginCheck) {
			t.Fatal("startup changed the account failure class")
		}
		if got.Startup.Login == "verified" || got.Startup.LaunchAvailable || len(got.Startup.Models) != 0 || len(r.calls) != 4 || got.Startup.Phases[len(got.Startup.Phases)-1].Name != "login_check" {
			t.Fatal("failed account check published readiness or continued preflight")
		}
		assertStartupClean(t, opts, r.startupRunnerFixture)
	}
}

type accountProcessObserver struct {
	startupRunner
	pid, calls   int
	cause        error
	identitySeen bool
}

func (r *accountProcessObserver) Run(ctx context.Context, command childproc.Command) (childproc.Result, error) {
	r.calls++
	result, err := r.startupRunner.Run(ctx, command)
	if strings.Join(command.Args, " ") == "whoami --format json" {
		r.pid, r.cause = result.PID, err
		r.identitySeen = string(result.Stdout) == "{\"accountType\":\"synthetic\",\"email\":\"startup-fixture@example.invalid\"}\n"
	}
	return result, err
}

func TestAccountCommandDeadlineStopsStartupAndJoinsOwner(t *testing.T) {
	bins := buildStartupBinaries(t)
	for _, mode := range []string{"hold", "exit"} {
		for _, inspect := range []bool{true, false} {
			t.Run(mode+map[bool]string{true: "-inspect", false: "-run"}[inspect], func(t *testing.T) {
				opts := startupOptions(t)
				configureStartupBinaries(t, &opts, bins)
				if os.WriteFile(filepath.Join(opts.Home, "preflight-identity-"+mode), nil, 0600) != nil {
					t.Fatal("cannot prepare independent account control")
				}
				inner, err := childproc.New(childproc.Config{Timeout: 2 * time.Second})
				if err != nil {
					t.Fatal("cannot prepare bounded process runner")
				}
				defer inner.Close()
				r := &accountProcessObserver{startupRunner: &startupCommandRunner{inner: inner, ordinary: time.Second, catalog: 2 * time.Second}}
				services := productionStartup()
				services.runner = func() (startupRunner, error) { return r, nil }
				services.policy = func(context.Context, LaunchOptions, KiroInfo) (launchPolicy, error) {
					t.Fatal("failed independent account command reached policy")
					return launchPolicy{}, nil
				}
				got, err := start(t.Context(), opts, childproc.AttachedIO{}, inspect, services)
				want := childproc.ErrExit
				if mode == "hold" {
					want = context.DeadlineExceeded
				}
				if r.pid <= 1 || r.calls != 4 || !r.identitySeen || inner.Active() != 0 || !errors.Is(r.cause, want) || !errors.Is(syscall.Kill(-r.pid, 0), syscall.ESRCH) {
					t.Fatal("account process did not reach the intended failure and join its group")
				}
				if err == nil || t.Context().Err() != nil || errors.Is(err, childproc.ErrCleanup) || errors.Is(err, ErrLoginCheck) != (mode == "exit") || mode == "hold" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("account process failure was misclassified")
				}
				entries, readErr := os.ReadDir(opts.RuntimeParent)
				settings, settingsErr := os.ReadFile(opts.UserSettings)
				if got.Startup.Login == "verified" || got.Startup.LaunchAvailable || readErr != nil || len(entries) != 0 || settingsErr != nil || string(settings) != `{"permissions":{"defaultMode":"manual"},"hooks":{}}` {
					t.Fatal("failed account check published readiness, retained runtime or changed settings")
				}
			})
		}
	}
}
