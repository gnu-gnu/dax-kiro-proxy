// Independently authored CLI boundary fixture. It reports only synthetic version/account/catalog
// data and records fixed command names in its explicitly supplied HOME. The ACP launch path checks
// owned agent/environment facts and execs the independently authored protocol fixture beside it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	name := filepath.Base(os.Args[0])
	args := strings.Join(os.Args[1:], " ")
	label := ""
	output := ""
	if name == "kiro-cli" && len(os.Args) == 6 && strings.Join(os.Args[1:5], " ") == "acp --agent-engine v2 --agent" {
		launchACP(os.Args[5])
		os.Exit(63)
	}
	switch {
	case name == "kiro-cli" && args == "--version":
		label = "kiro-version"
		output = "kiro-cli 2.21.3"
	case name == "kiro-cli-chat" && args == "--version":
		label = "helper-version"
		output = "kiro-cli-chat 2.21.3"
	case name == "kiro-cli" && args == "whoami --format json":
		label = "identity"
		output = `{"accountType":"synthetic","email":"startup-fixture@example.invalid"}`
	case name == "kiro-cli" && args == "chat --list-models --format json":
		label = "catalog"
		output = `{"default_model":"fixture-backend","models":[{"model_id":"fixture-backend","model_name":"Independent fixture"}]}`
	default:
		os.Exit(60)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "preflight-slow")); err == nil {
		time.Sleep(150 * time.Millisecond)
	}
	file, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "preflight-observations"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(61)
	}
	_, err = fmt.Fprintln(file, label)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		os.Exit(62)
	}
	fmt.Println(output)
}

func launchACP(name string) {
	fail := func(code int) {
		file, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "preflight-observations"), os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "acp-fixture-failure-%d\n", code)
			_ = file.Close()
		}
		os.Exit(code)
	}
	if !strings.HasPrefix(name, "dax-") || strings.ContainsAny(name, "/\\\x00\r\n") {
		fail(64)
	}
	cwd, err := os.Getwd()
	root := filepath.Dir(os.Getenv("KIRO_HOME"))
	root, rootErr := filepath.EvalSymlinks(root)
	cwd, cwdErr := filepath.EvalSymlinks(cwd)
	if err != nil || rootErr != nil || cwdErr != nil || !filepath.IsAbs(root) || !strings.HasPrefix(cwd, filepath.Join(root, "agents")+string(os.PathSeparator)) || os.Getenv("TERM") != "dumb" {
		fail(65)
	}
	settings, err := os.ReadFile(filepath.Join(os.Getenv("KIRO_HOME"), "settings", "cli.json"))
	if err != nil || string(settings) != `{"chat.disableInheritingDefaultResources":true}` {
		fail(66)
	}
	path := filepath.Join(cwd, ".kiro", "agents", name+".json")
	stat, err := os.Lstat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm() != 0600 || stat.Size() > 64<<10 {
		fail(67)
	}
	raw, err := os.ReadFile(path)
	var agent struct {
		Tools, AllowedTools, Resources []string
		Hooks                          map[string]any
		IncludeMcpJson                 bool
		MCPServers                     map[string]struct {
			Command string
			Args    []string
			Env     map[string]string
		}
	}
	if err != nil || json.Unmarshal(raw, &agent) != nil || agent.IncludeMcpJson || len(agent.Resources) != 0 || len(agent.Hooks) != 0 || len(agent.MCPServers) != 1 || len(agent.Tools) > 1 || strings.Join(agent.Tools, " ") != strings.Join(agent.AllowedTools, " ") {
		fail(68)
	}
	relay, present := agent.MCPServers["dax_session"]
	if !present || !filepath.IsAbs(relay.Command) || len(relay.Args) != 3 || relay.Args[0] != "relay" || relay.Args[1] != "--config" || !filepath.IsAbs(relay.Args[2]) || len(relay.Env) != 0 {
		fail(69)
	}
	executable, err := os.Executable()
	if err != nil {
		fail(70)
	}
	peer := filepath.Join(filepath.Dir(executable), "acp")
	args := []string{peer, "chat"}
	if len(agent.Tools) != 0 {
		if !strings.HasPrefix(agent.Tools[0], "@dax_session/relay_") {
			fail(71)
		}
		manifest := filepath.Join(cwd, "independent-relay.json")
		data, _ := json.Marshal(map[string]any{"command": relay.Command, "args": relay.Args, "env": []any{}})
		if os.WriteFile(manifest, data, 0600) != nil {
			fail(72)
		}
		args = []string{peer, "chat-tools-launch", manifest}
	}
	file, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "preflight-observations"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		fail(73)
	}
	_, err = fmt.Fprintln(file, "acp-launch")
	if file.Close() != nil || err != nil {
		fail(74)
	}
	_ = syscall.Exec(peer, args, os.Environ())
}
