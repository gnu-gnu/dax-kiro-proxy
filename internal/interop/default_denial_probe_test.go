package interop_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

func fullDenialRequest(id, reason string, failed bool) *anthropic.Request {
	r := denialRequestFixture(id, reason, failed)
	r.Tools = append(r.Tools,
		json.RawMessage(`{"name":"Write","input_schema":{"type":"object"}}`),
		json.RawMessage(`{"name":"Bash","input_schema":{"type":"object"}}`))
	r.Extra = map[string]json.RawMessage{"thinking": json.RawMessage(`{"type":"adaptive"}`), "context_management": json.RawMessage(`{}`)}
	return r
}

func TestDefaultDenialGuardKeepsOperationAndRequestBounds(t *testing.T) {
	for _, valid := range []bool{false, true} {
		b, f := newDenialBackendFixture()
		b.fullClient = true
		b.effect = &toolEffectExpectation{Tool: "Read", Input: json.RawMessage(`{"file_path":"/independent/fixture"}`), IsError: true, RequiredText: clientDenialReason}
		turn, err := b.Start(t.Context(), fullDenialRequest("", "", false))
		if err != nil {
			t.Fatal("default declarations blocked the owned initial request", err)
		}
		if _, err := turn.Next(t.Context()); err != nil {
			t.Fatal("owned Read did not pass the guard", err)
		}
		_, err = b.Start(t.Context(), fullDenialRequest("fixture-use", clientDenialReason, valid))
		if valid && (err != nil || f.starts != 2) || !valid && (!errors.Is(err, inference.ErrRequest) || f.starts != 1) {
			t.Fatal("broader declarations changed the exact refusal requirement")
		}
		if _, err := b.Start(t.Context(), fullDenialRequest("", "", false)); !errors.Is(err, inference.ErrRequest) {
			t.Fatal("broader declarations admitted a third request")
		}
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) {
			r.Messages[0].Content = append(r.Messages[0].Content, anthropic.Block{Type: "text", Text: "An additional question"})
		},
		func(r *anthropic.Request) {
			r.Messages[0].Content = append(r.Messages[0].Content, r.Messages[0].Content[0])
		},
	} {
		b, f := newDenialBackendFixture()
		b.fullClient = true
		turn, err := b.Start(t.Context(), fullDenialRequest("", "", false))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Next(t.Context()); err != nil {
			t.Fatal(err)
		}
		r := fullDenialRequest("fixture-use", clientDenialReason, true)
		mutate(r)
		if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 1 {
			t.Fatal("full-client result admitted extra content or a second logical question")
		}
	}
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) { r.Tools = nil },
		func(r *anthropic.Request) { r.Tools = r.Tools[1:] },
		func(r *anthropic.Request) { r.Tools = append(r.Tools, r.Tools[0]) },
		func(r *anthropic.Request) { r.Tools[1] = json.RawMessage(`{"name":"Write","name":"Bash"}`) },
		func(r *anthropic.Request) {
			for len(r.Tools) < 129 {
				r.Tools = append(r.Tools, r.Tools[0])
			}
		},
	} {
		b, f := newDenialBackendFixture()
		b.fullClient = true
		r := fullDenialRequest("", "", false)
		mutate(r)
		if _, err := b.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) || f.starts != 0 {
			t.Fatal("invalid default declaration envelope reached the driver")
		}
	}
	for _, use := range []anthropic.ToolUse{
		{ID: "owned", Name: "Write", Input: json.RawMessage(`{"file_path":"/independent/fixture"}`)},
		{ID: "owned", Name: "Read", Input: json.RawMessage(`{"file_path":"/independent/fixture","offset":1}`)},
		{ID: "owned", Name: "Read", Input: json.RawMessage(`{"file_path":"/unowned/file"}`)},
	} {
		b, f := newDenialBackendFixture()
		b.fullClient = true
		b.effect = &toolEffectExpectation{Tool: "Read", Input: json.RawMessage(`{"file_path":"/independent/fixture"}`), IsError: true, RequiredText: clientDenialReason}
		f.first[0].Tools = []anthropic.ToolUse{use}
		turn, err := b.Start(t.Context(), fullDenialRequest("", "", false))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Next(t.Context()); !errors.Is(err, inference.ErrRequest) || f.cancelled.Load() != 1 {
			t.Fatal("an unplanned tool or argument reached the client")
		}
	}
}

func TestClaudeDefaultDenialWithPreparedACP(t *testing.T) {
	client := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if client == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for the default-client fake-ACP rehearsal; no model credits")
	}
	runClientToolProbe(t, client, "", "default-read-denial")
}

func TestKiroLiveDefaultClientDenialRecreation(t *testing.T) {
	if os.Getenv("DAX_INTEROP_KIRO_CREDIT_OPT_IN") != "1" {
		t.Skip("live default-client test requires explicit credit opt-in")
	}
	client, kiro := os.Getenv("DAX_INTEROP_CLAUDE_BINARY"), os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if client == "" || kiro == "" {
		t.Fatal("both pinned executables are required")
	}
	runClientToolProbe(t, client, kiro, "default-read-denial")
}
