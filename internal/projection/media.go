package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
)

type Resource struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	Blob     string `json:"blob"`
}
type Part struct {
	Type, Text, MIMEType, Data string
	Resource                   *Resource
}

func (p Part) MarshalJSON() ([]byte, error) {
	switch p.Type {
	case "text":
		return json.Marshal(Text{Type: "text", Text: p.Text})
	case "image":
		return json.Marshal(struct {
			Type string `json:"type"`
			MIME string `json:"mimeType"`
			Data string `json:"data"`
		}{"image", p.MIMEType, p.Data})
	case "resource":
		if p.Resource == nil {
			return nil, anthropic.ErrRequest
		}
		return json.Marshal(struct {
			Type     string    `json:"type"`
			Resource *Resource `json:"resource"`
		}{"resource", p.Resource})
	default:
		return nil, anthropic.ErrRequest
	}
}
func DeltaWithCapabilities(r *anthropic.Request, start int, caps acp.PromptCapabilities) ([]Part, error) {
	if r == nil || start < 0 || start > r.LatestUserIndex() {
		return nil, anthropic.ErrRequest
	}
	next := *r
	next.System = nil
	next.Messages = r.Messages[start:]
	return FullWithCapabilities(&next, caps)
}

// FullWithCapabilities keeps media in its native ACP form in historical and current messages.
// Text-only requests retain the original projection. JSON markers bind each following ordered block
// sequence to its role; they do not claim a native ACP system-role or citation feature.
func FullWithCapabilities(r *anthropic.Request, caps acp.PromptCapabilities) ([]Part, error) {
	if r == nil || !r.ClientContent() || r.LatestUserIndex() < 0 {
		return nil, anthropic.ErrRequest
	}
	hasMedia := false
	mediaCount, mediaBytes := 0, 0
	results := make(map[[2]int]anthropic.ToolResult)
	for i, message := range r.Messages {
		for j, block := range message.Content {
			content := []anthropic.Block{block}
			var result anthropic.ToolResult
			if block.Type == "tool_result" {
				var err error
				result, err = anthropic.DecodeToolResult(block.Raw)
				if err != nil {
					return nil, err
				}
				content, err = result.PromptContent()
				if err != nil {
					return nil, err
				}
				result.Content = content
			}
			for _, child := range content {
				if media, ok := child.Media(); ok {
					hasMedia = true
					mediaCount++
					mediaBytes += media.Bytes
					if mediaCount > anthropic.MaxMediaParts || mediaBytes > anthropic.MaxMediaTotalBytes || media.Kind == "image" && !caps.Image || media.MIME == "application/pdf" && !caps.EmbeddedContext {
						return nil, anthropic.ErrRequest
					}
					if block.Type == "tool_result" {
						results[[2]int{i, j}] = result
					}
				}
			}
		}
	}
	if !hasMedia {
		text, err := Full(r)
		if err != nil {
			return nil, err
		}
		parts := make([]Part, len(text))
		for i, p := range text {
			parts[i] = Part{Type: "text", Text: p.Text}
		}
		return parts, nil
	}
	parts := []Part{{Type: "text", Text: "Conversation messages follow in order. Each JSON role marker introduces that message's ordered text, images and documents."}}
	appendJSON := func(v any) error {
		raw, err := json.Marshal(v)
		if err == nil {
			parts = append(parts, Part{Type: "text", Text: string(raw)})
		}
		return err
	}
	if len(r.System) > 0 {
		content := make([]string, len(r.System))
		for i, b := range r.System {
			content[i] = b.Text
		}
		if err := appendJSON(struct {
			Role    string   `json:"role"`
			Scope   string   `json:"scope"`
			Content []string `json:"content"`
		}{"system", "top_level", content}); err != nil {
			return nil, err
		}
	}
	for i, message := range r.Messages {
		if err := appendJSON(struct {
			Role  string `json:"role"`
			Index int    `json:"message_index"`
		}{message.Role, i}); err != nil {
			return nil, err
		}
		for j, block := range message.Content {
			if result, ok := results[[2]int{i, j}]; ok {
				// The result envelope binds every following content part to its original call and
				// error status. Text stays JSON result content rather than becoming user instructions.
				if err := appendJSON(struct {
					Type    string `json:"type"`
					ID      string `json:"tool_use_id"`
					IsError bool   `json:"is_error"`
					Count   int    `json:"content_blocks"`
				}{"tool_result", result.ID, result.IsError, len(result.Content)}); err != nil {
					return nil, err
				}
				for index, child := range result.Content {
					entry := struct {
						Type    string          `json:"type"`
						ID      string          `json:"tool_use_id"`
						Index   int             `json:"content_index"`
						Content json.RawMessage `json:"content"`
					}{"tool_result_content", result.ID, index, child.Raw}
					media, image := child.Media()
					if image {
						entry.Content = json.RawMessage(`{"type":"image","source":"following_acp_image"}`)
					}
					if err := appendJSON(entry); err != nil {
						return nil, err
					}
					if image {
						parts = append(parts, Part{Type: "image", MIMEType: media.MIME, Data: media.Data})
					}
				}
				if err := appendJSON(struct {
					Type string `json:"type"`
					ID   string `json:"tool_use_id"`
				}{"tool_result_end", result.ID}); err != nil {
					return nil, err
				}
				continue
			}
			media, ok := block.Media()
			if !ok {
				text := block.Text
				if block.Type != "text" {
					text = string(block.Raw)
				}
				parts = append(parts, Part{Type: "text", Text: text})
				continue
			}
			if media.Kind == "image" {
				parts = append(parts, Part{Type: "image", MIMEType: media.MIME, Data: media.Data})
				continue
			}
			metadata, err := media.DocumentText()
			if err != nil {
				return nil, err
			}
			parts = append(parts, Part{Type: "text", Text: metadata})
			if media.MIME == "application/pdf" {
				digest := sha256.Sum256([]byte(media.Data))
				parts = append(parts, Part{Type: "resource", Resource: &Resource{URI: "urn:dax-kiro-proxy:document:" + hex.EncodeToString(digest[:]), MIMEType: media.MIME, Blob: media.Data}})
			}
		}
	}
	return parts, nil
}
