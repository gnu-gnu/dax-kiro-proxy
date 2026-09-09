package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/privatefs"
)

func runPluginInteractive(t *testing.T, parent context.Context, command childproc.Command, root, project, observations string, exchange *pluginToolExchange, requests *atomic.Int32) childproc.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	profile := ""
	for i, entry := range command.Environment {
		if value, ok := strings.CutPrefix(entry, "CLAUDE_CONFIG_DIR="); ok {
			profile = value
		}
		if strings.HasPrefix(entry, "TERM=") {
			command.Environment[i] = "TERM=xterm-256color"
		}
	}
	state, err := privatefs.Open(profile)
	if err != nil {
		t.Fatal("cannot open owned plugin client state")
	}
	data, err := state.Read(".claude.json", 2<<20)
	var global map[string]any
	if err != nil || json.Unmarshal(data, &global) != nil {
		t.Fatal("invalid owned plugin client state")
	}
	projects, ok := global["projects"].(map[string]any)
	if !ok {
		projects = make(map[string]any)
		global["projects"] = projects
	}
	entry, ok := projects[project].(map[string]any)
	if !ok {
		entry = make(map[string]any)
		projects[project] = entry
	}
	entry["hasTrustDialogAccepted"] = true
	data, err = json.Marshal(global)
	if err != nil || state.Write(".claude.json", data) != nil {
		t.Fatal("cannot trust independently owned empty project")
	}
	command.Environment = append(command.Environment, "COLUMNS=160", "LINES=40", "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1")
	pidPath, wrapper := filepath.Join(root, "plugin-client.pid"), filepath.Join(root, "plugin-client.sh")
	script := "#!/bin/sh\nset -euC\numask 077\nprintf '%s' \"$$\" > " + probeShellQuote(pidPath) + "\n/bin/stty rows 40 cols 160\nexec " + probeShellQuote(command.Executable) + " \"$@\"\n"
	if os.WriteFile(wrapper, []byte(script), 0700) != nil {
		t.Fatal("cannot create owned plugin terminal wrapper")
	}
	command.Executable = "/usr/bin/script"
	command.Args = append([]string{"-q", os.DevNull, "/bin/sh", wrapper}, command.Args...)
	const prompt = "Call the independent plugin's effect-free probe and return its result."
	var inputStage atomic.Int32
	var unexpectedRequest, readyBeforeInput, rendered atomic.Bool
	type outcome struct {
		result  childproc.Result
		answers int
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		result, answers, err := runObservedTerminal(ctx, owner, command, nil, func(screen string) string {
			lower := strings.ToLower(strings.Join(strings.Fields(screen), " "))
			if inputStage.Load() == 0 {
				if requests.Load() != 0 {
					unexpectedRequest.Store(true)
					return ""
				}
				ledger, err := readDenialArtifact(observations, "plugin", 8192)
				ready := err == nil && bytes.Contains(ledger, []byte(" initialized\n")) && bytes.Contains(ledger, []byte(" listed\n"))
				if ready && statusProjectVisible(lower, project) && strings.Contains(screen, "❯") && !strings.Contains(lower, "enter to continue") && !strings.Contains(lower, "do you want") {
					readyBeforeInput.Store(true)
					inputStage.Store(1)
					return prompt
				}
			} else if inputStage.Load() == 1 && strings.Contains(screen, prompt) {
				inputStage.Store(2)
				return "\r"
			}
			if inputStage.Load() == 2 && strings.Contains(screen, "independent plugin observation complete") {
				rendered.Store(true)
			}
			return ""
		}, true)
		done <- outcome{result, answers, err}
	}()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	pid, group := 0, 0
	owned, exited := false, false
	var got outcome
wait:
	for {
		select {
		case got = <-done:
			exited = true
			break wait
		case <-ctx.Done():
			break wait
		case <-tick.C:
			if !owned {
				data, err := readDenialArtifact(root, filepath.Base(pidPath), 32)
				if err == nil {
					pid, err = strconv.Atoi(string(data))
					if err == nil && pid > 1 {
						group, err = syscall.Getpgid(pid)
						owned = err == nil && group == pid && group != syscall.Getpgrp()
					}
				}
			}
			exchange.mu.Lock()
			complete := exchange.complete && !exchange.failed
			exchange.mu.Unlock()
			if owned && complete && rendered.Load() {
				break wait
			}
		}
	}
	if owned {
		_ = syscall.Kill(-group, syscall.SIGTERM)
		until := time.Now().Add(250 * time.Millisecond)
		for time.Now().Before(until) && !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			time.Sleep(10 * time.Millisecond)
		}
		if !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH) {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
	}
	cancel()
	if !exited {
		got = <-done
	}
	owner.Close()
	groupGone := owned && errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
	t.Logf("interactive_plugin_ready_before_input=%v, input_stage=%d, unexpected_request_before_input=%v, completion_rendered=%v, client_group_gone=%v, setup_stage=%d, terminal_bytes=%d", readyBeforeInput.Load(), inputStage.Load(), unexpectedRequest.Load(), rendered.Load(), groupGone, got.answers, len(got.result.Stdout))
	if !readyBeforeInput.Load() || inputStage.Load() != 2 || unexpectedRequest.Load() || !rendered.Load() || !groupGone || owner.Active() != 0 || errors.Is(got.err, childproc.ErrCleanup) || errors.Is(got.err, childproc.ErrIO) || errors.Is(got.err, childproc.ErrOutputLimit) {
		t.Error("interactive first plugin tool or cleanup was not established")
	}
	return got.result
}
