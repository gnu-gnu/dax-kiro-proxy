package relay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestAttachmentWaitFollowsSetupWithoutRelaxingReads(t *testing.T) {
	executable := buildAttachmentPeer(t)
	for _, mode := range []string{"delayed", "delayed-stall", "setup-canceled", "setup-expired", "closed", "after-setup"} {
		t.Run(mode, func(t *testing.T) {
			owner := startAttachmentPeer(t, executable, "owner")
			if owner.line(t) != "owner\n" {
				t.Fatal("independent owner did not start")
			}
			setupLimit := 2 * time.Second
			if mode == "setup-expired" {
				setupLimit = 500 * time.Millisecond
			}
			setup, cancel := context.WithTimeout(t.Context(), setupLimit)
			defer cancel()
			broker, _, _ := fixtureBroker(t, nil)
			socket, err := Listen(broker, SocketConfig{ReadTimeout: 100 * time.Millisecond, AttachTimeout: 2 * time.Second, SetupContext: setup})
			if err != nil {
				t.Fatal("cannot create bounded attachment listener")
			}
			t.Cleanup(func() { _ = socket.Close() })
			if mode == "after-setup" {
				if socket.BindProcess(owner.cmd.Process.Pid) != nil {
					t.Fatal("cannot bind before setup completion")
				}
				cancel()
			}
			peerMode := "delayed"
			if mode == "delayed-stall" {
				peerMode = mode
			}
			peer := startAttachmentPeer(t, executable, socket.ConfigPath(), peerMode)
			if peer.line(t) != "attaching\n" {
				t.Fatal("independent peer did not send attachment")
			}
			switch mode {
			case "delayed", "delayed-stall":
				// Bind well beyond the per-message read limit, within the original setup bound.
				timer := time.NewTimer(350 * time.Millisecond)
				<-timer.C
				if socket.BindProcess(owner.cmd.Process.Pid) != nil {
					t.Fatal("on-time binding failed")
				}
			case "setup-canceled":
				cancel()
				if !errors.Is(socket.BindProcess(owner.cmd.Process.Pid), context.Canceled) {
					t.Fatal("expired setup accepted a late process binding")
				}
			case "setup-expired":
				<-setup.Done()
				if !errors.Is(socket.BindProcess(owner.cmd.Process.Pid), context.DeadlineExceeded) {
					t.Fatal("setup deadline accepted a late process binding")
				}
			case "closed":
				if socket.Close() != nil {
					t.Fatal("closing an unbound attachment failed")
				}
			}
			want := "rejected\n"
			if mode == "delayed" || mode == "after-setup" {
				want = "ready\n"
			}
			if peer.line(t) != want {
				t.Fatal("attachment wait did not respect setup and read boundaries")
			}
			if mode == "delayed-stall" {
				select {
				case <-broker.Done():
				case <-time.After(500 * time.Millisecond):
					t.Fatal("incomplete acknowledgement retained the broker")
				}
			}
			if err := socket.Close(); err != nil {
				t.Fatal("attachment cleanup failed")
			}
			select {
			case <-peer.done:
			case <-time.After(time.Second):
				t.Fatal("attachment peer was not joined")
			}
		})
	}
}

func TestAttachmentTimeoutConfigurationBounds(t *testing.T) {
	broker, _, _ := fixtureBroker(t, nil)
	socket, err := Listen(broker, SocketConfig{AttachTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal("cannot create bounded listener")
	}
	defer socket.Close()
	config, err := LoadChildConfig(socket.ConfigPath())
	if err != nil || config.AttachTimeoutMillis != 30000 || config.Version != 3 {
		t.Fatal("setup bound was not carried in the child contract")
	}
	for _, invalid := range []int64{-1, 0, 60001, 1<<63 - 1} {
		candidate := config
		candidate.AttachTimeoutMillis = invalid
		raw, _ := json.Marshal(candidate)
		if os.WriteFile(socket.ConfigPath(), raw, 0600) != nil {
			t.Fatal("cannot write owned invalid configuration")
		}
		if _, err := LoadChildConfig(socket.ConfigPath()); err == nil {
			t.Fatal("out-of-range attachment timeout accepted on disk")
		}
		if _, err := Attach(t.Context(), candidate); !errors.Is(err, ErrCall) {
			t.Fatal("out-of-range attachment timeout reached the socket")
		}
	}
	for _, version := range []int{1, 2} {
		candidate := config
		candidate.Version = version
		raw, _ := json.Marshal(candidate)
		if os.WriteFile(socket.ConfigPath(), raw, 0600) != nil {
			t.Fatal("cannot write owned legacy configuration")
		}
		if _, err := LoadChildConfig(socket.ConfigPath()); err == nil {
			t.Fatal("legacy configuration accepted")
		}
		if _, err := Attach(t.Context(), candidate); !errors.Is(err, ErrCall) {
			t.Fatal("legacy attachment reached the socket")
		}
	}
}
