package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
)

type Usage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}
type ResponseBlock struct {
	Type string  `json:"type"`
	Text *string `json:"text,omitempty"`
}
type Response struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Model        string          `json:"model"`
	Content      []ResponseBlock `json:"content"`
	StopReason   *string         `json:"stop_reason"`
	StopSequence *string         `json:"stop_sequence"`
	Usage        Usage           `json:"usage"`
}

func NewResponse(id, model, text, reason string) Response {
	return Response{ID: id, Type: "message", Role: "assistant", Model: model, Content: []ResponseBlock{{Type: "text", Text: &text}}, StopReason: &reason}
}

type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
type ErrorEnvelope struct {
	Type  string   `json:"type"`
	Error APIError `json:"error"`
}

func ErrorBody(kind, message string) ErrorEnvelope {
	return ErrorEnvelope{Type: "error", Error: APIError{kind, message}}
}

type textDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type stopDelta struct {
	Reason   string  `json:"stop_reason"`
	Sequence *string `json:"stop_sequence"`
}
type streamEvent struct {
	Type    string         `json:"type"`
	Message *Response      `json:"message,omitempty"`
	Index   *int           `json:"index,omitempty"`
	Block   *ResponseBlock `json:"content_block,omitempty"`
	Delta   any            `json:"delta,omitempty"`
	Usage   *Usage         `json:"usage,omitempty"`
}
type TextStream struct {
	writer io.Writer
	flush  func() error
}

func BeginTextStream(w io.Writer, flush func() error, id, model string) (*TextStream, error) {
	s := &TextStream{w, flush}
	message := Response{ID: id, Type: "message", Role: "assistant", Model: model, Content: []ResponseBlock{}}
	if err := s.event(streamEvent{Type: "message_start", Message: &message}); err != nil {
		return nil, err
	}
	index := 0
	empty := ""
	if err := s.event(streamEvent{Type: "content_block_start", Index: &index, Block: &ResponseBlock{Type: "text", Text: &empty}}); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *TextStream) Text(text string) error {
	index := 0
	return s.event(streamEvent{Type: "content_block_delta", Index: &index, Delta: textDelta{"text_delta", text}})
}
func (s *TextStream) End(reason string) error {
	index := 0
	if err := s.event(streamEvent{Type: "content_block_stop", Index: &index}); err != nil {
		return err
	}
	if err := s.event(streamEvent{Type: "message_delta", Delta: stopDelta{Reason: reason}, Usage: &Usage{}}); err != nil {
		return err
	}
	return s.event(streamEvent{Type: "message_stop"})
}
func (s *TextStream) Fail(message string) error {
	return s.packet("error", ErrorBody("api_error", message))
}
func (s *TextStream) event(e streamEvent) error { return s.packet(e.Type, e) }
func (s *TextStream) packet(name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	return s.flush()
}
