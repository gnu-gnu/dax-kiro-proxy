package kirofeature

import (
	"strings"
	"testing"

	"dax-kiro-proxy/internal/ndjson"
)

func TestStandaloneSearchFailureBoundsAndPublicContent(t *testing.T) {
	const fixture = `{"sessionUpdate":"tool_call_update","title":"web_search","kind":"search","status":"failed","rawInput":{"query":"independent query"},"content":[{"type":"content","content":{"type":"text","text":"Independent refusal"}}]}`
	fields, err := ndjson.Object([]byte(fixture))
	if err != nil || !StandaloneSearchFailure(fields) {
		t.Fatal("complete standalone failure rejected")
	}
	for _, raw := range []string{
		strings.Replace(fixture, "independent query", strings.Repeat("q", 4097), 1),
		strings.Replace(fixture, "independent query", "   ", 1),
		strings.Replace(fixture, "Independent refusal", strings.Repeat("x", 64<<10), 1),
		strings.Replace(fixture, "Independent refusal", "", 1),
		strings.Replace(fixture, `"content":[{"type":"content","content":{"type":"text","text":"Independent refusal"}}]`, `"content":null`, 1),
		strings.Replace(fixture, `"content":[{"type":"content","content":{"type":"text","text":"Independent refusal"}}]`, `"content":[]`, 1),
		strings.Replace(fixture, `"content":[`, `"rawOutput":null,"content":[`, 1),
		strings.Replace(fixture, `"content":[{"type":"content","content":{"type":"text","text":"Independent refusal"}}]`, `"content":[`+strings.Repeat(`{"type":"content","content":{"type":"text","text":"Synthetic"}},`, 8)+`{"type":"content","content":{"type":"text","text":"Synthetic"}}]`, 1),
	} {
		fields, err := ndjson.Object([]byte(raw))
		if err != nil || StandaloneSearchFailure(fields) {
			t.Fatal("invalid or excessive standalone failure admitted")
		}
	}
}
