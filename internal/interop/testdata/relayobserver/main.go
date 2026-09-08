// This independent test wrapper records only its own PID/group, then replaces itself with the
// adjacent product relay executable. It neither parses nor records MCP or tool content.
// A separately named variant also experiments with cooperative process-group movement.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	if !(len(os.Args) == 2 && os.Args[1] == "--version" || len(os.Args) == 4 && os.Args[1] == "relay" && os.Args[2] == "--config" && filepath.IsAbs(os.Args[3])) {
		os.Exit(70)
	}
	self, err := os.Executable()
	if err != nil {
		os.Exit(71)
	}
	root := filepath.Dir(self)
	joined := true
	if filepath.Base(self) == "joining-relay" && len(os.Args) == 4 {
		joined = joinParentGroup(root)
	}
	f, err := os.OpenFile(filepath.Join(root, "relay-processes.txt"), os.O_RDWR|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		os.Exit(72)
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		os.Exit(73)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 992 {
		os.Exit(74)
	}
	previous, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(previous) > 992 || bytes.Count(previous, []byte{'\n'}) >= 32 {
		os.Exit(74)
	}
	if _, err := fmt.Fprintf(f, "%d %d\n", os.Getpid(), syscall.Getpgrp()); err != nil {
		os.Exit(75)
	}
	if f.Close() != nil {
		os.Exit(76)
	}
	if !joined {
		os.Exit(78)
	}
	executable := filepath.Join(root, "owned-relay")
	if syscall.Exec(executable, append([]string{executable}, os.Args[1:]...), os.Environ()) != nil {
		os.Exit(77)
	}
}

// Only the explicitly named experiment uses its parent as the target. This does not establish
// a production identity or authorization protocol for choosing a process group.
func joinParentGroup(root string) bool {
	before := syscall.Getpgrp()
	parent := os.Getppid()
	target, err := syscall.Getpgid(parent)
	marker := "parent-unavailable\n"
	ok := false
	if err == nil && parent > 1 && target > 1 {
		err = syscall.Setpgid(0, target)
		switch {
		case err == nil && syscall.Getpgrp() == target:
			ok = true
			marker = "changed\n"
			if before == target {
				marker = "unchanged\n"
			}
		case err == syscall.EPERM:
			marker = "permission\n"
		default:
			marker = "failed\n"
		}
	}
	f, err := os.OpenFile(filepath.Join(root, "group-join.txt"), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return false
	}
	_, err = f.WriteString(marker)
	closeErr := f.Close()
	return ok && err == nil && closeErr == nil
}
