package childproc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

var ErrTerminal = errors.New("cannot acquire or restore the foreground terminal")

const terminalReclaimCommand = "internal-terminal-reclaim"

// IsTerminalReclaimer identifies an effect-free helper invocation. An executable using Foreground
// must handle this at entry by exiting successfully before loading settings or starting any work.
// The parent supplies the terminal/group through OS process attributes, never command-line input.
func IsTerminalReclaimer(args []string) bool {
	return len(args) == 1 && args[0] == terminalReclaimCommand
}

type AttachedConfig struct {
	Lifetime, GracePeriod, TermPeriod, KillPeriod time.Duration
}

// AttachedIO passes caller-owned descriptors directly to a client. There are no output capture or
// input-copy goroutines. Foreground requires Stdin to be this process's foreground controlling tty.
type AttachedIO struct {
	Stdin, Stdout, Stderr *os.File
	Foreground            bool
}

// Attached admits one client at a time. Close cancels admission and joins shielded group cleanup.
type Attached struct {
	cfg    AttachedConfig
	mu     sync.Mutex
	active int
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	work   sync.WaitGroup
}
type AttachedProcess struct {
	pid    int
	cancel context.CancelFunc
	done   chan struct{}
	result Result
	err    error
}

func NewAttached(cfg AttachedConfig) (*Attached, error) {
	if cfg.Lifetime == 0 {
		cfg.Lifetime = 24 * time.Hour
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
	if cfg.Lifetime <= 0 || cfg.Lifetime > 7*24*time.Hour {
		return nil, ErrParameters
	}
	for _, d := range []time.Duration{cfg.GracePeriod, cfg.TermPeriod, cfg.KillPeriod} {
		if d <= 0 || d > 5*time.Second {
			return nil, ErrParameters
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Attached{cfg: cfg, ctx: ctx, cancel: cancel}, nil
}
func (a *Attached) Active() int { a.mu.Lock(); defer a.mu.Unlock(); return a.active }
func (a *Attached) Close() {
	a.mu.Lock()
	a.closed = true
	a.cancel()
	a.mu.Unlock()
	a.work.Wait()
}
func (p *AttachedProcess) PID() int              { return p.pid }
func (p *AttachedProcess) Done() <-chan struct{} { return p.done }
func (p *AttachedProcess) Wait() (Result, error) { <-p.done; return p.result, p.err }
func (p *AttachedProcess) Close() error          { p.cancel(); <-p.done; return p.err }

func (a *Attached) Start(ctx context.Context, command Command, files AttachedIO) (*AttachedProcess, error) {
	if !validCommand(command) {
		return nil, ErrParameters
	}
	for _, file := range []*os.File{files.Stdin, files.Stdout, files.Stderr} {
		if file == nil {
			return nil, ErrParameters
		}
		if _, err := file.Stat(); err != nil {
			return nil, ErrParameters
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, ErrClosed
	}
	if a.active != 0 {
		a.mu.Unlock()
		return nil, ErrBusy
	}
	a.active++
	a.work.Add(1)
	a.mu.Unlock()
	leave := func() { a.mu.Lock(); a.active--; a.mu.Unlock(); a.work.Done() }
	owned, cancel := context.WithTimeout(ctx, a.cfg.Lifetime)
	stop := context.AfterFunc(a.ctx, cancel)
	if a.ctx.Err() != nil {
		cancel()
	}
	release := func() { stop(); cancel(); leave() }
	cmd := exec.Command(command.Executable, command.Args...)
	cmd.Dir, cmd.Env = command.Directory, append([]string{}, command.Environment...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = files.Stdin, files.Stdout, files.Stderr
	restore, err := prepareAttached(cmd, files)
	if err != nil {
		release()
		return nil, err
	}
	if err = owned.Err(); err != nil {
		err = errors.Join(err, restore())
		release()
		return nil, err
	}
	if cmd.Start() != nil {
		err = errors.Join(ErrStart, restore())
		release()
		return nil, err
	}
	p := &AttachedProcess{pid: cmd.Process.Pid, cancel: cancel, done: make(chan struct{}), result: Result{PID: cmd.Process.Pid, ExitCode: -1}}
	go func() {
		exited := make(chan struct{})
		var waitErr error
		go func() { waitErr = cmd.Wait(); close(exited) }()
		select {
		case <-owned.Done():
			p.err = owned.Err()
		case <-exited:
			if waitErr != nil {
				p.err = ErrExit
			}
		}
		p.err = errors.Join(p.err, shutdownGroup(p.pid, exited, a.cfg.GracePeriod, a.cfg.TermPeriod, a.cfg.KillPeriod))
		select {
		case <-exited:
			p.result.ExitCode = cmd.ProcessState.ExitCode()
		default:
			p.err = errors.Join(p.err, ErrCleanup)
		}
		p.err = errors.Join(p.err, restore())
		stop()
		cancel()
		a.mu.Lock()
		a.active--
		close(p.done)
		a.mu.Unlock()
		a.work.Done()
	}()
	return p, nil
}

func shutdownGroup(pid int, exited <-chan struct{}, grace, term, kill time.Duration) error {
	var err error
	if !waitGone(pid, exited, grace) {
		if signal(pid, false) != nil {
			err = ErrCleanup
		}
		if !waitGone(pid, exited, term) {
			if signal(pid, true) != nil {
				err = ErrCleanup
			}
			if !waitGone(pid, exited, kill) {
				err = ErrCleanup
			}
		}
	}
	return err
}
