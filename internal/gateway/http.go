// Package gateway serves the authenticated local Messages and status contracts.
package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/status"
)

const AuthFallbackHeader = "X-Dax-Kiro-Proxy-Auth-Fallback"
const loginMessage = "Kiro authentication expired. Run `kiro-cli login` and retry this request."

var errOutputLimit = errors.New("response exceeds output limit")

type Tokens struct{ Model, UI string }

func NewTokens() (Tokens, error) {
	m, err := randomID(32)
	if err != nil {
		return Tokens{}, err
	}
	u, err := randomID(32)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{m, u}, nil
}

func randomID(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type Config struct {
	Tokens            Tokens
	Backend           inference.Backend
	Usage             *status.UsageCache
	Metrics           *status.TurnQueue
	LaunchModel       string
	FirstEventTimeout time.Duration
	TurnTimeout       time.Duration
	WriteTimeout      time.Duration
	ReadTimeout       time.Duration
	KeepAliveInterval time.Duration
	MaxActiveRequests int
	MaxOutputBytes    int
}

type Handler struct {
	cfg      Config
	active   chan struct{}
	uiActive chan struct{}
}

func New(cfg Config) (*Handler, error) {
	if cfg.LaunchModel != "" {
		if _, err := status.LaunchNotice(cfg.LaunchModel); err != nil {
			return nil, errors.New("invalid prepared startup model")
		}
	}
	if cfg.Backend == nil || !validToken(cfg.Tokens.Model) || !validToken(cfg.Tokens.UI) || cfg.Tokens.Model == cfg.Tokens.UI {
		return nil, errors.New("gateway requires a backend and two distinct 256-bit tokens")
	}
	if cfg.FirstEventTimeout == 0 {
		cfg.FirstEventTimeout = 90 * time.Second
	}
	if cfg.TurnTimeout == 0 {
		cfg.TurnTimeout = 10 * time.Minute
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 15 * time.Second
	}
	if cfg.KeepAliveInterval == 0 {
		cfg.KeepAliveInterval = 15 * time.Second
	}
	if cfg.MaxActiveRequests == 0 {
		cfg.MaxActiveRequests = 16
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = 16 << 20
	}
	if cfg.FirstEventTimeout <= 0 || cfg.FirstEventTimeout > cfg.TurnTimeout || cfg.TurnTimeout > time.Hour ||
		cfg.WriteTimeout <= 0 || cfg.WriteTimeout > time.Minute || cfg.ReadTimeout <= 0 || cfg.ReadTimeout > time.Minute ||
		cfg.KeepAliveInterval <= 0 || cfg.KeepAliveInterval > time.Minute ||
		cfg.MaxActiveRequests < 1 || cfg.MaxActiveRequests > 256 || cfg.MaxOutputBytes < 1024 || cfg.MaxOutputBytes > 64<<20 {
		return nil, errors.New("invalid gateway resource limits")
	}
	return &Handler{cfg: cfg, active: make(chan struct{}, cfg.MaxActiveRequests), uiActive: make(chan struct{}, cfg.MaxActiveRequests)}, nil
}

func validToken(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func authorized(r *http.Request, expected string) bool {
	keys, auth := r.Header.Values("x-api-key"), r.Header.Values("Authorization")
	if len(keys) > 1 || len(auth) > 1 {
		return false
	}
	key := ""
	if len(keys) == 1 {
		key = keys[0]
		if key == "" {
			return false
		}
	}
	if len(auth) == 1 {
		kind, value, ok := strings.Cut(auth[0], " ")
		if !ok || !strings.EqualFold(kind, "Bearer") || value == "" || strings.ContainsAny(value, " \t\r\n") {
			return false
		}
		if key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(value)) != 1 {
			return false
		}
		key = value
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(expected)) == 1
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An exact table avoids path-cleaning redirects and prefix-based credential scope.
	var credential string
	switch r.Method + " " + r.URL.EscapedPath() {
	case "GET /health":
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	case "POST /v1/messages", "POST /messages", "GET /v1/models":
		credential = h.cfg.Tokens.Model
	case "GET /dax-kiro-proxy/status/usage", "POST /dax-kiro-proxy/hooks/turn-metrics", "POST /dax-kiro-proxy/hooks/model-capabilities":
		credential = h.cfg.Tokens.UI
	default:
		writeError(w, 404, "not_found_error", "Route not found")
		return
	}
	if !authorized(r, credential) {
		writeError(w, 401, "authentication_error", "Invalid gateway credential")
		return
	}
	if credential == h.cfg.Tokens.UI {
		h.ui(w, r)
		return
	}
	select {
	case h.active <- struct{}{}:
		defer func() { <-h.active }()
	default:
		writeError(w, 429, "overloaded_error", "Gateway request capacity reached")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.TurnTimeout)
	defer cancel()
	if r.Method == "GET" {
		models, err := h.cfg.Backend.Models(ctx)
		if err != nil {
			writeError(w, 502, "api_error", "Kiro model catalog unavailable")
			return
		}
		writeJSON(w, 200, struct {
			Object string            `json:"object"`
			Data   []inference.Model `json:"data"`
		}{"list", models})
		return
	}
	h.messages(ctx, w, r)
}

func (h *Handler) messages(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if kind := r.Header.Get("Content-Type"); kind != "" {
		media, _, err := mime.ParseMediaType(kind)
		if err != nil || media != "application/json" {
			writeError(w, 400, "invalid_request_error", "Expected application/json")
			return
		}
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(h.cfg.ReadTimeout))
	reader := http.MaxBytesReader(w, r.Body, anthropic.MaxBodyBytes)
	body, err := io.ReadAll(reader)
	_ = controller.SetReadDeadline(time.Time{})
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			writeError(w, 413, "request_too_large", "Messages body exceeds 16 MiB")
		} else {
			writeError(w, 400, "invalid_request_error", "Cannot read Messages body")
		}
		return
	}
	request, err := anthropic.DecodeRequest(body)
	if err != nil {
		message := "Invalid Messages request"
		var control *anthropic.UnsupportedControlError
		if errors.As(err, &control) {
			message = control.Error()
		}
		writeError(w, 400, "invalid_request_error", message)
		return
	}
	request.Identity, err = clientIdentity(r.Header)
	if err != nil {
		writeError(w, 400, "invalid_request_error", "Invalid client conversation identifier")
		return
	}
	if !request.ClientContent() {
		writeError(w, 400, "invalid_request_error", "Content or tools are unsupported by the current adapter")
		return
	}
	if _, err := request.ToolPolicy(); err != nil {
		writeError(w, 400, "invalid_request_error", "Unsupported tool choice restriction")
		return
	}
	id, err := randomID(18)
	if err != nil {
		writeError(w, 500, "api_error", "Cannot create response identifier")
		return
	}
	output := &responseWriter{w: w, controller: controller, timeout: h.cfg.WriteTimeout, remaining: h.cfg.MaxOutputBytes}
	turn, err := h.cfg.Backend.Start(ctx, request)
	if err != nil {
		var control *anthropic.UnsupportedControlError
		if errors.As(err, &control) {
			writeError(w, 400, "invalid_request_error", control.Error())
		} else if errors.Is(err, inference.ErrRequest) || errors.Is(err, anthropic.ErrRequest) {
			writeError(w, 400, "invalid_request_error", "Requested model or content is incompatible with the current Kiro catalog")
		} else if errors.Is(err, inference.ErrBusy) {
			writeError(w, 409, "invalid_request_error", "This conversation already has an active response")
		} else if errors.Is(err, acp.ErrOverloaded) {
			writeError(w, 429, "overloaded_error", "Kiro process or session capacity reached")
		} else if errors.Is(err, acp.ErrAuthentication) {
			h.fallback(output, request.Stream, "msg_"+id, request.Model, nil, "")
		} else {
			writeError(w, 502, "api_error", failureMessage(err, false, ctx))
		}
		return
	}
	if turn == nil {
		writeError(w, 502, "api_error", "Kiro backend did not start a turn")
		return
	}
	finished := false
	defer func() {
		if finished {
			turn.Finish()
		} else {
			turn.Cancel()
		}
	}()
	firstCtx, stopFirst := context.WithTimeout(ctx, h.cfg.FirstEventTimeout)
	defer stopFirst()
	var stream *anthropic.TextStream
	beginStream := func() error {
		if stream != nil {
			return nil
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Trailer", AuthFallbackHeader)
		var err error
		stream, err = anthropic.BeginStream(output, output.flush, "msg_"+id, turn.Model())
		return err
	}
	var buffered strings.Builder
	var blocks []anthropic.ResponseBlock
	var pendingTools []anthropic.ToolUse
	flushText := func() {
		if buffered.Len() > 0 {
			text := buffered.String()
			blocks = append(blocks, anthropic.ResponseBlock{Type: "text", Text: &text})
			buffered.Reset()
		}
	}
	gotFirst := false
	for {
		nextCtx := ctx
		if !gotFirst {
			nextCtx = firstCtx
		}
		waitCtx := nextCtx
		stopWait := func() {}
		if request.Stream {
			waitCtx, stopWait = context.WithTimeout(nextCtx, h.cfg.KeepAliveInterval)
		}
		event, err := turn.Next(waitCtx)
		pingDue := errors.Is(err, context.DeadlineExceeded) && waitCtx.Err() == context.DeadlineExceeded && nextCtx.Err() == nil
		stopWait()
		if deadline, ok := nextCtx.Deadline(); ok && !time.Now().Before(deadline) {
			pingDue = false
		}
		if request.Stream && pingDue {
			if err := beginStream(); err != nil {
				return
			}
			if output.remaining < 640 {
				_ = stream.Fail(errOutputLimit.Error())
				return
			}
			if err := stream.Ping(); err != nil {
				return
			}
			continue
		}
		if err == nil && nextCtx.Err() != nil {
			err = nextCtx.Err()
		}
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			if errors.Is(err, acp.ErrAuthentication) {
				h.fallback(output, request.Stream, "msg_"+id, turn.Model(), stream, buffered.String())
				return
			}
			message := failureMessage(err, !gotFirst, ctx)
			if stream != nil {
				_ = stream.Fail(message)
			} else {
				writeError(w, 502, "api_error", message)
			}
			return
		}
		if event.Kind == inference.Text && event.Text == "" {
			continue
		}
		if event.Kind != inference.Text && event.Kind != inference.End && event.Kind != inference.Tools || event.Kind == inference.Tools && !validToolBatch(request, event.Tools) || len(pendingTools) > 0 && event.Kind != inference.End {
			if stream != nil {
				_ = stream.Fail("Unsupported Kiro response event")
			} else {
				writeError(w, 502, "api_error", "Unsupported Kiro response event")
			}
			return
		}
		if !gotFirst {
			gotFirst = true
			stopFirst()
		}
		// Keep complete validated tools private until the backend confirms the tool-use handoff.
		if event.Kind == inference.Tools {
			pendingTools = event.Tools
			continue
		}
		if request.Stream && stream == nil {
			if err := beginStream(); err != nil {
				return
			}
		}
		if event.Kind == inference.Text {
			if request.Stream {
				encoded, _ := json.Marshal(event.Text)
				// Keep enough room for a normal terminal sequence or a safe error/fallback.
				if len(encoded)+128+512 > output.remaining {
					_ = stream.Fail(errOutputLimit.Error())
					return
				}
				if err := stream.Text(event.Text); err != nil {
					return
				}
			} else {
				if len(event.Text) > h.cfg.MaxOutputBytes-buffered.Len() {
					writeError(w, 502, "api_error", errOutputLimit.Error())
					return
				}
				buffered.WriteString(event.Text)
			}
			continue
		}
		if event.StopReason != "end_turn" && event.StopReason != "max_tokens" && event.StopReason != "refusal" && event.StopReason != "tool_use" || (event.StopReason == "tool_use") != (len(pendingTools) > 0) {
			if stream != nil {
				_ = stream.Fail("Kiro turn did not complete")
			} else {
				writeError(w, 502, "api_error", "Kiro turn did not complete")
			}
			return
		}
		if len(pendingTools) > 0 {
			if stream != nil {
				size := 0
				for _, tool := range pendingTools {
					n, e := stream.ToolBytes(tool)
					if e != nil {
						_ = stream.Fail("Invalid Kiro tool request")
						return
					}
					size += n
				}
				if size+512 > output.remaining {
					_ = stream.Fail(errOutputLimit.Error())
					return
				}
				for _, tool := range pendingTools {
					if err := stream.Tool(tool); err != nil {
						return
					}
				}
			} else {
				flushText()
				for _, tool := range pendingTools {
					blocks = append(blocks, tool.Block())
				}
			}
		}
		if request.Stream {
			err = stream.End(event.StopReason)
		} else {
			if len(blocks) == 0 {
				err = output.json(anthropic.NewResponse("msg_"+id, turn.Model(), buffered.String(), event.StopReason))
			} else {
				flushText()
				err = output.json(anthropic.NewBlocksResponse("msg_"+id, turn.Model(), blocks, event.StopReason))
			}
		}
		finished = err == nil && r.Context().Err() == nil
		return
	}
}

