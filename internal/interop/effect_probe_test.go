package interop_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
)

func TestEffectArgumentsCompareJSONValues(t *testing.T) {
	e := &toolEffectExpectation{Tool: "Bash", Input: json.RawMessage(`{"command":"printf foo \u003e /owned/fixture"}`)}
	for _, input := range []string{`{"command":"printf foo > /owned/fixture"}`, `{"command":"printf foo \u003e /owned/fixture"}`} {
		if !e.matches(anthropic.ToolUse{Name: "Bash", Input: json.RawMessage(input)}) {
			t.Fatal("equal decoded command was rejected because of JSON escaping")
		}
	}
	shape := e.compare(anthropic.ToolUse{Name: "Bash", Input: json.RawMessage(`{"command":"printf foo > /owned/fixture"}`)})
	if !shape.EncodingOnly || !shape.ValuesMatch || !shape.ValidObjects || shape.ExtraFields != 0 {
		t.Fatal("encoding-only difference was not distinguished")
	}
	for _, input := range []string{`{"command":"printf foo > /elsewhere"}`, `{"command":"printf foo > /owned/fixture","timeout":1000}`, `{"command":"bad","command":"printf foo > /owned/fixture"}`, `{"command":null}`} {
		if e.matches(anthropic.ToolUse{Name: "Bash", Input: json.RawMessage(input)}) {
			t.Fatal("a changed operation was accepted")
		}
	}
	shape = e.compare(anthropic.ToolUse{Name: "Bash", Input: json.RawMessage(`{"command":"printf foo > /owned/fixture","description":"private-description"}`)})
	encoded, _ := json.Marshal(shape)
	if !shape.ValuesMatch || shape.ExtraFields != 1 || !shape.ExtraDescription || strings.Contains(string(encoded), "private-description") || strings.Contains(string(encoded), "/owned") {
		t.Fatal("shape diagnostics lost the mismatch or retained values")
	}
	numbers := &toolEffectExpectation{Tool: "Bash", Input: json.RawMessage(`{"nested":{"number":9007199254740992}}`)}
	for _, raw := range []string{`{"nested":{"number":9007199254740993}}`, `{"nested":{"number":1,"number":9007199254740992}}`} {
		if numbers.matches(anthropic.ToolUse{Name: "Bash", Input: json.RawMessage(raw)}) {
			t.Fatal("numeric precision loss or duplicate nested key changed exact admission")
		}
	}
}

func TestBashEffectDescriptionDoesNotWidenCommandAdmission(t *testing.T) {
	e := &toolEffectExpectation{Tool: "Bash", Input: json.RawMessage(`{"command":"printf foo > /owned/fixture"}`)}
	for _, c := range []struct {
		input string
		valid bool
	}{
		{`{"command":"printf foo > /owned/fixture","description":"Create the owned fixture"}`, true},
		{`{"command":"printf foo > /elsewhere","description":"Create the owned fixture"}`, false},
		{`{"command":"printf foo > /owned/fixture","description":"ok","timeout":1}`, false},
		{`{"command":"printf foo > /owned/fixture","description":"ok","run_in_background":false}`, false},
		{`{"command":"printf foo > /owned/fixture","description":null}`, false},
		{`{"command":"printf foo > /owned/fixture","description":{"text":"ok"}}`, false},
		{`{"command":"printf foo > /owned/fixture","description":"line\nbreak"}`, false},
		{`{"command":"printf foo > /owned/fixture","description":"` + strings.Repeat("x", 257) + `"}`, false},
		{`{"command":"printf foo > /owned/fixture","description":"ok","description":"again"}`, false},
	} {
		if e.matches(anthropic.ToolUse{Name: "Bash", Input: json.RawMessage(c.input)}) != c.valid {
			t.Fatal("Bash description changed exact operation admission")
		}
	}
	write := &toolEffectExpectation{Tool: "Write", Input: json.RawMessage(`{"file_path":"/owned/fixture","content":"foo"}`)}
	if write.matches(anthropic.ToolUse{Name: "Write", Input: json.RawMessage(`{"file_path":"/owned/fixture","content":"foo","description":"extra"}`)}) {
		t.Fatal("Bash annotation allowance spread to another tool")
	}
}

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
	inspectNext               bool
	recoverNext               bool
	expect                    *toolEffectExpectation
	path, manifest, pre, post string
	kind                      string
	interactive               bool
	noDecision                bool
	bare                      bool
}

const ownedEffectText = "owned client effect"

func prepareClientEffect(t *testing.T, root, project, readPath, canary, kind, settings string) *clientEffectProbe {
	t.Helper()
	e := &clientEffectProbe{kind: kind, path: filepath.Join(project, "effect-fixture"), manifest: filepath.Join(root, "effect-expectation.json"), pre: filepath.Join(root, "effect-pre"), post: filepath.Join(root, "effect-post")}
	if strings.HasPrefix(kind, "ui-") {
		e.interactive = true
		kind = strings.TrimPrefix(kind, "ui-")
		if kind == "hold-write" {
			kind = "allow-write"
			e.noDecision = true
		}
		if kind == "bare-write" {
			kind = "deny-write"
			e.bare = true
		}
		if kind == "bare-next-write" || kind == "bare-recover-write" {
			e.recoverNext = kind == "bare-recover-write"
			kind = "deny-write"
			e.bare = true
			e.inspectNext = true
		}
	}
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
	if e.interactive {
		delete(permissions, "allow")
		delete(permissions, "deny")
		if e.expect.IsError && !e.bare {
			e.expect.RequiredText = clientDenialReason
		}
	}
	e.expect.Input, _ = json.Marshal(input)
	preOutput := `{}`
	if kind == "hook-bash" {
		e.expect.RequiredText = clientDenialReason
		data, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": clientDenialReason}})
		preOutput = string(data)
	}
	hooks := map[string]any{}
	if e.bare {
		for _, event := range []string{"Stop", "PostToolBatch"} {
			path := filepath.Join(root, event+"-bare.sh")
			script := "#!/bin/sh\nset -eu\numask 077\nprintf '%s' observed > " + probeShellQuote(filepath.Join(root, event+"-bare-observed")) + "\nprintf '%s' '{}'\n"
			if os.WriteFile(path, []byte(script), 0700) != nil {
				t.Fatal("cannot prepare bounded hook-presence observation")
			}
			hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": probeShellQuote(path), "timeout": 2}}}}
		}
	}
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

func (e *clientEffectProbe) beforeUse() bool {
	for _, marker := range []string{e.pre, e.post} {
		if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	if e.expect.ReadCanary {
		content, err := readDenialArtifact(filepath.Dir(e.path), filepath.Base(e.path), 128)
		return err == nil && string(content) == e.expect.RequiredText
	}
	_, err := os.Lstat(e.path)
	return errors.Is(err, os.ErrNotExist)
}

func (e *clientEffectProbe) check(t *testing.T) bool {
	t.Helper()
	pre, preErr := readDenialArtifact(filepath.Dir(e.pre), filepath.Base(e.pre), 32)
	post, postErr := readDenialArtifact(filepath.Dir(e.post), filepath.Base(e.post), 32)
	preSeen := preErr == nil && string(pre) == "observed"
	postSeen := postErr == nil && string(post) == "observed"
	effectOK := false
	if e.expect.IsError || e.noDecision {
		_, err := os.Lstat(e.path)
		effectOK = errors.Is(err, os.ErrNotExist) && errors.Is(postErr, os.ErrNotExist)
		if e.kind == "hook-bash" || e.interactive {
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
