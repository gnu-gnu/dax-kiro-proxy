package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
	"dax-kiro-proxy/internal/status"
)

var ErrClientVersion = errors.New("Claude Code installation or version is not supported")

// ErrExecutableNotFound accompanies ErrClientVersion or ErrKiroVersion when the executable itself
// is absent from PATH or the given path, as opposed to present but rejected.
var ErrExecutableNotFound = errors.New("required executable was not found")

// ClientLifetime bounds one interactive client session. It is the attached-process ceiling rather
// than a working-day figure: an interactive session left open overnight must not be cut off.
const ClientLifetime = 7 * 24 * time.Hour

// Default deadlines for one model turn. The tool wait covers a permission prompt the user answers
// late; the turn deadline spans every tool handoff of one prompt; the first-event wait bounds a
// silent backend. Each is a launch option so a user can trade responsiveness for patience.
const (
	DefaultToolTimeout       = 15 * time.Minute
	DefaultTurnTimeout       = 30 * time.Minute
	DefaultFirstEventTimeout = 90 * time.Second
)

var ErrPolicyUnverified = errors.New("launch is unavailable: execution restrictions for this Kiro version have not been verified")

type LaunchOptions struct {
	KiroExecutable, ClientExecutable, ProxyExecutable          string
	Home, Project, RuntimeParent, StateDirectory, UserSettings string
	InitialModel, InitialEffort                                string
	Environment                                                []string
	Interactive                                                bool
	KeepHistory                                                bool
	ResumeSession                                              string
	// Zero selects the Default* deadlines; see normalizeLaunchOptions for the accepted ranges.
	ToolTimeout, TurnTimeout, FirstEventTimeout time.Duration
}
type PhaseTiming struct {
	Name         string `json:"name"`
	Milliseconds int64  `json:"milliseconds"`
}
type StartupReport struct {
	KiroVersion string `json:"kiro_version,omitempty"`
	// True only for the exact measured Kiro build; a same-major build is admitted unmeasured (D114).
	KiroVersionMeasured bool   `json:"kiro_version_measured"`
	ClientVersion       string `json:"client_version,omitempty"`
	// ClientVersionMeasured is true only for the exact build behind the recorded evidence;
	// another admitted build of the same major version reports false (D110).
	ClientVersionMeasured bool              `json:"client_version_measured"`
	Login                 string            `json:"login"`
	Policy                string            `json:"policy"`
	LaunchAvailable       bool              `json:"launch_available"`
	SelectedModel         string            `json:"selected_model,omitempty"`
	ModelSource           ModelSource       `json:"model_source,omitempty"`
	CatalogStale          bool              `json:"catalog_stale"`
	Models                []inference.Model `json:"models,omitempty"`
	Phases                []PhaseTiming     `json:"phases"`
	ClientInitialization  string            `json:"client_initialization"`
}
type LaunchResult struct {
	Startup StartupReport
	Client  ClientRunResult
}

// Run has no caller-provided permission assertion, backend, or policy adapter. Only a built-in,
// independently verified adapter can admit model traffic. The current adapter enables development
// initial sessions on the measured CLI/engine/platform; it does not enable persisted-session loading.
func Run(ctx context.Context, opts LaunchOptions, files childproc.AttachedIO) (LaunchResult, error) {
	return start(ctx, opts, files, false, productionStartup())
}

// Inspect performs bounded executable/account/catalog checks and reports unresolved launch gates.
// It does not create an ACP session, open a model endpoint, launch the client, or save a preference.
// Compatible catalog metadata and the scope key are retained in the private product state directory.
func Inspect(ctx context.Context, opts LaunchOptions) (StartupReport, error) {
	result, err := start(ctx, opts, childproc.AttachedIO{}, true, productionStartup())
	return result.Startup, err
}

type startupRunner interface {
	CommandRunner
	Close()
}
type launchPolicy struct {
	process acp.Config
	prepare session.PrepareLaunch
}

// Test services are package-private and cannot be selected from CLI options, environment or files.
type startupServices struct {
	runner func() (startupRunner, error)
	policy func(context.Context, LaunchOptions, KiroInfo) (launchPolicy, error)
	client func(context.Context, ClientRunConfig) (ClientRunResult, error)
}

func productionStartup() startupServices {
	return startupServices{runner: func() (startupRunner, error) {
		inner, err := childproc.New(childproc.Config{Timeout: 15 * time.Second})
		if err != nil {
			return nil, err
		}
		return &startupCommandRunner{inner: inner, ordinary: 5 * time.Second, catalog: 15 * time.Second}, nil
	}, policy: preparedKiroPolicy, client: RunClient}
}

