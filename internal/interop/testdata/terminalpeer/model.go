package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

// Only the two independently declared test models are retained in memory. Receipts contain slots,
// never model/session IDs. The frame guard separately bounds bytes, prompt count and completion.
type modelWitness struct {
	mu                        sync.Mutex
	models                    [2]string
	session                   string
	current, pending          int
	marker, tail              string
	marked                    bool
	newID, selectID, promptID json.RawMessage
}

func (g *modelWitness) inspect(from string, raw []byte) (string, int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var packet struct {
		ID     json.RawMessage
		Method string
		Params struct {
			SessionID, ModelID string
			Update             struct {
				SessionUpdate string
				Content       struct{ Type, Text string }
			}
		}
		Result json.RawMessage
		Error  json.RawMessage
	}
	if len(raw) > 256<<10 || json.Unmarshal(raw, &packet) != nil || (from != "client" && from != "agent") {
		return "", 0, errFrame
	}
	validID := len(packet.ID) > 0 && !bytes.Equal(packet.ID, []byte("null"))
	if from == "client" {
		switch packet.Method {
		case "session/new":
			if !validID || g.session != "" || len(g.newID) != 0 {
				return "", 0, errFrame
			}
			g.newID = append([]byte(nil), packet.ID...)
		case "session/set_model", "session/prompt":
			if !validID || g.session == "" || packet.Params.SessionID != g.session || len(g.selectID) > 0 || len(g.promptID) > 0 {
				return "", 0, errFrame
			}
			if packet.Method == "session/prompt" {
				if g.current == 0 {
					return "", 0, errFrame
				}
				g.promptID = append([]byte(nil), packet.ID...)
				g.marker, g.tail, g.marked = "", "", false
				if bytes.Contains(raw, []byte("concatenation of ModelSecond and _67")) {
					g.marker = "ModelSecond_67"
				} else if bytes.Contains(raw, []byte("concatenation of ModelFirst and _61")) {
					g.marker = "ModelFirst_61"
				}
				return "prompt-model", g.current, nil
			}
			g.pending = g.slot(packet.Params.ModelID)
			if g.pending == 0 {
				return "", 0, errFrame
			}
			g.selectID = append([]byte(nil), packet.ID...)
		}
		return "", 0, nil
	}
	if packet.Method == "session/update" && len(g.promptID) > 0 && packet.Params.SessionID == g.session && packet.Params.Update.SessionUpdate == "agent_message_chunk" && packet.Params.Update.Content.Type == "text" && g.marker != "" && !g.marked {
		text := g.tail + packet.Params.Update.Content.Text
		if len(text) > 64 {
			g.tail = text[len(text)-64:]
		} else {
			g.tail = text
		}
		if strings.Contains(text, g.marker) {
			g.marked = true
			return "answer-marker", g.current, nil
		}
	}
	if packet.Method != "" || !validID {
		return "", 0, nil
	}
	if len(g.promptID) > 0 && bytes.Equal(packet.ID, g.promptID) {
		g.promptID = nil
	}
	newResult := len(g.newID) > 0 && bytes.Equal(packet.ID, g.newID)
	selectResult := len(g.selectID) > 0 && bytes.Equal(packet.ID, g.selectID)
	if !newResult && !selectResult {
		return "", 0, nil
	}
	var object map[string]json.RawMessage
	if len(packet.Error) != 0 || json.Unmarshal(packet.Result, &object) != nil || object == nil {
		return "", 0, errFrame
	}
	if newResult {
		var result struct {
			SessionID string
			Models    struct{ CurrentModelID string }
		}
		if json.Unmarshal(packet.Result, &result) != nil || result.SessionID == "" || len(result.SessionID) > 1024 || g.slot(result.Models.CurrentModelID) == 0 {
			return "", 0, errFrame
		}
		g.session, g.current, g.newID = result.SessionID, g.slot(result.Models.CurrentModelID), nil
		return "", 0, nil
	}
	g.current, g.pending, g.selectID = g.pending, 0, nil
	return "model-ack", g.current, nil
}

func (g *modelWitness) slot(id string) int {
	for i, model := range g.models {
		if model != "" && model == id {
			return i + 1
		}
	}
	return 0
}

var observedModel modelWitness

func noteModel(from string, raw []byte) error {
	if !cfg.ModelCheck {
		return nil
	}
	kind, slot, err := observedModel.inspect(from, raw)
	if err == nil && kind != "" {
		record(kind, map[string]any{"model_slot": slot})
	}
	return err
}
