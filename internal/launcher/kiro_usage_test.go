package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/status"
)

type usageLifecyclePeer struct {
	directory string
	stage     int
	closed    bool
	hold      bool
	cleanup   error
}

func (p *usageLifecyclePeer) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.stage++
	switch p.stage {
	case 1:
		if method != "session/new" {
			return nil, errors.New("expected new usage session")
		}
		return json.RawMessage(`{"sessionId":"usage-owned"}`), nil
	case 2:
		return json.RawMessage(`{"success":true,"data":{"tools":[]}}`), nil
	case 3:
		if p.hold {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return json.RawMessage(`{"success":true,"data":{"usageBreakdowns":[{"resourceType":"CREDIT","used":7,"hasLimit":true,"limit":90}]}}`), nil
	}
	return nil, errors.New("unexpected usage RPC")
}
func (p *usageLifecyclePeer) Next(context.Context) (acp.Notification, error) {
	return acp.Notification{Method: "_kiro.dev/commands/available", Params: json.RawMessage(`{"sessionId":"usage-owned","commands":[{"name":"usage"},{"name":"tools"}]}`)}, nil
}
func (p *usageLifecyclePeer) Close() error {
	p.closed = true
	if _, err := os.Stat(p.directory); err != nil {
		return ErrRuntime
	}
	return p.cleanup
}

type usageLifecycleRunner struct {
	commands []childproc.Command
	closed   bool
	mismatch bool
	cleanup  bool
}

func (r *usageLifecycleRunner) Close() { r.closed = true }
func (r *usageLifecycleRunner) Run(_ context.Context, c childproc.Command) (childproc.Result, error) {
	r.commands = append(r.commands, c)
	if r.cleanup {
		return childproc.Result{}, childproc.ErrCleanup
	}
	if c.Args[0] == "--version" {
		return childproc.Result{Stdout: []byte(filepath.Base(c.Executable) + " 2.21.3\n")}, nil
	}
	email := "usage@example.invalid"
	if r.mismatch {
		email = "other@example.invalid"
	}
	return childproc.Result{Stdout: []byte(`{"accountType":"synthetic","email":"` + email + `"}`)}, nil
}

func usageLifecycleConfig(t *testing.T) (KiroUsageConfig, *usageLifecycleRunner) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("usage process policy is measured on macOS arm64")
	}
	opts := startupOptions(t)
	cfg := KiroUsageConfig{Home: opts.Home, RuntimeParent: opts.RuntimeParent, ScopeKey: [32]byte{1}}
	r := new(usageLifecycleRunner)
	info, err := CheckKiro(t.Context(), r, KiroConfig{Executable: filepath.Join(filepath.Dir(opts.ProxyExecutable), "kiro-cli"), Home: opts.Home, Directory: opts.Project, ScopeKey: cfg.ScopeKey})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Installation = info
	r.commands = nil
	return cfg, r
}

