//go:build !darwin && !linux

package privatefs

import "os"

const platformSupported = false
const readFlags = os.O_RDONLY
const lockFlags = os.O_RDONLY

func lockExclusive(*os.File) error { return ErrFile }

func ownerOnly(os.FileInfo) bool { return false }
