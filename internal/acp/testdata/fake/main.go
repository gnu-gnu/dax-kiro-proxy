// This fixture is independently authored from the public JSON-RPC/ACP contracts.
// It deliberately shares no transport or state code with the proxy.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

var outputMu sync.Mutex

func write(v any) {
	outputMu.Lock()
	defer outputMu.Unlock()
	b, _ := json.Marshal(v)
	_, _ = os.Stdout.Write(append(b, '\n'))
}
func reply(id json.RawMessage, v any) { write(map[string]any{"jsonrpc": "2.0", "id": id, "result": v}) }
func forever() {
	for {
		time.Sleep(time.Hour)
	}
}
func main() {
	mode := "normal"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if strings.HasPrefix(mode, "inventory-") {
		inventoryFixture(mode)
		return
	}
	if strings.HasPrefix(mode, "pool-") {
		poolFixture(mode)
		return
	}
	if mode == "leaf" {
		signal.Ignore(syscall.SIGTERM)
		forever()
	}
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	r := bufio.NewReaderSize(os.Stdin, 4096)
	var pairs []request
	var hanging []json.RawMessage
	var parentCall json.RawMessage
	var parentReplyID json.RawMessage
	initialized := false
	session := ""
	currentModel := "fixture-backend"
	var calls []string
	var relayChild *fixtureRelay
	promptCount := 0
	defer func() {
		if relayChild != nil {
			relayChild.close()
		}
	}()
	modelOptions := func() []any {
		return []any{map[string]any{"id": "fixture-select", "name": "Model", "category": "model", "type": "select", "currentValue": currentModel, "options": []any{map[string]any{"value": "fixture-backend", "name": "Fixture"}, map[string]any{"value": "fixture-alternate", "name": "Alternate"}, map[string]any{"value": "auto", "name": "Automatic"}}}}
	}
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if mode == "stubborn" {
				forever()
			}
			return
		}
		var q request
		if json.Unmarshal(line, &q) != nil {
			os.Exit(20)
		}
		if !initialized {
			var p struct {
				Version      int            `json:"protocolVersion"`
				Capabilities map[string]any `json:"clientCapabilities"`
				Info         map[string]any `json:"clientInfo"`
			}
			_ = json.Unmarshal(q.Params, &p)
			if q.Method != "initialize" || p.Version != 1 || p.Capabilities == nil || len(p.Capabilities) != 0 || p.Info["name"] == nil {
				os.Exit(21)
			}
			if mode == "init-hang" {
				forever()
			}
			if mode == "chat-init-delay" {
				time.Sleep(250 * time.Millisecond)
			}
			version := 1
			if mode == "version" {
				version = 2
			}
			reply(q.ID, map[string]any{"protocolVersion": version, "agentCapabilities": map[string]any{"loadSession": true, "promptCapabilities": map[string]any{"image": true}}})
			initialized = true
			continue
		}
		if q.Method == "" && len(parentCall) > 0 {
			if string(q.ID) != string(parentReplyID) {
				os.Exit(24)
			}
			var response any
			if len(q.Error) > 0 {
				_ = json.Unmarshal(q.Error, &response)
			} else {
				_ = json.Unmarshal(q.Result, &response)
			}
			reply(parentCall, response)
			parentCall = nil
			continue
		}
		switch q.Method {
		case "schema/check", "schema/validate":
			if mode != "schema-hang" {
				os.Exit(33)
			}
			hanging = append(hanging, q.ID)
		case "session/new":
			calls = []string{"session/new"}
			if !strings.HasPrefix(mode, "chat") {
				os.Exit(25)
			}
			var p struct {
				CWD string            `json:"cwd"`
				MCP []json.RawMessage `json:"mcpServers"`
			}
			if json.Unmarshal(q.Params, &p) != nil || p.CWD == "" || p.MCP == nil {
				os.Exit(26)
			}
			if strings.HasPrefix(mode, "chat-tools") {
				if mode == "chat-tools-launch" {
					if len(p.MCP) != 0 || len(os.Args) != 3 {
						os.Exit(46)
					}
					raw, err := os.ReadFile(os.Args[2])
					if err != nil || !json.Valid(raw) {
						os.Exit(47)
					}
					p.MCP = []json.RawMessage{raw}
				}
				if len(p.MCP) != 1 {
					os.Exit(34)
				}
				relayChild = startFixtureRelayGroup(p.MCP[0], p.CWD, mode == "chat-tools-separate-group")
				if mode == "chat-tools-idle" {
					relayChild.send(90, "tools/call", map[string]any{"name": relayChild.alias, "arguments": map[string]any{"n": 1}})
					var outcome struct {
						IsError bool `json:"isError"`
					}
					if json.Unmarshal(relayChild.read(), &outcome) != nil || !outcome.IsError {
						os.Exit(44)
					}
				}
			}
			session = "fixture-conversation"
			if mode == "chat-no-id" {
				reply(q.ID, map[string]any{})
				continue
			}
			if mode == "chat-models" || mode == "chat-effort-reject" || mode == "chat-effort-corrupt" || mode == "chat-config" {
				write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/commands/available", "params": map[string]any{"sessionId": session, "commands": []any{map[string]any{"name": "/effort"}}}})
			}
			if mode == "chat-config" {
				reply(q.ID, map[string]any{"sessionId": session, "configOptions": modelOptions()})
				continue
			}
			reply(q.ID, map[string]any{"sessionId": session, "models": map[string]any{"currentModelId": currentModel, "availableModels": []any{map[string]any{"modelId": "fixture-backend", "name": "Fixture", "description": "Synthetic model"}, map[string]any{"modelId": "fixture-alternate", "name": "Alternate"}, map[string]any{"modelId": "auto", "name": "Automatic"}}}})
		case "session/set_model", "session/set_config_option":
			var params struct {
				Session string `json:"sessionId"`
				Model   string `json:"modelId"`
				Config  string `json:"configId"`
				Value   string `json:"value"`
			}
			if json.Unmarshal(q.Params, &params) != nil || params.Session != session {
				os.Exit(28)
			}
			model := params.Model
			if q.Method == "session/set_config_option" {
				if mode != "chat-config" || params.Config != "fixture-select" {
					os.Exit(29)
				}
				model = params.Value
			} else if mode == "chat-config" {
				os.Exit(30)
			}
			if model != "fixture-backend" && model != "fixture-alternate" && model != "auto" {
				os.Exit(31)
			}
			calls = append(calls, q.Method)
			currentModel = model
			if mode == "chat-config" {
				reply(q.ID, map[string]any{"configOptions": modelOptions()})
			} else if mode == "chat-model-mismatch" {
				reply(q.ID, map[string]any{"models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]any{"modelId": "fixture-backend"}, map[string]any{"modelId": "fixture-alternate"}}}})
			} else {
				reply(q.ID, map[string]any{})
			}
		case "_kiro.dev/commands/execute":
			var params struct {
				Session string `json:"sessionId"`
				Command struct {
					Name      string   `json:"name"`
					Arguments []string `json:"arguments"`
				} `json:"command"`
			}
			if json.Unmarshal(q.Params, &params) != nil || params.Session != session || params.Command.Name != "effort" || len(params.Command.Arguments) != 1 || currentModel == "auto" {
				os.Exit(32)
			}
			calls = append(calls, q.Method)
			if mode == "chat-effort-corrupt" {
				fmt.Fprintln(os.Stdout, "{broken")
				continue
			}
			reply(q.ID, map[string]any{"success": mode != "chat-effort-reject"})
		case "fixture/echo":
			reply(q.ID, json.RawMessage(q.Params))
		case "fixture/pair":
			pairs = append(pairs, q)
			if len(pairs) == 2 {
				reply(pairs[1].ID, json.RawMessage(pairs[1].Params))
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "rill"}}}})
				reply(pairs[0].ID, json.RawMessage(pairs[0].Params))
				pairs = nil
			}
		case "fixture/hang":
			write(map[string]any{"jsonrpc": "2.0", "method": "fixture/accepted", "params": map[string]any{"id": q.ID}})
			hanging = append(hanging, q.ID)
		case "session/prompt":
			promptCount++
			calls = append(calls, q.Method)
			if strings.HasPrefix(mode, "chat") {
				var p struct {
					Session string            `json:"sessionId"`
					Prompt  []json.RawMessage `json:"prompt"`
				}
				if json.Unmarshal(q.Params, &p) != nil || p.Session != session || len(p.Prompt) == 0 {
					os.Exit(27)
				}
				if mode == "chat-auth" {
					write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": 401, "message": "login required"}})
					continue
				}
				if mode == "chat-before" {
					hanging = append(hanging, q.ID)
					continue
				}
				if strings.HasPrefix(mode, "chat-tools") {
					if relayChild == nil || promptCount != 1 {
						os.Exit(35)
					}
					emit := func(text string) {
						write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}}})
					}
					emit("before client tool")
					for range 2 {
						write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "diagnostic-only", "title": "fixture status", "status": "pending"}}})
					}
					if mode == "chat-tools-auth" {
						relayChild.send(3, "tools/call", map[string]any{"name": relayChild.alias, "arguments": map[string]any{"n": 1}})
						time.Sleep(300 * time.Millisecond)
						write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": 401, "message": "login required"}})
						continue
					}
					if mode == "chat-tools-client" {
						if len(os.Args) != 3 {
							os.Exit(45)
						}
						relayChild.send(3, "tools/call", map[string]any{"name": relayChild.alias, "arguments": map[string]string{"file_path": os.Args[2]}})
						var returned struct {
							IsError bool                          `json:"isError"`
							Content []struct{ Type, Text string } `json:"content"`
						}
						if json.Unmarshal(relayChild.read(), &returned) != nil || relayChild.responseError || !returned.IsError {
							os.Exit(46)
						}
						deniedByHook := false
						for _, block := range returned.Content {
							deniedByHook = deniedByHook || block.Type == "text" && strings.Contains(block.Text, "independent fixture denial")
						}
						if !deniedByHook {
							os.Exit(47)
						}
						emit("independent client relay complete")
						reply(q.ID, map[string]any{"stopReason": "end_turn"})
						continue
					}
					result := relayChild.call()
					text, _ := json.Marshal(map[string]any{"promptCount": promptCount, "relayResult": result})
					emit(string(text))
					reply(q.ID, map[string]any{"stopReason": "end_turn"})
					continue
				}
				owner := session
				if mode == "chat-wrong-session" {
					owner = "some-other-session"
				}
				write(map[string]any{"jsonrpc": "2.0", "method": "_fixture/diagnostic", "params": map[string]any{"sessionId": owner}})
				chunks := []string{"birch ", "stone"}
				if mode == "chat-slow-pid" {
					chunks = []string{fmt.Sprintf("owned-pid:%d", os.Getpid())}
				}
				if mode == "chat-models" || mode == "chat-effort-reject" || mode == "chat-effort-corrupt" || mode == "chat-config" {
					body, _ := json.Marshal(map[string]any{"model": currentModel, "calls": calls})
					chunks = []string{string(body)}
				}
				if mode == "chat-project" {
					chunks = []string{string(q.Params)}
				}
				if mode == "chat-continuity" {
					body, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "session": session, "promptCount": promptCount, "prompt": p.Prompt})
					chunks = []string{string(body)}
				}
				if mode == "chat-order" {
					chunks = nil
					for i := range 32 {
						chunks = append(chunks, fmt.Sprintf("%d,", i))
					}
				}
				for _, text := range chunks {
					write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": owner, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}}})
				}
				if mode == "chat-slow" || mode == "chat-slow-pid" {
					hanging = append(hanging, q.ID)
					continue
				}
				stop := "end_turn"
				if mode == "chat-cancelled" {
					stop = "cancelled"
				}
				if mode == "chat-bad-stop" {
					stop = "invented"
				}
				reply(q.ID, map[string]any{"stopReason": stop})
				continue
			}
			hanging = append(hanging, q.ID)
		case "session/cancel":
			for _, id := range hanging {
				reply(id, map[string]any{"stopReason": "cancelled"})
			}
			hanging = nil
		case "fixture/bad":
			var p struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(q.Params, &p)
			switch p.Kind {
			case "json":
				fmt.Fprint(os.Stdout, "{broken\n")
			case "array":
				fmt.Fprint(os.Stdout, "[]\n")
			case "version":
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"1.0\",\"id\":%s,\"result\":{}}\n", q.ID)
			case "unknown-id":
				reply(json.RawMessage("7000000"), map[string]any{})
			case "string-id":
				reply(json.RawMessage(fmt.Sprintf("%q", string(q.ID))), map[string]any{})
			case "null-id":
				reply(json.RawMessage("null"), map[string]any{})
			case "fractional-id":
				reply(json.RawMessage("1.5"), map[string]any{})
			case "both":
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{},\"error\":{\"code\":1,\"message\":\"x\"}}\n", q.ID)
			case "missing-result":
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s}\n", q.ID)
			case "duplicate-key":
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"id\":%s,\"result\":{}}\n", q.ID, q.ID)
			case "null-error-code":
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":null,\"message\":\"error\"}}\n", q.ID)
			case "null-error-message":
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32603,\"message\":null}}\n", q.ID)
			case "oversize":
				fmt.Fprintln(os.Stdout, strings.Repeat("x", (8<<20)+1))
			case "truncated":
				fmt.Fprint(os.Stdout, "{\"jsonrpc\":")
				return
			case "exit":
				os.Exit(7)
			}
		case "fixture/permission", "fixture/permission-empty", "fixture/unsupported", "fixture/unsupported-numeric":
			parentCall = q.ID
			parentReplyID = json.RawMessage(`"agent-question"`)
			method := "session/request_permission"
			options := []any{map[string]any{"optionId": "allow-me", "kind": "allow_once"}, map[string]any{"optionId": "deny-me", "kind": "reject_once"}}
			if q.Method == "fixture/permission-empty" {
				options = nil
			}
			if q.Method == "fixture/unsupported" || q.Method == "fixture/unsupported-numeric" {
				method = "terminal/create"
			}
			if q.Method == "fixture/unsupported-numeric" {
				parentReplyID = q.ID
			}
			write(map[string]any{"jsonrpc": "2.0", "id": parentReplyID, "method": method, "params": map[string]any{"sessionId": "fixture-session", "options": options}})
		case "fixture/notify-permission":
			write(map[string]any{"jsonrpc": "2.0", "method": "session/request_permission", "params": map[string]any{"sessionId": "fixture-session"}})
			reply(q.ID, map[string]any{})
		case "fixture/escaped-version":
			fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"\\u0032.0\",\"id\":%s,\"result\":{}}\n", q.ID)
		case "fixture/uncorrelated-auth":
			write(map[string]any{"jsonrpc": "2.0", "id": 777777, "error": map[string]any{"code": 401, "message": "login required"}})
		case "fixture/visible-401":
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "HTTP 401 Unauthorized"}}}})
			reply(q.ID, map[string]any{})
		case "fixture/notifications":
			for range 100 {
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture-session"}})
			}
			reply(q.ID, map[string]any{})
		case "fixture/duplicate-response":
			reply(q.ID, map[string]any{})
			reply(q.ID, map[string]any{})
		case "fixture/stop-reading":
			reply(q.ID, map[string]any{})
			forever()
		case "fixture/grandchild":
			executable, _ := os.Executable()
			child := exec.Command(executable, "leaf")
			child.Env = []string{}
			child.Stdin = nil
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
			if err := child.Start(); err != nil {
				os.Exit(23)
			}
			group, _ := syscall.Getpgid(child.Process.Pid)
			reply(q.ID, map[string]any{"pid": child.Process.Pid, "group": group})
		case "fixture/exit-leader":
			reply(q.ID, map[string]any{})
			return
		case "fixture/auth-stderr":
			fmt.Fprintln(os.Stderr, "authentication failed: token expired; run kiro-cli login")
			return
		case "fixture/auth-error":
			write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32000, "message": "login required", "data": map[string]any{"statusCode": 401}}})
		case "fixture/generic-error":
			write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32603, "message": "private-prompt-sentinel", "data": map[string]any{"duration": 401}}})
		case "fixture/stderr":
			for range 200 {
				fmt.Fprintln(os.Stderr, "Authorization: Bearer synthetic-secret-abcdefghijklmnopqrstuvwxyz "+strings.Repeat("x", 8192))
			}
			reply(q.ID, map[string]any{})
		case "fixture/environment":
			reply(q.ID, map[string]any{"unexpected": os.Getenv("UNEXPECTED_PROVIDER_SECRET")})
		default:
			write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32601, "message": "unsupported fixture method"}})
		}
	}
}
