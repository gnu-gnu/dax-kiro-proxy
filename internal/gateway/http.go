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
	FirstEventTimeout time.Duration
	TurnTimeout       time.Duration
	WriteTimeout      time.Duration
	ReadTimeout       time.Duration
	MaxActiveRequests int
	MaxOutputBytes    int
}

type Handler struct {
	cfg    Config
	active chan struct{}
}

func New(cfg Config) (*Handler, error) {
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
	if cfg.MaxActiveRequests == 0 {
		cfg.MaxActiveRequests = 16
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = 16 << 20
	}
	if cfg.FirstEventTimeout <= 0 || cfg.FirstEventTimeout > cfg.TurnTimeout || cfg.TurnTimeout > time.Hour ||
		cfg.WriteTimeout <= 0 || cfg.WriteTimeout > time.Minute || cfg.ReadTimeout <= 0 || cfg.ReadTimeout > time.Minute ||
		cfg.MaxActiveRequests < 1 || cfg.MaxActiveRequests > 256 || cfg.MaxOutputBytes < 1024 || cfg.MaxOutputBytes > 64<<20 {
		return nil, errors.New("invalid gateway resource limits")
	}
	return &Handler{cfg: cfg, active: make(chan struct{}, cfg.MaxActiveRequests)}, nil
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
	case "GET /dax-kiro-proxy/status/usage":
		credential = h.cfg.Tokens.UI
	default:
		writeError(w, 404, "not_found_error", "Route not found")
		return
	}
	if !authorized(r, credential) {
		writeError(w, 401, "authentication_error", "Invalid gateway credential")
		return
	}
	if r.URL.Path == "/dax-kiro-proxy/status/usage" {
		writeJSON(w, 200, map[string]any{"available": false})
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
		writeError(w, 400, "invalid_request_error", "Invalid Messages request")
		return
	}
	if !request.TextOnly() {
		writeError(w, 400, "invalid_request_error", "Content or tools are unsupported by the current adapter")
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
		if errors.Is(err, inference.ErrRequest) || errors.Is(err, anthropic.ErrRequest) {
			writeError(w, 400, "invalid_request_error", "Requested model or content is incompatible with the current Kiro catalog")
		} else if errors.Is(err, inference.ErrBusy) {
			writeError(w, 409, "invalid_request_error", "This conversation already has an active response")
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
	var buffered strings.Builder
	gotFirst := false
	for {
		nextCtx := ctx
		if !gotFirst {
			nextCtx = firstCtx
		}
		event, err := turn.Next(nextCtx)
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
		if event.Kind != inference.Text && event.Kind != inference.End {
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
		if request.Stream && stream == nil {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Trailer", AuthFallbackHeader)
			stream, err = anthropic.BeginTextStream(output, output.flush, "msg_"+id, turn.Model())
			if err != nil {
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
		if event.StopReason != "end_turn" && event.StopReason != "max_tokens" && event.StopReason != "refusal" {
			if stream != nil {
				_ = stream.Fail("Kiro turn did not complete")
			} else {
				writeError(w, 502, "api_error", "Kiro turn did not complete")
			}
			return
		}
		if request.Stream {
			err = stream.End(event.StopReason)
		} else {
			err = output.json(anthropic.NewResponse("msg_"+id, turn.Model(), buffered.String(), event.StopReason))
		}
		finished = err == nil && r.Context().Err() == nil
		return
	}
}

func failureMessage(err error, first bool, total context.Context) string {
	if total.Err() != nil {
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
	return net.Listen("tcp", net.JoinHostPort(host, port))
}
