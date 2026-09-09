package interop_test

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/history"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/schemacheck"
)

const ownedPluginToolName = "mcp__plugin_dax-owned_owned__owned_probe"

// Observe only owned result equality and fixed request-comparison flags. The synthetic gateway
// requests exclusively client-advertised tools after independently checking their input schemas.
type pluginToolExchange struct {
	waitResult                              [32]byte
	stage                                   string
	toolCount, mcpCount                     int
	rawWait, rawToolSearch                  bool
	waitAdvertised, waitEmptyAccepted       bool
	mu                                      sync.Mutex
	validator                               *schemacheck.Pool
	first                                   *anthropic.Request
	waited, toolRequested, complete, failed bool
	comparison                              defaultClientComparison
	releaseWait                             func() error
	blockDigests                            map[string]int
	shapes                                  []string
	duplicateMessageIDs, coalesced          bool
}

func (e *pluginToolExchange) write(w http.ResponseWriter, stream bool, model string, blocks []map[string]any, stop string) {
	if e.duplicateMessageIDs {
		writeObservedMessageID(w, stream, model, "msg_owned_duplicate", blocks, stop)
		return
	}
	writeObservedMessage(w, stream, model, blocks, stop)
}

func (e *pluginToolExchange) respond(w http.ResponseWriter, httpRequest *http.Request, body []byte, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var envelope struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		e.toolCount = len(envelope.Tools)
		for _, tool := range envelope.Tools {
			e.rawWait = e.rawWait || tool.Name == "WaitForMcpServers"
			e.rawToolSearch = e.rawToolSearch || tool.Name == "ToolSearch"
			if strings.HasPrefix(tool.Name, "mcp__") {
				e.mcpCount++
			}
		}
	}
	e.stage = "decode"
	r, err := anthropic.DecodeRequest(body)
	if err != nil {
		e.failed = true
		w.WriteHeader(400)
		return
	}
	if requestfamily.Classify(r) == requestfamily.Title {
		e.write(w, r.Stream, model, []map[string]any{{"type": "text", "text": `{"title":"Independent plugin fixture"}`}}, "end_turn")
		return
	}
	r.Identity = anthropic.ClientIdentity{Session: httpRequest.Header.Get("x-claude-code-session-id"), Agent: httpRequest.Header.Get("x-claude-code-agent-id"), ParentAgent: httpRequest.Header.Get("x-claude-code-parent-agent-id")}
	if len(e.shapes) >= 3 {
		e.failed = true
		w.WriteHeader(429)
		return
	}
	if e.blockDigests == nil {
		e.blockDigests = make(map[string]int)
	}
	// Only block kinds and equality ordinals are retained; request text and digests stay
	// in memory. This records whether the client reorganizes earlier tool messages.
	var shape []string
	hasher := history.New([32]byte{1})
	for _, message := range r.Messages {
		var blocks []string
		for _, block := range message.Content {
			node, err := hasher.Message(anthropic.Message{Role: message.Role, Content: []anthropic.Block{block}})
			if err != nil {
				e.failed = true
				w.WriteHeader(400)
				return
			}
			ordinal, exists := e.blockDigests[node.Digest]
			if !exists {
				ordinal = len(e.blockDigests) + 1
				e.blockDigests[node.Digest] = ordinal
			}
			blocks = append(blocks, block.Type+"#"+strconv.Itoa(ordinal))
		}
		shape = append(shape, message.Role+"["+strings.Join(blocks, ",")+"]")
	}
	e.shapes = append(e.shapes, strings.Join(shape, " "))
	if e.first == nil {
		e.first = r
	} else {
		e.comparison = compareDefaultRequests(e.first, r)
	}
	e.stage = "results"
	results, err := r.LatestToolResults()
	if err != nil {
		e.failed = true
		w.WriteHeader(400)
		return
	}
	if e.toolRequested {
		e.stage = "plugin_result"
		pluginCount, waitCount := 0, 0
		valid := len(results) >= 1 && len(results) <= 2
		for _, result := range results {
			switch result.ID {
			case "owned_plugin_call":
				pluginCount++
				valid = valid && !result.IsError && len(result.Content) == 1 && result.Content[0].Type == "text" && result.Content[0].Text == "independent client asset result"
			case "owned_plugin_wait":
				waitCount++
				data, _ := json.Marshal(result)
				valid = valid && e.waited && sha256.Sum256(data) == e.waitResult
			default:
				valid = false
			}
		}
		if !valid || pluginCount != 1 || waitCount > 1 {
			e.failed = true
			w.WriteHeader(400)
			return
		}
		e.coalesced = waitCount == 1
		e.complete = true
		e.write(w, r.Stream, model, []map[string]any{{"type": "text", "text": "independent plugin observation complete"}}, "end_turn")
		return
	}
	wanted, id := ownedPluginToolName, "owned_plugin_call"
	find := func(name string) json.RawMessage {
		for _, raw := range r.Tools {
			var tool struct {
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"input_schema"`
			}
			if json.Unmarshal(raw, &tool) == nil && tool.Name == name {
				return tool.Schema
			}
		}
		return nil
	}
	schema := find(wanted)
	if schema == nil && !e.waited {
		wanted, id = "WaitForMcpServers", "owned_plugin_wait"
		schema = find(wanted)
		e.waitAdvertised = schema != nil
	}
	e.stage = "schema"
	valid := schema != nil && e.validator.Validate(httpRequest.Context(), schema, []byte(`{}`)) == nil
	if wanted == "WaitForMcpServers" {
		e.waitEmptyAccepted = valid
	}
	if !valid || (e.waited && (len(results) != 1 || results[0].ID != "owned_plugin_wait" || results[0].IsError)) {
		e.failed = true
		w.WriteHeader(400)
		return
	}
	if wanted == ownedPluginToolName {
		if e.releaseWait != nil && !e.waited {
			e.failed = true
			w.WriteHeader(400)
			return
		}
		if e.waited {
			// The client can repeat its completed wait beside the later plugin result. Keep
			// only an in-memory digest for exact equality; this observer adds no product policy.
			data, _ := json.Marshal(results[0])
			e.waitResult = sha256.Sum256(data)
		}
		e.toolRequested = true
	} else {
		if e.releaseWait != nil && e.releaseWait() != nil {
			e.failed = true
			w.WriteHeader(500)
			return
		}
		e.waited = true
	}
	e.write(w, r.Stream, model, []map[string]any{{"type": "tool_use", "id": id, "name": wanted, "input": json.RawMessage(`{}`)}}, "tool_use")
}

func (e *pluginToolExchange) verify(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	t.Logf("stage=%s, tools=%d, mcp_declarations=%d, raw_wait=%v, raw_tool_search=%v, wait_advertised=%v, wait_accepts_empty=%v, client_waited=%v, plugin_requested=%v, complete=%v, comparison=%+v", e.stage, e.toolCount, e.mcpCount, e.rawWait, e.rawToolSearch, e.waitAdvertised, e.waitEmptyAccepted, e.waited, e.toolRequested, e.complete, e.comparison)
	if e.failed || !e.toolRequested || !e.complete {
		t.Error("client plugin tool exchange did not complete")
	}
	for i, shape := range e.shapes {
		t.Logf("synthetic_main_request=%d, history_shape=%s", i+1, shape)
	}
	if e.releaseWait != nil && len(e.shapes) > 0 {
		t.Logf("duplicate_message_id=%v, coalesced_results=%v", e.duplicateMessageIDs, e.coalesced)
		if !e.waited || e.coalesced != e.duplicateMessageIDs {
			t.Error("message identity control did not match observed grouping")
		}
	}
	e.first = nil
}
