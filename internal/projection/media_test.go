package projection

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
)

func inlinePicture(t *testing.T) (map[string]any, string) {
	t.Helper()
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(raw.Bytes())
	return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": data}}, data
}
func projectedRequest(t *testing.T, messages []any) *anthropic.Request {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"model": "claude-dax-fixture", "max_tokens": 32, "system": "stable context", "messages": messages})
	if err != nil {
		t.Fatal(err)
	}
	r, err := anthropic.DecodeRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestHistoricalAndCurrentImagesKeepTheirNativeOrder(t *testing.T) {
	picture, data := inlinePicture(t)
	r := projectedRequest(t, []any{map[string]any{"role": "user", "content": []any{picture, map[string]any{"type": "text", "text": "old caption"}}}, map[string]any{"role": "assistant", "content": "old answer"}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "new caption"}, picture}}, map[string]any{"role": "system", "content": "last context"}})
	if _, err := FullWithCapabilities(r, acp.PromptCapabilities{}); err == nil {
		t.Fatal("image sent without a negotiated capability")
	}
	parts, err := FullWithCapabilities(r, acp.PromptCapabilities{Image: true})
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	var sequence []string
	for _, part := range parts {
		if part.Type == "image" {
			images++
			if part.Data != data || part.MIMEType != "image/png" {
				t.Fatal("image data changed")
			}
			sequence = append(sequence, "image")
		} else {
			if strings.Contains(part.Text, data) {
				t.Fatal("native history image became base64 text")
			}
			switch part.Text {
			case "old caption", "old answer", "new caption", "last context":
				sequence = append(sequence, part.Text)
			}
		}
	}
	if images != 2 || strings.Join(sequence, "|") != "image|old caption|old answer|new caption|image|last context" {
		t.Fatal("native media or message order lost")
	}
	delta, err := DeltaWithCapabilities(r, 2, acp.PromptCapabilities{Image: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(delta)
	if bytes.Contains(raw, []byte("old caption")) || bytes.Contains(raw, []byte("stable context")) || bytes.Count(raw, []byte(`"type":"image"`)) != 1 {
		t.Fatal("proven delta repeated historical media/context")
	}
}
func TestDocumentProjectionRequiresEmbeddingOnlyForBinaryContent(t *testing.T) {
	doc := map[string]any{"type": "document", "title": "fixture document", "context": "context note", "source": map[string]any{"type": "text", "media_type": "text/plain", "data": "plain document"}}
	r := projectedRequest(t, []any{map[string]any{"role": "user", "content": []any{doc}}})
	parts, err := FullWithCapabilities(r, acp.PromptCapabilities{})
	if err != nil {
		t.Fatal("text document depended on optional embedding")
	}
	raw, _ := json.Marshal(parts)
	for _, want := range []string{"plain document", "fixture document", "context note"} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatal("document metadata lost")
		}
	}
	// Only the proxy's header/transport contract is exercised by this synthetic PDF-shaped payload.
	data := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nsynthetic header test\n%%EOF\n"))
	doc["source"] = map[string]any{"type": "base64", "media_type": "application/pdf", "data": data}
	r = projectedRequest(t, []any{map[string]any{"role": "user", "content": []any{doc}}})
	if _, err := FullWithCapabilities(r, acp.PromptCapabilities{Image: true}); err == nil {
		t.Fatal("PDF silently degraded without embedding")
	}
	parts, err = FullWithCapabilities(r, acp.PromptCapabilities{EmbeddedContext: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range parts {
		if p.Type == "resource" {
			found = true
			if p.Resource == nil || p.Resource.Blob != data || !strings.HasPrefix(p.Resource.URI, "urn:dax-kiro-proxy:document:") {
				t.Fatal("binary resource contract lost")
			}
		}
	}
	if !found {
		t.Fatal("PDF missing from ACP resource content")
	}
}
