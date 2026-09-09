package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/ndjson"
)

// These finite probes query effort and can select an advertised model, then verify two settings.
// Empty-argument query success alone does not establish applied effort. No probe sends a prompt.
type effortWireProbe struct {
	used        bool
	selectModel bool
	apply       bool
	report      effortWireReport
}

type effortWireReport struct {
	Sent, Success                bool
	ModelSelected                bool
	Bytes                        int
	DescriptorShape, ResultShape map[string]string
	InitialLevels                map[string]int
	Readbacks                    []string
	SettingCalls                 int
	MetadataNotifications        int
}

func (p *effortWireProbe) query(ctx context.Context, caller kirofeature.Caller, inventory inventoryReport) error {
	if p.used || inventory.session == "" || len(inventory.effortDescriptor) == 0 || p.apply && !p.selectModel {
		return errInventoryShape
	}
	p.used = true
	p.report.DescriptorShape = map[string]string{}
	p.report.ResultShape = map[string]string{}
	descriptor, err := ndjson.Object(inventory.effortDescriptor)
	if err != nil || len(descriptor) > 32 {
		return errInventoryShape
	}
	if inventoryKind(descriptor["meta"]) == "object" {
		meta, err := ndjson.Object(descriptor["meta"])
		if err != nil || len(meta) > 32 {
			return errInventoryShape
		}
		// Description dictionaries have presentation labels as keys, not protocol member names.
		delete(meta, "subcommandDescriptions")
		delete(meta, "subcommandHints")
		descriptor["meta"], _ = json.Marshal(meta)
	}
	shapeInput, _ := json.Marshal(descriptor)
	if err := observeContextMeta(shapeInput, "descriptor", 0, p.report.DescriptorShape); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	selected := ""
	if p.selectModel {
		models, _, err := catalog.DecodeModels(map[string]json.RawMessage{"models": inventory.sessionModels})
		if err != nil {
			return err
		}
		for _, model := range models.Models() {
			if strings.Contains(model.ID, "sonnet") && strings.Contains(model.ID, "4.6") {
				selected = model.ID
				break
			}
		}
		if selected == "" {
			return catalog.ErrModel
		}
		raw, err := caller.Call(ctx, "session/set_model", map[string]any{"sessionId": inventory.session, "modelId": selected})
		if err != nil {
			return err
		}
		fields, err := ndjson.Object(raw)
		if err != nil {
			return errInventoryShape
		}
		if _, present := fields["models"]; present {
			confirmed, _, err := catalog.DecodeModels(fields)
			if err != nil || confirmed.Current() != selected {
				return errInventoryShape
			}
		}
		p.report.ModelSelected = true
	}
	p.report.Sent = true
	raw, err := caller.Call(ctx, "_kiro.dev/commands/execute", map[string]any{
		"sessionId": inventory.session, "command": map[string]any{"command": "effort", "args": map[string]any{}},
	})
	if err != nil {
		return err
	}
	p.report.Bytes = len(raw)
	if len(raw) > 64<<10 {
		return errInventoryLimit
	}
	fields, err := ndjson.Object(raw)
	if err != nil || string(fields["success"]) != "true" && string(fields["success"]) != "false" {
		return errInventoryShape
	}
	p.report.Success = string(fields["success"]) == "true"
	if err := observeContextMeta(raw, "result", 0, p.report.ResultShape); err != nil {
		return err
	}
	p.report.InitialLevels = effortMessageLevels(fields["message"])
	if !p.apply {
		return nil
	}
	if !p.report.Success {
		return errInventoryShape
	}
	adapter := kirofeature.NewEffort(nil)
	advertisement, _ := json.Marshal(map[string]any{"commands": []json.RawMessage{inventory.effortDescriptor}})
	if err := adapter.Advertise(advertisement); err != nil {
		return err
	}
	source, ok := caller.(effortNotificationSource)
	if !ok {
		return errInventoryShape
	}
	settings := &effortCountingCaller{caller: caller}
	for _, level := range []string{"high", "low"} {
		// Discard already queued state before dispatch; it cannot establish a new setting.
		if _, _, err := readEffortMetadata(ctx, source, inventory.session, 50*time.Millisecond); err != nil {
			return err
		}
		for repeat := 0; repeat < 2; repeat++ {
			status, err := adapter.Sync(ctx, settings, inventory.session, selected, level)
			if err != nil {
				return err
			}
			if status.State != kirofeature.Current || status.Applied != level || status.Rejected {
				return errInventoryShape
			}
		}
		if settings.calls != len(p.report.Readbacks)+1 {
			return errInventoryLimit
		}
		p.report.SettingCalls = settings.calls
		observed, count, err := readEffortMetadata(ctx, source, inventory.session, 300*time.Millisecond)
		p.report.MetadataNotifications += count
		if err != nil {
			return err
		}
		if observed != level {
			return errInventoryShape
		}
		p.report.Readbacks = append(p.report.Readbacks, level)
	}
	return nil
}

