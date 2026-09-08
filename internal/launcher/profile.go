// Package launcher prepares owned runtime configuration without writing client source settings.
package launcher

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/privatefs"
)

var (
	ErrConfig   = errors.New("invalid or unsupported client launch configuration")
	ErrSettings = errors.New("client settings must be a bounded, owned, regular JSON object")
	ErrRuntime  = errors.New("cannot prepare or remove private client runtime")
)

const MaxSettingsBytes = 2 << 20
const SupportedClientVersion = "2.1.263"

type ClientConfig struct {
	RuntimeParent, Home, Project, UserSettings         string
	Executable, Version, Model, GatewayURL, ModelToken string
	Environment                                        []string
}

type ClientProfile struct {
	mu             sync.Mutex
	path, settings string
	rootInfo       os.FileInfo
	command        childproc.Command
	closed         bool
	closeErr       error
}

func PrepareClient(cfg ClientConfig) (*ClientProfile, error) {
	if !validClient(cfg) {
		return nil, ErrConfig
	}
	env, err := platformEnvironment(cfg.Environment)
	if err != nil {
		return nil, err
	}
	user, err := readSettings(cfg.UserSettings)
	if err != nil {
		return nil, err
	}
	user, err = isolatedSettings(user)
	if err != nil {
		return nil, err
	}
	path, err := os.MkdirTemp(cfg.RuntimeParent, "dax-runtime-")
	if err != nil {
		return nil, ErrRuntime
	}
	p := &ClientProfile{path: path}
	p.rootInfo, err = os.Lstat(path)
	if err != nil {
		_ = os.RemoveAll(path)
		return nil, ErrRuntime
	}
	ok := false
	defer func() {
		if !ok {
			_ = p.Close()
		}
	}()
	profile, scratch := filepath.Join(path, "client"), filepath.Join(path, "tmp")
	for _, dir := range []string{profile, scratch} {
		if os.Mkdir(dir, 0700) != nil {
			return nil, ErrRuntime
		}
	}
	store, err := privatefs.New(profile)
	if err != nil {
		return nil, ErrRuntime
	}
	if store.Write("settings.json", user) != nil {
		return nil, ErrRuntime
	}
	env["HOME"], env["TMPDIR"], env["CLAUDE_CONFIG_DIR"] = cfg.Home, scratch, profile
	for key, value := range hostEnvironment(cfg.GatewayURL, cfg.ModelToken) {
		env[key] = value
	}
	// Only connection/authentication and optional traffic controls belong at the command-line layer.
	// Omitting permissions/hooks lets the client retain its user < project < local < managed order.
	overlay, _ := json.Marshal(map[string]any{"env": hostEnvironment(cfg.GatewayURL, cfg.ModelToken), "apiKeyHelper": "", "awsAuthRefresh": "", "awsCredentialExport": "", "otelHeadersHelper": ""})
	root, err := privatefs.New(path)
	if err != nil || root.Write("host-settings.json", overlay) != nil {
		return nil, ErrRuntime
	}
	p.settings = filepath.Join(path, "host-settings.json")
	p.command = childproc.Command{Executable: cfg.Executable, Directory: cfg.Project, Args: []string{"--settings", p.settings, "--model", cfg.Model}, Environment: sortedEnvironment(env)}
	ok = true
	return p, nil
}

func validClient(cfg ClientConfig) bool {
	if cfg.Version != SupportedClientVersion {
		return false
	}
	for _, path := range []string{cfg.RuntimeParent, cfg.Home, cfg.Project, cfg.UserSettings, cfg.Executable} {
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return false
		}
	}
	for _, path := range []string{cfg.RuntimeParent, cfg.Home, cfg.Project} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	u, err := url.Parse(cfg.GatewayURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return false
	}
	token, err := base64.RawURLEncoding.DecodeString(cfg.ModelToken)
	if err != nil || len(token) != 32 || base64.RawURLEncoding.EncodeToString(token) != cfg.ModelToken {
		return false
	}
	if !strings.HasPrefix(cfg.Model, "claude-dax-") || len(cfg.Model) <= len("claude-dax-") || len(cfg.Model) > 256 {
		return false
	}
	for _, c := range []byte(cfg.Model) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// The starting environment is an allowlist. Settings may still supply tool-specific variables;
