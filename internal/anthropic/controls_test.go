package anthropic

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestUnsupportedControlsRejectWithoutEchoingValues(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"stop_sequences", `["private-stop-marker"]`},
		{"temperature", `0`}, {"top_p", `0.75`}, {"top_k", `8`},
		{"mcp_servers", `[{"type":"url","name":"private-server","url":"https://private.invalid","authorization_token":"private-token"}]`},
		{"container", `"private-container"`},
		{"container", `{"skills":[{"type":"custom","skill_id":"private-skill"}]}`},
		{"service_tier", `"standard_only"`}, {"service_tier", `"auto"`},
		{"inference_geo", `"private-region"`},
	} {
		t.Run(tc.field+"/"+string(tc.value[0]), func(t *testing.T) {
			body := `{"model":"claude-dax-fixture","max_tokens":16,"messages":[{"role":"user","content":"synthetic input"}],"` + tc.field + `":` + tc.value + `}`
			_, err := DecodeRequest([]byte(body))
			var unsupported *UnsupportedControlError
			if !errors.Is(err, ErrRequest) || !errors.As(err, &unsupported) {
				t.Fatal("unsupported constraint was not rejected before normalization")
			}
			if unsupported.Control() != tc.field || strings.Contains(err.Error(), "private-") {
				t.Fatal("constraint error lost its fixed name or exposed a supplied value")
			}
		})
	}
}

func TestAbsentAndEmptyControlsPreserveCompatibleRequests(t *testing.T) {
	r, err := DecodeRequest([]byte(`{"model":"claude-dax-fixture","max_tokens":16,"messages":[{"role":"user","content":"synthetic input"}],"stop_sequences":[],"mcp_servers":[],"container":null,"inference_geo":null,"metadata":{"user_id":"opaque"},"future_field":{"version":3},"output_config":{"effort":false}}`))
	if err != nil || r == nil || r.Effort != "" || len(r.Warnings) == 0 || !json.Valid(r.Extra["future_field"]) {
		t.Fatal("empty controls or compatible metadata lost existing semantics")
	}
	for _, tc := range []struct{ field, value string }{
		{"stop_sequences", `null`}, {"stop_sequences", `{}`},
		{"mcp_servers", `null`}, {"mcp_servers", `{}`},
		{"temperature", `null`}, {"top_p", `null`}, {"top_k", `null`},
		{"service_tier", `null`},
	} {
		request := &Request{Extra: map[string]json.RawMessage{tc.field: json.RawMessage(tc.value)}}
		if !errors.Is(request.ValidateControls(), ErrRequest) {
			t.Errorf("unimplemented non-default %s shape accepted", tc.field)
		}
	}
}

func TestControlRejectionPriorityIsStable(t *testing.T) {
	r := &Request{Extra: map[string]json.RawMessage{"temperature": json.RawMessage(`0`), "container": json.RawMessage(`"private-container"`), "stop_sequences": json.RawMessage(`["private-stop"]`)}}
	for range 20 {
		var unsupported *UnsupportedControlError
		if err := r.ValidateControls(); !errors.As(err, &unsupported) || unsupported.Control() != "container" {
			t.Fatal("execution controls must reject first with a stable safe diagnostic")
		}
	}
}
