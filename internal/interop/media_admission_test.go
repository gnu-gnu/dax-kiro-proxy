package interop_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/requestfamily"
	"dax-kiro-proxy/internal/session"
)

// Public streaming user input carries 20 images, one new image, then a text-only question.
// The actual client and gateway must preserve complete request history while ACP sees only the
// first 20, the new one, then no images. No client tool or actual Kiro process is involved.
func TestClaudeMediaHistoryUsesOnlyNewACPImages(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for local image history control; no model credits")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("client executable must be absolute")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-media-history-")
	if err != nil {
		t.Fatal("cannot create owned native media root")
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	if os.MkdirAll(filepath.Join(home, ".claude"), 0700) != nil || os.Mkdir(project, 0700) != nil {
		t.Fatal("cannot create owned native media workspace")
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if os.WriteFile(settings, []byte(`{"disableAllHooks":true}`), 0600) != nil {
		t.Fatal("cannot prepare owned client policy")
	}
	global := filepath.Join(home, ".claude.json")
	if os.WriteFile(global, []byte(`{}`), 0600) != nil {
		t.Fatal("cannot prepare owned client state")
	}
	before := [2][32]byte{fileFingerprint(t, settings), fileFingerprint(t, global)}
	runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 4096})
	if err != nil {
		t.Fatal("cannot prepare native control runner")
	}
	defer runner.Close()
	v, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "DISABLE_AUTOUPDATER=1"}})
	version, ok := launcher.ClientVersionFromOutput(v.Stdout)
	if err != nil || !ok || version != launcher.SupportedClientVersion {
		t.Fatal("media control requires the measured client")
	}
	peer := buildDenialACPFixture(t, t.Context(), runner, root)
	witness := filepath.Join(root, "acp-media-events")
	driver, err := session.New(session.Config{Process: acp.Config{Executable: peer, Directory: project, Args: []string{"media-history-peer", witness}, ClientInfo: acp.Info{Name: "native-media-control", Version: "1"}}, TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare independent media ACP driver")
	}
	defer driver.Close()
	models, err := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Owned media fixture"}}, "fixture-backend")
	if err != nil {
		t.Fatal("cannot prepare independent model catalog")
	}
	model, _ := models.ClientID("fixture-backend")
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("cannot prepare local authentication")
	}
	actual, err := gateway.New(gateway.Config{Tokens: tokens, Backend: driver, TurnTimeout: 5 * time.Second, FirstEventTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare actual media gateway")
	}
	var picture bytes.Buffer
	if png.Encode(&picture, image.NewGray(image.Rect(0, 0, 2, 2))) != nil {
		t.Fatal("cannot encode independent native image")
	}
	data := base64.StdEncoding.EncodeToString(picture.Bytes())
	digest := sha256.Sum256([]byte(data))
	imageBlock := map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": data}}
	var mu sync.Mutex
	requests := 0
	var counts []int
	invalid := false
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tokens.Model && r.Header.Get("x-api-key") != tokens.Model {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": 1})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Owned media fixture"}}})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		requests++
		if requests > 4 {
			invalid = true
			w.WriteHeader(429)
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(r.Body, anthropic.MaxBodyBytes+1))
		input, decodeErr := anthropic.DecodeRequest(body)
		if readErr != nil || decodeErr != nil || input.Model != model {
			invalid = true
			w.WriteHeader(400)
			return
		}
		if requestfamily.Classify(input) == requestfamily.Title {
			writeObservedMessage(w, input.Stream, model, []map[string]any{{"type": "text", "text": `{"title":"Owned media history"}`}}, "end_turn")
			return
		}
		if len(counts) >= 3 {
			invalid = true
			w.WriteHeader(400)
			return
		}
		count := 0
		for _, message := range input.Messages {
			for _, block := range message.Content {
				if media, ok := block.Media(); ok {
					if media.Kind != "image" || media.MIME != "image/png" || media.Data != data {
						invalid = true
						w.WriteHeader(400)
						return
					}
					count++
				}
			}
		}
		counts = append(counts, count)
		r.Body = io.NopCloser(bytes.NewReader(body))
		actual.ServeHTTP(w, r)
	}))
	server.Config.ReadHeaderTimeout = time.Second
	server.Config.ReadTimeout = 6 * time.Second
	server.Config.WriteTimeout = 7 * time.Second
	server.Config.MaxHeaderBytes = 16 << 10
	server.Start()
	defer server.Close()
	profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: version, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
	if err != nil {
		t.Fatal("cannot prepare isolated native media profile")
	}
	defer profile.Close()
	command := profile.Command()
	command.Args = append(command.Args, "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--system-prompt", "Independent local media continuity control.")
	stdin, send, err := os.Pipe()
	if err != nil {
		t.Fatal("cannot prepare owned client input")
	}
	defer stdin.Close()
	defer send.Close()
	read, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal("cannot prepare owned client output")
	}
	defer read.Close()
	defer stdout.Close()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("cannot prepare discarded client stderr")
	}
	defer null.Close()
	owner, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 30 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare bounded native media owner")
	}
	defer owner.Close()
	process, err := owner.Start(t.Context(), command, childproc.AttachedIO{Stdin: stdin, Stdout: stdout, Stderr: null})
	if err != nil {
		t.Fatal("cannot start isolated media client")
	}
	defer process.Close()
	stdin.Close()
	stdout.Close()
	reader := bufio.NewReaderSize(read, 64<<10)
	frames := 0
	receive := func(wanted string) map[string]json.RawMessage {
		t.Helper()
		if read.SetReadDeadline(time.Now().Add(8*time.Second)) != nil {
			t.Fatal("cannot bound media client read")
		}
		for frames < 128 {
			frames++
			line, err := reader.ReadSlice('\n')
			if err != nil {
				t.Fatalf("media client frame missing or excessive: frames=%d", frames)
			}
			var fields map[string]json.RawMessage
			var kind string
			if json.Unmarshal(line, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
				t.Fatal("invalid native media output")
			}
			if kind == wanted {
				return fields
			}
		}
		t.Fatal("native media frame count exceeded")
		return nil
	}
	write := func(value any) {
		t.Helper()
		if send.SetWriteDeadline(time.Now().Add(3*time.Second)) != nil || json.NewEncoder(send).Encode(value) != nil {
			t.Fatal("cannot submit bounded client media input")
		}
	}
	write(map[string]any{"type": "control_request", "request_id": "owned-media-init", "request": map[string]string{"subtype": "initialize"}})
	init := receive("control_response")
	var response struct {
		Subtype string
		ID      string `json:"request_id"`
	}
	if json.Unmarshal(init["response"], &response) != nil || response.Subtype != "success" || response.ID != "owned-media-init" {
		t.Fatal("native media initialization failed")
	}
	acpPID := 0
	for step, n := range []int{20, 1, 0} {
		content := []any{map[string]string{"type": "text", "text": "Continue the owned media exercise."}}
		for range n {
			content = append(content, imageBlock)
		}
		write(map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}})
		result := receive("result")
		var failed bool
		var text, subtype string
		if json.Unmarshal(result["is_error"], &failed) != nil || failed || json.Unmarshal(result["subtype"], &subtype) != nil || subtype != "success" || json.Unmarshal(result["result"], &text) != nil {
			t.Fatalf("native media completion failed: step=%d", step)
		}
		var facts struct {
			PID, Prompts int
			Parts        []struct {
				Type, MIME, Digest string
				Bytes              int
			}
		}
		if json.Unmarshal([]byte(text), &facts) != nil || facts.PID <= 1 {
			t.Fatal("missing independent ACP media facts")
		}
		if acpPID == 0 {
			acpPID = facts.PID
		}
		images := 0
		for _, part := range facts.Parts {
			if part.Type == "image" {
				images++
				if part.MIME != "image/png" || part.Bytes != len(data) || part.Digest != hex.EncodeToString(digest[:]) {
					t.Fatal("ACP image bytes changed")
				}
			}
		}
		if facts.PID != acpPID || facts.Prompts != step+1 || images != n {
			t.Fatal("native media history replayed or lost backend continuity")
		}
		t.Logf("step=%d submitted_images=%d ACP_images=%d ACP_prompts=%d same_owner=true completion=true", step, n, images, facts.Prompts)
	}
	send.Close()
	result, err := process.Wait()
	if err != nil || result.ExitCode != 0 || !errors.Is(syscall.Kill(-process.PID(), 0), syscall.ESRCH) {
		t.Fatal("native media client cleanup failed")
	}
	if driver.Close() != nil || !errors.Is(syscall.Kill(-acpPID, 0), syscall.ESRCH) {
		t.Fatal("native media backend cleanup failed")
	}
	mu.Lock()
	good := !invalid && len(counts) == 3 && counts[0] == 20 && counts[1] == 21 && counts[2] == 21
	t.Logf("client=%s model_requests=%d history_image_counts=%v all_groups_joined=true", version, requests, counts)
	mu.Unlock()
	if !good {
		t.Fatal("actual client did not retain complete media history")
	}
	if fileFingerprint(t, settings) != before[0] || fileFingerprint(t, global) != before[1] {
		t.Fatal("native media control changed source settings")
	}
	if profile.Close() != nil {
		t.Fatal("native media profile cleanup failed")
	}
	if _, err := os.Stat(profile.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("native media profile retained")
	}
}
