package status

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLaunchNoticeDoesNotInventNegotiatedCapabilities(t *testing.T) {
	const model = "claude-dax-independent-0123456789abcdef"
	notice, err := LaunchNotice(model)
	if err != nil || notice.Model != model || notice.Version != 1 || notice.ImageInput != "unknown" || notice.PDFInput != "unknown" || notice.Effort != "unknown" || notice.NativeWebSearch != "unsupported" || notice.ClientTools != "client_permissions" || notice.ProviderTokenUsage != "unreported" {
		t.Fatal("startup notice asserted unchecked model support or billing")
	}
	raw, _ := json.Marshal(notice)
	line, err := FormatNotice(raw, model)
	if err != nil || !strings.HasPrefix(line, "Kiro launch independent.") || !strings.Contains(line, "client permissions") || !strings.Contains(line, "unverified") || !strings.Contains(line, "unreported") || strings.Contains(line, "0123456789abcdef") || len(line) > 768 || strings.ContainsAny(line, "\r\n\x1b") {
		t.Fatal("startup notice did not display bounded, truthful feature states")
	}
	for _, malformed := range [][]byte{
		[]byte(`null`),
		[]byte(strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1)),
		[]byte(strings.Replace(string(raw), `"image_input":"unknown"`, `"image_input":"supported"`, 1)),
		[]byte(strings.Replace(string(raw), `"image_input":"unknown"`, `"IMAGE_INPUT":"unknown"`, 1)),
		[]byte(strings.Replace(string(raw), `"effort":"unknown"`, `"effort":null`, 1)),
		[]byte(strings.Replace(string(raw), `"unreported"`, `"private-diagnostic-sentinel"`, 1)),
		[]byte(strings.Replace(string(raw), model, "claude-dax-other", 1)),
		append(raw[:len(raw)-1:len(raw)-1], []byte(`,"diagnostic":"private-sentinel"}`)...),
		[]byte(strings.Repeat(" ", (64<<10)+1)),
	} {
		if out, err := FormatNotice(malformed, model); err == nil || out != "" {
			t.Fatal("unvalidated model state or diagnostic reached startup display")
		}
	}
}
