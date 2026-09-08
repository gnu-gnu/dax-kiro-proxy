package ndjson

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFramingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		limit       int
		wantErr     error
	}{
		{"exact", "{\"x\":1}\n", 7, nil},
		{"too big", "{\"x\":1}\n", 6, ErrTooLarge},
		{"missing newline", "{}", 10, ErrInvalid},
		{"empty", "", 10, io.EOF},
		{"two lines", "{}\n{}\n", 10, nil},
		{"cr whitespace", "{}\r\n", 3, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewReader(strings.NewReader(tc.input), tc.limit).Read()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v, want %v", err, tc.wantErr)
			}
		})
	}
	data := []byte(`{"text":"한글\nline"}` + "\n{}\n")
	r := NewReader(&oneByteReader{data: data}, 100)
	for range 2 {
		b, err := r.Read()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Object(b); err != nil {
			t.Fatal(err)
		}
	}
}

type oneByteReader struct{ data []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func TestStrictJSONObjects(t *testing.T) {
	for _, input := range []string{"", "null", "[]", "3", "{}{}", `{"a":1,"a":2}`, `{"x":{"b":1,"b":2}}`, "{\"x\":\"\xff\"}", `{"x":NaN}`, strings.Repeat(`{"x":`, 65) + `0` + strings.Repeat("}", 65)} {
		if _, err := Object([]byte(input)); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid object (%d bytes)", len(input))
		}
	}
	obj, err := Object([]byte(`{"id":9007199254740991,"unknown":{"safe":true}}`))
	if err != nil || string(obj["id"]) != "9007199254740991" {
		t.Fatalf("number lost: %s %v", obj["id"], err)
	}
}

func TestDefaultFrameCeiling(t *testing.T) {
	const limit = 8 << 20
	for _, extra := range []int{0, 1} {
		line := append(bytes.Repeat([]byte{' '}, limit+extra), '\n')
		b, err := NewReader(bytes.NewReader(line), limit).Read()
		if extra == 0 && (err != nil || len(b) != limit) {
			t.Fatalf("exact ceiling: %v", err)
		}
		if extra == 1 && !errors.Is(err, ErrTooLarge) {
			t.Fatalf("oversize: %v", err)
		}
	}
}

func FuzzObject(f *testing.F) {
	for _, seed := range []string{`{}`, `{"id":1}`, `{"x":{"x":0}}`, `{"x":1,"x":2}`, `[]`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			t.Skip()
		}
		_, _ = Object(b)
	})
}
