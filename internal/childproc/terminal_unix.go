//go:build darwin || linux

package childproc

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var terminalOwner sync.Mutex

func prepareAttached(cmd *exec.Cmd, files AttachedIO) (func() error, error) {
	if !files.Foreground {
		return func() error { return nil }, configure(cmd)
	}
	if !terminalOwner.TryLock() {
		return nil, ErrBusy
	}
	owned, err := duplicateTerminal(files.Stdin)
	if err != nil {
		terminalOwner.Unlock()
		return nil, ErrTerminal
	}
	fd := int(owned.Fd())
	group, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	state, stateErr := unix.IoctlGetTermios(fd, getTerminalState)
	if err != nil || stateErr != nil || group != syscall.Getpgrp() {
		owned.Close()
		terminalOwner.Unlock()
		return nil, ErrTerminal
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: fd}
	return func() error {
		defer terminalOwner.Unlock()
		defer owned.Close()
		// Go's child setup can transfer terminal foreground ownership before exec without changing
		// the launcher's process-wide SIGTTOU disposition. This short-lived helper joins our group;
		// a timeout kills only that helper, never its (now shared) process group.
		groupErr := reclaimTerminal(fd, group)
		actual, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		if err != nil || actual != group {
			return ErrTerminal
		}
		stateErr := unix.IoctlSetTermios(fd, setTerminalState, state)
		if groupErr != nil || stateErr != nil {
			return ErrTerminal
		}
		return nil
	}, nil
}

func reclaimTerminal(fd, group int) error {
	self, err := os.Executable()
	if err != nil {
		return ErrTerminal
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, terminalReclaimCommand)
	cmd.Dir, cmd.Env = "/", []string{"GORACE=atexit_sleep_ms=0"}
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: fd, Pgid: group}
	if cmd.Run() != nil {
		return ErrTerminal
	}
	return nil
}

func duplicateTerminal(file *os.File) (*os.File, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, err
	}
	var fd int
	var duplicateErr error
	err = raw.Control(func(original uintptr) { fd, duplicateErr = unix.FcntlInt(original, unix.F_DUPFD_CLOEXEC, 0) })
	if err != nil {
		return nil, err
	}
	if duplicateErr != nil {
		return nil, duplicateErr
	}
	return os.NewFile(uintptr(fd), "dax-owned-terminal"), nil
}
