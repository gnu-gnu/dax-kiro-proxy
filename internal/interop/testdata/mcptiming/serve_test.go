package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type wireControl struct {
	conn   net.Conn
	reader *bufio.Reader
	done   chan bool
	cancel context.CancelFunc
	events []string
	once   sync.Once
	valid  bool
}

func openWire(t *testing.T) *wireControl {
	t.Helper()
	client, server := net.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	w := &wireControl{conn: client, reader: bufio.NewReader(client), done: make(chan bool, 1), cancel: cancel}
	go func() {
		w.done <- serve(ctx, server, server, "owned_wait", 20*time.Millisecond, func(kind string) bool { w.events = append(w.events, kind); return true })
	}()
	t.Cleanup(func() { w.finish(t) })
	return w
}

func (w *wireControl) finish(t *testing.T) bool {
	t.Helper()
	w.once.Do(func() {
		w.conn.Close()
		w.cancel()
		select {
		case w.valid = <-w.done:
		case <-time.After(time.Second):
			t.Fatal("independent peer did not join")
		}
	})
	return w.valid
}

func (w *wireControl) send(t *testing.T, value any) {
	t.Helper()
	if w.conn.SetWriteDeadline(time.Now().Add(time.Second)) != nil || json.NewEncoder(w.conn).Encode(value) != nil {
		t.Fatal("owned MCP input failed")
	}
}
func (w *wireControl) read(t *testing.T, id any) map[string]json.RawMessage {
	t.Helper()
	if w.conn.SetReadDeadline(time.Now().Add(time.Second)) != nil {
		t.Fatal("cannot bound owned read")
	}
	line, err := w.reader.ReadBytes('\n')
	var frame map[string]json.RawMessage
	var got any
	if err != nil || len(line) > 64<<10 || json.Unmarshal(line, &frame) != nil || json.Unmarshal(frame["id"], &got) != nil || !reflect.DeepEqual(got, id) || frame["error"] != nil {
		t.Fatal("MCP response was missing, invalid or miscorrelated")
	}
	return frame
}

func request(id any, method string, params any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
}
func (w *wireControl) prepare(t *testing.T) {
	t.Helper()
	w.send(t, request(1, "initialize", map[string]string{"protocolVersion": "2025-03-26"}))
	r := w.read(t, float64(1))
	var init struct{ ProtocolVersion string }
	if json.Unmarshal(r["result"], &init) != nil || init.ProtocolVersion != "2025-03-26" {
		t.Fatal("MCP version was changed")
	}
	w.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	w.send(t, request("list-request", "tools/list", map[string]any{}))
	r = w.read(t, "list-request")
	var listed struct {
		Tools []struct {
			Name        string
			InputSchema json.RawMessage
		}
	}
	if json.Unmarshal(r["result"], &listed) != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "owned_wait" {
		t.Fatal("unexpected MCP tool inventory")
	}
}

func TestDelayedCallKeepsCorrelationAndCancellationDistinct(t *testing.T) {
	for _, mode := range []string{"ordinary", "foreign", "matching", "repeated", "after-result"} {
		t.Run(mode, func(t *testing.T) {
			w := openWire(t)
			w.prepare(t)
			w.send(t, request("call-request", "tools/call", map[string]any{"name": "owned_wait", "arguments": map[string]any{}}))
			cancel := func(id string) {
				w.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]string{"requestId": id, "reason": "independent cancellation"}})
			}
			switch mode {
			case "foreign":
				cancel("another-request")
			case "matching":
				cancel("call-request")
			case "repeated":
				cancel("call-request")
				cancel("call-request")
			}
			r := w.read(t, "call-request")
			var result struct {
				IsError bool
				Content []struct{ Type, Text string }
			}
			if json.Unmarshal(r["result"], &result) != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != "Owned delayed result is available." {
				t.Fatal("delayed tool result changed")
			}
			if mode == "after-result" {
				cancel("call-request")
			}
			// A round-trip barrier ensures post-response cancellation was processed before closure.
			w.send(t, request(7, "ping", map[string]any{}))
			w.read(t, float64(7))
			if !w.finish(t) {
				t.Fatal("valid finite MCP session rejected")
			}
			counts := map[string]int{}
			for _, kind := range w.events {
				counts[kind]++
			}
			late := mode == "matching" || mode == "repeated"
			if counts["call_received"] != 1 || counts["call_sent"]+counts["late_call_sent"] != 1 || (counts["late_call_sent"] == 1) != late {
				t.Fatal("call count or late-response observation changed")
			}
			if (counts["foreign_cancel"] == 1) != (mode == "foreign") || (counts["after_result_cancel"] == 1) != (mode == "after-result") || (counts["repeated_cancel"] == 1) != (mode == "repeated") {
				t.Fatal("cancellation ownership or timing was conflated")
			}
		})
	}
}

