package kiroauth

import "testing"

func TestAccountExpiryBoundary(t *testing.T) {
	c := Classifier{}
	for _, s := range []string{"HTTP 401 Unauthorized", "login required", "token expired", "invalid_token", "invalid_grant", "reauthentication required", "Run kiro-cli login"} {
		if !c.Stderr([]byte(s)) {
			t.Errorf("did not recognize %q", s)
		}
	}
	for _, s := range []string{"request took 401 ms", "socket closed", "unexpected EOF", "a token bucket is empty", "tool returned HTTP 401", "web_search: HTTP 401 Unauthorized"} {
		if c.Stderr([]byte(s)) {
			t.Errorf("misclassified %q", s)
		}
	}
	if !c.Error(401, "", nil) || !c.Error(-32000, "", []byte(`{"statusCode":401}`)) || !c.Error(-32000, "token expired", nil) {
		t.Fatal("structured auth not recognized")
	}
	if c.Error(-32603, "internal failure", []byte(`{"duration":401}`)) {
		t.Fatal("unrelated numeric data classified as auth")
	}
}
