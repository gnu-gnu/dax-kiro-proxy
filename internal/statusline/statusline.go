// Package statusline reads the launcher's UI-only credential and displays bounded cached status.
// It never reads client stdin, launches a subprocess, follows a redirect or invokes a model route.
package statusline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/status"
)

var ErrConfig = errors.New("invalid private status-line configuration")

type Config struct {
	Version  int    `json:"version"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	Model    string `json:"model"`
}

func valid(cfg Config) bool {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || len(cfg.Endpoint) > 256 || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(cfg.Endpoint, "\r\n#") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return false
	}
	token, err := base64.RawURLEncoding.DecodeString(cfg.Token)
	if err != nil || len(token) != 32 || base64.RawURLEncoding.EncodeToString(token) != cfg.Token {
		return false
	}
	_, err = status.ModelLabel(cfg.Model)
	return cfg.Version == 1 && err == nil
}

func EncodeConfig(cfg Config) ([]byte, error) {
	if !valid(cfg) {
		return nil, ErrConfig
	}
	return json.Marshal(cfg)
}

func load(path string) (Config, error) {
	var cfg Config
	if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return cfg, ErrConfig
	}
	store, err := privatefs.Open(filepath.Dir(path))
	if err != nil {
		return cfg, ErrConfig
	}
	raw, err := store.Read(filepath.Base(path), 4096)
	if err != nil {
		return cfg, ErrConfig
	}
	fields, err := ndjson.Object(raw)
	if err != nil || len(fields) != 4 {
		return cfg, ErrConfig
	}
	for _, key := range []string{"version", "endpoint", "token", "model"} {
		if _, ok := fields[key]; !ok {
			return cfg, ErrConfig
		}
	}
	if json.Unmarshal(raw, &cfg) != nil || !valid(cfg) {
		return Config{}, ErrConfig
	}
	return cfg, nil
}

// Display has one 750ms HTTP budget. Ordinary network/status failures yield a fixed local display;
// caller cancellation and invalid private configuration are errors. There is no retry or fallback.
func Display(ctx context.Context, path string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	cfg, err := load(path)
	if err != nil {
		return "", err
	}
	bounded, stop := context.WithTimeout(ctx, 750*time.Millisecond)
	defer stop()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext, DisableCompression: true, DisableKeepAlives: true, MaxConnsPerHost: 1, MaxResponseHeaderBytes: 8 << 10, ResponseHeaderTimeout: 300 * time.Millisecond}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(bounded, http.MethodGet, cfg.Endpoint+"/dax-kiro-proxy/status/usage", nil)
	if err != nil {
		return "", ErrConfig
	}
	req.Header.Set("x-api-key", cfg.Token)
	req.Header.Set("Accept", "application/json")
	fallback := func() (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "Kiro | status unavailable\n", nil
	}
	response, err := client.Do(req)
	if err != nil {
		return fallback()
	}
	defer response.Body.Close()
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != 200 || mediaErr != nil || media != "application/json" {
		return fallback()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return fallback()
	}
	line, err := status.FormatView(raw, cfg.Model)
	if err != nil {
		return fallback()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return line, nil
}
