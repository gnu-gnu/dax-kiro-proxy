// Package startupnotice emits a display-only client hook response, never model context or a turn.
package startupnotice

import (
	"context"
	"encoding/json"
	"errors"

	"dax-kiro-proxy/internal/status"
	"dax-kiro-proxy/internal/uiclient"
)

func Output(ctx context.Context, path string) (string, error) {
	view, err := uiclient.Read(ctx, path, uiclient.ModelCapabilities)
	if err != nil && !errors.Is(err, uiclient.ErrUnavailable) {
		return "", err
	}
	message := "Kiro model capabilities unavailable."
	if err == nil {
		if text, err := status.FormatNotice(view.Body, view.Model); err == nil {
			message = text
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	raw, err := json.Marshal(struct {
		Message string `json:"systemMessage"`
	}{Message: message})
	if err != nil || len(raw) > 1023 {
		return "", status.ErrView
	}
	return string(raw) + "\n", nil
}
