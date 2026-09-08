//go:build darwin || linux

package acp

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func signalGroup(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	err := syscall.Kill(-pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func groupExists(pid int) bool { return !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) }
