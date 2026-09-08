package launcher

import (
	"context"
	"encoding/json"
	"math"
	"unicode"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/ndjson"
)

// ReadKiroCatalog uses the pinned CLI's public listing command. It does not create an ACP session
// or submit a model prompt. Account preflight, cache identity and later ACP compatibility checks
// remain separate responsibilities of the caller.
func ReadKiroCatalog(ctx context.Context, runner CommandRunner, cfg KiroConfig) (*catalog.Catalog, error) {
	_, command, err := checkedKiroCommand(ctx, runner, cfg)
	if err != nil {
		return nil, err
	}
	command.Args = []string{"chat", "--list-models", "--format", "json"}
	result, err := runner.Run(ctx, command)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || result.ExitCode != 0 {
		return nil, catalog.ErrCatalog
	}
	return decodeKiroListing(result.Stdout)
}

func decodeKiroListing(raw []byte) (*catalog.Catalog, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return nil, catalog.ErrCatalog
	}
	root, err := ndjson.Object(raw)
	if err != nil || len(root) > 16 {
		return nil, catalog.ErrCatalog
	}
	text := func(raw json.RawMessage, out *string) bool {
		return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, out) == nil
	}
	var current string
	if !text(root["default_model"], &current) || current == "" {
		return nil, catalog.ErrCatalog
	}
	var entries []json.RawMessage
	list := root["models"]
	if len(list) == 0 || list[0] != '[' || json.Unmarshal(list, &entries) != nil || len(entries) == 0 || len(entries) > catalog.MaxModels {
		return nil, catalog.ErrCatalog
	}
	models := make([]catalog.Backend, 0, len(entries))
	currentListed := false
	for _, entry := range entries {
		fields, err := ndjson.Object(entry)
		if err != nil || len(fields) > 16 {
			return nil, catalog.ErrCatalog
		}
		var model catalog.Backend
		if !text(fields["model_id"], &model.ID) {
			return nil, catalog.ErrCatalog
		}
		for _, field := range []struct {
			key    string
			target *string
		}{{"model_name", &model.Name}, {"description", &model.Description}} {
			if value, ok := fields[field.key]; ok && !text(value, field.target) {
				return nil, catalog.ErrCatalog
			}
		}
		if value, ok := fields["rate_multiplier"]; ok && string(value) != "null" {
			var number float64
			if len(value) == 0 || (value[0] != '-' && (value[0] < '0' || value[0] > '9')) || json.Unmarshal(value, &number) != nil || math.IsInf(number, 0) || math.IsNaN(number) || number < 0 || number > 1000 {
				return nil, catalog.ErrCatalog
			}
		}
		if value, ok := fields["rate_unit"]; ok && string(value) != "null" {
			var unit string
			if !text(value, &unit) || len(unit) > 64 {
				return nil, catalog.ErrCatalog
			}
			for _, r := range unit {
				if unicode.IsControl(r) {
					return nil, catalog.ErrCatalog
				}
			}
		}
		// The observed numeric rate has no verified credit-unit contract yet. Neither it nor
		// context_window_tokens may become provider billing, a tokenizer, or negotiated ACP support.
		currentListed = currentListed || model.ID == current
		models = append(models, model)
	}
	// Unlike an ACP session's explicitly selected current model, a CLI default alone is not evidence
	// of a missing advertised model. Never synthesize a catalog entry from this field.
	if !currentListed {
		return nil, catalog.ErrCatalog
	}
	return catalog.New(models, current)
}
