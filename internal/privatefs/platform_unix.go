//go:build darwin || linux

package privatefs

import (
	"os"
	"syscall"
)

const platformSupported = true
const readFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

func ownerOnly(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0 && (info.IsDir() || stat.Nlink == 1)
}
