package anthropic

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func mediaBody(t *testing.T, blocks []any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"model": "claude-dax-fixture", "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": blocks}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func imageBlock(mime, data string) map[string]any {
	return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mime, "data": data}}
}
func TestInlineImagesAreValidatedWithoutPixelDecoding(t *testing.T) {
	picture := image.NewPaletted(image.Rect(0, 0, 2, 3), color.Palette{color.Black, color.White})
	picture.SetColorIndex(1, 2, 1)
	for _, kind := range []string{"png", "jpeg", "gif"} {
		t.Run(kind, func(t *testing.T) {
			var encoded bytes.Buffer
			var err error
			switch kind {
			case "png":
				err = png.Encode(&encoded, picture)
			case "jpeg":
				err = jpeg.Encode(&encoded, picture, nil)
			case "gif":
				err = gif.Encode(&encoded, picture, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			source := base64.StdEncoding.EncodeToString(encoded.Bytes())
			r, err := DecodeRequest(mediaBody(t, []any{imageBlock("image/"+kind, source)}))
			if err != nil || !r.ClientContent() {
				t.Fatal("independently encoded image rejected")
			}
			part, ok := r.Messages[0].Content[0].Media()
			if !ok || part.Width != 2 || part.Height != 3 || part.Data != source || part.Bytes != encoded.Len() {
				t.Fatal("validated media metadata or original bytes lost")
			}
			if _, err = DecodeRequest(mediaBody(t, []any{imageBlock("image/webp", source)})); err == nil {
				t.Fatal("MIME mismatch accepted")
			}
		})
	}
}
func TestMediaSourcesLimitsAndUnsupportedControls(t *testing.T) {
	invalid := []any{
		imageBlock("image/png", "AQID"), imageBlock("image/svg+xml", "PHN2Zy8+"), imageBlock("image/webp", "AQID"),
		imageBlock("image/png", "AA\n=="), imageBlock("image/png", strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxMediaPartBytes)+4)),
		map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "http://127.0.0.1/private"}},
		map[string]any{"type": "document", "source": map[string]any{"type": "file", "file_id": "fixture-unavailable"}},
		map[string]any{"type": "document", "source": map[string]any{"type": "text", "media_type": "text/plain", "data": "contents"}, "citations": map[string]bool{"enabled": true}},
	}
	for i, block := range invalid {
		if _, err := DecodeRequest(mediaBody(t, []any{block})); err == nil {
			t.Fatalf("invalid media case %d accepted", i)
		}
	}
	doc := map[string]any{"type": "document", "source": map[string]any{"type": "text", "media_type": "text/plain", "data": "synthetic document"}, "title": "fixture", "context": "ordered context", "citations": map[string]bool{"enabled": false}}
	r, err := DecodeRequest(mediaBody(t, []any{doc}))
	if err != nil || !r.ClientContent() {
		t.Fatal("plain text document rejected")
	}
	part, ok := r.Messages[0].Content[0].Media()
	if !ok || part.Text != "synthetic document" || part.Title != "fixture" || part.Context != "ordered context" {
		t.Fatal("document content metadata lost")
	}
	many := make([]any, MaxMediaParts+1)
	for i := range many {
		many[i] = doc
	}
	if _, err := DecodeRequest(mediaBody(t, many)); err == nil {
		t.Fatal("media count limit ignored")
	}
	large := map[string]any{"type": "document", "source": map[string]any{"type": "text", "media_type": "text/plain", "data": strings.Repeat("x", MaxMediaPartBytes)}}
	if _, err := DecodeRequest(mediaBody(t, []any{large, large})); err == nil {
		t.Fatal("aggregate media bytes ignored")
	}
}

func TestImageDimensionLimit(t *testing.T) {
	for _, width := range []int{MaxImageDimension, MaxImageDimension + 1} {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, width, 1))); err != nil {
			t.Fatal(err)
		}
		_, err := DecodeRequest(mediaBody(t, []any{imageBlock("image/png", base64.StdEncoding.EncodeToString(b.Bytes()))}))
		if (err == nil) != (width == MaxImageDimension) {
			t.Fatal("image dimension boundary was not enforced")
		}
	}
}
