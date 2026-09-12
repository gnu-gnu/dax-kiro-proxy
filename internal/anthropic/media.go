package anthropic

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
	_ "golang.org/x/image/webp"
)

const (
	// The complete client history is bounded separately from one projected ACP prompt. This
	// permits proven deltas after earlier media has already been committed to the backend.
	MaxRequestMediaParts      = 256
	MaxRequestMediaTotalBytes = 12 << 20

	MaxMediaParts      = 20
	MaxMediaPartBytes  = 4 << 20
	MaxMediaTotalBytes = 6 << 20
	MaxImageDimension  = 8000
)

// Media retains validated inline source data. Pixel buffers and extracted document text are never
// allocated by the gateway; header validation is not a proof that every media payload is decodable.
type Media struct {
	Kind, MIME, Data, Text, Title, Context string
	Width, Height, Bytes                   int
}

func (b Block) Media() (Media, bool) {
	if b.media == nil || b.media.Kind != b.Type {
		return Media{}, false
	}
	return *b.media, true
}
func decodeMedia(block Block) (*Media, error) {
	fields, err := ndjson.Object(block.Raw)
	if err != nil {
		return nil, ErrRequest
	}
	m := &Media{Kind: block.Type}
	for key, value := range fields {
		switch key {
		case "type", "source", "cache_control":
		case "title", "context":
			if block.Type != "document" {
				return nil, ErrRequest
			}
			if string(value) == "null" {
				continue
			}
			var text string
			if !stringField(value, &text) {
				return nil, ErrRequest
			}
			if key == "title" {
				if len(text) > 8192 {
					return nil, ErrRequest
				}
				m.Title = text
			} else {
				if len(text) > 32768 {
					return nil, ErrRequest
				}
				m.Context = text
			}
		case "citations":
			c, err := ndjson.Object(value)
			if block.Type != "document" || err != nil || len(c) != 1 || string(c["enabled"]) != "false" {
				return nil, ErrRequest
			}
		default:
			return nil, ErrRequest
		}
	}
	source, err := ndjson.Object(fields["source"])
	var kind, data string
	if err != nil || len(source) != 3 || !stringField(source["type"], &kind) || !stringField(source["media_type"], &m.MIME) || !stringField(source["data"], &data) {
		return nil, ErrRequest
	}
	if kind == "text" && block.Type == "document" && m.MIME == "text/plain" {
		if len(data) == 0 || len(data) > MaxMediaPartBytes {
			return nil, ErrRequest
		}
		m.Text = data
		m.Bytes = len(data)
		return m, nil
	}
	if kind != "base64" || len(data) == 0 || len(data) > base64.StdEncoding.EncodedLen(MaxMediaPartBytes) || strings.ContainsAny(data, "\r\n") {
		return nil, ErrRequest
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil || len(decoded) == 0 || len(decoded) > MaxMediaPartBytes {
		return nil, ErrRequest
	}
	m.Data = data
	m.Bytes = len(decoded)
	if block.Type == "document" {
		if m.MIME != "application/pdf" || http.DetectContentType(decoded) != "application/pdf" {
			return nil, ErrRequest
		}
		return m, nil
	}
	expected := ""
	switch m.MIME {
	case "image/png":
		expected = "png"
	case "image/jpeg":
		expected = "jpeg"
	case "image/gif":
		expected = "gif"
	case "image/webp":
		expected = "webp"
	default:
		return nil, ErrRequest
	}
	header, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || format != expected || header.Width < 1 || header.Height < 1 || header.Width > MaxImageDimension || header.Height > MaxImageDimension {
		return nil, ErrRequest
	}
	m.Width = header.Width
	m.Height = header.Height
	return m, nil
}

// DocumentText keeps document metadata and source text in one unambiguous JSON object.
func (m Media) DocumentText() (string, error) {
	raw, err := json.Marshal(struct {
		Type    string `json:"type"`
		Title   string `json:"title,omitempty"`
		Context string `json:"context,omitempty"`
		Text    string `json:"text"`
	}{"document", m.Title, m.Context, m.Text})
	return string(raw), err
}
