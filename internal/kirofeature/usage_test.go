package kirofeature

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
)

const usageFixture = `{"success":true,"message":"private-account-label","data":{"usageBreakdowns":[{"resourceType":"CREDIT","displayName":"Credits","used":12.5,"hasLimit":true,"limit":100}],"bonusCredits":[{"remaining":900}],"addOnCredits":[{"remaining":800}]}}`

func TestAccountUsageAcceptsOnlyReportedCreditAmounts(t *testing.T) {
	for _, tc := range []struct {
		name, raw   string
		used, limit float64
		limited     bool
	}{
		{"reported", usageFixture, 12.5, 100, true},
		{"over-limit", strings.Replace(usageFixture, `"used":12.5`, `"used":125`, 1), 125, 100, true},
		{"no-limit", strings.Replace(usageFixture, `"hasLimit":true`, `"hasLimit":false`, 1), 12.5, 0, false},
		{"zero", strings.ReplaceAll(strings.Replace(usageFixture, "12.5", "0", 1), `"limit":100`, `"limit":0`), 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeAccountUsage([]byte(tc.raw))
			if err != nil || got.Used == nil || *got.Used != tc.used || (got.Limit != nil) != tc.limited || tc.limited && *got.Limit != tc.limit {
				t.Fatal("reported amount mapping failed", err)
			}
			raw, _ := json.Marshal(got)
			for _, absent := range []string{"private-account-label", "remaining", "900", "800"} {
				if strings.Contains(string(raw), absent) {
					t.Fatal("copied identity or invented aggregate")
				}
			}
		})
	}
}

func TestAccountUsageRejectsAmbiguousOrMalformedAmounts(t *testing.T) {
	for name, raw := range map[string]string{
		"failure":            strings.Replace(usageFixture, `"success":true`, `"success":false`, 1),
		"duplicate":          strings.Replace(usageFixture, `"used":12.5`, `"used":12.5,"used":20`, 1),
		"unknown-unit":       strings.Replace(usageFixture, "CREDIT", "TOKENS", 1),
		"wrong-case":         strings.Replace(usageFixture, "CREDIT", "credit", 1),
		"negative":           strings.Replace(usageFixture, "12.5", "-1", 1),
		"overflow":           strings.Replace(usageFixture, "12.5", "1e999", 1),
		"excess":             strings.Replace(usageFixture, "12.5", "1000000000001", 1),
		"null":               strings.Replace(usageFixture, "12.5", "null", 1),
		"string":             strings.Replace(usageFixture, "12.5", `"12.5"`, 1),
		"missing-used":       strings.Replace(usageFixture, `"used":12.5,`, "", 1),
		"missing-limit":      strings.Replace(usageFixture, `,"limit":100`, "", 1),
		"limit-type":         strings.Replace(usageFixture, `"hasLimit":true`, `"hasLimit":1`, 1),
		"two-credit-entries": `{"success":true,"data":{"usageBreakdowns":[{"resourceType":"CREDIT","used":1,"hasLimit":false},{"resourceType":"CREDIT","used":2,"hasLimit":false}]}}`,
		"too-many":           `{"success":true,"data":{"usageBreakdowns":[` + strings.Repeat(`{"resourceType":"OTHER"},`, 16) + `{"resourceType":"CREDIT","used":1,"hasLimit":false}]}}`,
		"large":              strings.Repeat(" ", 64<<10) + usageFixture,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAccountUsage([]byte(raw)); !errors.Is(err, ErrUsageUnavailable) || strings.Contains(err.Error(), "private-account-label") {
				t.Fatal("unsafe usage accepted or diagnostic exposed", err)
			}
		})
	}
}

type usagePeerFixture struct {
	calls         []string
	notifications []acp.Notification
	tools, usage  string
}

func (p *usagePeerFixture) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	p.calls = append(p.calls, method+" "+string(raw))
	switch len(p.calls) {
	case 1:
		if method != "session/new" || string(raw) != `{"cwd":"/owned-work","mcpServers":[]}` {
			return nil, errors.New("unexpected session request")
		}
		return json.RawMessage(`{"sessionId":"usage-owned"}`), nil
	case 2, 3:
		command := "tools"
		result := p.tools
		if len(p.calls) == 3 {
			command = "usage"
			result = p.usage
		}
		if method != "_kiro.dev/commands/execute" || string(raw) != `{"command":{"args":{},"command":"`+command+`"},"sessionId":"usage-owned"}` {
			return nil, errors.New("unexpected command request")
		}
		return json.RawMessage(result), nil
	}
	return nil, errors.New("usage exceeded RPC budget")
}
func (p *usagePeerFixture) Next(ctx context.Context) (acp.Notification, error) {
	if len(p.notifications) == 0 {
		<-ctx.Done()
		return acp.Notification{}, ctx.Err()
	}
	n := p.notifications[0]
	p.notifications = p.notifications[1:]
	return n, nil
}
func usageFixturePeer() *usagePeerFixture {
	return &usagePeerFixture{notifications: []acp.Notification{{Method: "_kiro.dev/commands/available", Params: json.RawMessage(`{"sessionId":"usage-owned","commands":[{"name":"/tools"},{"name":"usage"}]}`)}}, tools: `{"success":true,"data":{"tools":[]}}`, usage: usageFixture}
}

func TestAccountUsageQueryRequiresOwnedAdvertisementAndEmptyTools(t *testing.T) {
	for _, name := range []string{"success", "foreign", "missing-usage", "duplicate-command", "native-tools", "missing-tools", "cancelled", "mcp", "flood"} {
		t.Run(name, func(t *testing.T) {
			p := usageFixturePeer()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch name {
			case "foreign":
				p.notifications[0].Params = json.RawMessage(strings.Replace(string(p.notifications[0].Params), "usage-owned", "foreign", 1))
			case "missing-usage":
				p.notifications[0].Params = json.RawMessage(strings.Replace(string(p.notifications[0].Params), `"usage"`, `"model"`, 1))
			case "duplicate-command":
				p.notifications[0].Params = json.RawMessage(strings.Replace(string(p.notifications[0].Params), `{"name":"usage"}`, `{"name":"usage"},{"name":"/usage"}`, 1))
			case "native-tools":
				p.tools = `{"success":true,"data":{"tools":[{"name":"execute"}]}}`
			case "missing-tools":
				p.tools = `{"success":true,"data":{}}`
			case "cancelled":
				cancel()
			case "mcp":
				p.notifications = append([]acp.Notification{{Method: "_kiro.dev/mcp/server_initialized", Params: json.RawMessage(`{"sessionId":"usage-owned","serverName":"unwanted"}`)}}, p.notifications...)
			case "flood":
				p.notifications = make([]acp.Notification, 65)
				for i := range p.notifications {
					p.notifications[i] = acp.Notification{Method: "unknown", Params: json.RawMessage(`{}`)}
				}
			}
			got, err := ReadAccountUsage(ctx, p, "/owned-work")
			if name == "success" {
				if err != nil || got.Used == nil || *got.Used != 12.5 || len(p.calls) != 3 {
					t.Fatal("usage request did not complete", err)
				}
			} else {
				if err == nil || len(p.calls) > 2 {
					t.Fatal("failed prerequisite allowed usage command")
				}
				if name == "cancelled" && len(p.calls) != 0 {
					t.Fatal("cancelled query dispatched")
				}
			}
			for _, call := range p.calls {
				if strings.Contains(call, "session/prompt") {
					t.Fatal("usage created a model turn")
				}
			}
		})
	}
}
