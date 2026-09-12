// Package schemawire defines the bounded, effect-free schema helper's input policy.
package schemawire

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
)

const MaxSchemaBytes = 64 << 10
const MaxArgumentBytes = 1 << 20
const SchemaCode = -32040
const ArgumentCode = -32041

var ErrInput = errors.New("invalid bounded schema input")

type Request struct {
	Schema    json.RawMessage `json:"schema"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func Schema(raw []byte) (any, error) {
	value, err := object(raw, MaxSchemaBytes, 8192)
	if err != nil {
		return nil, err
	}
	fields := value.(map[string]any)
	if fields["type"] != "object" {
		return nil, ErrInput
	}
	if draft, ok := fields["$schema"]; ok && !supportedDialect(draft) {
		return nil, ErrInput
	}
	return value, nil
}

// Admit fixed draft identifiers without rewriting the document. The worker selects the declared
// dialect; only an absent declaration defaults to 2020-12. Empty fragments and HTTP/HTTPS aliases
// are understood by the reviewed compiler. Other metaschemas cannot trigger resource retrieval.
func supportedDialect(value any) bool {
	uri, ok := value.(string)
	if !ok {
		return false
	}
	uri = strings.TrimSuffix(uri, "#")
	if rest, ok := strings.CutPrefix(uri, "https://"); ok {
		uri = rest
	} else if rest, ok := strings.CutPrefix(uri, "http://"); ok {
		uri = rest
	} else {
		return false
	}
	switch uri {
	case "json-schema.org/draft-07/schema", "json-schema.org/draft/2019-09/schema", "json-schema.org/draft/2020-12/schema":
		return true
	default:
		return false
	}
}

func Arguments(raw []byte) (any, error) { return object(raw, MaxArgumentBytes, 65536) }
func object(raw []byte, limit, nodes int) (any, error) {
	if len(raw) > limit {
		return nil, ErrInput
	}
	if _, err := ndjson.Object(raw); err != nil {
		return nil, ErrInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || !bounded(value, &nodes) {
		return nil, ErrInput
	}
	return value, nil
}
func bounded(value any, nodes *int) bool {
	*nodes--
	if *nodes < 0 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			if !bounded(child, nodes) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !bounded(child, nodes) {
				return false
			}
		}
	case json.Number:
		s := v.String()
		if len(s) > 128 {
			return false
		}
		if at := strings.IndexAny(s, "eE"); at >= 0 {
			exponent, err := strconv.Atoi(s[at+1:])
			if err != nil || exponent > 1000 || exponent < -1000 {
				return false
			}
		}
	}
	return true
}
