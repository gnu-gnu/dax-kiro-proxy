package status

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/ndjson"
)

var ErrView = errors.New("invalid local status view")

// ModelLabel is a display-only shortening of a validated client alias, never a reverse mapping.
func ModelLabel(alias string) (string, error) {
	if !strings.HasPrefix(alias, "claude-dax-") || len(alias) <= len("claude-dax-") || len(alias) > 256 {
		return "", ErrView
	}
	for _, c := range []byte(alias) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", ErrView
		}
	}
	label := strings.TrimPrefix(alias, "claude-dax-")
	if index := strings.LastIndexByte(label, '-'); index > 0 && len(label[index+1:]) == 16 {
		digest := true
		for _, c := range []byte(label[index+1:]) {
			digest = digest && (c >= '0' && c <= '9' || c >= 'a' && c <= 'f')
		}
		if digest {
			label = label[:index]
		}
	}
	return label, nil
}

// FormatView consumes only the gateway's bounded numeric status contract. No client stdin,
// transcript, account identity, remote error prose or provider token estimate is rendered.
func FormatView(raw []byte, fallbackModel string) (string, error) {
	model, err := ModelLabel(fallbackModel)
	if err != nil || len(raw) > 64<<10 {
		return "", ErrView
	}
	fields, err := ndjson.Object(raw)
	if err != nil || len(fields) > 16 {
		return "", ErrView
	}
	var view struct {
		UsageSnapshot
		Latest *TurnRecord `json:"latest_turn"`
	}
	// Extract exact protocol names before typed decoding; casing variants cannot override them.
	known := make(map[string]json.RawMessage)
	for _, key := range []string{"available", "refreshing", "stale", "state", "data"} {
		value, ok := fields[key]
		if !ok || string(value) == "null" {
			return "", ErrView
		}
		known[key] = value
	}
	if value, ok := fields["latest_turn"]; ok {
		known["latest_turn"] = value
	}
	filtered, _ := json.Marshal(known)
	if json.Unmarshal(filtered, &view) != nil {
		return "", ErrView
	}
	switch view.State {
	case "unsupported", "unavailable", "current", "refresh_failed":
	default:
		return "", ErrView
	}
	hasUsage := view.Data.Used != nil || view.Data.Limit != nil || view.Data.Remaining != nil
	if (view.Available || hasUsage) && !validUsage(view.Data) {
		return "", ErrView
	}
	parts := []string{"Kiro " + model, "no completed turn"}
	if last := view.Latest; last != nil {
		if !validRecord(*last) {
			return "", ErrView
		}
		model, err = ModelLabel(last.Model)
		if err != nil {
			return "", ErrView
		}
		effort := string(last.Effort.State)
		if last.Effort.State == kirofeature.Current && last.Effort.Applied != "" {
			effort = last.Effort.Applied
		}
		parts = []string{"Kiro last " + model, "effort " + effort}
		if last.Multiplier != nil {
			parts = append(parts, fmt.Sprintf("model %gx", *last.Multiplier))
		}
		if last.Metadata.ContextPercent != nil {
			parts = append(parts, fmt.Sprintf("context %.1f%%", *last.Metadata.ContextPercent))
		}
		parts = append(parts, fmt.Sprintf("%.1fs", float64(last.ElapsedMS)/1000))
		for _, meter := range last.Metadata.Metering {
			if meter.Unit == "credit" {
				parts = append(parts, fmt.Sprintf("turn %g credits", meter.Value))
			}
		}
		if last.Estimate != nil {
			parts = append(parts, fmt.Sprintf("~tokens in %d out %d", last.Estimate.InputContextTokens, last.Estimate.VisibleOutputTokens))
		}
	}
	usage := "usage unavailable"
	if view.Available {
		var amounts []string
		if view.Data.Remaining != nil {
			amounts = append(amounts, fmt.Sprintf("%g credits left", *view.Data.Remaining))
		}
		if view.Data.Used != nil {
			amounts = append(amounts, fmt.Sprintf("%g credits used", *view.Data.Used))
		}
		if view.Data.Limit != nil {
			amounts = append(amounts, fmt.Sprintf("%g credit limit", *view.Data.Limit))
		}
		usage = strings.Join(amounts, ", ")
		if view.Stale {
			usage += " (stale)"
		}
	}
	parts = append(parts, usage)
	line := strings.Join(parts, " | ") + "\n"
	if len(line) > 1024 {
		return "", ErrView
	}
	return line, nil
}
