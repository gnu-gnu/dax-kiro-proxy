// Independently authored ACP peer for a two-process native conversation restart.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 3 || (os.Args[1] != "0" && os.Args[1] != "1") {
		os.Exit(70)
	}
	timer := time.AfterFunc(30*time.Second, func() { os.Exit(71) })
	defer timer.Stop()
	if os.WriteFile(os.Args[2], []byte(fmt.Sprint(os.Getpid())), 0600) != nil {
		os.Exit(72)
	}
	write := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			os.Exit(73)
		}
		if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
			os.Exit(73)
		}
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	initialized, created, prompted := false, false, false
	for scan.Scan() {
		var r struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &r) != nil {
			os.Exit(74)
		}
		var result any
		switch r.Method {
		case "initialize":
			var p struct {
				ProtocolVersion    int
				ClientCapabilities map[string]any
			}
			if initialized || json.Unmarshal(r.Params, &p) != nil || p.ProtocolVersion != 1 || p.ClientCapabilities == nil || len(p.ClientCapabilities) != 0 {
				os.Exit(75)
			}
			initialized = true
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}}
		case "session/new":
			if !initialized || created {
				os.Exit(76)
			}
			created = true
			result = map[string]any{"sessionId": "independent-history-session", "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Independent history"}}}}
		case "session/prompt":
			var p struct {
				SessionID string
				Prompt    []struct{ Type, Text string }
			}
			if !created || prompted || json.Unmarshal(r.Params, &p) != nil || p.SessionID != "independent-history-session" {
				os.Exit(77)
			}
			prompted = true
			var parts []string
			for _, part := range p.Prompt {
				if part.Type != "text" {
					os.Exit(78)
				}
				parts = append(parts, part.Text)
			}
			text := strings.Join(parts, "\n")
			old, next := 0, 0
			answer := "ArchiveReady_91"
			if os.Args[1] == "1" {
				old, next, answer = 1, 1, "ArchiveResumed_97"
			}
			if strings.Count(text, "SeedQuestion_91") != 1 || strings.Count(text, "ArchiveReady_91") != old || strings.Count(text, "ContinuedQuestion_97") != next || strings.Contains(text, "NeverSubmitted_99") {
				os.Exit(79)
			}
			if os.Args[1] == "1" {
				_, after, _ := strings.Cut(text, "SeedQuestion_91 ")
				seed, _, ok := strings.Cut(after, " ")
				if !ok || len(seed) != 26 {
					os.Exit(79)
				}
				answer = seed + " " + answer
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "independent-history-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": answer}}}})
			result = map[string]string{"stopReason": "end_turn"}
		default:
			// Loading an old backend session or sending another command fails the observation.
			os.Exit(80)
		}
		write(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": result})
	}
	if scan.Err() != nil {
		os.Exit(81)
	}
}