type effortNotificationSource interface {
	Next(context.Context) (acp.Notification, error)
}

func readEffortMetadata(ctx context.Context, source effortNotificationSource, session string, wait time.Duration) (string, int, error) {
	window, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	latest, count, bytes := "", 0, 0
	for events := 0; events < 64; events++ {
		event, err := source.Next(window)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return latest, count, nil
		}
		if err != nil {
			return latest, count, err
		}
		bytes += len(event.Params)
		if len(event.Params) > 64<<10 || bytes > 1<<20 {
			return latest, count, errInventoryLimit
		}
		if event.Method != "_kiro.dev/metadata" {
			continue
		}
		fields, err := ndjson.Object(event.Params)
		var owner string
		if err != nil || json.Unmarshal(fields["sessionId"], &owner) != nil || owner != session {
			return latest, count, errInventoryShape
		}
		count++
		if raw, present := fields["effort"]; present {
			var level string
			if json.Unmarshal(raw, &level) != nil || level == "" || kirofeature.Normalize(level) != level {
				return latest, count, errInventoryShape
			}
			latest = level
		}
	}
	return latest, count, errInventoryLimit
}

func effortMessageLevels(raw json.RawMessage) map[string]int {
	levels := map[string]int{}
	var value string
	if len(raw) > 64<<10 || json.Unmarshal(raw, &value) != nil {
		return levels
	}
	for word := range strings.FieldsFuncSeq(value, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if normalized := kirofeature.Normalize(word); normalized != "" {
			levels[normalized]++
		}
	}
	return levels
}

type effortNotificationFunc func(context.Context) (acp.Notification, error)

func (f effortNotificationFunc) Next(ctx context.Context) (acp.Notification, error) { return f(ctx) }

