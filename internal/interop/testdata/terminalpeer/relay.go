//go:build darwin

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exactly one admitted main prompt owns this connection. Keep it reachable until the ACP process
// exits: closing it after a single tool result would signal relay lifetime loss to the product.
var heldRelayInput io.WriteCloser

// Read only the launcher-created agent's public MCP command declaration. The peer does not open
// the relay's private configuration or the requested file, and never performs a client tool effect.
func fakeHeldTool() error {
	if len(os.Args) != 6 || strings.Join(os.Args[1:5], " ") != "acp --agent-engine v2 --agent" || strings.ContainsAny(os.Args[5], "/\\") {
		return errFrame
	}
	f, err := os.Open(filepath.Join(".kiro", "agents", os.Args[5]+".json"))
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	f.Close()
	var agent struct {
		MCPServers map[string]struct {
			Command string
			Args    []string
			Env     map[string]string
		}
	}
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &agent) != nil || len(agent.MCPServers) != 1 {
		return errFrame
	}
	var cmd *exec.Cmd
	for _, declaration := range agent.MCPServers {
		if declaration.Command != cfg.Proxy || len(declaration.Args) != 3 || declaration.Args[0] != "relay" || declaration.Args[1] != "--config" || !filepath.IsAbs(declaration.Args[2]) || len(declaration.Env) != 0 {
			return errFrame
		}
		cmd = exec.Command(declaration.Command, declaration.Args...)
		cmd.Env = []string{}
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return err
	}
	record("relay", map[string]any{"child": cmd.Process.Pid})
	heldRelayInput = input
	go cmd.Wait()
	reader := bufio.NewReaderSize(output, 256<<10)
	send := func(id int, method string, params any) error {
		packet := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
		if id != 0 {
			packet["id"] = id
		}
		data, err := json.Marshal(packet)
		if err != nil {
			return err
		}
		_, err = input.Write(append(data, '\n'))
		return err
	}
	read := func(id int) (json.RawMessage, error) {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		var response struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int
			Result  json.RawMessage
			Error   json.RawMessage
		}
		if json.Unmarshal(line, &response) != nil || response.JSONRPC != "2.0" || response.ID != id || len(response.Result) == 0 || len(response.Error) != 0 {
			return nil, errFrame
		}
		return response.Result, nil
	}
	if err = send(101, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "independent-keyboard-peer", "version": "1"}}); err != nil {
		return err
	}
	if _, err = read(101); err != nil {
		return err
	}
	if err = send(0, "notifications/initialized", map[string]any{}); err != nil {
		return err
	}
	if err = send(102, "tools/list", map[string]any{}); err != nil {
		return err
	}
	list, err := read(102)
	var tools struct {
		Tools []struct{ Name, Description string }
	}
	if err != nil || json.Unmarshal(list, &tools) != nil || len(tools.Tools) != 1 || !strings.HasPrefix(tools.Tools[0].Description, `Client tool name: "Read".`) {
		return errFrame
	}
	if err = send(103, "tools/call", map[string]any{"name": tools.Tools[0].Name, "arguments": map[string]string{"file_path": filepath.Join(cfg.Project, "read-fixture")}}); err != nil {
		return err
	}
	record("relay-called", nil)
	result, err := read(103)
	if err != nil {
		return err
	}
	var returned struct {
		IsError bool
		Content []struct{ Type, Text string }
	}
	if json.Unmarshal(result, &returned) != nil || returned.IsError {
		return errFrame
	}
	matched := false
	for _, item := range returned.Content {
		matched = matched || item.Type == "text" && strings.Contains(item.Text, "OwnedHookRead_47")
	}
	if !matched {
		return errors.New("independent read result absent")
	}
	record("relay-result", nil)
	return nil
}