func clientIdentity(headers http.Header) (anthropic.ClientIdentity, error) {
	var identity anthropic.ClientIdentity
	for name, target := range map[string]*string{
		"x-claude-code-session-id":      &identity.Session,
		"x-claude-code-agent-id":        &identity.Agent,
		"x-claude-code-parent-agent-id": &identity.ParentAgent,
	} {
		values := headers.Values(name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 128 {
			return anthropic.ClientIdentity{}, anthropic.ErrRequest
		}
		for _, c := range []byte(values[0]) {
			if c < 0x21 || c > 0x7e || c == ',' {
				return anthropic.ClientIdentity{}, anthropic.ErrRequest
			}
		}
		*target = values[0]
	}
	return identity, nil
}

func validToolBatch(request *anthropic.Request, tools []anthropic.ToolUse) bool {
	disabled, err := request.ToolPolicy()
	if err != nil || disabled || len(tools) == 0 || len(tools) > 64 {
		return false
	}
	names := make(map[string]bool)
	for _, raw := range request.Tools {
		var declaration struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &declaration) != nil {
			return false
		}
		names[declaration.Name] = true
	}
	ids := make(map[string]bool)
	for _, tool := range tools {
		if !tool.Valid() || !names[tool.Name] || ids[tool.ID] {
			return false
		}
		ids[tool.ID] = true
	}
	return true
}

