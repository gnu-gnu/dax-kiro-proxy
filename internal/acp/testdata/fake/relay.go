package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// The fake ACP backend talks only to the supplied MCP stdio child. It never reads the parent's
// private control configuration or invokes a client tool effect.
type fixtureRelay struct {
	cmd           *exec.Cmd
	input         io.WriteCloser
	output        *bufio.Reader
	alias         string
	responseError bool
}

func startFixtureRelay(raw []byte, cwd string) *fixtureRelay {
	return startFixtureRelayGroup(raw, cwd, false)
}

func startFixtureRelayGroup(raw []byte, cwd string, separate bool) *fixtureRelay {
	var config struct {
		Command string                         `json:"command"`
		Args    []string                       `json:"args"`
		Env     []struct{ Name, Value string } `json:"env"`
	}
	if json.Unmarshal(raw, &config) != nil || config.Command == "" {
		os.Exit(36)
	}
	cmd := exec.Command(config.Command, config.Args...)
	if separate {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Dir = cwd
	cmd.Env = []string{}
	for _, v := range config.Env {
		cmd.Env = append(cmd.Env, v.Name+"="+v.Value)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		os.Exit(37)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		os.Exit(38)
	}
	if cmd.Start() != nil {
		os.Exit(39)
	}
	f := &fixtureRelay{cmd: cmd, input: in, output: bufio.NewReader(out)}
	f.send(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "synthetic-acp", "version": "1"}})
	f.read()
	f.send(nil, "notifications/initialized", nil)
	f.send(2, "tools/list", nil)
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(f.read(), &list) != nil || len(list.Tools) != 1 {
		os.Exit(40)
	}
	f.alias = list.Tools[0].Name
	return f
}
func (f *fixtureRelay) send(id any, method string, params any) {
	v := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		v["id"] = id
	}
	if params != nil {
		v["params"] = params
	}
	b, _ := json.Marshal(v)
	if _, err := f.input.Write(append(b, '\n')); err != nil {
		os.Exit(41)
	}
}
func (f *fixtureRelay) read() json.RawMessage {
	line, err := f.output.ReadBytes('\n')
	if err != nil {
		os.Exit(42)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(line, &response) != nil {
		os.Exit(43)
	}
	f.responseError = len(response.Error) > 0
	if f.responseError {
		return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"relay interrupted"}]}`)
	}
	return response.Result
}
func (f *fixtureRelay) call() json.RawMessage {
	f.send(3, "tools/call", map[string]any{"name": f.alias, "arguments": map[string]any{"n": 1}})
	return f.read()
}

// This manifest supplies only the synthetic request and expected result. The peer never opens the
// requested file or runs the requested command: the unmodified client alone performs that effect.
func (f *fixtureRelay) effect(manifest string) {
	file, err := os.Open(manifest)
	if err != nil {
		os.Exit(92)
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	closeErr := file.Close()
	var spec struct {
		Input        map[string]any `json:"input"`
		IsError      bool           `json:"isError"`
		RequiredText string         `json:"requiredText"`
	}
	if err != nil || closeErr != nil || len(data) > 64<<10 || json.Unmarshal(data, &spec) != nil || len(spec.Input) == 0 || len(spec.RequiredText) > 128 {
		os.Exit(93)
	}
	f.send(3, "tools/call", map[string]any{"name": f.alias, "arguments": spec.Input})
	var returned struct {
		IsError bool                          `json:"isError"`
		Content []struct{ Type, Text string } `json:"content"`
	}
	if json.Unmarshal(f.read(), &returned) != nil || f.responseError || returned.IsError != spec.IsError {
		os.Exit(94)
	}
	var text strings.Builder
	for _, block := range returned.Content {
		if block.Type != "text" || text.Len()+len(block.Text) > 64<<10 {
			os.Exit(95)
		}
		text.WriteString(block.Text)
	}
	if !strings.Contains(text.String(), spec.RequiredText) {
		os.Exit(96)
	}
}
func (f *fixtureRelay) close() {
	_ = f.input.Close()
	done := make(chan struct{})
	go func() { _ = f.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = f.cmd.Process.Kill()
		<-done
	}
}
