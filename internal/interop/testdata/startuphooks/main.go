// Independently authored public-hook fixture. It ignores stdin and performs only two authenticated
// loopback observations and one bounded JSON notice, with no model or client tool operation.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type configuration struct {
	Version  int    `json:"version"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	Kind     string `json:"kind"`
}

func main() {
	time.AfterFunc(12*time.Second, func() { os.Exit(70) })
	if !run() {
		os.Exit(1)
	}
}

func run() bool {
	if len(os.Args) != 2 || !filepath.IsAbs(os.Args[1]) {
		return false
	}
	f, err := os.OpenFile(os.Args[1], os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 1024 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1025))
	var cfg configuration
	if err != nil || len(raw) > 1024 || json.Unmarshal(raw, &cfg) != nil || cfg.Version != 1 || (cfg.Kind != "fast" && cfg.Kind != "held") {
		return false
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Port() == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	key, err := base64.RawURLEncoding.DecodeString(cfg.Token)
	if ip == nil || !ip.IsLoopback() || err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != cfg.Token {
		return false
	}
	root, err := os.OpenRoot(filepath.Dir(os.Args[1]))
	if err != nil {
		return false
	}
	defer root.Close()
	record, err := root.OpenFile(cfg.Kind+".pid", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false
	}
	_, writeErr := io.WriteString(record, strconv.Itoa(os.Getpid()))
	if err := record.Close(); err != nil || writeErr != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, DisableKeepAlives: true, DisableCompression: true, MaxConnsPerHost: 1, MaxResponseHeaderBytes: 4096}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, phase := range []string{"entered", "returned"} {
		req, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint+"/startup-observation/"+cfg.Kind+"/"+phase, strings.NewReader("{}"))
		if err != nil {
			return false
		}
		req.Header.Set("X-Dax-Observation-Key", cfg.Token)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return false
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 257))
		response.Body.Close()
		if response.StatusCode != 200 || readErr != nil || string(body) != "{}" {
			return false
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"systemMessage": "Dax startup " + cfg.Kind + " complete"}) == nil
}