// The installed public listing command can emit JSON well before exiting. Require its successful
// bounded exit; partial stdout from a timed-out process cannot become a trusted catalog snapshot.
type startupCommandRunner struct {
	inner             *childproc.Runner
	ordinary, catalog time.Duration
}

func (r *startupCommandRunner) Close() { r.inner.Close() }
func (r *startupCommandRunner) Run(ctx context.Context, command childproc.Command) (childproc.Result, error) {
	limit := r.ordinary
	if len(command.Args) == 4 && command.Args[0] == "chat" && command.Args[1] == "--list-models" && command.Args[2] == "--format" && command.Args[3] == "json" {
		limit = r.catalog
	}
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return r.inner.Run(bounded, command)
}
func unverifiedKiroPolicy(ctx context.Context, _ LaunchOptions, _ KiroInfo) (launchPolicy, error) {
	if ctx.Err() != nil {
		return launchPolicy{}, ctx.Err()
	}
	return launchPolicy{}, ErrPolicyUnverified
}

func preparedKiroPolicy(ctx context.Context, opts LaunchOptions, info KiroInfo) (launchPolicy, error) {
	execution, err := PrepareKiroExecution(ctx, KiroExecutionConfig{Installation: info, Home: opts.Home, Project: opts.Project, RuntimeDirectory: opts.RuntimeParent})
	return launchPolicy{process: execution.Process, prepare: execution.Prepare}, err
}

