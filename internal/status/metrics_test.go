package status

import (
	"dax-kiro-proxy/internal/kirofeature"
	"strings"
	"testing"
)

func TestMetricsQueueBoundsPendingDataAndPreservesOneLatestRecord(t *testing.T) {
	q := NewTurnQueue()
	record := TurnRecord{Scope: strings.Repeat("a", 64), Model: "claude-dax-fixture", SessionState: "created", ElapsedMS: 10, Effort: kirofeature.Status{State: kirofeature.Unknown}, Metadata: kirofeature.Metadata{ContextPercent: amount(5)}}
	for range 40 {
		if !q.Push(record) {
			t.Fatal("valid completion rejected")
		}
	}
	page := q.Drain()
	if len(page.Records) != 32 || page.Dropped != 8 || page.Records[0].Sequence != 9 || page.Records[31].Sequence != 40 {
		t.Fatal("metrics capacity or order was not bounded")
	}
	*page.Records[31].Metadata.ContextPercent = 99
	latest := q.Latest()
	if latest == nil || *latest.Metadata.ContextPercent != 5 {
		t.Fatal("drain mutated last turn status")
	}
	if len(q.Drain().Records) != 0 || q.Latest() == nil {
		t.Fatal("consumed completion reappeared or model-only status disappeared")
	}
	record.Scope = "raw user prompt"
	if q.Push(record) {
		t.Fatal("unbounded/non-digest correlation admitted")
	}
}
