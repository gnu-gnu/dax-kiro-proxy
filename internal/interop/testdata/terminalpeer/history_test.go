//go:build darwin

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func historyPacket(method, session, body string) []byte {
	raw, _ := json.Marshal(map[string]any{"id": 3, "method": method, "params": map[string]any{"sessionId": session, "prompt": []any{map[string]string{"type": "text", "text": body}}}})
	return raw
}

func TestHistoryObservationRejectsMissingDuplicatedAndForeignContext(t *testing.T) {
	const seed = "owned-seed"
	valid := "UIHistoryFirst_101 " + seed + " ArchiveUI_101 UIHistoryNext_107"
	for _, input := range []string{strings.ReplaceAll(valid, "ArchiveUI_101", ""), valid + " UIHistoryFirst_101", valid + " " + seed, valid + " UnsentHistory_109"} {
		g := historyWitness{stage: 2, seed: seed, session: "owned"}
		if _, err := g.inspect("client", historyPacket("session/prompt", "owned", input), false); err == nil {
			t.Fatal("unproven native history accepted")
		}
	}
	g := historyWitness{stage: 2, seed: seed, session: "owned"}
	if _, err := g.inspect("client", historyPacket("session/prompt", "foreign", valid), false); err == nil {
		t.Fatal("foreign history session accepted")
	}
	if _, err := g.inspect("client", historyPacket("session/load", "owned", valid), false); err == nil {
		t.Fatal("backend load admitted")
	}
}

func TestHistoryAnswerRequiresCorrelatedSuccessfulEnd(t *testing.T) {
	for _, result := range []string{`"result":{"stopReason":"end_turn"}`, `"result":{"stopReason":"cancelled"}`, `"error":{"code":401}`} {
		g := historyWitness{stage: 2, seed: "owned-seed", session: "owned"}
		kind, err := g.inspect("client", historyPacket("session/prompt", "owned", "UIHistoryFirst_101 owned-seed ArchiveUI_101 UIHistoryNext_107"), false)
		if err != nil || kind != "history-input" {
			t.Fatal("valid history rejected")
		}
		for _, part := range []struct{ session, text string }{{"foreign", "owned-seed ArchiveUI_107"}, {"owned", "owned-seed Archive"}, {"owned", "UI_107"}} {
			raw, _ := json.Marshal(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": part.session, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": part.text}}}})
			if k, e := g.inspect("agent", raw, false); e != nil || k != "" {
				t.Fatal("premature history answer receipt")
			}
		}
		if k, e := g.inspect("agent", []byte(`{"id":99,"result":{"stopReason":"end_turn"}}`), false); e != nil || k != "" {
			t.Fatal("foreign response completed history")
		}
		kind, err = g.inspect("agent", []byte(`{"id":3,`+result+`}`), false)
		if strings.Contains(result, "end_turn") {
			if err != nil || kind != "history-answer" {
				t.Fatal("successful history answer absent")
			}
		} else if err == nil {
			t.Fatal("failed response counted as history answer")
		}
	}
}
