package status

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/kirofeature"
)

func noticeRecord() TurnRecord {
	return TurnRecord{Scope: strings.Repeat("a", 64), Model: "claude-dax-fixture-0123456789abcdef", SessionState: "created", ElapsedMS: 1250, Effort: kirofeature.Status{State: kirofeature.Current, Applied: "high"}, Metadata: kirofeature.Metadata{ContextPercent: amount(5), DurationMS: amount(900), Metering: []kirofeature.Metering{{Unit: "credit", Value: .01}, {Unit: "token", Value: 25}}}, Estimate: &Estimate{Approximate: true, Method: "utf8-bytes/4-v1", InputContextTokens: 120, VisibleOutputTokens: 4, LogicalPrefixTokens: 20, MediaExcluded: true, HiddenThinkingExcluded: true}}
}

func TestMetricsNoticeFormatsOnlyBoundedCompletedRecords(t *testing.T) {
	q := NewTurnQueue()
	q.Push(noticeRecord())
	raw, _ := json.Marshal(q.Drain())
	text, err := FormatMetrics(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Kiro turn#1 fixture", "effort high", "created", "1.2s local", "context 5.0%", "Kiro 0.9s", "0.01 credits", "metered tokens 25", "~tokens in 120 out 4"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing completion diagnostic", want)
		}
	}
	if strings.Contains(text, strings.Repeat("a", 64)) || strings.Contains(text, "0123456789abcdef") || strings.ContainsAny(text, "\x1b\r") {
		t.Fatal("private identity or terminal control reached completion display")
	}
	if empty, err := FormatMetrics([]byte(`{"records":[],"dropped":42}`)); err != nil || empty != "" {
		t.Fatal("empty queue emitted repeated notices")
	}
	for _, bad := range []string{
		`null`, `{"records":null,"dropped":0}`, `{"records":[],"dropped":null}`,
		strings.Replace(string(raw), `"sequence":1`, `"sequence":0`, 1),
		strings.Replace(string(raw), `"sequence":1`, `"sequence":9007199254740992`, 1),
		strings.Replace(string(raw), `"elapsed_ms":1250`, `"elapsed_ms":null`, 1),
		strings.Replace(string(raw), `"scope":`, `"SCOPE":`, 1),
		strings.Replace(string(raw), `"effort":{`, `"effort":{"reason":"private-sentinel",`, 1),
		strings.Replace(string(raw), `"unit":"credit"`, `"unit":"private-sentinel"`, 1),
		strings.Replace(string(raw), `"approximate":true`, `"approximate":false`, 1),
		strings.Replace(string(raw), `"applied":"high"`, `"applied":"high","APPLIED":"low"`, 1),
		strings.Replace(string(raw), `"dropped":0`, `"dropped":0,"initialUserMessage":"private-sentinel"`, 1),
		strings.Repeat(" ", (64<<10)+1),
	} {
		if out, err := FormatMetrics([]byte(bad)); err == nil || out != "" {
			t.Fatal("invalid metric page reached completion display")
		}
	}
}

func TestMetricsNoticeRetainsEveryQueuedRecordWithinHookOutputLimit(t *testing.T) {
	q := NewTurnQueue()
	r := noticeRecord()
	r.Model = "claude-dax-" + strings.Repeat("a", 230)
	r.Effort.State = kirofeature.Configured
	r.ElapsedMS, r.Multiplier = 7200000, amount(math.SmallestNonzeroFloat64)
	r.Metadata = kirofeature.Metadata{ContextPercent: amount(100), DurationMS: amount(3600000), Metering: []kirofeature.Metering{{Unit: "credit", Value: math.SmallestNonzeroFloat64}, {Unit: "token", Value: math.SmallestNonzeroFloat64}}}
	r.Estimate.InputContextTokens, r.Estimate.VisibleOutputTokens = 8<<20, 1<<38
	for range 33 {
		if !q.Push(r) {
			t.Fatal("extreme valid record rejected")
		}
	}
	page := q.Drain()
	raw, _ := json.Marshal(page)
	text, err := FormatMetrics(raw)
	output, _ := json.Marshal(map[string]string{"systemMessage": text})
	if err != nil || len(output)+1 > 9<<10 || strings.Count(text, "Kiro turn#") != 32 || !strings.Contains(text, "1 older records dropped") {
		t.Fatal("bounded hook output lost queued completions", len(output), err)
	}
	for n := 2; n <= 33; n++ {
		if !strings.Contains(text, fmt.Sprintf("Kiro turn#%d ", n)) {
			t.Fatal("completion sequence missing")
		}
	}
	for i := range page.Records {
		page.Records[i].Sequence = 9007199254740991 - 31 + uint64(i)
	}
	page.Dropped = 9007199254740991 - 32
	raw, _ = json.Marshal(page)
	text, err = FormatMetrics(raw)
	output, _ = json.Marshal(map[string]string{"systemMessage": text})
	if err != nil || len(output)+1 > 9<<10 || strings.Count(text, "Kiro turn#") != 32 {
		t.Fatal("maximum sequence widths lost queued completions", len(output), err)
	}
	page.Records[1].Sequence = page.Records[0].Sequence
	raw, _ = json.Marshal(page)
	if _, err := FormatMetrics(raw); err == nil {
		t.Fatal("duplicate completion sequence accepted")
	}
}
