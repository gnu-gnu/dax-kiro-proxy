package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

// The 2.10 changelog establishes this setting's existence, while current public documentation
// supplies setter/getter and file-path hypotheses for this pinned, fully synthetic observation:
// https://kiro.dev/changelog/cli/2-10/
// https://kiro.dev/docs/cli/reference/settings/
// Persistence and readback do not establish effective resource suppression in an agent session.
func TestKiroOwnedHomeSettingsDiscovery(t *testing.T) {
	observeKiroHomeSettings(t, kiroSettingsOptions{})
}

// This differential case makes the documented default settings directory available inside the
// synthetic HOME. It must not become a shared store when KIRO_HOME selects another owned root.
func TestKiroOwnedHomeSettingsWithFallbackDirectory(t *testing.T) {
	observeKiroHomeSettings(t, kiroSettingsOptions{fallbackDirectory: true})
}

// Reading independently written A/B values can establish the getter's lookup behavior even when
// the public setter fails. It cannot establish setter success or effective agent resource policy.
func TestKiroOwnedHomeSettingsReadback(t *testing.T) {
	observeKiroHomeSettings(t, kiroSettingsOptions{seededReadback: true})
}

// The account variant permits only version/help/getter commands; settings fixtures remain owned
// and no setter is permitted. Account/cache maintenance inside Kiro is not audited.
func TestKiroAccountHomeSettingsReadback(t *testing.T) {
	observeKiroHomeSettings(t, kiroSettingsOptions{seededReadback: true, accountHome: true})
}

type kiroSettingsOptions struct{ fallbackDirectory, seededReadback, accountHome bool }

