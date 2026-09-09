package main

import "testing"

func TestModelAnswerMarkerBelongsToActiveSessionText(t *testing.T) {
	g := modelWitness{models: [2]string{"fixture-backend", "fixture-target"}, session: "owned-stream", current: 1}
	for _, tc := range []struct{ from, raw, kind string }{
		{"client", `{"id":1,"method":"session/prompt","params":{"sessionId":"owned-stream","prompt":[{"type":"text","text":"concatenation of ModelFirst and _61"}]}}`, "prompt-model"},
		{"agent", `{"method":"session/update","params":{"sessionId":"other","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ModelFirst_61"}}}}`, ""},
		{"agent", `{"method":"session/update","params":{"sessionId":"owned-stream","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ModelFirst"}}}}`, ""},
		{"agent", `{"method":"session/update","params":{"sessionId":"owned-stream","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"_61"}}}}`, "answer-marker"},
		{"agent", `{"method":"session/update","params":{"sessionId":"owned-stream","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ModelFirst_61"}}}}`, ""},
		{"agent", `{"id":1,"result":{"stopReason":"end_turn"}}`, ""},
		{"agent", `{"method":"session/update","params":{"sessionId":"owned-stream","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ModelFirst_61"}}}}`, ""},
	} {
		kind, _, err := g.inspect(tc.from, []byte(tc.raw))
		if err != nil || kind != tc.kind {
			t.Fatal("marker attributed outside its active session response")
		}
	}
}

func TestModelObservationRequiresSuccessfulCorrelatedSelection(t *testing.T) {
	g := modelWitness{models: [2]string{"fixture-backend", "fixture-target"}}
	for _, tc := range []struct {
		from, raw, kind string
		slot            int
	}{
		{"client", `{"id":1,"method":"session/new"}`, "", 0},
		{"agent", `{"id":1,"result":{"sessionId":"owned-stream","models":{"currentModelId":"fixture-backend"}}}`, "", 0},
		{"client", `{"id":2,"method":"session/prompt","params":{"sessionId":"owned-stream"}}`, "prompt-model", 1},
		{"agent", `{"id":2,"result":{"stopReason":"end_turn"}}`, "", 0},
		{"client", `{"id":3,"method":"session/set_model","params":{"sessionId":"owned-stream","modelId":"fixture-target"}}`, "", 0},
		{"agent", `{"id":99,"result":{}}`, "", 0},
		{"agent", `{"id":3,"result":{}}`, "model-ack", 2},
		{"client", `{"id":4,"method":"session/prompt","params":{"sessionId":"owned-stream"}}`, "prompt-model", 2},
	} {
		kind, slot, err := g.inspect(tc.from, []byte(tc.raw))
		if err != nil || kind != tc.kind || slot != tc.slot {
			t.Fatal("model observation did not follow correlated idle selection")
		}
	}
}

func TestModelObservationRejectsMissingAcknowledgementAndActiveSwitch(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"active", `{"id":2,"method":"session/prompt","params":{"sessionId":"owned-stream"}}`, `{"id":3,"method":"session/set_model","params":{"sessionId":"owned-stream","modelId":"fixture-target"}}`},
		{"unacknowledged", `{"id":2,"method":"session/set_model","params":{"sessionId":"owned-stream","modelId":"fixture-target"}}`, `{"id":3,"method":"session/prompt","params":{"sessionId":"owned-stream"}}`},
		{"unknown", "", `{"id":3,"method":"session/set_model","params":{"sessionId":"owned-stream","modelId":"fixture-absent"}}`},
		{"wrong-session", "", `{"id":3,"method":"session/set_model","params":{"sessionId":"foreign-session","modelId":"fixture-target"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := modelWitness{models: [2]string{"fixture-backend", "fixture-target"}, session: "owned-stream", current: 1}
			if tc.first != "" {
				if _, _, err := g.inspect("client", []byte(tc.first)); err != nil {
					t.Fatal("control")
				}
			}
			if _, _, err := g.inspect("client", []byte(tc.second)); err == nil {
				t.Fatal("unproven selection accepted")
			}
		})
	}
	for _, result := range []string{`"error":{"code":-32000}`, `"result":null`, `"result":[]`} {
		g := modelWitness{models: [2]string{"fixture-backend", "fixture-target"}, session: "owned-stream", current: 1}
		_, _, _ = g.inspect("client", []byte(`{"id":3,"method":"session/set_model","params":{"sessionId":"owned-stream","modelId":"fixture-target"}}`))
		if _, _, err := g.inspect("agent", []byte(`{"id":3,`+result+`}`)); err == nil || g.current != 1 {
			t.Fatal("failed selection changed observed model")
		}
	}
}
