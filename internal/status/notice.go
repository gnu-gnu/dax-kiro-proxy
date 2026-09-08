package status

import (
	"encoding/json"

	"dax-kiro-proxy/internal/ndjson"
)

// ModelNotice describes the prepared launch and the currently implemented adapter. No ACP session
// exists on this path, so negotiated media/effort support cannot be asserted for the selected model.
type ModelNotice struct {
	Version            int    `json:"version"`
	Model              string `json:"model"`
	ImageInput         string `json:"image_input"`
	PDFInput           string `json:"pdf_input"`
	NativeWebSearch    string `json:"native_web_search"`
	Effort             string `json:"effort"`
	ClientTools        string `json:"client_tools"`
	ProviderTokenUsage string `json:"provider_token_usage"`
}

func LaunchNotice(model string) (ModelNotice, error) {
	if _, err := ModelLabel(model); err != nil {
		return ModelNotice{}, err
	}
	return ModelNotice{Version: 1, Model: model, ImageInput: "unknown", PDFInput: "unknown", NativeWebSearch: "unsupported", Effort: "unknown", ClientTools: "client_permissions", ProviderTokenUsage: "unreported"}, nil
}

func FormatNotice(raw []byte, model string) (string, error) {
	expected, err := LaunchNotice(model)
	if err != nil || len(raw) > 64<<10 {
		return "", ErrView
	}
	fields, err := ndjson.Object(raw)
	if err != nil || len(fields) != 8 {
		return "", ErrView
	}
	for _, key := range []string{"version", "model", "image_input", "pdf_input", "native_web_search", "effort", "client_tools", "provider_token_usage"} {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			return "", ErrView
		}
	}
	var got ModelNotice
	if json.Unmarshal(raw, &got) != nil || got != expected {
		return "", ErrView
	}
	label, _ := ModelLabel(model)
	return "Kiro launch " + label + ". Tools follow client permissions. Image/PDF and effort support: unverified. Native web search: unavailable. Provider token usage: unreported.", nil
}
