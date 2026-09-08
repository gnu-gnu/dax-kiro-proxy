// Package turnnotice displays queued completion diagnostics through a synchronous client hook.
package turnnotice

import (
	"context"
	"encoding/json"
	"errors"

	"dax-kiro-proxy/internal/status"
	"dax-kiro-proxy/internal/uiclient"
)

func Output(ctx context.Context, path string) (string, error) {
	view, err := uiclient.Read(ctx, path, uiclient.TurnMetrics)
	if err != nil && !errors.Is(err, uiclient.ErrUnavailable) {
		return "", err
	}
	message := ""
	if err == nil {
		message, _ = status.FormatMetrics(view.Body)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	raw, err := json.Marshal(struct {
		Message string `json:"systemMessage,omitempty"`
	}{Message: message})
	if err != nil || len(raw)+1 > status.MaxMetricsOutput {
		return "", status.ErrView
	}
	return string(raw) + "\n", nil
}
