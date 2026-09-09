//go:build darwin || linux

package session_test

import (
	"encoding/json"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
)

func pendingGuardFixture(t *testing.T) (pendingHistory, pendingObservation) {
	t.Helper()
	prefix := "owned synthetic context"
	state := pendingHistory{first: "PENDING_START_1", question: "PENDING_RECOVER_1", old: pendingObservation{observedPrompt: observedPrompt{PID: 321}, RelayPID: 322, RelayConfig: "/private/tmp/dax-r-old/relay.json"}, blocks: []anthropic.ResponseBlock{{Type: "text", Text: &prefix}, {Type: "tool_use", ID: "owned-call", Name: "client_action", Input: json.RawMessage(`{"n":1}`)}}}
	use, _ := json.Marshal(state.blocks[1])
	contextData, _ := json.Marshal(map[string]any{"system": []string{}, "history": []map[string]any{{"role": "user", "content": []string{state.first}}, {"role": "assistant", "content": []string{prefix, string(use)}}}})
	body, _ := json.Marshal(map[string]any{"pid": 421, "relayPID": 422, "relayConfig": "/private/tmp/dax-r-new/relay.json", "promptCount": 1, "session": "fresh", "prompt": []map[string]string{{"text": "context"}, {"text": string(contextData)}, {"text": "current"}, {"text": "result"}, {"text": `{"type":"tool_result","tool_use_id":"owned-call","is_error":true,"content":"owned synthetic denial"}`}, {"text": state.question}}})
	var next pendingObservation
	if json.Unmarshal(body, &next) != nil {
		t.Fatal("independent pending observation construction")
	}
	return state, next
}

func TestPendingRecoveryObserverRejectsForeignOrIncompleteHistory(t *testing.T) {
	for _, fault := range []string{"none", "old-process", "old-relay", "old-config", "wrong-question", "missing-history", "changed-prefix", "foreign-result", "successful-result", "changed-tool"} {
		t.Run(fault, func(t *testing.T) {
			state, next := pendingGuardFixture(t)
			switch fault {
			case "old-process":
				next.PID = state.old.PID
			case "old-relay":
				next.RelayPID = state.old.RelayPID
			case "old-config":
				next.RelayConfig = state.old.RelayConfig
			case "wrong-question":
				next.Prompt[5].Text = "PENDING_RECOVER_foreign"
			case "missing-history":
				next.Prompt = next.Prompt[1:]
			case "changed-prefix":
				*state.blocks[0].Text = "foreign old response"
			case "foreign-result":
				next.Prompt[4].Text = `{"type":"tool_result","tool_use_id":"foreign-call","is_error":true,"content":"owned synthetic denial"}`
			case "successful-result":
				next.Prompt[4].Text = `{"type":"tool_result","tool_use_id":"owned-call","is_error":false,"content":"owned synthetic denial"}`
			case "changed-tool":
				state.blocks[1].ID = "different-original-call"
			}
			if valid := checkPendingRecovery(state, next) == nil; valid != (fault == "none") {
				t.Fatal("pending recovery observer accepted mismatched history or ownership")
			}
		})
	}
}

func TestPendingOwnershipObservationRequiresBoundedDistinctProcesses(t *testing.T) {
	for _, fault := range []string{"none", "missing-pid", "shared-pid", "reused-prompt", "loaded", "relative-config", "wrong-config", "oversized-config"} {
		t.Run(fault, func(t *testing.T) {
			_, got := pendingGuardFixture(t)
			switch fault {
			case "missing-pid":
				got.PID = 0
			case "shared-pid":
				got.RelayPID = got.PID
			case "reused-prompt":
				got.Count = 2
			case "loaded":
				got.Loaded = true
			case "relative-config":
				got.RelayConfig = "dax-r-relative/relay.json"
			case "wrong-config":
				got.RelayConfig = "/private/tmp/dax-r-owned/other.json"
			case "oversized-config":
				got.RelayConfig = "/private/tmp/dax-r-" + string(make([]byte, 257)) + "/relay.json"
			}
			body, _ := json.Marshal(got)
			text := string(body)
			_, err := decodePendingObservation(anthropic.ResponseBlock{Type: "text", Text: &text})
			if (err == nil) != (fault == "none") {
				t.Fatal("invalid pending-process observation was accepted")
			}
		})
	}
}
