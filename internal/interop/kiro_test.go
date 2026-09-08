package interop_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

// This probe requests only version and existing login identity. It does not start ACP/chat, change
// authentication, execute a client tool or retain account values in its report.
func TestKiroReadOnlyPreflightSurface(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for a read-only installed CLI probe; no model request")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("expected an absolute Kiro executable")
	}
	runner, err := childproc.New(childproc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	scratch := t.TempDir()
	command := childproc.Command{Executable: executable, Directory: scratch, Environment: []string{"HOME=" + os.Getenv("HOME"), "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"}}
	command.Args = []string{"--version"}
	version, err := runner.Run(context.Background(), command)
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	text := strings.TrimSpace(string(version.Stdout))
	if !regexp.MustCompile(`^kiro-cli [0-9]+\.[0-9]+\.[0-9]+$`).MatchString(text) {
		t.Fatal("unrecognized version output shape")
	}
	t.Logf("version=%s", text)
	command.Args = []string{"whoami", "--format", "json"}
	identity, runErr := runner.Run(context.Background(), command)
	firstLine, _, _ := strings.Cut(string(identity.Stdout), "\n")
	firstJSON := []byte(firstLine)
	var value any
	rootKind := "non-json"
	if json.Unmarshal(identity.Stdout, &value) == nil {
		switch value.(type) {
		case nil:
			rootKind = "null"
		case string:
			rootKind = "string"
		case []any:
			rootKind = "array"
		case map[string]any:
			rootKind = "object"
		case bool:
			rootKind = "boolean"
		case float64:
			rootKind = "number"
		}
	}
	var fields map[string]json.RawMessage
	object := json.Unmarshal(identity.Stdout, &fields) == nil && fields != nil
	firstObject := false
	if !object {
		firstObject = json.Unmarshal(firstJSON, &fields) == nil && fields != nil
	}
	keys := []string{}
	fieldName := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]{0,63}$`)
	if object || firstObject {
		for key, value := range fields {
			if !fieldName.MatchString(key) {
				key = "unrecognized-field"
			}
			kind := "unknown"
			if len(value) > 0 {
				switch value[0] {
				case '"':
					kind = "string"
				case '{':
					kind = "object"
				case '[':
					kind = "array"
				case 't', 'f':
					kind = "boolean"
				case 'n':
					kind = "null"
				default:
					kind = "number"
				}
			}
			keys = append(keys, key+":"+kind)
		}
	}
	sort.Strings(keys)
	t.Logf("whoami_exit=%d, root_kind=%s, stdout_bytes=%d, object=%v, fields=%v, command_error=%v", identity.ExitCode, rootKind, len(identity.Stdout), object, keys, runErr)
	t.Logf("whoami_output_shape=%v", kiroOutputShape(identity.Stdout))
	t.Logf("first_line_object=%v, first_line_fields=%v", firstObject, keys)
	command.Executable = filepath.Join(filepath.Dir(executable), "kiro-cli-chat")
	command.Args = []string{"--version"}
	helperVersion, helperErr := runner.Run(t.Context(), command)
	if helperErr != nil || strings.TrimSpace(string(helperVersion.Stdout)) != "kiro-cli-chat 2.21.1" {
		t.Log("matching helper unavailable")
		return
	}
	command.Args = []string{"whoami", "--format", "json"}
	helperIdentity, helperErr := runner.Run(t.Context(), command)
	var helperFields map[string]json.RawMessage
	helperObject := json.Unmarshal(helperIdentity.Stdout, &helperFields) == nil && helperFields != nil
	identityPresent := false
	if helperObject {
		var email, accountType string
		identityPresent = json.Unmarshal(helperFields["email"], &email) == nil && json.Unmarshal(helperFields["accountType"], &accountType) == nil && email != "" && accountType != ""
	}
	t.Logf("matching_helper_exit=%d, object=%v, identity_fields_nonempty=%v, stdout_bytes=%d, output_shape=%v, safe_error=%v", helperIdentity.ExitCode, helperObject, identityPresent, len(helperIdentity.Stdout), kiroOutputShape(helperIdentity.Stdout), helperErr)
}

func kiroOutputShape(raw []byte) map[string]any {
	text := string(raw)
	lower := strings.ToLower(text)
	markers := []string{}
	for _, word := range []string{"not logged in", "logged in", "login required", "expired", "warning", "error", "update", "kiro-cli-chat", "not found", "json"} {
		if strings.Contains(lower, word) {
			markers = append(markers, word)
		}
	}
	lineKinds := []string{}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if len(lineKinds) == 16 {
			break
		}
		line = strings.TrimSpace(line)
		kind := "text"
		if line == "" {
			kind = "empty"
		} else if json.Valid([]byte(line)) {
			kind = "json-value"
		}
		lineKinds = append(lineKinds, kind)
	}
	return map[string]any{"markers": markers, "ansi": strings.ContainsRune(text, 27), "line_kinds": lineKinds}
}

func TestKiroPinnedLoginPreflight(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for the read-only pinned login adapter")
	}
	runner, err := childproc.New(childproc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	var scopeKey [32]byte
	if _, err := rand.Read(scopeKey[:]); err != nil {
		t.Fatal(err)
	}
	info, err := launcher.CheckKiro(t.Context(), runner, launcher.KiroConfig{Executable: executable, Home: os.Getenv("HOME"), Directory: t.TempDir(), ScopeKey: scopeKey})
	if err != nil {
		t.Fatalf("pinned read-only login preflight failed: %v", err)
	}
	t.Logf("version=%s, identity_verified=true, private_scope_present=%v, bounded_postamble=%v", info.Version, len(info.ProfileScope) == 64, info.HadPostamble)
}
