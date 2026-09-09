//go:build darwin

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
	"syscall"
	"time"
)

func inspectHeldHook(raw []byte, event, path string) (string, error) {
	var packet struct {
		Event string                     `json:"hook_event_name"`
		Tool  string                     `json:"tool_name"`
		ID    string                     `json:"tool_use_id"`
		Input map[string]json.RawMessage `json:"tool_input"`
	}
	var filename string
	if len(raw) > 64<<10 || json.Unmarshal(raw, &packet) != nil || packet.Event != event || packet.Tool != "Read" || len(packet.ID) == 0 || len(packet.ID) > 256 || len(packet.Input) != 1 || json.Unmarshal(packet.Input["file_path"], &filename) != nil || filename != path {
		return "", errors.New("independent hook input differs")
	}
	digest := sha256.Sum256([]byte(packet.ID))
	return hex.EncodeToString(digest[:]), nil
}

func heldHook(post bool) {
	fail := func() { record("guard-failed", nil); os.Exit(2) }
	inputTimer := time.AfterFunc(2*time.Second, fail)
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, (64<<10)+1))
	inputTimer.Stop()
	event := "PreToolUse"
	if post {
		event = "PostToolUse"
	}
	digest, inputErr := inspectHeldHook(raw, event, filepath.Join(cfg.Project, "read-fixture"))
	if err != nil || inputErr != nil {
		fail()
	}
	marker := filepath.Join(cfg.Root, "hook-admission")
	if post {
		previous, err := os.ReadFile(marker)
		if err != nil || string(previous) != digest {
			fail()
		}
		record("hook-post", nil)
		return
	}
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fail()
	}
	_, err = io.WriteString(f, digest)
	if f.Close() != nil || err != nil {
		fail()
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	record("hook-held", nil)
	timer, tick := time.NewTimer(20*time.Second), time.NewTicker(20*time.Millisecond)
	defer timer.Stop()
	defer tick.Stop()
	for {
		select {
		case <-interrupt:
			record("hook-interrupted", nil)
			os.Exit(2)
		case <-timer.C:
			fail()
		case <-tick.C:
			release, err := os.ReadFile(filepath.Join(cfg.Root, "hook-release"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || string(release) != "release-owned-hook" {
				fail()
			}
			record("hook-released", nil)
			fmt.Println(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`)
			return
		}
	}
}
