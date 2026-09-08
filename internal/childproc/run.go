// Package childproc owns bounded noninteractive CLI checks with no ambient environment or logs.
package childproc

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrParameters  = errors.New("invalid CLI command or limits")
	ErrClosed      = errors.New("CLI runner is closed")
	ErrBusy        = errors.New("CLI process capacity reached")
	ErrStart       = errors.New("cannot start CLI process")
	ErrExit        = errors.New("CLI process exited unsuccessfully")
	ErrOutputLimit = errors.New("CLI output exceeds its limit")
	ErrIO          = errors.New("CLI output pipe failed")
	ErrCleanup     = errors.New("CLI process cleanup did not complete")
)

type Config struct {
	MaxProcesses, MaxOutputBytes                 int
	Timeout, GracePeriod, TermPeriod, KillPeriod time.Duration
}
type Command struct {
	Executable, Directory string
	Args, Environment     []string
}
type Result struct {
	Stdout        []byte
	ExitCode, PID int
}
type Runner struct {
	cfg    Config
	mu     sync.Mutex
	active int
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	work   sync.WaitGroup
}

func New(cfg Config) (*Runner, error) {
	if cfg.MaxProcesses == 0 {
		cfg.MaxProcesses = 2
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = 64 << 10
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.GracePeriod == 0 {
		cfg.GracePeriod = 100 * time.Millisecond
	}
	if cfg.TermPeriod == 0 {
		cfg.TermPeriod = 250 * time.Millisecond
	}
	if cfg.KillPeriod == 0 {
		cfg.KillPeriod = time.Second
	}
	if cfg.MaxProcesses < 1 || cfg.MaxProcesses > 16 || cfg.MaxOutputBytes < 1 || cfg.MaxOutputBytes > 4<<20 || cfg.Timeout <= 0 || cfg.Timeout > time.Minute {
		return nil, ErrParameters
	}
	for _, d := range []time.Duration{cfg.GracePeriod, cfg.TermPeriod, cfg.KillPeriod} {
		if d <= 0 || d > 5*time.Second {
			return nil, ErrParameters
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{cfg: cfg, ctx: ctx, cancel: cancel}, nil
}
func (r *Runner) Active() int { r.mu.Lock(); defer r.mu.Unlock(); return r.active }
func (r *Runner) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.work.Wait()
}
func validCommand(c Command) bool {
	if !filepath.IsAbs(c.Executable) || !filepath.IsAbs(c.Directory) || len(c.Args) > 512 || len(c.Environment) > 128 || strings.ContainsRune(c.Executable, 0) || strings.ContainsRune(c.Directory, 0) {
		return false
	}
	bytes := len(c.Executable) + len(c.Directory)
	for _, a := range c.Args {
		bytes += len(a)
		if strings.ContainsRune(a, 0) {
			return false
		}
	}
	keys := map[string]bool{}
	for _, value := range c.Environment {
		bytes += len(value)
		key, _, ok := strings.Cut(value, "=")
		if !ok || key == "" || keys[key] || strings.ContainsRune(value, 0) {
			return false
		}
		for i, c := range []byte(key) {
			if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
				return false
			}
		}
		keys[key] = true
	}
	return bytes <= 64<<10
}
func (r *Runner) Run(ctx context.Context, command Command) (Result, error) {
	result := Result{ExitCode: -1}
	if !validCommand(command) {
		return result, ErrParameters
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return result, ErrClosed
	}
	if r.active == r.cfg.MaxProcesses {
		r.mu.Unlock()
		return result, ErrBusy
	}
	r.active++
	r.work.Add(1)
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock(); r.work.Done() }()
	owned, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	stop := context.AfterFunc(r.ctx, cancel)
	defer func() { stop(); cancel() }()
	if r.ctx.Err() != nil {
		cancel()
	}
	cmd := exec.Command(command.Executable, command.Args...)
	cmd.Dir = command.Directory
	cmd.Env = append([]string{}, command.Environment...)
	if configure(cmd) != nil {
		return result, ErrParameters
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return result, ErrStart
	}
	defer outR.Close()
	defer outW.Close()
	errR, errW, err := os.Pipe()
	if err != nil {
		return result, ErrStart
	}
	defer errR.Close()
	defer errW.Close()
	cmd.Stdout, cmd.Stderr = outW, errW
	if owned.Err() != nil {
		return result, owned.Err()
	}
	if cmd.Start() != nil {
		return result, ErrStart
	}
	result.PID = cmd.Process.Pid
	outW.Close()
	errW.Close()
	exited := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(exited) }()
	failure := make(chan error, 2)
	stdout := make(chan captured, 1)
	stderr := make(chan captured, 1)
	go func() { stdout <- drain(outR, r.cfg.MaxOutputBytes, failure) }()
	go func() { stderr <- drain(errR, 0, failure) }()
	select {
	case <-owned.Done():
		err = owned.Err()
	case err = <-failure:
	case <-exited:
		if waitErr != nil {
			err = ErrExit
		}
	}
	// Cleanup has its own finite stages and does not inherit a canceled request's context.
	if !waitGone(result.PID, exited, r.cfg.GracePeriod) {
		if signal(result.PID, false) != nil {
			err = errors.Join(err, ErrCleanup)
		}
		if !waitGone(result.PID, exited, r.cfg.TermPeriod) {
			if signal(result.PID, true) != nil {
				err = errors.Join(err, ErrCleanup)
			}
			if !waitGone(result.PID, exited, r.cfg.KillPeriod) {
				err = errors.Join(err, ErrCleanup)
			}
		}
	}
	// A same-group descendant cannot retain the pipes. Unexpected inherited descriptors also get
	// a bounded drain window; closing these owned file handles unblocks their readers.
	join := time.NewTimer(r.cfg.KillPeriod)
	defer join.Stop()
	for stdout != nil || stderr != nil {
		select {
		case out := <-stdout:
			result.Stdout = out.data
			err = errors.Join(err, out.err)
			stdout = nil
		case out := <-stderr:
			err = errors.Join(err, out.err)
			stderr = nil
		case <-join.C:
			outR.Close()
			errR.Close()
			err = errors.Join(err, ErrCleanup)
		}
	}
	select {
	case <-exited:
		result.ExitCode = cmd.ProcessState.ExitCode()
	default:
		err = errors.Join(err, ErrCleanup)
	}
	return result, err
}

type captured struct {
	data []byte
	err  error
}

func drain(file *os.File, limit int, failure chan<- error) captured {
	var result captured
	var buffer [4096]byte
	for {
		n, err := file.Read(buffer[:])
		if n > 0 && limit > 0 {
			keep := min(n, limit-len(result.data))
			result.data = append(result.data, buffer[:keep]...)
			if keep < n && result.err == nil {
				result.err = ErrOutputLimit
				select {
				case failure <- result.err:
				default:
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				result.err = errors.Join(result.err, ErrIO)
				select {
				case failure <- ErrIO:
				default:
				}
			}
			return result
		}
	}
}
func waitGone(pid int, exited <-chan struct{}, duration time.Duration) bool {
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if !exists(pid) {
			select {
			case <-exited:
				return true
			default:
			}
		}
		select {
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}
