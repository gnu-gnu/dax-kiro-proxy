package interop_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

func TestKiroReadOnlyToolsInventoryProtocol(t *testing.T) {
	root := t.TempDir()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "independent-acp")
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	_, err = runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd,
		Environment: env, Args: []string{"build", "-o", executable, "../acp/testdata/fake"}})
	if err != nil {
		t.Fatal("cannot build independent read-only inventory peer")
	}
	for _, test := range []struct {
		mode           string
		query, success bool
		wantErr        error
	}{
		{"ready", true, true, nil},
		{"listed", true, true, nil},
		{"unavailable", false, false, nil},
		{"foreign", false, false, errInventoryBinding},
		{"malformed", false, false, errInventoryShape},
		{"duplicate", false, false, errInventoryShape},
		{"silent", false, false, context.DeadlineExceeded},
		{"rejected", true, false, nil},
		{"bad-result", true, false, errInventoryShape},
	} {
		t.Run(test.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			client, err := acp.Start(ctx, acp.Config{Executable: executable, Args: []string{"inventory-" + test.mode}, Directory: root,
				ClientInfo: acp.Info{Name: "independent-inventory-test", Version: "1"}, Limits: acp.Limits{RequestTimeout: 2 * time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			report, err := readOnlyToolsInventory(ctx, client, root, 80*time.Millisecond)
			if !errors.Is(err, test.wantErr) || report.QuerySent != test.query || report.Success != test.success || !report.SessionCreated {
				t.Fatalf("unexpected read-only observation: report=%+v, error=%v", report, err)
			}
			if test.mode == "ready" && (report.TextBytes == 0 || !report.NativeNames["fs_read"] || report.ResultKinds["output"] != "string") {
				t.Fatal("preceding diagnostic inventory was not observed before RPC completion")
			}
			if test.mode == "ready" && (report.DataKinds["tools"] != "array" || report.DataSizes["tools"] != 0) {
				t.Fatal("bounded result shape was not observed")
			}
			if test.mode == "listed" && (report.DataSizes["tools"] != 1 || !report.ListedNativeNames["read"] || report.ListedNativeNames["fs_read"] || report.ToolEntryKinds["name"] != "string") {
				t.Fatal("structured inventory was not distinguished from diagnostic text")
			}
			if client.Close() != nil || !errors.Is(syscall.Kill(-client.PID(), 0), syscall.ESRCH) {
				t.Fatal("read-only inventory peer survived cleanup")
			}
		})
	}
}

func TestInventoryDiagnosticBoundsAndPrivacy(t *testing.T) {
	newReport := func() inventoryReport {
		return inventoryReport{NativeNames: map[string]bool{}, ResultKinds: map[string]string{}, NotificationKinds: map[string]int{}}
	}
	r := newReport()
	for i := 0; i < 64; i++ {
		if r.observe(acp.Notification{Method: "fixture-unknown", Params: json.RawMessage(`{}`)}, "fixture-owned") != nil {
			t.Fatal("bounded optional notification was rejected")
		}
	}
	if !errors.Is(r.observe(acp.Notification{Method: "fixture-extra"}, "fixture-owned"), errInventoryLimit) || len(r.NotificationKinds) != 1 {
		t.Fatal("notification limit or fixed diagnostic vocabulary failed")
	}
	r = newReport()
	if !errors.Is(r.observe(acp.Notification{Method: "fixture-large", Params: json.RawMessage(strings.Repeat(" ", (64<<10)+1))}, "fixture-owned"), errInventoryLimit) {
		t.Fatal("oversized notification accepted")
	}
	o := new(inventoryErrorObserver)
	if o.Error(-32700, "Independent parse error", json.RawMessage(`"missing field `+"`command`"+`; fixture-private-value"`)) {
		t.Fatal("parse failure was reclassified as account failure")
	}
	if !o.markers()["missing field `command`"] || len(o.markers()) != 3 {
		t.Fatal("fixed parse markers were not retained")
	}
	for key := range o.markers() {
		if strings.Contains(key, "fixture-private-value") {
			t.Fatal("remote error value entered retained diagnostics")
		}
	}
	if !o.Error(401, "login required", nil) {
		t.Fatal("account failure classification changed")
	}
}

// This opt-in observation uses the normal account HOME and an empty, owned KIRO_HOME/workspace.
// It selects newly authored agents and never sends a model prompt or a mutating slash
// command. Session creation may update Kiro's own account/cache state. No credentials are copied.
func TestKiroPinnedReadOnlyToolsInventory(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for one owned session and advertised tools query; no model prompt")
	}
	for _, test := range []struct {
		name   string
		tools  []string
		listed []string
	}{{"empty", []string{}, []string{}}, {"one-native", []string{"fs_read"}, []string{"read"}}} {
		t.Run(test.name, func(t *testing.T) { observePinnedToolsInventory(t, executable, test.tools, test.listed) })
	}
}

