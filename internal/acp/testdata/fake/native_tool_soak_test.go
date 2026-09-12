package main

import (
	"encoding/json"
	"testing"
)

func TestNativeSoakResultCorrelationAndRefusal(t *testing.T) {
	for _, kind := range []string{"success", "denied", "wrong-id", "wrong-status", "wrong-text", "wrong-round", "leaked-denial", "unrelated-refusal", "duplicate-text", "rpc-error"} {
		t.Run(kind, func(t *testing.T) {
			lane := nativeSoakLane{Seed: "OWNED_RESULT"}
			denied := kind == "denied" || kind == "leaked-denial" || kind == "unrelated-refusal"
			id, isError, text := 101, denied, "OWNED_RESULT_VALUE_1_END"
			if denied {
				text = "independent native refusal"
			}
			switch kind {
			case "wrong-id":
				id = 102
			case "wrong-status":
				isError = true
			case "wrong-text":
				text = "unrelated"
			case "wrong-round":
				text = "OWNED_RESULT_VALUE_10_END"
			case "leaked-denial":
				text = "OWNED_RESULT_VALUE_1_END"
			case "unrelated-refusal":
				text = "file unavailable"
			case "duplicate-text":
				text += text
			}
			r := map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"isError": isError, "content": []any{map[string]string{"type": "text", "text": text}}}}
			if kind == "rpc-error" {
				r["error"] = map[string]any{"code": -32000, "message": "owned failure"}
			}
			raw, _ := json.Marshal(r)
			if validNativeSoakResult(raw, 101, lane, 1, denied) != (kind == "success" || kind == "denied") {
				t.Fatal("result correlation or refusal observer accepted an invalid outcome")
			}
		})
	}
}
