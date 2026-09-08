package session

import (
	"context"

	"dax-kiro-proxy/internal/toolregistry"
)

// LaunchInput exposes only the validated current tool registry and its owned, initially closed relay.
// Conversation content, HTTP credentials and request identity never enter process configuration.
type LaunchInput struct {
	Registry                     *toolregistry.Registry
	RelayExecutable, RelayConfig string
}

// LaunchResources adds arguments and an optional process cwd to the fixed transport configuration.
// The ACP session cwd remains the original project. RelayAtLaunch avoids adding a second MCP server
// when an independently verified launch profile already binds the exact supplied relay.
type LaunchResources struct {
	Args          []string
	Directory     string
	RelayAtLaunch bool
	Cleanup       func() error
}

// A preparer must honor its setup context and return partial resources on error. Cleanup is called
// once after the process group joins, shielded from caller cancellation; it must have a finite bound.
// This extension is not a restriction proof and supplies no permission to use an unverified Kiro agent.
type PrepareLaunch func(context.Context, LaunchInput) (LaunchResources, error)
