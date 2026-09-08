package relay

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func socketPeerPID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, ErrAuth
	}
	var pid int
	var credentialErr error
	err = raw.Control(func(fd uintptr) {
		credentials, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil || credentials.Uid != uint32(os.Geteuid()) {
			credentialErr = ErrAuth
			return
		}
		pid = int(credentials.Pid)
	})
	if err != nil || credentialErr != nil {
		return 0, ErrAuth
	}
	return pid, nil
}
