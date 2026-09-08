package launcher_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/launcher"
)

const independentModels = `{"default_model":"independent-a","models":[{"model_id":"independent-a","model_name":"Independent A","description":"First synthetic choice","context_window_tokens":128000,"rate_multiplier":1.5,"rate_unit":"unverified-unit"},{"model_id":"independent-b","model_name":"Independent B","description":"Second synthetic choice"}]}`

func TestReadOnlyKiroCatalogPinsVersionsAndMapsOnlyAdvertisedModels(t *testing.T) {
	f := &preflightFixture{identity: independentModels}
	cfg := launcher.KiroConfig{Executable: "/fixture/bin/kiro-cli", Home: t.TempDir(), Directory: t.TempDir()}
	c, err := launcher.ReadKiroCatalog(t.Context(), f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 || strings.Join(f.calls[2].Args, " ") != "chat --list-models --format json" {
		t.Fatal("catalog discovery started an interactive or model command")
	}
	for _, command := range f.calls {
		env := envMap(command.Environment)
		if len(env) != 5 || env["HOME"] != cfg.Home || env["PATH"] != "/fixture/bin:/usr/bin:/bin:/usr/sbin:/sbin" {
			t.Fatal("catalog discovery inherited an uncontrolled environment")
		}
	}
	if c.Current() != "independent-a" || len(c.List()) != 2 {
		t.Fatal("wrong advertised catalog/default")
	}
	for _, id := range []string{"independent-a", "independent-b"} {
		alias, err := c.ClientID(id)
		if err != nil || !strings.HasPrefix(alias, "claude-dax-") {
			t.Fatal("catalog alias was not derived")
		}
		backend, err := c.Resolve(alias)
		if err != nil || backend.ID != id || backend.Multiplier != nil {
			t.Fatal("catalog changed a model or claimed unverified credit units")
		}
	}
	if c.List()[0].Description != "Kiro: First synthetic choice" {
		t.Fatal("compatible catalog description was lost")
	}
}

func TestReadOnlyKiroCatalogRejectsAmbiguousOrUnsafeMetadata(t *testing.T) {
	cfg := launcher.KiroConfig{Executable: "/fixture/bin/kiro-cli", Home: t.TempDir(), Directory: t.TempDir()}
	for _, body := range []string{
		`[]`, `null`, `{}`, independentModels + "\n{}", independentModels + "\npostamble",
		`{"default_model":"independent-a","models":[]}`,
		strings.Replace(independentModels, `"default_model":"independent-a"`, `"default_model":"not-advertised"`, 1),
		strings.Replace(independentModels, `"model_id":"independent-b"`, `"model_id":"independent-a"`, 1),
		strings.Replace(independentModels, `"model_id":"independent-b"`, `"model_id":"contains space"`, 1),
		strings.Replace(independentModels, `"model_name":"Independent A"`, `"model_name":false`, 1),
		strings.Replace(independentModels, `"description":"First synthetic choice"`, `"description":"escape\u001bsequence"`, 1),
		strings.Replace(independentModels, `"model_id":"independent-a"`, `"model_id":"independent-a","model_id":"duplicate-key"`, 1),
		strings.Replace(independentModels, `"rate_multiplier":1.5`, `"rate_multiplier":-1`, 1),
		strings.Replace(independentModels, `"rate_multiplier":1.5`, `"rate_multiplier":1e999`, 1),
		strings.Replace(independentModels, `"rate_unit":"unverified-unit"`, `"rate_unit":{}`, 1),
		strings.Repeat(" ", 64<<10) + independentModels,
	} {
		f := &preflightFixture{identity: body}
		if _, err := launcher.ReadKiroCatalog(t.Context(), f, cfg); !errors.Is(err, catalog.ErrCatalog) {
			t.Fatal("invalid listing accepted or exposed raw diagnostics")
		}
	}
	f := &preflightFixture{identity: independentModels, version: "unverified"}
	if _, err := launcher.ReadKiroCatalog(t.Context(), f, cfg); !errors.Is(err, launcher.ErrKiroVersion) || len(f.calls) != 1 {
		t.Fatal("unverified CLI reached model listing")
	}
	f = &preflightFixture{identity: independentModels, failure: errors.New("synthetic-secret")}
	if _, err := launcher.ReadKiroCatalog(t.Context(), f, cfg); !errors.Is(err, catalog.ErrCatalog) || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("CLI listing error disclosed raw failure details")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f = &preflightFixture{identity: independentModels}
	if _, err := launcher.ReadKiroCatalog(ctx, f, cfg); !errors.Is(err, context.Canceled) || len(f.calls) != 0 {
		t.Fatal("canceled catalog discovery ran a command")
	}
}
