package status

import (
	"encoding/json"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/kirofeature"
)

func TestStatusDisplayKeepsProviderUsageAndEstimatesDistinct(t *testing.T) {
	contextPercent, remaining, credits, multiplier := 12.5, 40.0, 0.25, 1.3
	record := TurnRecord{Scope: strings.Repeat("a", 64), Model: "claude-dax-fixture-0123456789abcdef", Multiplier: &multiplier, SessionState: "reused", ElapsedMS: 1250,
		Effort: kirofeature.Status{State: kirofeature.Current, Applied: "high"}, Metadata: kirofeature.Metadata{ContextPercent: &contextPercent, Metering: []kirofeature.Metering{{Unit: "credit", Value: credits}}},
		Estimate: &Estimate{Approximate: true, Method: "utf8-bytes/4-v1", InputContextTokens: 123, VisibleOutputTokens: 9, MediaExcluded: true, HiddenThinkingExcluded: true}}
	view := map[string]any{"available": true, "refreshing": false, "stale": true, "state": "refresh_failed", "data": UsageData{Remaining: &remaining}, "latest_turn": record}
	raw, _ := json.Marshal(view)
	line, err := FormatView(raw, record.Model)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Kiro last fixture", "model 1.3x", "effort high", "context 12.5%", "1.2s", "turn 0.25 credits", "40 credits left (stale)", "~tokens in 123 out 9"} {
		if !strings.Contains(line, want) {
			t.Fatal("missing normalized status segment", want, line)
		}
	}
	if strings.Contains(line, record.Scope) || strings.Contains(line, "0123456789abcdef") || strings.Contains(line, "billed") || len(line) > 1024 || strings.Count(line, "\n") != 1 {
		t.Fatal("status display exposed identifiers or unsupported billing claims", line)
	}
	view["available"], view["data"] = false, UsageData{}
	raw, _ = json.Marshal(view)
	line, err = FormatView(raw, record.Model)
	if err != nil || !strings.Contains(line, "Kiro last fixture") || !strings.Contains(line, "usage unavailable") {
		t.Fatal("account-usage failure removed the last model turn")
	}
	for _, mode := range []string{"model", "effort", "estimate", "negative"} {
		bad := copyRecord(record)
		switch mode {
		case "model":
			bad.Model = "claude-dax-private\x1b[31m"
		case "effort":
			bad.Effort.State = "private-status"
		case "estimate":
			bad.Estimate.Approximate = false
		case "negative":
			bad.ElapsedMS = -1
		}
		view["latest_turn"] = bad
		raw, _ = json.Marshal(view)
		if line, err := FormatView(raw, record.Model); err == nil || line != "" || strings.Contains(err.Error(), "private-") {
			t.Fatal("invalid display data was printed", line, err)
		}
	}
}
