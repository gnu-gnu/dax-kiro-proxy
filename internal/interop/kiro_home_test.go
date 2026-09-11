package interop_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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

// This opt-in check compares ephemeral account scopes without copying credentials or querying an
// agent, session, or model. Identity continuity alone does not prove configuration isolation.
func TestKiroOwnedHomeIdentityContinuity(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for bounded owned KIRO_HOME identity checks; no session or prompt")
	}
	home := os.Getenv("HOME")
	if !filepath.IsAbs(executable) || !filepath.IsAbs(home) {
		t.Fatal("identity continuity requires absolute executable and HOME paths")
	}
	root := t.TempDir()
	work, first, second := filepath.Join(root, "work"), filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, directory := range []string{work, first, second} {
		if os.Mkdir(directory, 0700) != nil {
			t.Fatal("cannot create owned identity-check directories")
		}
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal("cannot create ephemeral identity scope key")
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, MaxOutputBytes: 64 << 10, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal("cannot create bounded identity-check runner")
	}
	var groups []int
	t.Cleanup(func() {
		runner.Close()
		if runner.Active() != 0 {
			t.Error("identity-check process ownership was not released")
		}
		for _, pid := range groups {
			if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
				t.Error("identity-check process group survived final cleanup")
			}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 65*time.Second)
	defer cancel()
	helper := filepath.Join(filepath.Dir(executable), "kiro-cli-chat")
	commands := 0
	baseline := ""
	alternateFailed, allEqual := false, true
	for _, selected := range []struct{ label, directory string }{
		{"baseline-before", ""},
		{"first-root", first},
		{"second-root", second},
		{"baseline-after", ""},
	} {
		if selected.directory != "" && alternateFailed {
			t.Logf("scope=%s, skipped=true, reason=earlier-alternate-unverified", selected.label)
			continue
		}
		observed := kiroHomeIdentityRunner(func(callContext context.Context, command childproc.Command) (childproc.Result, error) {
			t.Helper()
			class := ""
			switch {
			case len(command.Args) == 1 && command.Args[0] == "--version" && command.Executable == executable:
				class = "main-version"
			case len(command.Args) == 1 && command.Args[0] == "--version" && command.Executable == helper:
				class = "helper-version"
			case len(command.Args) == 3 && command.Args[0] == "whoami" && command.Args[1] == "--format" && command.Args[2] == "json" && command.Executable == executable:
				class = "identity"
			}
			if class == "" || commands >= 12 {
				t.Fatal("identity-check command escaped its exact bounded allowlist")
			}
			commands++
			command.Environment = append([]string(nil), command.Environment...)
			for _, entry := range command.Environment {
				if strings.HasPrefix(entry, "KIRO_HOME=") {
					t.Fatal("identity-check command supplied an unexpected configuration root")
				}
			}
			if selected.directory != "" {
				command.Environment = append(command.Environment, "KIRO_HOME="+selected.directory)
			}
			result, runErr := runner.Run(callContext, command)
			groupGone := true
			if result.PID > 0 {
				groups = append(groups, result.PID)
				groupGone = errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH)
			}
			cleanupJoined := groupGone && runner.Active() == 0 && !errors.Is(runErr, childproc.ErrCleanup)
			t.Logf("scope=%s, command=%s, exit=%d, output_bytes=%d, failure=%s, cleanup_joined=%v", selected.label, class, result.ExitCode, len(result.Stdout), kiroHomeProbeFailure(runErr), cleanupJoined)
			if !cleanupJoined {
				t.Fatal("identity-check process group survived cleanup")
			}
			return result, runErr
		})
		info, checkErr := launcher.CheckKiro(ctx, observed, launcher.KiroConfig{Executable: executable, Home: home, Directory: work, ScopeKey: key})
		failure := kiroHomeProbeFailure(checkErr)
		if errors.Is(checkErr, launcher.ErrLoginCheck) {
			failure = "identity-unverified"
		} else if errors.Is(checkErr, launcher.ErrKiroVersion) {
			failure = "version"
		}
		verified := checkErr == nil && info.ProfileScope != ""
		if selected.label == "baseline-before" && verified {
			baseline = info.ProfileScope
		}
		equal := verified && baseline != "" && info.ProfileScope == baseline
		t.Logf("scope=%s, identity_verified=%v, baseline_equal=%v, failure=%s", selected.label, verified, equal, failure)
		if !verified || !equal {
			if selected.label == "baseline-before" {
				t.Fatal("initial baseline identity was not established")
			}
			// A failed alternate root stops further alternate checks, but the final baseline
			// still detects a concurrent account change. Its success cannot erase this failure.
			alternateFailed, allEqual = true, false
			t.Errorf("scope=%s: identity continuity was not established", selected.label)
		}
	}
	t.Logf("identity_continuity_verified=%v, agent_selection_verified=false, execution_restriction_verified=false, session_created=false, prompt_sent=false", allEqual)
}