func observeKiroHomeSettings(t *testing.T, options kiroSettingsOptions) {
	t.Helper()
	const key = "chat.disableInheritingDefaultResources"
	if options.accountHome && (!options.seededReadback || options.fallbackDirectory) {
		t.Fatal("account HOME is permitted only with independent read-only fixtures")
	}
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for owned HOME settings discovery; no session or prompt")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("expected an absolute Kiro executable")
	}
	root := t.TempDir()
	home, cwd, scratch := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, directory := range []string{home, cwd, scratch, first, second} {
		if os.Mkdir(directory, 0700) != nil {
			t.Fatal("cannot create owned settings-discovery directories")
		}
	}
	if options.seededReadback {
		for index, directory := range []string{first, second} {
			data, err := json.Marshal(map[string]bool{key: index == 0})
			if err != nil || os.Mkdir(filepath.Join(directory, "settings"), 0700) != nil || os.WriteFile(filepath.Join(directory, "settings", "cli.json"), data, 0600) != nil {
				t.Fatal("cannot create independent owned settings readback fixtures")
			}
		}
	}
	fallback := filepath.Join(home, ".kiro")
	if options.fallbackDirectory {
		if os.MkdirAll(filepath.Join(fallback, "settings"), 0700) != nil || os.WriteFile(filepath.Join(fallback, "settings", "cli.json"), []byte("{}"), 0600) != nil {
			t.Fatal("cannot create owned fallback settings fixture")
		}
	}
	childHome := home
	if options.accountHome {
		childHome = os.Getenv("HOME")
		if !filepath.IsAbs(childHome) {
			t.Fatal("account HOME settings readback requires an absolute HOME")
		}
	}
	t.Logf("synthetic_home=%v, fallback_directory_prepared=%v, seeded_readback=%v", !options.accountHome, options.fallbackDirectory, options.seededReadback)
	environment := []string{
		"HOME=" + childHome,
		"PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + scratch,
		"TERM=dumb",
		"LANG=en_US.UTF-8",
	}
	commandLimit, totalLimit := 5*time.Second, 40*time.Second
	if options.accountHome {
		commandLimit, totalLimit = 15*time.Second, 55*time.Second
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, MaxOutputBytes: 32 << 10, Timeout: commandLimit})
	if err != nil {
		t.Fatal("cannot create bounded settings-discovery runner")
	}
	t.Cleanup(func() {
		runner.Close()
		if runner.Active() != 0 {
			t.Error("settings-discovery process ownership was not released")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), totalLimit)
	defer cancel()
	homeBound := 0
	run := func(label, binary, directory string, args []string, captureDiagnostics bool) childproc.Result {
		t.Helper()
		versionCommand := len(args) == 1 && args[0] == "--version"
		helpCommand := len(args) == 2 && args[0] == "settings" && args[1] == "--help"
		getterCommand := len(args) == 4 && args[0] == "settings" && args[1] == key && args[2] == "--format" && args[3] == "json"
		if options.accountHome && !versionCommand && !helpCommand && !getterCommand {
			t.Fatal("account HOME command escaped the read-only allowlist")
		}
		env := append(append([]string(nil), environment...), "KIRO_HOME="+directory)
		command := childproc.Command{Executable: binary, Directory: cwd, Environment: env, Args: args}
		if captureDiagnostics {
			// Only synthetic settings commands merge diagnostics into bounded capture. Paths
			// and arguments stay quoted positional parameters of this fixed shell program.
			command.Executable = "/bin/sh"
			command.Args = append([]string{"-c", `exec "$@" 2>&1`, "dax-settings-discovery", binary}, args...)
		}
		limit := 5 * time.Second
		if options.accountHome && getterCommand {
			limit = commandLimit
		}
		commandContext, commandCancel := context.WithTimeout(ctx, limit)
		defer commandCancel()
		result, runErr := runner.Run(commandContext, command)
		if getterCommand {
			_, exact := kiroSettingsProbeBoolean(result.Stdout)
			t.Logf("command=%s, exact_boolean=%v, json_value=%v", label, exact, json.Valid(result.Stdout))
		}
		groupGone := result.PID == 0 || errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
		cleanupJoined := groupGone && runner.Active() == 0 && !errors.Is(runErr, childproc.ErrCleanup)
		lower := strings.ToLower(string(result.Stdout))
		authenticationMarker, syntaxMarker := false, false
		markers := []string{}
		for _, marker := range []struct{ phrase, label string }{
			{"not logged", "auth-not-logged"},
			{"not signed in", "auth-not-signed-in"},
			{"not authenticated", "auth-not-authenticated"},
			{"unauthenticated", "auth-unauthenticated"},
			{"login required", "auth-login-required"},
			{"log in required", "auth-log-in-required"},
			{"sign in required", "auth-sign-in-required"},
			{"sign-in required", "auth-sign-in-hyphen-required"},
			{"authentication required", "auth-required"},
			{"authentication failed", "auth-failed"},
			{"please log in", "auth-please-log-in"},
			{"please login", "auth-please-login"},
			{"please sign in", "auth-please-sign-in"},
			{"run kiro-cli login", "auth-login-command"},
			{"run `kiro-cli login`", "auth-quoted-login-command"},
			{"unexpected argument", "syntax-unexpected-argument"},
			{"unrecognized subcommand", "syntax-unrecognized-subcommand"},
			{"invalid value", "syntax-invalid-value"},
			{"invalid type", "syntax-invalid-type"},
			{"unknown setting", "syntax-unknown-setting"},
			{"unknown argument", "syntax-unknown-argument"},
			{"no such file or directory", "missing-file-or-directory"},
			{"os error 2", "missing-os-error-2"},
			{"does not exist", "missing-does-not-exist"},
			{"not found", "missing-not-found"},
			{"permission denied", "permission-denied"},
			{"unable", "failure-unable"},
			{"could not", "failure-could-not"},
			{"failed to", "failure-failed-to"},
		} {
			if strings.Contains(lower, marker.phrase) {
				markers = append(markers, marker.label)
				authenticationMarker = authenticationMarker || strings.HasPrefix(marker.label, "auth-")
				syntaxMarker = syntaxMarker || strings.HasPrefix(marker.label, "syntax-")
			}
		}
		t.Logf("command=%s, exit=%d, output_bytes=%d, authentication_marker=%v, syntax_marker=%v, fixed_marker_count=%d, fixed_markers=%v, failure=%s, cleanup_joined=%v", label, result.ExitCode, len(result.Stdout), authenticationMarker, syntaxMarker, len(markers), markers, kiroHomeProbeFailure(runErr), cleanupJoined)
		if !cleanupJoined {
			t.Fatal("owned settings-discovery process group survived cleanup")
		}
		// Measured 2.21.3: the settings command also needs the login store kept under the account
		// HOME, so a synthetic HOME sees only a missing-file diagnostic (D114). The account-HOME
		// readback control covers the product's configuration; an earlier build succeeded here.
		homeBoundShape := !options.accountHome && !versionCommand && !helpCommand && result.ExitCode != 0 && !authenticationMarker && !syntaxMarker && strings.Contains(lower, "no such file or directory")
		if runErr != nil || result.ExitCode != 0 || authenticationMarker || syntaxMarker {
			if strings.HasSuffix(label, "-set") {
				// Inspect only the expected owned paths. A missing or linked parent prevents
				// inspecting its child; no configuration directory is created as a repair.
				parentType := kiroSettingsProbePathType(filepath.Join(directory, "settings"))
				fileType := "uninspected"
				if parentType == "directory" {
					fileType = kiroSettingsProbePathType(filepath.Join(directory, "settings", "cli.json"))
				}
				t.Logf("command=%s, owned_settings_directory_type=%s, owned_settings_file_type=%s", label, parentType, fileType)
				if fileType == "regular" {
					data := readKiroSettingsProbeFile(t, directory)
					var fields map[string]json.RawMessage
					object := json.Unmarshal(data, &fields) == nil && fields != nil
					_, flat := kiroSettingsProbeBoolean(fields["chat.disableInheritingDefaultResources"])
					var chat map[string]json.RawMessage
					_ = json.Unmarshal(fields["chat"], &chat)
					_, nested := kiroSettingsProbeBoolean(chat["disableInheritingDefaultResources"])
					t.Logf("command=%s, failed_setter_owned_file_bytes=%d, root_object=%v, top_level_fields=%d, flat_boolean=%v, nested_boolean=%v, successful_exit=false", label, len(data), object, len(fields), flat, nested)
				}
			}
			if homeBoundShape {
				homeBound++
				return result
			}
			t.Fatal("owned HOME settings command failed; settings discovery remains unverified")
		}
		return result
	}
	for _, binary := range []struct{ label, path, name string }{
		{"main-version", executable, "kiro-cli"},
		{"helper-version", filepath.Join(filepath.Dir(executable), "kiro-cli-chat"), "kiro-cli-chat"},
	} {
		result := run(binary.label, binary.path, first, []string{"--version"}, false)
		verified := launcher.CompatibleKiroOutput(binary.name, result.Stdout)
		t.Logf("command=%s, pinned_version_verified=%v", binary.label, verified)
		if !verified {
			t.Fatal("owned settings probe requires both pinned public binaries")
		}
	}
	for _, binary := range []struct{ label, path string }{{"main", executable}, {"helper", filepath.Join(filepath.Dir(executable), "kiro-cli-chat")}} {
		help := run(binary.label+"-settings-help", binary.path, first, []string{"settings", "--help"}, true)
		positionals := regexp.MustCompile(`(?im)^\s*usage:\s+[^\r\n]*\bsettings\b[^\r\n]*\[KEY\][^\r\n]*\[VALUE\]`).Match(help.Stdout)
		jsonOutput := strings.Contains(string(help.Stdout), "--format") && regexp.MustCompile(`(?m)^\s*- json:`).Match(help.Stdout)
		t.Logf("binary=%s, settings_key_value_positionals_advertised=%v, json_output_advertised=%v", binary.label, positionals, jsonOutput)
		if !positionals || !jsonOutput {
			t.Fatal("public help did not confirm the expected settings positionals and JSON output; no setter was invoked")
		}
	}
	cases := []struct {
		label, directory, literal string
		want                      bool
	}{
		{"first-root", first, "true", true},
		{"second-root", second, "false", false},
	}
	// Complete both writes before either readback so a single shared settings store cannot pass.
	for _, selected := range cases {
		if options.seededReadback {
			continue
		}
		run(selected.label+"-set", executable, selected.directory, []string{"settings", key, selected.literal}, true)
		if options.fallbackDirectory {
			data := readKiroSettingsProbeFile(t, fallback)
			var fields map[string]json.RawMessage
			object := json.Unmarshal(data, &fields) == nil && fields != nil
			_, boolean := kiroSettingsProbeBoolean(fields[key])
			unchanged := string(data) == "{}"
			t.Logf("command=%s-set, fallback_bytes=%d, fallback_object=%v, fallback_setting_boolean=%v, fallback_unchanged=%v", selected.label, len(data), object, boolean, unchanged)
			if !unchanged {
				t.Error("alternate settings root modified the owned fallback fixture")
			}
		}
	}
	for _, selected := range cases {
		result := run(selected.label+"-get", executable, selected.directory, []string{"settings", key, "--format", "json"}, true)
		value, boolean := kiroSettingsProbeBoolean(result.Stdout)
		matches := boolean && value == selected.want
		t.Logf("scope=%s, getter_boolean=%v, getter_matches=%v", selected.label, boolean, matches)
		if homeBound != 0 {
			if matches {
				t.Fatal("synthetic HOME getter succeeded after a login-bound setter diagnostic")
			}
			continue
		}
		if !matches {
			t.Fatal("owned settings getter did not return the expected Boolean; readback remains unverified")
		}
		data := readKiroSettingsProbeFile(t, selected.directory)
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			t.Fatal("owned settings candidate is not a JSON object; storage shape remains unverified")
		}
		flatValue, flat := kiroSettingsProbeBoolean(fields[key])
		var chat map[string]json.RawMessage
		nestedValue, nested := false, false
		if json.Unmarshal(fields["chat"], &chat) == nil && chat != nil {
			nestedValue, nested = kiroSettingsProbeBoolean(chat["disableInheritingDefaultResources"])
		}
		fileMatches := flat != nested && (flat && flatValue == selected.want || nested && nestedValue == selected.want)
		t.Logf("scope=%s, owned_file_bytes=%d, top_level_fields=%d, flat_boolean=%v, nested_boolean=%v, file_matches=%v", selected.label, len(data), len(fields), flat, nested, fileMatches)
		if !fileMatches {
			t.Fatal("owned settings file did not contain one unambiguous expected Boolean")
		}
	}
	expectedHomeBound := 2
	if !options.seededReadback {
		expectedHomeBound += 2
	}
	if homeBound != 0 && homeBound != expectedHomeBound {
		t.Fatal("synthetic HOME settings commands did not share one measured login-bound diagnostic")
	}
	t.Logf("settings_search_root_verified=%v, settings_readback_verified=%v, setter_success_verified=%v, login_bound_home=%v, resource_suppression_verified=false, session_created=false, prompt_sent=false", homeBound == 0 && !t.Failed(), homeBound == 0 && !t.Failed(), homeBound == 0 && !options.seededReadback && !t.Failed(), homeBound != 0)
}

