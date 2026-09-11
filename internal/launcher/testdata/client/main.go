// This independent client fixture speaks HTTP only to its explicitly supplied loopback gateway.
// It returns synthetic tool results and never executes a file, shell, hook or other client tool.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("2.1.267 (Claude Code)")
		return
	}
	if len(os.Args) != 5 || os.Args[1] != "--settings" || os.Args[3] != "--model" {
		os.Exit(40)
	}
	endpoint, token := os.Getenv("ANTHROPIC_BASE_URL"), os.Getenv("ANTHROPIC_API_KEY")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback() || len(token) != 43 || os.Getenv("ANTHROPIC_AUTH_TOKEN") != token {
		os.Exit(41)
	}
	if os.Getenv("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST") != "1" || os.Getenv("CLAUDE_CODE_USE_BEDROCK") != "" || os.Getenv("AWS_ACCESS_KEY_ID") != "" {
		os.Exit(42)
	}
	profile := os.Getenv("CLAUDE_CONFIG_DIR")
	for _, path := range []string{os.Args[2], filepath.Join(profile, "settings.json")} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			os.Exit(43)
		}
	}
	mode := os.Getenv("TERM")
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	if mode == "catalog-text" {
		req, err := http.NewRequest(http.MethodGet, endpoint+"/v1/models?limit=1000", nil)
		if err != nil {
			os.Exit(52)
		}
		req.Header.Set("x-api-key", token)
		response, err := client.Do(req)
		if err != nil {
			os.Exit(53)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		var list struct{ Data []struct{ ID string } }
		if readErr != nil || response.StatusCode != 200 || json.Unmarshal(body, &list) != nil {
			os.Exit(54)
		}
		found := false
		for _, entry := range list.Data {
			found = found || entry.ID == os.Args[4]
		}
		if !found {
			os.Exit(55)
		}
	}
	r := map[string]any{"model": os.Args[4], "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": "independent launcher request"}}}
	if strings.HasPrefix(mode, "tools-") {
		r["tools"] = []any{map[string]any{"name": "client_action", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]string{"type": "integer"}}, "required": []string{"n"}}}}
	}
	post := func() map[string]json.RawMessage {
		body, _ := json.Marshal(r)
		req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			os.Exit(44)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(req)
		if err != nil {
			os.Exit(45)
		}
		defer response.Body.Close()
		body, err = io.ReadAll(io.LimitReader(response.Body, 1<<20))
		var fields map[string]json.RawMessage
		if err != nil || response.StatusCode != 200 || json.Unmarshal(body, &fields) != nil {
			os.Exit(46)
		}
		return fields
	}
	response := post()
	if strings.HasPrefix(mode, "tools-") {
		if string(response["stop_reason"]) != `"tool_use"` {
			os.Exit(47)
		}
		if mode == "tools-complete" {
			var blocks []map[string]json.RawMessage
			if json.Unmarshal(response["content"], &blocks) != nil {
				os.Exit(48)
			}
			var results []any
			for _, block := range blocks {
				if string(block["type"]) != `"tool_use"` {
					continue
				}
				var id, name string
				if json.Unmarshal(block["id"], &id) != nil || json.Unmarshal(block["name"], &name) != nil || name != "client_action" {
					os.Exit(49)
				}
				results = append(results, map[string]any{"type": "tool_result", "tool_use_id": id, "content": "independent controlled result"})
			}
			if len(results) != 1 {
				os.Exit(50)
			}
			r["messages"] = append(r["messages"].([]any), map[string]any{"role": "assistant", "content": json.RawMessage(response["content"])}, map[string]any{"role": "user", "content": results})
			response = post()
		}
	}
	if !strings.HasPrefix(mode, "tools-") || mode == "tools-complete" {
		if string(response["stop_reason"]) != `"end_turn"` {
			os.Exit(51)
		}
	}
	// Only owned paths, local address and lifecycle state are observed. Never print the token,
	// request body, response text or tool arguments/results.
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"clientPID": os.Getpid(), "endpoint": endpoint, "runtime": filepath.Dir(profile), "state": mode})
	if strings.HasSuffix(mode, "hold") {
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
	}
}
