package websearch

import (
	"encoding/json"
	"errors"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/projection"
)

// ValidateRequest performs all locally decidable admission before allocating a search owner.
// Model membership remains a property of the actual newly negotiated ACP session catalog.
func ValidateRequest(r *anthropic.Request, spec anthropic.SearchSpec) ([]projection.Text, error) {
	if r == nil || !r.ClientContent() {
		return nil, inference.ErrRequest
	}
	if err := r.ValidateControls(); err != nil {
		return nil, errors.Join(inference.ErrRequest, err)
	}
	declared, matched, err := anthropic.SearchDeclaration(r.Tools)
	if err != nil || !matched || declared != spec {
		return nil, inference.ErrRequest
	}
	for _, message := range r.Messages {
		for _, block := range message.Content {
			if block.Type != "text" {
				return nil, inference.ErrRequest
			}
		}
	}
	disabled, err := r.ToolPolicy()
	if err != nil || disabled {
		return nil, inference.ErrRequest
	}
	prompt, err := projection.Full(r)
	if err != nil {
		return nil, inference.ErrRequest
	}
	encoded, err := json.Marshal(prompt)
	if err != nil || len(encoded) > 512<<10 {
		return nil, inference.ErrRequest
	}
	return prompt, nil
}
