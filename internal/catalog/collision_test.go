package catalog

import "testing"

func TestDerivedAliasCollisionIsRejected(t *testing.T) {
	_, err := newWithID([]Backend{{ID: "synthetic-a"}, {ID: "synthetic-b"}}, "synthetic-a", func(string) string { return "claude-dax-synthetic-collision" })
	if err == nil {
		t.Fatal("two backend models shared an ambiguous client alias")
	}
}
