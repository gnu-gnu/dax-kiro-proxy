package kirofeature

import (
	"encoding/json"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/ndjson"
)

type SearchHit struct{ URL, Title string }

// SearchResults admits the structured shape observed on Kiro 2.21.4: one items entry,
// one opaque transport tag, and a payload with query/results/error. The tag is not a
// result field. No recursive URL discovery or parsing of generated answer text is used.
func SearchResults(raw json.RawMessage, query string) ([]SearchHit, error) {
	if len(raw) > 512<<10 || query == "" {
		return nil, acp.ErrProtocol
	}
	outer, err := ndjson.Object(raw)
	var items []json.RawMessage
	if err != nil || len(outer) != 1 || json.Unmarshal(outer["items"], &items) != nil || len(items) != 1 {
		return nil, acp.ErrProtocol
	}
	item, err := ndjson.Object(items[0])
	if err != nil || len(item) != 1 {
		return nil, acp.ErrProtocol
	}
	var payload map[string]json.RawMessage
	for _, value := range item {
		payload, err = ndjson.Object(value)
	}
	var actualQuery string
	var results []json.RawMessage
	if err != nil || !stringField(payload["query"], &actualQuery) || actualQuery != query || string(payload["error"]) != "null" || json.Unmarshal(payload["results"], &results) != nil || results == nil || len(results) > 32 {
		return nil, acp.ErrProtocol
	}
	hits := make([]SearchHit, 0, len(results))
	for _, result := range results {
		fields, err := ndjson.Object(result)
		var hit SearchHit
		if err != nil || !stringField(fields["url"], &hit.URL) || !stringField(fields["title"], &hit.Title) || len(hit.URL) > 8192 || len(hit.Title) > 4096 {
			return nil, acp.ErrProtocol
		}
		if raw, present := fields["snippet"]; present {
			var snippet string
			if !stringField(raw, &snippet) {
				return nil, acp.ErrProtocol
			}
		}
		hits = append(hits, hit)
	}
	return hits, nil
}
