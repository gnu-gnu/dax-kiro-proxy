package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/toolregistry"
)

// A cooperative-child experiment, not a production parent-identity or policy assertion. The fake
// ACP creates a separate child group; only the joining-relay fixture requests its parent's group.
func TestRelayCanJoinOwnedACPGroup(t *testing.T) {
	for _, variant := range []string{"joining-relay", "observed-relay"} {
		t.Run(variant, func(t *testing.T) {
			for _, mode := range []string{"normal", "forced", "pending"} {
				t.Run(mode, func(t *testing.T) {
					joined := buildNamedRelayObserver(t, variant)
					root := filepath.Dir(joined)
					fake := filepath.Join(root, "fake-acp")
					runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
					if err != nil {
						t.Fatal(err)
					}
					defer runner.Close()
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
					if _, err := runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Args: []string{"build", "-o", fake, "../acp/testdata/fake"}, Directory: cwd, Environment: env}); err != nil {
						t.Fatal("cannot build the independent process-group peer")
					}
					registry, err := toolregistry.Build(t.Context(), []json.RawMessage{json.RawMessage(`{"name":"GroupFixture","input_schema":{"type":"object"}}`)}, nil, syntaxFixtureValidator{})
					if err != nil {
						t.Fatal(err)
					}
					broker, err := relay.NewBroker(registry, relay.Limits{ToolTimeout: 10 * time.Second})
					if err != nil {
						t.Fatal(err)
					}
					defer broker.Close()
					socket, err := relay.Listen(broker, relay.SocketConfig{})
					if err != nil {
						t.Fatal(err)
					}
					defer socket.Close()
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					client, err := acp.Start(ctx, acp.Config{Executable: fake, Args: []string{"chat-tools-separate-group"}, Directory: root,
						ClientInfo: acp.Info{Name: "independent-group-test", Version: "1"}, Limits: acp.Limits{RequestTimeout: 5 * time.Second}})
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					if socket.BindProcess(client.PID()) != nil {
						t.Fatal("cannot bind the owned ACP group")
					}
					raw, err := client.Call(ctx, "session/new", map[string]any{"cwd": root, "mcpServers": []any{map[string]any{"name": "dax_session", "command": joined, "args": []string{"relay", "--config", socket.ConfigPath()}, "env": []any{}}}})
					if err != nil {
						t.Fatal(err)
					}
					var session struct {
						ID string `json:"sessionId"`
					}
					if json.Unmarshal(raw, &session) != nil || session.ID == "" {
						t.Fatal("fixture session was not created")
					}
					if variant == "joining-relay" && relayJoinOutcome(joined) != "changed" {
						t.Fatal("the fixture did not move from its separate group into the owned ACP group")
					}
					peer, verified := socket.PeerPID()
					if !verified || peer <= 1 {
						t.Fatal("production relay attachment was not verified")
					}
					if group, err := syscall.Getpgid(peer); err != nil || group != client.PID() {
						t.Fatal("actual relay membership differs from the bound ACP group")
					}
					var promptDone chan error
					if mode == "pending" {
						if broker.BeginTurn() != nil {
							t.Fatal("cannot admit a synthetic pending tool")
						}
						promptDone = make(chan error, 1)
						go func() {
							_, err := client.Call(ctx, "session/prompt", map[string]any{"sessionId": session.ID, "prompt": []any{map[string]any{"type": "text", "text": "Independent pending-call fixture"}}})
							promptDone <- err
						}()
						for broker.Stats().Pending != 1 && ctx.Err() == nil {
							time.Sleep(time.Millisecond)
						}
						if broker.Stats().Pending != 1 {
							t.Fatal("fixture did not establish its pending call")
						}
					}
					if mode == "forced" && syscall.Kill(-client.PID(), syscall.SIGKILL) != nil {
						t.Fatal("cannot terminate the owned fake ACP group")
					}
					var wait sync.WaitGroup
					for range 8 {
						wait.Go(func() {
							if client.Close() != nil {
								t.Error("owned group shutdown failed")
							}
						})
					}
					wait.Wait()
					if promptDone != nil {
						select {
						case err := <-promptDone:
							if err == nil {
								t.Error("interrupted prompt unexpectedly succeeded")
							}
						case <-ctx.Done():
							t.Fatal("interrupted prompt did not settle")
						}
					}
					checkRelayCleanupWithAttachment(t, joined, client.PID(), peer)
					broker.Close()
					socket.Close()
					_, statErr := os.Lstat(socket.ConfigPath())
					if !errors.Is(syscall.Kill(-client.PID(), 0), syscall.ESRCH) || broker.Stats().Pending != 0 || !errors.Is(statErr, os.ErrNotExist) {
						t.Fatal("owned group, tool work or relay configuration survived cleanup")
					}
				})
			}
		})
	}
}
