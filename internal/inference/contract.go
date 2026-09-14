// Package inference defines the gateway/backend boundary without owning HTTP or subprocesses.
package inference

import (
	"context"
	"dax-kiro-proxy/internal/anthropic"
	"errors"
)

var ErrRequest = errors.New("request is incompatible with the active backend")
var ErrBusy = errors.New("session has an active response")

type Kind uint8

const (
	Text Kind = iota + 1
	End
	Tools
	// Progress carries no content. It satisfies the first-event wait, never the turn deadline.
	Progress
	// Search is a complete server-side web search exchange, never a client tool handoff.
	Search
)

type Event struct {
	Kind       Kind
	Text       string
	StopReason string
	Tools      []anthropic.ToolUse
	Searches   []anthropic.SearchExchange
}
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Object      string `json:"object"`
	OwnedBy     string `json:"owned_by"`
	Created     int64  `json:"created"`
}
type Backend interface {
	Start(context.Context, *anthropic.Request) (Turn, error)
	Models(context.Context) ([]Model, error)
}
type Turn interface {
	Model() string
	// A wait deadline consumes no event and permits another Next call. Cancel owns turn disposal.
	Next(context.Context) (Event, error)
	// Finish releases a successfully delivered HTTP response. Tool handoff may leave the ACP turn live.
	Finish()
	// Cancel discards backend state even if model generation already completed.
	Cancel()
}
