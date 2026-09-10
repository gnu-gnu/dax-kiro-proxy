package interop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func imagePixels(img image.Image) [32]byte {
	h := sha256.New()
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			h.Write([]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8), byte(a >> 8)})
		}
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

// Only authored identity/digest observations survive each decoded public request.
type imageHistoryBackend struct {
	*session.Manager
	mu                     sync.Mutex
	models                 *catalog.Catalog
	stage, starts, results int
	path, identity, call   string
	pixels, encoded        [32]byte
	failed                 bool
}

func (b *imageHistoryBackend) Models(context.Context) ([]inference.Model, error) {
	return b.models.List(), nil
}
func (b *imageHistoryBackend) Start(ctx context.Context, r *anthropic.Request) (inference.Turn, error) {
	b.mu.Lock()
	b.starts++
	err := b.check(r)
	b.failed = b.failed || err != nil
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return b.Manager.Start(ctx, r)
}

func (b *imageHistoryBackend) check(r *anthropic.Request) error {
	bad := inference.ErrRequest
	if r == nil || len(r.Messages) > 16 || len(r.Tools) != 1 || b.starts > 2-b.stage {
		return bad
	}
	if b.identity == "" {
		b.identity = r.Identity.Session
	}
	if !nativeHistoryID(b.identity) || r.Identity.Session != b.identity {
		return bad
	}
	for _, block := range r.System {
		for _, marker := range []string{"ImageQuestion_353", "ImageReadDone_359", "ImageFollow_367"} {
			if strings.Contains(block.Text, marker) {
				return bad
			}
		}
	}
	uses, results, images, old, next, answer := 0, 0, 0, 0, 0, 0
	for _, m := range r.Messages {
		for _, block := range m.Content {
			switch block.Type {
			case "text":
				if strings.Contains(block.Text, "ImageQuestion_353") {
					if m.Role != "user" || uses != 0 {
						return bad
					}
					old += strings.Count(block.Text, "ImageQuestion_353")
				}
				if strings.Contains(block.Text, "ImageReadDone_359") {
					if m.Role != "assistant" || results != 1 {
						return bad
					}
					answer += strings.Count(block.Text, "ImageReadDone_359")
				}
				if strings.Contains(block.Text, "ImageFollow_367") {
					if m.Role != "user" || answer != 1 {
						return bad
					}
					next += strings.Count(block.Text, "ImageFollow_367")
				}
			case "tool_use":
				var use struct {
					ID, Name string
					Input    map[string]string
				}
				if m.Role != "assistant" || old != 1 || json.Unmarshal(block.Raw, &use) != nil || use.Name != "Read" || len(use.Input) != 1 || use.Input["file_path"] != b.path {
					return bad
				}
				if b.call == "" {
					b.call = use.ID
				}
				if use.ID == "" || use.ID != b.call {
					return bad
				}
				uses++
			case "tool_result":
				result, err := anthropic.DecodeToolResult(block.Raw)
				if err != nil || m.Role != "user" || uses != 1 || result.ID != b.call || result.IsError {
					return bad
				}
				results++
				for _, c := range result.Content {
					if c.Type == "text" {
						continue
					}
					var part struct {
						Type   string
						Source struct {
							Type      string
							MediaType string `json:"media_type"`
							Data      string
						}
					}
					if json.Unmarshal(c.Raw, &part) != nil || part.Type != "image" || part.Source.Type != "base64" || part.Source.MediaType != "image/png" || len(part.Source.Data) > 64<<10 {
						return bad
					}
					raw, err := base64.StdEncoding.Strict().DecodeString(part.Source.Data)
					if err != nil {
						return bad
					}
					config, err := png.DecodeConfig(bytes.NewReader(raw))
					if err != nil || config.Width != 12 || config.Height != 9 {
						return bad
					}
					img, err := png.Decode(bytes.NewReader(raw))
					if err != nil || imagePixels(img) != b.pixels {
						return bad
					}
					digest := sha256.Sum256(raw)
					if b.encoded != [32]byte{} && b.encoded != digest {
						return bad
					}
					b.encoded = digest
					images++
				}
			default:
				return bad
			}
		}
	}
	want := 0
	if b.stage == 1 || b.starts == 2 {
		want = 1
	}
	if old != 1 || uses != want || results != want || images != want || next != b.stage || answer != b.stage {
		return bad
	}
	b.results += results
	return nil
}

