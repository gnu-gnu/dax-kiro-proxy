// Package ndjson implements the bounded wire framing shared by the ACP and relay adapters.
package ndjson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var (
	ErrTooLarge = errors.New("JSON frame exceeds byte limit")
	ErrInvalid  = errors.New("invalid JSON frame")
)

type Reader struct {
	input *bufio.Reader
	limit int
}

func NewReader(input io.Reader, limit int) *Reader {
	return &Reader{bufio.NewReaderSize(input, 4096), limit}
}
func (r *Reader) Read() ([]byte, error) {
	var frame []byte
	for {
		part, err := r.input.ReadSlice('\n')
		terminated := len(part) > 0 && part[len(part)-1] == '\n'
		if terminated {
			part = part[:len(part)-1]
		}
		if len(part) > r.limit-len(frame) {
			return nil, ErrTooLarge
		}
		frame = append(frame, part...)
		if terminated {
			return frame, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(frame) == 0 {
				return nil, io.EOF
			}
			return nil, ErrInvalid
		}
		if err != nil {
			return nil, err
		}
	}
}

// Object validates the complete document before decoding raw fields. All errors omit input data.
func Object(frame []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(frame) {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(frame))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, ErrInvalid
	}
	if err = members(d, 1, '}'); err != nil {
		return nil, ErrInvalid
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(frame, &result) != nil {
		return nil, ErrInvalid
	}
	return result, nil
}
func members(d *json.Decoder, depth int, end json.Delim) error {
	if depth > 64 {
		return ErrInvalid
	}
	seen := make(map[string]struct{})
	for d.More() {
		if end == '}' {
			tok, err := d.Token()
			if err != nil {
				return ErrInvalid
			}
			key, ok := tok.(string)
			if !ok {
				return ErrInvalid
			}
			if _, ok = seen[key]; ok {
				return ErrInvalid
			}
			seen[key] = struct{}{}
		}
		tok, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		if delimiter, ok := tok.(json.Delim); ok {
			switch delimiter {
			case '{':
				err = members(d, depth+1, '}')
			case '[':
				err = members(d, depth+1, ']')
			default:
				return ErrInvalid
			}
			if err != nil {
				return err
			}
		}
	}
	tok, err := d.Token()
	if err != nil || tok != end {
		return ErrInvalid
	}
	return nil
}
