//go:build !darwin && !linux

package privatefs

import "os"

const platformSupported = false
const readFlags = os.O_RDONLY

func ownerOnly(os.FileInfo) bool { return false }
