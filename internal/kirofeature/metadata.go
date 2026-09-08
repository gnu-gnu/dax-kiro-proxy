package kirofeature

import (
	"encoding/json"
	"math"
	"strconv"

	"dax-kiro-proxy/internal/ndjson"
)

type Metering struct {
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
}
type Metadata struct {
	ContextPercent *float64   `json:"context_percent,omitempty"`
	DurationMS     *float64   `json:"duration_ms,omitempty"`
	Metering       []Metering `json:"metering,omitempty"`
}
type TurnMetadata struct{ data Metadata }

// Apply handles the repository's provisional private metadata shape. It retains only recognized
// numeric diagnostics. Independent live observations must confirm the version-specific mapping.
func (t *TurnMetadata) Apply(raw []byte) {
	if len(raw) > 64<<10 {
		return
	}
	fields, err := ndjson.Object(raw)
	if err != nil {
		return
	}
	if n := metadataNumber(fields["contextUsagePercentage"], 100); n != nil {
		t.data.ContextPercent = n
	}
	if n := metadataNumber(fields["turnDurationMs"], 3600000); n != nil {
		t.data.DurationMS = n
	}
	if data, ok := fields["meteringUsage"]; ok {
		if meters, valid := metadataMeters(data); valid {
			t.data.Metering = meters
		}
	}
}
func metadataNumber(raw []byte, max float64) *float64 {
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
		return nil
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || !validMetadataNumber(value, max) {
		return nil
	}
	return &value
}
func validMetadataNumber(value, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= max
}
func metadataMeters(raw []byte) ([]Metering, bool) {
	var entries []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &entries) != nil || len(entries) > 16 {
		return nil, false
	}
	meters := []Metering{}
	seen := map[string]bool{}
	for _, entry := range entries {
		fields, err := ndjson.Object(entry)
		var unit string
		if err != nil || !stringField(fields["unit"], &unit) {
			return nil, false
		}
		switch unit {
		case "credits":
			unit = "credit"
		case "tokens":
			unit = "token"
		case "credit", "token":
		default:
			continue
		}
		n := metadataNumber(fields["value"], 1e12)
		if n == nil || seen[unit] {
			return nil, false
		}
		seen[unit] = true
		meters = append(meters, Metering{unit, *n})
	}
	return meters, len(entries) == 0 || len(meters) > 0
}
func (t *TurnMetadata) Snapshot() Metadata { return t.data.Copy() }
func (m Metadata) Copy() Metadata {
	copyNumber := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		n := *p
		return &n
	}
	return Metadata{ContextPercent: copyNumber(m.ContextPercent), DurationMS: copyNumber(m.DurationMS), Metering: append([]Metering(nil), m.Metering...)}
}
func (m Metadata) Valid() bool {
	if m.ContextPercent != nil && !validMetadataNumber(*m.ContextPercent, 100) || m.DurationMS != nil && !validMetadataNumber(*m.DurationMS, 3600000) || len(m.Metering) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, meter := range m.Metering {
		if meter.Unit != "credit" && meter.Unit != "token" || seen[meter.Unit] || !validMetadataNumber(meter.Value, 1e12) {
			return false
		}
		seen[meter.Unit] = true
	}
	return true
}
