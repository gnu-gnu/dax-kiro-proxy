//go:build darwin

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

// Retain only bounded in-memory text to recognize the independently generated answer.
// A receipt requires the owning session, matching request ID and successful terminal reply.
type historyWitness struct {
	mu                  sync.Mutex
	stage               int
	seed, session, text string
	newID, promptID     json.RawMessage
	main                bool
}

func (g *historyWitness) inspect(from string, raw []byte, title bool) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var p struct {
		ID     json.RawMessage
		Method string
		Params struct {
			SessionID string
			Update    struct {
				SessionUpdate string
				Content       struct{ Type, Text string }
			}
		}
		Result json.RawMessage
		Error  json.RawMessage
	}
	if g.stage < 1 || g.stage > 2 || g.seed == "" || len(g.seed) > 64 || len(raw) > 256<<10 || (from != "client" && from != "agent") || json.Unmarshal(raw, &p) != nil {
		return "", errFrame
	}
	validID := len(p.ID) > 0 && !bytes.Equal(p.ID, []byte("null"))
	if from == "client" {
		switch p.Method {
		case "session/load":
			return "", errFrame
		case "session/new":
			if !validID || g.session != "" || len(g.newID) > 0 {
				return "", errFrame
			}
			g.newID = append([]byte(nil), p.ID...)
		case "session/prompt":
			if !validID || g.session == "" || p.Params.SessionID != g.session || len(g.promptID) > 0 {
				return "", errFrame
			}
			g.promptID = append([]byte(nil), p.ID...)
			g.main, g.text = !title, ""
			if !g.main {
				return "", nil
			}
			for marker, want := range map[string]int{"UIHistoryFirst_101": 1, g.seed: 1, "ArchiveUI_101": g.stage - 1, "UIHistoryNext_107": g.stage - 1, "UnsentHistory_109": 0} {
				if bytes.Count(raw, []byte(marker)) != want {
					return "", errFrame
				}
			}
			return "history-input", nil
		}
		return "", nil
	}
	if p.Method == "session/update" && g.main && len(g.promptID) > 0 && p.Params.SessionID == g.session && p.Params.Update.SessionUpdate == "agent_message_chunk" && p.Params.Update.Content.Type == "text" {
		if len(g.text)+len(p.Params.Update.Content.Text) > 8192 {
			return "", errFrame
		}
		g.text += p.Params.Update.Content.Text
	}
	if p.Method != "" || !validID {
		return "", nil
	}
	if len(g.newID) > 0 && bytes.Equal(g.newID, p.ID) {
		var result struct{ SessionID string }
		if len(p.Error) > 0 || json.Unmarshal(p.Result, &result) != nil || result.SessionID == "" || len(result.SessionID) > 1024 {
			return "", errFrame
		}
		g.session, g.newID = result.SessionID, nil
	}
	if len(g.promptID) == 0 || !bytes.Equal(g.promptID, p.ID) {
		return "", nil
	}
	g.promptID = nil
	if !g.main {
		return "", nil
	}
	var result struct{ StopReason string }
	marker := "ArchiveUI_101"
	if g.stage == 2 {
		marker = "ArchiveUI_107"
	}
	if len(p.Error) > 0 || json.Unmarshal(p.Result, &result) != nil || result.StopReason != "end_turn" || strings.Count(g.text, marker) != 1 || strings.Count(g.text, g.seed) != g.stage-1 {
		return "", errFrame
	}
	g.text = ""
	return "history-answer", nil
}

var observedHistory historyWitness

func noteHistory(from string, raw []byte) error {
	if cfg.HistoryStage == 0 {
		return nil
	}
	kind, err := observedHistory.inspect(from, raw, titleScope.Load())
	if err == nil && kind != "" {
		record(kind, nil)
	}
	return err
}
