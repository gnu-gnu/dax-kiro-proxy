// Package uiclient reads private UI configuration and accesses only enumerated local UI routes.
// It never reads client stdin, launches a subprocess, follows redirects or invokes model routes.
package uiclient

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

var ErrConfig = errors.New("invalid private UI configuration")
var ErrUnavailable = errors.New("local UI information unavailable")

type Operation uint8

const (
	Usage Operation = iota
	ModelCapabilities
	TurnMetrics
)

type View struct {
	Model string
	Body  []byte
}

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

// Read has one 750ms HTTP budget and returns only bounded content or a fixed error class.
func Read(ctx context.Context, path string, operation Operation) (View, error) {
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	method, route, body := http.MethodGet, "/dax-kiro-proxy/status/usage", ""
	switch operation {
	case Usage:
	case ModelCapabilities:
		method, route, body = http.MethodPost, "/dax-kiro-proxy/hooks/model-capabilities", "{}"
	case TurnMetrics:
		method, route, body = http.MethodPost, "/dax-kiro-proxy/hooks/turn-metrics", "{}"
	default:
		return View{}, ErrConfig
	}
	cfg, err := load(path)
	if err != nil {
		return View{}, err
	}
	bounded, stop := context.WithTimeout(ctx, 750*time.Millisecond)
	defer stop()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext, DisableCompression: true, DisableKeepAlives: true, MaxConnsPerHost: 1, MaxResponseHeaderBytes: 8 << 10, ResponseHeaderTimeout: 300 * time.Millisecond}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(bounded, method, cfg.Endpoint+route, strings.NewReader(body))
	if err != nil {
		return View{}, ErrConfig
	}
	req.Header.Set("x-api-key", cfg.Token)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	failure := func() (View, error) {
		if ctx.Err() != nil {
			return View{}, ctx.Err()
		}
		return View{}, ErrUnavailable
	}
	response, err := client.Do(req)
	if err != nil {
		return failure()
	}
	defer response.Body.Close()
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != 200 || mediaErr != nil || media != "application/json" {
		return failure()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return failure()
	}
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	return View{Model: cfg.Model, Body: raw}, nil
}
