package main

import (
	"os"
	"strings"
	"testing"
)

func TestSearchHookAlwaysDeniesInvalidOrExhaustedBudget(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture")
	}
	for _, args := range [][]string{{}, {dir, "-1"}, {dir, "9"}, {dir, "null"}} {
		if searchBudget(args, strings.NewReader(`{}`)) != 2 {
			t.Fatal("invalid hook did not block")
		}
	}
	if searchBudget([]string{dir, "1"}, strings.NewReader("invalid")) != 2 {
		t.Fatal("invalid hook context admitted")
	}
	if searchBudget([]string{dir, "1"}, strings.NewReader(`{"tool_name":"web_search"}`)) != 0 {
		t.Fatal("first search denied")
	}
	if searchBudget([]string{dir, "1"}, strings.NewReader(`{"tool_name":"web_search"}`)) != 2 {
		t.Fatal("excess search admitted")
	}
}
