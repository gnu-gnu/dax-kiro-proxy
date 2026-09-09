package relay

import (
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestSocketCloseRevokesIdleAttachmentWithoutDeadlineDelay(t *testing.T) {
	executable := buildAttachmentPeer(t)
	owner := startAttachmentPeer(t, executable, "owner")
	if owner.line(t) != "owner\n" {
		t.Fatal("independent group owner did not start")
	}
	broker, _, _ := fixtureBroker(t, nil)
	socket, err := Listen(broker, SocketConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := socket.Close(); err != nil {
			t.Error(err)
		}
	}()
	if socket.BindProcess(owner.cmd.Process.Pid) != nil {
		t.Fatal("cannot bind the independent owner")
	}
	peer := startAttachmentPeer(t, executable, socket.ConfigPath(), "join")
	if peer.line(t) != "ready\n" {
		t.Fatal("idle attachment did not complete authentication")
	}
	if pid, verified := socket.PeerPID(); !verified || pid != peer.cmd.Process.Pid {
		t.Fatal("verified peer ownership was not retained")
	}
	// Readiness follows the complete handshake; the peer now awaits lifetime loss. Measure
	// shutdown only, excluding child compilation/startup and authentication from the budget.
	started := time.Now()
	var callers sync.WaitGroup
	for range 8 {
		callers.Go(func() {
			if err := socket.Close(); err != nil {
				t.Error("idle attachment cleanup failed")
			}
		})
	}
	callers.Wait()
	elapsed := time.Since(started)
	t.Logf("idle attachment joined close: %dms", elapsed.Milliseconds())
	if elapsed >= 750*time.Millisecond {
		t.Error("idle attachment shutdown waited for the connection read deadline")
	}
	select {
	case <-peer.done:
	case <-time.After(time.Second):
		t.Error("shutdown returned before the recorded child was joined")
	}
	select {
	case <-broker.Done():
	default:
		t.Error("shutdown left tool admission available")
	}
	socket.mu.Lock()
	retained := len(socket.connections)
	socket.mu.Unlock()
	if retained != 0 || broker.Stats().Pending != 0 || !processGone(peer.cmd.Process.Pid) {
		t.Error("idle attachment shutdown retained ownership")
	}
	if _, err := os.Lstat(socket.Directory()); !os.IsNotExist(err) {
		t.Error("idle attachment shutdown retained private artifacts")
	}
	if syscall.Kill(owner.cmd.Process.Pid, 0) != nil {
		t.Error("socket cleanup signalled its healthy ACP owner")
	}
}
