package kirofeature

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionalMetadataReplacesDuplicateValuesAndDegradesIndependently(t *testing.T) {
	var turn TurnMetadata
	raw := []byte(`{"sessionId":"synthetic","contextUsagePercentage":14.5,"turnDurationMs":825,"meteringUsage":[{"value":0.025,"unit":"credit","unitPlural":"credits"}],"future":"private-prompt-sentinel"}`)
	for range 3 {
		turn.Apply(raw)
	}
	got := turn.Snapshot()
	if got.ContextPercent == nil || *got.ContextPercent != 14.5 || got.DurationMS == nil || *got.DurationMS != 825 || len(got.Metering) != 1 || got.Metering[0].Value != 0.025 {
		t.Fatal("optional metadata was lost or duplicate credit was summed")
	}
	turn.Apply([]byte(`{"contextUsagePercentage":101,"turnDurationMs":900,"meteringUsage":[{"value":-1,"unit":"credit"},{"value":1,"unit":"private-prompt-sentinel"}]}`))
	got = turn.Snapshot()
	if *got.ContextPercent != 14.5 || *got.DurationMS != 900 || len(got.Metering) != 1 || got.Metering[0].Value != 0.025 {
		t.Fatal("malformed optional fields discarded independent valid values")
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "private-prompt-sentinel") {
		t.Fatal("unrecognized private metadata escaped into status")
	}
	*got.ContextPercent = 99
	got.Metering[0].Value = 999
	if *turn.Snapshot().ContextPercent != 14.5 || turn.Snapshot().Metering[0].Value != 0.025 {
		t.Fatal("metadata snapshot shares mutable storage")
	}
	turn.Apply([]byte(`{"meteringUsage":[{"value":0.1,"unit":"credit"},{"value":0.2,"unit":"credit"}]}`))
	if turn.Snapshot().Metering[0].Value != 0.025 {
		t.Fatal("ambiguous repeated metering unit changed the last valid snapshot")
	}
}
