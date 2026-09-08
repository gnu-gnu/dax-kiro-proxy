//go:build !darwin && !linux

package childproc

import "os/exec"

func prepareAttached(*exec.Cmd, AttachedIO) (func() error, error) { return nil, ErrParameters }
