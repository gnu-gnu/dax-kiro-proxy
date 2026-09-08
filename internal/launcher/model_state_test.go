package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/inference"
)

func modelStateFixture(t *testing.T) (ModelConfig, *catalog.Catalog) {
	t.Helper()
	data, err := catalog.New([]catalog.Backend{{ID: "fixture.one"}, {ID: "fixture.two"}}, "fixture.one")
	if err != nil {
		t.Fatal(err)
	}
	cfg := ModelConfig{Cache: catalog.CacheConfig{Directory: filepath.Join(t.TempDir(), "models"), Identity: catalog.Identity{Executable: "/independent/kiro-cli", Version: "fixture-1", ProfileDigest: strings.Repeat("1", 64), AgentDigest: strings.Repeat("2", 64), CapabilitiesDigest: strings.Repeat("3", 64)}}, Interactive: true}
	cfg.Discover = func(context.Context) (*catalog.Catalog, error) { return data, nil }
	return cfg, data
}

func TestPreparedModelSelectionPrecedenceAndExactAliases(t *testing.T) {
	for _, tc := range []struct {
		name, initial, saved, want string
		interactive                bool
		source                     ModelSource
	}{
		{"explicit", "fixture.one", "fixture.two", "fixture.one", true, ModelConfigured},
		{"restored", "", "fixture.two", "fixture.two", true, ModelLastUsed},
		{"default", "", "", "fixture.one", true, ModelDefault},
		{"noninteractive", "", "fixture.two", "fixture.one", false, ModelDefault},
		{"removed-saved-model", "", "fixture.removed", "fixture.one", true, ModelDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, data := modelStateFixture(t)
			cfg.Interactive, cfg.Cache.Identity.InitialModel = tc.interactive, tc.initial
			if tc.saved != "" {
				if err := catalog.SaveLastModel(cfg.Cache.Directory, cfg.Cache.Identity, tc.saved, true); err != nil {
					t.Fatal(err)
				}
			}
			models, err := PrepareModels(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer models.Close()
			selection := models.Selection()
			alias, _ := data.ClientID(tc.want)
			if selection.Backend != tc.want || selection.Client != alias || selection.Source != tc.source || selection.Stale || selection.LoadTime <= 0 {
				t.Fatal("startup model precedence or exact alias lost")
			}
		})
	}
	cfg, _ := modelStateFixture(t)
	cfg.Cache.Identity.InitialModel = "fixture.unknown"
	if models, err := PrepareModels(t.Context(), cfg); !errors.Is(err, catalog.ErrModel) || models != nil {
		t.Fatal("unknown configured model silently fell back")
	}
}

type modelFixtureBackend struct {
	turn          *modelFixtureTurn
	lists, starts atomic.Int32
}

func (b *modelFixtureBackend) Models(context.Context) ([]inference.Model, error) {
	b.lists.Add(1)
	return nil, errors.New("unexpected inference discovery")
}
func (b *modelFixtureBackend) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	b.starts.Add(1)
	return b.turn, nil
}

type modelFixtureTurn struct {
	model, stop              string
	failure                  error
	reads, finishes, cancels atomic.Int32
}

func (t *modelFixtureTurn) Model() string { return t.model }
func (t *modelFixtureTurn) Next(context.Context) (inference.Event, error) {
	if t.reads.Add(1) > 1 {
		return inference.Event{}, io.EOF
	}
	return inference.Event{Kind: inference.End, StopReason: t.stop}, t.failure
}
func (t *modelFixtureTurn) Finish() { t.finishes.Add(1) }
func (t *modelFixtureTurn) Cancel() { t.cancels.Add(1) }