func awaitLauncherUsage(t *testing.T, c *status.UsageCache) status.UsageSnapshot {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		s := c.Read()
		if !s.Refreshing {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("usage did not settle")
	return status.UsageSnapshot{}
}

func TestKiroUsageOwnsSeparateEmptyAgentAndJoinsEveryRefresh(t *testing.T) {
	cfg, r := usageLifecycleConfig(t)
	var peer *usageLifecyclePeer
	var starts atomic.Int32
	var clock atomic.Int64
	c, err := newKiroUsageCache(cfg, usageServices{runner: func() (startupRunner, error) { return r, nil }, start: func(_ context.Context, c acp.Config) (usageProcess, error) {
		starts.Add(1)
		if c.Directory == cfg.Home || !strings.HasPrefix(c.Directory, cfg.RuntimeParent+"/") {
			t.Error("usage borrowed the user workspace")
		}
		env := map[string]string{}
		for _, v := range c.Environment {
			k, v, _ := strings.Cut(v, "=")
			env[k] = v
		}
		if len(env) != 6 || env["HOME"] != cfg.Home || !strings.HasPrefix(env["KIRO_HOME"], cfg.RuntimeParent+"/") {
			t.Error("usage inherited ambient configuration")
		}
		settings, err := os.ReadFile(filepath.Join(env["KIRO_HOME"], "settings", "cli.json"))
		if err != nil || string(settings) != `{"chat.disableInheritingDefaultResources":true}` {
			t.Error("usage resource suppression missing")
		}
		agent, err := os.ReadFile(filepath.Join(c.Directory, ".kiro", "agents", "dax-account-usage.json"))
		var parsed struct {
			Tools, AllowedTools, Resources []any
			MCPServers, Hooks              map[string]any
			IncludeMcpJson                 bool
		}
		if err != nil || json.Unmarshal(agent, &parsed) != nil || parsed.Tools == nil || len(parsed.Tools) != 0 || parsed.AllowedTools == nil || len(parsed.AllowedTools) != 0 || parsed.Resources == nil || len(parsed.Resources) != 0 || parsed.MCPServers == nil || len(parsed.MCPServers) != 0 || len(parsed.Hooks) != 0 || parsed.IncludeMcpJson {
			t.Error("usage agent can execute or inherit resources")
		}
		peer = &usageLifecyclePeer{directory: c.Directory}
		return peer, nil
	}, now: func() time.Time { return time.Unix(1700000000, 0).Add(time.Duration(clock.Load())) }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := range 2 {
		clock.Store(int64(time.Duration(i) * 61 * time.Second))
		s := awaitLauncherUsage(t, c)
		if !s.Available || s.Data.Used == nil || *s.Data.Used != 7 || s.Data.Remaining != nil || starts.Load() != int32(i+1) || !peer.closed || !r.closed {
			t.Fatal("usage data or joined lifecycle missing")
		}
		entries, _ := os.ReadDir(cfg.RuntimeParent)
		if len(entries) != 0 {
			t.Fatal("usage retained session files")
		}
	}
	if c.Close() != nil {
		t.Fatal("usage close failed")
	}
	c.Read()
	if starts.Load() != 2 {
		t.Fatal("late status reader recreated a usage owner")
	}
}

func TestKiroUsageFailuresNeverAdmitUnboundedOwners(t *testing.T) {
	for _, mode := range []string{"identity", "runner-cleanup", "start-cleanup", "peer-cleanup", "timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			cfg, r := usageLifecycleConfig(t)
			r.mismatch = mode == "identity"
			r.cleanup = mode == "runner-cleanup"
			var starts atomic.Int32
			var offset atomic.Int64
			entered := make(chan struct{})
			c, err := newKiroUsageCache(cfg, usageServices{runner: func() (startupRunner, error) { return r, nil }, start: func(_ context.Context, c acp.Config) (usageProcess, error) {
				starts.Add(1)
				close(entered)
				if mode == "start-cleanup" {
					return nil, acp.ErrCleanup
				}
				p := &usageLifecyclePeer{directory: c.Directory, hold: mode == "timeout" || mode == "cancel"}
				if mode == "peer-cleanup" {
					p.cleanup = errors.New("private-cleanup-value")
				}
				return p, nil
			}, timeout: 100 * time.Millisecond, now: func() time.Time { return time.Unix(1700000000, 0).Add(time.Duration(offset.Load())) }})
			if err != nil {
				t.Fatal(err)
			}
			c.Read()
			if mode == "cancel" {
				<-entered
			} else {
				if s := awaitLauncherUsage(t, c); s.Available || s.State != "refresh_failed" {
					t.Fatal("failed usage reported current")
				}
			}
			retained := strings.Contains(mode, "cleanup")
			if retained {
				before := starts.Load()
				offset.Store(int64(61 * time.Second))
				if s := awaitLauncherUsage(t, c); s.Available || starts.Load() != before {
					t.Fatal("cleanup failure admitted another owner")
				}
			}
			err = c.Close()
			if (err != nil) != retained || err != nil && strings.Contains(err.Error(), "private-cleanup-value") {
				t.Fatal("cleanup failure lost or disclosed", err)
			}
			entries, _ := os.ReadDir(cfg.RuntimeParent)
			if (len(entries) != 0) != retained || len(entries) > 1 {
				t.Fatal("incorrect usage artifact ownership")
			}
			if (mode == "identity" || mode == "runner-cleanup") && starts.Load() != 0 {
				t.Fatal("failed preflight started ACP")
			}
			if !r.closed {
				t.Fatal("preflight runner not joined")
			}
		})
	}
}
