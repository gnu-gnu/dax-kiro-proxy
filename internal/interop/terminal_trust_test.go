package interop_test

import (
	"strings"
	"testing"
)

// The owned trust control may navigate only from an observed option. In particular, moving down
// from the last row need not wrap to the first row on another client build.
func terminalTrustChoiceKey(screen string) string {
	yes, no, selected := -1, -1, -1
	for i, line := range strings.Split(screen, "\n") {
		row := strings.ToLower(strings.Join(strings.Fields(line), " "))
		marked := strings.HasPrefix(row, "❯")
		if marked {
			if selected >= 0 {
				return ""
			}
			selected = i
			row = strings.TrimSpace(strings.TrimPrefix(row, "❯"))
		}
		switch row {
		case "yes, i trust this folder":
			if yes >= 0 {
				return ""
			}
			yes = i
		case "no, exit":
			if no >= 0 {
				return ""
			}
			no = i
		default:
			if marked {
				return ""
			}
		}
	}
	if yes < 0 || no < 0 || selected < 0 {
		return ""
	}
	if selected == yes {
		return "\r"
	}
	if yes < no {
		return "\x1b[A"
	}
	return "\x1b[B"
}

func TestTrustChoiceRequiresObservedRowsAndSelection(t *testing.T) {
	for _, tc := range []struct{ name, screen, key string }{
		{"last-no", "Yes, I trust this folder\n❯ No, exit", "\x1b[A"},
		{"first-no", "❯ No, exit\nYes, I trust this folder", "\x1b[B"},
		{"selected-yes", "❯ Yes, I trust this folder\nNo, exit", "\r"},
		{"padded-yes", "  ❯   Yes, I trust this folder  \n  No, exit  ", "\r"},
		{"absent-selection", "Yes, I trust this folder\nNo, exit", ""},
		{"absent-no", "❯ Yes, I trust this folder", ""},
		{"absent-yes", "❯ No, exit", ""},
		{"partial-row", "❯\nYes, I trust this folder\nNo, exit", ""},
		{"two-selections", "❯ Yes, I trust this folder\n❯ No, exit", ""},
		{"duplicate-yes", "❯ Yes, I trust this folder\nYes, I trust this folder\nNo, exit", ""},
		{"duplicate-no", "Yes, I trust this folder\n❯ No, exit\nNo, exit", ""},
		{"unknown-choice", "Yes, I trust this folder\nNo, exit\n❯ Unknown", ""},
		{"quoted-label", "❯ Quoted Yes, I trust this folder\nNo, exit", ""},
		{"unmeasured-suffix", "❯ Yes, I trust this folder temporarily\nNo, exit", ""},
		{"unmeasured-numbering", "❯ 1. Yes, I trust this folder\n2. No, exit", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if terminalTrustChoiceKey(tc.screen) != tc.key {
				t.Fatal("unproven trust selection or navigation")
			}
		})
	}
}
