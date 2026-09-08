//go:build !darwin && !linux

package launcher

import "os"

const settingsReadFlags = os.O_RDONLY

func safeSettings(os.FileInfo) bool { return false }
