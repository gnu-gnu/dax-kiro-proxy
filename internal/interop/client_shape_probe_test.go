package interop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

// requestShapeRecorder keeps only structural facts about each Messages request: message roles,
// block types in order, tool_result error flags and the gateway's status. No text, ids or paths.
type requestShapeRecorder struct {
	mu     sync.Mutex
	shapes []string
}

func (r *requestShapeRecorder) record(body []byte, status int) {
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		System json.RawMessage   `json:"system"`
		Tools  []json.RawMessage `json:"tools"`
	}
	summary := "unparsed"
	if json.Unmarshal(body, &request) == nil {
		var parts []string
		for _, m := range request.Messages {
			var blocks []struct {
				Type    string          `json:"type"`
				IsError *bool           `json:"is_error"`
				Content json.RawMessage `json:"content"`
			}
			kinds := []string{}
			if err := json.Unmarshal(m.Content, &blocks); err != nil {
				var text string
				if json.Unmarshal(m.Content, &text) == nil {
					kinds = append(kinds, "string")
				} else {
					kinds = append(kinds, "?")
				}
			}
			for _, b := range blocks {
				kind := b.Type
				if b.Type == "tool_result" {
					kind += "("
					if b.IsError != nil && *b.IsError {
						kind += "error"
					} else {
						kind += "ok"
					}
					var nested []struct{ Type string }
					if json.Unmarshal(b.Content, &nested) == nil {
						for _, n := range nested {
							kind += "," + n.Type
						}
					} else if len(b.Content) > 0 && b.Content[0] == '"' {
						kind += ",string"
					}
					kind += ")"
				}
				kinds = append(kinds, kind)
			}
			// A content digest lets two requests be compared for byte identity without logging content;
			// system-role messages also carry a text-level digest so string and block forms compare.
			sum := sha256.Sum256(m.Content)
			label := m.Role + ":" + strings.Join(kinds, "+") + "#" + hex.EncodeToString(sum[:4])
			if m.Role == "system" {
				label += "/t" + textDigest(m.Content)
			}
			parts = append(parts, label)
		}
		system := "none"
		if len(request.System) > 0 {
			if request.System[0] == '"' {
				system = "string"
			} else {
				var blocks []struct{ Type string }
				_ = json.Unmarshal(request.System, &blocks)
				system = "blocks:" + itoa(len(blocks))
			}
		}
		systemSum := sha256.Sum256(request.System)
		var systemBlocks []struct{ Text string }
		_ = json.Unmarshal(request.System, &systemBlocks)
		for i, b := range systemBlocks {
			t := sha256.Sum256([]byte(b.Text))
			system += " b" + itoa(i) + "/t" + hex.EncodeToString(t[:4]) + "(" + itoa(len(b.Text)) + ")"
		}
		summary = "status=" + itoa(status) + " tools=" + itoa(len(request.Tools)) + " system=" + system + "#" + hex.EncodeToString(systemSum[:4]) + " messages=[" + strings.Join(parts, " | ") + "]"
	}
	r.mu.Lock()
	r.shapes = append(r.shapes, summary)
	r.mu.Unlock()
}

// textDigest digests the concatenated text of a string or text-block message content.
func textDigest(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) != nil {
		var blocks []struct{ Text string }
		_ = json.Unmarshal(content, &blocks)
		for _, b := range blocks {
			text += b.Text
		}
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:4]) + "(" + itoa(len(text)) + ")"
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + json.Number(intString(n)).String())
}
func intString(n int) string {
	data, _ := json.Marshal(n)
	return string(data)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// TestClaudeToolResultRequestShapeProbe records the structural shape of every Messages request the
// installed client sends through one denied-tool continuation, so a client build's request form can
// be compared against the measured build without logging any content.
func TestClaudeToolResultRequestShapeProbe(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for the owned fake ACP/client shape probe; no model credits")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-client-shape-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("Go toolchain is unavailable")
	}
	goPath, _ = filepath.Abs(goPath)
	buildEnv := []string{}
	for _, key := range []string{"HOME", "PATH", "GOTOOLCHAIN", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOSUMDB"} {
		if value, ok := os.LookupEnv(key); ok {
			buildEnv = append(buildEnv, key+"="+value)
		}
	}
	cwd, _ := os.Getwd()
	fake, relayPath := filepath.Join(root, "fake-acp"), filepath.Join(root, "relay")
	for _, target := range []struct{ output, source string }{{fake, "../acp/testdata/fake"}, {relayPath, "../../cmd/dax-kiro-proxy"}} {
		if _, err := runner.Run(t.Context(), childproc.Command{Executable: goPath, Directory: cwd, Environment: buildEnv, Args: []string{"build", "-o", target.output, target.source}}); err != nil {
			t.Fatalf("cannot build owned protocol fixture: %v", err)
		}
	}
	home, project, backendDir, workerDir := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "backend"), filepath.Join(root, "worker")
	for _, path := range []string{home, project, backendDir, workerDir} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("cannot create owned workspaces")
		}
	}
	userSettings := filepath.Join(home, "settings.json")
	denial, _ := json.Marshal(map[string]any{"permissions": map[string]string{"defaultMode": "manual"}, "hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": `printf '%s' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"independent fixture denial"}}'`, "timeout": 2}}}}}})
	if os.WriteFile(userSettings, denial, 0600) != nil {
		t.Fatal("cannot create owned permission policy")
	}
	validator, err := schemacheck.New(schemacheck.Config{Executable: relayPath, Directory: workerDir})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Close()
	driver, err := session.New(session.Config{Process: acp.Config{Executable: fake, Args: []string{"chat-tools-client", filepath.Join(project, "denied-fixture")}, Directory: backendDir, ClientInfo: acp.Info{Name: "independent-client-probe", Version: "1"}}, Validator: validator, RelayExecutable: relayPath, TurnTimeout: 15 * time.Second, SetupTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	models, err := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent tool fixture"}}, "fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	model, _ := models.ClientID("fixture-backend")
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal(err)
	}
	var starts, toolCount atomic.Int32
	handler, err := gateway.New(gateway.Config{Tokens: tokens, Backend: clientToolBackend{driver, models, &starts, &toolCount}, TurnTimeout: 15 * time.Second, FirstEventTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &requestShapeRecorder{}
	observed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			handler.ServeHTTP(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		r.Body = io.NopCloser(bytes.NewReader(body))
		status := &statusRecorder{ResponseWriter: w, status: 200}
		handler.ServeHTTP(status, r)
		recorder.record(body, status.status)
	})
	server := httptest.NewServer(observed)
	defer server.Close()
	version, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}, Args: []string{"--version"}})
	clientVersion, named := launcher.ClientVersionFromOutput(version.Stdout)
	if err != nil || !named {
		t.Fatal("unverified installed client version")
	}
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: userSettings, Executable: executable, Version: clientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	command := profile.Command()
	command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "Read", "--no-session-persistence", "--system-prompt", "Independent client relay exercise.", "Request the fixture tool and then finish.")
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	result, runErr := runner.Run(ctx, command)
	recorder.mu.Lock()
	shapes := append([]string(nil), recorder.shapes...)
	recorder.mu.Unlock()
	t.Logf("client=%s run_err=%v exit=%d completion_marker=%v backend_starts=%d driver_state=%s requests=%d", clientVersion, runErr, result.ExitCode, bytes.Contains(result.Stdout, []byte("independent client relay complete")), starts.Load(), driver.State(), len(shapes))
	for i, shape := range shapes {
		t.Logf("request[%d] %s", i, shape)
	}
}
