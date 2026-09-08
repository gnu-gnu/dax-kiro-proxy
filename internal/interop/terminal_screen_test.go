package interop_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/width"
)

// Reconstruct only the owned 160x40 terminal's text cells. Searching stripped output misses
// words assembled through cursor edits. This bounded observer is not a general terminal emulator;
// colors, links and terminal modes do not contribute text. Raw output remains in bounded memory.
func statusTerminalScreen(raw []byte) string {
	const rows, cols = 40, 160
	var cells [rows][cols]rune
	x, y, savedX, savedY := 0, 0, 0, 0
	clamp := func(n, ceiling int) int { return max(0, min(n, ceiling)) }
	scroll := func() {
		copy(cells[:], cells[1:])
		clear(cells[rows-1][:])
		y = rows - 1
	}
	for i := 0; i < len(raw); {
		if raw[i] == 0x1b {
			i++
			if i == len(raw) {
				break
			}
			kind := raw[i]
			i++
			if kind == ']' {
				for i < len(raw) && raw[i] != 7 && !(raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '\\') {
					i++
				}
				if i < len(raw) && raw[i] == 7 {
					i++
				} else if i+1 < len(raw) {
					i += 2
				}
				continue
			}
			if kind == '[' {
				start := i
				for i < len(raw) && (raw[i] < 0x40 || raw[i] > 0x7e) {
					i++
				}
				if i == len(raw) {
					break
				}
				final := raw[i]
				i++
				if i-start > 64 {
					continue
				}
				params := strings.Split(string(raw[start:i-1]), ";")
				value := func(index, fallback int) int {
					if index >= len(params) || params[index] == "" {
						return fallback
					}
					n, err := strconv.Atoi(params[index])
					if err != nil || n < 0 || n > 10000 {
						return fallback
					}
					return n
				}
				n := max(1, value(0, 1))
				switch final {
				case 'A':
					y = clamp(y-n, rows-1)
				case 'B':
					y = clamp(y+n, rows-1)
				case 'C':
					x = clamp(x+n, cols-1)
				case 'D':
					x = clamp(x-n, cols-1)
				case 'E':
					y, x = clamp(y+n, rows-1), 0
				case 'F':
					y, x = clamp(y-n, rows-1), 0
				case 'G':
					x = clamp(n-1, cols-1)
				case 'H', 'f':
					y, x = clamp(n-1, rows-1), clamp(max(1, value(1, 1))-1, cols-1)
				case 'J':
					switch value(0, 0) {
					case 0:
						clear(cells[y][min(x, cols):])
						for row := y + 1; row < rows; row++ {
							clear(cells[row][:])
						}
					case 1:
						for row := 0; row < y; row++ {
							clear(cells[row][:])
						}
						clear(cells[y][:min(x+1, cols)])
					case 2:
						cells = [rows][cols]rune{}
					}
				case 'K':
					switch value(0, 0) {
					case 0:
						clear(cells[y][min(x, cols):])
					case 1:
						clear(cells[y][:min(x+1, cols)])
					case 2:
						clear(cells[y][:])
					}
				case 'P':
					at := min(x, cols)
					n = min(n, cols-at)
					copy(cells[y][at:], cells[y][at+n:])
					clear(cells[y][cols-n:])
				case '@':
					at := min(x, cols)
					n = min(n, cols-at)
					copy(cells[y][at+n:], cells[y][at:cols-n])
					clear(cells[y][at : at+n])
				case 'X':
					clear(cells[y][min(x, cols):min(x+n, cols)])
				case 's':
					savedX, savedY = x, y
				case 'u':
					x, y = savedX, savedY
				}
				continue
			}
			switch kind {
			case '7':
				savedX, savedY = x, y
			case '8':
				x, y = savedX, savedY
			case 'M':
				y = max(0, y-1)
			}
			continue
		}
		ch, size := utf8.DecodeRune(raw[i:])
		i += size
		switch ch {
		case '\r':
			x = 0
		case '\n':
			y++
			if y == rows {
				scroll()
			}
		case '\b':
			x = max(0, x-1)
		case '\t':
			x = min((x/8+1)*8, cols-1)
		default:
			if ch < 32 || ch == 127 || unicode.Is(unicode.Mn, ch) || unicode.Is(unicode.Me, ch) {
				continue
			}
			advance := 1
			if k := width.LookupRune(ch).Kind(); k == width.EastAsianWide || k == width.EastAsianFullwidth {
				advance = 2
			}
			if x+advance > cols {
				x, y = 0, y+1
				if y == rows {
					scroll()
				}
			}
			cells[y][x] = ch
			if advance == 2 {
				cells[y][x+1] = ' '
			}
			x += advance
		}
	}
	var result strings.Builder
	for _, row := range cells {
		for _, ch := range row {
			if ch == 0 {
				ch = ' '
			}
			result.WriteRune(ch)
		}
		result.WriteByte('\n')
	}
	return result.String()
}

func TestStatusScreenReconstructsCursorEditsWithoutInventingText(t *testing.T) {
	for _, c := range []struct{ input, want, absent string }{
		{"Kiro lost status-fixture\r\x1b[6Ca", "Kiro last status-fixture", "Kiro lost"},
		{"Kiro status-fixture\r\x1b[5C\x1b[5@last ", "Kiro last status-fixture", "Kiro status-fixture"},
		{"Kiro last status-fixture\r\x1b[2KReplaced", "Replaced", "Kiro last"},
		{"Kiro last status-fixture\x1b[2J\x1b[HOther", "Other", "Kiro last"},
		{"\x1b]0;Kiro last status-fixture\x07Body", "Body", "Kiro last"},
		{"\x1b[31mKiro\x1b[0m last status-fixture", "Kiro last status-fixture", "31m"},
		{"한글Kiro lost status-fixture\r\x1b[10Ca", "Kiro last status-fixture", "Kiro lost"},
	} {
		out := statusTerminalScreen([]byte(c.input))
		if !strings.Contains(out, c.want) || strings.Contains(out, c.absent) || len(out) > 4*40*160+40 {
			t.Fatal("terminal observation lost a cursor edit or retained erased/control text")
		}
	}
}
