//go:build darwin

// Independently authored, test-only CLI/ACP observer. Native frames are forwarded unchanged;
// only fixed events and owned lifecycle coordinates are recorded. No tool effect is executed.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type config struct {
	HeldHook                                        bool
	Root, Proxy, Client, Kiro, AccountHome, Project string
	Args                                            []string
}

var cfg config
var events sync.Mutex
var eventBytes int
var titleScope atomic.Bool

func record(kind string, values map[string]any) {
	events.Lock()
	defer events.Unlock()
	if values == nil {
		values = make(map[string]any)
	}
	values["kind"], values["pid"], values["group"] = kind, os.Getpid(), syscall.Getpgrp()
	values["title_scope"] = titleScope.Load()
	data, _ := json.Marshal(values)
	eventBytes += len(data) + 1
	if eventBytes > 64<<10 {
		os.Exit(72)
	}
	f, err := os.OpenFile(filepath.Join(cfg.Root, "events", strconv.Itoa(os.Getpid())+".jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		os.Exit(72)
	}
	_, err = f.Write(append(data, '\n'))
	if f.Close() != nil || err != nil {
		os.Exit(72)
	}
}

func main() {
	self, err := os.Executable()
	if err != nil {
		os.Exit(70)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(self), "terminal.json"))
	if err != nil || len(data) > 16<<10 || json.Unmarshal(data, &cfg) != nil || !filepath.IsAbs(cfg.Root) {
		os.Exit(70)
	}
	timer := time.AfterFunc(90*time.Second, func() { record("guard-failed", nil); os.Exit(74) })
	defer timer.Stop()
	role := filepath.Base(os.Args[0])
	switch role {
	case "supervisor":
		supervise()
	case "held-hook", "post-hook":
		heldHook(role == "post-hook")
	case "claude":
		if len(os.Args) == 2 && os.Args[1] == "--version" {
			_ = syscall.Exec(cfg.Client, append([]string{cfg.Client}, os.Args[1:]...), os.Environ())
			os.Exit(71)
		}
		profile := os.Getenv("CLAUDE_CONFIG_DIR")
		if !strings.HasPrefix(profile, cfg.Root+string(os.PathSeparator)) {
			os.Exit(71)
		}
		state := filepath.Join(profile, ".claude.json")
		data, err := os.ReadFile(state)
		var global map[string]any
		if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &global) != nil {
			os.Exit(71)
		}
		projects, _ := global["projects"].(map[string]any)
		if projects == nil {
			projects = make(map[string]any)
			global["projects"] = projects
		}
		project, _ := projects[cfg.Project].(map[string]any)
		if project == nil {
			project = make(map[string]any)
			projects[cfg.Project] = project
		}
		project["hasTrustDialogAccepted"] = true
		data, _ = json.Marshal(global)
		if os.WriteFile(state, data, 0600) != nil {
			os.Exit(71)
		}
		foreground, _ := unix.IoctlGetInt(0, unix.TIOCGPGRP)
		record("client", map[string]any{"profile": profile, "endpoint": os.Getenv("ANTHROPIC_BASE_URL"), "foreground": foreground})
		args := append([]string{cfg.Client}, os.Args[1:]...)
		toolList, instruction := "", "Follow the user's text-only instruction. Do not use tools."
		if cfg.HeldHook {
			toolList, instruction = "Read", "Use only the supplied Read tool, exactly once for the user's specified path. Do not use any other tool."
		}
		args = append(args, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--tools", toolList, "--system-prompt", instruction)
		env := append(os.Environ(), "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1", "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
		_ = syscall.Exec(cfg.Client, args, env)
		os.Exit(71)
	case "kiro-cli", "kiro-cli-chat":
		acp := len(os.Args) > 1 && os.Args[1] == "acp"
		if cfg.Kiro == "" {
			if acp {
				record("acp", nil)
				fakeACP()
				return
			}
			switch strings.Join(os.Args[1:], " ") {
			case "--version":
				fmt.Println(role + " 2.21.2")
			case "whoami --format json":
				fmt.Println(`{"accountType":"fixture","email":"terminal@example.invalid"}`)
			case "chat --list-models --format json":
				fmt.Println(`{"default_model":"fixture-backend","models":[{"model_id":"fixture-backend","model_name":"Independent terminal stream"}]}`)
			default:
				os.Exit(71)
			}
			return
		}
		target := cfg.Kiro
		if role == "kiro-cli-chat" {
			target = filepath.Join(filepath.Dir(target), role)
		}
		env := []string{}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "HOME=") {
				env = append(env, entry)
			}
		}
		env = append(env, "HOME="+cfg.AccountHome)
		if !acp {
			_ = syscall.Exec(target, append([]string{target}, os.Args[1:]...), env)
			os.Exit(71)
		}
		record("acp", nil)
		os.Exit(bridge(target, env))
	default:
		os.Exit(70)
	}
}

func supervise() {
	if unix.IoctlSetWinsize(0, unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 160}) != nil {
		os.Exit(71)
	}
	before, err := unix.IoctlGetTermios(0, unix.TIOCGETA)
	group, groupErr := unix.IoctlGetInt(0, unix.TIOCGPGRP)
	if err != nil || groupErr != nil || group != syscall.Getpgrp() {
		os.Exit(71)
	}
	record("supervisor", nil)
	cmd := exec.Command(cfg.Proxy, cfg.Args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if cmd.Start() != nil {
		os.Exit(71)
	}
	record("proxy", map[string]any{"child": cmd.Process.Pid})
	waitErr := cmd.Wait()
	after, stateErr := unix.IoctlGetTermios(0, unix.TIOCGETA)
	afterGroup, groupErr := unix.IoctlGetInt(0, unix.TIOCGPGRP)
	before.Lflag &^= unix.PENDIN
	if after != nil {
		after.Lflag &^= unix.PENDIN
	}
	restored := stateErr == nil && groupErr == nil && afterGroup == group && after != nil && *after == *before
	code := cmd.ProcessState.ExitCode()
	record("proxy-exit", map[string]any{"code": code, "restored": restored})
	if waitErr != nil || !restored {
		os.Exit(73)
	}
}

