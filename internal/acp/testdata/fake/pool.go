package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// This independently authored process exercises public multi-session routing without external I/O.
func poolFixture(mode string) {
	var launch struct {
		Aliases     []string `json:"aliases"`
		RelayConfig string   `json:"relayConfig"`
	}
	if mode == "pool-prepared" {
		if len(os.Args) != 3 {
			os.Exit(67)
		}
		raw, err := os.ReadFile(os.Args[2])
		if err != nil || json.Unmarshal(raw, &launch) != nil || launch.Aliases == nil || launch.RelayConfig == "" {
			os.Exit(68)
		}
	}
	launchDirectory, _ := os.Getwd()
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 9<<20)
	var mu sync.Mutex
	counts := map[string]int{}
	loaded := map[string]bool{}
	sessionDirectories := map[string]string{}
	mcpCounts := map[string]int{}
	next := 0
	emit := func(id, text string) {
		write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": id, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
	}
	for input.Scan() {
		var q request
		if json.Unmarshal(input.Bytes(), &q) != nil {
			os.Exit(60)
		}
		switch q.Method {
		case "initialize":
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]bool{"loadSession": mode != "pool-no-load"}})
		case "session/new", "session/load":
			var p struct {
				Session string            `json:"sessionId"`
				CWD     string            `json:"cwd"`
				MCP     []json.RawMessage `json:"mcpServers"`
			}
			if json.Unmarshal(q.Params, &p) != nil || p.CWD == "" || p.MCP == nil {
				os.Exit(61)
			}
			next++
			if mode == "pool-prepared" && (next != 1 || len(p.MCP) != 0) {
				os.Exit(69)
			}
			id := fmt.Sprintf("pool-session-%d", next)
			if q.Method == "session/load" {
				id = p.Session
				if mode == "pool-load-fail" {
					write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32000, "message": "synthetic load failure"}})
					continue
				}
			}
			mu.Lock()
			counts[id] = 0
			loaded[id] = q.Method == "session/load"
			sessionDirectories[id] = p.CWD
			mcpCounts[id] = len(p.MCP)
			mu.Unlock()
			if q.Method == "session/load" {
				emit(id, "synthetic replay that must be discarded")
			} else if mode == "pool-early" || mode == "pool-ambiguous" {
				if next == 2 {
					emit("pool-session-1", "established notification")
				}
				emit(id, "early notification")
				if mode == "pool-ambiguous" {
					emit("unbound-other-session", "ambiguous notification")
				}
			}
			if mode == "pool-load-mismatch" && q.Method == "session/load" {
				id = "inconsistent-loaded-id"
			}
			if mode == "pool-load-null" && q.Method == "session/load" {
				reply(q.ID, nil)
				continue
			}
			reply(q.ID, map[string]any{"sessionId": id, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Fixture"}}}})
		case "session/prompt":
			if mode == "pool-prepared" {
				if _, err := os.Stat(os.Args[2]); err != nil {
					os.Exit(70)
				}
			} else if len(os.Args) > 2 {
				entries, err := os.ReadDir(os.Args[2])
				if err != nil {
					os.Exit(65)
				}
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".json") {
						if _, err := os.Stat(filepath.Join(os.Args[2], entry.Name())); err == nil {
							os.Exit(66)
						}
					}
				}
			}
			var p struct {
				Session string            `json:"sessionId"`
				Prompt  []json.RawMessage `json:"prompt"`
			}
			if json.Unmarshal(q.Params, &p) != nil || len(p.Prompt) == 0 {
				os.Exit(62)
			}
			mu.Lock()
			count, ok := counts[p.Session]
			wasLoaded := loaded[p.Session]
			sessionDirectory, mcpCount := sessionDirectories[p.Session], mcpCounts[p.Session]
			count++
			counts[p.Session] = count
			mu.Unlock()
			if !ok {
				os.Exit(63)
			}
			if mode == "pool-hang" {
				continue
			}
			id := append(json.RawMessage(nil), q.ID...)
			go func() {
				if mode == "pool-metadata" {
					for range 3 {
						write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/metadata", "params": map[string]any{"sessionId": p.Session, "contextUsagePercentage": 12.5, "turnDurationMs": 725, "meteringUsage": []any{map[string]any{"unit": "credits", "value": 0.025}}, "unknown": "synthetic-private-content"}})
					}
				}
				if mode == "pool-concurrent" {
					for i := range 32 {
						emit(p.Session, fmt.Sprintf("%s:%d", p.Session, i))
						time.Sleep(time.Millisecond)
					}
				} else {
					observation := map[string]any{"pid": os.Getpid(), "session": p.Session, "promptCount": count, "prompt": p.Prompt, "loaded": wasLoaded}
					if mode == "pool-prepared" {
						observation["launchDirectory"], observation["sessionDirectory"] = launchDirectory, sessionDirectory
						observation["mcpCount"], observation["launchAliases"], observation["relayConfig"], observation["launchMarker"] = mcpCount, launch.Aliases, launch.RelayConfig, os.Args[2]
					}
					body, _ := json.Marshal(observation)
					emit(p.Session, string(body))
				}
				reply(id, map[string]string{"stopReason": "end_turn"})
			}()
		case "fixture/crash":
			os.Exit(64)
		case "session/set_model":
			reply(q.ID, map[string]any{})
		case "session/cancel":
		default:
			reply(q.ID, map[string]any{})
		}
	}
}
