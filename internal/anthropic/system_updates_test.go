package anthropic

import "testing"

func TestClientSystemUpdatesKeepTheLatestUserBoundary(t *testing.T) {
	r, err := DecodeRequest([]byte(`{"model":"claude-dax-fixture","max_tokens":128,"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"reply"},{"role":"system","content":[{"type":"text","text":"synthetic context update"}]},{"role":"user","content":"second"},{"role":"system","content":[{"type":"text","text":"synthetic runtime facts"}]}]}`))
	if err != nil {
		t.Fatal("documented client system update rejected")
	}
	if len(r.Messages) != 5 || r.LatestUserIndex() != 3 || !r.ClientContent() {
		t.Fatal("system update changed user ownership or order")
	}
	for _, raw := range []string{
		`{"model":"claude-dax-fixture","max_tokens":128,"messages":[{"role":"system","content":"only system"}]}`,
		`{"model":"claude-dax-fixture","max_tokens":128,"messages":[{"role":"user","content":"user"},{"role":"assistant","content":"prefill"},{"role":"system","content":"later"}]}`,
		`{"model":"claude-dax-fixture","max_tokens":128,"messages":[{"role":"user","content":"user"},{"role":"system","content":[{"type":"tool_result","tool_use_id":"foreign","content":"bad"}]}]}`,
	} {
		if _, err := DecodeRequest([]byte(raw)); err == nil {
			t.Fatal("system messages bypassed role/content validation")
		}
	}
}

func TestUnsupportedSystemLifetimeAndEffortAreNotSilentlyLost(t *testing.T) {
	for _, field := range []string{`"clear_at":"next_user_message"`, `"clear_at":true`, `"output_config":{"effort":"low"}`} {
		body := `{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":"first"},{"role":"system","content":"temporary rule",` + field + `}]}`
		if _, err := DecodeRequest([]byte(body)); err == nil {
			t.Fatal("unsupported system instruction semantics were silently dropped")
		}
	}
	if _, err := DecodeRequest([]byte(`{"model":"claude-dax-fixture","max_tokens":32,"messages":[{"role":"user","content":"first"},{"role":"system","content":"standing rule","clear_at":"never"}]}`)); err != nil {
		t.Fatal("standing instruction lifetime rejected")
	}
}
