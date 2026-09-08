//go:build !darwin && !linux

package acp

import "os/exec"

func configureProcess(*exec.Cmd) error { return ErrParameters }
func signalGroup(int, bool) error      { return ErrParameters }
func groupExists(int) bool             { return false }
