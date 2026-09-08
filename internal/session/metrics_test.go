package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/session"
	"dax-kiro-proxy/internal/status"
)

func TestToolHandoffPublishesOnlyAfterOwnedTurnCompletes(t *testing.T) {
	q := status.NewTurnQueue()
	d := toolDriver(t, "chat-tools", time.Second, func(cfg *session.Config) { cfg.Metrics = q; cfg.MetricsScope = strings.Repeat("b", 64) })
	r := toolRequest(t)
	turn, err := d.Start(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	prefix, uses := toolHandoff(t, turn)
	turn.Finish()
	if len(q.Drain().Records) != 0 {
		t.Fatal("tool handoff incorrectly completed the owned turn")
	}
	next, err := d.Start(context.Background(), followup(t, r, prefix, uses))
	if err != nil {
		t.Fatal(err)
	}
	finalText, err := collect(context.Background(), next)
	if err != nil {
		next.Cancel()
		t.Fatal(err)
	}
	if len(q.Drain().Records) != 0 {
		t.Fatal("turn metric preceded final delivery")
	}
	next.Finish()
	next.Finish()
	page := q.Drain()
	if len(page.Records) != 1 {
		t.Fatal("tool round trip did not create exactly one completion")
	}
	estimate := page.Records[0].Estimate
	want := int64((len(prefix) + len(finalText) + len(uses[0].Input) + 3) / 4)
	if estimate == nil || !estimate.Approximate || estimate.VisibleOutputTokens != want {
		t.Fatal("visible output estimate lost text or tool arguments across HTTP handoffs")
	}
}

func TestForegroundMetricsRequireFinalDeliveryAndReplaceRepeatedMetadata(t *testing.T) {
	for _, kind := range []string{"main", "title", "background", "undelivered", "cancelled", "no-private-fields"} {
		t.Run(kind, func(t *testing.T) {
			mode := "pool-metadata"
			if kind == "no-private-fields" {
				mode = "pool-normal"
			}
			cfg := managerConfig(t, mode)
			queue := status.NewTurnQueue()
			cfg.Metrics = queue
			m, err := session.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			r := mainRequest(t, "synthetic-identity-must-not-be-in-metrics")
			if kind == "title" {
				r.System = []anthropic.Block{{Type: "text", Text: "Create a title for this conversation."}}
				r.Extra["thinking"] = json.RawMessage(`{"type":"disabled"}`)
				r.Extra["output_config"] = json.RawMessage(`{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}}`)
			}
			if kind == "background" {
				r.Identity.ParentAgent = "synthetic-parent"
			}
			turn, err := m.Start(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "cancelled" {
				turn.Cancel()
			} else {
				text, err := collect(context.Background(), turn)
				if err != nil {
					turn.Cancel()
					t.Fatal(err)
				}
				if len(queue.Drain().Records) != 0 {
					t.Fatal("completion published before delivery")
				}
				if kind == "undelivered" {
					turn.Cancel()
				} else {
					turn.Finish()
					turn.Finish()
				}
				if kind == "main" {
					r.Messages = append(r.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: text}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "synthetic second input"}}})
					managerTurn(t, m, r)
				}
			}
			page := queue.Drain()
			expected := 0
			if kind == "main" {
				expected = 2
			}
			if kind == "no-private-fields" {
				expected = 1
			}
			if len(page.Records) != expected {
				t.Fatalf("got %d records, want %d", len(page.Records), expected)
			}
			for i, record := range page.Records {
				if record.Estimate == nil || !record.Estimate.Approximate || record.Estimate.InputContextTokens <= 0 || record.Estimate.VisibleOutputTokens <= 0 || i == 1 && record.Estimate.LogicalPrefixTokens <= 0 {
					t.Fatal("local estimate unavailable or missing repeated-prefix opportunity")
				}
				state := "created"
				if i == 1 {
					state = "reused"
				}
				if record.Sequence != uint64(i+1) || record.SessionState != state || record.Model != fixtureClientID || len(record.Scope) != 64 {
					t.Fatal("invalid turn identity or lifecycle metadata")
				}
				if kind == "main" {
					meta := record.Metadata
					if meta.ContextPercent == nil || *meta.ContextPercent != 12.5 || meta.DurationMS == nil || *meta.DurationMS != 725 || len(meta.Metering) != 1 || meta.Metering[0].Unit != "credit" || meta.Metering[0].Value != 0.025 {
						t.Fatal("private metadata absent or counted repeatedly")
					}
				} else if len(record.Metadata.Metering) != 0 {
					t.Fatal("invented missing metering")
				}
			}
			encoded, _ := json.Marshal(page)
			if strings.Contains(string(encoded), "synthetic-") {
				t.Fatal("raw backend metadata or client identity retained")
			}
			if len(queue.Drain().Records) != 0 {
				t.Fatal("metrics replayed")
			}
		})
	}
}
