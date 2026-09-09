//go:build darwin || linux

package launcher

import (
	"os"
	"syscall"
)

const settingsReadFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

func safeSettings(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0022 == 0 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

func safeClientAssetDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode().Perm()&0022 == 0 && stat.Uid == uint32(os.Geteuid())
}
