// Package relay suspends effect-free tool requests until the owning client returns their results.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"dax-kiro-proxy/internal/jsoncanon"
	"dax-kiro-proxy/internal/schemawire"
	"dax-kiro-proxy/internal/toolregistry"
)

const MaxFrameBytes = 4 << 20

var ErrAuth = errors.New("relay authentication rejected")
var ErrCall = errors.New("invalid or repeated relay call")
var ErrResults = errors.New("tool results do not match the delivered batch")
var ErrCapacity = errors.New("relay capacity reached")
var ErrTimeout = errors.New("client tool result deadline exceeded")
var ErrClosed = errors.New("session relay closed")
var ErrBatch = errors.New("tool batch is not ready")

type Credentials struct {
	Owner  string `json:"owner"`
	Secret string `json:"secret"`
}
type Call struct {
	Version   int             `json:"version"`
	Owner     string          `json:"owner"`
	Secret    string          `json:"secret"`
	ID        string          `json:"callId"`
	Alias     string          `json:"alias"`
	Arguments json.RawMessage `json:"arguments"`
}
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

func (c Content) MarshalJSON() ([]byte, error) {
	if c.Type == "text" {
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{"text", c.Text})
	}
	if c.Type == "image" {
		return json.Marshal(struct {
			Type string `json:"type"`
			Data string `json:"data"`
			MIME string `json:"mimeType"`
		}{"image", c.Data, c.MIMEType})
	}
	return nil, ErrResults
}

type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}
type Result struct {
	ID string
	ToolResult
}
type Use struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}
type Batch struct {
	Number uint64
	Calls  []Use
}
type Limits struct {
	Pending, PendingBytes, CallHistory int
	ToolTimeout                        time.Duration
}
type Stats struct{ Pending, Queued, Sealed, Seen int }
type pendingCall struct {
	use   Use
	done  chan ToolResult
	timer *time.Timer
	bytes int
}
type Broker struct {
	mu              sync.Mutex
	registry        *toolregistry.Registry
	credentials     Credentials
	limits          Limits
	pending         map[string]*pendingCall
	seen            map[string]bool
	queued, sealed  []string
	reserved, bytes int
	number          uint64
	delivered       bool
	active          bool
	wake            chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	err             error
}

func NewBroker(registry *toolregistry.Registry, limits Limits) (*Broker, error) {
	if limits.Pending == 0 {
		limits.Pending = 64
	}
	if limits.PendingBytes == 0 {
		limits.PendingBytes = 16 << 20
	}
	if limits.CallHistory == 0 {
		limits.CallHistory = 4096
	}
	if limits.ToolTimeout == 0 {
		limits.ToolTimeout = 5 * time.Minute
	}
	if registry == nil || limits.Pending < 1 || limits.Pending > 64 || limits.PendingBytes < schemawire.MaxArgumentBytes || limits.PendingBytes > 32<<20 || limits.CallHistory < 1 || limits.CallHistory > 65536 || limits.ToolTimeout <= 0 || limits.ToolTimeout > time.Hour {
		return nil, ErrCall
	}
	owner, err := randomID(16)
	if err != nil {
		return nil, err
	}
	secret, err := randomID(32)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Broker{registry: registry, credentials: Credentials{owner, secret}, limits: limits, pending: make(map[string]*pendingCall), seen: make(map[string]bool), wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}, nil
}
func (b *Broker) Credentials() Credentials         { return b.credentials }
func (b *Broker) Registry() *toolregistry.Registry { return b.registry }
func (b *Broker) Done() <-chan struct{}            { return b.ctx.Done() }
func (b *Broker) Ready() <-chan struct{}           { return b.wake }
func (b *Broker) Err() error                       { b.mu.Lock(); defer b.mu.Unlock(); return b.err }
func (b *Broker) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Stats{len(b.pending) + b.reserved, len(b.queued), len(b.sealed), len(b.seen)}
}
func (b *Broker) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// BeginTurn permits calls only while the session owns an ACP prompt. A new broker starts idle.
func (b *Broker) BeginTurn() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return ErrClosed
	}
	if b.active || len(b.pending)+b.reserved != 0 {
		return ErrCall
	}
	b.active = true
	return nil
}