func TestPeerRejectsUnreadyWrongOrRepeatedCalls(t *testing.T) {
	for _, mode := range []string{"unready", "wrong-tool", "extra-argument", "null-argument", "second-call", "duplicate-id"} {
		t.Run(mode, func(t *testing.T) {
			w := openWire(t)
			if mode != "unready" {
				w.prepare(t)
			}
			name := "owned_wait"
			var args any = map[string]any{}
			if mode == "wrong-tool" {
				name = "another_tool"
			}
			if mode == "extra-argument" {
				args = map[string]any{"command": "independent"}
			}
			if mode == "null-argument" {
				args = nil
			}
			id := any("owned-call")
			if mode == "duplicate-id" {
				id = "list-request"
			}
			w.send(t, request(id, "tools/call", map[string]any{"name": name, "arguments": args}))
			if mode == "second-call" {
				w.read(t, id)
				w.send(t, request("second-call", "tools/call", map[string]any{"name": name, "arguments": args}))
			}
			if w.conn.SetReadDeadline(time.Now().Add(time.Second)) != nil {
				t.Fatal("cannot bound rejection")
			}
			if _, err := w.reader.ReadByte(); err == nil {
				t.Fatal("invalid call received output")
			}
			if w.finish(t) {
				t.Fatal("invalid call was accepted")
			}
		})
	}
}

func TestPeerRejectsWireAndLedgerExcess(t *testing.T) {
	for _, mode := range []string{"oversized", "invalid-utf8", "invalid-id", "request-ledger", "frame-count"} {
		t.Run(mode, func(t *testing.T) {
			w := openWire(t)
			switch mode {
			case "oversized", "invalid-utf8":
				data := strings.Repeat("x", 64<<10) + "\n"
				if mode == "invalid-utf8" {
					data = "\xff\n"
				}
				w.conn.SetWriteDeadline(time.Now().Add(time.Second))
				_, _ = w.conn.Write([]byte(data))
			case "invalid-id":
				w.send(t, request(nil, "ping", map[string]any{}))
			case "request-ledger":
				for i := 0; i < 16; i++ {
					w.send(t, request(i, "ping", map[string]any{}))
					w.read(t, float64(i))
				}
				w.send(t, request(16, "ping", map[string]any{}))
			case "frame-count":
				for i := 0; i < 32; i++ {
					w.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 1}})
				}
			}
			w.conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := w.reader.ReadByte(); err == nil {
				t.Fatal("excess input received a response")
			}
			if w.finish(t) {
				t.Fatal("excess input admitted")
			}
		})
	}
}

func TestDefaultWaitPeerReleasesPendingLongCallOnShutdown(t *testing.T) {
	client, server := net.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	w := &wireControl{conn: client, reader: bufio.NewReader(client), done: make(chan bool, 1), cancel: cancel}
	go func() {
		w.done <- serveWithin(ctx, server, server, "owned_wait", 135*time.Second, 135*time.Second, func(kind string) bool { w.events = append(w.events, kind); return true })
	}()
	t.Cleanup(func() { w.finish(t) })
	w.prepare(t)
	w.send(t, request("call-request", "tools/call", map[string]any{"name": "owned_wait", "arguments": map[string]any{}}))
	w.send(t, request("barrier", "ping", map[string]any{}))
	w.read(t, "barrier")
	if !w.finish(t) {
		t.Fatal("long wait did not join on shutdown")
	}
	counts := map[string]int{}
	for _, kind := range w.events {
		counts[kind]++
	}
	if counts["call_received"] != 1 || counts["call_sent"] != 0 || counts["late_call_sent"] != 0 {
		t.Fatal("pending long call was not preserved until shutdown")
	}
}

func TestDefaultWaitPeerRequiresExactObservationProfile(t *testing.T) {
	for _, cfg := range []configuration{
		{DelayMilliseconds: 135000},
		{DelayMilliseconds: 135000, Observation: "unknown"},
		{DelayMilliseconds: 3000, Observation: "default-wait-135s"},
		{DelayMilliseconds: 135001, Observation: "default-wait-135s"},
	} {
		if _, _, _, ok := peerBounds(cfg); ok {
			t.Fatal("unapproved peer lifetime admitted")
		}
	}
	for _, cfg := range []configuration{{DelayMilliseconds: 3000}, {DelayMilliseconds: 135000, Observation: "default-wait-135s"}} {
		limit, outer, delay, ok := peerBounds(cfg)
		if !ok || outer <= limit || delay >= limit || outer > 235*time.Second {
			t.Fatal("invalid peer observation envelope")
		}
	}
}
