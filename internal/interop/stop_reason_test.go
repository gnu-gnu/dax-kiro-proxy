package interop_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/session"
)

type stoppedRequest struct {
	Messages, FirstAnswerCopies            int
	LastRole                               string
	InitialQuestion, Decoded, NextQuestion bool
}

// Observe the unmodified client's handling of a completed text response whose sampling loop paused.
// The local server admits at most three requests and records structural facts, never prompt bodies.
func TestClaudePausedTextResponseObservation(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for the local stop-reason observation; no model credits")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("client executable must be absolute")
	}
	for _, stop := range []string{"end_turn", "pause_turn", "acp-limit"} {
		t.Run(stop, func(t *testing.T) {
			root, err := os.MkdirTemp("/private/tmp", "dax-stop-observation-")
			if err != nil {
				t.Fatal("cannot create owned observation root")
			}
			defer os.RemoveAll(root)
			home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
			for _, dir := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "projects"), project} {
				if os.Mkdir(dir, 0700) != nil {
					t.Fatal("cannot create owned client workspace")
				}
			}
			settings := filepath.Join(home, ".claude", "settings.json")
			if os.WriteFile(settings, []byte(`{"permissions":{"defaultMode":"manual"}}`), 0600) != nil {
				t.Fatal("cannot prepare owned client policy")
			}
			before := fileFingerprint(t, settings)
			runner, err := childproc.New(childproc.Config{Timeout: 20 * time.Second, MaxOutputBytes: 128 << 10})
			if err != nil {
				t.Fatal("cannot prepare client runner")
			}
			defer runner.Close()
			version, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "DISABLE_AUTOUPDATER=1"}})
			clientVersion, verified := launcher.ClientVersionFromOutput(version.Stdout)
			if err != nil || !verified || clientVersion != launcher.SupportedClientVersion {
				t.Fatal("the stop-reason control requires the measured client")
			}
			tokens, err := gateway.NewTokens()
			if err != nil {
				t.Fatal("cannot prepare local authentication")
			}
			models, err := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Owned stop fixture"}}, "fixture-backend")
			if err != nil {
				t.Fatal("cannot prepare local model catalog")
			}
			model, _ := models.ClientID("fixture-backend")
			const question = "Answer the independent completion exercise."
			const first = "A copper triangle marks the completed first segment."
			const final = "The independent continuation is complete."
			const nextQuestion = "Continue the independent completion exercise."
			var backend *session.Driver
			var actual http.Handler
			witness := filepath.Join(root, "backend-owner")
			if stop == "acp-limit" {
				peer := buildDenialACPFixture(t, t.Context(), runner, root)
				backend, err = session.New(session.Config{Process: acp.Config{Executable: peer, Directory: project, Args: []string{"stop-peer-max_turn_requests", witness}, ClientInfo: acp.Info{Name: "stop-observation", Version: "1"}}, TurnTimeout: 5 * time.Second})
				if err != nil {
					t.Fatal("cannot prepare independent ACP driver")
				}
				defer backend.Close()
				actual, err = gateway.New(gateway.Config{Tokens: tokens, Backend: backend, TurnTimeout: 5 * time.Second, FirstEventTimeout: 3 * time.Second})
				if err != nil {
					t.Fatal("cannot prepare actual completion gateway")
				}
			}
			var mu sync.Mutex
			var observed []stoppedRequest
			requests, invalid := 0, false
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+tokens.Model && r.Header.Get("x-api-key") != tokens.Model {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" && r.URL.Path == "/v1/models" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": model, "display_name": "Owned stop fixture"}}})
					return
				}
				if r.Method == "POST" && r.URL.Path == "/v1/messages/count_tokens" {
					_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": 1})
					return
				}
				if r.Method != "POST" || r.URL.Path != "/v1/messages" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				requests++
				if requests > 3 {
					invalid = true
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				body, readErr := io.ReadAll(io.LimitReader(r.Body, anthropic.MaxBodyBytes+1))
				var request struct {
					Model    string `json:"model"`
					Stream   bool   `json:"stream"`
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if readErr != nil || len(body) > anthropic.MaxBodyBytes || json.Unmarshal(body, &request) != nil || request.Model != model || len(request.Messages) == 0 || len(request.Messages) > 4096 {
					invalid = true
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, decodeErr := anthropic.DecodeRequest(body)
				facts := stoppedRequest{Messages: len(request.Messages), Decoded: decodeErr == nil}
				for _, message := range request.Messages {
					var text string
					if json.Unmarshal(message.Content, &text) != nil {
						var blocks []struct{ Type, Text string }
						if json.Unmarshal(message.Content, &blocks) != nil {
							invalid = true
						}
						for _, block := range blocks {
							if block.Type == "text" {
								text += block.Text
							}
						}
					}
					switch message.Role {
					case "user":
						facts.InitialQuestion = facts.InitialQuestion || strings.Contains(text, question)
						facts.NextQuestion = facts.NextQuestion || strings.Contains(text, nextQuestion)
						facts.LastRole = "user"
					case "assistant":
						if text == first {
							facts.FirstAnswerCopies++
						}
						facts.LastRole = "assistant"
					case "system":
					default:
						invalid = true
					}
				}
				observed = append(observed, facts)
				if actual != nil {
					r.Body = io.NopCloser(bytes.NewReader(body))
					actual.ServeHTTP(w, r)
					return
				}
				answer, reason := first, stop
				if requests > 1 {
					answer, reason = final, "end_turn"
				}
				writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "text", "text": answer}}, reason)
			}))
			server.Config.ReadHeaderTimeout = time.Second
			server.Config.ReadTimeout = 5 * time.Second
			server.Config.WriteTimeout = 6 * time.Second
			server.Config.MaxHeaderBytes = 16 << 10
			server.Start()
			defer server.Close()
			profile, err := launcher.PrepareClient(launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: executable, Version: clientVersion, Model: model, GatewayURL: server.URL, ModelToken: tokens.Model, KeepHistory: true, Environment: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}})
			if err != nil {
				t.Fatal("cannot prepare isolated client profile")
			}
			defer profile.Close()
			var identity string
			for stage, input := range []string{question, nextQuestion} {
				command := profile.Command()
				command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--system-prompt", "Independent local completion observation.")
				if stage == 1 {
					command.Args = append(command.Args, "--resume", identity)
				}
				command.Args = append(command.Args, input)
				result, runErr := runner.Run(t.Context(), command)
				mu.Lock()
				facts := append([]stoppedRequest(nil), observed...)
				count, bad := requests, invalid
				mu.Unlock()
				var response struct {
					Type, Subtype, Result string
					IsError               bool   `json:"is_error"`
					SessionID             string `json:"session_id"`
				}
				parsed := json.Unmarshal(result.Stdout, &response) == nil
				firstVisible, finalVisible := bytes.Contains(result.Stdout, []byte(first)), bytes.Contains(result.Stdout, []byte(final))
				cleaned := result.PID > 1 && errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
				t.Logf("stage=%d client=%s requests=%d exit=%d run_failed=%v parsed=%v response_error=%v first_visible=%v final_visible=%v group_joined=%v", stage, clientVersion, count, result.ExitCode, runErr != nil, parsed, response.IsError, firstVisible, finalVisible, cleaned)
				for i, fact := range facts {
					t.Logf("request=%d messages=%d last_role=%s first_answer_copies=%d initial_question=%v next_question=%v decoder_accepted=%v", i+1, fact.Messages, fact.LastRole, fact.FirstAnswerCopies, fact.InitialQuestion, fact.NextQuestion, fact.Decoded)
				}
				want := first
				if stage == 1 {
					want = final
				}
				if count != stage+1 || len(facts) != count || bad || !cleaned || runErr != nil || !parsed || response.IsError || response.Type != "result" || response.Subtype != "success" || response.Result != want || !nativeHistoryID(response.SessionID) {
					t.Fatal("native completion did not preserve one request per explicit question")
				}
				fact := facts[stage]
				if !fact.Decoded || fact.LastRole != "user" || !fact.InitialQuestion || fact.FirstAnswerCopies != stage || fact.NextQuestion != (stage == 1) || stage == 1 && response.SessionID != identity {
					t.Fatal("native follow-up did not retain exact completed text and original ownership")
				}
				identity = response.SessionID
			}
			if fileFingerprint(t, settings) != before {
				t.Fatal("native stop observation changed source settings")
			}
			if backend != nil {
				raw, err := os.ReadFile(witness)
				var facts struct {
					PID, Prompts int
					SecondDelta  bool
				}
				if err != nil || len(raw) > 1024 || json.Unmarshal(raw, &facts) != nil || facts.PID <= 1 || facts.Prompts != 2 || !facts.SecondDelta || backend.State() != session.Idle {
					t.Fatal("native continuation did not reuse the ACP owner with only its new delta")
				}
				if backend.Close() != nil || !errors.Is(syscall.Kill(-facts.PID, 0), syscall.ESRCH) {
					t.Fatal("native completion retained its ACP process group")
				}
				t.Log("ACP prompts=2 delta_only=true owner_reused=true group_joined=true")
			}
			if profile.Close() != nil {
				t.Fatal("profile cleanup failed")
			}
			if _, err := os.Stat(profile.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("owned profile survived cleanup")
			}
		})
	}
}
