package session_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

func mediaRequest(t *testing.T) *anthropic.Request {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	source := base64.StdEncoding.EncodeToString(b.Bytes())
	raw, err := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": source}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := anthropic.DecodeRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestNativeImageReachesFakeACPAndOnlyNewDeltaFollows(t *testing.T) {
	d := driver(t, "chat-continuity")
	r := mediaRequest(t)
	first, reply := observedTurn(t, d, r)
	var wire struct {
		Prompt []map[string]json.RawMessage `json:"prompt"`
	}
	if json.Unmarshal([]byte(reply), &wire) != nil {
		t.Fatal("invalid fake capture")
	}
	images := 0
	for _, p := range wire.Prompt {
		if string(p["type"]) == `"image"` {
			images++
			if len(p) != 3 || string(p["mimeType"]) != `"image/png"` || len(p["data"]) == 0 {
				t.Fatal("native image wire shape lost")
			}
		}
	}
	if images != 1 {
		t.Fatal("image did not reach the ACP child")
	}
	r.Messages = append(r.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: reply}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "new question"}}})
	second, _ := observedTurn(t, d, r)
	if first.PID != second.PID || second.Count != 2 || len(second.Prompt) != 1 || second.Prompt[0].Text != "new question" {
		t.Fatal("native history was resent or lost continuity")
	}
}
func TestUnsupportedMediaAndEncodedPromptLimitPreserveActiveSibling(t *testing.T) {
	for _, kind := range []string{"capability", "encoded-size"} {
		t.Run(kind, func(t *testing.T) {
			cfg := managerConfig(t, "pool-hang")
			cfg.Session.Process.Limits.FrameBytes = 2048
			m, err := session.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			active, err := m.Start(context.Background(), mainRequest(t, "active"))
			if err != nil {
				t.Fatal(err)
			}
			defer active.Cancel()
			bad := mainRequest(t, "invalid")
			if kind == "capability" {
				bad.Messages = mediaRequest(t).Messages
			} else {
				bad.Messages[0].Content[0].Text = strings.Repeat("<", 500)
			}
			if _, err = m.Start(context.Background(), bad); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("unsupported prompt was not rejected before dispatch", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if _, err = active.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("local prompt rejection retired an active sibling", err)
			}
		})
	}
}
