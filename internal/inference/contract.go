// Package inference defines the gateway/backend boundary without owning HTTP or subprocesses.
package inference

import (
	"context"
	"dax-kiro-proxy/internal/anthropic"
)

type Kind uint8

const (
	Text Kind = iota + 1
	End
)

type Event struct {
	Kind       Kind
	Text       string
	StopReason string
}
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
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
	Next(context.Context) (Event, error)
	// Finish releases a successfully delivered HTTP response. Tool handoff may leave the ACP turn live.
	Finish()
	// Cancel discards backend state even if model generation already completed.
	Cancel()
}
