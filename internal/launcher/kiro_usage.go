package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/status"
)

type KiroUsageConfig struct {
	Installation        KiroInfo
	Home, RuntimeParent string
	ScopeKey            [32]byte
}

type usageProcess interface {
	kirofeature.UsagePeer
	Close() error
}
type usageServices struct {
	runner  func() (startupRunner, error)
	start   func(context.Context, acp.Config) (usageProcess, error)
	now     func() time.Time
	timeout time.Duration
}

// NewKiroUsageCache is lazy: neither construction nor shutdown creates a process. Each refresh
// owns fresh temporary state, three finite identity checks and one empty-agent ACP session.
// RuntimeParent must outlive the cache; failed cleanup preserves its root directly under that
// parent, separate from model/client runtime removal, and prevents further refresh admission.
func NewKiroUsageCache(cfg KiroUsageConfig) (*status.UsageCache, error) {
	return newKiroUsageCache(cfg, productionUsageServices())
}

func productionUsageServices() usageServices {
	return usageServices{
		runner: func() (startupRunner, error) {
			return childproc.New(childproc.Config{MaxProcesses: 1, Timeout: 5 * time.Second})
		},
		start: func(ctx context.Context, cfg acp.Config) (usageProcess, error) {
			client, err := acp.Start(ctx, cfg)
			if err != nil {
				return nil, err
			}
			return client, nil
		},
	}
}

func newKiroUsageCache(cfg KiroUsageConfig, services usageServices) (*status.UsageCache, error) {
	if cfg.Installation.Version != kiroDevelopmentVersion || cfg.Installation.ProfileScope == "" || len(cfg.Installation.ProfileScope) > 64 || cfg.ScopeKey == ([32]byte{}) || services.runner == nil || services.start == nil {
		return nil, ErrConfig
	}
	for _, path := range []string{cfg.Home, cfg.RuntimeParent} {
		if !identityPath(path) {
			return nil, ErrConfig
		}
	}
	parent, err := os.Lstat(cfg.RuntimeParent)
	if err != nil || !parent.IsDir() {
		return nil, ErrRuntime
	}
	if services.timeout == 0 {
		services.timeout = 15 * time.Second
	}
	owner := &kiroUsageOwner{cfg: cfg, services: services, parent: parent}
	return status.NewUsageCache(status.UsageConfig{Fetch: owner.fetch, Close: func() error { return owner.cleanupErr }, TTL: time.Minute, Timeout: services.timeout, Now: services.now})
}

// Access is serialized by UsageCache; Close reads cleanupErr only after its worker joins.
type kiroUsageOwner struct {
	cfg        KiroUsageConfig
	services   usageServices
	parent     os.FileInfo
	cleanupErr error
}

func (o *kiroUsageOwner) fetch(ctx context.Context) (data status.UsageData, fetchErr error) {
	if ctx.Err() != nil {
		return data, ctx.Err()
	}
	if o.cleanupErr != nil {
		return data, kirofeature.ErrUsageUnavailable
	}
	parent, err := os.Lstat(o.cfg.RuntimeParent)
	if err != nil || !parent.IsDir() || !os.SameFile(parent, o.parent) {
		return data, kirofeature.ErrUsageUnavailable
	}
	root, err := os.MkdirTemp(o.cfg.RuntimeParent, "dax-account-usage-")
	if err != nil {
		return data, kirofeature.ErrUsageUnavailable
	}
	original, statErr := os.Lstat(root)
	var runner startupRunner
	var peer usageProcess
	defer func() {
		if peer != nil && peer.Close() != nil {
			o.cleanupErr = ErrRunCleanup
		}
		if runner != nil {
			runner.Close()
		}
		if o.cleanupErr == nil {
			current, err := os.Lstat(root)
			if err != nil || original == nil || !current.IsDir() || current.Mode().Perm() != 0700 || !os.SameFile(original, current) || os.RemoveAll(root) != nil {
				o.cleanupErr = ErrRunCleanup
			}
		}
		if o.cleanupErr != nil || fetchErr != nil {
			data = status.UsageData{}
			fetchErr = kirofeature.ErrUsageUnavailable
		}
	}()
	if statErr != nil {
		return data, kirofeature.ErrUsageUnavailable
	}
	work := filepath.Join(root, "work")
	if os.Mkdir(work, 0700) != nil {
		return data, kirofeature.ErrUsageUnavailable
	}
	execution, err := PrepareKiroExecution(ctx, KiroExecutionConfig{Installation: o.cfg.Installation, Home: o.cfg.Home, Project: work, RuntimeDirectory: root})
	if err != nil {
		return data, err
	}
	agents, err := privatefs.New(filepath.Join(work, ".kiro", "agents"))
	if err != nil {
		return data, err
	}
	const agent = `{"name":"dax-account-usage","description":"Read-only account usage query","tools":[],"allowedTools":[],"mcpServers":{},"resources":[],"hooks":{},"includeMcpJson":false}`
	if agents.Write("dax-account-usage.json", []byte(agent)) != nil {
		return data, kirofeature.ErrUsageUnavailable
	}
	process := execution.Process
	process.Args = append(process.Args, "--agent", "dax-account-usage")
	process.Limits = acp.Limits{FrameBytes: 128 << 10, Pending: 4, EventQueue: 64, EventBytes: 1 << 20, WriteQueue: 4, WriteBytes: 128 << 10, RequestTimeout: 15 * time.Second}
	runner, err = o.services.runner()
	if err != nil || runner == nil {
		return data, kirofeature.ErrUsageUnavailable
	}
	checked := usageIdentityRunner{runner: runner, environment: process.Environment, cleanup: &o.cleanupErr}
	info, err := CheckKiro(ctx, checked, KiroConfig{Executable: o.cfg.Installation.Executable, Home: o.cfg.Home, Directory: work, ScopeKey: o.cfg.ScopeKey})
	if err != nil || info.ProfileScope != o.cfg.Installation.ProfileScope {
		return data, kirofeature.ErrUsageUnavailable
	}
	peer, err = o.services.start(ctx, process)
	if err != nil {
		if errors.Is(err, acp.ErrCleanup) {
			o.cleanupErr = ErrRunCleanup
		}
		return data, err
	}
	if peer == nil {
		return data, kirofeature.ErrUsageUnavailable
	}
	usage, err := kirofeature.ReadAccountUsage(ctx, peer, work)
	if err != nil {
		return data, err
	}
	return status.UsageData{Used: usage.Used, Limit: usage.Limit}, nil
}

type usageIdentityRunner struct {
	runner      CommandRunner
	environment []string
	cleanup     *error
}

func (r usageIdentityRunner) Run(ctx context.Context, c childproc.Command) (childproc.Result, error) {
	c.Environment = append([]string(nil), r.environment...)
	result, err := r.runner.Run(ctx, c)
	if errors.Is(err, childproc.ErrCleanup) {
		*r.cleanup = ErrRunCleanup
	}
	return result, err
}
