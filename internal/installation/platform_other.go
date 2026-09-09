//go:build !darwin && !linux

package installation

import "os"

const supported = false
const readFlags = os.O_RDONLY
const lockFlags = os.O_RDWR

func safeFile(os.FileInfo, os.FileMode) bool { return false }
func safeDirectory(os.FileInfo, bool) bool   { return false }
func safeLink(os.FileInfo) bool              { return false }
func lockFile(*os.File, bool) error          { return ErrLocation }
