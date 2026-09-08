//go:build !darwin && !linux

package childproc

import "os/exec"

func configure(*exec.Cmd) error { return ErrParameters }
func exists(int) bool           { return false }
func signal(int, bool) error    { return ErrParameters }
