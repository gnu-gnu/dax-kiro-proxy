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

func TestHistoricalToolImagesRemainNativeAndOwnedByTheirResult(t *testing.T) {
	picture, data := inlinePicture(t)
	for _, failed := range []bool{false, true} {
		r := projectedRequest(t, []any{
			map[string]any{"role": "user", "content": "read the owned image"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "image-call", "name": "Read", "input": map[string]string{"file_path": "/owned/image.png"}}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "image-call", "is_error": failed, "content": []any{map[string]string{"type": "text", "text": "before image"}, picture, map[string]string{"type": "text", "text": "after image"}}}}},
			map[string]any{"role": "assistant", "content": "image acknowledged"},
			map[string]any{"role": "user", "content": "continue without reading again"},
		})
		parts, err := FullWithCapabilities(r, acp.PromptCapabilities{Image: true})
		if err != nil {
			t.Fatal(err)
		}
		images := 0
		var owner string
		index, envelopes, ends := 0, 0, 0
		var sequence []string
		for _, p := range parts {
			if p.Type == "image" {
				images++
				if p.MIMEType != "image/png" || p.Data != data || owner != "image-call" || index != 2 {
					t.Fatal("historical tool image changed")
				}
				sequence = append(sequence, "image")
			} else if strings.Contains(p.Text, data) {
				t.Fatal("historical tool image was serialized as text")
			} else {
				var entry struct {
					Type    string
					ID      string `json:"tool_use_id"`
					IsError bool   `json:"is_error"`
					Count   int    `json:"content_blocks"`
					Index   int    `json:"content_index"`
					Content struct{ Type, Text, Source string }
				}
				if json.Unmarshal([]byte(p.Text), &entry) != nil {
					continue
				}
				switch entry.Type {
				case "tool_result":
					if owner != "" || entry.ID != "image-call" || entry.IsError != failed || entry.Count != 3 {
						t.Fatal("result envelope lost ownership")
					}
					owner = entry.ID
					envelopes++
				case "tool_result_content":
					if owner == "" || entry.ID != owner || entry.Index != index {
						t.Fatal("result content lost order")
					}
					index++
					if entry.Content.Type == "text" {
						sequence = append(sequence, entry.Content.Text)
					} else if entry.Content.Type != "image" || entry.Content.Source != "following_acp_image" {
						t.Fatal("result image binding missing")
					}
				case "tool_result_end":
					if owner == "" || entry.ID != owner || index != 3 {
						t.Fatal("result boundary lost")
					}
					owner = ""
					ends++
				}
			}
		}
		if images != 1 || owner != "" || envelopes != 1 || ends != 1 || strings.Join(sequence, "|") != "before image|image|after image" {
			t.Fatal("historical tool image or result boundaries missing")
		}
		if _, err := Full(r); err == nil {
			t.Fatal("text projection accepted an image result")
		}
		if _, err := FullWithCapabilities(r, acp.PromptCapabilities{}); err == nil {
			t.Fatal("historical tool image bypassed capability negotiation")
		}
		delta, err := DeltaWithCapabilities(r, 4, acp.PromptCapabilities{})
		if err != nil || len(delta) != 1 || delta[0].Text != "continue without reading again" {
			t.Fatal("committed image leaked into the text delta")
		}
	}
}

func TestToolImageHistoryRejectsInvalidMediaAndCombinedLimits(t *testing.T) {
	for _, kind := range []string{"invalid-base64", "wrong-mime", "broken-header", "dimension", "count", "combined-count", "total-bytes"} {
		t.Run(kind, func(t *testing.T) {
			picture, data := inlinePicture(t)
			source := picture["source"].(map[string]any)
			count := 1
			switch kind {
			case "invalid-base64":
				source["data"] = "%%%"
			case "wrong-mime":
				source["media_type"] = "image/jpeg"
			case "broken-header":
				source["data"] = base64.StdEncoding.EncodeToString([]byte("not a picture"))
			case "dimension":
				var buffer bytes.Buffer
				if png.Encode(&buffer, image.NewGray(image.Rect(0, 0, anthropic.MaxImageDimension+1, 1))) != nil {
					t.Fatal("dimension fixture")
				}
				source["data"] = base64.StdEncoding.EncodeToString(buffer.Bytes())
			case "count":
				count = anthropic.MaxMediaParts + 1
			case "combined-count":
				count = anthropic.MaxMediaParts
			case "total-bytes":
				raw, _ := base64.StdEncoding.DecodeString(data)
				padded := make([]byte, anthropic.MaxMediaTotalBytes/2+1)
				copy(padded, raw)
				source["data"] = base64.StdEncoding.EncodeToString(padded)
				count = 2
			}
			content := make([]any, count)
			for i := range content {
				content[i] = picture
			}
			first := []any{map[string]string{"type": "text", "text": "initial"}}
			if kind == "combined-count" {
				first = append(first, picture)
			}
			r := projectedRequest(t, []any{
				map[string]any{"role": "user", "content": first},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "bounded-image", "name": "Read", "input": map[string]string{"file_path": "/owned/image.png"}}}},
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "bounded-image", "content": content}}},
				map[string]any{"role": "assistant", "content": "received"},
				map[string]any{"role": "user", "content": "continue"},
			})
			before := append([]byte(nil), r.Messages[2].Content[0].Raw...)
			if _, err := FullWithCapabilities(r, acp.PromptCapabilities{Image: true}); err == nil {
				t.Fatal("invalid or excessive historical image accepted")
			}
			if !bytes.Equal(before, r.Messages[2].Content[0].Raw) {
				t.Fatal("stored result changed on rejection")
			}
		})
	}
}

func TestUnsupportedToolResultSourcesKeepOpaqueFallback(t *testing.T) {
	content := []any{
		map[string]any{"type": "image", "source": map[string]string{"type": "url", "url": "http://127.0.0.1/never-fetch"}},
		map[string]any{"type": "document", "source": map[string]string{"type": "text", "media_type": "text/plain", "data": "opaque document"}},
	}
	r := projectedRequest(t, []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "opaque-call", "content": content}}}})
	want := string(r.Messages[0].Content[0].Raw)
	parts, err := FullWithCapabilities(r, acp.PromptCapabilities{})
	if err != nil {
		t.Fatal("opaque result rejected")
	}
	count := 0
	for _, p := range parts {
		if p.Type != "text" {
			t.Fatal("unsupported result became native media")
		}
		if p.Text == want {
			count++
		}
	}
	if count != 1 {
		t.Fatal("opaque fallback was changed or repeated")
	}
}
