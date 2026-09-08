package gateway

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/status"
)

// UI admission and deadlines are independent from the model path. These routes never invoke it.
func (h *Handler) ui(w http.ResponseWriter, r *http.Request) {
	select {
	case h.uiActive <- struct{}{}:
		defer func() { <-h.uiActive }()
	default:
		writeError(w, 429, "overloaded_error", "Status request capacity reached")
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(h.cfg.WriteTimeout))
	defer controller.SetWriteDeadline(time.Time{})
	if r.URL.Path == "/dax-kiro-proxy/status/usage" {
		view := struct {
			status.UsageSnapshot
			Latest *status.TurnRecord `json:"latest_turn,omitempty"`
		}{UsageSnapshot: status.UsageSnapshot{State: "unsupported"}}
		if h.cfg.Usage != nil {
			view.UsageSnapshot = h.cfg.Usage.Read()
		}
		if h.cfg.Metrics != nil {
			view.Latest = h.cfg.Metrics.Latest()
		}
		writeJSON(w, 200, view)
		return
	}
	if kind := r.Header.Get("Content-Type"); kind != "" {
		media, _, err := mime.ParseMediaType(kind)
		if err != nil || media != "application/json" {
			writeError(w, 400, "invalid_request_error", "Expected application/json")
			return
		}
	}
	_ = controller.SetReadDeadline(time.Now().Add(h.cfg.ReadTimeout))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	_ = controller.SetReadDeadline(time.Time{})
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			writeError(w, 413, "request_too_large", "Hook body exceeds 4 KiB")
		} else {
			writeError(w, 400, "invalid_request_error", "Cannot read hook request")
		}
		return
	}
	if len(bytes.TrimSpace(body)) != 0 {
		fields, err := ndjson.Object(body)
		if err != nil || len(fields) != 0 {
			writeError(w, 400, "invalid_request_error", "Expected an empty hook request")
			return
		}
	}
	page := status.MetricsPage{Records: []status.TurnRecord{}}
	if h.cfg.Metrics != nil {
		page = h.cfg.Metrics.Drain()
	}
	writeJSON(w, 200, page)
}
