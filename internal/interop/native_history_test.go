package interop_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

// Native transcript bytes are never edited or decoded. Only independently supplied marker/secret
// presence is checked in bounded disposable files; continued context is observed at public HTTP.
func TestClaudeNativeHistoryAcrossFreshProfiles(t *testing.T) {
	for _, mode := range []string{"product", "reference-candidate", "private-control", "disabled-control"} {
		t.Run(mode, func(t *testing.T) { observeNativeHistory(t, mode) })
	}
}

func nativeHistoryID(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	return err == nil && len(decoded) == 16
}

type nativeHistoryFacts struct {
	requests                    int
	oldUser, oldAnswer, newUser int
	wrong                       bool
}

func observeNativeHistory(t *testing.T, mode string) {
	t.Helper()
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set pinned Claude for disposable native history; no Kiro or external inference")
	}
	root, err := os.MkdirTemp("/private/tmp", "dax-native-history-")
	if err != nil {
		t.Fatal("fixture root")
	}
	defer os.RemoveAll(root)
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	dataRoot := filepath.Join(home, ".claude", "projects")
	for _, path := range []string{home, filepath.Join(home, ".claude"), dataRoot, project, filepath.Join(project, ".claude")} {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("fixture directory")
		}
	}
	settings, global, local := filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json"), filepath.Join(project, ".claude", "settings.local.json")
	for path, content := range map[string]string{settings: `{"disableAllHooks":true,"autoMemoryEnabled":false}`, global: `{}`, local: `{"permissions":{"deny":["Bash","Write","Edit"]}}`} {
		if os.WriteFile(path, []byte(content), 0600) != nil {
			t.Fatal("fixture settings")
		}
	}
	protected := []string{settings, global, local}
	before := make([][32]byte, len(protected))
	for i, path := range protected {
		before[i] = fileFingerprint(t, path)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	runner, err := childproc.New(childproc.Config{Timeout: 15 * time.Second, MaxProcesses: 1, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("runner")
	}
	defer runner.Close()
	v, err := runner.Run(ctx, childproc.Command{Executable: client, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=dumb"}})
	if err != nil || strings.TrimSpace(string(v.Stdout)) != launcher.SupportedClientVersion+" (Claude Code)" {
		t.Fatal("unverified native client")
	}
	const model = "claude-dax-native-history"
	prompts := [2]string{"Remember FirstQuestion_73. Reply briefly.", "Use the earlier conversation for SecondQuestion_79. Reply briefly."}
	answers := [2]string{"StoredPhase_71", "ResumedPhase_79"}
	var tokens [2]gateway.Tokens
	var servers [2]*httptest.Server
	var facts [2]nativeHistoryFacts
	var mu sync.Mutex
	for i := range 2 {
		tokens[i], err = gateway.NewTokens()
		if err != nil {
			t.Fatal("tokens")
		}
		servers[i] = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				w.WriteHeader(404)
				return
			}
			if r.Header.Get("x-api-key") != tokens[i].Model && r.Header.Get("Authorization") != "Bearer "+tokens[i].Model {
				mu.Lock()
				facts[i].wrong = true
				mu.Unlock()
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.Method == "GET" && r.URL.Path == "/v1/models" {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": model, "display_name": "Independent native history"}}})
				return
			}
			if r.Method == "POST" && r.URL.Path == "/v1/messages/count_tokens" {
				_, _ = w.Write([]byte(`{"input_tokens":1}`))
				return
			}
			if r.Method != "POST" || r.URL.Path != "/v1/messages" {
				w.WriteHeader(404)
				return
			}
			body, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			var request struct {
				Model    string
				Stream   bool
				Tools    []json.RawMessage
				Messages []struct {
					Role    string
					Content json.RawMessage
				}
			}
			if e != nil || len(body) > 1<<20 || json.Unmarshal(body, &request) != nil || request.Model != model || len(request.Tools) != 0 {
				mu.Lock()
				facts[i].wrong = true
				mu.Unlock()
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			facts[i].requests++
			for _, m := range request.Messages {
				if m.Role == "user" {
					facts[i].oldUser += bytes.Count(m.Content, []byte("FirstQuestion_73"))
					facts[i].newUser += bytes.Count(m.Content, []byte("SecondQuestion_79"))
				}
				if m.Role == "assistant" {
					facts[i].oldAnswer += bytes.Count(m.Content, []byte(answers[0]))
				}
			}
			facts[i].wrong = facts[i].wrong || bytes.Contains(body, []byte("UnsubmittedHistory_83"))
			admitted := facts[i].requests == 1 && !facts[i].wrong
			mu.Unlock()
			if !admitted {
				w.WriteHeader(400)
				return
			}
			writeObservedMessage(w, request.Stream, model, []map[string]any{{"type": "text", "text": answers[i]}}, "end_turn")
		}))
		servers[i].Config.ReadHeaderTimeout = time.Second
		servers[i].Config.ReadTimeout = 5 * time.Second
		servers[i].Config.WriteTimeout = 5 * time.Second
		servers[i].Config.MaxHeaderBytes = 16 << 10
		servers[i].Start()
		defer servers[i].Close()
	}
	var ids [2]string
	var completed [2]bool
	var paths [2]string
	for i := range 2 {
		cfg := launcher.ClientConfig{RuntimeParent: root, Home: home, Project: project, UserSettings: settings, Executable: client, Version: launcher.SupportedClientVersion, Model: model, GatewayURL: servers[i].URL, ModelToken: tokens[i].Model, Environment: []string{"PATH=/usr/bin:/bin", "TERM=dumb"}, KeepHistory: mode == "product" || mode == "disabled-control"}
		if i == 1 && mode == "product" {
			cfg.ResumeSession = ids[0]
		}
		p, e := launcher.PrepareClient(cfg)
		if e != nil {
			t.Fatal("private profile")
		}
		defer p.Close()
		paths[i] = p.Path()
		if mode == "reference-candidate" {
			if os.Symlink(dataRoot, filepath.Join(p.Path(), "client", "projects")) != nil {
				t.Fatal("owned native-data reference")
			}
		}
		command := p.Command()
		command.Args = append(command.Args, "--print", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--system-prompt", "Follow the user's text-only instruction.")
		command.Environment = append(command.Environment, "CLAUDE_CODE_DISABLE_THINKING=1", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1")
		if mode == "disabled-control" {
			command.Args = append(command.Args, "--no-session-persistence")
		}
		if i == 1 && mode != "product" {
			command.Args = append(command.Args, "--resume", ids[0])
		}
		command.Args = append(command.Args, prompts[i])
		result, runErr := runner.Run(ctx, command)
		var response struct {
			Type, Subtype, Result string
			IsError               bool   `json:"is_error"`
			SessionID             string `json:"session_id"`
			Turns                 int    `json:"num_turns"`
		}
		decoded := json.Unmarshal(result.Stdout, &response) == nil
		completed[i] = runErr == nil && result.ExitCode == 0 && decoded && response.Type == "result" && response.Subtype == "success" && !response.IsError && response.Turns == 1 && response.Result == answers[i] && nativeHistoryID(response.SessionID)
		ids[i] = response.SessionID
		if runner.Active() != 0 || result.PID > 0 && !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
			t.Fatal("native history process survived cleanup")
		}
		if i == 0 && !completed[i] {
			t.Fatal("native first conversation failed")
		}
		if p.Close() != nil {
			t.Fatal("private profile cleanup")
		}
		if i == 0 {
			servers[0].Close()
		}
	}
	files, total, marked, secrets := 0, int64(0), 0, false
	err = filepath.WalkDir(dataRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		files++
		if files > 128 || d.Type()&os.ModeSymlink != 0 {
			return errors.New("history inventory bound")
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			return errors.New("history file shape")
		}
		total += info.Size()
		if total > 8<<20 {
			return errors.New("history byte bound")
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if filepath.Base(path) == ids[0]+".jsonl" && bytes.Contains(data, []byte(answers[0])) {
			marked++
		}
		for _, pair := range tokens {
			secrets = secrets || bytes.Contains(data, []byte(pair.Model)) || bytes.Contains(data, []byte(pair.UI))
		}
		return nil
	})
	sources := true
	for i, path := range protected {
		sources = sources && before[i] == fileFingerprint(t, path)
	}
	removed := true
	for _, path := range paths {
		_, e := os.Lstat(path)
		removed = removed && os.IsNotExist(e)
	}
	mu.Lock()
	observed := facts
	mu.Unlock()
	expectResume := mode == "product" || mode == "reference-candidate"
	valid := completed[0] && completed[1] == expectResume && paths[0] != paths[1] && servers[0].URL != servers[1].URL && sources && removed && err == nil && !secrets && observed[0].requests == 1 && observed[0].oldUser == 1 && observed[0].oldAnswer == 0 && observed[0].newUser == 0 && !observed[0].wrong && !observed[1].wrong
	if expectResume {
		valid = valid && ids[0] == ids[1] && observed[1].requests == 1 && observed[1].oldUser == 1 && observed[1].oldAnswer == 1 && observed[1].newUser == 1 && marked == 1
	} else {
		valid = valid && observed[1].requests == 0 && marked == 0
	}
	t.Logf("mode=%s initial_complete=%v resumed_complete=%v same_native_session=%v first_requests=%d resumed_requests=%d old_user_markers=%d old_assistant_markers=%d new_user_markers=%d native_marked_transcripts=%d inventory_entries=%d inventory_bytes=%d credentials_in_native_data=%v sources_unchanged=%v profiles_removed=%v", mode, completed[0], completed[1], ids[0] == ids[1], observed[0].requests, observed[1].requests, observed[1].oldUser, observed[1].oldAnswer, observed[1].newUser, marked, files, total, secrets, sources, removed)
	if !valid {
		t.Error("native conversation did not satisfy cross-profile persistence control")
	}
}
