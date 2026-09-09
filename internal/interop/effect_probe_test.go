package interop_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/inference"
)

func TestOnePromptEffectGuardRequiresExactCallAndResult(t *testing.T) {
	for _, c := range []struct {
		name, result   string
		isError, valid bool
	}{
		{"read-success", "synthetic-canary-value", false, true},
		{"read-error", "synthetic-canary-value", true, false},
		{"read-without-content", "unrelated", false, false},
		{"write-success", "created", false, true},
		{"write-error", "not created", true, false},
		{"denied-write", "permission denied", true, true},
		{"unexpected-write-success", "created", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, f := newDenialBackendFixture()
			effect := &toolEffectExpectation{Tool: "Write", Input: json.RawMessage(`{"file_path":"/independent/fixture","content":"probe"}`)}
			if c.name == "read-success" || c.name == "read-error" || c.name == "read-without-content" {
				effect.Tool, effect.Input, effect.RequiredText = "Read", json.RawMessage(`{"file_path":"/independent/fixture"}`), b.canary
				effect.ReadCanary = true
			}
			if c.name == "denied-write" || c.name == "unexpected-write-success" {
				effect.IsError = true
			}
			b.effect = effect
			f.first[0].Tools[0].Name, f.first[0].Tools[0].Input = effect.Tool, effect.Input
			request := denialRequestFixture("", "", false)
			request.Tools[0] = json.RawMessage(`{"name":"` + effect.Tool + `","input_schema":{"type":"object"}}`)
			turn, err := b.Start(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.Next(t.Context()); err != nil {
				t.Fatal(err)
			}
			result := denialRequestFixture("fixture-use", c.result, c.isError)
			result.Tools = request.Tools
			_, err = b.Start(t.Context(), result)
			if (err == nil) != c.valid || f.starts != 1+btoi(c.valid) {
				t.Fatal("effect result admitted with the wrong status or content")
			}
			if c.valid && b.snapshot().Results != 1 {
				t.Fatal("matching result was not counted")
			}
		})
	}
	for _, input := range []string{
		`{"file_path":"/elsewhere","content":"probe"}`,
		`{"file_path":"/independent/fixture","content":"changed"}`,
		`{"file_path":"/independent/fixture","content":"probe","extra":true}`,
	} {
		b, f := newDenialBackendFixture()
		b.effect = &toolEffectExpectation{Tool: "Write", Input: json.RawMessage(`{"file_path":"/independent/fixture","content":"probe"}`)}
		f.first[0].Tools[0].Name, f.first[0].Tools[0].Input = "Write", json.RawMessage(input)
		r := denialRequestFixture("", "", false)
		r.Tools[0] = json.RawMessage(`{"name":"Write","input_schema":{"type":"object"}}`)
		turn, err := b.Start(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Next(t.Context()); !errors.Is(err, inference.ErrRequest) || f.cancelled.Load() != 1 {
			t.Fatal("unapproved effect input reached the client")
		}
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestClaudeClientToolEffectPoliciesWithFakeACP(t *testing.T) {
	executable := os.Getenv("DAX_INTEROP_CLAUDE_BINARY")
	if executable == "" {
		t.Skip("set DAX_INTEROP_CLAUDE_BINARY for owned client effects with fake ACP; no external inference")
	}
	for _, kind := range []string{"allow-read", "allow-write", "allow-bash", "deny-write", "deny-bash", "hook-bash"} {
		t.Run(kind, func(t *testing.T) { runClientToolProbe(t, executable, "", kind) })
	}
}

type clientEffectProbe struct {
	expect                    *toolEffectExpectation
	path, manifest, pre, post string
	kind                      string
}

const ownedEffectText = "owned client effect"

func prepareClientEffect(t *testing.T, root, project, readPath, canary, kind, settings string) *clientEffectProbe {
	t.Helper()
	e := &clientEffectProbe{kind: kind, path: filepath.Join(project, "effect-fixture"), manifest: filepath.Join(root, "effect-expectation.json"), pre: filepath.Join(root, "effect-pre"), post: filepath.Join(root, "effect-post")}
	e.expect = &toolEffectExpectation{Tool: "Write", IsError: strings.HasPrefix(kind, "deny-") || kind == "hook-bash"}
	input := map[string]string{"file_path": e.path, "content": ownedEffectText}
	permissions := map[string]any{"defaultMode": "manual", "allow": []string{"Edit(/" + e.path + ")"}}
	switch kind {
	case "allow-read":
		e.expect.Tool, e.expect.ReadCanary, e.expect.RequiredText = "Read", true, canary
		e.path = readPath
		input = map[string]string{"file_path": readPath}
		permissions["allow"] = []string{"Read(/" + readPath + ")"}
	case "allow-write":
	case "deny-write":
		permissions["deny"] = []string{"Edit(/" + e.path + ")"}
	case "allow-bash", "deny-bash", "hook-bash":
		e.expect.Tool = "Bash"
		input = map[string]string{"command": "printf '%s' " + probeShellQuote(ownedEffectText) + " > " + probeShellQuote(e.path)}
		permissions["allow"] = []string{"Bash(printf *)", "Edit(/" + e.path + ")"}
		if kind == "deny-bash" {
			permissions["deny"] = []string{"Bash(printf *)"}
		}
	default:
		t.Fatal("unknown owned effect scenario")
	}
	e.expect.Input, _ = json.Marshal(input)
	preOutput := `{}`
	if kind == "hook-bash" {
		e.expect.RequiredText = clientDenialReason
		data, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": clientDenialReason}})
		preOutput = string(data)
	}
	hooks := map[string]any{}
	for _, h := range []struct{ event, marker, output string }{{"PreToolUse", e.pre, preOutput}, {"PostToolUse", e.post, `{}`}} {
		path := filepath.Join(root, h.event+"-effect.sh")
		script := "#!/bin/sh\nset -eu\numask 077\nprintf '%s' observed > " + probeShellQuote(h.marker) + "\nprintf '%s' " + probeShellQuote(h.output) + "\n"
		if os.WriteFile(path, []byte(script), 0700) != nil {
			t.Fatal("cannot prepare owned effect hook")
		}
		hooks[h.event] = []any{map[string]any{"matcher": e.expect.Tool, "hooks": []any{map[string]any{"type": "command", "command": probeShellQuote(path), "timeout": 2}}}}
	}
	data, _ := json.Marshal(map[string]any{"permissions": permissions, "hooks": hooks})
	if os.WriteFile(settings, data, 0600) != nil {
		t.Fatal("cannot write effect policy")
	}
	data, _ = json.Marshal(map[string]any{"input": e.expect.Input, "isError": e.expect.IsError, "requiredText": e.expect.RequiredText})
	if os.WriteFile(e.manifest, data, 0600) != nil {
		t.Fatal("cannot write independent effect expectation")
	}
	return e
}

func probeShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func (e *clientEffectProbe) check(t *testing.T) bool {
	t.Helper()
	pre, preErr := readDenialArtifact(filepath.Dir(e.pre), filepath.Base(e.pre), 32)
	post, postErr := readDenialArtifact(filepath.Dir(e.post), filepath.Base(e.post), 32)
	preSeen := preErr == nil && string(pre) == "observed"
	postSeen := postErr == nil && string(post) == "observed"
	effectOK := false
	if e.expect.IsError {
		_, err := os.Lstat(e.path)
		effectOK = errors.Is(err, os.ErrNotExist) && errors.Is(postErr, os.ErrNotExist)
		if e.kind == "hook-bash" {
			effectOK = effectOK && preSeen
		}
	} else if e.expect.ReadCanary {
		effectOK = preSeen && postSeen // The guarded result separately requires the exact read canary.
	} else {
		info, err := os.Lstat(e.path)
		if err == nil && info.Mode().IsRegular() && info.Size() <= 128 {
			f, openErr := os.Open(e.path)
			if openErr == nil {
				after, statErr := f.Stat()
				data, readErr := io.ReadAll(io.LimitReader(f, 129))
				closeErr := f.Close()
				effectOK = statErr == nil && os.SameFile(info, after) && readErr == nil && closeErr == nil && string(data) == ownedEffectText && preSeen && postSeen
			}
		}
	}
	t.Logf("client_effect=%s, pre_hook_seen=%v, post_hook_seen=%v, effect_matches_policy=%v", e.kind, preSeen, postSeen, effectOK)
	return effectOK
}
