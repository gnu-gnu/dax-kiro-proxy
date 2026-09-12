package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Independently observes exact prompt part digests without echoing media into answer history.
// The owned witness records only process identity and the number of prompts actually received.
func mediaHistoryFixture() {
	if len(os.Args) != 3 || !filepath.IsAbs(os.Args[2]) {
		os.Exit(117)
	}
	timer := time.AfterFunc(time.Minute, func() { os.Exit(118) })
	defer timer.Stop()
	f, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		os.Exit(119)
	}
	defer f.Close()
	facts := struct{ PID, Prompts int }{PID: os.Getpid()}
	mark := func() {
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
			os.Exit(119)
		}
		if json.NewEncoder(f).Encode(facts) != nil {
			os.Exit(119)
		}
	}
	mark()
	const sessionID = "owned-media-session"
	initialized, ready := false, false
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for frames := 0; frames < 128 && scanner.Scan(); frames++ {
		var q request
		if json.Unmarshal(scanner.Bytes(), &q) != nil {
			os.Exit(120)
		}
		switch q.Method {
		case "initialize":
			if initialized {
				os.Exit(120)
			}
			initialized = true
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"promptCapabilities": map[string]bool{"image": true}}})
		case "session/new":
			if !initialized || ready {
				os.Exit(120)
			}
			ready = true
			reply(q.ID, map[string]any{"sessionId": sessionID, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Independent media control"}}}})
		case "session/prompt":
			var params struct {
				Session string `json:"sessionId"`
				Prompt  []struct {
					Type, Text, Data string
					MIME             string `json:"mimeType"`
				} `json:"prompt"`
			}
			if !ready || facts.Prompts >= 40 || json.Unmarshal(q.Params, &params) != nil || params.Session != sessionID || len(params.Prompt) == 0 || len(params.Prompt) > 512 {
				os.Exit(121)
			}
			type part struct {
				Type, MIME, Digest string
				Bytes              int
			}
			parts := make([]part, 0, len(params.Prompt))
			for _, p := range params.Prompt {
				data := p.Text
				if p.Type == "image" {
					data = p.Data
				} else if p.Type != "text" {
					os.Exit(121)
				}
				digest := sha256.Sum256([]byte(data))
				parts = append(parts, part{p.Type, p.MIME, hex.EncodeToString(digest[:]), len(data)})
			}
			facts.Prompts++
			mark()
			body, _ := json.Marshal(struct {
				PID, Prompts int
				Parts        []part
			}{facts.PID, facts.Prompts, parts})
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": string(body)}}}})
			reply(q.ID, map[string]string{"stopReason": "end_turn"})
		case "session/cancel":
		default:
			if len(q.ID) > 0 {
				write(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32601, "message": "unsupported fixture method"}})
			}
		}
	}
}
