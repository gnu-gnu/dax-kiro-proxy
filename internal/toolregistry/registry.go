// Package toolregistry owns immutable session-local tool declarations, aliases and input validation.
// It has no tool execution capability.
package toolregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/schemawire"
)

const MaxTools = 128
const MaxBytes = 1 << 20

var ErrRegistry = errors.New("invalid or unsupported client tool registry")
var ErrAlias = errors.New("tool alias is not in the session registry")
var ErrArguments = errors.New("invalid tool arguments")

type Validator interface {
	Check(context.Context, []byte) error
	Validate(context.Context, []byte, []byte) error
}
type Tool struct {
	Name        string          `json:"name"`
	Alias       string          `json:"alias"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
}
type Registry struct {
	tools       []Tool
	aliases     map[string]int
	fingerprint string
	validator   Validator
}

func Build(ctx context.Context, raw []json.RawMessage, native []string, validator Validator) (*Registry, error) {
	return build(ctx, raw, native, validator, func(name string) [32]byte { return sha256.Sum256([]byte("dax-tool-name-v1\x00" + name)) })
}
func build(ctx context.Context, raw []json.RawMessage, native []string, validator Validator, digest func(string) [32]byte) (*Registry, error) {
	// No native adapter is enabled before its capability/constraint mapping is implemented.
	if len(raw) > MaxTools || len(native) != 0 || validator == nil {
		return nil, ErrRegistry
	}
	total := 0
	for _, declaration := range raw {
		total += len(declaration)
		if total > MaxBytes {
			return nil, ErrRegistry
		}
	}
	r := &Registry{aliases: make(map[string]int), validator: validator, tools: make([]Tool, 0, len(raw))}
	names := make(map[string]bool)
	for _, declaration := range raw {
		fields, err := ndjson.Object(declaration)
		if err != nil {
			return nil, ErrRegistry
		}
		var tool Tool
		if !readString(fields["name"], &tool.Name) || !validName(tool.Name) || names[tool.Name] {
			return nil, ErrRegistry
		}
		names[tool.Name] = true
		if v, ok := fields["description"]; ok && (!readString(v, &tool.Description) || len(tool.Description) > 8192) {
			return nil, ErrRegistry
		}
		for key, value := range fields {
			switch key {
			case "name", "description", "input_schema", "cache_control", "input_examples":
			case "type":
				if string(value) != `"custom"` {
					return nil, ErrRegistry
				}
			case "strict":
				if string(value) != "true" && string(value) != "false" {
					return nil, ErrRegistry
				}
			case "defer_loading":
				if string(value) != "false" {
					return nil, ErrRegistry
				}
			case "allowed_callers":
				var callers []string
				if json.Unmarshal(value, &callers) != nil || len(callers) != 1 || callers[0] != "direct" {
					return nil, ErrRegistry
				}
			default:
				return nil, ErrRegistry
			}
		}
		if _, err := schemawire.Schema(fields["input_schema"]); err != nil {
			return nil, ErrRegistry
		}
		tool.Schema, err = jsoncanon.Object(fields["input_schema"])
		if err != nil {
			return nil, ErrRegistry
		}
		r.tools = append(r.tools, tool)
	}
	sort.Slice(r.tools, func(i, j int) bool { return r.tools[i].Name < r.tools[j].Name })
	hashes := make([]string, len(r.tools))
	lengths := make([]int, len(r.tools))
	for i, t := range r.tools {
		h := digest(t.Name)
		hashes[i] = hex.EncodeToString(h[:])
		lengths[i] = 16
	}
	for {
		groups := make(map[string][]int)
		for i, h := range hashes {
			key := h[:lengths[i]]
			groups[key] = append(groups[key], i)
		}
		changed := false
		for _, group := range groups {
			if len(group) < 2 {
				continue
			}
			for _, i := range group {
				if lengths[i] == 64 {
					return nil, ErrRegistry
				}
				lengths[i] += 2
			}
			changed = true
		}
		if !changed {
			break
		}
	}
	for i := range r.tools {
		r.tools[i].Alias = "relay_" + hashes[i][:lengths[i]]
		r.aliases[r.tools[i].Alias] = i
	}
	// Version 2 includes explicit original-name attribution in the relay's tool metadata.
	identity, err := json.Marshal(struct {
		Version int
		Tools   []Tool
		Native  []string
	}{2, r.tools, []string{}})
	if err != nil || len(identity) > MaxBytes {
		return nil, ErrRegistry
	}
	h := sha256.Sum256(identity)
	r.fingerprint = hex.EncodeToString(h[:])
	// Complete structural validation and collision resolution before any expensive work starts.
	for _, t := range r.tools {
		if err := validator.Check(ctx, t.Schema); err != nil {
			return nil, errors.Join(ErrRegistry, err)
		}
	}
	return r, nil
}
func (r *Registry) Fingerprint() string { return r.fingerprint }
func (r *Registry) Tools() []Tool {
	result := make([]Tool, len(r.tools))
	for i, t := range r.tools {
		result[i] = copyTool(t)
	}
	return result
}
func (r *Registry) Lookup(alias string) (Tool, bool) {
	i, ok := r.aliases[alias]
	if !ok {
		return Tool{}, false
	}
	return copyTool(r.tools[i]), true
}
func (r *Registry) Validate(ctx context.Context, alias string, arguments []byte) (Tool, error) {
	i, ok := r.aliases[alias]
	if !ok {
		return Tool{}, ErrAlias
	}
	if _, err := schemawire.Arguments(arguments); err != nil {
		return Tool{}, ErrArguments
	}
	if err := r.validator.Validate(ctx, r.tools[i].Schema, arguments); err != nil {
		return Tool{}, errors.Join(ErrArguments, err)
	}
	return copyTool(r.tools[i]), nil
}
func copyTool(t Tool) Tool { t.Schema = append(json.RawMessage(nil), t.Schema...); return t }
func readString(v []byte, dest *string) bool {
	return len(v) > 0 && v[0] == '"' && json.Unmarshal(v, dest) == nil
}
func validName(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