func TestClaudeImageToolResultAndNativeResume(t *testing.T) {
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set pinned Claude for owned image history; local fake ACP only")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-image-history-")
	if err != nil {
		t.Fatal("owned root")
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, p := range []string{filepath.Join(home, ".claude"), project} {
		if os.MkdirAll(p, 0700) != nil {
			t.Fatal("owned directories")
		}
	}
	target := filepath.Join(project, "owned.png")
	picture := image.NewNRGBA(image.Rect(0, 0, 12, 9))
	for y := 0; y < 9; y++ {
		for x := 0; x < 12; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: byte(17*x + 3*y), G: byte(7*x + 23*y), B: byte(211 - 3*x - 5*y), A: 255})
		}
	}
	var pngData bytes.Buffer
	if png.Encode(&pngData, picture) != nil || os.WriteFile(target, pngData.Bytes(), 0600) != nil {
		t.Fatal("owned PNG")
	}
	pixels, sourceEncoded := imagePixels(picture), sha256.Sum256(pngData.Bytes())
	beforeImage := fileFingerprint(t, target)
	pre, post := filepath.Join(root, "pre"), filepath.Join(root, "post")
	settings, global := filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json")
	hooks := map[string]any{}
	for name, path := range map[string]string{"PreToolUse": pre, "PostToolUse": post} {
		hooks[name] = []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": "printf r >> '" + path + "'", "timeout": 2}}}}
	}
	policy, _ := json.Marshal(map[string]any{"autoMemoryEnabled": false, "permissions": map[string]any{"allow": []string{"Read"}}, "hooks": hooks})
	if os.WriteFile(settings, policy, 0600) != nil || os.WriteFile(global, []byte(`{}`), 0600) != nil {
		t.Fatal("owned settings")
	}
	beforeSettings, beforeGlobal := fileFingerprint(t, settings), fileFingerprint(t, global)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: 30 * time.Second, MaxProcesses: 1, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("runner")
	}
	defer runner.Close()
	v, err := runner.Run(ctx, childproc.Command{Executable: client, Directory: root, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, Args: []string{"--version"}})
	if err != nil || strings.TrimSpace(string(v.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified client")
	}
	fake := buildDenialACPFixture(t, ctx, runner, root)
	relay := buildRelayObserver(t)
	proxy := filepath.Join(filepath.Dir(relay), "owned-relay")
	models, _ := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent image history"}}, "fixture-backend")
	model, _ := models.ClientID("fixture-backend")
	var identity, call string
	var encoded [32]byte
	for stage := range 2 {
		dir := filepath.Join(root, strconv.Itoa(stage))
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("stage")
		}
		manifest := filepath.Join(dir, "expect.json")
		spec := map[string]string{"Path": target, "Pixels": hex.EncodeToString(pixels[:]), "Encoded": hex.EncodeToString(sourceEncoded[:])}
		if stage == 1 {
			spec["Encoded"] = hex.EncodeToString(encoded[:])
		}
		data, _ := json.Marshal(spec)
		if os.WriteFile(manifest, data, 0600) != nil {
			t.Fatal("expectation")
		}
		validator, err := schemacheck.New(schemacheck.Config{Executable: proxy, Directory: dir})
		if err != nil {
			t.Fatal("validator")
		}
		defer validator.Close()
		manager, err := session.NewManager(session.ManagerConfig{Session: session.Config{Process: acp.Config{Executable: fake, Args: []string{"native-image-history", strconv.Itoa(stage), manifest}, Directory: dir, ClientInfo: acp.Info{Name: "independent-image-test", Version: "1"}}, Validator: validator, RelayExecutable: relay, TurnTimeout: 20 * time.Second, SetupTimeout: 10 * time.Second}, ProfileScope: "independent-image-test", MaxSessions: 1})
		if err != nil {
			t.Fatal("manager")
		}
		defer manager.Close()
		backend := &imageHistoryBackend{Manager: manager, models: models, stage: stage, path: target, identity: identity, call: call, pixels: pixels, encoded: encoded}
		tokens, err := gateway.NewTokens()
		if err != nil {
			t.Fatal("tokens")
		}
		server, err := gateway.StartServer(ctx, gateway.ServerConfig{MaxConnections: 4, HeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, Gateway: gateway.Config{Tokens: tokens, Backend: backend, TurnTimeout: 20 * time.Second, FirstEventTimeout: 10 * time.Second}})
		if err != nil {
			t.Fatal("gateway")
		}
		defer server.Close()
		profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: client, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: server.URL(), ModelToken: tokens.Model, Environment: []string{"PATH=/usr/bin:/bin", "TERM=dumb"}, KeepHistory: true, ResumeSession: identity})
		if err != nil {
			t.Fatal("profile")
		}
		defer profile.Close()
		question, answer := "ImageQuestion_353: Read the supplied owned image once and then finish.", "ImageReadDone_359"
		if stage == 1 {
			question, answer = "ImageFollow_367: Continue using the completed image result. Do not read any file again.", "ImageResumeDone_373"
		}
		command := profile.Command()
		command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "Read", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--system-prompt", "Follow the current instruction; never repeat a completed tool operation.", question)
		command.Environment = append(command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
		result, runErr := runner.Run(ctx, command)
		var response struct {
			Type, Subtype, Result string
			SessionID             string `json:"session_id"`
			IsError               bool   `json:"is_error"`
		}
		complete := runErr == nil && result.ExitCode == 0 && json.Unmarshal(result.Stdout, &response) == nil && response.Type == "result" && response.Subtype == "success" && !response.IsError && response.Result == answer
		serverErr, managerErr := server.Close(), manager.Close()
		validator.Close()
		profileErr := profile.Close()
		backend.mu.Lock()
		observed := !backend.failed && backend.starts == 2-stage && backend.results == 1 && backend.encoded != [32]byte{} && response.SessionID == backend.identity
		identity, call, encoded = backend.identity, backend.call, backend.encoded
		starts, results := backend.starts, backend.results
		backend.mu.Unlock()
		var facts struct {
			PID, Prompts, Calls, Images int
			Valid                       bool
		}
		factData, factErr := os.ReadFile(manifest + ".facts")
		peer := factErr == nil && json.Unmarshal(factData, &facts) == nil && facts.Valid && facts.Prompts == 1 && facts.Calls == 1-stage && facts.Images == 1
		_, removed := os.Lstat(profile.Path())
		gone := serverErr == nil && managerErr == nil && profileErr == nil && os.IsNotExist(removed) && runner.Active() == 0
		for _, pid := range []int{result.PID, facts.PID} {
			gone = gone && pid > 1 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) && errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
		}
		records, recordErr := relayProcessRecords(relay)
		gone = gone && recordErr == nil && len(records) == stage+1
		for _, record := range records {
			gone = gone && errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH)
		}
		connection, dialErr := net.DialTimeout("tcp", strings.TrimPrefix(server.URL(), "http://"), time.Second)
		if connection != nil {
			connection.Close()
		}
		gone = gone && dialErr != nil
		preBytes, preErr := os.ReadFile(pre)
		postBytes, postErr := os.ReadFile(post)
		once := preErr == nil && postErr == nil && string(preBytes) == "r" && string(postBytes) == "r"
		sources := fileFingerprint(t, settings) == beforeSettings && fileFingerprint(t, global) == beforeGlobal
		t.Logf("stage=%d requests=%d image_results=%d request_history_valid=%v source_encoding_preserved=%v native_image_parts=%d peer_valid=%v complete=%v read_hooks_once=%v sources_unchanged=%v ownership_removed=%v", stage, starts, results, !backend.failed, encoded == sourceEncoded, facts.Images, peer, complete, once, sources, gone)
		if !observed || !peer || !complete || !once || !sources || !gone {
			t.Fatal("native image result or resume failed")
		}
		if stage == 0 {
			if fileFingerprint(t, target) != beforeImage || os.Remove(target) != nil {
				t.Fatal("remove original image before resume")
			}
		} else if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("source image reappeared")
		}
	}
}

