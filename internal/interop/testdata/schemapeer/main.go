// Independent public MCP peer. The sole tool has no effects and accepts one fixed tuple.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 3 || !filepath.IsAbs(os.Args[1]) || !filepath.IsAbs(os.Args[2]) {
		os.Exit(70)
	}
	timer := time.AfterFunc(20*time.Second, func() { os.Exit(71) })
	defer timer.Stop()
	source, err := os.OpenFile(os.Args[1], os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		os.Exit(72)
	}
	schema, err := io.ReadAll(io.LimitReader(source, (64<<10)+1))
	source.Close()
	var obj map[string]any
	if err != nil || len(schema) > 64<<10 || json.Unmarshal(schema, &obj) != nil || obj["type"] != "object" {
		os.Exit(72)
	}
	witness, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		os.Exit(73)
	}
	defer witness.Close()
	mark := func(event string) bool {
		_, err := fmt.Fprintf(witness, "%d %s\n", os.Getpid(), event)
		return err == nil
	}
	if !mark("started") || !serve(os.Stdin, os.Stdout, schema, mark) {
		os.Exit(74)
	}
}

func serve(input io.Reader, output io.Writer, schema json.RawMessage, mark func(string) bool) bool {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	handshake, ready, listed, called := false, false, false, false
	for n := 0; n < 64; n++ {
		if !scanner.Scan() {
			return scanner.Err() == nil
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.JSONRPC != "2.0" {
			return false
		}
		if len(request.ID) == 0 {
			if request.Method == "notifications/initialized" {
				if !handshake || ready || !mark("initialized") {
					return false
				}
				ready = true
			}
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			if handshake {
				return false
			}
			handshake = true
			response["result"] = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "independent-schema-peer", "version": "1"}}
		case "ping":
			response["result"] = map[string]any{}
		case "tools/list":
			if !ready || listed || !mark("listed") {
				return false
			}
			listed = true
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": "schema_probe", "description": "Independent effect-free schema observation.", "inputSchema": schema}}}
		case "tools/call":
			var call struct {
				Name      string
				Arguments map[string]json.RawMessage
			}
			var tuple []int
			if !listed || called || json.Unmarshal(request.Params, &call) != nil || call.Name != "schema_probe" || len(call.Arguments) != 1 || json.Unmarshal(call.Arguments["tuple"], &tuple) != nil || len(tuple) != 1 || tuple[0] != 7 || !mark("called") {
				return false
			}
			called = true
			response["result"] = map[string]any{"isError": false, "content": []any{map[string]string{"type": "text", "text": "independent schema tool complete"}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "unsupported fixture method"}
		}
		if json.NewEncoder(output).Encode(response) != nil {
			return false
		}
	}
	return false
}