func forward(r io.Reader, w io.Writer, from string, g *frameGuard) error {
	reader := bufio.NewReaderSize(r, 256<<10)
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return err
		}
		if from == "client" {
			notePrompt(line)
		}
		kind, err := g.inspect(from, line)
		if err != nil {
			record("guard-failed", nil)
			return err
		}
		if kind == "prompt" && admitPrompt(titleScope.Load()) != nil {
			record("guard-failed", nil)
			return errFrame
		}
		if _, err = w.Write(line); err != nil {
			return err
		}
		if kind != "" {
			record(kind, nil)
		}
	}
}

func notePrompt(raw []byte) {
	var packet struct{ Method string }
	if json.Unmarshal(raw, &packet) == nil && packet.Method == "session/prompt" {
		lower := bytes.ToLower(raw)
		titleScope.Store(bytes.Contains(lower, []byte("title")))
		record("prompt-attempt", map[string]any{
			"main_hint":    bytes.Contains(raw, []byte("concatenation of Ready and _47")),
			"title_hint":   bytes.Contains(lower, []byte("title")),
			"summary_hint": bytes.Contains(lower, []byte("summar")),
			"prompt_bytes": len(raw),
		})
	}
}

// The exclusive marker also bounds prompts across replacement ACP observer processes.
func admitPrompt(title bool) error {
	name := "prompt-admission-main"
	if title {
		name = "prompt-admission-title"
	}
	f, err := os.OpenFile(filepath.Join(cfg.Root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errFrame
	}
	_, err = fmt.Fprintln(f, os.Getpid())
	if f.Close() != nil || err != nil {
		return errFrame
	}
	return nil
}

func bridge(target string, env []string) int {
	cmd := exec.Command(target, os.Args[1:]...)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 71
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 71
	}
	if cmd.Start() != nil {
		return 71
	}
	record("agent", map[string]any{"child": cmd.Process.Pid})
	var g frameGuard
	go func() {
		if err := forward(os.Stdin, stdin, "client", &g); err != nil && !errors.Is(err, io.EOF) {
			record("guard-failed", nil)
			_ = cmd.Process.Kill()
		}
		stdin.Close()
	}()
	err = forward(stdout, os.Stdout, "agent", &g)
	if err != nil && !errors.Is(err, io.EOF) {
		record("guard-failed", nil)
		_ = cmd.Process.Kill()
	}
	if cmd.Wait() != nil {
		return 71
	}
	return 0
}

func fakeACP() {
	var g frameGuard
	var output sync.Mutex
	send := func(value any) {
		output.Lock()
		defer output.Unlock()
		data, _ := json.Marshal(value)
		kind, err := g.inspect("agent", data)
		if err != nil {
			record("guard-failed", nil)
			os.Exit(71)
		}
		fmt.Println(string(data))
		if kind != "" {
			record(kind, nil)
		}
	}
	reply := func(id json.RawMessage, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	var cancel chan struct{}
	reader := bufio.NewReaderSize(os.Stdin, 256<<10)
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return
		}
		notePrompt(line)
		kind, err := g.inspect("client", line)
		if err != nil {
			record("guard-failed", nil)
			return
		}
		if kind == "prompt" && admitPrompt(titleScope.Load()) != nil {
			record("guard-failed", nil)
			return
		}
		if kind != "" {
			record(kind, nil)
		}
		var r struct {
			ID     json.RawMessage
			Method string
		}
		if json.Unmarshal(line, &r) != nil {
			return
		}
		switch r.Method {
		case "initialize":
			reply(r.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}, "agentInfo": map[string]string{"name": "independent-terminal", "version": "1"}})
		case "session/new":
			reply(r.ID, map[string]any{"sessionId": "owned-stream", "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Independent"}}}})
		case "session/set_model":
			reply(r.ID, map[string]any{})
		case "session/prompt":
			if titleScope.Load() {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": `{"title":"Independent keyboard test"}`}}}})
				reply(r.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			cancel = make(chan struct{})
			if cfg.HeldHook {
				go func(id json.RawMessage) {
					if err := fakeHeldTool(); err != nil {
						if !errors.Is(err, io.EOF) {
							record("guard-failed", nil)
						}
						return
					}
					send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "HookControl_47"}}}})
					reply(id, map[string]string{"stopReason": "end_turn"})
				}(r.ID)
				continue
			}
			go func(id json.RawMessage, stop <-chan struct{}) {
				for i := 0; i < 40; i++ {
					text := fmt.Sprintf("%d\n", i)
					if i == 0 {
						text = "Ready_47\n"
					}
					send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
					select {
					case <-stop:
						reply(id, map[string]string{"stopReason": "cancelled"})
						return
					case <-time.After(150 * time.Millisecond):
					}
				}
				reply(id, map[string]string{"stopReason": "end_turn"})
			}(r.ID, cancel)
		case "session/cancel":
			if cancel != nil {
				close(cancel)
				cancel = nil
			}
		default:
			if len(r.ID) > 0 {
				send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32601, "message": "Independent fixture method absent"}})
			}
		}
	}
}
