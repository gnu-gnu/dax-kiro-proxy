package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"
)

// Independent ACP peer: emit only progress until the controlled final-answer time.
func progressFixture(mode string) {
	if len(os.Args) != 3 || os.WriteFile(os.Args[2], []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
		os.Exit(101)
	}
	var workers sync.WaitGroup
	var cancel context.CancelFunc
	defer func() {
		if cancel != nil {
			cancel()
		}
		workers.Wait()
	}()
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		var q request
		if json.Unmarshal(scan.Bytes(), &q) != nil {
			os.Exit(102)
		}
		switch q.Method {
		case "initialize":
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}})
		case "session/new":
			reply(q.ID, map[string]any{"sessionId": "progress-session", "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]any{"modelId": "fixture-backend", "name": "Progress fixture"}}}})
		case "session/prompt":
			if cancel != nil {
				os.Exit(103)
			}
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			workers.Add(1)
			go func(id json.RawMessage) {
				defer workers.Done()
				progressFixturePrompt(ctx, id, mode)
			}(q.ID)
		case "session/cancel":
			if cancel != nil {
				cancel()
			}
		}
	}
}

func progressFixturePrompt(ctx context.Context, id json.RawMessage, mode string) {
	emit := func(update map[string]any) {
		write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "progress-session", "update": update}})
	}
	update := map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "Unpublished fixture reasoning."}}
	if mode == "plan" {
		update = map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "Unpublished fixture plan.", "priority": "medium", "status": "pending"}}}
	}
	if mode == "tool" {
		emit(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "observed-fixture-call", "title": "Unpublished fixture title.", "kind": "think"})
		update = map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "observed-fixture-call", "status": "in_progress"}
	}
	if mode == "none" {
		emit(map[string]any{"sessionUpdate": "unknown_activity", "counter": 1})
		update = map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": ""}}
	}
	emit(update)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	delay := 450 * time.Millisecond
	if mode == "total" || mode == "cancel" {
		delay = 3 * time.Second
	}
	final := time.NewTimer(delay)
	defer final.Stop()
	for {
		select {
		case <-ctx.Done():
			reply(id, map[string]any{"stopReason": "cancelled"})
			return
		case <-ticker.C:
			emit(update)
		case <-final.C:
			emit(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Progress fixture answer."}})
			reply(id, map[string]any{"stopReason": "end_turn"})
			return
		}
	}
}
