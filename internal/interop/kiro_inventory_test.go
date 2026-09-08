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
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/toolregistry"
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
		{"mcp-ready", true, true, nil},
		{"mcp-multiple", true, true, nil},
		{"mcp-late", true, true, nil},
		{"mcp-multiple-missing", false, false, context.DeadlineExceeded},
		{"mcp-silent", false, false, context.DeadlineExceeded},
		{"mcp-foreign", false, false, errInventoryBinding},
		{"mcp-other-server", false, false, context.DeadlineExceeded},
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
			required := inventoryPrerequisite{}
			if strings.HasPrefix(test.mode, "mcp-") {
				required = inventoryPrerequisite{WaitForMCP: true, Alias: "fixture_relay_alias"}
			}
			if strings.HasPrefix(test.mode, "mcp-multiple") {
				required.ObservedTools = map[string]string{"dax_session": "fixture_relay_alias", "dax_scope_fixture": "foreign_fixture_alias"}
				required.WaitForAllMCP = true
			}
			if test.mode == "mcp-late" {
				required.ObservedTools = map[string]string{"dax_session": "fixture_relay_alias", "dax_scope_fixture": "foreign_fixture_alias"}
				required.SettleWindow = 80 * time.Millisecond
			}
			report, err := readOnlyToolsInventoryAfter(ctx, client, root, 80*time.Millisecond, required)
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
			if test.mode == "mcp-ready" && (!report.AliasMatched || report.AliasNameForm != "bare" || report.NotificationKinds["_kiro.dev/mcp/server_initialized"] != 1 || report.MCPDeclaredNameMatches != 1) {
				t.Fatal("MCP initialization and the intended relay alias were not observed")
			}
			if test.mode == "mcp-multiple" && (!report.ToolMatches["dax_session"] || !report.ToolMatches["dax_scope_fixture"] || report.MCPMatches["dax_scope_fixture"] != 1) {
				t.Fatal("multiple owned MCP servers were not observed independently")
			}
			if test.mode == "mcp-late" && report.MCPMatches["dax_scope_fixture"] != 1 {
				t.Fatal("bounded observation window missed the post-reply notification")
			}
			if client.Close() != nil || !errors.Is(syscall.Kill(-client.PID(), 0), syscall.ESRCH) {
				t.Fatal("read-only inventory peer survived cleanup")
			}
		})
	}
}

func TestInventoryDiagnosticBoundsAndPrivacy(t *testing.T) {
	for _, required := range []inventoryPrerequisite{
		{WaitForAllMCP: true},
		{ObservedTools: map[string]string{"dax_one": "same_alias", "dax_two": "same_alias"}},
		{ObservedTools: map[string]string{"invalid-source": "alias"}},
		{SettleWindow: 2 * time.Second},
	} {
		if _, err := readOnlyToolsInventoryAfter(t.Context(), nil, t.TempDir(), time.Second, required); !errors.Is(err, errInventoryShape) {
			t.Fatal("invalid observation prerequisites reached dispatch")
		}
	}
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
		t.Run(test.name, func(t *testing.T) { observePinnedToolsInventory(t, executable, test.tools, test.listed, "") })
	}
}

func TestKiroPinnedRelayInventory(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for one owned ACP/relay setup and tools query; no prompt or tool call")
	}
	observePinnedToolsInventory(t, executable, nil, nil, buildRelayObserver(t))
}

func TestKiroPinnedRelayGroupJoin(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_KIRO_BINARY for an owned relay group-join experiment and tools query; no model prompt")
	}
	joined := buildNamedRelayObserver(t, "joining-relay")
	t.Cleanup(func() {
		outcome := relayJoinOutcome(joined)
		t.Logf("experimental_relay_group_join=%s", outcome)
		if !t.Failed() && outcome != "changed" {
			t.Error("the installed relay did not move into the ACP parent's group")
		}
	})
	observePinnedToolsInventory(t, executable, nil, nil, joined)
}

func buildRelayObserver(t *testing.T) string {
	return buildNamedRelayObserver(t, "observed-relay")
}

func buildNamedRelayObserver(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot identify owned build directory")
	}
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"}
	for _, key := range []string{"GOMODCACHE", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	for _, target := range []struct{ name, source string }{{"owned-relay", "../../cmd/dax-kiro-proxy"}, {name, "./testdata/relayobserver"}} {
		_, err = runner.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: env,
			Args: []string{"build", "-o", filepath.Join(root, target.name), target.source}})
		if err != nil {
			t.Fatal("cannot build the owned effect-free relay observation")
		}
	}
	return filepath.Join(root, name)
}

func observePinnedToolsInventory(t *testing.T, executable string, declaredTools, listedTools []string, relayExecutable string) {
	t.Helper()
	observePinnedScopedInventory(t, executable, declaredTools, listedTools, relayExecutable, nil)
}

