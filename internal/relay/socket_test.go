package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/toolregistry"
)

func TestRelayMetadataNamesTheClientToolWithoutChangingWireIdentity(t *testing.T) {
	for _, description := range []string{"", "Independent tool information.", strings.Repeat("x", 8192)} {
		name := strings.Repeat("a", 64)
		raw, _ := json.Marshal(map[string]any{"name": name, "description": description, "input_schema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]string{"type": "integer"}}}})
		registry, err := toolregistry.Build(t.Context(), []json.RawMessage{raw}, nil, integerFixture{})
		if err != nil {
			t.Fatal(err)
		}
		broker, err := NewBroker(registry, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		defer broker.Close()
		socket, err := Listen(broker, SocketConfig{BaseDirectory: "/private/tmp"})
		if err != nil {
			t.Fatal(err)
		}
		defer socket.Close()
		config, err := LoadChildConfig(socket.ConfigPath())
		if err != nil || len(config.Tools) != 1 {
			t.Fatal("cannot read complete bounded relay metadata")
		}
		original, exposed := registry.Tools()[0], config.Tools[0]
		if exposed.Name != original.Alias || exposed.Name == original.Name || !strings.Contains(exposed.Description, `Client tool name: "`+name+`"`) || !strings.Contains(exposed.Description, "client permissions and hooks") || !strings.HasSuffix(exposed.Description, description) || string(exposed.InputSchema) != string(original.Schema) {
			t.Fatal("relay metadata lost client-name correspondence, schema or original description")
		}
		if original.Description != description || len(exposed.Description) > 8192+256 {
			t.Fatal("client declaration was mutated or wire description became unbounded")
		}
	}
}

func TestControlFramingBoundsAndExactEnvelope(t *testing.T) {
	var stream bytes.Buffer
	if err := WriteFrame(&stream, map[string]any{"synthetic": "한 줄\n다음"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFrame(&stream); err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{[]byte(`[]`), []byte(`{"x":1,"x":2}`), []byte(`{"x":`)} {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.BigEndian, uint32(len(payload)))
		b.Write(payload)
		if _, err := ReadFrame(&b); err == nil {
			t.Fatal("malformed control frame accepted")
		}
	}
	var tooLarge bytes.Buffer
	_ = binary.Write(&tooLarge, binary.BigEndian, uint32(MaxFrameBytes+1))
	if _, err := ReadFrame(&tooLarge); err == nil {
		t.Fatal("oversize frame allocated or accepted")
	}
	var partial bytes.Buffer
	_ = binary.Write(&partial, binary.BigEndian, uint32(4))
	partial.WriteString("{}")
	if _, err := ReadFrame(&partial); err == nil {
		t.Fatal("truncated frame accepted")
	}
}
func TestPrivateSocketRoundTripAndCleanup(t *testing.T) {
	b, c, alias := fixtureBroker(t, nil)
	s, err := Listen(b, SocketConfig{BaseDirectory: "/private/tmp", ReadTimeout: 100 * time.Millisecond, WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{s.Directory(), 0700}, {s.Path(), 0600}, {s.ConfigPath(), 0600}} {
		info, err := os.Lstat(item.path)
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatal("relay artifact not owner-only")
		}
	}
	config, err := LoadChildConfig(s.ConfigPath())
	if err != nil || config.Owner != c.Owner || len(config.Tools) != 1 || config.Tools[0].Name != alias {
		t.Fatal("child config does not expose only aliases")
	}
	call := Call{Version: 1, Owner: c.Owner, Secret: c.Secret, ID: "socket-call", Alias: alias, Arguments: json.RawMessage(`{"n":1}`)}
	bad := call
	bad.Secret = "wrong"
	if _, err := Exchange(context.Background(), config, bad); err == nil {
		t.Fatal("wrong socket secret accepted")
	}
	done := make(chan resultOutcome, 1)
	go func() { v, err := Exchange(context.Background(), config, call); done <- resultOutcome{v, err} }()
	waitQueued(t, b, 1)
	batch, _ := b.Seal()
	_ = b.Delivered(batch.Number)
	if err := b.Resolve(c.Owner, []Result{{ID: batch.Calls[0].ID, ToolResult: ToolResult{Content: []Content{{Type: "text", Text: "socket synthetic"}}}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if out.err != nil || out.value.Content[0].Text != "socket synthetic" {
			t.Fatal("control reply mismatch")
		}
	case <-time.After(time.Second):
		t.Fatal("control wait leaked")
	}
	// Slow clients cannot retain an unbounded number of accepted connections or delay shutdown.
	conn, err := net.Dial("unix", s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	one := make([]byte, 1)
	if _, err := conn.Read(one); err != io.EOF {
		t.Fatalf("slow header was not closed: %v", err)
	}
	dir := s.Directory()
	var closes sync.WaitGroup
	for range 8 {
		closes.Go(func() {
			if s.Close() != nil {
				t.Error("relay cleanup failed")
			}
		})
	}
	closes.Wait()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("private relay directory retained")
	}
}
func TestChildConfigRejectsSymlinksAndPublicFiles(t *testing.T) {
	b, _, _ := fixtureBroker(t, nil)
	s, err := Listen(b, SocketConfig{BaseDirectory: "/private/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	link := filepath.Join(s.Directory(), "link.json")
	if err := os.Symlink(s.ConfigPath(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChildConfig(link); err == nil {
		t.Fatal("symlink config accepted")
	}
	if err := os.Chmod(s.ConfigPath(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChildConfig(s.ConfigPath()); err == nil {
		t.Fatal("public config accepted")
	}
}

func FuzzControlFrames(f *testing.F) {
	for _, payload := range []string{`{}`, `{"version":1,"owner":"synthetic"}`, `{"x":1,"x":2}`, `[]`} {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.BigEndian, uint32(len(payload)))
		b.WriteString(payload)
		f.Add(b.Bytes())
	}
	f.Add([]byte{255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			return
		}
		value, err := ReadFrame(bytes.NewReader(raw))
		if err == nil {
			var object map[string]json.RawMessage
			if json.Unmarshal(value, &object) != nil || object == nil || len(value) > MaxFrameBytes {
				t.Fatal("control framing accepted invalid object")
			}
		}
	})
}
