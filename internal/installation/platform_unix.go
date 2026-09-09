//go:build darwin || linux

package installation

import (
	"os"
	"syscall"
)

const supported = true
const readFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
const lockFlags = os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

func safeFile(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	if mode == 0 {
		return info.Mode().Perm()&0022 == 0 && info.Mode().Perm()&0100 != 0
	}
	return info.Mode().Perm() == mode
}
func safeDirectory(info os.FileInfo, private bool) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != uint32(os.Geteuid()) || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	if private {
		return info.Mode().Perm() == 0700
	}
	return info.Mode().Perm()&0700 == 0700 && info.Mode().Perm()&0022 == 0
}
func safeLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode()&os.ModeSymlink != 0 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}
func lockFile(file *os.File, shared bool) error {
	kind := syscall.LOCK_EX
	if shared {
		kind = syscall.LOCK_SH
	}
	err := syscall.Flock(int(file.Fd()), kind|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return ErrBusy
	}
	if err != nil {
		return ErrIO
	}
	return nil
}
