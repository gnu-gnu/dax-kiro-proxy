// Independently authored MCP peer for client configuration experiments. It performs no client tool
// effect and records only its own PID and fixed lifecycle labels in an explicitly owned directory.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if (len(os.Args) != 3 && len(os.Args) != 4) || !filepath.IsAbs(os.Args[2]) {
		os.Exit(70)
	}
	hold := len(os.Args) == 4 && os.Args[1] == "plugin" && os.Args[3] == "hold-initialize"
	if len(os.Args) == 4 && !hold {
		os.Exit(70)
	}
	label := os.Args[1]
	if label != "user" && label != "local" && label != "project" && label != "plugin" {
		os.Exit(70)
	}
	f, err := os.OpenFile(filepath.Join(os.Args[2], label), os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		os.Exit(71)
	}
	defer f.Close()
	mark := func(event string) {
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX) != nil {
			os.Exit(72)
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			os.Exit(72)
		}
		if _, err := fmt.Fprintf(f, "%d %s\n", os.Getpid(), event); err != nil {
			os.Exit(72)
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_UN) != nil {
			os.Exit(72)
		}
	}
	mark("started")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	initialized := false
	for n := 0; n < 128 && scanner.Scan(); n++ {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.JSONRPC != "2.0" {
			os.Exit(73)
		}
		if len(request.ID) == 0 {
			if request.Method == "notifications/initialized" {
				initialized = true
				mark("initialized")
			}
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			if hold {
				mark("held")
				deadline := time.Now().Add(10 * time.Second)
				for {
					if info, err := os.Lstat(filepath.Join(os.Args[2], "release-plugin")); err == nil && info.Mode().IsRegular() && info.Size() == 0 {
						break
					}
					if !time.Now().Before(deadline) {
						os.Exit(76)
					}
					time.Sleep(10 * time.Millisecond)
				}
				hold = false
			}
			response["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "independent-client-assets", "version": "1"}}
		case "ping":
			response["result"] = map[string]any{}
		case "tools/list":
			if !initialized {
				os.Exit(74)
			}
			mark("listed")
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": "owned_probe", "description": "Independent effect-free configuration probe.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}}}
		case "tools/call":
			mark("called")
			response["result"] = map[string]any{"isError": false, "content": []any{map[string]string{"type": "text", "text": "independent client asset result"}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "unsupported fixture method"}
		}
		if json.NewEncoder(os.Stdout).Encode(response) != nil {
			os.Exit(75)
		}
	}
}