func TestEffortMetadataObservationBounds(t *testing.T) {
	for _, test := range []struct {
		name        string
		size, calls int
	}{
		{"event-count", 2, 64}, {"frame-bytes", (64 << 10) + 1, 1}, {"aggregate-bytes", 64 << 10, 17},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			peer := effortNotificationFunc(func(context.Context) (acp.Notification, error) {
				calls++
				return acp.Notification{Method: "fixture/unknown", Params: json.RawMessage(strings.Repeat(" ", test.size))}, nil
			})
			if _, _, err := readEffortMetadata(t.Context(), peer, "owned", time.Second); !errors.Is(err, errInventoryLimit) || calls != test.calls {
				t.Fatal("observation bound failed", err, calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	peer := effortNotificationFunc(func(ctx context.Context) (acp.Notification, error) { return acp.Notification{}, ctx.Err() })
	if _, _, err := readEffortMetadata(ctx, peer, "owned", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal("parent cancellation was treated as a completed observation")
	}
}

type effortCountingCaller struct {
	caller kirofeature.Caller
	calls  int
}

func (c *effortCountingCaller) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.calls++
	if c.calls > 2 {
		return nil, errInventoryLimit
	}
	return c.caller.Call(ctx, method, params)
}

type effortQueryFixture struct {
	calls            int
	selectModel      bool
	apply            bool
	level            string
	readbackOverride string
	rejectSetting    bool
	pending          bool
	silent           bool
	foreign          bool
}

func (f *effortQueryFixture) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.calls++
	raw, _ := json.Marshal(params)
	if f.selectModel && f.calls == 1 {
		if method != "session/set_model" || string(raw) != `{"modelId":"owned-sonnet-4.6","sessionId":"owned-effort-session"}` {
			return nil, errors.New("unexpected independent model selection")
		}
		return json.RawMessage(`{}`), nil
	}
	if f.apply && (f.calls == 3 || f.calls == 4) {
		var input struct {
			Session string `json:"sessionId"`
			Command struct {
				Name string `json:"command"`
				Args struct {
					Value string `json:"value"`
				} `json:"args"`
			} `json:"command"`
		}
		if method != "_kiro.dev/commands/execute" || json.Unmarshal(raw, &input) != nil || input.Session != "owned-effort-session" || input.Command.Name != "effort" || input.Command.Args.Value != map[int]string{3: "high", 4: "low"}[f.calls] {
			return nil, errors.New("independent setting order mismatch")
		}
		f.level = input.Command.Args.Value
		f.pending = !f.silent
		if f.rejectSetting {
			return json.RawMessage(`{"success":false}`), nil
		}
		return json.RawMessage(`{"success":true}`), nil
	}
	if method != "_kiro.dev/commands/execute" || string(raw) != `{"command":{"args":{},"command":"effort"},"sessionId":"owned-effort-session"}` {
		return nil, errors.New("unexpected independent effort query")
	}
	level := f.level
	if level == "" {
		level = "medium"
	}
	if f.readbackOverride != "" {
		level = f.readbackOverride
	}
	result, _ := json.Marshal(map[string]any{"success": true, "message": "Independent setting query: " + level, "output": "private-fixture-text", "data": map[string]any{"current": level, "choices": []string{"low", "high"}}})
	return result, nil
}

func (f *effortQueryFixture) Next(ctx context.Context) (acp.Notification, error) {
	if !f.pending {
		<-ctx.Done()
		return acp.Notification{}, ctx.Err()
	}
	f.pending = false
	level := f.level
	if f.readbackOverride != "" {
		level = f.readbackOverride
	}
	owner := "owned-effort-session"
	if f.foreign {
		owner = "foreign-effort-session"
	}
	raw, _ := json.Marshal(map[string]any{"sessionId": owner, "effort": level})
	return acp.Notification{Method: "_kiro.dev/metadata", Params: raw}, nil
}

func TestEffortWireReadbackRejectsUnconfirmedSettings(t *testing.T) {
	inventory := inventoryReport{session: "owned-effort-session", effortDescriptor: json.RawMessage(`{"name":"effort"}`), sessionModels: json.RawMessage(`{"currentModelId":"auto","availableModels":[{"modelId":"auto"},{"modelId":"owned-sonnet-4.6"}]}`)}
	for _, mode := range []string{"confirmed", "stale", "ambiguous", "rejected", "silent", "foreign", "already-queued"} {
		t.Run(mode, func(t *testing.T) {
			p := &effortWireProbe{selectModel: true, apply: true}
			fixture := &effortQueryFixture{selectModel: true, apply: true}
			if mode == "stale" {
				fixture.readbackOverride = "medium"
			}
			if mode == "ambiguous" {
				fixture.readbackOverride = "high low"
			}
			fixture.rejectSetting = mode == "rejected"
			fixture.silent = mode == "silent" || mode == "already-queued"
			fixture.foreign = mode == "foreign"
			if mode == "already-queued" {
				fixture.level, fixture.pending = "high", true
			}
			err := p.query(t.Context(), fixture, inventory)
			if mode == "confirmed" {
				if err != nil || strings.Join(p.report.Readbacks, ",") != "high,low" || p.report.SettingCalls != 2 || fixture.calls != 4 {
					t.Fatal("finite setting/readback fixture failed", err)
				}
			} else if !errors.Is(err, errInventoryShape) || len(p.report.Readbacks) != 0 || fixture.calls > 4 {
				t.Fatal("unconfirmed effort accepted or setting budget continued", err)
			}
		})
	}
}

func TestEffortWireQueryBudgetAndPrivacy(t *testing.T) {
	inventory := inventoryReport{session: "owned-effort-session", effortDescriptor: json.RawMessage(`{"name":"effort","description":"private-fixture-description","meta":{"arguments":[]}}`)}
	fixture := &effortQueryFixture{}
	for _, invalid := range []inventoryReport{{}, {session: inventory.session}, {effortDescriptor: inventory.effortDescriptor}} {
		p := &effortWireProbe{}
		if !errors.Is(p.query(t.Context(), fixture, invalid), errInventoryShape) || fixture.calls != 0 {
			t.Fatal("invalid query dispatched")
		}
	}
	p := &effortWireProbe{}
	if err := p.query(t.Context(), fixture, inventory); err != nil || !p.report.Success || !p.report.Sent || fixture.calls != 1 {
		t.Fatal("query fixture failed", err)
	}
	encoded, _ := json.Marshal(p.report)
	if strings.Contains(string(encoded), "private-fixture") || strings.Contains(string(encoded), inventory.session) || p.report.ResultShape["result.data.current"] != "string" {
		t.Fatal("query diagnostics leaked values or lost structure")
	}
	if !errors.Is(p.query(t.Context(), fixture, inventory), errInventoryShape) || fixture.calls != 1 {
		t.Fatal("query budget was not enforced")
	}
}

func TestKiroPinnedEffortCommandQuery(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for one owned effort query; no model prompt")
	}
	observeEffortInventory(t, executable, &effortWireProbe{})
}