// the pinned client's host-provider guard keeps their routing/authentication variables inactive.
func platformEnvironment(source []string) (map[string]string, error) {
	if len(source) > 1024 {
		return nil, ErrConfig
	}
	allowed := map[string]bool{"PATH": true, "USER": true, "LOGNAME": true, "SHELL": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TERM": true, "COLORTERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "COLUMNS": true, "LINES": true, "TZ": true, "SSH_AUTH_SOCK": true}
	out := map[string]string{"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "TERM": "xterm-256color", "LANG": "en_US.UTF-8"}
	seen := map[string]bool{}
	total := 0
	for _, entry := range source {
		total += len(entry)
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || seen[key] || strings.ContainsRune(entry, 0) || total > 128<<10 {
			return nil, ErrConfig
		}
		seen[key] = true
		if allowed[key] {
			if len(value) > 8192 || strings.ContainsAny(value, "\r\n") {
				return nil, ErrConfig
			}
			out[key] = value
		}
	}
	return out, nil
}
func hostEnvironment(endpoint, token string) map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL": endpoint, "ANTHROPIC_API_KEY": token, "ANTHROPIC_AUTH_TOKEN": token,
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":                 "1",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY":           "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":             "1",
		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL": "1",
		"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK":            "1",
		"CLAUDE_CODE_MAX_RETRIES":                              "0",
		"DISABLE_TELEMETRY":                                    "1", "DISABLE_ERROR_REPORTING": "1", "DISABLE_AUTOUPDATER": "1",
		"HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "http_proxy": "", "https_proxy": "", "all_proxy": "",
		"NO_PROXY": "127.0.0.1,localhost,::1", "no_proxy": "127.0.0.1,localhost,::1",
	}
}
func sortedEnvironment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
func reservedEnvironment(key string) bool {
	if _, ok := hostEnvironment("", "")[key]; ok {
		return true
	}
	for _, prefix := range []string{"ANTHROPIC_", "AWS_", "AZURE_", "GOOGLE_", "GCLOUD_", "CLAUDE_CODE_USE_", "CLAUDE_CODE_OAUTH_", "OTEL_"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	switch key {
	case "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_FALLBACK_MODEL", "NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "CLAUDE_CODE_ENABLE_TELEMETRY":
		return true
	}
	return false
}
func isolatedSettings(raw []byte) ([]byte, error) {
	fields, err := ndjson.Object(raw)
	if err != nil {
		return nil, ErrSettings
	}
	for _, key := range []string{"model", "fallbackModel", "modelOverrides", "apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "otelHeadersHelper"} {
		delete(fields, key)
	}
	if data, ok := fields["env"]; ok {
		entries, err := ndjson.Object(data)
		if err != nil {
			return nil, ErrSettings
		}
		for key, value := range entries {
			var text string
			if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &text) != nil || strings.ContainsRune(key, 0) || strings.ContainsAny(key, "=\r\n") || key == "" || strings.ContainsRune(text, 0) {
				return nil, ErrSettings
			}
			if reservedEnvironment(key) {
				delete(entries, key)
			}
		}
		fields["env"], _ = json.Marshal(entries)
	}
	result, err := json.Marshal(fields)
	if err != nil || len(result) > MaxSettingsBytes {
		return nil, ErrSettings
	}
	return result, nil
}
func readSettings(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return []byte(`{}`), nil
	}
	if err != nil {
		return nil, ErrSettings
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Base(path), settingsReadFlags, 0)
	if errors.Is(err, os.ErrNotExist) {
		return []byte(`{}`), nil
	}
	if err != nil {
		return nil, ErrSettings
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !safeSettings(info) || info.Size() > MaxSettingsBytes {
		return nil, ErrSettings
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxSettingsBytes+1))
	if err != nil || len(data) > MaxSettingsBytes {
		return nil, ErrSettings
	}
	return data, nil
}
func (p *ClientProfile) Path() string         { return p.path }
func (p *ClientProfile) SettingsPath() string { return p.settings }
func (p *ClientProfile) Command() childproc.Command {
	out := p.command
	out.Args = append([]string(nil), out.Args...)
	out.Environment = append([]string(nil), out.Environment...)
	return out
}
func (p *ClientProfile) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.closeErr
	}
	p.closed = true
	current, err := os.Lstat(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !current.IsDir() || !os.SameFile(current, p.rootInfo) || os.RemoveAll(p.path) != nil {
		p.closeErr = ErrRuntime
	}
	return p.closeErr
}
