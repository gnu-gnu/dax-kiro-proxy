// Package history reconciles bounded, keyed conversation digests without retaining text.
package history

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/ndjson"
)

const MaxNodes = 8192

type Mode string

const (
	Fresh     Mode = "created"
	Extend    Mode = "reused"
	Duplicate Mode = "duplicate"
	Diverged  Mode = "diverged"
)

type Node struct {
	Role   string `json:"role"`
	Digest string `json:"digest"`
}
type Pair struct {
	Assistant string `json:"assistant"`
	User      string `json:"user"`
}
type Snapshot struct {
	Nodes []Node `json:"nodes"`
	Pairs []Pair `json:"pairs"`
}
type Plan struct {
	Mode  Mode
	Start int
	nodes []Node
}
type Hasher struct{ key [32]byte }

func New(key [32]byte) *Hasher { return &Hasher{key: key} }
func (h *Hasher) Digest(domain string, value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil || len(b) > anthropic.MaxBodyBytes {
		return "", anthropic.ErrRequest
	}
	m := hmac.New(sha256.New, h.key[:])
	m.Write([]byte("dax-history-v1\x00"))
	m.Write([]byte(domain))
	m.Write([]byte{0})
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil)), nil
}
func (h *Hasher) Message(m anthropic.Message) (Node, error) {
	if m.Role != "user" && m.Role != "assistant" && m.Role != "system" {
		return Node{}, anthropic.ErrRequest
	}
	blocks := make([]json.RawMessage, 0, len(m.Content))
	for _, b := range m.Content {
		raw, err := canonicalBlock(b)
		if err != nil {
			return Node{}, err
		}
		blocks = append(blocks, raw)
	}
	digest, err := h.Digest("message", struct {
		Role    string
		Content []json.RawMessage
	}{m.Role, blocks})
	return Node{Role: m.Role, Digest: digest}, err
}
func canonicalBlock(b anthropic.Block) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if b.Type == "text" && (len(b.Raw) == 0 || b.Raw[0] == '"') {
		raw, _ := json.Marshal(map[string]string{"type": "text", "text": b.Text})
		return raw, nil
	}
	fields, err := ndjson.Object(b.Raw)
	if err != nil {
		return nil, anthropic.ErrRequest
	}
	// Hints on a content block do not change its meaning. Never recurse into tool arguments.
	delete(fields, "cache_control")
	if b.Type == "tool_result" {
		if _, ok := fields["is_error"]; !ok {
			fields["is_error"] = json.RawMessage("false")
		}
		raw := fields["content"]
		if len(raw) == 0 {
			fields["content"] = json.RawMessage("[]")
		} else if raw[0] == '"' {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return nil, anthropic.ErrRequest
			}
			fields["content"], _ = json.Marshal([]any{map[string]string{"type": "text", "text": text}})
		} else if raw[0] == '[' {
			var values []json.RawMessage
			if json.Unmarshal(raw, &values) != nil {
				return nil, anthropic.ErrRequest
			}
			for i, value := range values {
				obj, err := ndjson.Object(value)
				if err != nil {
					return nil, anthropic.ErrRequest
				}
				delete(obj, "cache_control")
				values[i], err = json.Marshal(obj)
				if err != nil {
					return nil, anthropic.ErrRequest
				}
			}
			fields["content"], _ = json.Marshal(values)
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, anthropic.ErrRequest
	}
	return jsoncanon.Object(raw)
}
func (h *Hasher) Nodes(messages []anthropic.Message) ([]Node, error) {
	if len(messages) == 0 || len(messages) > MaxNodes {
		return nil, anthropic.ErrRequest
	}
	result := make([]Node, 0, len(messages))
	for _, m := range messages {
		n, err := h.Message(m)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, nil
}
func (h *Hasher) Plan(s Snapshot, r *anthropic.Request) (Plan, error) {
	nodes, err := h.Nodes(r.Messages)
	if err != nil || r.LatestUserIndex() < 0 {
		return Plan{}, anthropic.ErrRequest
	}
	p := Plan{Mode: Fresh, nodes: nodes}
	if len(s.Nodes) == 0 {
		return p, nil
	}
	if !s.Valid() {
		return Plan{}, anthropic.ErrRequest
	}
	p.Mode = Diverged
	if len(nodes) == len(s.Nodes)-1 && slices.Equal(nodes, s.Nodes[:len(s.Nodes)-1]) {
		p.Mode = Duplicate
		return p, nil
	}
	start := -1
	if len(nodes) > len(s.Nodes) && slices.Equal(nodes[:len(s.Nodes)], s.Nodes) {
		start = len(s.Nodes)
	} else {
		// An assistant-only overlap (for example "OK") cannot establish continuity.
		for count := min(len(s.Nodes), len(nodes)-1); count >= 2; count-- {
			prefix := nodes[:count]
			if !slices.Equal(s.Nodes[len(s.Nodes)-count:], prefix) {
				continue
			}
			user, assistant := false, false
			for _, n := range prefix {
				user = user || n.Role == "user"
				assistant = assistant || n.Role == "assistant"
			}
			if user && assistant {
				start = count
				break
			}
		}
	}
	if start < 0 || r.LatestUserIndex() < start || len(s.Nodes)+len(nodes)-start >= MaxNodes {
		return p, nil
	}
	p.Mode = Extend
	p.Start = start
	p.nodes = append(slices.Clone(s.Nodes), nodes[start:]...)
	return p, nil
}
func (h *Hasher) Complete(p Plan, content []anthropic.Block) (Snapshot, error) {
	if len(p.nodes) == 0 || len(p.nodes) >= MaxNodes {
		return Snapshot{}, anthropic.ErrRequest
	}
	n, err := h.Message(anthropic.Message{Role: "assistant", Content: content})
	if err != nil {
		return Snapshot{}, err
	}
	nodes := append(slices.Clone(p.nodes), n)
	return Snapshot{Nodes: nodes, Pairs: pairs(nodes)}, nil
}

// Pending records delivered handoffs for exact continuation; it does not commit an idle snapshot.
func (h *Hasher) Pending(messages []anthropic.Message, content []anthropic.Block) (Snapshot, error) {
	nodes, err := h.Nodes(messages)
	if err != nil {
		return Snapshot{}, err
	}
	return h.Complete(Plan{nodes: nodes}, content)
}
func pairs(nodes []Node) []Pair {
	result := []Pair{}
	previous := ""
	for _, n := range nodes {
		if n.Role == "assistant" {
			previous = n.Digest
		}
		if n.Role == "user" {
			result = append(result, Pair{Assistant: previous, User: n.Digest})
			previous = ""
		}
	}
	return result
}
func (s Snapshot) Valid() bool {
	if len(s.Nodes) < 2 || len(s.Nodes) > MaxNodes || len(s.Pairs) == 0 || s.Nodes[len(s.Nodes)-1].Role != "assistant" {
		return false
	}
	for _, n := range s.Nodes {
		if n.Role != "user" && n.Role != "assistant" && n.Role != "system" || len(n.Digest) != 64 {
			return false
		}
		b, err := hex.DecodeString(n.Digest)
		if err != nil || len(b) != 32 || hex.EncodeToString(b) != n.Digest {
			return false
		}
	}
	return slices.Equal(s.Pairs, pairs(s.Nodes))
}
