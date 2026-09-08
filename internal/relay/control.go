package relay

import (
	"encoding/binary"
	"encoding/json"
	"io"

	"dax-kiro-proxy/internal/ndjson"
)

// The private control channel uses a four-byte big-endian payload length. MCP stdio remains NDJSON.
func ReadFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 2 || size > MaxFrameBytes {
		return nil, ErrCall
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	if _, err := ndjson.Object(payload); err != nil {
		return nil, ErrCall
	}
	return payload, nil
}
func WriteFrame(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > MaxFrameBytes {
		return ErrCall
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	for _, part := range [][]byte{header[:], payload} {
		for len(part) > 0 {
			n, err := writer.Write(part)
			if err != nil {
				return err
			}
			if n <= 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}
func decodeCall(raw []byte) (Call, error) {
	fields, err := ndjson.Object(raw)
	if err != nil || len(fields) != 6 {
		return Call{}, ErrCall
	}
	var call Call
	if string(fields["version"]) != "1" || !controlString(fields["owner"], &call.Owner) || !controlString(fields["secret"], &call.Secret) || !controlString(fields["callId"], &call.ID) || !controlString(fields["alias"], &call.Alias) {
		return Call{}, ErrCall
	}
	call.Version = 1
	call.Arguments = fields["arguments"]
	return call, nil
}
func controlString(raw []byte, out *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, out) == nil
}
