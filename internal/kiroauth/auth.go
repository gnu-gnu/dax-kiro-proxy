// Package kiroauth recognizes account failures only at Kiro's error/stderr boundary.
package kiroauth

import (
	"encoding/json"
	"regexp"
	"strings"
)

type Classifier struct{}

var unauthorized = regexp.MustCompile(`(?i)(?:^|[\s:])(?:HTTP(?:/[0-9.]+)?\s+401(?:\s|$)|401\s+Unauthorized\b)`)

func (Classifier) Stderr(line []byte) bool { return recognized(string(line)) }
func (Classifier) Error(code int, message string, data json.RawMessage) bool {
	if code == 401 || recognized(message) {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	for _, key := range []string{"statusCode", "status", "httpStatus"} {
		if string(fields[key]) == "401" {
			return true
		}
	}
	for _, key := range []string{"code", "error", "message"} {
		var value string
		if json.Unmarshal(fields[key], &value) == nil && recognized(value) {
			return true
		}
	}
	return false
}
func recognized(text string) bool {
	lower := strings.ToLower(text)
	for _, prefix := range []string{"tool returned", "tool error", "web_search:", "web_fetch:"} {
		if strings.Contains(lower, prefix) {
			return false
		}
	}
	for _, phrase := range []string{"login required", "login-required", "kiro-cli login", "token expired", "expired token", "expired-token", "invalid token", "invalid-token", "invalid_token", "invalid grant", "invalid-grant", "invalid_grant", "reauthentication required", "reauthentication needed"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return unauthorized.MatchString(text)
}