func failureMessage(err error, first bool, total context.Context) string {
	deadline, hasDeadline := total.Deadline()
	if total.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
		return "Kiro turn deadline exceeded"
	}
	if errors.Is(err, context.DeadlineExceeded) && first {
		return "Timed out waiting for the first model event"
	}
	if errors.Is(err, errOutputLimit) {
		return errOutputLimit.Error()
	}
	return "Kiro backend failed; retry with a new session"
}

func (h *Handler) fallback(w *responseWriter, streaming bool, id, model string, stream *anthropic.TextStream, prefix string) {
	if stream != nil {
		w.w.Header().Set(AuthFallbackHeader, "1") // Declared before commitment; transmitted as a trailer.
		if stream.Text("\n\n"+loginMessage) == nil {
			_ = stream.End("end_turn")
		}
		return
	}
	w.w.Header().Set(AuthFallbackHeader, "1")
	if prefix != "" {
		prefix += "\n\n"
	}
	if !streaming {
		_ = w.json(anthropic.NewResponse(id, model, prefix+loginMessage, "end_turn"))
		return
	}
	w.w.Header().Del("Trailer")
	w.w.Header().Set("Content-Type", "text/event-stream")
	s, err := anthropic.BeginTextStream(w, w.flush, id, model)
	if err == nil && s.Text(prefix+loginMessage) == nil {
		_ = s.End("end_turn")
	}
}

