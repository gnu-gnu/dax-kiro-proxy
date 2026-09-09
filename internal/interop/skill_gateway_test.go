package interop_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

func TestClaudePluginSkillThroughGatewayAndACP(t *testing.T) {
	observePluginAssetMode(t, false, pluginGatewaySkill)
}

func prepareSkillGateway(t *testing.T, root, relayExecutable string, tokens gateway.Tokens, validator *schemacheck.Pool) (*defaultClientBackend, http.Handler, string, func()) {
	t.Helper()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	fake := buildDenialACPFixture(t, t.Context(), runner, root)
	backendDir, ledger := filepath.Join(root, "backend"), filepath.Join(root, "skill-processes")
	if os.Mkdir(backendDir, 0700) != nil {
		t.Fatal("cannot create owned skill backend")
	}
	driver, err := session.New(session.Config{Process: acp.Config{Executable: fake, Args: []string{"chat-tools-plugin-skill", ledger}, Directory: backendDir, Environment: []string{"HOME=" + filepath.Join(root, "home"), "PATH=/usr/bin:/bin"}, ClientInfo: acp.Info{Name: "independent-skill-client", Version: "1"}}, Validator: validator, RelayExecutable: relayExecutable, SetupTimeout: 10 * time.Second, TurnTimeout: 15 * time.Second, MaxRecreations: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Close() })
	models, err := catalog.New([]catalog.Backend{{ID: "fixture-backend", Name: "Independent skill backend"}}, "fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	model, err := models.ClientID("fixture-backend")
	if err != nil {
		t.Fatal(err)
	}
	backend := &defaultClientBackend{Driver: driver, catalog: models, limit: 2}
	handler, err := gateway.New(gateway.Config{Tokens: tokens, Backend: backend, TurnTimeout: 15 * time.Second, FirstEventTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	finish := func() {
		if driver.Close() != nil {
			t.Error("skill driver cleanup failed")
		}
		pids := strings.Fields(string(boundedAssetFile(t, ledger)))
		gone := len(pids) == 2
		for _, value := range pids {
			pid, err := strconv.Atoi(value)
			gone = gone && err == nil && pid > 1 && errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
		}
		t.Logf("skill_gateway_requests=%d, failed=%v, backend_processes=%d, groups_gone=%v", backend.starts.Load(), backend.failed.Load(), len(pids), gone)
		if backend.starts.Load() != 2 || backend.failed.Load() || !gone {
			t.Error("skill gateway sequence or cleanup incomplete")
		}
	}
	return backend, handler, model, finish
}
