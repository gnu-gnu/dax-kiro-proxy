//go:build darwin || linux

package session_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestChurnContinuationObservationRejectsMismatchedHistory(t *testing.T) {
	state := churnConversation{observation: observedPrompt{PID: 321, Session: "owned"}}
	for _, fault := range []string{"none", "pid", "session", "count", "loaded", "extra-history", "wrong-delta"} {
		t.Run(fault, func(t *testing.T) {
			data := map[string]any{"pid": 321, "session": "owned", "promptCount": 2, "loaded": false, "prompt": []map[string]string{{"text": "CHURN_HOLD_1"}}}
			switch fault {
			case "pid":
				data["pid"] = 322
			case "session":
				data["session"] = "foreign"
			case "count":
				data["promptCount"] = 1
			case "loaded":
				data["loaded"] = true
			case "extra-history":
				data["prompt"] = []map[string]string{{"text": "old"}, {"text": "CHURN_HOLD_1"}}
			case "wrong-delta":
				data["prompt"] = []map[string]string{{"text": "CHURN_HOLD_2"}}
			}
			body, _ := json.Marshal(data)
			if valid := checkChurnDelta(state, string(body), "CHURN_HOLD_1") == nil; valid != (fault == "none") {
				t.Fatal("continuation observation accepted the wrong ownership or history")
			}
		})
	}
}

type churnResponse string

func (r churnResponse) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(string(r)))}, nil
}

func TestChurnStreamCannotCountTerminalOutputAsCancellation(t *testing.T) {
	state := churnConversation{observation: observedPrompt{PID: 321, Session: "owned"}}
	observation := `{"pid":321,"session":"owned","promptCount":2,"prompt":[{"text":"CHURN_HOLD_1"}]}`
	data, _ := json.Marshal(map[string]any{"type": "content_block_delta", "delta": map[string]string{"type": "text_delta", "text": observation}})
	for _, ending := range []string{"", `{"type":"message_stop"}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"error"}`, `{malformed}`} {
		body := "data: " + string(data) + "\n\n"
		if ending != "" {
			body += "data: " + ending + "\n\n"
		}
		client := churnClient{url: "http://owned.invalid", client: &http.Client{Transport: churnResponse(body)}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		ready := make(chan struct{}, 1)
		err := client.stream(ctx, state, "CHURN_HOLD_1", ready)
		if (err == nil) != (ending == "") || len(ready) != 1 {
			t.Fatal("terminal or malformed stream counted as caller cancellation")
		}
	}
}

func TestChurnDescriptorObserverDetectsRetainedHandles(t *testing.T) {
	base, err := measureChurnResources()
	if err != nil {
		t.Fatal(err)
	}
	var handles []*os.File
	defer func() {
		for _, handle := range handles {
			if err := handle.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	for range 8 {
		handle, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
	}
	now, err := measureChurnResources()
	if err != nil || now.FDs < base.FDs+8 || checkChurnResources(base, now) == nil {
		t.Fatal("retained descriptor control was invisible to the observer")
	}
}
