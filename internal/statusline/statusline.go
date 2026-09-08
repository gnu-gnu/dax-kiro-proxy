// Package statusline displays bounded cached status with UI-only authority.
package statusline

import (
	"context"
	"errors"

	"dax-kiro-proxy/internal/status"
	"dax-kiro-proxy/internal/uiclient"
)

type Config = uiclient.Config

var ErrConfig = uiclient.ErrConfig

func EncodeConfig(cfg Config) ([]byte, error) { return uiclient.EncodeConfig(cfg) }

func Display(ctx context.Context, path string) (string, error) {
	view, err := uiclient.Read(ctx, path, uiclient.Usage)
	if err != nil {
		if !errors.Is(err, uiclient.ErrUnavailable) {
			return "", err
		}
		return "Kiro | status unavailable\n", nil
	}
	line, err := status.FormatView(view.Body, view.Model)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "Kiro | status unavailable\n", nil
	}
	return line, nil
}
