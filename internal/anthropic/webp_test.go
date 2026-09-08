package anthropic

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

// This tiny constant-color fixture is encoded from format facts, without imported images or code.
// Sources: https://developers.google.com/speed/webp/docs/webp_lossless_bitstream_specification
// (sections 3, 4, 5.2.3, 6.2.1-3) and https://developers.google.com/speed/webp/docs/riff_container.
func solidWebP(c color.NRGBA, width, height int) []byte {
	var stream []byte
	position := 0
	bits := func(value uint32, count int) {
		for i := range count {
			if position%8 == 0 {
				stream = append(stream, 0)
			}
			stream[position/8] |= byte(value>>i&1) << uint(position%8)
			position++
		}
	}
	bits(uint32(width-1), 14)
	bits(uint32(height-1), 14)
	alpha := uint32(0)
	if c.A != 255 {
		alpha = 1
	}
	bits(alpha, 1)
	bits(0, 3) // Format version.
	bits(0, 3) // No transform, color cache or spatially varying prefix groups.
	for _, channel := range []byte{c.G, c.R, c.B, c.A, 0} {
		bits(1, 1) // Simple prefix alphabet.
		bits(0, 1) // One symbol needs no per-pixel bits.
		bits(1, 1)
		bits(uint32(channel), 8)
	}
	payload := append([]byte{0x2f}, stream...)
	chunk := binary.LittleEndian.AppendUint32([]byte("VP8L"), uint32(len(payload)))
	chunk = append(chunk, payload...)
	if len(payload)%2 != 0 {
		chunk = append(chunk, 0)
	}
	file := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(4+len(chunk)))
	file = append(file, []byte("WEBP")...)
	return append(file, chunk...)
}

func TestIndependentWebPFixtureDecodesPixelsAndPassesPromptValidation(t *testing.T) {
	for _, chosen := range []color.NRGBA{{47, 121, 203, 255}, {208, 65, 33, 128}} {
		encoded := solidWebP(chosen, 2, 3)
		decoded, format, err := image.Decode(bytes.NewReader(encoded))
		if err != nil || format != "webp" || decoded.Bounds() != image.Rect(0, 0, 2, 3) {
			t.Fatalf("independent WebP fixture failed full decoding: %v", err)
		}
		for y := range 3 {
			for x := range 2 {
				if color.NRGBAModel.Convert(decoded.At(x, y)) != chosen {
					t.Fatal("fixture color was not decoded losslessly")
				}
			}
		}
		source := base64.StdEncoding.EncodeToString(encoded)
		r, err := DecodeRequest(mediaBody(t, []any{imageBlock("image/webp", source)}))
		if err != nil {
			t.Fatal("valid independently generated WebP rejected")
		}
		media, ok := r.Messages[0].Content[0].Media()
		if !ok || media.Width != 2 || media.Height != 3 || media.Data != source {
			t.Fatal("WebP prompt metadata or content changed")
		}
	}
}
