// Independent native hook for an owned Bash operation. It never executes the operation.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type expectation struct{ Session, Command string }
type receipt struct {
	PID, Group int
	Digest     string
}

func inspect(raw []byte, event string, want expectation) (string, error) {
	var p struct {
		Session string                     `json:"session_id"`
		Event   string                     `json:"hook_event_name"`
		Tool    string                     `json:"tool_name"`
		ID      string                     `json:"tool_use_id"`
		Input   map[string]json.RawMessage `json:"tool_input"`
	}
	bad := errors.New("owned hook request differs")
	if len(raw) > 64<<10 || json.Unmarshal(raw, &p) != nil || want.Session == "" || want.Command == "" || p.Session != want.Session || p.Event != event || p.Tool != "Bash" || len(p.ID) == 0 || len(p.ID) > 96 {
		return "", bad
	}
	for _, c := range p.ID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return "", bad
		}
	}
	var command string
	if json.Unmarshal(p.Input["command"], &command) != nil || command != want.Command {
		return "", bad
	}
	for key, value := range p.Input {
		if key == "command" {
			continue
		}
		var description string
		if key != "description" || json.Unmarshal(value, &description) != nil || len(description) > 256 || strings.ContainsAny(description, "\r\n") {
			return "", bad
		}
	}
	digest := sha256.Sum256([]byte(p.ID))
	return hex.EncodeToString(digest[:]), nil
}

func main() {
	if len(os.Args) != 3 || !filepath.IsAbs(os.Args[1]) || (os.Args[2] != "pre" && os.Args[2] != "post") {
		os.Exit(2)
	}
	root := filepath.Dir(os.Args[1])
	fail := func() { _ = os.WriteFile(filepath.Join(root, "hook-failed"), []byte("failed"), 0600); os.Exit(2) }
	timer := time.AfterFunc(2*time.Second, fail)
	read := func(path string, limit int64) ([]byte, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, limit+1))
		if len(data) > int(limit) {
			return nil, errors.New("owned hook limit")
		}
		return data, err
	}
	config, err := read(os.Args[1], 16<<10)
	var want expectation
	if err != nil || json.Unmarshal(config, &want) != nil {
		fail()
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, (64<<10)+1))
	event := "PreToolUse"
	if os.Args[2] == "post" {
		event = "PostToolUse"
	}
	digest, inspectErr := inspect(raw, event, want)
	if err != nil || inspectErr != nil {
		fail()
	}
	timer.Stop()
	appendReceipt := func(name string) {
		f, err := os.OpenFile(filepath.Join(root, name), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			fail()
		}
		stat, err := f.Stat()
		if err != nil || stat.Size() > 64 {
			f.Close()
			fail()
		}
		_, err = io.WriteString(f, "observed\n")
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			fail()
		}
	}
	record := func(name string) {
		data, _ := json.Marshal(receipt{os.Getpid(), syscall.Getpgrp(), digest})
		f, err := os.OpenFile(filepath.Join(root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fail()
		}
		_, err = f.Write(data)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			fail()
		}
	}
	if os.Args[2] == "post" {
		data, err := read(filepath.Join(root, "held-receipt"), 256)
		var prior receipt
		if err != nil || json.Unmarshal(data, &prior) != nil || prior.Digest != digest {
			fail()
		}
		appendReceipt("effect-post")
		record("post-receipt")
		fmt.Println(`{}`)
		return
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	appendReceipt("effect-pre")
	record("held-receipt")
	deadline, tick := time.NewTimer(30*time.Second), time.NewTicker(20*time.Millisecond)
	defer deadline.Stop()
	defer tick.Stop()
	for {
		select {
		case <-interrupt:
			if os.WriteFile(filepath.Join(root, "hook-stopped"), []byte("stopped"), 0600) != nil {
				fail()
			}
			os.Exit(2)
		case <-deadline.C:
			fail()
		case <-tick.C:
			data, err := read(filepath.Join(root, "hook-release"), 32)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || string(data) != "release-owned-hook" {
				fail()
			}
			if os.WriteFile(filepath.Join(root, "hook-released"), []byte("released"), 0600) != nil {
				fail()
			}
			fmt.Println(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`)
			return
		}
	}
}