type responseWriter struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
	remaining  int
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if len(b) > w.remaining {
		return 0, errOutputLimit
	}
	if err := w.deadline(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	w.remaining -= n
	return n, err
}
func (w *responseWriter) deadline() error {
	err := w.controller.SetWriteDeadline(time.Now().Add(w.timeout))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}
func (w *responseWriter) flush() error {
	if err := w.deadline(); err != nil {
		return err
	}
	return w.controller.Flush()
}
func (w *responseWriter) json(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b)+1 > w.remaining {
		writeError(w.w, 502, "api_error", errOutputLimit.Error())
		return errOutputLimit
	}
	w.w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(append(b, '\n'))
	if err == nil {
		err = w.flush()
	}
	return err
}

func writeError(w http.ResponseWriter, status int, kind, message string) {
	writeJSON(w, status, anthropic.ErrorBody(kind, message))
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Listen rejects arbitrary names before DNS resolution unless the unsafe override is explicit.
func Listen(address string, unsafeNetwork bool) (net.Listener, error) {
	return listen(context.Background(), address, unsafeNetwork)
}

func listen(ctx context.Context, address string, unsafeNetwork bool) (net.Listener, error) {
	if address == "" {
		address = "127.0.0.1:0"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid gateway bind address")
	}
	if !unsafeNetwork {
		if host == "localhost" {
			host = "127.0.0.1"
		} else {
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return nil, errors.New("non-loopback bind requires unsafe-network")
			}
		}
	}
	return (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(host, port))
}
