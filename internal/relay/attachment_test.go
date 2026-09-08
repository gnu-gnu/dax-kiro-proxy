package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
)

type attachmentPeer struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	done   chan struct{}
}

func buildAttachmentPeer(t *testing.T) string {
	t.Helper()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	executable := filepath.Join(root, "independent-attachment")
	if _, err := runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Args: []string{"build", "-o", executable, "./testdata/attachment"}, Directory: cwd, Environment: env}); err != nil {
		t.Fatal("cannot build independent attachment peer")
	}
	return executable
}

func startAttachmentPeer(t *testing.T, executable string, args ...string) *attachmentPeer {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, writer, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		t.Fatal(err)
	}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		_ = writer.Close()
		t.Fatal(err)
	}
	_ = writer.Close()
	p := &attachmentPeer{cmd: cmd, input: in, output: bufio.NewReader(out), done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		_ = in.Close()
		_ = out.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(time.Second):
			t.Error("owned attachment peer survived cleanup")
		}
	})
	return p
}

func (p *attachmentPeer) line(t *testing.T) string {
	t.Helper()
	done := make(chan string, 1)
	go func() { line, _ := p.output.ReadString('\n'); done <- line }()
	select {
	case line := <-done:
		return line
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		t.Fatal("attachment observation deadline")
		return ""
	}
}

func TestRelayAttachmentAuthenticationAndLifetime(t *testing.T) {
	executable := buildAttachmentPeer(t)
	for _, mode := range []string{"join", "wrong", "extra", "lie", "bad-ack", "stall", "unbound", "duplicate", "lost", "linger"} {
		t.Run(mode, func(t *testing.T) {
			owner := startAttachmentPeer(t, executable, "owner")
			if owner.line(t) != "owner\n" {
				t.Fatal("owner did not start")
			}
			broker, _, _ := fixtureBroker(t, nil)
			socket, err := Listen(broker, SocketConfig{ReadTimeout: 150 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = socket.Close() })
			for _, pid := range []int{0, 1, -1, os.Getpid()} {
				if socket.BindProcess(pid) == nil {
					t.Fatal("invalid group owner accepted")
				}
			}
			if mode != "unbound" {
				if socket.BindProcess(owner.cmd.Process.Pid) != nil {
					t.Fatal("owned group binding failed")
				}
				if socket.BindProcess(owner.cmd.Process.Pid) == nil {
					t.Fatal("group binding repeated")
				}
			}
			peerMode := mode
			if mode == "unbound" || mode == "duplicate" {
				peerMode = "join"
			}
			peer := startAttachmentPeer(t, executable, socket.ConfigPath(), peerMode)
			line := peer.line(t)
			good := mode == "join" || mode == "duplicate" || mode == "lost" || mode == "linger"
			if good && line != "ready\n" || !good && line != "rejected\n" {
				t.Fatal("unexpected attachment result")
			}
			if good {
				pid, verified := socket.PeerPID()
				if !verified || pid != peer.cmd.Process.Pid {
					t.Fatal("kernel peer identity was not retained")
				}
				if mode != "lost" {
					group, err := syscall.Getpgid(pid)
					if err != nil || group != owner.cmd.Process.Pid {
						t.Fatal("peer did not join the owned group")
					}
				}
			} else if _, verified := socket.PeerPID(); verified {
				t.Fatal("rejected attachment retained verified membership")
			}
			if mode == "wrong" || mode == "extra" {
				valid := startAttachmentPeer(t, executable, socket.ConfigPath(), "join")
				if valid.line(t) != "ready\n" {
					t.Fatal("invalid attachment consumed the valid lifetime slot")
				}
			}
			if mode == "duplicate" {
				second := startAttachmentPeer(t, executable, socket.ConfigPath(), "join")
				if second.line(t) != "rejected\n" {
					t.Fatal("duplicate attachment accepted")
				}
				select {
				case <-broker.Done():
					t.Fatal("duplicate retired the valid attachment")
				default:
				}
			}
			if mode == "join" {
				config, err := LoadChildConfig(socket.ConfigPath())
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				_, err = Exchange(ctx, config, Call{Version: 1, Owner: config.Owner, Secret: config.Secret, ID: "wrong-peer", Alias: broker.Registry().Tools()[0].Alias, Arguments: json.RawMessage(`{"n":1}`)})
				if err == nil || broker.Stats().Seen != 0 {
					t.Fatal("unregistered peer reached tool admission")
				}
			}
			if mode == "lost" {
				select {
				case <-broker.Done():
				case <-time.After(time.Second):
					t.Fatal("lost attachment left broker reusable")
				}
			}
			var callers sync.WaitGroup
			for range 8 {
				callers.Go(func() {
					err := socket.Close()
					if mode == "linger" && !errors.Is(err, ErrCleanup) || mode != "linger" && err != nil {
						t.Error("unexpected relay cleanup outcome")
					}
				})
			}
			callers.Wait()
			if mode != "linger" {
				select {
				case <-peer.done:
				case <-time.After(time.Second):
					t.Fatal("attachment child did not exit")
				}
			}
			if _, err := os.Lstat(socket.ConfigPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("relay config retained")
			}
			if socket.BindProcess(owner.cmd.Process.Pid) == nil {
				t.Fatal("closed socket accepted a binding")
			}
		})
	}
}

func TestAttachmentRejectsUnknownSupervisorAndLegacyConfiguration(t *testing.T) {
	broker, _, _ := fixtureBroker(t, nil)
	socket, err := Listen(broker, SocketConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	config, err := LoadChildConfig(socket.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	config.SupervisorPID++
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := Attach(ctx, config); !errors.Is(err, ErrAuth) {
		t.Fatal("unexpected supervisor was accepted")
	}
	if pid, verified := socket.PeerPID(); pid != 0 || verified {
		t.Fatal("rejected supervisor consumed an attachment")
	}
	config.Version = 1
	if _, err := Attach(ctx, config); !errors.Is(err, ErrCall) {
		t.Fatal("legacy child configuration was accepted")
	}
	raw, err := json.Marshal(config)
	if err != nil || os.WriteFile(socket.ConfigPath(), raw, 0600) != nil {
		t.Fatal("cannot write independent invalid config")
	}
	if _, err := LoadChildConfig(socket.ConfigPath()); err == nil {
		t.Fatal("legacy on-disk configuration was accepted")
	}
}
