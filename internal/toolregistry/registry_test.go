package toolregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// This stand-in accepts only the synthetic integer fixture. Draft semantics are tested through
// the real, separately supervised schema worker in internal/schemacheck.
type fixtureValidator struct{}

func (fixtureValidator) Check(_ context.Context, schema []byte) error {
	var obj map[string]any
	if json.Unmarshal(schema, &obj) != nil || obj["type"] != "object" {
		return errors.New("fixture schema")
	}
	return nil
}
func (fixtureValidator) Validate(_ context.Context, _, args []byte) error {
	if string(args) != `{"n":1}` {
		return errors.New("fixture arguments")
	}
	return nil
}
func declaration(name, description string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"name": name, "description": description, "input_schema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer"}}}})
	return b
}
func TestRegistryIdentityAndOwnership(t *testing.T) {
	ctx := context.Background()
	input := []json.RawMessage{declaration("alpha", "one"), declaration("beta", "two")}
	r, err := Build(ctx, input, nil, fixtureValidator{})
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(struct {
		Version int
		Tools   []Tool
		Native  []string
	}{1, r.Tools(), []string{}})
	oldDigest := sha256.Sum256(legacy)
	if r.Fingerprint() == hex.EncodeToString(oldDigest[:]) {
		t.Fatal("explicit client-name metadata reused the previous registry policy identity")
	}
	reordered, err := Build(ctx, []json.RawMessage{input[1], input[0]}, nil, fixtureValidator{})
	if err != nil || r.Fingerprint() != reordered.Fingerprint() {
		t.Fatal("registry order changes identity")
	}
	tool := r.Tools()[0]
	if tool.Name == tool.Alias || !strings.HasPrefix(tool.Alias, "relay_") {
		t.Fatal("original tool name exposed as alias")
	}
	if found, err := r.Validate(ctx, tool.Alias, []byte(`{"n":1}`)); err != nil || found.Name != tool.Name {
		t.Fatal("session-local alias reverse lookup failed")
	}
	for _, alias := range []string{tool.Name, "relay_unknown"} {
		if _, err := r.Validate(ctx, alias, []byte(`{"n":1}`)); err == nil {
			t.Fatal("unknown alias accepted")
		}
	}
	if _, err := r.Validate(ctx, tool.Alias, []byte(`{"n":"bad"}`)); err == nil {
		t.Fatal("invalid arguments accepted")
	}
	tool.Schema[0] = '!'
	if _, err := r.Validate(ctx, r.Tools()[0].Alias, []byte(`{"n":1}`)); err != nil {
		t.Fatal("returned data mutated retained registry")
	}
	for _, changed := range [][]json.RawMessage{{declaration("alpha", "changed"), input[1]}, {declaration("gamma", "one"), input[1]}} {
		other, err := Build(ctx, changed, nil, fixtureValidator{})
		if err != nil || other.Fingerprint() == r.Fingerprint() {
			t.Fatal("compatibility omitted tool metadata")
		}
	}
}
func TestToolAliasPrefixesExtendDeterministically(t *testing.T) {
	digest := func(name string) [32]byte {
		d := sha256.Sum256([]byte(name))
		for i := 0; i < 8; i++ {
			d[i] = 0
		}
		return d
	}
	input := []json.RawMessage{declaration("alpha", ""), declaration("beta", "")}
	r, err := build(context.Background(), input, nil, fixtureValidator{}, digest)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := build(context.Background(), []json.RawMessage{input[1], input[0]}, nil, fixtureValidator{}, digest)
	if err != nil || reverse.Fingerprint() != r.Fingerprint() {
		t.Fatal("collision resolution depends on order")
	}
	tools := r.Tools()
	if len(tools[0].Alias) <= len("relay_")+16 || tools[0].Alias == tools[1].Alias {
		t.Fatal("digest prefix collision unresolved")
	}
	if _, err := build(context.Background(), input, nil, fixtureValidator{}, func(string) [32]byte { return [32]byte{} }); err == nil {
		t.Fatal("full hash collision accepted")
	}
}
func TestUnsafeToolDeclarationsRejectedBeforeValidation(t *testing.T) {
	good := declaration("alpha", "")
	for _, declarations := range [][]json.RawMessage{
		{good, good}, {declaration("", "")}, {declaration("has space", "")}, {declaration(strings.Repeat("a", 65), "")},
		{declaration("alpha", strings.Repeat("x", 8193))}, {json.RawMessage(`{"name":"a","type":"bash_20250124","input_schema":{"type":"object"}}`)},
		{json.RawMessage(`{"name":"a","input_schema":true}`)}, {json.RawMessage(`{"name":"a","input_schema":{"type":"object"},"allowed_callers":["code_execution_20250825"]}`)},
		{json.RawMessage(`{"name":"a","input_schema":{"type":"object"},"defer_loading":true}`)},
	} {
		if _, err := Build(context.Background(), declarations, nil, fixtureValidator{}); err == nil {
			t.Fatal("unsafe declaration accepted")
		}
	}
	if _, err := Build(context.Background(), []json.RawMessage{good}, []string{"unapproved-native"}, fixtureValidator{}); err == nil {
		t.Fatal("native tools enabled without adapter")
	}
}
