// Package acp implements public ACP version 1 without executing agent-requested effects.
package acp

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrProtocol       = errors.New("ACP protocol failure")
	ErrTransport      = errors.New("ACP transport failure")
	ErrFrameTooLarge  = errors.New("ACP frame exceeds byte limit")
	ErrTimeout        = errors.New("ACP deadline exceeded")
	ErrAuthentication = errors.New("Kiro authentication expired; run kiro-cli login")
	ErrOverloaded     = errors.New("ACP resource limit exceeded")
	ErrClosed         = errors.New("ACP process is closed")
	ErrParameters     = errors.New("invalid ACP request parameters")
)

type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}
type MCPCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}
type Capabilities struct {
	LoadSession bool               `json:"loadSession"`
	Prompt      PromptCapabilities `json:"promptCapabilities"`
	MCP         MCPCapabilities    `json:"mcpCapabilities"`
}
type Notification struct {
	Method    string
	SessionID string
	Params    json.RawMessage
	size      int
}
type Diagnostics struct {
	StderrLines    int
	StderrBytes    int
	TruncatedLines uint64
}
type AuthClassifier interface {
	Error(int, string, json.RawMessage) bool
	Stderr([]byte) bool
}
type Config struct {
	Executable string
	Args       []string
	Directory  string
	// Environment is a complete caller-allowlisted environment; nil inherits nothing.
	Environment []string
	ClientInfo  Info
	Limits      Limits
	Auth        AuthClassifier
}
type Limits struct {
	FrameBytes     int
	Pending        int
	EventQueue     int
	EventBytes     int
	WriteQueue     int
	WriteBytes     int
	RequestTimeout time.Duration
	WriteTimeout   time.Duration
	CancelTimeout  time.Duration
	GracePeriod    time.Duration
	TermPeriod     time.Duration
	KillPeriod     time.Duration
}

func (l Limits) normalized() (Limits, error) {
	integers := []struct {
		value    *int
		def, max int
	}{{&l.FrameBytes, 8 << 20, 32 << 20}, {&l.Pending, 64, 1024}, {&l.EventQueue, 64, 1024}, {&l.EventBytes, 16 << 20, 64 << 20}, {&l.WriteQueue, 64, 1024}, {&l.WriteBytes, 16 << 20, 64 << 20}}
	for _, p := range integers {
		if *p.value == 0 {
			*p.value = p.def
		}
		if *p.value < 1 || *p.value > p.max {
			return l, ErrParameters
		}
	}
	durations := []struct {
		value    *time.Duration
		def, max time.Duration
	}{{&l.RequestTimeout, 30 * time.Second, 24 * time.Hour}, {&l.WriteTimeout, 5 * time.Second, 30 * time.Second}, {&l.CancelTimeout, 500 * time.Millisecond, 5 * time.Second}, {&l.GracePeriod, time.Second, 5 * time.Second}, {&l.TermPeriod, time.Second, 5 * time.Second}, {&l.KillPeriod, time.Second, 5 * time.Second}}
	for _, p := range durations {
		if *p.value == 0 {
			*p.value = p.def
		}
		if *p.value <= 0 || *p.value > p.max {
			return l, ErrParameters
		}
	}
	return l, nil
}

type RemoteError struct{ Code int }

func (e *RemoteError) Error() string { return fmt.Sprintf("ACP request rejected (code %d)", e.Code) }
