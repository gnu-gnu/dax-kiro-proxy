//go:build !darwin && !linux

package relay

import "net"

func joinRelayGroup(int) bool { return false }

func socketPeerPID(*net.UnixConn) (int, error) { return 0, ErrAuth }
func validOwnedGroup(int) bool                 { return false }
func processInGroup(int, int) bool             { return false }
func processGone(int) bool                     { return false }
