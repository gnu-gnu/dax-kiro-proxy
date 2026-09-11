package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The client owns all effects and native transcript writes. This independent ACP peer reads only
// an owned operation expectation, the public MCP launch declaration and incoming protocol frames.
func nativeToolHistory() {
	if len(os.Args) != 5 || (os.Args[2] != "0" && os.Args[2] != "1") {
		os.Exit(101)
	}
	timer := time.AfterFunc(60*time.Second, func() { os.Exit(102) })
	defer timer.Stop()
	expectationFile, err := os.Open(os.Args[3])
	if err != nil {
		os.Exit(103)
	}
	expectationData, readErr := io.ReadAll(io.LimitReader(expectationFile, (64<<10)+1))
	expectationCloseErr := expectationFile.Close()
	var expectation struct{ Interrupted, Preface, FollowupEffect bool }
	if readErr != nil || expectationCloseErr != nil || len(expectationData) > 64<<10 || json.Unmarshal(expectationData, &expectation) != nil {
		os.Exit(103)
	}
	// observedForm names which measured interrupted-history representation the projected prompt
	// carried, so the HTTP-side witness can require agreement. It is a fixed label, never content.
	observedForm := ""
	mark := func(stage int) {
		record := fmt.Sprintf("%d %d", os.Getpid(), stage)
		if observedForm != "" {
			record += " " + observedForm
		}
		if os.WriteFile(filepath.Join(filepath.Dir(os.Args[3]), "peer-stage-"+os.Args[2]), []byte(record), 0600) != nil {
			os.Exit(112)
		}
	}
	mark(0)
	f, err := os.Open(os.Args[4])
	if err != nil {
		os.Exit(103)
	}
	declaration, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || len(declaration) > 64<<10 {
		os.Exit(103)
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	initialized, created, prompted := false, false, false
	var relay *fixtureRelay
	for scan.Scan() {
		var q request
		if json.Unmarshal(scan.Bytes(), &q) != nil || len(q.ID) == 0 {
			os.Exit(104)
		}
		switch q.Method {
		case "initialize":
			mark(1)
			var p struct {
				ProtocolVersion    int
				ClientCapabilities map[string]any
			}
			if initialized || json.Unmarshal(q.Params, &p) != nil || p.ProtocolVersion != 1 || p.ClientCapabilities == nil || len(p.ClientCapabilities) != 0 {
				os.Exit(105)
			}
			initialized = true
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}})
		case "session/new":
			mark(2)
			var p struct {
				CWD        string
				MCPServers []json.RawMessage
			}
			if !initialized || created || json.Unmarshal(q.Params, &p) != nil || p.CWD == "" || p.MCPServers == nil || len(p.MCPServers) != 0 {
				os.Exit(106)
			}
			created = true
			relay = startFixtureRelay(declaration, p.CWD)
			mark(3)
			defer relay.close()
			reply(q.ID, map[string]any{"sessionId": "independent-tool-history", "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Independent tool history"}}}})
		case "session/prompt":
			mark(4)
			var p struct {
				SessionID string
				Prompt    []struct{ Type, Text string }
			}
			if !created || prompted || json.Unmarshal(q.Params, &p) != nil || p.SessionID != "independent-tool-history" {
				os.Exit(107)
			}
			prompted = true
			var parts []string
			for _, part := range p.Prompt {
				if part.Type != "text" {
					os.Exit(108)
				}
				parts = append(parts, part.Text)
			}
			text := strings.Join(parts, "\n")
			if strings.Count(text, "EffectQuestion_131") != 1 || strings.Contains(text, "UnsentEffect_139") {
				os.Exit(109)
			}
			answer := "ToolArchiveReady_131"
			if os.Args[2] == "0" {
				if strings.Contains(text, "EffectFollow_137") || strings.Contains(text, answer) {
					os.Exit(109)
				}
				if expectation.Preface {
					write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": p.SessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "OwnedPendingPrefix_149"}}}})
				}
				relay.effect(os.Args[3])
			} else {
				oldAnswers := 1
				if expectation.Interrupted {
					oldAnswers = 0
				}
				toolContext := strings.Contains(text, "tool_use") && strings.Contains(text, "tool_result")
				if expectation.Interrupted {
					// Two measured native representations: the 2.1.263 build omits the unfinished pair
					// ("abandoned"); the 2.1.267 build keeps it with a fixed error result and a fixed
					// continuation line before the placeholder ("retained"). Anything else is unmeasured.
					placeholder, prefix := strings.Count(text, "No response requested."), strings.Count(text, "OwnedPendingPrefix_149")
					// Historical blocks are embedded as escaped JSON strings inside the projected context.
					uses, results := strings.Count(text, `\"tool_use\"`), strings.Count(text, `\"tool_result\"`)
					errorFlags := strings.Count(text, `\"is_error\"`)
					interrupted, continuation := strings.Count(text, "[Request interrupted by user for tool use]"), strings.Count(text, "Continue from where you left off.")
					switch {
					case uses == 0 && results == 0 && interrupted == 0 && continuation == 0 && (placeholder == 1 && prefix == 0 || expectation.Preface && prefix == 1 && placeholder == 0):
						observedForm, toolContext = "abandoned", true
					case uses == 1 && results == 1 && errorFlags == 1 && interrupted == 1 && continuation == 1 && placeholder == 1 && prefix == 0:
						observedForm, toolContext = "retained", true
					default:
						observedForm, toolContext = fmt.Sprintf("unmeasured tu=%d tr=%d ef=%d ph=%d px=%d ir=%d ct=%d", uses, results, errorFlags, placeholder, prefix, interrupted, continuation), false
					}
				}
				if strings.Count(text, "EffectFollow_137") != 1 || strings.Count(text, answer) != oldAnswers || !toolContext {
					mark(9)
					os.Exit(109)
				}
				mark(4)
				answer = "ToolArchiveResumed_137"
				if expectation.FollowupEffect {
					relay.effect(os.Args[3])
				}
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": p.SessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": answer}}}})
			reply(q.ID, map[string]string{"stopReason": "end_turn"})
		default:
			// A load or second prompt cannot earn a passing observation.
			os.Exit(110)
		}
	}
	if scan.Err() != nil {
		os.Exit(111)
	}
}
