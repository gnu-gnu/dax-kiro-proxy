package websearch

import (
	"encoding/json"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
)

func notification(raw string) acp.Notification {
	return acp.Notification{Method: "session/update", SessionID: "owned-search", Params: json.RawMessage(`{"sessionId":"owned-search","update":` + raw + `}`)}
}

func TestSearchCollectorConvertsNativeOutputAndFreezesBothCarriers(t *testing.T) {
	initial := notification(`{"sessionUpdate":"tool_call","toolCallId":"native-only","title":"Searching","kind":"search","status":"in_progress","rawInput":{"query":"independent query"}}`)
	completed := `{"sessionUpdate":"tool_call_update","toolCallId":"native-only","status":"completed","content":[{"type":"content","content":{"type":"text","text":"Display only"}}],"rawOutput":{"items":[{"owned_transport":{"query":"independent query","error":null,"results":[{"url":"https://example.org/native","title":"Independent structured source","snippet":"Synthetic excerpt"}]}}]}}`
	for _, display := range []bool{false, true} {
		raw := completed
		if !display {
			raw = strings.Replace(raw, `"content":[{"type":"content","content":{"type":"text","text":"Display only"}}],`, "", 1)
		}
		c := newCollector("owned-search", 1)
		for _, event := range []acp.Notification{initial, notification(raw), notification(raw)} {
			if c.observe(event) != nil {
				t.Fatal("valid native output rejected")
			}
		}
		exchanges, _, err := c.finish()
		if err != nil || len(exchanges) != 1 || len(exchanges[0].Results) != 1 || exchanges[0].Results[0].URL != "https://example.org/native" {
			t.Fatal("native output was lost or duplicated")
		}
		if c.observe(notification(strings.Replace(raw, "example.org/native", "example.org/changed", 1))) == nil {
			t.Fatal("completed native result changed")
		}
	}
	for _, invalid := range []string{
		strings.Replace(completed, "https://example.org/native", "file:///private/owned", 1),
		strings.Replace(completed, "https://example.org/native", "https://secret@example.org/native", 1),
		strings.Replace(completed, "independent query", "changed query", 1),
	} {
		c := newCollector("owned-search", 1)
		if c.observe(initial) != nil {
			t.Fatal("initial fixture")
		}
		if c.observe(notification(invalid)) == nil {
			if _, _, err := c.finish(); err == nil {
				t.Fatal("unsafe or mismatched native output admitted")
			}
		}
	}
}

// This standalone failure uses synthetic content; only its shape comes from native observation.
const standaloneFailure = `{"sessionUpdate":"tool_call_update","toolCallId":"blocked-search","title":"web_search","kind":"search","status":"failed","rawInput":{"query":"independent blocked query"},"content":[{"type":"content","content":{"type":"text","text":"Synthetic refusal"}}]}`

func TestSearchCollectorAcceptsSelfContainedNativeFailure(t *testing.T) {
	c := newCollector("owned-search", 1)
	for range 2 {
		if err := c.observe(notification(standaloneFailure)); err != nil {
			t.Fatal("self-contained native failure rejected")
		}
	}
	x, text, err := c.finish()
	if err != nil || len(x) != 1 || x[0].Query != "independent blocked query" || x[0].ErrorCode != "unavailable" || len(x[0].Results) != 0 || text != "" {
		t.Fatal("native failure did not become one bounded error exchange")
	}
	for _, mutated := range []string{
		strings.Replace(standaloneFailure, "blocked query", "changed query", 1),
		strings.Replace(standaloneFailure, "Synthetic refusal", "Changed refusal", 1),
		strings.Replace(standaloneFailure, `"failed"`, `"completed"`, 1),
		strings.Replace(standaloneFailure, "blocked-search", "another-search", 1),
	} {
		if c.observe(notification(mutated)) == nil {
			t.Fatal("finalized standalone failure changed or exceeded call bound")
		}
	}
}