func TestModelPreferenceRequiresDeliveredForegroundCompletion(t *testing.T) {
	for _, kind := range []string{"main", "followup", "title", "agent", "parent-agent", "handoff", "cancel", "early-finish", "auth", "noninteractive", "unadvertised"} {
		t.Run(kind, func(t *testing.T) {
			cfg, data := modelStateFixture(t)
			cfg.Interactive = kind != "noninteractive"
			models, err := PrepareModels(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer models.Close()
			actual, _ := data.ClientID("fixture.two")
			original := &modelFixtureTurn{model: actual, stop: "end_turn"}
			backend := &modelFixtureBackend{turn: original}
			wrapped := &catalogBackend{inner: backend, models: models}
			list, err := wrapped.Models(t.Context())
			if err != nil || len(list) != 2 || backend.lists.Load() != 0 {
				t.Fatal("model listing used inference backend instead of prepared catalog")
			}
			r := &anthropic.Request{Model: models.Selection().Client, Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "synthetic foreground input"}}}}, Extra: map[string]json.RawMessage{}}
			switch kind {
			case "title":
				r.System = []anthropic.Block{{Type: "text", Text: "Create a title for this conversation."}}
				r.Extra["thinking"] = json.RawMessage(`{"type":"disabled"}`)
				r.Extra["output_config"] = json.RawMessage(`{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}}`)
			case "followup":
				r.Messages[0].Content = []anthropic.Block{{Type: "tool_result", Raw: json.RawMessage(`{"type":"tool_result","tool_use_id":"independent-call","content":"synthetic result"}`)}}
			case "agent":
				r.Identity.Agent = "independent-agent"
			case "parent-agent":
				r.Identity.ParentAgent = "independent-parent"
			case "handoff":
				original.stop = "tool_use"
			case "auth":
				original.failure = acp.ErrAuthentication
			case "unadvertised":
				original.model = "claude-dax-unknown-fixture"
			}
			turn, err := wrapped.Start(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if kind != "early-finish" {
				_, _ = turn.Next(t.Context())
			}
			if kind == "cancel" {
				turn.Cancel()
			} else {
				turn.Finish()
			}
			turn.Finish()
			turn.Cancel()
			got, ok, err := catalog.LoadLastModel(cfg.Cache.Directory, cfg.Cache.Identity, data)
			want := kind == "main" || kind == "followup"
			if err != nil || ok != want || want && got != "fixture.two" {
				t.Fatal("last model did not match actual delivered foreground use")
			}
			if original.finishes.Load()+original.cancels.Load() != 1 {
				t.Fatal("wrapped response finalized twice")
			}
			if models.SaveFailed() != (kind == "unadvertised") {
				t.Fatal("actual model outside the catalog did not retain a safe save failure")
			}
		})
	}
}

func TestPreparedCatalogStaleRefreshAndCloseOwnership(t *testing.T) {
	cfg, data := modelStateFixture(t)
	var clock atomic.Int64
	clock.Store(1_780_000_000)
	cfg.Cache.Now = func() time.Time { return time.Unix(clock.Load(), 0) }
	cfg.Cache.TTL = time.Minute
	var calls atomic.Int32
	started, stopped := make(chan struct{}), make(chan struct{})
	cfg.Discover = func(ctx context.Context) (*catalog.Catalog, error) {
		if calls.Add(1) == 1 {
			return data, nil
		}
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}
	models, err := PrepareModels(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer models.Close()
	clock.Add(61)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if list, err := models.Models(ctx); err != nil || len(list) != 2 {
		t.Fatal("stale catalog blocked on refresh")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stale refresh did not start")
	}
	for range 5 {
		if _, err := models.Models(ctx); err != nil {
			t.Fatal(err)
		}
	}
	models.Close()
	select {
	case <-stopped:
	default:
		t.Fatal("model owner did not join catalog refresh")
	}
	if calls.Load() != 2 {
		t.Fatal("stale catalog spawned duplicate discovery")
	}
	if _, err := models.Models(t.Context()); err == nil {
		t.Fatal("closed catalog admitted work")
	}
}

func TestModelPreferenceSaveFailureDoesNotFailDeliveredTurn(t *testing.T) {
	cfg, data := modelStateFixture(t)
	models, err := PrepareModels(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer models.Close()
	if os.Mkdir(filepath.Join(cfg.Cache.Directory, "last-model.json"), 0700) != nil {
		t.Fatal("cannot create owned write failure fixture")
	}
	alias, _ := data.ClientID("fixture.two")
	original := &modelFixtureTurn{model: alias, stop: "end_turn"}
	wrapped := &catalogBackend{inner: &modelFixtureBackend{turn: original}, models: models}
	r := &anthropic.Request{Messages: []anthropic.Message{{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "owned input"}}}}}
	turn, err := wrapped.Start(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Next(t.Context()); err != nil {
		t.Fatal(err)
	}
	turn.Finish()
	if original.finishes.Load() != 1 || !models.SaveFailed() {
		t.Fatal("preference failure lost delivered response or safe diagnostic")
	}
}