// EndTurn closes admission atomically with checking that no tool is still suspended or validating.
// Completion with unfinished tool state is ambiguous and permanently retires this relay.
func (b *Broker) EndTurn() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return ErrClosed
	}
	if !b.active {
		return ErrCall
	}
	b.active = false
	if len(b.pending)+b.reserved != 0 {
		b.failLocked(ErrCall)
		return ErrCall
	}
	return nil
}

func (b *Broker) Call(ctx context.Context, call Call) (ToolResult, error) {
	if call.Owner != b.credentials.Owner || subtle.ConstantTimeCompare([]byte(call.Secret), []byte(b.credentials.Secret)) != 1 {
		return ToolResult{}, ErrAuth
	}
	if call.Version != 1 || !validCallID(call.ID) {
		return ToolResult{}, ErrCall
	}
	if _, ok := b.registry.Lookup(call.Alias); !ok {
		return ToolResult{}, ErrCall
	}
	if _, err := schemawire.Arguments(call.Arguments); err != nil {
		return ToolResult{}, ErrCall
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	b.mu.Lock()
	if b.err != nil {
		b.mu.Unlock()
		return ToolResult{}, ErrClosed
	}
	if !b.active {
		b.mu.Unlock()
		return ToolResult{}, ErrCall
	}
	if len(b.pending)+b.reserved >= b.limits.Pending {
		b.mu.Unlock()
		return ToolResult{}, ErrCapacity
	}
	if b.seen[call.ID] {
		b.mu.Unlock()
		return ToolResult{}, ErrCall
	}
	if len(b.seen) >= b.limits.CallHistory {
		b.mu.Unlock()
		b.fail(ErrCapacity)
		return ToolResult{}, ErrCapacity
	}
	b.seen[call.ID] = true
	b.reserved++
	b.mu.Unlock()
	validating, stop := context.WithCancel(ctx)
	unlink := context.AfterFunc(b.ctx, stop)
	tool, err := b.registry.Validate(validating, call.Alias, call.Arguments)
	unlink()
	stop()
	input, canonicalErr := jsoncanon.Object(call.Arguments)
	b.mu.Lock()
	b.reserved--
	defer b.mu.Unlock()
	if b.err != nil {
		return ToolResult{}, ErrClosed
	}
	if err != nil || canonicalErr != nil || len(input) > schemawire.MaxArgumentBytes {
		return ToolResult{}, ErrCall
	}
	if ctx.Err() != nil {
		return ToolResult{}, ctx.Err()
	}
	id, err := randomID(24)
	if err != nil {
		return ToolResult{}, ErrCall
	}
	use := Use{ID: "toolu_" + id, Name: tool.Name, Input: input}
	encoded, _ := json.Marshal(use)
	if b.bytes+len(encoded) > b.limits.PendingBytes {
		return ToolResult{}, ErrCapacity
	}
	p := &pendingCall{use: use, done: make(chan ToolResult, 1), bytes: len(encoded)}
	b.pending[use.ID] = p
	b.queued = append(b.queued, use.ID)
	b.bytes += p.bytes
	p.timer = time.AfterFunc(b.limits.ToolTimeout, func() { b.expire(use.ID, p) })
	b.signal()
	b.mu.Unlock()
	var value ToolResult
	select {
	case value = <-p.done:
	case <-ctx.Done():
		b.fail(context.Canceled)
		value = <-p.done
	}
	b.mu.Lock()
	return value, nil
}

// Seal snapshots only calls included in this response. Later calls remain queued for another batch.
func (b *Broker) Seal() (Batch, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return Batch{}, ErrClosed
	}
	if len(b.sealed) != 0 || len(b.queued) == 0 {
		return Batch{}, ErrBatch
	}
	b.number++
	batch := Batch{Number: b.number}
	bytes := 0
	for _, id := range b.queued {
		p := b.pending[id]
		if bytes+p.bytes > MaxFrameBytes-1024 {
			break
		}
		bytes += p.bytes
		use := p.use
		use.Input = append(json.RawMessage(nil), use.Input...)
		batch.Calls = append(batch.Calls, use)
		b.sealed = append(b.sealed, id)
	}
	if len(b.sealed) == 0 {
		return Batch{}, ErrCapacity
	}
	b.queued = append([]string(nil), b.queued[len(b.sealed):]...)
	b.delivered = false
	return batch, nil
}
func (b *Broker) Delivered(number uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil || number != b.number || len(b.sealed) == 0 || b.delivered {
		return ErrBatch
	}
	b.delivered = true
	return nil
}

