package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/ndjson"
)

const MaxMetricsOutput = 9 << 10

// FormatMetrics formats every retained completion, without identity digests or arbitrary text.
// Long model labels are shortened for display only; sequence numbers still identify each record.
func FormatMetrics(raw []byte) (string, error) {
	if len(raw) > 64<<10 {
		return "", ErrView
	}
	root, err := metricObject(raw, []string{"records", "dropped"}, nil)
	if err != nil {
		return "", ErrView
	}
	var page MetricsPage
	var records []json.RawMessage
	if json.Unmarshal(raw, &page) != nil || json.Unmarshal(root["records"], &records) != nil || len(records) > 32 || page.Dropped > 9007199254740991 {
		return "", ErrView
	}
	var previous uint64
	lines := make([]string, 0, len(records)+1)
	for i, encoded := range records {
		fields, err := metricObject(encoded, []string{"sequence", "scope", "model", "session_state", "elapsed_ms", "effort", "metadata"}, []string{"multiplier", "local_estimate"})
		if err != nil {
			return "", ErrView
		}
		if _, err := metricObject(fields["effort"], []string{"state", "model"}, []string{"requested", "applied", "rejected"}); err != nil {
			return "", ErrView
		}
		meta, err := metricObject(fields["metadata"], nil, []string{"context_percent", "duration_ms", "metering"})
		if err != nil {
			return "", ErrView
		}
		if metering, ok := meta["metering"]; ok {
			var meters []json.RawMessage
			if json.Unmarshal(metering, &meters) != nil || len(meters) > 2 {
				return "", ErrView
			}
			for _, meter := range meters {
				if _, err := metricObject(meter, []string{"unit", "value"}, nil); err != nil {
					return "", ErrView
				}
			}
		}
		if estimate, ok := fields["local_estimate"]; ok {
			if _, err := metricObject(estimate, []string{"approximate", "method", "input_context_tokens", "visible_output_tokens", "logical_prefix_tokens", "media_excluded", "hidden_thinking_excluded"}, nil); err != nil {
				return "", ErrView
			}
		}
		r := page.Records[i]
		if !validRecord(r) || r.Sequence <= previous || r.Sequence > 9007199254740991 || r.Effort.Model != r.Model {
			return "", ErrView
		}
		previous = r.Sequence
		label, err := ModelLabel(r.Model)
		if err != nil {
			return "", ErrView
		}
		if len(label) > 40 {
			label = label[:37] + "..."
		}
		effort := string(r.Effort.State)
		if r.Effort.State == kirofeature.Current && r.Effort.Applied != "" {
			effort = r.Effort.Applied
		}
		parts := []string{fmt.Sprintf("Kiro turn#%d %s", r.Sequence, label), "effort " + effort, r.SessionState, fmt.Sprintf("%.1fs local", float64(r.ElapsedMS)/1000)}
		if r.Multiplier != nil {
			parts = append(parts, fmt.Sprintf("model %.6gx", *r.Multiplier))
		}
		if r.Metadata.ContextPercent != nil {
			parts = append(parts, fmt.Sprintf("context %.1f%%", *r.Metadata.ContextPercent))
		}
		if r.Metadata.DurationMS != nil {
			parts = append(parts, fmt.Sprintf("Kiro %.1fs", *r.Metadata.DurationMS/1000))
		}
		for _, meter := range r.Metadata.Metering {
			if meter.Unit == "credit" {
				parts = append(parts, fmt.Sprintf("%.6g credits", meter.Value))
			} else {
				parts = append(parts, fmt.Sprintf("metered tokens %.6g", meter.Value))
			}
		}
		if r.Estimate != nil {
			parts = append(parts, fmt.Sprintf("~tokens in %d out %d", r.Estimate.InputContextTokens, r.Estimate.VisibleOutputTokens))
		}
		lines = append(lines, strings.Join(parts, " | "))
	}
	if len(lines) > 0 && page.Dropped > 0 {
		lines = append(lines, fmt.Sprintf("Kiro metrics: %d older records dropped (runtime total).", page.Dropped))
	}
	text := strings.Join(lines, "\n")
	if len(text) > MaxMetricsOutput-128 {
		return "", ErrView
	}
	return text, nil
}

func metricObject(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	fields, err := ndjson.Object(raw)
	if err != nil {
		return nil, ErrView
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, ErrView
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrView
		}
	}
	return fields, nil
}
