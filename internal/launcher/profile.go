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
	"dax-kiro-proxy/internal/statusline"
)

var (
	ErrConfig   = errors.New("invalid or unsupported client launch configuration")
	ErrSettings = errors.New("client settings and assets must use bounded, owned, regular sources")
	ErrRuntime  = errors.New("cannot prepare or remove private client runtime")
)

const MaxSettingsBytes = 2 << 20

// SupportedClientVersion is the measured client build behind the recorded installed-client
// evidence (D113 migrated it from 2.1.263 after a full regression pass). Launch admission
// compares only its major component (D110); startup reports whether the detected build is this
// measured one rather than treating it as verified.
const SupportedClientVersion = "2.1.267"

// ClientVersionFromOutput parses the client's --version output and reports whether that
// build is admitted. The output must be the bare version followed by the client's name.
func ClientVersionFromOutput(output []byte) (string, bool) {
	if len(output) > 256 {
		return "", false
	}
	version, named := strings.CutSuffix(strings.TrimSpace(string(output)), " (Claude Code)")
	if !named || !CompatibleClientVersion(version) {
		return "", false
	}
	return version, true
}

// CompatibleClientOutput is ClientVersionFromOutput's admission result alone.
func CompatibleClientOutput(output []byte) bool {
	_, ok := ClientVersionFromOutput(output)
	return ok
}

// CompatibleClientVersion reports whether a client build may launch. Only the
// major component is compared against SupportedClientVersion, so minor and
// patch updates of the measured client stay usable without repinning.
func CompatibleClientVersion(version string) bool {
	major, ok := clientVersionMajor(version)
	if !ok {
		return false
	}
	supported, ok := clientVersionMajor(SupportedClientVersion)
	return ok && major == supported
}

// clientVersionMajor accepts only bounded dotted decimal versions so a reported
// value stays safe to compare and to echo back in diagnostics.
func clientVersionMajor(version string) (string, bool) {
	if version == "" || len(version) > 32 {
		return "", false
	}
	fields := strings.Split(version, ".")
	if len(fields) > 4 {
		return "", false
	}
	for _, field := range fields {
		if field == "" || len(field) > 8 || strings.TrimLeft(field, "0123456789") != "" {
			return "", false
		}
	}
	return fields[0], true
}

