package acp

import "testing"

func TestCapabilityTypesAndExactNames(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"loadSession":null}`, `{"loadSession":"true"}`, `{"promptCapabilities":null}`, `{"promptCapabilities":{"image":1}}`, `{"mcpCapabilities":{"http":null}}`} {
		if _, err := decodeCapabilities([]byte(raw)); err == nil {
			t.Fatalf("invalid capability shape accepted: %s", raw)
		}
	}
	caps, err := decodeCapabilities([]byte(`{"loadSession":false,"LoadSession":true,"promptCapabilities":{"image":false,"Image":true,"embeddedContext":true},"mcpCapabilities":{"http":true,"SSE":true},"future":{"supported":true}}`))
	if err != nil || caps.LoadSession || caps.Prompt.Image || !caps.Prompt.EmbeddedContext || !caps.MCP.HTTP || caps.MCP.SSE {
		t.Fatal("unnegotiated capability enabled or supported capability lost")
	}
}
