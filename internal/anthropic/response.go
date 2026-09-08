package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"dax-kiro-proxy/internal/schemawire"
)

type Usage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}
type ResponseBlock struct {
	Type  string          `json:"type"`
	Text  *string         `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}
type ToolUse struct {
	ID, Name string
	Input    json.RawMessage
}

func (t ToolUse) Block() ResponseBlock {
	return ResponseBlock{Type: "tool_use", ID: t.ID, Name: t.Name, Input: t.Input}
}
func (t ToolUse) Valid() bool {
	if len(t.ID) < 1 || len(t.ID) > 96 || len(t.Name) < 1 || len(t.Name) > 64 {
		return false
	}
	for _, s := range []string{t.ID, t.Name} {
		for _, c := range []byte(s) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return false
			}
		}
	}
	_, err := schemawire.Arguments(t.Input)
	return err == nil
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
func NewBlocksResponse(id, model string, blocks []ResponseBlock, reason string) Response {
	r := NewResponse(id, model, "", reason)
	r.Content = blocks
	return r
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
	writer      io.Writer
	flush       func() error
	index       int
	open, ended bool
}

func BeginTextStream(w io.Writer, flush func() error, id, model string) (*TextStream, error) {
	s, err := BeginStream(w, flush, id, model)
	if err != nil {
		return nil, err
	}
	if err := s.startText(); err != nil {
		return nil, err
	}
	return s, nil
}
func BeginStream(w io.Writer, flush func() error, id, model string) (*TextStream, error) {
	s := &TextStream{writer: w, flush: flush}
	message := Response{ID: id, Type: "message", Role: "assistant", Model: model, Content: []ResponseBlock{}}
	if err := s.event(streamEvent{Type: "message_start", Message: &message}); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *TextStream) startText() error {
	empty := ""
	if err := s.event(streamEvent{Type: "content_block_start", Index: &s.index, Block: &ResponseBlock{Type: "text", Text: &empty}}); err != nil {
		return err
	}
	s.open = true
	return nil
}
func (s *TextStream) Text(text string) error {
	if s.ended {
		return errors.New("message already ended")
	}
	if !s.open {
		if err := s.startText(); err != nil {
			return err
		}
	}
	return s.event(streamEvent{Type: "content_block_delta", Index: &s.index, Delta: textDelta{"text_delta", text}})
}
func (s *TextStream) stopBlock() error {
	if !s.open {
		return nil
	}
	if err := s.event(streamEvent{Type: "content_block_stop", Index: &s.index}); err != nil {
		return err
	}
	s.open = false
	s.index++
	return nil
}
func (s *TextStream) Tool(t ToolUse) error {
	if s.ended || !t.Valid() {
		return errors.New("invalid complete tool block")
	}
	index := s.index
	if s.open {
		index++
	}
	packet, err := toolPacket(t, index)
	if err != nil {
		return err
	}
	if err := s.stopBlock(); err != nil {
		return err
	}
	if _, err := s.writer.Write(packet); err != nil {
		return err
	}
	s.index++
	return s.flush()
}

// ToolBytes reserves the entire block before any tool JSON is exposed, including a possible text stop.
func (s *TextStream) ToolBytes(t ToolUse) (int, error) {
	if s.ended || !t.Valid() {
		return 0, errors.New("invalid complete tool block")
	}
	index := s.index
	if s.open {
		index++
	}
	packet, err := toolPacket(t, index)
	return len(packet) + 128, err
}
func toolPacket(t ToolUse, index int) ([]byte, error) {
	block := t.Block()
	block.Input = json.RawMessage(`{}`)
	delta := struct {
		Type    string `json:"type"`
		Partial string `json:"partial_json"`
	}{"input_json_delta", string(t.Input)}
	var output bytes.Buffer
	for _, event := range []streamEvent{{Type: "content_block_start", Index: &index, Block: &block}, {Type: "content_block_delta", Index: &index, Delta: delta}, {Type: "content_block_stop", Index: &index}} {
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&output, "event: %s\ndata: %s\n\n", event.Type, encoded)
	}
	return output.Bytes(), nil
}
func (s *TextStream) End(reason string) error {
	if s.ended {
		return errors.New("message already ended")
	}
	if s.index == 0 && !s.open {
		if err := s.startText(); err != nil {
			return err
		}
	}
	if err := s.stopBlock(); err != nil {
		return err
	}
	if err := s.event(streamEvent{Type: "message_delta", Delta: stopDelta{Reason: reason}, Usage: &Usage{}}); err != nil {
		return err
	}
	s.ended = true
	return s.event(streamEvent{Type: "message_stop"})
}
func (s *TextStream) Fail(message string) error {
	return s.packet("error", ErrorBody("api_error", message))
}

func (s *TextStream) Ping() error {
	if s.ended {
		return errors.New("message already ended")
	}
	return s.event(streamEvent{Type: "ping"})
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