type ClientConfig struct {
	RuntimeParent, Home, Project, UserSettings         string
	Executable, Version, Model, GatewayURL, ModelToken string
	Environment                                        []string
	StatusExecutable, UIToken                          string
	KeepHistory                                        bool
	ResumeSession                                      string
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
	mcpState, err := clientMCPState(cfg.Home)
	if err != nil {
		return nil, err
	}
	pluginSeed, err := clientPluginSeed(cfg.Home)
	if err != nil {
		return nil, err
	}
	pluginRegistrations, err := clientPluginRegistrations(pluginSeed)
	if err != nil {
		return nil, err
	}
	customizations, err := clientCustomizations(cfg.Home)
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
	if err := writeClientCustomizations(profile, customizations); err != nil {
		return nil, err
	}
	store, err := privatefs.New(profile)
	if err != nil {
		return nil, ErrRuntime
	}
	if store.Write(".claude.json", mcpState) != nil {
		return nil, ErrRuntime
	}
	if len(pluginRegistrations) != 0 {
		pluginDir := filepath.Join(profile, "plugins")
		if os.Mkdir(pluginDir, 0700) != nil {
			return nil, ErrRuntime
		}
		pluginStore, err := privatefs.New(pluginDir)
		if err != nil {
			return nil, ErrRuntime
		}
		for name, data := range pluginRegistrations {
			if pluginStore.Write(name, data) != nil {
				return nil, ErrRuntime
			}
		}
	}
	env["HOME"], env["TMPDIR"], env["CLAUDE_CONFIG_DIR"] = cfg.Home, scratch, profile
	if pluginSeed != "" {
		env["CLAUDE_CODE_PLUGIN_SEED_DIR"] = pluginSeed
	}
	for key, value := range hostEnvironment(cfg.GatewayURL, cfg.ModelToken) {
		env[key] = value
	}
	// Connection, authentication and optional traffic use the command-line layer.
	// Permission policy retains its user < project < local < managed order. Product hooks are additive.
	host := map[string]any{"env": hostEnvironment(cfg.GatewayURL, cfg.ModelToken), "apiKeyHelper": "", "awsAuthRefresh": "", "awsCredentialExport": "", "otelHeadersHelper": ""}
	root, err := privatefs.New(path)
	if err != nil {
		return nil, ErrRuntime
	}
	if cfg.StatusExecutable != "" {
		data, err := statusline.EncodeConfig(statusline.Config{Version: 1, Endpoint: cfg.GatewayURL, Token: cfg.UIToken, Model: cfg.Model})
		if err != nil || root.Write("statusline.json", data) != nil {
			return nil, ErrRuntime
		}
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
		// The client invokes this command. The display helper has no model/provider environment;
		// its sole credential is read from the private file, never embedded in a shell argument.
		command := "/usr/bin/env -i PATH=/usr/bin:/bin " + quote(cfg.StatusExecutable) + " statusline --config " + quote(filepath.Join(path, "statusline.json"))
		// Status is a user-scope default. Keep an existing value exactly, and let the client
		// resolve higher-priority project/local/managed settings through its native rules.
		fields, err := ndjson.Object(user)
		if err != nil {
			return nil, ErrSettings
		}
		if _, exists := fields["statusLine"]; !exists && clientStatusDefaultAllowed(cfg.Project) {
			fields["statusLine"], _ = json.Marshal(map[string]any{"type": "command", "command": command, "refreshInterval": 5})
			withDefault, err := json.Marshal(fields)
			if err != nil {
				return nil, ErrRuntime
			}
			// This optional display must not enlarge an otherwise valid settings snapshot past
			// its bound. The independent notice/metrics hooks can still use their UI config.
			if len(withDefault) <= MaxSettingsBytes {
				user = withDefault
			}
		}
		noticeCommand := "/usr/bin/env -i PATH=/usr/bin:/bin " + quote(cfg.StatusExecutable) + " model-notice --config " + quote(filepath.Join(path, "statusline.json"))
		metricsCommand := "/usr/bin/env -i PATH=/usr/bin:/bin " + quote(cfg.StatusExecutable) + " turn-metrics --config " + quote(filepath.Join(path, "statusline.json"))
		host["hooks"] = map[string]any{
			"SessionStart": []any{map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": noticeCommand, "timeout": 3}}}},
			"Stop":         []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": metricsCommand, "timeout": 3}}}},
		}
	}
	if store.Write("settings.json", user) != nil {
		return nil, ErrRuntime
	}
	overlay, _ := json.Marshal(host)
	if root.Write("host-settings.json", overlay) != nil {
		return nil, ErrRuntime
	}
	p.settings = filepath.Join(path, "host-settings.json")
	p.command = childproc.Command{Executable: cfg.Executable, Directory: cfg.Project, Args: []string{"--settings", p.settings, "--model", cfg.Model}, Environment: sortedEnvironment(env)}
	if cfg.KeepHistory {
		if err := referenceClientHistory(cfg.Home, profile); err != nil {
			return nil, err
		}
	}
	if cfg.ResumeSession != "" {
		p.command.Args = append(p.command.Args, "--resume", cfg.ResumeSession)
	}
	ok = true
	return p, nil
}

func validClient(cfg ClientConfig) bool {
	if cfg.ResumeSession != "" && (!cfg.KeepHistory || !validNativeSessionID(cfg.ResumeSession)) {
		return false
	}
	if !CompatibleClientVersion(cfg.Version) {
		return false
	}
	if cfg.StatusExecutable != "" || cfg.UIToken != "" {
		if !filepath.IsAbs(cfg.StatusExecutable) || len(cfg.StatusExecutable) > 4096 || strings.ContainsAny(cfg.StatusExecutable, "\x00\r\n") || cfg.UIToken == cfg.ModelToken {
			return false
		}
		if _, err := statusline.EncodeConfig(statusline.Config{Version: 1, Endpoint: cfg.GatewayURL, Token: cfg.UIToken, Model: cfg.Model}); err != nil {
			return false
		}
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
		// Product model IDs are deliberately absent from the client's built-in catalog (D08). A
		// later client clamps unknown models to an assumed context window; keep the measured
		// build's behavior of deferring to the API instead (D112).
		"CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT": "1",
		"DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1", "DISABLE_AUTOUPDATER": "1",
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