func TestSearchCollectorRejectsIncompleteStandaloneFailure(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(standaloneFailure, `"failed"`, `"completed"`, 1),
		strings.Replace(standaloneFailure, `"failed"`, `"pending"`, 1),
		strings.Replace(standaloneFailure, `"kind":"search",`, "", 1),
		strings.Replace(standaloneFailure, `"title":"web_search",`, "", 1),
		strings.Replace(standaloneFailure, `"title":"web_search"`, `"title":"other"`, 1),
		strings.Replace(standaloneFailure, "independent blocked query", "", 1),
		strings.Replace(standaloneFailure, `"query":`, `"other":`, 1),
		strings.Replace(standaloneFailure, `"content":[`, `"rawOutput":{},"content":[`, 1),
		strings.Replace(standaloneFailure, `"type":"text"`, `"type":"image"`, 1),
		strings.Replace(standaloneFailure, `"text":"Synthetic refusal"`, `"text":false`, 1),
		strings.Replace(standaloneFailure, `"type":"content"`, `"type":"diff"`, 1),
	} {
		c := newCollector("owned-search", 1)
		if c.observe(notification(raw)) == nil {
			t.Fatal("unsupported standalone failure admitted")
		}
	}
	for _, envelope := range []bool{false, true} {
		n := notification(standaloneFailure)
		if envelope {
			n.SessionID = "foreign-search"
		} else {
			n.Params = json.RawMessage(strings.Replace(string(n.Params), "owned-search", "foreign-search", 1))
		}
		if newCollector("owned-search", 1).observe(n) == nil {
			t.Fatal("foreign standalone failure admitted")
		}
	}
}
func TestSearchCollectorCorrelatesAndDeduplicatesPublicACP(t *testing.T) {
	c := newCollector("owned-search", 2)
	initial := notification(`{"sessionUpdate":"tool_call","toolCallId":"native-a","title":"web_search","kind":"search","status":"in_progress","rawInput":{"query":"synthetic search"}}`)
	result := notification(`{"sessionUpdate":"tool_call_update","toolCallId":"native-a","status":"completed","content":[{"type":"content","content":{"type":"resource_link","uri":"https://example.org/public-acp","name":"Independent result"}}]}`)
	for _, n := range []acp.Notification{initial, initial, result, result} {
		if err := c.observe(n); err != nil {
			t.Fatal("valid correlated event rejected")
		}
	}
	exchanges, text, err := c.finish()
	if err != nil || len(exchanges) != 1 || len(exchanges[0].Results) != 1 || exchanges[0].Results[0].URL != "https://example.org/public-acp" || text != "" {
		t.Fatal("search conversion lost correlation or duplicated result")
	}
}
func TestSearchCollectorRejectsDivergenceAndIncompleteResults(t *testing.T) {
	for _, mode := range []string{"orphan", "foreign", "wrong-tool", "changed-tool", "changed-query", "regression", "changed-result", "unfinished", "no-search", "too-many"} {
		c := newCollector("owned-search", 1)
		initial := notification(`{"sessionUpdate":"tool_call","toolCallId":"native-a","title":"web_search","kind":"search","status":"in_progress","rawInput":{"query":"synthetic"}}`)
		if mode != "orphan" && mode != "no-search" {
			if c.observe(initial) != nil {
				t.Fatal("fixture rejected")
			}
		}
		var n acp.Notification
		switch mode {
		case "orphan":
			n = notification(`{"sessionUpdate":"tool_call_update","toolCallId":"unknown","status":"completed","content":[]}`)
		case "foreign":
			n = initial
			n.SessionID = "foreign"
		case "wrong-tool":
			n = notification(`{"sessionUpdate":"tool_call","toolCallId":"native-b","title":"execute_bash","kind":"execute","rawInput":{"command":"false"}}`)
		case "changed-query":
			n = notification(`{"sessionUpdate":"tool_call_update","toolCallId":"native-a","rawInput":{"query":"changed"}}`)
		case "regression":
			n = notification(`{"sessionUpdate":"tool_call_update","toolCallId":"native-a","status":"pending"}`)
		case "changed-tool":
			n = notification(`{"sessionUpdate":"tool_call_update","toolCallId":"native-a","title":"execute_bash","kind":"execute"}`)
		case "changed-result":
			if c.observe(notification(`{"sessionUpdate":"tool_call_update","toolCallId":"native-a","status":"completed","content":[]}`)) != nil {
				t.Fatal("fixture rejected")
			}
			n = notification(`{"sessionUpdate":"tool_call_update","toolCallId":"native-a","status":"failed"}`)
		case "too-many":
			n = notification(`{"sessionUpdate":"tool_call","toolCallId":"native-b","title":"web_search","kind":"search","rawInput":{"query":"second"}}`)
		case "unfinished", "no-search":
			if _, _, err := c.finish(); err == nil {
				t.Fatalf("invalid final state=%s", mode)
			}
			continue
		}
		if c.observe(n) == nil {
			t.Fatalf("invalid event accepted=%s", mode)
		}
	}
}

func TestSearchProgressRequiresValidatedOwnedActivity(t *testing.T) {
	for _, n := range []acp.Notification{
		{Method: "_kiro.dev/metadata", Params: json.RawMessage(`{}`)},
		notification(`{"sessionUpdate":"unknown_activity"}`),
		notification(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"image","text":"synthetic"}}`),
		notification(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":""}}`),
	} {
		c := newCollector("owned-search", 1)
		if c.observe(n) != nil || c.progress {
			t.Fatal("unvalidated notification established readiness")
		}
	}
	c := newCollector("owned-search", 1)
	if c.observe(notification(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"synthetic progress"}}`)) != nil || !c.progress || c.text.Len() != 0 {
		t.Fatal("valid thought must be silent progress")
	}
	if c.observe(acp.Notification{Method: "_kiro.dev/metadata", Params: json.RawMessage(`{}`)}) != nil || c.progress {
		t.Fatal("metadata reused previous progress")
	}
}

func TestSearchCollectorAcceptsPendingInputAndDisplayUpdates(t *testing.T) {
	c := newCollector("owned-search", 1)
	for _, raw := range []string{
		`{"sessionUpdate":"tool_call","toolCallId":"pending-search","title":"Searching the public web","status":"pending"}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"pending-search","rawInput":{"query":"part"},"content":[{"type":"content","content":{"type":"text","text":"Preparing search"}}]}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"pending-search","title":"Running search","status":"in_progress","rawInput":{"query":"complete query"}}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"pending-search","status":"completed","content":[]}`,
	} {
		if err := c.observe(notification(raw)); err != nil {
			t.Fatal("valid pending input or display update rejected")
		}
	}
	x, _, err := c.finish()
	if err != nil || len(x) != 1 || x[0].Query != "complete query" {
		t.Fatal("final input not preserved")
	}
}
