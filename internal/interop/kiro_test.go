package interop_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/childproc"
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
	command := childproc.Command{Executable: executable, Directory: scratch, Environment: []string{"HOME=" + os.Getenv("HOME"), "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"}}
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
	var fields map[string]json.RawMessage
	object := json.Unmarshal(identity.Stdout, &fields) == nil && fields != nil
	keys := []string{}
	fieldName := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]{0,63}$`)
	if object {
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
	t.Logf("whoami_exit=%d, object=%v, fields=%v, command_error=%v", identity.ExitCode, object, keys, runErr)
	if runErr == nil && !object {
		t.Fatal("successful identity command did not return a JSON object")
	}
}
