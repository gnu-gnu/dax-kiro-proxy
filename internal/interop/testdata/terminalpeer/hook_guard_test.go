//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestHeldHookRequiresTheExactOwnedRead(t *testing.T) {
	valid := `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_use_id":"owned-call","tool_input":{"file_path":"/owned/read-fixture"}}`
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"exact", valid, true},
		{"other-tool", strings.Replace(valid, `"Read"`, `"Write"`, 1), false},
		{"other-event", strings.Replace(valid, "PreToolUse", "PostToolUse", 1), false},
		{"other-path", strings.Replace(valid, "/owned/read-fixture", "/unowned/read-fixture", 1), false},
		{"missing-id", strings.Replace(valid, `"owned-call"`, `""`, 1), false},
		{"extra-input", strings.Replace(valid, `"file_path":`, `"offset":1,"file_path":`, 1), false},
		{"oversized", strings.Repeat(" ", 64<<10) + valid, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inspectHeldHook([]byte(tc.input), "PreToolUse", "/owned/read-fixture")
			if (err == nil) != tc.valid {
				t.Fatal("unproven hook admitted or owned control refused")
			}
		})
	}
}
