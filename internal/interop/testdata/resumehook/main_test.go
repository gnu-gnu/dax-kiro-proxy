package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHeldResumeHookRequiresOwnedSessionCallAndCommand(t *testing.T) {
	want := expectation{Session: "owned-native-session", Command: "printf owned >> /owned/effect"}
	base := `{"session_id":"owned-native-session","hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"owned-call","tool_input":{"command":"printf owned >> /owned/effect"}}`
	digest, err := inspect([]byte(base), "PreToolUse", want)
	if err != nil || len(digest) != 64 || strings.Contains(digest, "owned-call") {
		t.Fatal("owned hook rejected or identifier not digested")
	}
	for _, change := range [][2]string{
		{"owned-native-session", "other-session"}, {"PreToolUse", "PostToolUse"}, {"Bash", "Write"}, {"owned-call", ""}, {"owned-call", "invalid/id"}, {"printf owned >> /owned/effect", "printf owned >> /other/effect"},
		{`"command":"printf owned >> /owned/effect"`, `"command":"printf owned >> /owned/effect","timeout":1000`},
		{`"command":"printf owned >> /owned/effect"`, `"command":"printf owned >> /owned/effect","description":"line\nbreak"`},
	} {
		if _, err := inspect([]byte(strings.Replace(base, change[0], change[1], 1)), "PreToolUse", want); err == nil {
			t.Fatal("unowned hook accepted")
		}
	}
	if _, err := inspect([]byte(strings.Repeat("x", (64<<10)+1)), "PreToolUse", want); err == nil {
		t.Fatal("unbounded hook accepted")
	}
	var packet map[string]any
	if json.Unmarshal([]byte(base), &packet) != nil {
		t.Fatal("fixture")
	}
	packet["tool_input"].(map[string]any)["description"] = "Owned operation"
	raw, _ := json.Marshal(packet)
	if other, err := inspect(raw, "PreToolUse", want); err != nil || other != digest {
		t.Fatal("bounded display annotation changed owned call")
	}
	post := strings.Replace(base, "PreToolUse", "PostToolUse", 1)
	if other, err := inspect([]byte(post), "PostToolUse", want); err != nil || other != digest {
		t.Fatal("matching post hook lost call identity")
	}
}
