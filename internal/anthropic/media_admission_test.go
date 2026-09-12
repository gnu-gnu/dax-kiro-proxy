package anthropic

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestRequestMediaEnvelopeIncludesHistoryAndResultImages(t *testing.T) {
	var picture bytes.Buffer
	if png.Encode(&picture, image.NewGray(image.Rect(0, 0, 2, 2))) != nil {
		t.Fatal("cannot encode independent image")
	}
	part := imageBlock("image/png", base64.StdEncoding.EncodeToString(picture.Bytes()))
	for _, tc := range []struct {
		name        string
		top, nested int
		valid       bool
	}{
		{"past-dispatch", 21, 0, true}, {"request-boundary", 256, 0, true}, {"request-excess", 257, 0, false},
		{"combined-boundary", 128, 128, true}, {"combined-excess", 128, 129, false}, {"nested-excess", 0, 257, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var top, children []any
			for range tc.top {
				top = append(top, part)
			}
			for range tc.nested {
				children = append(children, part)
			}
			var result []byte
			if len(children) > 0 {
				block := map[string]any{"type": "tool_result", "tool_use_id": "owned-media-call", "content": children}
				result, _ = json.Marshal(block)
				top = append(top, json.RawMessage(result))
			}
			r, err := DecodeRequest(mediaBody(t, top))
			if (err == nil) != tc.valid {
				t.Fatalf("request media count admission mismatch: accepted=%v", err == nil)
			}
			if err == nil && result != nil && !bytes.Equal(r.Messages[0].Content[len(top)-1].Raw, result) {
				t.Fatal("validating result images changed their stored JSON")
			}
		})
	}
}

func TestRequestMediaByteEnvelopeAndHistoricalValidation(t *testing.T) {
	doc := func(n int) any {
		return map[string]any{"type": "document", "source": map[string]string{"type": "text", "media_type": "text/plain", "data": strings.Repeat("x", n)}}
	}
	for _, tc := range []struct {
		name  string
		sizes []int
		valid bool
	}{
		{"past-dispatch", []int{4 << 20, 4 << 20}, true},
		{"request-boundary", []int{4 << 20, 4 << 20, 4 << 20}, true},
		{"request-excess", []int{4 << 20, 4 << 20, 4 << 20, 1}, false},
		{"part-excess", []int{(4 << 20) + 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := make([]any, 0, len(tc.sizes))
			for _, n := range tc.sizes {
				parts = append(parts, doc(n))
			}
			body := mediaBody(t, parts)
			if len(body) > MaxBodyBytes {
				t.Fatal("byte boundary control exceeded HTTP envelope")
			}
			_, err := DecodeRequest(body)
			if (err == nil) != tc.valid {
				t.Fatalf("request media byte admission mismatch: accepted=%v", err == nil)
			}
		})
	}
	for _, data := range []string{"%%%", base64.StdEncoding.EncodeToString([]byte("invalid image header"))} {
		body, err := json.Marshal(map[string]any{"model": "claude-dax-fixture", "max_tokens": 32, "messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "owned-media-call", "content": []any{imageBlock("image/png", data)}}}},
			map[string]string{"role": "assistant", "content": "owned completed reply"},
			map[string]string{"role": "user", "content": "new text question"},
		}})
		if err != nil {
			t.Fatal("cannot encode owned history")
		}
		if _, err := DecodeRequest(body); err == nil {
			t.Fatal("invalid historical result image bypassed input validation")
		}
	}
}

func TestRequestByteBudgetCombinesDocumentsAndResultImages(t *testing.T) {
	var picture bytes.Buffer
	if png.Encode(&picture, image.NewGray(image.Rect(0, 0, 2, 2))) != nil {
		t.Fatal("cannot encode independent header")
	}
	// This padded payload exercises the existing header/byte contract, not full image rendering.
	imageData := make([]byte, 4<<20)
	copy(imageData, picture.Bytes())
	part := imageBlock("image/png", base64.StdEncoding.EncodeToString(imageData))
	doc := map[string]any{"type": "document", "source": map[string]string{"type": "text", "media_type": "text/plain", "data": strings.Repeat("x", 4<<20)}}
	result := map[string]any{"type": "tool_result", "tool_use_id": "owned-mixed-media", "content": []any{part}}
	for _, extra := range []bool{false, true} {
		blocks := []any{doc, doc, result}
		if extra {
			blocks = append(blocks, map[string]any{"type": "document", "source": map[string]string{"type": "text", "media_type": "text/plain", "data": "x"}})
		}
		body := mediaBody(t, blocks)
		if len(body) > MaxBodyBytes {
			t.Fatal("mixed byte control exceeds HTTP envelope")
		}
		_, err := DecodeRequest(body)
		if (err != nil) != extra {
			t.Fatalf("mixed media byte admission mismatch: extra=%v accepted=%v", extra, err == nil)
		}
	}
}
