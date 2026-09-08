//go:build !darwin && !linux

package mcp

import (
	"os"

	"dax-kiro-proxy/internal/relay"
)

func cancellableFile(*os.File) (*os.File, error) { return nil, relay.ErrCall }
