//go:build darwin || linux

package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
)

func TestBatchRecoveryRejectsMissingOrChangedThirdCall(t *testing.T) {
	for _, fault := range []string{"none", "third-use", "third-result", "result-order", "old-question", "extra-prompt", "replayed-call", "nontext"} {
		t.Run(fault, func(t *testing.T) {
			state := batchHTTPState{nonce: "BATCH_REPLY_guard"}
			uses := make([]string, 3)
			latest := make([]any, 0, 4)
			observed := batchHTTPObservation{PromptCount: 1, Prompt: make([]batchHTTPPart, 10)}
			for i := range observed.Prompt {
				observed.Prompt[i].Type = "text"
			}
			for i := range 3 {
				state.uses = append(state.uses, anthropic.ResponseBlock{Type: "tool_use", ID: fmt.Sprintf("owned-call-%d", i), Name: "batch_action", Input: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i+1))})
				raw, _ := json.Marshal(state.uses[i])
				uses[i] = string(raw)
				result := map[string]any{"type": "tool_result", "tool_use_id": state.uses[i].ID, "content": fmt.Sprintf("owned-refusal-%d", i), "is_error": true}
				latest = append(latest, result)
				raw, _ = json.Marshal(result)
				observed.Prompt[4+2*i].Text = string(raw)
			}
			history, _ := json.Marshal(map[string]any{"system": []string{}, "history": []any{map[string]any{"role": "user", "content": []string{state.nonce}}, map[string]any{"role": "assistant", "content": uses}}})
			observed.Prompt[1].Text, observed.Prompt[9].Text = string(history), "BATCH_RECOVER_guard"
			latest = append(latest, map[string]string{"type": "text", "text": "BATCH_RECOVER_guard"})
			switch fault {
			case "third-use":
				state.uses[2].ID = "foreign-call"
			case "third-result":
				observed.Prompt[8].Text = `{"type":"tool_result","tool_use_id":"foreign-call","is_error":true,"content":"owned-refusal-2"}`
			case "result-order":
				observed.Prompt[4], observed.Prompt[8] = observed.Prompt[8], observed.Prompt[4]
			case "old-question":
				state.nonce = "different-question"
			case "extra-prompt":
				observed.Prompt = append(observed.Prompt, batchHTTPPart{Type: "text", Text: "extra"})
			case "replayed-call":
				observed.Calls = 1
			case "nontext":
				observed.Prompt[0].Type = "image"
			}
			if batchRecoveryContentMatches(state, latest, observed, "BATCH_RECOVER_guard") != (fault == "none") {
				t.Fatal("incomplete or changed multi-call recovery counted as exact history")
			}
		})
	}
}

func TestBatchSSERejectsIncompleteToolSequence(t *testing.T) {
	for _, fault := range []string{"none", "missing-input", "missing-third-stop", "foreign-index", "missing-terminal", "duplicate-terminal", "missing-start", "duplicate-start", "early-terminal", "early-delta", "post-terminal-content", "post-terminal-start"} {
		t.Run(fault, func(t *testing.T) {
			var wire strings.Builder
			packet := func(v any) { data, _ := json.Marshal(v); wire.WriteString("data: " + string(data) + "\n\n") }
			if fault != "missing-start" {
				packet(map[string]string{"type": "message_start"})
			}
			if fault == "duplicate-start" {
				packet(map[string]string{"type": "message_start"})
			}
			if fault == "early-terminal" {
				packet(map[string]string{"type": "message_stop"})
			}
			if fault == "early-delta" {
				packet(map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "tool_use"}})
			}
			for i := range 3 {
				packet(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": fmt.Sprintf("owned-%d", i), "name": "batch_action", "input": map[string]any{}}})
				if fault != "missing-input" || i != 2 {
					index := i
					if fault == "foreign-index" && i == 2 {
						index = 3
					}
					packet(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]string{"type": "input_json_delta", "partial_json": fmt.Sprintf(`{"n":%d}`, i+1)}})
				}
				if (fault != "missing-third-stop" && fault != "post-terminal-content") || i != 2 {
					packet(map[string]any{"type": "content_block_stop", "index": i})
				}
			}
			if fault != "early-delta" {
				packet(map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "tool_use"}})
			}
			if fault != "missing-terminal" && fault != "early-terminal" {
				packet(map[string]string{"type": "message_stop"})
			}
			if fault == "duplicate-terminal" {
				packet(map[string]string{"type": "message_stop"})
			}
			if fault == "post-terminal-content" {
				packet(map[string]any{"type": "content_block_stop", "index": 2})
			}
			if fault == "post-terminal-start" {
				packet(map[string]string{"type": "message_start"})
			}
			result, err := decodeBatchSSE([]byte(wire.String()))
			if (err == nil) != (fault == "none") {
				t.Fatal("incomplete SSE tool sequence counted as complete")
			}
			if err == nil && len(result.Content) != 3 {
				t.Fatal("complete SSE batch lost a call")
			}
		})
	}
}
