package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestPeerRequiresHandshakeListingAndOneExactCall(t *testing.T) {
	const init = "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n"
	const list = "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n"
	const call = "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"schema_probe\",\"arguments\":{\"tuple\":[7]}}}\n"
	for _, tc := range []struct {
		name, frames string
		valid        bool
		calls        int
	}{
		{"valid", init + list + call, true, 1},
		{"uninitialized", list + call, false, 0},
		{"unlisted", init + call, false, 0},
		{"duplicate", init + list + call + call, false, 1},
		{"wrong-argument", init + list + strings.ReplaceAll(call, "[7]", "[8]"), false, 0},
		{"large-frame", strings.Repeat("x", 64<<10), false, 0},
		{"many-frames", strings.Repeat("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/test\"}\n", 64), false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string
			var output bytes.Buffer
			ok := serve(strings.NewReader(tc.frames), &output, []byte(`{"type":"object"}`), func(s string) bool { events = append(events, s); return true })
			calls := 0
			for _, event := range events {
				if event == "called" {
					calls++
				}
			}
			if ok != tc.valid || calls != tc.calls {
				t.Fatal("peer admitted an invalid sequence or rejected its owned call")
			}
			if tc.valid && (!reflect.DeepEqual(events, []string{"initialized", "listed", "called"}) || bytes.Count(output.Bytes(), []byte("\n")) != 3) {
				t.Fatal("peer lifecycle or correlated response count changed")
			}
		})
	}
}
