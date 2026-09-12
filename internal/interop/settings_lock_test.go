package interop_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/launcher"
)

// This observation uses only an unmodified client, independently authored settings and a local
// response. A held candidate lock is compared with an active unlocked writer; no account or model
// service is involved. The observation does not assume that every native write uses one lock.
func TestClaudeGlobalSettingsLockObservation(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY; owned local settings observation, no model credits")
	}
	root := t.TempDir()
	runner, err := childproc.New(childproc.Config{Timeout: 15 * time.Second, MaxOutputBytes: 128 << 10})
	if err != nil {
		t.Fatal("cannot prepare bounded client runner")
	}
	defer runner.Close()
	version, err := runner.Run(t.Context(), childproc.Command{Executable: executable, Directory: root, Args: []string{"--version"}, Environment: []string{"HOME=" + root, "PATH=/usr/bin:/bin", "DISABLE_AUTOUPDATER=1"}})
	observed, ok := launcher.ClientVersionFromOutput(version.Stdout)
	if err != nil || !ok {
		t.Fatal("unverified client version")
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		t.Fatal("cannot prepare local credential")
	}
	const model = "claude-dax-lock-observation-0123456789abcdef"
	const answer = "independent settings lock observation complete"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tokens.Model {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": model, "display_name": "Owned lock fixture"}}})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil || requests.Add(1) > 4 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeObservedMessageID(w, string(body["stream"]) == "true", model, "msg_owned_lock", []map[string]any{{"type": "text", "text": answer}}, "end_turn")
	}))
	defer server.Close()
	for _, mode := range []string{"unlocked", "directory", "file", "released_directory"} {
		t.Run(mode, func(t *testing.T) {
			home := filepath.Join(root, mode)
			project, scratch := filepath.Join(home, "project"), filepath.Join(home, "tmp")
			for _, dir := range []string{home, filepath.Join(home, ".claude"), project, scratch} {
				if os.Mkdir(dir, 0700) != nil {
					t.Fatal("cannot prepare owned directories")
				}
			}
			const seed = `{"numStartups":3,"hasCompletedOnboarding":true}`
			source := filepath.Join(home, ".claude.json")
			if os.WriteFile(source, []byte(seed), 0600) != nil || os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0600) != nil {
				t.Fatal("cannot prepare owned settings")
			}
			lock := source + ".lock"
			if (mode == "directory" || mode == "released_directory") && os.Mkdir(lock, 0700) != nil || mode == "file" && os.WriteFile(lock, nil, 0600) != nil {
				t.Fatal("cannot prepare held lock")
			}
			var released chan bool
			if mode == "released_directory" {
				ownedLock, err := os.Lstat(lock)
				if err != nil {
					t.Fatal("cannot observe held lock")
				}
				released = make(chan bool, 1)
				time.AfterFunc(time.Second, func() {
					before, readErr := readDenialArtifact(home, ".claude.json", 2<<20)
					current, lockErr := os.Lstat(lock)
					preserved := readErr == nil && string(before) == seed && lockErr == nil && os.SameFile(ownedLock, current)
					removed := false
					if lockErr == nil && os.SameFile(ownedLock, current) {
						removed = os.Remove(lock) == nil
					}
					released <- preserved && removed
				})
			}
			started := time.Now()
			result, runErr := runner.Run(t.Context(), childproc.Command{
				Executable: executable, Directory: project,
				Args:        []string{"--print", "--output-format", "json", "--model", model, "--strict-mcp-config", "--tools", "", "--no-session-persistence", "--system-prompt", "Independent local settings exercise.", "Return the local fixture response."},
				Environment: []string{"HOME=" + home, "TMPDIR=" + scratch, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=en_US.UTF-8", "TERM=dumb", "ANTHROPIC_BASE_URL=" + server.URL, "ANTHROPIC_AUTH_TOKEN=" + tokens.Model, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "NO_PROXY=127.0.0.1,localhost"},
			})
			after, readErr := readDenialArtifact(home, ".claude.json", 2<<20)
			_, lockErr := os.Lstat(lock)
			changed := sha256.Sum256(after) != sha256.Sum256([]byte(seed))
			completed := bytes.Contains(result.Stdout, []byte(answer))
			t.Logf("version=%s mode=%s settings_changed=%v lock_present=%v completed=%v run_error=%v exit=%d elapsed_ms=%d", observed, mode, changed, lockErr == nil, completed, runErr != nil, result.ExitCode, time.Since(started).Milliseconds())
			if released != nil {
				preserved := <-released
				t.Logf("held_source_preserved_until_release=%v", preserved)
				if !preserved {
					t.Error("native writer did not preserve the source during the short lock hold")
				}
			}
			if readErr != nil || !json.Valid(after) {
				t.Fatal("owned settings became unreadable")
			}
			if !changed || !completed || runErr != nil || result.ExitCode != 0 {
				t.Fatal("control did not demonstrate the observed successful native writer")
			}
			if (mode == "directory" || mode == "file") != (lockErr == nil) {
				t.Fatal("native writer changed the held lock's presence")
			}
		})
	}
}
