package requestfamily_test

import (
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/requestfamily"
	"encoding/json"
	"testing"
)

func TestTitleRequiresAllSignals(t *testing.T) {
	base := func() *anthropic.Request {
		return &anthropic.Request{System: []anthropic.Block{{Type: "text", Text: "Create a short title for this conversation."}}, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "independent example"}}}}, Extra: map[string]json.RawMessage{"thinking": json.RawMessage(`{"type":"disabled"}`), "output_config": json.RawMessage(`{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}}}`)}}
	}
	if requestfamily.Classify(base()) != requestfamily.Title {
		t.Fatal("combined title signals ignored")
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) { r.System[0].Text = "Discuss how to implement a title widget." },
		func(r *anthropic.Request) { delete(r.Extra, "thinking") },
		func(r *anthropic.Request) { delete(r.Extra, "output_config") },
		func(r *anthropic.Request) {
			r.Tools = []json.RawMessage{json.RawMessage(`{"name":"action","input_schema":{"type":"object"}}`)}
		},
	} {
		r := base()
		mutate(r)
		if requestfamily.Classify(r) != requestfamily.Main {
			t.Fatal("loose title signal captured main work")
		}
	}
}
