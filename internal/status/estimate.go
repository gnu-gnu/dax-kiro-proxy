package status

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"hash"
	"sync"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/ndjson"
)

// This deterministic local heuristic counts selected UTF-8 bytes, not any provider's tokenizer.
// No request text, schemas or tool results are retained in the cache or the returned estimate.
type Estimate struct {
	Approximate            bool   `json:"approximate"`
	Method                 string `json:"method"`
	InputContextTokens     int64  `json:"input_context_tokens"`
	VisibleOutputTokens    int64  `json:"visible_output_tokens"`
	LogicalPrefixTokens    int64  `json:"logical_prefix_tokens"`
	MediaExcluded          bool   `json:"media_excluded"`
	HiddenThinkingExcluded bool   `json:"hidden_thinking_excluded"`
}

func (e Estimate) valid() bool {
	return e.Approximate && e.Method == "utf8-bytes/4-v1" && e.MediaExcluded && e.HiddenThinkingExcluded &&
		e.InputContextTokens >= 0 && e.InputContextTokens <= 8<<20 && e.VisibleOutputTokens >= 0 && e.VisibleOutputTokens <= 1<<38 && e.LogicalPrefixTokens >= 0 && e.LogicalPrefixTokens <= e.InputContextTokens
}

type estimatePart struct {
	key   [32]byte
	bytes int64
}
type InputEstimate struct {
	parts []estimatePart
	bytes int64
}
type EstimatorStats struct {
	Entries      int
	Computations uint64
}
type Estimator struct {
	mu           sync.Mutex
	key          [32]byte
	cache        map[[32]byte]estimatePart
	order        [128][32]byte
	next         int
	computations uint64
}

