package schemacheck_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/toolregistry"
)

// These independently authored examples distinguish drafts by validation outcome. Merely removing
// $schema would reject legacy tuples and apply reference siblings to Draft 7 incorrectly.
func TestDeclaredDraftSemanticsAndRegistryIdentity(t *testing.T) {
	p := pool(t, nil)
	var fingerprints []string
	var alias string
	for _, tc := range []struct {
		name, dialect      string
		legacy, refSibling bool
	}{
		{"draft-7", "http://json-schema.org/draft-07/schema#", true, false},
		{"draft-2019", "https://json-schema.org/draft/2019-09/schema", true, true},
		{"draft-2020", "https://json-schema.org/draft/2020-12/schema", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDialect := func(body string) []byte {
				return []byte(`{"$schema":"` + tc.dialect + `",` + body[1:])
			}
			check := func(schema, valid, invalid []byte) {
				t.Helper()
				if p.Check(t.Context(), schema) != nil {
					t.Fatal("declared schema rejected")
				}
				if p.Validate(t.Context(), schema, valid) != nil {
					t.Fatal("declared dialect rejected valid arguments")
				}
				if !errors.Is(p.Validate(t.Context(), schema, invalid), schemacheck.ErrArguments) {
					t.Fatal("declared dialect accepted invalid arguments")
				}
			}
			schema := withDialect(`{"type":"object","definitions":{"number":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/definitions/number","minimum":10}},"required":["n"]}`)
			valid := []byte(`{"n":11}`)
			check(schema, valid, []byte(`{"n":-1}`))
			err := p.Validate(t.Context(), schema, []byte(`{"n":5}`))
			if tc.refSibling && !errors.Is(err, schemacheck.ErrArguments) || !tc.refSibling && err != nil {
				t.Fatal("reference sibling semantics were reinterpreted")
			}
			declaration, _ := json.Marshal(map[string]any{"name": "mcp__owned__schema_probe", "input_schema": json.RawMessage(schema)})
			registry, err := toolregistry.Build(t.Context(), []json.RawMessage{declaration}, nil, p)
			if err != nil {
				t.Fatal("declared dialect rejected during tool registration")
			}
			tool := registry.Tools()[0]
			var original, retained any
			_ = json.Unmarshal(schema, &original)
			_ = json.Unmarshal(tool.Schema, &retained)
			a, _ := json.Marshal(original)
			b, _ := json.Marshal(retained)
			if string(a) != string(b) {
				t.Fatal("registry changed the schema declaration")
			}
			if alias != "" && alias != tool.Alias {
				t.Fatal("dialect change altered opaque name alias")
			}
			alias = tool.Alias
			for _, old := range fingerprints {
				if registry.Fingerprint() == old {
					t.Fatal("registry identity omitted the declared dialect")
				}
			}
			fingerprints = append(fingerprints, registry.Fingerprint())
			if _, err := registry.Validate(t.Context(), alias, valid); err != nil {
				t.Fatal("registry did not validate the declared dialect")
			}
			tuple := `{"type":"object","properties":{"tuple":{"type":"array","prefixItems":[{"type":"integer"}],"items":false}},"required":["tuple"]}`
			if tc.legacy {
				tuple = `{"type":"object","properties":{"tuple":{"type":"array","items":[{"type":"integer"}],"additionalItems":false}},"required":["tuple"]}`
			}
			check(withDialect(tuple), []byte(`{"tuple":[7]}`), []byte(`{"tuple":[7,8]}`))
			check(withDialect(`{"type":"object","properties":{"n":{"const":9007199254740993}},"required":["n"]}`), []byte(`{"n":9007199254740993}`), []byte(`{"n":9007199254740992}`))
			for _, target := range []string{"https://example.invalid/owned-schema", "file:///private/tmp/owned-schema"} {
				if !errors.Is(p.Check(t.Context(), withDialect(`{"type":"object","properties":{"n":{"$ref":"`+target+`"}}}`)), schemacheck.ErrSchema) {
					t.Fatal("declared draft enabled schema retrieval")
				}
			}
		})
	}
	if len(fingerprints) != 3 {
		t.Fatal("did not retain three distinct dialect identities")
	}
	if p.Stats().Started != 1 {
		t.Fatal("dialect checks did not reuse the bounded compiler process")
	}
}

func TestDeclaredDialectAliasesAndRejections(t *testing.T) {
	p := pool(t, nil)
	for _, path := range []string{"draft-07", "draft/2019-09", "draft/2020-12"} {
		for _, scheme := range []string{"http", "https"} {
			for _, fragment := range []string{"", "#"} {
				uri := scheme + "://json-schema.org/" + path + "/schema" + fragment
				if p.Check(t.Context(), []byte(`{"type":"object","$schema":"`+uri+`"}`)) != nil {
					t.Errorf("known dialect alias rejected: path=%s scheme=%s empty_fragment=%v", path, scheme, fragment != "")
				}
			}
		}
	}
	for i, declaration := range []string{
		`null`, `17`, `{}`, `[]`, `true`, `""`, `"json-schema.org/draft-07/schema"`,
		`"https://json-schema.org/schema"`, `"https://json-schema.org/draft-04/schema"`,
		`"http://json-schema.org/draft-06/schema#"`, `"https://json-schema.org/draft-07/schema#wrong"`,
		`"https://json-schema.org/draft-07/schema##"`, `"https://json-schema.org/draft-07/schema?draft=7"`,
		`"https://untrusted.invalid/private-sentinel"`,
	} {
		err := p.Check(t.Context(), []byte(`{"type":"object","$schema":`+declaration+`}`))
		if !errors.Is(err, schemacheck.ErrSchema) {
			t.Errorf("unsupported declaration admitted: case=%d", i)
		} else if strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("schema error exposed input")
		}
	}
	// Omitted declarations retain 2020 semantics; legacy tuple syntax must not silently select 7.
	if !errors.Is(p.Check(t.Context(), []byte(`{"type":"object","properties":{"x":{"items":[{"type":"integer"}]}}}`)), schemacheck.ErrSchema) {
		t.Fatal("undeclared schema silently used an older draft")
	}
	// Unknown nested metaschemas still use the denied loader; data examples are not subschemas.
	if !errors.Is(p.Check(t.Context(), []byte(`{"type":"object","$defs":{"x":{"$id":"https://example.invalid/x","$schema":"https://untrusted.invalid/private-sentinel","type":"integer"}},"properties":{"x":{"$ref":"#/$defs/x"}}}`)), schemacheck.ErrSchema) {
		t.Fatal("unknown nested dialect admitted")
	}
	if p.Check(t.Context(), []byte(`{"type":"object","examples":[{"$schema":"arbitrary instance data"}]}`)) != nil {
		t.Fatal("instance data treated as a schema declaration")
	}
}
