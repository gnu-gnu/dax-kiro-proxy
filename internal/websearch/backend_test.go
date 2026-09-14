package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

type mainFixture struct{ starts, closed atomic.Int32 }

func (m *mainFixture) Start(context.Context, *anthropic.Request) (inference.Turn, error) {
	m.starts.Add(1)
	return nil, inference.ErrBusy
}
func (m *mainFixture) Models(context.Context) ([]inference.Model, error) {
	return []inference.Model{{ID: "fixture"}}, nil
}
func (m *mainFixture) Close() error { m.closed.Add(1); return nil }

type searchFixture struct {
	closed  atomic.Int32
	hold    bool
	cleanup error
}

func (s *searchFixture) Model() string { return "fixture" }
func (s *searchFixture) Run(ctx context.Context, emit func(inference.Event) bool) error {
	if s.hold {
		<-ctx.Done()
		return ctx.Err()
	}
	if !emit(inference.Event{Kind: inference.Search, Searches: []anthropic.SearchExchange{{ID: "srvtoolu_independent", Query: "synthetic", Results: []anthropic.SearchResult{}}}}) {
		return ctx.Err()
	}
	if !emit(inference.Event{Kind: inference.End, StopReason: "end_turn"}) {
		return ctx.Err()
	}
	return nil
}
func (s *searchFixture) Close() error { s.closed.Add(1); return s.cleanup }
func searchRequest(t *testing.T) *anthropic.Request {
	t.Helper()
	r, err := anthropic.DecodeRequest([]byte(`{"model":"fixture","max_tokens":32,"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}],"messages":[{"role":"user","content":"synthetic search"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Identity.Session = "same-main-conversation"
	return r
}
func TestSearchBackendSeparatesNestedOwnership(t *testing.T) {
	main := new(mainFixture)
	var peers []*searchFixture
	b, err := New(main, func(context.Context, *anthropic.Request, anthropic.SearchSpec) (Session, error) {
		p := new(searchFixture)
		peers = append(peers, p)
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for range 2 {
		turn, err := b.Start(t.Context(), searchRequest(t))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			e, err := turn.Next(t.Context())
			if err != nil || i == 0 && e.Kind != inference.Search || i == 1 && e.Kind != inference.End {
				t.Fatal("search exchange missing")
			}
		}
		turn.Finish()
		turn.Cancel()
	}
	if main.starts.Load() != 0 || len(peers) != 2 || peers[0] == peers[1] || peers[0].closed.Load() != 1 || peers[1].closed.Load() != 1 {
		t.Fatal("nested search borrowed or retained main owner")
	}
	ordinary := searchRequest(t)
	ordinary.Tools = []json.RawMessage{json.RawMessage(`{"name":"WebSearch","input_schema":{"type":"object"}}`)}
	if _, err := b.Start(t.Context(), ordinary); !errors.Is(err, inference.ErrBusy) || main.starts.Load() != 1 {
		t.Fatal("ordinary client WebSearch must retain client authority")
	}
}
func TestSearchBackendCapacityWaitAndRepeatedCancel(t *testing.T) {
	var peers []*searchFixture
	b, _ := New(new(mainFixture), func(context.Context, *anthropic.Request, anthropic.SearchSpec) (Session, error) {
		p := &searchFixture{hold: true}
		peers = append(peers, p)
		return p, nil
	})
	defer b.Close()
	first, _ := b.Start(t.Context(), searchRequest(t))
	second, _ := b.Start(t.Context(), searchRequest(t))
	if _, err := b.Start(t.Context(), searchRequest(t)); !errors.Is(err, acp.ErrOverloaded) {
		t.Fatal("unbounded search processes")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, err := first.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("short wait consumed turn")
	}
	first.Cancel()
	first.Cancel()
	second.Cancel()
	if peers[0].closed.Load() != 1 || peers[1].closed.Load() != 1 {
		t.Fatal("cancel did not join each process once")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Start(t.Context(), searchRequest(t)); err == nil {
		t.Fatal("search admitted after close")
	}
}
func TestSearchBackendCloseCancelsPreparationAndRetainsCleanupError(t *testing.T) {
	entered := make(chan struct{})
	returned := make(chan error, 1)
	b, _ := New(new(mainFixture), func(ctx context.Context, _ *anthropic.Request, _ anthropic.SearchSpec) (Session, error) {
		close(entered)
		<-ctx.Done()
		return nil, acp.ErrCleanup
	})
	go func() { _, err := b.Start(context.Background(), searchRequest(t)); returned <- err }()
	<-entered
	if !errors.Is(b.Close(), acp.ErrCleanup) {
		t.Fatal("preparation cleanup failure lost")
	}
	if err := <-returned; err == nil {
		t.Fatal("canceled preparation succeeded")
	}
	if !errors.Is(b.Close(), acp.ErrCleanup) {
		t.Fatal("repeated close lost cleanup failure")
	}
}
func TestSearchBackendRejectsUnsupportedBeforeOpening(t *testing.T) {
	opened := false
	b, _ := New(new(mainFixture), func(context.Context, *anthropic.Request, anthropic.SearchSpec) (Session, error) {
		opened = true
		return nil, io.EOF
	})
	defer b.Close()
	r := searchRequest(t)
	r.Tools = []json.RawMessage{json.RawMessage(`{"type":"web_search_20250305","name":"web_search","blocked_domains":["example.org"]}`)}
	if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || opened {
		t.Fatal("unsupported restriction reached a process")
	}
}

func TestSearchBackendRejectsContentAndControlsBeforeAllocation(t *testing.T) {
	for _, kind := range []string{"media", "disabled-tools", "large", "control"} {
		opened := false
		b, _ := New(new(mainFixture), func(context.Context, *anthropic.Request, anthropic.SearchSpec) (Session, error) {
			opened = true
			return nil, io.EOF
		})
		r := searchRequest(t)
		switch kind {
		case "media":
			r.Messages[0].Content = []anthropic.Block{{Type: "image", Raw: json.RawMessage(`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aQ=="}}`)}}
		case "disabled-tools":
			r.Extra["tool_choice"] = json.RawMessage(`{"type":"none"}`)
		case "large":
			r.Messages[0].Content[0].Text = strings.Repeat("x", 512<<10)
		case "control":
			r.Extra["temperature"] = json.RawMessage(`0.5`)
		}
		if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || opened {
			t.Fatalf("invalid search allocated=%s", kind)
		}
		if b.Close() != nil {
			t.Fatal("cleanup")
		}
	}
}