func NewEstimator(key [32]byte) *Estimator {
	return &Estimator{key: key, cache: make(map[[32]byte]estimatePart)}
}
func (e *Estimator) Stats() EstimatorStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EstimatorStats{len(e.cache), e.computations}
}
func (e *Estimator) Measure(r *anthropic.Request) (InputEstimate, bool) {
	if r == nil || len(r.Messages) == 0 || len(r.Messages) > 4096 || len(r.Tools) > 128 {
		return InputEstimate{}, false
	}
	budget, blocks := int64(0), 0
	check := func(content []anthropic.Block) bool {
		blocks += len(content)
		for _, b := range content {
			budget += int64(len(b.Type)) + int64(len(b.Text)) + int64(len(b.Raw))
		}
		return blocks <= 65536 && budget <= 32<<20
	}
	if !check(r.System) {
		return InputEstimate{}, false
	}
	for _, m := range r.Messages {
		if !check(m.Content) {
			return InputEstimate{}, false
		}
	}
	for _, raw := range r.Tools {
		budget += int64(len(raw))
	}
	if budget > 32<<20 {
		return InputEstimate{}, false
	}
	result := InputEstimate{parts: make([]estimatePart, 0, len(r.Messages)+2)}
	add := func(kind string, content []anthropic.Block, tools []json.RawMessage) bool {
		part, ok := e.part(kind, content, tools)
		if !ok {
			return false
		}
		result.parts = append(result.parts, part)
		result.bytes += part.bytes
		return result.bytes <= 32<<20
	}
	if !add("system", r.System, nil) || !add("tools", nil, r.Tools) {
		return InputEstimate{}, false
	}
	for _, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" && m.Role != "system" || !add("message:"+m.Role, m.Content, nil) {
			return InputEstimate{}, false
		}
	}
	return result, true
}
func hashPiece(h hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	h.Write(size[:])
	h.Write(value)
}
func (e *Estimator) part(kind string, content []anthropic.Block, tools []json.RawMessage) (estimatePart, bool) {
	h := hmac.New(sha256.New, e.key[:])
	h.Write([]byte("dax-estimate-v1\x00"))
	hashPiece(h, []byte(kind))
	for _, b := range content {
		hashPiece(h, []byte(b.Type))
		hashPiece(h, []byte(b.Text))
		hashPiece(h, b.Raw)
	}
	for _, tool := range tools {
		hashPiece(h, tool)
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	e.mu.Lock()
	defer e.mu.Unlock()
	if cached, ok := e.cache[key]; ok {
		return cached, true
	}
	part := estimatePart{key: key}
	for _, b := range content {
		n, ok := blockBytes(b)
		if !ok {
			return estimatePart{}, false
		}
		part.bytes += n
	}
	for _, raw := range tools {
		fields, err := ndjson.Object(raw)
		if err != nil {
			return estimatePart{}, false
		}
		for _, name := range []string{"name", "description"} {
			if raw, present := fields[name]; present {
				var text string
				if json.Unmarshal(raw, &text) != nil {
					return estimatePart{}, false
				}
				part.bytes += int64(len(text))
			}
		}
		if schema, present := fields["input_schema"]; present {
			canonical, err := jsoncanon.Object(schema)
			if err != nil {
				return estimatePart{}, false
			}
			part.bytes += int64(len(canonical))
		}
	}
	if len(e.cache) == len(e.order) {
		delete(e.cache, e.order[e.next])
	}
	e.cache[key] = part
	e.order[e.next] = key
	e.next = (e.next + 1) % len(e.order)
	e.computations++
	return part, true
}
func blockBytes(b anthropic.Block) (int64, bool) {
	switch b.Type {
	case "text":
		return int64(len(b.Text)), true
	case "image", "document", "thinking", "redacted_thinking":
		return 0, true
	case "tool_use":
		fields, err := ndjson.Object(b.Raw)
		if err != nil {
			return 0, false
		}
		var name string
		if json.Unmarshal(fields["name"], &name) != nil {
			return 0, false
		}
		canonical, err := jsoncanon.Object(fields["input"])
		return int64(len(name) + len(canonical)), err == nil
	case "tool_result":
		fields, err := ndjson.Object(b.Raw)
		if err != nil {
			return 0, false
		}
		content := fields["content"]
		if len(content) == 0 {
			return 0, true
		}
		if content[0] == '"' {
			var text string
			if json.Unmarshal(content, &text) != nil {
				return 0, false
			}
			return int64(len(text)), true
		}
		var parts []json.RawMessage
		if content[0] != '[' || json.Unmarshal(content, &parts) != nil || len(parts) > 4096 {
			return 0, false
		}
		n := int64(0)
		for _, raw := range parts {
			item, err := ndjson.Object(raw)
			var typ, text string
			if err != nil || json.Unmarshal(item["type"], &typ) != nil {
				return 0, false
			}
			if typ == "text" {
				if json.Unmarshal(item["text"], &text) != nil {
					return 0, false
				}
				n += int64(len(text))
			}
		}
		return n, true
	default:
		return 0, false
	}
}
func (i InputEstimate) Tokens() int64 { return (i.bytes + 3) / 4 }
func (i InputEstimate) Summary(previous InputEstimate, output VisibleOutput) (Estimate, bool) {
	if len(i.parts) == 0 || output.invalid {
		return Estimate{}, false
	}
	prefix := int64(0)
	for n := range min(len(i.parts), len(previous.parts)) {
		if i.parts[n].key != previous.parts[n].key {
			break
		}
		prefix += i.parts[n].bytes
	}
	return Estimate{Approximate: true, Method: "utf8-bytes/4-v1", InputContextTokens: i.Tokens(), VisibleOutputTokens: (output.bytes + 3) / 4, LogicalPrefixTokens: (prefix + 3) / 4, MediaExcluded: true, HiddenThinkingExcluded: true}, true
}

type VisibleOutput struct {
	bytes   int64
	invalid bool
}

func (v *VisibleOutput) add(n int64) {
	if n < 0 || n > 1<<40-v.bytes {
		v.invalid = true
		return
	}
	v.bytes += n
}
func (v *VisibleOutput) AddText(text string) { v.add(int64(len(text))) }
func (v *VisibleOutput) AddTools(tools []anthropic.ToolUse) {
	for _, use := range tools {
		canonical, err := jsoncanon.Object(use.Input)
		if err != nil {
			v.invalid = true
			return
		}
		v.add(int64(len(canonical)))
	}
}
