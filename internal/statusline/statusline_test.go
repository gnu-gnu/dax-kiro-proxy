package statusline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureConfig(endpoint string) Config {
	return Config{Version: 1, Endpoint: endpoint, Token: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), Model: "claude-dax-fixture-0123456789abcdef"}
}

func writeConfig(t *testing.T, cfg Config) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dax-statusline-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	raw, err := EncodeConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "status.json")
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

const emptyView = `{"available":false,"refreshing":false,"stale":false,"state":"unsupported","data":{}}`

func TestStatuslineOnlyRequestsOwnedStatusWithUICredential(t *testing.T) {
	var calls atomic.Int32
	var expected Config
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.RequestURI() != "/dax-kiro-proxy/status/usage" || r.Header.Get("x-api-key") != expected.Token || r.Header.Get("Authorization") != "" || r.ContentLength > 0 {
			t.Error("status request crossed its fixed route or credential contract")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(emptyView))
	}))
	defer server.Close()
	expected = fixtureConfig(server.URL)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy"} {
		t.Setenv(key, "http://127.0.0.1:1/private-proxy-sentinel")
	}
	line, err := Display(t.Context(), writeConfig(t, expected))
	if err != nil || line != "Kiro fixture | no completed turn | usage unavailable\n" || calls.Load() != 1 {
		t.Fatal("status display failed or caused another request", line, err, calls.Load())
	}
}

func TestStatuslineNeverFollowsRedirectsAndBoundsFailures(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer other.Close()
	for _, mode := range []string{"redirect", "unauthorized", "oversized", "duplicate", "control-text", "bad-type", "slow-body"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "redirect":
					http.Redirect(w, r, other.URL+"/private-redirect-sentinel", http.StatusFound)
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte("private-credential-sentinel"))
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat(" ", (64<<10)+1)))
				case "duplicate":
					_, _ = w.Write([]byte(`{"state":"unsupported","state":"private-state"}`))
				case "control-text":
					_, _ = w.Write([]byte(strings.Replace(emptyView, "unsupported", `private-\u001b[31m`, 1)))
				case "bad-type":
					_, _ = w.Write([]byte(strings.Replace(emptyView, `"available":false`, `"available":"private-value"`, 1)))
				case "slow-body":
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			start := time.Now()
			line, err := Display(t.Context(), writeConfig(t, fixtureConfig(server.URL)))
			if err != nil || line != "Kiro | status unavailable\n" || time.Since(start) > 2*time.Second {
				t.Fatal("status failure was not bounded and fixed", line, err)
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatal("UI credential followed an HTTP redirect")
	}
}

func TestStatuslineCancellationDoesNotStartAnotherRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	path := writeConfig(t, fixtureConfig(server.URL))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Display(ctx, path); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("canceled status invocation still performed work")
	}
}

func TestStatuslinePrivateConfigurationAndNoEndpointOverride(t *testing.T) {
	valid := fixtureConfig("http://127.0.0.1:32123")
	for _, endpoint := range []string{"https://127.0.0.1:32123", "http://localhost:32123", "http://outside.invalid:32123", "http://127.0.0.1:32123/v1/messages", "http://127.0.0.1:32123?token=x", "http://secret@127.0.0.1:32123", "http://127.0.0.1:32123#fragment"} {
		cfg := valid
		cfg.Endpoint = endpoint
		if _, err := EncodeConfig(cfg); !errors.Is(err, ErrConfig) {
			t.Fatal("unsupported status endpoint accepted")
		}
	}
	for _, mode := range []string{"version", "token", "model", "extra", "symlink", "hardlink", "public", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			path := writeConfig(t, valid)
			raw, _ := os.ReadFile(path)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			switch mode {
			case "version":
				fields["version"] = 2
			case "token":
				fields["token"] = "private-token-sentinel"
			case "model":
				fields["model"] = "private-model-sentinel\x1b[31m"
			case "extra":
				fields["path"] = "/v1/messages"
			case "symlink":
				if os.Symlink(path, path+".link") != nil {
					t.Fatal("cannot create fixture link")
				}
				path += ".link"
			case "hardlink":
				if os.Link(path, path+".link") != nil {
					t.Fatal("cannot create fixture link")
				}
			case "public":
				if os.Chmod(path, 0644) != nil {
					t.Fatal("cannot change fixture mode")
				}
			}
			if mode == "version" || mode == "token" || mode == "model" || mode == "extra" || mode == "oversized" {
				raw, _ = json.Marshal(fields)
				if mode == "oversized" {
					raw = append(raw, []byte(strings.Repeat(" ", 4096))...)
				}
				if os.WriteFile(path, raw, 0600) != nil {
					t.Fatal("cannot update fixture configuration")
				}
			}
			line, err := Display(t.Context(), path)
			if !errors.Is(err, ErrConfig) || line != "" || strings.Contains(err.Error(), "private-") {
				t.Fatal("untrusted status configuration was used", line, err)
			}
		})
	}
}

func TestStatuslineCannotRecreateRemovedRuntime(t *testing.T) {
	path := writeConfig(t, fixtureConfig("http://127.0.0.1:32123"))
	directory := filepath.Dir(path)
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := Display(t.Context(), path); !errors.Is(err, ErrConfig) {
		t.Fatal("removed configuration accepted")
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late status command recreated an expired runtime")
	}
}
