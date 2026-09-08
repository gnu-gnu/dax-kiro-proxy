//go:build darwin || linux

package privatefs

import (
	"os"
	"syscall"
)

const platformSupported = true
const readFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
const lockFlags = os.O_CREATE | os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

func lockExclusive(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return ErrLocked
		}
		return ErrFile
	}
	return nil
}

func ownerOnly(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0 && (info.IsDir() || stat.Nlink == 1)
}
