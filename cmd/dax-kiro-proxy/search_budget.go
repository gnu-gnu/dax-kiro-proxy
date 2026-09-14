package main

import (
	"io"
	"strconv"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/websearch"
)

// A hook failure must exit 2 (the documented Kiro blocking status), including malformed input.
func searchBudget(args []string, input io.Reader) int {
	if len(args) != 2 {
		return 2
	}
	limit, err := strconv.Atoi(args[1])
	if err != nil {
		return 2
	}
	data, err := io.ReadAll(io.LimitReader(input, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return 2
	}
	if _, err := ndjson.Object(data); err != nil {
		return 2
	}
	if !websearch.TakeBudget(args[0], limit) {
		return 2
	}
	return 0
}
