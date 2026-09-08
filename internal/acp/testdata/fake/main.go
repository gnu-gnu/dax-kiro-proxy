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

func write(v any)                     { b, _ := json.Marshal(v); _, _ = os.Stdout.Write(append(b, '\n')) }
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
		case "session/new":
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
			session = "fixture-conversation"
			if mode == "chat-no-id" {
				reply(q.ID, map[string]any{})
				continue
			}
			reply(q.ID, map[string]any{"sessionId": session, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]any{"modelId": "fixture-backend", "name": "Fixture", "description": "Synthetic model"}}}})
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
				owner := session
				if mode == "chat-wrong-session" {
					owner = "some-other-session"
				}
				write(map[string]any{"jsonrpc": "2.0", "method": "_fixture/diagnostic", "params": map[string]any{"sessionId": owner}})
				chunks := []string{"birch ", "stone"}
				if mode == "chat-project" {
					chunks = []string{string(q.Params)}
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
				if mode == "chat-slow" {
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
