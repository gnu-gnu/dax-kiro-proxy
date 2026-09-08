package acp

import (
	"strings"
	"testing"
)

func TestStderrRedactionAndTruncation(t *testing.T) {
	for _, line := range []string{"Authorization: Bearer secret-fixture-12345", "refresh_token=secret-fixture-12345", `{"api_key":"secret-fixture-12345"}`, "sk-ant-secret-fixture-12345", "eyJsecret-fixture-12345.abc.def"} {
		if strings.Contains(retainableStderr([]byte(line), false), "secret-fixture") {
			t.Error("credential survived redaction")
		}
	}
	if strings.Contains(retainableStderr([]byte("unmarked-secret-suffix"), true), "secret") {
		t.Error("truncated credential fragment retained")
	}
}
