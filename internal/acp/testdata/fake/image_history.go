package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"os"
	"strings"
	"time"
)

// Read only the authored expectation and public protocol frames, never the image or transcript.
func nativeImageHistory() {
	if len(os.Args) != 4 || (os.Args[2] != "0" && os.Args[2] != "1") {
		os.Exit(113)
	}
	timer := time.AfterFunc(25*time.Second, func() { os.Exit(114) })
	defer timer.Stop()
	raw, err := os.ReadFile(os.Args[3])
	var spec struct{ Path, Pixels, Encoded string }
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &spec) != nil || spec.Path == "" || len(spec.Pixels) != 64 {
		os.Exit(115)
	}
	facts := struct {
		PID, Prompts, Calls, Images int
		Valid                       bool
	}{PID: os.Getpid()}
	mark := func() {
		data, _ := json.Marshal(facts)
		if os.WriteFile(os.Args[3]+".facts", data, 0600) != nil {
			os.Exit(116)
		}
	}
	mark()
	checkImage := func(mime, data string) bool {
		if mime != "image/png" || len(data) > 64<<10 {
			return false
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(data)
		if err != nil {
			return false
		}
		config, err := png.DecodeConfig(bytes.NewReader(decoded))
		if err != nil || config.Width != 12 || config.Height != 9 {
			return false
		}
		img, err := png.Decode(bytes.NewReader(decoded))
		if err != nil {
			return false
		}
		h := sha256.New()
		for y := 0; y < 9; y++ {
			for x := 0; x < 12; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				h.Write([]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8), byte(a >> 8)})
			}
		}
		digest := sha256.Sum256(decoded)
		return hex.EncodeToString(h.Sum(nil)) == spec.Pixels && (spec.Encoded == "" || hex.EncodeToString(digest[:]) == spec.Encoded)
	}
	var relay *fixtureRelay
	initialized, created := false, false
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		var q request
		if json.Unmarshal(scan.Bytes(), &q) != nil || len(q.ID) == 0 {
			os.Exit(117)
		}
		switch q.Method {
		case "initialize":
			var p struct {
				ProtocolVersion    int
				ClientCapabilities map[string]any
			}
			if initialized || json.Unmarshal(q.Params, &p) != nil || p.ProtocolVersion != 1 || p.ClientCapabilities == nil || len(p.ClientCapabilities) != 0 {
				os.Exit(118)
			}
			initialized = true
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"promptCapabilities": map[string]bool{"image": true}}})
		case "session/new":
			var p struct {
				CWD        string
				MCPServers []json.RawMessage
			}
			if !initialized || created || json.Unmarshal(q.Params, &p) != nil || len(p.MCPServers) != 1 {
				os.Exit(119)
			}
			created = true
			relay = startFixtureRelay(p.MCPServers[0], p.CWD)
			defer relay.close()
			reply(q.ID, map[string]any{"sessionId": "independent-image-history", "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Independent image history"}}}})
		case "session/prompt":
			var p struct {
				SessionID string
				Prompt    []struct{ Type, Text, MIMEType, Data string }
			}
			if !created || facts.Prompts != 0 || json.Unmarshal(q.Params, &p) != nil || p.SessionID != "independent-image-history" {
				os.Exit(120)
			}
			facts.Prompts++
			var text strings.Builder
			var call, owner string
			count, index, envelopes, ends := 0, 0, 0, 0
			expectImage := false
			for _, part := range p.Prompt {
				if expectImage && part.Type != "image" {
					os.Exit(121)
				}
				if part.Type == "text" {
					text.WriteString(part.Text)
					text.WriteByte('\n')
					if os.Args[2] == "1" {
						var entry struct {
							Type, ID, Name string
							Owner          string `json:"tool_use_id"`
							IsError        bool   `json:"is_error"`
							Count          int    `json:"content_blocks"`
							Index          int    `json:"content_index"`
							Input          struct {
								Path string `json:"file_path"`
							}
							Content struct{ Type, Source string }
						}
						if json.Unmarshal([]byte(part.Text), &entry) == nil {
							switch entry.Type {
							case "tool_use":
								if call != "" || entry.ID == "" || entry.Name != "Read" || entry.Input.Path != spec.Path {
									os.Exit(121)
								}
								call = entry.ID
							case "tool_result":
								if call == "" || owner != "" || entry.Owner != call || entry.IsError || entry.Count < 1 || entry.Count > 32 {
									os.Exit(121)
								}
								owner = entry.Owner
								count = entry.Count
								envelopes++
							case "tool_result_content":
								if owner == "" || entry.Owner != owner || entry.Index != index {
									os.Exit(121)
								}
								index++
								if entry.Content.Type == "image" {
									if entry.Content.Source != "following_acp_image" {
										os.Exit(121)
									}
									expectImage = true
								}
							case "tool_result_end":
								if owner == "" || entry.Owner != owner || index != count {
									os.Exit(121)
								}
								owner = ""
								ends++
							}
						}
					}
				} else if part.Type == "image" && checkImage(part.MIMEType, part.Data) {
					if os.Args[2] == "1" && (!expectImage || owner == "") {
						os.Exit(121)
					}
					expectImage = false
					facts.Images++
				} else {
					os.Exit(121)
				}
			}
			mark()
			answer := "ImageReadDone_359"
			if os.Args[2] == "0" {
				if facts.Images != 0 || strings.Count(text.String(), "ImageQuestion_353") != 1 {
					os.Exit(122)
				}
				facts.Calls++
				mark()
				relay.send(3, "tools/call", map[string]any{"name": relay.alias, "arguments": map[string]string{"file_path": spec.Path}})
				var result struct {
					IsError bool
					Content []struct{ Type, Text, Data, MIMEType string }
				}
				if json.Unmarshal(relay.read(), &result) != nil || relay.responseError || result.IsError {
					os.Exit(123)
				}
				for _, part := range result.Content {
					if part.Type == "image" && checkImage(part.MIMEType, part.Data) {
						facts.Images++
					} else if part.Type != "text" {
						os.Exit(124)
					}
				}
			} else {
				if strings.Count(text.String(), "ImageQuestion_353") != 1 || strings.Count(text.String(), "ImageReadDone_359") != 1 || strings.Count(text.String(), "ImageFollow_367") != 1 {
					os.Exit(125)
				}
				answer = "ImageResumeDone_373"
			}
			facts.Valid = facts.Images == 1
			if os.Args[2] == "1" {
				facts.Valid = facts.Valid && call != "" && owner == "" && !expectImage && envelopes == 1 && ends == 1
			}
			mark()
			if !facts.Valid {
				os.Exit(126)
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": p.SessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": answer}}}})
			reply(q.ID, map[string]string{"stopReason": "end_turn"})
		default:
			os.Exit(127)
		}
	}
	if scan.Err() != nil {
		os.Exit(128)
	}
}
