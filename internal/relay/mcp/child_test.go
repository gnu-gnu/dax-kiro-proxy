package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/toolregistry"
)

var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dax-mcp-fixture-")
	if err != nil {
		panic(err)
	}
	binaryPath = filepath.Join(dir, "dax-kiro-proxy")
	build := exec.Command("go", "build", "-o", binaryPath, "../../../cmd/dax-kiro-proxy")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if build.Run() != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type fixtureCheck struct{}

func (fixtureCheck) Check(context.Context, []byte) error            { return nil }
func (fixtureCheck) Validate(context.Context, []byte, []byte) error { return nil }

type child struct {
	input  io.WriteCloser
	output *bufio.Reader
	cmd    *exec.Cmd
	done   chan error
}

func launch(t *testing.T) (*child, *relay.Broker, *relay.Socket, string) {
	t.Helper()
	r, err := toolregistry.Build(context.Background(), []json.RawMessage{json.RawMessage(`{"name":"synthetic_client_tool","description":"Only the fixture client completes this request.","input_schema":{"type":"object"}}`)}, nil, fixtureCheck{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := relay.NewBroker(r, relay.Limits{ToolTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	if err := b.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	s, err := relay.Listen(b, relay.SocketConfig{BaseDirectory: "/private/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	cmd := exec.Command(binaryPath, "relay", "--config", s.ConfigPath())
	cmd.Dir = t.TempDir()
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{in, bufio.NewReader(out), cmd, make(chan error, 1)}
	go func() { c.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = in.Close()
		select {
		case <-c.done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-c.done
			t.Error("relay child required forced cleanup")
		}
	})
	if s.BindProcess(cmd.Process.Pid) != nil {
		t.Fatal("cannot bind the owned MCP fixture group")
	}
	return c, b, s, r.Tools()[0].Alias
}
func (c *child) send(t *testing.T, id any, method string, params any) {
	t.Helper()
	v := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		v["id"] = id
	}
	if params != nil {
		v["params"] = params
	}
	data, _ := json.Marshal(v)
	if _, err := c.input.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}
func (c *child) receive(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	type received struct {
		raw []byte
		err error
	}
	done := make(chan received, 1)
	go func() { raw, err := c.output.ReadBytes('\n'); done <- received{raw, err} }()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(got.raw, &result) != nil {
			t.Fatal("invalid MCP output")
		}
		return result
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		t.Fatal("MCP reply deadline")
		return nil
	}
}
func initialize(t *testing.T, c *child) {
	c.send(t, 1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "independent-fixture", "version": "1"}})
	response := c.receive(t)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(response["result"], &fields)
	if string(fields["protocolVersion"]) != `"2025-06-18"` {
		t.Fatal("version not negotiated")
	}
	c.send(t, nil, "notifications/initialized", nil)
}
func TestMCPChildLifecycleAndClientOnlyRoundTrip(t *testing.T) {
	c, b, _, alias := launch(t)
	c.send(t, 0, "tools/list", nil)
	if c.receive(t)["error"] == nil {
		t.Fatal("tools available before initialization")
	}
	initialize(t, c)
	c.send(t, 2, "tools/list", nil)
	list := c.receive(t)
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		}
	}
	_ = json.Unmarshal(list["result"], &tools)
	if len(tools.Tools) != 1 || tools.Tools[0].Name != alias {
		t.Fatal("MCP list exposed undeclared/original tool")
	}
	c.send(t, "invoke-id", "tools/call", map[string]any{"name": alias, "arguments": map[string]any{"synthetic": "value"}})
	until := time.Now().Add(time.Second)
	for b.Stats().Queued != 1 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	batch, err := b.Seal()
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Delivered(batch.Number)
	// A suspended tools/call must not block the child reader or an unrelated ping.
	c.send(t, 3, "ping", nil)
	ping := c.receive(t)
	if string(ping["id"]) != "3" {
		t.Fatal("pending call blocked ping or completed without client")
	}
	if err := b.Resolve(b.Credentials().Owner, []relay.Result{{ID: batch.Calls[0].ID, ToolResult: relay.ToolResult{Content: []relay.Content{{Type: "text", Text: "fixture result"}}, IsError: true}}}); err != nil {
		t.Fatal(err)
	}
	response := c.receive(t)
	var result relay.ToolResult
	_ = json.Unmarshal(response["result"], &result)
	if string(response["id"]) != `"invoke-id"` || !result.IsError || len(result.Content) != 1 || result.Content[0].Text != "fixture result" {
		t.Fatal("tool result not preserved")
	}
	for _, method := range []string{"resources/read", "prompts/get", "sampling/createMessage", "terminal/create"} {
		c.send(t, 4, method, map[string]any{})
		if c.receive(t)["error"] == nil {
			t.Fatal("unsupported effect method accepted")
		}
	}
	c.send(t, nil, "unknown/notification", nil)
	c.send(t, 5, "ping", nil)
	if string(c.receive(t)["id"]) != "5" {
		t.Fatal("notification received a response")
	}
}
func TestMCPCancellationSettlesSuspendedCall(t *testing.T) {
	c, b, _, alias := launch(t)
	initialize(t, c)
	c.send(t, 9, "tools/call", map[string]any{"name": alias, "arguments": map[string]any{}})
	until := time.Now().Add(time.Second)
	for b.Stats().Pending != 1 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	c.send(t, nil, "notifications/cancelled", map[string]any{"requestId": 9, "reason": "fixture cancel"})
	response := c.receive(t)
	if string(response["id"]) != "9" {
		t.Fatal("cancellation returned wrong request")
	}
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("canceled relay session remained reusable")
	}
}

func TestMCPOwnerClosureStopsBlockedInputAndPendingTools(t *testing.T) {
	for _, mode := range []string{"idle", "pending", "blocked-output"} {
		t.Run(mode, func(t *testing.T) {
			c, broker, socket, alias := launch(t)
			initialize(t, c)
			pid, verified := socket.PeerPID()
			if !verified || pid != c.cmd.Process.Pid {
				t.Fatal("MCP started without verified process attachment")
			}
			if mode == "pending" {
				c.send(t, 20, "tools/call", map[string]any{"name": alias, "arguments": map[string]any{}})
				until := time.Now().Add(time.Second)
				for broker.Stats().Pending != 1 && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if broker.Stats().Pending != 1 {
					t.Fatal("tool did not suspend")
				}
			}
			var writerDone chan struct{}
			if mode == "blocked-output" {
				writerDone = make(chan struct{})
				go func() {
					defer close(writerDone)
					_, _ = io.WriteString(c.input, strings.Repeat("{\"jsonrpc\":\"2.0\",\"id\":30,\"method\":\"ping\"}\n", 32768))
				}()
				select {
				case <-writerDone:
					t.Fatal("bounded fixture did not fill the unread pipe")
				case <-time.After(100 * time.Millisecond):
				}
			}
			var callers sync.WaitGroup
			for range 8 {
				callers.Go(func() {
					if socket.Close() != nil {
						t.Error("MCP cleanup failed")
					}
				})
			}
			callers.Wait()
			if writerDone != nil {
				select {
				case <-writerDone:
				case <-time.After(time.Second):
					t.Fatal("blocked fixture writer did not join")
				}
			}
			if syscall.Kill(pid, 0) != syscall.ESRCH || broker.Stats().Pending != 0 {
				t.Fatal("MCP or pending work survived supervisor closure")
			}
		})
	}
}