func kiroSettingsProbePathType(path string) string {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent"
	}
	if err != nil {
		return "unavailable"
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	case info.IsDir():
		return "directory"
	case info.Mode().IsRegular():
		return "regular"
	default:
		return "other"
	}
}

func kiroSettingsProbeBoolean(data []byte) (bool, bool) {
	switch strings.TrimSpace(string(data)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func readKiroSettingsProbeFile(t *testing.T, directory string) []byte {
	t.Helper()
	before, err := os.Lstat(directory)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		t.Fatal("owned settings root is unavailable or linked")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal("cannot open owned settings root")
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil || !os.SameFile(before, current) {
		t.Fatal("owned settings root changed during inspection")
	}
	parent, err := root.Lstat("settings")
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		t.Fatal("owned settings directory is unavailable or linked")
	}
	const candidate = "settings/cli.json"
	before, err = root.Lstat(candidate)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 32<<10 {
		t.Fatal("owned settings candidate is absent, linked, nonregular, or oversized")
	}
	file, err := root.OpenFile(candidate, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal("cannot open owned settings candidate without following links")
	}
	defer file.Close()
	current, err = file.Stat()
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(before, current) || current.Size() > 32<<10 {
		t.Fatal("owned settings candidate changed during inspection")
	}
	data, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	if err != nil || len(data) > 32<<10 {
		t.Fatal("owned settings candidate exceeded bounded reading")
	}
	return data
}
