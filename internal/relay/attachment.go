package relay

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"

	"dax-kiro-proxy/internal/ndjson"
)

var ErrCleanup = errors.New("relay process cleanup did not complete")

// BindProcess accepts the already-owned ACP leader from the local process owner, never a wire PID.
// Attachment may wait briefly for this one-time binding while ACP initialization completes.
func (s *Socket) BindProcess(pid int) error {
	if !validOwnedGroup(pid) {
		return ErrCall
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.group != 0 {
		return ErrClosed
	}
	if err := s.cfg.SetupContext.Err(); err != nil {
		return err
	}
	if deadline, ok := s.cfg.SetupContext.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	s.group = pid
	close(s.groupReady)
	return nil
}

// PeerPID retains only the kernel peer identity and whether group membership was verified. It is
// historical after shutdown and is not a license to signal that PID or an arbitrary peer group.
func (s *Socket) PeerPID() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peer, s.joined
}

func (s *Socket) attach(conn *net.UnixConn, fields map[string]json.RawMessage) {
	var owner, secret, operation string
	if len(fields) != 4 || string(fields["version"]) != "1" || !controlString(fields["operation"], &operation) || operation != "attach" || !controlString(fields["owner"], &owner) || !controlString(fields["secret"], &secret) ||
		subtle.ConstantTimeCompare([]byte(owner), []byte(s.broker.credentials.Owner)) != 1 || subtle.ConstantTimeCompare([]byte(secret), []byte(s.broker.credentials.Secret)) != 1 {
		return
	}
	pid, err := socketPeerPID(conn)
	if err != nil || pid <= 1 || pid == os.Getpid() {
		return
	}
	select {
	case <-s.groupReady:
	default:
		timer := time.NewTimer(s.cfg.AttachTimeout)
		defer timer.Stop()
		select {
		case <-s.groupReady:
		case <-s.closing:
			return
		case <-s.cfg.SetupContext.Done():
			// Successful setup cancels its context too. A binding made before that boundary
			// remains valid for a child that is already connecting or starts later.
			select {
			case <-s.groupReady:
			default:
				return
			}
		case <-timer.C:
			return
		}
	}
	s.mu.Lock()
	if s.closed || s.peer != 0 {
		s.mu.Unlock()
		return
	}
	s.peer = pid
	group := s.group
	_ = conn.SetDeadline(time.Now().Add(s.cfg.ReadTimeout))
	s.mu.Unlock()
	// Once an authenticated child claims the single lifetime slot, incomplete setup or loss retires
	// the broker. A fresh session must supply fresh credentials; attachment is never replayed.
	defer s.broker.Close()
	if WriteFrame(conn, map[string]any{"version": 1, "operation": "attach", "group": group}) != nil {
		return
	}
	raw, err := ReadFrame(conn)
	if err != nil {
		return
	}
	ack, err := ndjson.Object(raw)
	if err != nil || len(ack) != 2 || string(ack["version"]) != "1" || string(ack["operation"]) != `"joined"` || !processInGroup(pid, group) {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.joined = true
	s.mu.Unlock()
	if WriteFrame(conn, map[string]any{"version": 1, "operation": "ready"}) != nil {
		return
	}
	s.mu.Lock()
	if !s.closed {
		_ = conn.SetDeadline(time.Time{})
	}
	s.mu.Unlock()
	var one [1]byte
	_, _ = conn.Read(one[:])
}

func waitRelayExit(pid int) bool {
	if pid == 0 {
		return true
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if processGone(pid) {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}