func TestEffortWireSelectsOnlyAdvertisedModelBeforeQuery(t *testing.T) {
	inventory := inventoryReport{session: "owned-effort-session", effortDescriptor: json.RawMessage(`{"name":"effort"}`), sessionModels: json.RawMessage(`{"currentModelId":"auto","availableModels":[{"modelId":"auto"},{"modelId":"owned-sonnet-4.6"}]}`)}
	p, fixture := &effortWireProbe{selectModel: true}, &effortQueryFixture{selectModel: true}
	if err := p.query(t.Context(), fixture, inventory); err != nil || !p.report.ModelSelected || !p.report.Success || fixture.calls != 2 {
		t.Fatal("selection/query order failed", err)
	}
	inventory.sessionModels = json.RawMessage(`{"currentModelId":"auto","availableModels":[{"modelId":"auto"}]}`)
	p, fixture = &effortWireProbe{selectModel: true}, &effortQueryFixture{selectModel: true}
	if !errors.Is(p.query(t.Context(), fixture, inventory), catalog.ErrModel) || fixture.calls != 0 {
		t.Fatal("unadvertised model was selected")
	}
}

func TestKiroPinnedSelectedModelEffortQuery(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for owned model selection and effort query; no prompt")
	}
	observeEffortInventory(t, executable, &effortWireProbe{selectModel: true})
}

func TestKiroPinnedEffortRoundTrip(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for two owned effort settings/readbacks; no prompt")
	}
	observeEffortInventory(t, executable, &effortWireProbe{selectModel: true, apply: true})
}

func observeEffortInventory(t *testing.T, executable string, probe *effortWireProbe) {
	t.Helper()
	observeSettingsPreservingInventory(t, executable, inventoryVariant{effort: probe})
}

func observeSettingsPreservingInventory(t *testing.T, executable string, variant inventoryVariant) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".kiro", "settings", "cli.json")
	check := func() {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) || err == nil && (!info.Mode().IsRegular() || info.Size() > 1<<20) {
			t.Fatal("cannot bound protected Kiro settings")
		}
	}
	check()
	before := fileFingerprint(t, path)
	defer func() {
		check()
		unchanged := before == fileFingerprint(t, path)
		t.Logf("source_kiro_settings_unchanged=%v", unchanged)
		if !unchanged {
			t.Error("protected Kiro settings changed")
		}
	}()
	observePinnedInventory(t, executable, []string{}, []string{}, "", variant)
}