func observePinnedToolsInventory(t *testing.T, executable string, declaredTools, listedTools []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("/private/tmp", "dax-kiro-inventory-")
	if err != nil {
		t.Fatal("cannot create owned inventory root")
	}
	defer os.RemoveAll(root)
	configuration, cwd, scratch := filepath.Join(root, "kiro-home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	for _, dir := range []string{configuration, filepath.Join(configuration, "settings"), cwd, scratch, filepath.Join(cwd, ".kiro"), filepath.Join(cwd, ".kiro", "agents")} {
		if os.Mkdir(dir, 0700) != nil {
			t.Fatal("cannot create owned inventory directories")
		}
	}
	const name = "dax-readonly-inventory"
	agent, err := json.Marshal(map[string]any{"name": name, "description": "Independent read-only protocol observation", "tools": declaredTools, "allowedTools": []string{}, "mcpServers": map[string]any{}, "resources": []any{}, "hooks": map[string]any{}, "includeMcpJson": false})
	if err != nil {
		t.Fatal("cannot encode independent inventory agent")
	}
	if os.WriteFile(filepath.Join(cwd, ".kiro", "agents", name+".json"), agent, 0600) != nil ||
		os.WriteFile(filepath.Join(configuration, "settings", "cli.json"), []byte(`{"chat.disableInheritingDefaultResources":true}`), 0600) != nil {
		t.Fatal("cannot write owned inventory configuration")
	}
	runner, err := childproc.New(childproc.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal("cannot create ephemeral identity key")
	}
	observed := kiroHomeIdentityRunner(func(ctx context.Context, command childproc.Command) (childproc.Result, error) {
		command.Environment = append(append([]string(nil), command.Environment...), "KIRO_HOME="+configuration)
		started := time.Now()
		result, err := runner.Run(ctx, command)
		stage := "version"
		if len(command.Args) > 0 && command.Args[0] == "whoami" {
			stage = "identity"
		}
		t.Logf("preflight_stage=%s, exit=%d, stdout_bytes=%d, deadline=%v, elapsed_ms=%d", stage, result.ExitCode, len(result.Stdout), errors.Is(err, context.DeadlineExceeded), time.Since(started).Milliseconds())
		if result.PID > 0 && !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
			return result, errors.New("inventory preflight group survived cleanup")
		}
		return result, err
	})
	info, err := launcher.CheckKiro(ctx, observed, launcher.KiroConfig{Executable: executable, Home: os.Getenv("HOME"), Directory: cwd, ScopeKey: key})
	if err != nil {
		t.Fatalf("inventory preflight failed: %v", err)
	}
	started := time.Now()
	diagnostics := new(inventoryErrorObserver)
	client, err := acp.Start(ctx, acp.Config{Executable: executable, Directory: cwd, Args: []string{"acp", "--agent", name, "--agent-engine", "v2"},
		Environment: []string{"HOME=" + os.Getenv("HOME"), "KIRO_HOME=" + configuration, "PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + scratch, "TERM=dumb", "LANG=en_US.UTF-8"},
		ClientInfo:  acp.Info{Name: "dax-readonly-inventory", Version: "1"}, Auth: diagnostics, Limits: acp.Limits{RequestTimeout: 15 * time.Second}})
	if err != nil {
		t.Fatalf("inventory ACP initialization failed: %s", kiroSetupFailure(err))
	}
	defer client.Close()
	report, callErr := readOnlyToolsInventory(ctx, client, cwd, 3*time.Second)
	closeErr := client.Close()
	groupGone := errors.Is(syscall.Kill(-client.PID(), 0), syscall.ESRCH)
	runner.Close()
	t.Logf("version=%s, engine=v2, owned_configuration_root=true, session_created=%v, advertised=%v, command_count=%d, tools_available=%v, query_sent=%v, query_success=%v, prompt_sent=false, result_bytes=%d, result_kinds=%v, other_result_fields=%d, notifications=%d, notification_kinds=%v, text_bytes=%d, native_name_presence=%v, execution_restriction_verified=false, cleanup_joined=%v, elapsed_ms=%d", info.Version,
		report.SessionCreated, report.Advertised, report.Commands, report.ToolsAvailable, report.QuerySent, report.Success, report.ResultBytes, report.ResultKinds, report.UnknownResultFields, report.Notifications, report.NotificationKinds, report.TextBytes, report.NativeNames,
		closeErr == nil && groupGone && runner.Active() == 0, time.Since(started).Milliseconds())
	t.Logf("declared_tool_count=%d, data_kinds=%v, data_container_sizes=%v, tool_entry_kinds=%v, listed_native_names=%v", len(declaredTools), report.DataKinds, report.DataSizes, report.ToolEntryKinds, report.ListedNativeNames)
	if closeErr != nil || !groupGone || runner.Active() != 0 {
		t.Fatal("inventory cleanup did not join all owners")
	}
	if callErr != nil {
		t.Logf("private_error_markers=%v", diagnostics.markers())
		t.Fatalf("read-only inventory failed: %v", callErr)
	}
	if !report.SessionCreated || !report.ToolsAvailable || !report.QuerySent || !report.Success {
		t.Fatal("read-only command availability and successful result are not established")
	}
	if report.DataKinds["tools"] != "array" || report.DataSizes["tools"] != len(declaredTools) {
		t.Fatal("declared and listed tool counts differ")
	}
	for _, name := range listedTools {
		if !report.ListedNativeNames[name] {
			t.Fatal("declared native tool is not the listed native tool")
		}
	}
}
