package main

import "testing"

func TestFixturePluginResultToken(t *testing.T) {
	const token = "ABCDEFGHIJKLM"
	for _, answer := range []string{"NOPQRSTUVWXYZ" + token, "VERIFIED " + token} {
		if got := fixturePluginResultToken(answer); got != token {
			t.Fatal("fixture discarded result-token bytes")
		}
	}
}
