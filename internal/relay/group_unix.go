//go:build darwin || linux

package relay

import (
	"errors"
	"os"
	"syscall"
)

func validOwnedGroup(pid int) bool {
	return pid > 1 && pid != os.Getpid() && pid != syscall.Getpgrp() && processInGroup(pid, pid)
}

func processInGroup(pid, group int) bool {
	got, err := syscall.Getpgid(pid)
	return err == nil && group > 1 && got == group
}

func processGone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

func joinRelayGroup(group int) bool {
	return group > 1 && processInGroup(group, group) && syscall.Setpgid(0, group) == nil && processInGroup(os.Getpid(), group)
}
