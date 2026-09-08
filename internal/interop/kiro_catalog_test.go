package interop_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/catalog"
)

var errInventoryCatalog = errors.New("CLI and ACP catalog identities differ")

// Counts describe validated catalogs, including an ACP current model omitted from its list.
// No model ID, description, session ID or selector ID is retained in this report.
type inventoryCatalogReport struct {
	Decoded, CLIAuto, ACPAuto, CurrentMatches       bool
	Selector                                        string
	CLIEntries, ACPEntries, SharedIDs, AliasMatches int
	OnlyCLI, OnlyACP                                int
}

func compareInventoryCatalog(raw []byte, expected *catalog.Catalog) (inventoryCatalogReport, error) {
	var report inventoryCatalogReport
	if expected == nil || len(expected.Models()) == 0 {
		return report, errInventoryShape
	}
	if len(raw) > catalog.MaxBytes {
		return report, errInventoryLimit
	}
	observed, err := catalog.DecodeSession(raw)
	if err != nil {
		return report, err
	}
	report.Decoded = true
	report.Selector = "legacy"
	if observed.ConfigID != "" {
		report.Selector = "config-option"
	}
	report.CLIEntries, report.ACPEntries = len(expected.Models()), len(observed.Catalog.Models())
	report.CurrentMatches = expected.Current() != "" && expected.Current() == observed.Catalog.Current()
	_, cliAutoErr := expected.Backend("auto")
	_, acpAutoErr := observed.Catalog.Backend("auto")
	report.CLIAuto, report.ACPAuto = cliAutoErr == nil, acpAutoErr == nil
	for _, model := range expected.Models() {
		acpAlias, err := observed.Catalog.ClientID(model.ID)
		if err != nil {
			report.OnlyCLI++
			continue
		}
		report.SharedIDs++
		cliAlias, cliErr := expected.ClientID(model.ID)
		resolved, resolveErr := observed.Catalog.Resolve(cliAlias)
		if cliErr == nil && resolveErr == nil && acpAlias == cliAlias && resolved.ID == model.ID {
			report.AliasMatches++
		}
	}
	report.OnlyACP = report.ACPEntries - report.SharedIDs
	if report.OnlyCLI != 0 || report.OnlyACP != 0 || report.AliasMatches != report.SharedIDs {
		return report, errInventoryCatalog
	}
	return report, nil
}

func inventoryFixtureCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.New([]catalog.Backend{{ID: "auto"}, {ID: "fixture.model"}, {ID: "fixture-model"}}, "auto")
	if err != nil {
		t.Fatal("cannot build independent catalog")
	}
	return c
}

func TestInventoryCatalogComparison(t *testing.T) {
	legacy := `{"sessionId":"private-fixture-session","models":{"currentModelId":"auto","availableModels":[{"modelId":"auto"},{"modelId":"fixture.model"},{"modelId":"fixture-model"}]}}`
	modern := `{"sessionId":"private-fixture-session","configOptions":[{"id":"private-fixture-selector","category":"model","type":"select","currentValue":"auto","options":[{"value":"fixture-model"},{"value":"auto"},{"value":"fixture.model"}]}]}`
	for _, test := range []struct {
		name, raw, selector string
		onlyCLI, onlyACP    int
		auto, current       bool
		wantErr             error
	}{
		{name: "same", raw: legacy, selector: "legacy", auto: true, current: true},
		{name: "public-option-reordered", raw: modern, selector: "config-option", auto: true, current: true},
		{name: "different-current", raw: strings.Replace(legacy, `"currentModelId":"auto"`, `"currentModelId":"fixture.model"`, 1), selector: "legacy", auto: true},
		{name: "selected-omitted", raw: strings.Replace(legacy, `{"modelId":"auto"},`, "", 1), selector: "legacy", auto: true, current: true},
		{name: "missing", raw: strings.Replace(legacy, `,{"modelId":"fixture.model"}`, "", 1), selector: "legacy", onlyCLI: 1, auto: true, current: true, wantErr: errInventoryCatalog},
		{name: "additional", raw: strings.Replace(legacy, `{"modelId":"auto"}`, `{"modelId":"auto"},{"modelId":"private-extra-model"}`, 1), selector: "legacy", onlyACP: 1, auto: true, current: true, wantErr: errInventoryCatalog},
		{name: "missing-auto", raw: strings.ReplaceAll(legacy, `"auto"`, `"private-replacement-model"`), selector: "legacy", onlyCLI: 1, onlyACP: 1, wantErr: errInventoryCatalog},
		{name: "duplicate", raw: strings.ReplaceAll(legacy, "fixture-model", "fixture.model"), wantErr: catalog.ErrCatalog},
		{name: "missing-state", raw: `{"sessionId":"private-fixture-session"}`, wantErr: catalog.ErrCatalog},
		{name: "oversized", raw: legacy + strings.Repeat(" ", catalog.MaxBytes), wantErr: errInventoryLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := compareInventoryCatalog([]byte(test.raw), inventoryFixtureCatalog(t))
			if !errors.Is(err, test.wantErr) || report.Selector != test.selector || report.OnlyCLI != test.onlyCLI || report.OnlyACP != test.onlyACP || report.ACPAuto != test.auto || report.CurrentMatches != test.current {
				t.Fatalf("unexpected catalog comparison: %+v, error=%v", report, err)
			}
			if err == nil && (!report.Decoded || report.CLIEntries != 3 || report.ACPEntries != 3 || report.SharedIDs != 3 || report.AliasMatches != 3 || !report.CLIAuto) {
				t.Fatal("catalog identity and exact alias round trips were not established")
			}
			data, _ := json.Marshal(report)
			if strings.Contains(string(data), "private-") || strings.Contains(string(data), "fixture.model") || strings.Contains(fmt.Sprint(err), "private-") {
				t.Fatal("catalog values entered retained diagnostics")
			}
		})
	}
	for _, invalid := range []*catalog.Catalog{nil, {}} {
		if _, err := compareInventoryCatalog([]byte(legacy), invalid); !errors.Is(err, errInventoryShape) {
			t.Fatal("invalid expected catalog accepted")
		}
	}
	if _, err := readOnlyToolsInventoryAfter(t.Context(), nil, t.TempDir(), time.Second, inventoryPrerequisite{Catalog: &catalog.Catalog{}}); !errors.Is(err, errInventoryShape) {
		t.Fatal("invalid catalog prerequisite reached session dispatch")
	}
}

// Finite CLI discovery and session/new expose catalogs without generating a model response.
// The selected agent has no tools/MCP/resources/hooks. No selection or prompt method is sent.
func TestKiroPinnedCatalogAgreement(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for CLI/ACP catalog comparison; no model prompt")
	}
	observePinnedInventory(t, executable, []string{}, nil, "", inventoryVariant{catalog: true})
}
