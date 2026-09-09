package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/ndjson"
)

type usageCommandProbe struct {
	used   bool
	report usageCommandReport
}

type usageCommandReport struct {
	Sent, Success              bool
	Bytes                      int
	Shape                      map[string]string
	ResourceKinds              []string
	BonusEntries, AddOnEntries int
	CreditLabels               int
}

func (p *usageCommandProbe) query(ctx context.Context, caller kirofeature.Caller, inventory inventoryReport) error {
	if p.used || inventory.session == "" || len(inventory.usageDescriptor) == 0 {
		return errInventoryShape
	}
	p.used = true
	fields, err := ndjson.Object(inventory.usageDescriptor)
	var name string
	if err != nil || json.Unmarshal(fields["name"], &name) != nil || name != "usage" && name != "/usage" {
		return errInventoryShape
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.report.Sent = true
	raw, err := caller.Call(ctx, "_kiro.dev/commands/execute", map[string]any{"sessionId": inventory.session, "command": map[string]any{"command": "usage", "args": map[string]any{}}})
	if err != nil {
		return err
	}
	p.report.Bytes = len(raw)
	if len(raw) > 64<<10 {
		return errInventoryLimit
	}
	fields, err = ndjson.Object(raw)
	if err != nil || string(fields["success"]) != "true" && string(fields["success"]) != "false" {
		return errInventoryShape
	}
	p.report.Success = string(fields["success"]) == "true"
	p.report.Shape = map[string]string{}
	if err := observeContextMeta(raw, "result", 0, p.report.Shape); err != nil {
		return err
	}
	if data, err := ndjson.Object(fields["data"]); err == nil {
		for _, field := range []string{"usageBreakdowns", "bonusCredits", "addOnCredits"} {
			if value, present := data[field]; present {
				var entries []json.RawMessage
				if inventoryKind(value) != "array" || json.Unmarshal(value, &entries) != nil || len(entries) > 16 {
					return errInventoryShape
				}
				if field == "bonusCredits" {
					p.report.BonusEntries = len(entries)
				}
				if field == "addOnCredits" {
					p.report.AddOnEntries = len(entries)
				}
				if field != "usageBreakdowns" {
					continue
				}
				for _, entry := range entries {
					fields, err := ndjson.Object(entry)
					var kind string
					if err != nil || json.Unmarshal(fields["resourceType"], &kind) != nil {
						return errInventoryShape
					}
					var label string
					if json.Unmarshal(fields["displayName"], &label) == nil && strings.EqualFold(strings.TrimSpace(label), "credits") {
						p.report.CreditLabels++
					}
					// Retain only a bounded enum-shaped resource class, never labels or amounts.
					if len(kind) == 0 || len(kind) > 64 || strings.IndexFunc(kind, func(r rune) bool { return r != '_' && (r < 'A' || r > 'Z') }) >= 0 {
						kind = "unclassified"
					}
					p.report.ResourceKinds = append(p.report.ResourceKinds, kind)
				}
			}
		}
	}
	return nil
}

type usageQueryFixture struct{ calls int }

func (f *usageQueryFixture) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.calls++
	raw, _ := json.Marshal(params)
	if method != "_kiro.dev/commands/execute" || string(raw) != `{"command":{"args":{},"command":"usage"},"sessionId":"owned-usage"}` {
		return nil, errors.New("unexpected independent usage request")
	}
	return json.RawMessage(`{"success":true,"data":{"exampleUsed":12,"exampleLimit":100,"privateLabel":"private-fixture-value","usageBreakdowns":[{"resourceType":"CREDIT","displayName":"Credits"},{"resourceType":"private-fixture-value"}],"bonusCredits":[],"addOnCredits":[]}}`), nil
}

func TestUsageQueryBudgetAndPrivacy(t *testing.T) {
	inventory := inventoryReport{session: "owned-usage", usageDescriptor: json.RawMessage(`{"name":"usage"}`)}
	fixture, p := &usageQueryFixture{}, &usageCommandProbe{}
	if err := p.query(t.Context(), fixture, inventory); err != nil || !p.report.Sent || !p.report.Success || fixture.calls != 1 {
		t.Fatal("usage fixture query failed", err)
	}
	raw, _ := json.Marshal(p.report)
	if strings.Contains(string(raw), "private-fixture-value") || strings.Contains(string(raw), "owned-usage") || p.report.Shape["result.data.exampleUsed"] != "number" {
		t.Fatal("usage report leaked values or omitted structure")
	}
	if strings.Join(p.report.ResourceKinds, ",") != "CREDIT,unclassified" || p.report.CreditLabels != 1 {
		t.Fatal("usage resource class classification failed")
	}
	if !errors.Is(p.query(t.Context(), fixture, inventory), errInventoryShape) || fixture.calls != 1 {
		t.Fatal("usage query budget exceeded")
	}
	for _, invalid := range []inventoryReport{{}, {session: "owned-usage"}, {session: "owned-usage", usageDescriptor: json.RawMessage(`{"name":"model"}`)}} {
		if !errors.Is((&usageCommandProbe{}).query(t.Context(), fixture, invalid), errInventoryShape) || fixture.calls != 1 {
			t.Fatal("invalid usage prerequisite dispatched")
		}
	}
}

func TestUsageAdvertisementIsSessionBound(t *testing.T) {
	for _, owner := range []string{"owned-usage", "foreign-usage"} {
		report := inventoryReport{NotificationKinds: map[string]int{}}
		raw, _ := json.Marshal(map[string]any{"sessionId": owner, "commands": []any{map[string]any{"name": "/usage", "description": "private-fixture-description"}}})
		err := report.observe(acp.Notification{Method: "_kiro.dev/commands/available", Params: raw}, "owned-usage")
		if owner == "owned-usage" {
			if err != nil || len(report.usageDescriptor) == 0 || report.ToolsAvailable {
				t.Fatal("usage advertisement lost or confused with tools")
			}
		} else if err != errInventoryBinding || len(report.usageDescriptor) != 0 {
			t.Fatal("foreign usage advertisement accepted")
		}
	}
}

func TestKiroPinnedUsageAdvertisement(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for empty-agent command discovery; no usage query or model prompt")
	}
	observeSettingsPreservingInventory(t, executable, inventoryVariant{})
}

func TestKiroPinnedUsageQuery(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for one advertised read-only usage query; no model prompt")
	}
	observeSettingsPreservingInventory(t, executable, inventoryVariant{usage: &usageCommandProbe{}})
}
