package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

type startupRunnerFixture struct {
	calls         []string
	closed        bool
	fail          string
	cancel        context.CancelFunc
	cancelAt      string
	root          string
	cancelOnClose context.CancelFunc
}

func (r *startupRunnerFixture) Close() {
	r.closed = true
	if r.cancelOnClose != nil {
		r.cancelOnClose()
	}
}
func (r *startupRunnerFixture) Run(ctx context.Context, c childproc.Command) (childproc.Result, error) {
	name := filepath.Base(c.Executable) + " " + strings.Join(c.Args, " ")
	r.calls = append(r.calls, name)
	r.root = filepath.Dir(c.Directory)
	if c.Directory == "" || ctx.Err() != nil {
		return childproc.Result{}, ctx.Err()
	}
	if name == r.cancelAt {
		r.cancel()
		return childproc.Result{}, ctx.Err()
	}
	if name == r.fail {
		return childproc.Result{}, errors.New("synthetic-private-cli-diagnostic")
	}
	var raw string
	switch name {
	case "claude --version":
		raw = "2.1.263 (Claude Code)\n"
	case "kiro-cli --version":
		raw = "kiro-cli 2.21.1\n"
	case "kiro-cli-chat --version":
		raw = "kiro-cli-chat 2.21.1\n"
	case "kiro-cli whoami --format json":
		raw = `{"accountType":"synthetic","email":"own-fixture@example.invalid"}`
	case "kiro-cli chat --list-models --format json":
		raw = `{"default_model":"fixture-backend","models":[{"model_id":"fixture-backend"}]}`
	default:
		return childproc.Result{}, errors.New("unexpected independent command")
	}
	return childproc.Result{ExitCode: 0, Stdout: []byte(raw)}, nil
}
func startupOptions(t *testing.T) LaunchOptions {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"home", "project", "runtime", "bin"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"claude", "kiro-cli", "kiro-cli-chat", "proxy"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte("independent unexecuted fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return LaunchOptions{Home: filepath.Join(root, "home"), Project: filepath.Join(root, "project"), RuntimeParent: filepath.Join(root, "runtime"), StateDirectory: filepath.Join(root, "state"), ProxyExecutable: filepath.Join(root, "bin", "proxy"), Environment: []string{"PATH=" + filepath.Join(root, "bin")}, Interactive: true}
}
func startupServicesFor(r *startupRunnerFixture) startupServices {
	return startupServices{runner: func() (startupRunner, error) { return r, nil }, policy: unverifiedKiroPolicy, client: RunClient}
}
func assertStartupClean(t *testing.T, opts LaunchOptions, r *startupRunnerFixture) {
	t.Helper()
	if !r.closed {
		t.Fatal("finite runner was not closed")
	}
	entries, err := os.ReadDir(opts.RuntimeParent)
	if err != nil || len(entries) != 0 {
		t.Fatal("ephemeral runtime retained", err, len(entries))
	}
}

func TestStartupPreflightAndMandatoryPolicyBarrier(t *testing.T) {
	for _, inspect := range []bool{false, true} {
		t.Run(map[bool]string{true: "doctor", false: "run"}[inspect], func(t *testing.T) {
			opts := startupOptions(t)
			r := new(startupRunnerFixture)
			services := startupServicesFor(r)
			launched := false
			services.client = func(context.Context, ClientRunConfig) (ClientRunResult, error) {
				launched = true
				return ClientRunResult{}, nil
			}
			got, err := start(context.Background(), opts, childproc.AttachedIO{}, inspect, services)
			if inspect && err != nil || !inspect && !errors.Is(err, ErrPolicyUnverified) {
				t.Fatalf("wrong policy result: %v", err)
			}
			if launched || got.Startup.Policy != "unverified" || got.Startup.LaunchAvailable || got.Startup.Login != "verified" || len(got.Startup.Models) != 1 || got.Startup.ModelSource != ModelDefault {
				t.Fatalf("unsafe or incomplete startup report: %+v", got.Startup)
			}
			want := []string{"claude --version", "kiro-cli --version", "kiro-cli-chat --version", "kiro-cli whoami --format json", "kiro-cli --version", "kiro-cli-chat --version", "kiro-cli chat --list-models --format json"}
			if !reflect.DeepEqual(r.calls, want) {
				t.Fatalf("command order %v", r.calls)
			}
			assertStartupClean(t, opts, r)
		})
	}
}
func TestStartupFailureAndCancellationOrder(t *testing.T) {
	for _, failed := range []string{"claude --version", "kiro-cli --version", "kiro-cli-chat --version", "kiro-cli whoami --format json", "kiro-cli chat --list-models --format json"} {
		for _, cancelled := range []bool{false, true} {
			t.Run(failed+map[bool]string{true: "-cancel", false: "-failure"}[cancelled], func(t *testing.T) {
				opts := startupOptions(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				r := &startupRunnerFixture{cancel: cancel}
				if cancelled {
					r.cancelAt = failed
				} else {
					r.fail = failed
				}
				services := startupServicesFor(r)
				policyCalled := false
				services.policy = func(context.Context, LaunchOptions, KiroInfo) (launchPolicy, error) {
					policyCalled = true
					return launchPolicy{}, ErrPolicyUnverified
				}
				_, err := start(ctx, opts, childproc.AttachedIO{}, false, services)
				if err == nil || strings.Contains(err.Error(), "private-cli") || policyCalled {
					t.Fatalf("unsafe failure: %v, policy=%v", err, policyCalled)
				}
				if cancelled && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				assertStartupClean(t, opts, r)
			})
		}
	}
}
func TestStartupUnknownModelAndInvalidSettingsFailBeforePolicy(t *testing.T) {
	for _, kind := range []string{"model", "settings", "missing-client", "invalid-effort"} {
		t.Run(kind, func(t *testing.T) {
			opts := startupOptions(t)
			switch kind {
			case "model":
				opts.InitialModel = "missing-model"
			case "settings":
				opts.UserSettings = filepath.Join(opts.Home, "bad.json")
				if err := os.WriteFile(opts.UserSettings, []byte(`{"hooks":42`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-client":
				opts.ClientExecutable = filepath.Join(opts.Home, "missing")
			case "invalid-effort":
				opts.InitialEffort = "invented"
			}
			r := new(startupRunnerFixture)
			services := startupServicesFor(r)
			policyCalled := false
			services.policy = func(context.Context, LaunchOptions, KiroInfo) (launchPolicy, error) {
				policyCalled = true
				return launchPolicy{}, ErrPolicyUnverified
			}
			_, err := start(context.Background(), opts, childproc.AttachedIO{}, false, services)
			if err == nil || policyCalled {
				t.Fatal("invalid startup reached policy", err)
			}
			if kind == "model" && !errors.Is(err, catalog.ErrModel) {
				t.Fatal("unknown model silently fell back", err)
			}
			if kind != "model" && len(r.calls) != 0 {
				t.Fatal("invalid local configuration spawned commands")
			}
			entries, _ := os.ReadDir(opts.RuntimeParent)
			if len(entries) != 0 {
				t.Fatal("failed startup retained runtime")
			}
		})
	}
}

func TestStartupPostPreflightCancellationJoinsTransferredOwners(t *testing.T) {
	for _, where := range []string{"policy", "client-entry"} {
		t.Run(where, func(t *testing.T) {
			opts := startupOptions(t)
			r := new(startupRunnerFixture)
			services := startupServicesFor(r)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			services.policy = func(context.Context, LaunchOptions, KiroInfo) (launchPolicy, error) {
				if where == "policy" {
					cancel()
				}
				return launchPolicy{process: acp.Config{Executable: opts.KiroExecutable, Directory: opts.Project, ClientInfo: acp.Info{Name: "independent-startup", Version: "1"}}, prepare: func(context.Context, session.LaunchInput) (session.LaunchResources, error) {
					t.Error("canceled startup reached launch preparation")
					return session.LaunchResources{}, ErrRuntime
				}}, nil
			}
			called := false
			services.client = func(ctx context.Context, cfg ClientRunConfig) (ClientRunResult, error) {
				called = true
				cancel()
				result, err := RunClient(ctx, cfg)
				if cfg.Schema.Stats().Started != 0 {
					t.Error("canceled startup spawned a schema worker")
				}
				if checkErr := cfg.Schema.Check(context.Background(), []byte(`{"type":"object"}`)); !errors.Is(checkErr, schemacheck.ErrClosed) {
					t.Error("schema owner not closed", checkErr)
				}
				if _, modelErr := cfg.Models.Models(context.Background()); modelErr == nil {
					t.Error("catalog owner not closed")
				}
				return result, err
			}
			_, err := start(ctx, opts, childproc.AttachedIO{}, false, services)
			if !errors.Is(err, context.Canceled) || called != (where == "client-entry") {
				t.Fatal("wrong cancellation ownership", err, called)
			}
			assertStartupClean(t, opts, r)
		})
	}
}

func TestStartupRefusesToRemoveReplacedRuntimeRoot(t *testing.T) {
	opts := startupOptions(t)
	r := new(startupRunnerFixture)
	services := startupServicesFor(r)
	var replacement string
	services.policy = func(context.Context, LaunchOptions, KiroInfo) (launchPolicy, error) {
		replacement = r.root
		if err := os.Rename(replacement, replacement+"-owned"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(replacement, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(replacement, "independent-sentinel"), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
		return launchPolicy{}, ErrPolicyUnverified
	}
	_, err := start(t.Context(), opts, childproc.AttachedIO{}, false, services)
	if !errors.Is(err, ErrPolicyUnverified) || !errors.Is(err, ErrRunCleanup) || !r.closed {
		t.Fatal("lost cleanup failure", err)
	}
	if data, err := os.ReadFile(filepath.Join(replacement, "independent-sentinel")); err != nil || string(data) != "preserve" {
		t.Fatal("replacement runtime was removed")
	}
}

func TestStartupCancellationDuringFinalCleanupRemainsVisible(t *testing.T) {
	for _, inspect := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "inspect"}[inspect], func(t *testing.T) {
			opts := startupOptions(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := &startupRunnerFixture{cancelOnClose: cancel}
			_, err := start(ctx, opts, childproc.AttachedIO{}, inspect, startupServicesFor(r))
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cleanup lost caller cancellation", err)
			}
			assertStartupClean(t, opts, r)
		})
	}
}
