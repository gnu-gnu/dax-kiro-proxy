package interop_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

// prepareLiveSkillGateway is prepareSkillGateway with the actual restricted Kiro process behind
// the real gateway, so plugin skill content and plugin hooks are observed around real model turns.
// It admits at most expectedStarts backend turns and joins the owned process and relay cleanup.
func prepareLiveSkillGateway(t *testing.T, ctx context.Context, root string, tokens gateway.Tokens, validator *schemacheck.Pool, expectedStarts int32) (*defaultClientBackend, http.Handler, string, func()) {
	t.Helper()
	runner, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.Close)
	backendDir := filepath.Join(root, "backend")
	if os.Mkdir(backendDir, 0700) != nil {
		t.Fatal("cannot create owned live skill backend")
	}
	models, execution := prepareLiveKiroProbe(t, ctx, runner, os.Getenv("DAX_INTEROP_KIRO_BINARY"), root, backendDir, filepath.Join(root, "preflight-kiro"))
	process := execution.Process
	process.Limits.RequestTimeout = 45 * time.Second
	pool, err := acppool.New(acppool.Config{Process: process, MaxProcesses: 1, SessionsPerProcess: 1, MaxIdle: 1, SetupTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal("cannot create owned live skill ACP pool")
	}
	relay := buildRelayObserver(t)
	var prepared, cleaned atomic.Int32
	config := session.Config{Process: process, Pool: pool, InitialModel: "auto", Validator: validator, RelayExecutable: relay, SetupTimeout: 20 * time.Second, TurnTimeout: 45 * time.Second, MaxRecreations: 1}
	config.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		prepared.Add(1)
		owned, err := execution.Prepare(ctx, input)
		if owned.Cleanup != nil {
			cleanup := owned.Cleanup
			owned.Cleanup = func() error { err := cleanup(); cleaned.Add(1); return err }
		}
		return owned, err
	}
	driver, err := session.New(config)
	if err != nil {
		pool.Close()
		t.Fatal("cannot create owned live skill session")
	}
	model, err := models.ClientID(models.Current())
	if err != nil {
		t.Fatal("live catalog has no default alias")
	}
	backend := &defaultClientBackend{Driver: driver, catalog: models, limit: expectedStarts}
	handler, err := gateway.New(gateway.Config{Tokens: tokens, Backend: backend, TurnTimeout: 45 * time.Second, FirstEventTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	finish := func() {
		closeErr, poolErr := driver.Close(), pool.Close()
		records, err := relayProcessRecords(relay)
		relaysGone := err == nil
		for _, record := range records {
			if !errors.Is(syscall.Kill(record.pid, 0), syscall.ESRCH) {
				relaysGone = false
				_ = syscall.Kill(record.pid, syscall.SIGKILL)
			}
		}
		t.Logf("live_skill_gateway_requests=%d, failed=%v, prepared=%d, cleaned=%d, relay_records=%d, relays_gone=%v, pool_empty=%v, close_ok=%v", backend.starts.Load(), backend.failed.Load(), prepared.Load(), cleaned.Load(), len(records), relaysGone, pool.Stats().Processes == 0, closeErr == nil && poolErr == nil)
		if closeErr != nil || poolErr != nil || pool.Stats().Processes != 0 || prepared.Load() != cleaned.Load() || !relaysGone {
			t.Error("live skill gateway cleanup was not established")
		}
	}
	return backend, handler, model, finish
}