// Resolve validates and encodes the entire result set before completing any suspended call.
func (b *Broker) Resolve(owner string, results []Result) error {
	if owner != b.credentials.Owner || len(results) == 0 || len(results) > b.limits.Pending {
		return ErrResults
	}
	prepared := make(map[string]ToolResult, len(results))
	bytes := 0
	for _, result := range results {
		if _, exists := prepared[result.ID]; exists {
			return ErrResults
		}
		value, err := validateResult(result.ToolResult)
		if err != nil {
			return ErrResults
		}
		encoded, _ := json.Marshal(value)
		bytes += len(encoded)
		if len(encoded) > MaxFrameBytes-1024 || bytes > 16<<20 {
			return ErrResults
		}
		prepared[result.ID] = value
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil || !b.delivered || len(b.sealed) != len(prepared) {
		return ErrResults
	}
	for _, id := range b.sealed {
		if _, ok := prepared[id]; !ok {
			return ErrResults
		}
	}
	for _, id := range b.sealed {
		p := b.pending[id]
		p.timer.Stop()
		p.done <- prepared[id]
		b.bytes -= p.bytes
		delete(b.pending, id)
	}
	b.sealed = nil
	b.delivered = false
	if len(b.queued) > 0 {
		b.signal()
	}
	return nil
}
func (b *Broker) Close() { b.fail(ErrClosed) }

func (b *Broker) expire(id string, call *pendingCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Stop does not join a timer callback that already started. Only its still-pending call can expire.
	if b.pending[id] == call {
		b.failLocked(ErrTimeout)
	}
}
func (b *Broker) fail(reason error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failLocked(reason)
}
func (b *Broker) failLocked(reason error) {
	if b.err != nil {
		return
	}
	b.err = reason
	b.cancel()
	message := "Client tool request cancelled"
	if errors.Is(reason, ErrTimeout) {
		message = "Client tool result timed out"
	}
	for id, p := range b.pending {
		p.timer.Stop()
		p.done <- ToolResult{Content: []Content{{Type: "text", Text: message}}, IsError: true}
		delete(b.pending, id)
	}
	b.queued = nil
	b.sealed = nil
	b.bytes = 0
	b.delivered = false
	b.signal()
}
func validateResult(result ToolResult) (ToolResult, error) {
	if len(result.Content) > 4096 {
		return ToolResult{}, ErrResults
	}
	copyResult := ToolResult{Content: make([]Content, len(result.Content)), IsError: result.IsError}
	bytes := 0
	for i, c := range result.Content {
		bytes += len(c.Text) + len(c.Data)
		if bytes > MaxFrameBytes-1024 {
			return ToolResult{}, ErrResults
		}
		switch c.Type {
		case "text":
			if c.Data != "" || c.MIMEType != "" {
				return ToolResult{}, ErrResults
			}
		case "image":
			if c.Text != "" || c.Data == "" {
				return ToolResult{}, ErrResults
			}
			switch c.MIMEType {
			case "image/png", "image/jpeg", "image/gif", "image/webp":
			default:
				return ToolResult{}, ErrResults
			}
			if strings.ContainsAny(c.Data, "\r\n") {
				return ToolResult{}, ErrResults
			}
			if _, err := base64.StdEncoding.Strict().DecodeString(c.Data); err != nil {
				return ToolResult{}, ErrResults
			}
		default:
			return ToolResult{}, ErrResults
		}
		copyResult.Content[i] = c
	}
	return copyResult, nil
}
func randomID(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", ErrCall
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// NewCallID creates an independent relay request identity without reusing a client's JSON-RPC ID.
func NewCallID() (string, error) { return randomID(24) }
func validCallID(id string) bool {
	if len(id) < 1 || len(id) > 96 {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