func observePinnedScopedInventory(t *testing.T, executable string, declaredTools, listedTools []string, relayExecutable string, scope *mcpScopeProbe) {
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
	name := "dax-readonly-inventory"
	required := inventoryPrerequisite{}
	var broker *relay.Broker
	var socket *relay.Socket
	if relayExecutable == "" {
		agent, err := json.Marshal(map[string]any{"name": name, "description": "Independent read-only protocol observation", "tools": declaredTools, "allowedTools": []string{}, "mcpServers": map[string]any{}, "resources": []any{}, "hooks": map[string]any{}, "includeMcpJson": false})
		if err != nil || os.WriteFile(filepath.Join(cwd, ".kiro", "agents", name+".json"), agent, 0600) != nil {
			t.Fatal("cannot write independent inventory agent")
		}
	} else {
		// A fresh synthetic name prevents this observation from passing with an earlier tool cache.
		tool, err := json.Marshal(map[string]any{"name": "Inventory" + rand.Text(), "description": "Independent no-effect inventory tool", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}})
		if err != nil {
			t.Fatal("cannot encode independent tool declaration")
		}
		registry, err := toolregistry.Build(ctx, []json.RawMessage{tool}, nil, syntaxFixtureValidator{})
		if err != nil {
			t.Fatal("cannot build independent inventory registry")
		}
		broker, err = relay.NewBroker(registry, relay.Limits{ToolTimeout: 5 * time.Second})
		if err != nil {
			t.Fatal("cannot create effect-free inventory relay")
		}
		defer broker.Close()
		socket, err = relay.Listen(broker, relay.SocketConfig{BaseDirectory: root})
		if err != nil {
			t.Fatal("cannot create private inventory control socket")
		}
		defer socket.Close()
		candidate, err := launcher.WriteCandidateAgent(launcher.AgentConfig{Directory: cwd, Registry: registry, RelayExecutable: relayExecutable, RelayConfig: socket.ConfigPath()})
		if err != nil || candidate.ExecutionVerified {
			t.Fatal("cannot create unverified relay-only candidate")
		}
		name = candidate.Name
		required = inventoryPrerequisite{WaitForMCP: true, Alias: registry.Tools()[0].Alias}
		declaredTools = []string{"@dax_session/" + required.Alias}
		if scope != nil {
			defer scope.close(t)
			declaredTools = scope.prepare(t, ctx, root, configuration, cwd, candidate.Path, &required)
		}
	}
	if os.WriteFile(filepath.Join(configuration, "settings", "cli.json"), []byte(`{"chat.disableInheritingDefaultResources":true}`), 0600) != nil {
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
	if socket != nil && socket.BindProcess(client.PID()) != nil {
		t.Fatal("cannot bind the owned inventory ACP group")
	}
	if scope != nil {
		scope.bind(t, client.PID())
	}
	report, callErr := readOnlyToolsInventoryAfter(ctx, client, cwd, 3*time.Second, required)
	closeErr := client.Close()
	groupGone := errors.Is(syscall.Kill(-client.PID(), 0), syscall.ESRCH)
	runner.Close()
	if scope != nil {
		scope.check(t, client.PID(), report)
	}
	if broker != nil {
		peer, verified := socket.PeerPID()
		if !verified {
			t.Error("relay did not establish authenticated kernel-verified group membership")
		}
		if !verified {
			peer = 0
		}
		checkRelayCleanupWithAttachment(t, relayExecutable, client.PID(), peer)
		stats := broker.Stats()
		broker.Close()
		if socket.Close() != nil {
			t.Error("registered relay cleanup failed")
		}
		_, configErr := os.Lstat(socket.ConfigPath())
		t.Logf("relay_pending=%d, relay_alias_matched=%v, relay_alias_form=%s, relay_config_removed=%v, mcp_param_kinds=%v, mcp_declared_name_matches=%d", stats.Pending, report.AliasMatched, report.AliasNameForm, errors.Is(configErr, os.ErrNotExist), report.MCPParamKinds, report.MCPDeclaredNameMatches)
		if stats.Pending != 0 || stats.Queued != 0 || stats.Sealed != 0 || !errors.Is(configErr, os.ErrNotExist) {
			t.Error("relay observation retained tool work or private artifacts")
		}
	}
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
	expectedTools := len(declaredTools)
	if scope != nil {
		expectedTools = scope.toolCount(t, report)
	}
	if report.DataKinds["tools"] != "array" || report.DataSizes["tools"] != expectedTools {
		t.Fatal("declared and listed tool counts differ")
	}
	if required.WaitForMCP && (!report.AliasMatched || report.NotificationKinds["_kiro.dev/mcp/server_initialized"] == 0 || report.MCPDeclaredNameMatches == 0 || len(report.ListedNativeNames) != 0) {
		t.Fatal("the sole relay alias and MCP initialization were not both observed")
	}
	for _, name := range listedTools {
		if !report.ListedNativeNames[name] {
			t.Fatal("declared native tool is not the listed native tool")
		}
	}
}