type kiroHomeIdentityRunner func(context.Context, childproc.Command) (childproc.Result, error)

func (r kiroHomeIdentityRunner) Run(ctx context.Context, command childproc.Command) (childproc.Result, error) {
	return r(ctx, command)
}

// This opt-in probe observes only global agent discovery in newly owned directories. Kiro 2.3
// documents KIRO_HOME as relocating global agents and other configuration:
// https://kiro.dev/changelog/cli/2-3/
// It does not establish authentication continuity, agent activation, or execution restrictions.
// On the measured 2.21.3 build the main entry point cannot list agents from a synthetic HOME because
// login is kept under the account HOME (D114); this probe records that diagnostic and still verifies
// search-root selection through the helper. The account-HOME variant verifies both entry points.
func TestKiroOwnedHomeAgentSelection(t *testing.T) {
	observeKiroHomeAgentSelection(t, false)
}

// The account HOME is supplied only to child commands. All newly authored agent files stay in
// owned directories; finding their markers does not prove that unrelated agents or MCP are absent.
func TestKiroAccountHomeAgentSelection(t *testing.T) {
	observeKiroHomeAgentSelection(t, true)
}

func observeKiroHomeAgentSelection(t *testing.T, accountHome bool) {
	t.Helper()
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		if accountHome {
			t.Skip("set DAX_INTEROP_KIRO_BINARY for account HOME and owned agent-root discovery; no session or prompt")
		}
		t.Skip("set DAX_INTEROP_KIRO_BINARY for owned HOME agent-list discovery; no session or prompt")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("expected an absolute Kiro executable")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	scratch := filepath.Join(root, "tmp")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, directory := range []string{home, cwd, scratch, first, second} {
		if os.Mkdir(directory, 0700) != nil {
			t.Fatal("cannot create owned agent-discovery directories")
		}
	}
	markers := []string{"dax-home-probe-fallback", "dax-home-probe-first", "dax-home-probe-second"}
	for index, directory := range []string{filepath.Join(home, ".kiro"), first, second} {
		agents := filepath.Join(directory, "agents")
		if os.MkdirAll(agents, 0700) != nil {
			t.Fatal("cannot create owned global agent directory")
		}
		data, err := json.Marshal(map[string]any{
			"name":        markers[index],
			"description": "Independent configuration discovery probe",
			"tools":       []string{},
			"mcpServers":  map[string]any{},
			"resources":   []string{},
			"hooks":       map[string]any{},
		})
		if err != nil || os.WriteFile(filepath.Join(agents, markers[index]+".json"), data, 0600) != nil {
			t.Fatal("cannot write owned discovery agent")
		}
	}

	commandLimit := 5 * time.Second
	if accountHome {
		// A first account-backed inventory produced a selected marker but did not exit in
		// five seconds. This observation still requires exit success within a finite budget.
		commandLimit = 15 * time.Second
	}
	runner, err := childproc.New(childproc.Config{MaxProcesses: 1, MaxOutputBytes: 32 << 10, Timeout: commandLimit})
	if err != nil {
		t.Fatal("cannot create bounded agent-discovery runner")
	}
	t.Cleanup(func() {
		runner.Close()
		if runner.Active() != 0 {
			t.Error("agent-discovery process ownership was not released")
		}
	})
	totalLimit := 45 * time.Second
	if accountHome {
		totalLimit = 75 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), totalLimit)
	defer cancel()
	childHome := home
	if accountHome {
		childHome = os.Getenv("HOME")
		if !filepath.IsAbs(childHome) {
			t.Fatal("account HOME agent discovery requires an absolute HOME")
		}
	}
	environment := []string{
		"HOME=" + childHome,
		"PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + scratch,
		"TERM=dumb",
		"LANG=en_US.UTF-8",
	}
	binaries := []struct {
		label, path, name string
	}{
		{"main", executable, "kiro-cli"},
		{"helper", filepath.Join(filepath.Dir(executable), "kiro-cli-chat"), "kiro-cli-chat"},
	}
	run := func(command childproc.Command) (childproc.Result, error) {
		t.Helper()
		limit := commandLimit
		if len(command.Args) == 1 && command.Args[0] == "--version" {
			limit = 5 * time.Second
		}
		commandContext, commandCancel := context.WithTimeout(ctx, limit)
		defer commandCancel()
		result, runErr := runner.Run(commandContext, command)
		if errors.Is(runErr, childproc.ErrCleanup) || result.PID > 0 && !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
			t.Fatal("owned agent-discovery process group survived cleanup")
		}
		return result, runErr
	}
	for _, binary := range binaries {
		result, runErr := run(childproc.Command{
			Executable: binary.path, Directory: cwd, Environment: environment, Args: []string{"--version"},
		})
		verified := runErr == nil && result.ExitCode == 0 && launcher.CompatibleKiroOutput(binary.name, result.Stdout)
		t.Logf("binary=%s, pinned_version_verified=%v, exit=%d, output_bytes=%d, failure=%s", binary.label, verified, result.ExitCode, len(result.Stdout), kiroHomeProbeFailure(runErr))
		if !verified {
			t.Fatal("owned HOME probe requires both pinned public binaries")
		}
	}

	for _, binary := range binaries {
		for index, selected := range []struct {
			label, directory string
		}{
			{"home-default", ""},
			{"first-root", first},
			{"second-root", second},
		} {
			if accountHome && index == 0 {
				continue
			}
			env := append([]string(nil), environment...)
			if selected.directory != "" {
				env = append(env, "KIRO_HOME="+selected.directory)
			}
			// Merge only this synthetic probe's streams into bounded memory. The fixed shell
			// program passes every path as a quoted positional argument, never as shell text.
			result, runErr := run(childproc.Command{
				Executable: "/bin/sh", Directory: cwd, Environment: env,
				Args: []string{"-c", `exec "$@" 2>&1`, "dax-home-discovery", binary.path, "agent", "list"},
			})
			present := make([]bool, len(markers))
			for i, marker := range markers {
				present[i] = regexp.MustCompile(`(^|[^a-zA-Z0-9_-])` + regexp.QuoteMeta(marker) + `([^a-zA-Z0-9_-]|$)`).Match(result.Stdout)
			}
			authenticationMarker := false
			lower := strings.ToLower(string(result.Stdout))
			for _, marker := range []string{"not logged in", "login required", "authentication required", "please log in", "run kiro-cli login", "run `kiro-cli login`"} {
				authenticationMarker = authenticationMarker || strings.Contains(lower, marker)
			}
			selectedOnly := present[index]
			for i := range present {
				selectedOnly = selectedOnly && (i == index || !present[i])
			}
			t.Logf("binary=%s, scope=%s, exit=%d, output_bytes=%d, fallback_present=%v, first_present=%v, second_present=%v, selected_only=%v, authentication_marker=%v, failure=%s, cleanup_joined=true", binary.label, selected.label, result.ExitCode, len(result.Stdout), present[0], present[1], present[2], selectedOnly, authenticationMarker, kiroHomeProbeFailure(runErr))
			if !accountHome && binary.label == "main" {
				if result.ExitCode == 0 || !authenticationMarker || present[0] || present[1] || present[2] {
					t.Fatal("synthetic HOME main agent listing differed from the measured login-bound diagnostic")
				}
				continue
			}
			if runErr != nil || result.ExitCode != 0 || authenticationMarker {
				t.Fatal("owned HOME agent listing did not complete without authentication diagnostics; search-root selection remains unverified")
			}
			if !selectedOnly {
				t.Fatal("owned HOME agent inventory did not distinguish the selected search root")
			}
		}
	}
	if accountHome {
		t.Log("owned_marker_search_root_distinction_verified=true, exclusive_inventory_verified=false, authentication_continuity_verified=false, mcp_restriction_verified=false, agent_activation_verified=false, execution_restriction_verified=false, session_created=false, prompt_sent=false")
	} else {
		t.Log("global_agent_search_root_verified=true, helper_only=true, main_login_bound_home=true, authentication_continuity_verified=false, execution_restriction_verified=false, session_created=false, prompt_sent=false")
	}
}

func kiroHomeProbeFailure(err error) string {
	if err == nil {
		return "none"
	}
	for _, known := range []struct {
		err  error
		name string
	}{
		{childproc.ErrCleanup, "cleanup"},
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "canceled"},
		{childproc.ErrOutputLimit, "output-limit"},
		{childproc.ErrIO, "io"},
		{childproc.ErrStart, "start"},
		{childproc.ErrExit, "exit"},
		{childproc.ErrParameters, "parameters"},
		{childproc.ErrClosed, "closed"},
		{childproc.ErrBusy, "capacity"},
	} {
		if errors.Is(err, known.err) {
			return known.name
		}
	}
	return "unclassified"
}
