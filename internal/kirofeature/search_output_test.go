package kirofeature

import (
	"encoding/json"
	"strings"
	"testing"
)

// Only the observed shape is used; all tags, queries, URLs and result text are synthetic.
const independentSearchOutput = `{"items":[{"owned_transport":{"query":"independent query","error":null,"results":[{"url":"https://example.org/one","title":"First independent source","snippet":"Synthetic excerpt","owned_rank":1},{"url":"https://example.org/two","title":"Second independent source","snippet":"Another synthetic excerpt","owned_flag":false}]}}]}`

func TestSearchOutputPreservesObservedStructuredResults(t *testing.T) {
	hits, err := SearchResults(json.RawMessage(independentSearchOutput), "independent query")
	if err != nil || len(hits) != 2 || hits[0].URL != "https://example.org/one" || hits[1].Title != "Second independent source" {
		t.Fatal("structured native results were not preserved in order")
	}
	empty := `{"items":[{"owned_transport":{"query":"independent query","error":null,"results":[]}}]}`
	if hits, err := SearchResults(json.RawMessage(empty), "independent query"); err != nil || hits == nil || len(hits) != 0 {
		t.Fatal("explicit empty result collection rejected")
	}
}

func TestSearchOutputRejectsAmbiguousOrUncorrelatedLayouts(t *testing.T) {
	for name, raw := range map[string]string{
		"query":          strings.Replace(independentSearchOutput, "independent query", "different query", 1),
		"error":          strings.Replace(independentSearchOutput, `"error":null`, `"error":"synthetic failure"`, 1),
		"missing-error":  strings.Replace(independentSearchOutput, `"error":null,`, "", 1),
		"null-results":   `{"items":[{"owned_transport":{"query":"independent query","error":null,"results":null}}]}`,
		"nested-prose":   `{"items":[{"owned_transport":{"query":"independent query","error":null,"results":"https://example.org/generated"}}]}`,
		"answer-only":    `{"text":"Synthetic answer https://example.org/answer"}`,
		"ambiguous-tag":  `{"items":[{"one":{},"two":{}}]}`,
		"multiple-items": `{"items":[{},{}]}`,
		"duplicate-url":  strings.Replace(independentSearchOutput, `"url":"https://example.org/one"`, `"url":"https://example.org/one","url":"https://example.org/other"`, 1),
		"bad-title":      strings.Replace(independentSearchOutput, `"title":"First independent source"`, `"title":42`, 1),
		"too-large":      `{"items":[],"padding":"` + strings.Repeat("x", 512<<10) + `"}`,
		"too-many":       `{"items":[{"owned_transport":{"query":"independent query","error":null,"results":[` + strings.Repeat(`{"url":"https://example.org/one","title":"Synthetic"},`, 32) + `{"url":"https://example.org/one","title":"Synthetic"}]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SearchResults(json.RawMessage(raw), "independent query"); err == nil {
				t.Fatal("unsupported result admitted")
			}
		})
	}
}
