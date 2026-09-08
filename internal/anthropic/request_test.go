package anthropic

import (
	"encoding/json"
	"testing"
)

func TestRequestValidationAndOrderedContent(t *testing.T) {
	valid := []byte(`{"model":"claude-dax-fixture","max_tokens":32,"stream":true,"system":[{"type":"text","text":"first"},{"type":"text","text":"second"}],"messages":[{"role":"user","content":[{"type":"text","text":"third"},{"type":"text","text":"fourth"}]}],"future_field":{"version":2}}`)
	r, err := DecodeRequest(valid)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stream || r.System[0].Text != "first" || r.System[1].Text != "second" || r.Messages[0].Content[0].Text != "third" || r.Messages[0].Content[1].Text != "fourth" {
		t.Fatal("content order lost")
	}
	if !json.Valid(r.Extra["future_field"]) {
		t.Fatal("compatible extension discarded")
	}
	for _, body := range []string{
		`[]`, `null`, `{}`, `{"model":"x","messages":[]}`, `{"model":null,"messages":[{"role":"user","content":"x"}]}`,
		`{"model":"x","max_tokens":0,"messages":[{"role":"user","content":"x"}]}`,
		`{"model":"x","max_tokens":1,"stream":null,"messages":[{"role":"user","content":"x"}]}`,
		`{"model":"x","max_tokens":1,"messages":[{"role":"system","content":"x"}]}`,
		`{"model":"x","max_tokens":1,"messages":[{"role":"user","content":5}]}`,
		`{"model":"x","max_tokens":1,"messages":[{"role":"user","content":"x"}],"system":[{"type":"image","text":"x"}]}`,
		`{"model":"x","model":"y","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`,
	} {
		if _, err := DecodeRequest([]byte(body)); err == nil {
			t.Errorf("accepted invalid request: %s", body)
		}
	}
}

func TestMalformedEffortIsNonfatal(t *testing.T) {
	r, err := DecodeRequest([]byte(`{"model":"claude-dax-fixture","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"output_config":{"effort":{"bad":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Effort != "" || len(r.Warnings) == 0 {
		t.Fatal("malformed effort must be ignored with a safe warning")
	}
}