func start(ctx context.Context, opts LaunchOptions, files childproc.AttachedIO, inspect bool, services startupServices) (result LaunchResult, runErr error) {
	result.Startup = StartupReport{Login: "unchecked", Policy: "unchecked", ClientInitialization: "unverified", Phases: []PhaseTiming{}}
	result.Client.ExitCode = -1
	var runtime string
	var runtimeInfo os.FileInfo
	var runner startupRunner
	var models *ModelState
	var schema *schemacheck.Pool
	var backend *session.Manager
	var metrics *status.TurnQueue
	var usage *status.UsageCache
	var identity catalog.Identity
	transferred := false
	defer func() {
		// Catalog refresh owns finite commands; the runner and runtime outlive its joined cancellation.
		if !transferred {
			if usage != nil && usage.Close() != nil {
				runErr = errors.Join(runErr, ErrRunCleanup)
			}
			if backend != nil && backend.Close() != nil {
				runErr = errors.Join(runErr, ErrRunCleanup)
			}
			if schema != nil {
				schema.Close()
			}
			if models != nil {
				models.Close()
			}
		}
		if runner != nil {
			runner.Close()
		}
		if runtime != "" {
			info, err := os.Lstat(runtime)
			if err != nil || runtimeInfo == nil || !info.IsDir() || !os.SameFile(info, runtimeInfo) || info.Mode().Perm() != 0700 || os.RemoveAll(runtime) != nil {
				runErr = errors.Join(runErr, ErrRunCleanup)
			}
		}
		// Caller cancellation can arrive while the final refresh/runner/filesystem owners join.
		// Preserve it alongside any fixed cleanup failure for direct API callers as well as the CLI.
		if ctx.Err() != nil && !errors.Is(runErr, ctx.Err()) {
			runErr = errors.Join(runErr, ctx.Err())
		}
	}()
	phase := func(name string, fn func() error) error {
		started := time.Now()
		err := fn()
		result.Startup.Phases = append(result.Startup.Phases, PhaseTiming{name, time.Since(started).Milliseconds()})
		return err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if services.runner == nil || services.policy == nil || services.client == nil {
		return result, ErrConfig
	}
	setup, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var err error
	if err = phase("binary_settings", func() error {
		opts, err = normalizeLaunchOptions(opts)
		if err != nil {
			return err
		}
		user, err := readSettings(opts.UserSettings)
		if err != nil {
			return err
		}
		_, err = isolatedSettings(user)
		return err
	}); err != nil {
		return result, err
	}
	if err = phase("runtime_preparation", func() error {
		runtime, err = os.MkdirTemp(opts.RuntimeParent, "dax-startup-")
		if err != nil {
			return ErrRuntime
		}
		runtimeInfo, err = os.Lstat(runtime)
		if err != nil {
			return ErrRuntime
		}
		for _, name := range []string{"checks", "schema"} {
			if os.Mkdir(filepath.Join(runtime, name), 0700) != nil {
				return ErrRuntime
			}
		}
		runner, err = services.runner()
		if err != nil || runner == nil {
			return ErrRuntime
		}
		return nil
	}); err != nil {
		return result, err
	}
	var key [32]byte
	if err = phase("state", func() error { key, err = loadScopeKey(setup, opts.StateDirectory); return err }); err != nil {
		return result, err
	}
	if err = phase("client_version", func() error {
		check := childproc.Command{Executable: opts.ClientExecutable, Directory: filepath.Join(runtime, "checks"), Args: []string{"--version"}, Environment: []string{"HOME=" + opts.Home, "TMPDIR=" + filepath.Join(runtime, "checks"), "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1"}}
		output, e := runner.Run(setup, check)
		if setup.Err() != nil {
			return setup.Err()
		}
		version, named := parseClientVersion(output.Stdout)
		if e != nil || output.ExitCode != 0 || !named {
			return ErrClientVersion
		}
		if !CompatibleClientVersion(version) {
			return &VersionError{Component: "Claude Code", Found: version, Expected: "major version " + majorOf(SupportedClientVersion), Sentinel: ErrClientVersion}
		}
		result.Startup.ClientVersion, result.Startup.ClientVersionMeasured = version, version == SupportedClientVersion
		return nil
	}); err != nil {
		return result, err
	}
	kiroCfg := KiroConfig{Executable: opts.KiroExecutable, Home: opts.Home, Directory: filepath.Join(runtime, "checks"), ScopeKey: key}
	var info KiroInfo
	if err = phase("login_check", func() error {
		info, err = CheckKiro(setup, runner, kiroCfg)
		if err != nil {
			return err
		}
		result.Startup.KiroVersion, result.Startup.KiroVersionMeasured = info.Version, info.Version == SupportedKiroVersion
		result.Startup.Login = "verified"
		return nil
	}); err != nil {
		return result, err
	}
	if err = phase("model_catalog", func() error {
		identity, err = makeLaunchIdentity(key, opts.Home, info, opts.InitialModel, opts.InitialEffort)
		if err != nil {
			return err
		}
		models, err = PrepareModels(setup, ModelConfig{Cache: catalog.CacheConfig{Directory: filepath.Join(opts.StateDirectory, "models"), Identity: identity}, Interactive: opts.Interactive, Discover: func(ctx context.Context) (*catalog.Catalog, error) { return ReadKiroCatalog(ctx, runner, kiroCfg) }})
		if setup.Err() != nil {
			return setup.Err()
		}
		if err != nil {
			return err
		}
		result.Startup.Models, err = models.Models(setup)
		selected := models.Selection()
		result.Startup.SelectedModel, result.Startup.ModelSource, result.Startup.CatalogStale = selected.Client, selected.Source, selected.Stale
		return err
	}); err != nil {
		return result, err
	}
	var policy launchPolicy
	err = phase("execution_policy", func() error {
		policyOptions := opts
		policyOptions.RuntimeParent = runtime
		policy, err = services.policy(setup, policyOptions, info)
		if setup.Err() != nil {
			return setup.Err()
		}
		if err != nil {
			return err
		}
		if policy.prepare == nil {
			return ErrPolicyUnverified
		}
		return nil
	})
	if errors.Is(err, ErrPolicyUnverified) {
		result.Startup.Policy = "unverified"
		if inspect {
			return result, nil
		}
		return result, err
	}
	if err != nil {
		return result, err
	}
	result.Startup.Policy = "verified"
	result.Startup.LaunchAvailable = true
	if inspect {
		return result, nil
	}
	if err = phase("backend_preparation", func() error {
		if setup.Err() != nil {
			return setup.Err()
		}
		schema, err = schemacheck.New(schemacheck.Config{Executable: opts.ProxyExecutable, Directory: filepath.Join(runtime, "schema")})
		if err != nil {
			return ErrRuntime
		}
		metrics = status.NewTurnQueue()
		backend, err = session.NewManager(session.ManagerConfig{ProfileScope: identity.ProfileDigest, BackendVersion: info.Version, Metrics: metrics, Session: session.Config{Process: policy.process, InitialModel: opts.InitialModel, InitialEffort: opts.InitialEffort, Validator: schema, RelayExecutable: opts.ProxyExecutable, HistoryKey: key, PrepareLaunch: policy.prepare, TurnTimeout: opts.TurnTimeout, RelayLimits: relay.Limits{ToolTimeout: opts.ToolTimeout}}})
		if err != nil {
			return ErrRuntime
		}
		usage, err = NewKiroUsageCache(KiroUsageConfig{Installation: info, Home: opts.Home, RuntimeParent: opts.RuntimeParent, ScopeKey: key})
		if err != nil {
			return ErrRuntime
		}
		return nil
	}); err != nil {
		return result, err
	}
	if setup.Err() != nil {
		return result, setup.Err()
	}
	cancel()
	// RunClient takes ownership even when it rejects its configuration or parent is now canceled.
	transferred = true
	result.Client, err = services.client(ctx, ClientRunConfig{Backend: backend, Models: models, Schema: schema, Client: ClientConfig{RuntimeParent: runtime, Home: opts.Home, Project: opts.Project, UserSettings: opts.UserSettings, Executable: opts.ClientExecutable, StatusExecutable: opts.ProxyExecutable, Version: result.Startup.ClientVersion, Environment: opts.Environment, KeepHistory: opts.KeepHistory, ResumeSession: opts.ResumeSession}, IO: files, Server: gateway.ServerConfig{Gateway: gateway.Config{Metrics: metrics, Usage: usage, FirstEventTimeout: opts.FirstEventTimeout, TurnTimeout: opts.TurnTimeout}}, Attached: childproc.AttachedConfig{Lifetime: ClientLifetime}})
	result.Startup.Phases = append(result.Startup.Phases, PhaseTiming{"gateway_startup", result.Client.GatewayTime.Milliseconds()}, PhaseTiming{"client_profile", result.Client.ProfileTime.Milliseconds()}, PhaseTiming{"process_launch", result.Client.LaunchTime.Milliseconds()}, PhaseTiming{"runtime_cleanup", result.Client.CleanupTime.Milliseconds()})
	return result, err
}

func normalizeLaunchOptions(opts LaunchOptions) (LaunchOptions, error) {
	if opts.ToolTimeout == 0 {
		opts.ToolTimeout = DefaultToolTimeout
	}
	if opts.TurnTimeout == 0 {
		opts.TurnTimeout = DefaultTurnTimeout
	}
	if opts.FirstEventTimeout == 0 {
		opts.FirstEventTimeout = DefaultFirstEventTimeout
	}
	if opts.ToolTimeout < 30*time.Second || opts.ToolTimeout > time.Hour || opts.TurnTimeout < time.Minute || opts.TurnTimeout > time.Hour || opts.ToolTimeout > opts.TurnTimeout || opts.FirstEventTimeout < 10*time.Second || opts.FirstEventTimeout > opts.TurnTimeout {
		return LaunchOptions{}, ErrConfig
	}
	if opts.ResumeSession != "" {
		if !validNativeSessionID(opts.ResumeSession) {
			return LaunchOptions{}, ErrConfig
		}
		opts.KeepHistory = true
	}
	for _, path := range []string{opts.Home, opts.Project, opts.RuntimeParent, opts.StateDirectory, opts.ProxyExecutable} {
		if !safeLaunchPath(path) {
			return LaunchOptions{}, ErrConfig
		}
	}
	for _, path := range []string{opts.Home, opts.Project, opts.RuntimeParent} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return LaunchOptions{}, ErrConfig
		}
	}
	if len(opts.InitialModel) > 256 || opts.InitialEffort != "" && kirofeature.Normalize(opts.InitialEffort) == "" {
		return LaunchOptions{}, ErrConfig
	}
	if opts.InitialEffort != "" {
		opts.InitialEffort = kirofeature.Normalize(opts.InitialEffort)
	}
	if opts.UserSettings == "" {
		opts.UserSettings = filepath.Join(opts.Home, ".claude", "settings.json")
	}
	if !safeLaunchPath(opts.UserSettings) {
		return LaunchOptions{}, ErrConfig
	}
	env, err := platformEnvironment(opts.Environment)
	if err != nil {
		return LaunchOptions{}, ErrConfig
	}
	opts.Environment = append([]string{}, opts.Environment...)
	opts.ClientExecutable, err = resolveLaunchExecutable(opts.ClientExecutable, "claude", env["PATH"])
	if err != nil {
		return LaunchOptions{}, errors.Join(ErrClientVersion, ErrExecutableNotFound)
	}
	opts.KiroExecutable, err = resolveLaunchExecutable(opts.KiroExecutable, "kiro-cli", env["PATH"])
	if err != nil {
		return LaunchOptions{}, errors.Join(ErrKiroVersion, ErrExecutableNotFound)
	}
	if _, err = resolveLaunchExecutable(opts.ProxyExecutable, "", ""); err != nil {
		return LaunchOptions{}, ErrConfig
	}
	return opts, nil
}
func safeLaunchPath(path string) bool {
	return filepath.IsAbs(path) && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n")
}
func resolveLaunchExecutable(path, name, search string) (string, error) {
	valid := func(path string) bool {
		if !safeLaunchPath(path) {
			return false
		}
		info, err := os.Stat(path)
		return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
	}
	if path != "" {
		if valid(path) {
			return filepath.Clean(path), nil
		}
		return "", ErrConfig
	}
	dirs := filepath.SplitList(search)
	if len(dirs) > 128 {
		return "", ErrConfig
	}
	for _, dir := range dirs {
		if !safeLaunchPath(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if valid(candidate) {
			return candidate, nil
		}
	}
	return "", ErrConfig
}
