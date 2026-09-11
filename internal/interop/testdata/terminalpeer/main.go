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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type config struct {
	HistoryStage                        int
	HistorySeed, HistoryID, HistoryName string
	ModelCheck                          bool
	ModelEntries                        int
	ModelIDs                            [2]string
	AllowFollowup                       bool
	HeldHook                            bool
	// Reproduce the measured logged-out boundary on the follow-up prompt: one stderr line naming
	// the login command, nothing on stdout, exit status 1 (D117).
	AuthExpiry bool
	// Reproduce the same boundary while a relayed tool call is still waiting (D117): the process
	// exits with the login line once the parent writes the auth-cut marker.
	AuthCut bool
	// Answer this many numbered soak questions after the first turn, each with its echoed marker,
	// so the parent can sample the proxy's resources across many turns in one session (D120).
	SoakTurns                                       int
	Root, Proxy, Client, Kiro, AccountHome, Project string
	Args                                            []string
}

var cfg config
var events sync.Mutex
var eventBytes int
var titleScope atomic.Bool
var followScope atomic.Bool
var recoveryScope atomic.Bool
var soakScope atomic.Int32
var soakTitles atomic.Int32
var soakPattern = regexp.MustCompile(`concatenation of Soak and _([0-9]{1,3})`)

func record(kind string, values map[string]any) {
	events.Lock()
	defer events.Unlock()
	if values == nil {
		values = make(map[string]any)
	}
	values["kind"], values["pid"], values["group"] = kind, os.Getpid(), syscall.Getpgrp()
	values["title_scope"] = titleScope.Load()
	values["follow_scope"] = followScope.Load()
	values["recovery_scope"] = recoveryScope.Load()
	values["soak_scope"] = soakScope.Load()
	data, _ := json.Marshal(values)
	eventBytes += len(data) + 1
	limit := 64 << 10
	if cfg.SoakTurns > 0 {
		limit = 2 << 20 // many numbered turns write proportionally more fixed-shape receipts
	}
	if eventBytes > limit {
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
	observedModel.models = cfg.ModelIDs
	observedHistory.stage, observedHistory.seed = cfg.HistoryStage, cfg.HistorySeed
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
		foreground, _ := unix.IoctlGetInt(0, unix.TIOCGPGRP)
		record("client", map[string]any{"profile": profile, "endpoint": os.Getenv("ANTHROPIC_BASE_URL"), "foreground": foreground})
		args := append([]string{cfg.Client}, os.Args[1:]...)
		toolList, instruction := "", "Follow the user's text-only instruction. Do not use tools."
		if cfg.HeldHook {
			toolList, instruction = "Read", "Use only the supplied Read tool, exactly once for the user's specified path. Do not use any other tool."
		}
		args = append(args, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--tools", toolList, "--system-prompt", instruction)
		env := append(os.Environ(), "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
		if cfg.HistoryStage == 0 {
			env = append(env, "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1")
		} else if cfg.HistoryStage == 1 {
			args = append(args, "--session-id", cfg.HistoryID)
			if cfg.HistoryName != "" {
				args = append(args, "--name", cfg.HistoryName)
			}
		}
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
				fmt.Println(role + " 2.21.3")
			case "whoami --format json":
				fmt.Println(`{"accountType":"fixture","email":"terminal@example.invalid"}`)
			case "chat --list-models --format json":
				if cfg.ModelCheck {
					var rows []map[string]string
					for _, row := range fakeModelEntries() {
						rows = append(rows, map[string]string{"model_id": row["modelId"], "model_name": row["name"]})
					}
					data, _ := json.Marshal(map[string]any{"default_model": "fixture-backend", "models": rows})
					fmt.Println(string(data))
				} else {
					fmt.Println(`{"default_model":"fixture-backend","models":[{"model_id":"fixture-backend","model_name":"Independent terminal stream"}]}`)
				}
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
		if err == nil {
			err = noteModel(from, line)
		}
		if err == nil {
			err = noteHistory(from, line)
		}
		if err != nil {
			record("guard-failed", nil)
			return err
		}
		if kind == "prompt" && admitObservedPrompt(titleScope.Load(), followScope.Load()) != nil {
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
		followScope.Store(bytes.Contains(raw, []byte("concatenation of Follow and _49")))
		recoveryScope.Store(bytes.Contains(raw, []byte("concatenation of Recovered and _53")))
		soakScope.Store(0)
		if m := soakPattern.FindSubmatch(raw); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			soakScope.Store(int32(n))
		}
		if cfg.ModelCheck {
			followScope.Store(bytes.Contains(raw, []byte("concatenation of ModelSecond and _67")))
		}
		record("prompt-attempt", map[string]any{
			"main_hint":        bytes.Contains(raw, []byte("concatenation of Ready and _47")),
			"title_hint":       bytes.Contains(lower, []byte("title")),
			"summary_hint":     bytes.Contains(lower, []byte("summar")),
			"prompt_bytes":     len(raw),
			"old_input":        bytes.Contains(raw, []byte("concatenation of Ready and _47")) || bytes.Contains(raw, []byte("concatenation of HookControl and _47")),
			"partial_marker":   bytes.Contains(raw, []byte("Ready_47")),
			"new_input":        followScope.Load(),
			"recovery_input":   recoveryScope.Load(),
			"null_marker":      bytes.Contains(raw, []byte("UnsentControl_53")),
			"interrupted_form": interruptedForm(raw),
		})
	}
}

// interruptedForm counts the historical tool blocks a prompt carries. Historical blocks are
// embedded as escaped JSON strings inside the projected context; only fixed counts leave the peer.
func interruptedForm(raw []byte) string {
	var packet struct {
		Params struct {
			Prompt []struct{ Type, Text string }
		}
	}
	if json.Unmarshal(raw, &packet) != nil {
		return ""
	}
	var parts []string
	for _, part := range packet.Params.Prompt {
		parts = append(parts, part.Text)
	}
	text := strings.Join(parts, "\n")
	uses, results, errorFlags := strings.Count(text, `\"tool_use\"`), strings.Count(text, `\"tool_result\"`), strings.Count(text, `\"is_error\"`)
	interrupted, continuation, placeholder := strings.Count(text, "[Request interrupted by user for tool use]"), strings.Count(text, "Continue from where you left off."), strings.Count(text, "No response requested.")
	if uses+results+errorFlags+interrupted+continuation+placeholder == 0 {
		return ""
	}
	return fmt.Sprintf("tu=%d tr=%d ef=%d ir=%d ct=%d ph=%d", uses, results, errorFlags, interrupted, continuation, placeholder)
}

// The exclusive marker also bounds prompts across replacement ACP observer processes.
func admitPrompt(title bool) error {
	name := "prompt-admission-main"
	if title {
		name = "prompt-admission-title"
	}
	return admitNamed(name)
}

func admitObservedPrompt(title, follow bool) error {
	intent := func() bool {
		data, err := os.ReadFile(filepath.Join(cfg.Root, "followup-allowed"))
		return cfg.AllowFollowup && err == nil && string(data) == "owned-new-question"
	}
	// The recovery question after a reproduced login loss needs its own parent authorization.
	recoveryIntent := func() bool {
		data, err := os.ReadFile(filepath.Join(cfg.Root, "recovery-allowed"))
		return cfg.AuthExpiry && err == nil && string(data) == "owned-recovery-question"
	}
	if cfg.SoakTurns > 0 {
		// Soak questions are admitted one at a time by the parent's counter; titles are unbounded
		// within the declared turn budget because the client may retitle a long session.
		if title {
			return admitNamed("prompt-admission-title-" + strconv.Itoa(int(soakTitles.Add(1))))
		}
		if n := soakScope.Load(); n > 0 {
			data, err := os.ReadFile(filepath.Join(cfg.Root, "soak-allowed"))
			allowed, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || convErr != nil || int(n) > allowed || int(n) > cfg.SoakTurns {
				return errFrame
			}
			return admitNamed("prompt-admission-soak-" + strconv.Itoa(int(n)))
		}
	}
	if !title && recoveryScope.Load() {
		if !recoveryIntent() {
			return errFrame
		}
		return admitNamed("prompt-admission-recovery")
	}
	if !title && follow {
		if !intent() {
			return errFrame
		}
		return admitNamed("prompt-admission-followup")
	}
	err := admitPrompt(title)
	if title && errors.Is(err, os.ErrExist) && intent() {
		err = admitNamed("prompt-admission-second-title")
		if errors.Is(err, os.ErrExist) && recoveryIntent() {
			return admitNamed("prompt-admission-third-title")
		}
	}
	return err
}

func admitNamed(name string) error {
	f, err := os.OpenFile(filepath.Join(cfg.Root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
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
	if cfg.AllowFollowup {
		g.maxPrompts = 2
	}
	if cfg.SoakTurns > 0 {
		// An actual backend streams many frames per turn; scale the frame budget with the turns.
		g.maxPrompts, g.maxFrames, g.maxBytes = cfg.SoakTurns+1, 1024+128*cfg.SoakTurns, 8<<20+(256<<10)*cfg.SoakTurns
	}
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
	if cfg.AllowFollowup {
		g.maxPrompts = 2
	}
	if cfg.SoakTurns > 0 {
		// An actual backend streams many frames per turn; scale the frame budget with the turns.
		g.maxPrompts, g.maxFrames, g.maxBytes = cfg.SoakTurns+1, 1024+128*cfg.SoakTurns, 8<<20+(256<<10)*cfg.SoakTurns
	}
	var output sync.Mutex
	send := func(value any) {
		output.Lock()
		defer output.Unlock()
		data, _ := json.Marshal(value)
		kind, err := g.inspect("agent", data)
		if err == nil {
			err = noteModel("agent", data)
		}
		if err == nil {
			err = noteHistory("agent", data)
		}
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
		if err == nil {
			err = noteModel("client", line)
		}
		if err == nil {
			err = noteHistory("client", line)
		}
		if err != nil {
			record("guard-failed", nil)
			return
		}
		if kind == "prompt" && admitObservedPrompt(titleScope.Load(), followScope.Load()) != nil {
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
			models := []map[string]string{{"modelId": "fixture-backend", "name": "Independent first"}}
			if cfg.ModelCheck {
				models = fakeModelEntries()
			}
			reply(r.ID, map[string]any{"sessionId": "owned-stream", "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": models}})
		case "session/set_model":
			reply(r.ID, map[string]any{})
		case "session/prompt":
			if titleScope.Load() {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": `{"title":"Independent keyboard test"}`}}}})
				reply(r.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			cancel = make(chan struct{})
			if cfg.HistoryStage > 0 {
				text := "ArchiveUI_101"
				if cfg.HistoryStage == 2 {
					text = cfg.HistorySeed + " ArchiveUI_107"
				}
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
				reply(r.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if cfg.ModelCheck {
				text := "ModelFirst_61"
				if followScope.Load() {
					text = "ModelSecond_67"
				}
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
				reply(r.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if n := soakScope.Load(); n > 0 && cfg.SoakTurns > 0 {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "Soak_" + strconv.Itoa(int(n))}}}})
				reply(r.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if recoveryScope.Load() {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "Recovered_53"}}}})
				reply(r.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
			if followScope.Load() && cfg.AuthExpiry {
				record("acp-auth-exit", nil)
				fmt.Fprintln(os.Stderr, "error: You are not logged in, please log in with kiro-cli login")
				os.Exit(1)
			}
			if followScope.Load() {
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "owned-stream", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "Follow_49"}}}})
				reply(r.ID, map[string]string{"stopReason": "end_turn"})
				continue
			}
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

func fakeModelEntries() []map[string]string {
	rows := []map[string]string{{"modelId": "fixture-backend", "name": "Independent first"}, {"modelId": "fixture-target", "name": "Independent second"}}
	for i := 2; i < min(cfg.ModelEntries, 32); i++ {
		rows = append(rows, map[string]string{"modelId": fmt.Sprintf("fixture-spare-%02d", i), "name": fmt.Sprintf("Independent unused model %02d", i)})
	}
	return rows
}
