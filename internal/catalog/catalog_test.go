package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/catalog"
)

func TestStableCatalogMappingAndValidation(t *testing.T) {
	models := []catalog.Backend{{ID: "Model.A", Name: "A", Description: "Synthetic model"}, {ID: "model-a", Name: "B"}, {ID: "model_a", Name: "C"}}
	first, err := catalog.New(models, "auto")
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.New(models, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.List()) != 4 {
		t.Fatal("omitted current model not included")
	}
	seen := map[string]bool{}
	for i, m := range first.List() {
		if !strings.HasPrefix(m.ID, "claude-dax-") || seen[m.ID] || m.ID != second.List()[i].ID {
			t.Fatal("unstable/colliding IDs")
		}
		seen[m.ID] = true
		entry, err := first.Resolve(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 && entry.ID != models[i].ID {
			t.Fatal("incorrect reverse mapping")
		}
		if m.Object != "model" || m.OwnedBy != "kiro" {
			t.Fatal("incorrect catalog envelope")
		}
	}
	for _, bad := range []string{"Model.A", "claude-sonnet-4", "claude-dax-unknown", first.List()[0].ID + "-other"} {
		if _, err := first.Resolve(bad); err == nil {
			t.Fatal("accepted ID outside current catalog")
		}
	}
	for _, tc := range []struct {
		models  []catalog.Backend
		current string
	}{{nil, ""}, {[]catalog.Backend{{ID: "same"}, {ID: "same"}}, "same"}, {[]catalog.Backend{{ID: ""}}, "auto"}, {[]catalog.Backend{{ID: "white space"}}, ""}, {[]catalog.Backend{{ID: "x", Description: strings.Repeat("x", 8193)}}, ""}} {
		if _, err := catalog.New(tc.models, tc.current); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
	models[0].ID = "mutated"
	if _, err := first.Resolve(first.List()[0].ID); err != nil {
		t.Fatal("caller mutation changed catalog")
	}
}

func TestCreditMultiplierIsOnlyDisplayMetadata(t *testing.T) {
	value := 1.25
	a, err := catalog.New([]catalog.Backend{{ID: "backend", Name: "Model", Multiplier: &value}}, "backend")
	if err != nil {
		t.Fatal(err)
	}
	value = 2
	b, err := catalog.New([]catalog.Backend{{ID: "backend", Name: "Model", Multiplier: &value}}, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if a.List()[0].ID != b.List()[0].ID || !strings.Contains(a.List()[0].Name, "1.25") || !strings.Contains(a.List()[0].Description, "Kiro") {
		t.Fatal("display metadata changed backend identity")
	}
}

func TestSessionCatalogWireShapes(t *testing.T) {
	legacy := []byte(`{"sessionId":"synth-session","models":{"currentModelId":"model.a","availableModels":[{"modelId":"model.a","name":"A","description":"Synthetic"}]}}`)
	s, err := catalog.DecodeSession(legacy)
	if err != nil || s.ID != "synth-session" || s.Catalog.Current() != "model.a" || s.ConfigID != "" {
		t.Fatalf("legacy model state: %v", err)
	}
	modern := []byte(`{"sessionId":"synth-session","configOptions":[{"id":"safe-model-selector","category":"model","name":"Model","type":"select","currentValue":"model.a","options":[{"value":"model.a","name":"A"},{"value":"model.b","name":"B"}]}]}`)
	s, err = catalog.DecodeSession(modern)
	if err != nil || s.ConfigID != "safe-model-selector" || len(s.Catalog.List()) != 2 {
		t.Fatalf("public config selector: %v", err)
	}
	for _, bad := range []string{`{}`, `{"sessionId":null,"models":{}}`, `{"sessionId":"s","models":{"availableModels":[],"currentModelId":null}}`, `{"sessionId":"s","models":{"availableModels":[{"modelId":"a"},{"modelId":"a"}]}}`, `{"sessionId":"s","models":{"availableModels":[{"modelId":null}]}}`, `{"sessionId":"s","configOptions":[{"id":"m","category":"model","type":"boolean","currentValue":true}]}`} {
		if _, err := catalog.DecodeSession([]byte(bad)); err == nil {
			t.Fatal("invalid session catalog accepted")
		}
	}
	if !json.Valid(legacy) {
		t.Fatal("invalid independent fixture")
	}
}
