//go:build darwin || linux

package childproc

import (
	"errors"
	"os/exec"
	"syscall"
)

func configure(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func exists(pid int) bool { return !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) }
func signal(pid int, force bool) error {
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
