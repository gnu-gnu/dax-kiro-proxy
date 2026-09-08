//go:build darwin || linux

package mcp

import (
	"os"

	"dax-kiro-proxy/internal/relay"
	"golang.org/x/sys/unix"
)

// Inherited stdio may be a blocking descriptor created before Go's poller sees it. A nonblocking
// duplicate lets closing our own os.File interrupt a stalled read or write during cancellation.
func cancellableFile(file *os.File) (*os.File, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, relay.ErrCall
	}
	fd := -1
	var copyErr error
	err = raw.Control(func(source uintptr) { fd, copyErr = unix.FcntlInt(source, unix.F_DUPFD_CLOEXEC, 3) })
	if err != nil || copyErr != nil {
		return nil, relay.ErrCall
	}
	if unix.SetNonblock(fd, true) != nil {
		_ = unix.Close(fd)
		return nil, relay.ErrCall
	}
	owned := os.NewFile(uintptr(fd), "relay-stdio")
	if owned == nil {
		_ = unix.Close(fd)
		return nil, relay.ErrCall
	}
	return owned, nil
}