func TestImageHistoryObservationRejectsFalsePreservation(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 12, 9))
	var encoded bytes.Buffer
	if png.Encode(&encoded, img) != nil {
		t.Fatal("guard PNG")
	}
	data := base64.StdEncoding.EncodeToString(encoded.Bytes())
	const identity = "361eadc5-4914-4a75-a275-a90d107cb4fd"
	for _, kind := range []string{"valid", "missing-image", "wrong-id", "failed-result", "wrong-role", "different-pixels", "base64-text", "changed-encoding", "duplicate-result", "reordered-answer", "foreign-session", "system-marker"} {
		t.Run(kind, func(t *testing.T) {
			result := map[string]any{"type": "tool_result", "tool_use_id": "owned-image-call", "content": []any{map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": data}}}}
			resultMessage := map[string]any{"role": "user", "content": []any{result}}
			switch kind {
			case "missing-image":
				result["content"] = "image read"
			case "wrong-id":
				result["tool_use_id"] = "foreign-image-call"
			case "failed-result":
				result["is_error"] = true
			case "wrong-role":
				resultMessage["role"] = "assistant"
			case "different-pixels":
				other := image.NewNRGBA(img.Rect)
				other.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
				var buffer bytes.Buffer
				if png.Encode(&buffer, other) != nil {
					t.Fatal("other PNG")
				}
				result["content"] = []any{map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(buffer.Bytes())}}}
			case "base64-text":
				result["content"] = data
			case "duplicate-result":
				resultMessage["content"] = []any{result, result}
			}
			messages := []any{
				map[string]any{"role": "user", "content": "ImageQuestion_353"},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "owned-image-call", "name": "Read", "input": map[string]string{"file_path": "/owned/image.png"}}}},
				resultMessage,
				map[string]any{"role": "assistant", "content": "ImageReadDone_359"},
				map[string]any{"role": "user", "content": "ImageFollow_367"},
			}
			if kind == "reordered-answer" {
				messages[2], messages[3] = messages[3], messages[2]
			}
			body, _ := json.Marshal(map[string]any{"model": "claude-dax-image", "max_tokens": 32, "tools": []any{map[string]any{"name": "Read", "input_schema": map[string]string{"type": "object"}}}, "messages": messages})
			r, err := anthropic.DecodeRequest(body)
			if err != nil {
				t.Fatal("guard request")
			}
			r.Identity.Session = identity
			if kind == "foreign-session" {
				r.Identity.Session = "a68c5dfb-d915-46dd-999c-3345497a0a36"
			}
			if kind == "system-marker" {
				r.System = []anthropic.Block{{Type: "text", Text: "ImageFollow_367"}}
			}
			b := imageHistoryBackend{stage: 1, starts: 1, path: "/owned/image.png", identity: identity, call: "owned-image-call", pixels: imagePixels(img), encoded: sha256.Sum256(encoded.Bytes())}
			if kind == "changed-encoding" {
				b.encoded[0] ^= 1
			}
			err = b.check(r)
			if (err == nil) != (kind == "valid") {
				t.Fatal("image provenance guard accepted false evidence")
			}
		})
	}
}
