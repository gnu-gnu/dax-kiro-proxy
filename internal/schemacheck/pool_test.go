package schemacheck_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/schemacheck"
)

var workerBinary, fakeBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dax-schema-fixture-")
	if err != nil {
		panic(err)
	}
	workerBinary = filepath.Join(dir, "dax-kiro-proxy")
	fakeBinary = filepath.Join(dir, "fake-acp")
	for _, build := range []struct{ output, source string }{{workerBinary, "../../cmd/dax-kiro-proxy"}, {fakeBinary, "../acp/testdata/fake"}} {
		cmd := exec.Command("go", "build", "-o", build.output, build.source)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
func pool(t *testing.T, change func(*schemacheck.Config)) *schemacheck.Pool {
	t.Helper()
	cfg := schemacheck.Config{Executable: workerBinary, Directory: t.TempDir(), Timeout: 500 * time.Millisecond, StartupTimeout: 3 * time.Second}
	if change != nil {
		change(&cfg)
	}
	p, err := schemacheck.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}
func TestDraft2020ArgumentSemanticsInWorker(t *testing.T) {
	p := pool(t, nil)
	for _, tc := range []struct{ name, schema, valid, invalid string }{
		{"types", `{"type":"object","properties":{"n":{"type":"integer","minimum":0}},"required":["n"],"additionalProperties":false}`, `{"n":3}`, `{"n":-1}`},
		{"local-ref", `{"type":"object","$defs":{"label":{"type":"string","minLength":1}},"properties":{"label":{"$ref":"#/$defs/label"}},"required":["label"]}`, `{"label":"synthetic"}`, `{"label":""}`},
		{"dynamic-ref", `{"type":"object","$defs":{"value":{"$dynamicAnchor":"value","type":"integer"}},"properties":{"n":{"$dynamicRef":"#value"}}}`, `{"n":3}`, `{"n":"3"}`},
		{"unevaluated", `{"type":"object","allOf":[{"properties":{"x":{"type":"string"}}}],"unevaluatedProperties":false}`, `{"x":"synthetic"}`, `{"x":"synthetic","y":1}`},
		{"exact-integer", `{"type":"object","properties":{"n":{"const":9007199254740993}},"required":["n"]}`, `{"n":9007199254740993}`, `{"n":9007199254740992}`},
		{"decimal", `{"type":"object","properties":{"n":{"multipleOf":0.01}}}`, `{"n":0.03}`, `{"n":0.031}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.Check(context.Background(), []byte(tc.schema)); err != nil {
				t.Fatal(err)
			}
			if err := p.Validate(context.Background(), []byte(tc.schema), []byte(tc.valid)); err != nil {
				t.Fatal(err)
			}
			if err := p.Validate(context.Background(), []byte(tc.schema), []byte(tc.invalid)); !errors.Is(err, schemacheck.ErrArguments) {
				t.Fatalf("invalid instance accepted: %v", err)
			}
		})
	}
	if p.Stats().Started != 1 {
		t.Fatal("healthy worker was not reused")
	}
}

func TestSchemaRetrievalRegexAndInputLimits(t *testing.T) {
	p := pool(t, nil)
	for _, schema := range []string{`true`, `{"type":"string"}`, `{"type":"object","$ref":"https://example.invalid/private-sentinel"}`, `{"type":"object","$ref":"file:///private/tmp/private-sentinel"}`, `{"type":"object","properties":{"x":{"pattern":"(?=x)x"}}}`, `{"type":"object","$schema":"https://json-schema.org/draft-07/schema"}`, `{"type":"object","minimum":1e100000}`, strings.Repeat("x", (64<<10)+1)} {
		err := p.Check(context.Background(), []byte(schema))
		if err == nil {
			t.Fatal("unsupported or excessive schema accepted")
		}
		if strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("schema diagnostic leaked untrusted content")
		}
	}
	for _, args := range []string{`[]`, `null`, `{"x":1,"x":2}`, `{"x":1e100000}`, strings.Repeat("x", (1<<20)+1)} {
		if err := p.Validate(context.Background(), []byte(`{"type":"object"}`), []byte(args)); err == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
}

func TestWorkerDeadlineAdmissionAndRepeatedClose(t *testing.T) {
	p := pool(t, func(c *schemacheck.Config) {
		c.Executable = fakeBinary
		c.Args = []string{"schema-hang"}
		c.MaxWorkers = 1
		c.Timeout = 80 * time.Millisecond
		c.QueueTimeout = 10 * time.Millisecond
	})
	done := make(chan error, 1)
	go func() { done <- p.Check(context.Background(), []byte(`{"type":"object"}`)) }()
	deadline := time.Now().Add(time.Second)
	for p.Stats().Active == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := p.Check(context.Background(), []byte(`{"type":"object"}`)); !errors.Is(err, schemacheck.ErrOverloaded) {
		t.Fatalf("unbounded worker admission: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, schemacheck.ErrBudget) {
			t.Fatalf("worker deadline not enforced: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker process was not retired")
	}
	var closes sync.WaitGroup
	for range 8 {
		closes.Go(p.Close)
	}
	closes.Wait()
	if p.Stats().Active != 0 || p.Stats().Idle != 0 {
		t.Fatal("schema worker retained after cleanup")
	}
}

func TestQueuedAdmissionServesConcurrentChecks(t *testing.T) {
	p := pool(t, func(c *schemacheck.Config) { c.MaxWorkers = 1 })
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- p.Check(context.Background(), []byte(`{"type":"object","properties":{"n":{"type":"integer"}}}`))
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("queued check was refused instead of waiting for the worker", err)
		}
	}
	if p.Stats().Active != 0 {
		t.Fatal("worker slot not released after queued checks")
	}
	if _, err := schemacheck.New(schemacheck.Config{Executable: workerBinary, Directory: t.TempDir(), QueueTimeout: time.Minute}); !errors.Is(err, schemacheck.ErrWorker) {
		t.Fatal("unbounded queue wait accepted")
	}
}
