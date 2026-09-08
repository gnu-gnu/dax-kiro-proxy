// Package jsoncanon supplies a conservative JSON identity: keys and whitespace are normalized,
// while exact number spellings are retained. It never rounds numbers through binary floating point.
package jsoncanon

import (
	"bytes"
	"encoding/json"

	"dax-kiro-proxy/internal/ndjson"
)

func Object(raw []byte) ([]byte, error) {
	if _, err := ndjson.Object(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
