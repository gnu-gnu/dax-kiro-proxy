package launcher

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/kiroauth"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/session"
)

type KiroExecutionConfig struct {
	Installation                    KiroInfo
	Home, Project, RuntimeDirectory string
}

// The development policy is measured on this Kiro build; a same-major build runs it unmeasured
// (D114). Changing the preflight pin alone must not admit a different execution policy.
const kiroDevelopmentVersion = "2.21.3"
const kiroDevelopmentPolicy = "dax-kiro-initial-v2:2.21.3:darwin-arm64:relay-metadata-v2:owned-default-resources-off"

type KiroExecution struct {
	Process acp.Config
	Prepare session.PrepareLaunch
}

// PrepareKiroExecution prepares the D59-D61 initial-session development policy. The caller owns
// RuntimeDirectory and must remove it only after all session/process cleanup has joined. It never
// reads or copies credentials and supplies no persisted-session load or native-tool configuration.
func PrepareKiroExecution(ctx context.Context, cfg KiroExecutionConfig) (KiroExecution, error) {
	if ctx.Err() != nil {
		return KiroExecution{}, ctx.Err()
	}
	info := cfg.Installation
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" || !compatibleMajor(info.Version, kiroDevelopmentVersion) {
		return KiroExecution{}, ErrPolicyUnverified
	}
	if info.Helper != filepath.Join(filepath.Dir(info.Executable), "kiro-cli-chat") {
		return KiroExecution{}, ErrConfig
	}
	for _, path := range []string{cfg.Home, cfg.Project, cfg.RuntimeDirectory} {
		if !identityPath(path) {
			return KiroExecution{}, ErrConfig
		}
		stat, err := os.Stat(path)
		if err != nil || !stat.IsDir() {
			return KiroExecution{}, ErrConfig
		}
	}
	for _, path := range []string{info.Executable, info.Helper} {
		if _, err := resolveLaunchExecutable(path, "", ""); err != nil {
			return KiroExecution{}, ErrConfig
		}
	}
	if _, err := privatefs.Open(cfg.RuntimeDirectory); err != nil {
		return KiroExecution{}, ErrRuntime
	}
	root, err := os.MkdirTemp(cfg.RuntimeDirectory, "kiro-execution-")
	if err != nil {
		return KiroExecution{}, ErrRuntime
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(root)
		}
	}()
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return KiroExecution{}, ErrRuntime
	}
	home, scratch, launches := filepath.Join(root, "profile"), filepath.Join(root, "tmp"), filepath.Join(root, "agents")
	for _, path := range []string{home, filepath.Join(home, "settings"), scratch, launches} {
		if os.Mkdir(path, 0700) != nil {
			return KiroExecution{}, ErrRuntime
		}
	}
	settings, err := privatefs.Open(filepath.Join(home, "settings"))
	const suppression = `{"chat.disableInheritingDefaultResources":true}`
	if err != nil || settings.Write("cli.json", []byte(suppression)) != nil {
		return KiroExecution{}, ErrRuntime
	}
	execution := KiroExecution{Process: acp.Config{
		Executable: info.Executable, Directory: cfg.Project, Args: []string{"acp", "--agent-engine", "v2"},
		Environment: []string{"HOME=" + cfg.Home, "KIRO_HOME=" + home, "PATH=" + filepath.Dir(info.Executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"},
		ClientInfo:  acp.Info{Name: "dax-kiro-proxy", Version: "1"}, Auth: kiroauth.Classifier{},
	}}
	execution.Prepare = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		if ctx.Err() != nil {
			return session.LaunchResources{}, ctx.Err()
		}
		current, err := os.Lstat(root)
		if err != nil || !os.SameFile(rootInfo, current) || !current.IsDir() || current.Mode().Perm() != 0700 {
			return session.LaunchResources{}, ErrRuntime
		}
		data, err := settings.Read("cli.json", 128)
		if err != nil || string(data) != suppression {
			return session.LaunchResources{}, ErrRuntime
		}
		if input.Registry == nil || !identityPath(input.RelayExecutable) || !identityPath(input.RelayConfig) {
			return session.LaunchResources{}, ErrConfig
		}
		if _, err := privatefs.Open(launches); err != nil {
			return session.LaunchResources{}, ErrRuntime
		}
		directory, err := os.MkdirTemp(launches, "session-")
		if err != nil {
			return session.LaunchResources{}, ErrRuntime
		}
		original, err := os.Lstat(directory)
		if err != nil {
			_ = os.RemoveAll(directory)
			return session.LaunchResources{}, ErrRuntime
		}
		var once sync.Once
		var cleanupErr error
		owned := session.LaunchResources{Directory: directory, RelayAtLaunch: true, Cleanup: func() error {
			once.Do(func() {
				current, err := os.Lstat(directory)
				if err != nil || !os.SameFile(original, current) || !current.IsDir() || current.Mode().Perm() != 0700 || os.RemoveAll(directory) != nil {
					cleanupErr = ErrRuntime
				}
			})
			return cleanupErr
		}}
		agent, err := WriteCandidateAgent(AgentConfig{Directory: directory, Registry: input.Registry, RelayExecutable: input.RelayExecutable, RelayConfig: input.RelayConfig})
		if err != nil {
			return owned, err
		}
		owned.Args = []string{"--agent", agent.Name}
		return owned, ctx.Err()
	}
	if ctx.Err() != nil {
		return KiroExecution{}, ctx.Err()
	}
	ok = true
	return execution, nil
}
