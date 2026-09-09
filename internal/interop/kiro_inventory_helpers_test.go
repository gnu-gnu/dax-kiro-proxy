package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/kiroauth"
	"dax-kiro-proxy/internal/ndjson"
)

var (
	errInventoryBinding = errors.New("inventory notification has no matching owned session")
	errInventoryShape   = errors.New("invalid inventory protocol shape")
	errInventoryLimit   = errors.New("inventory observation limit exceeded")
)

// Live diagnostics select only bounded field names, kinds, counts and Boolean observations.
// Name presence is not permission status or evidence that a native tool was denied. The in-memory
// session identifier is excluded from live diagnostics. This report never enables production policy.
type inventoryReport struct {
	skills            *skillInventoryProbe
	contextFiles      map[string]string
	contextFile       string
	contextDescriptor json.RawMessage
	contextQueried    bool
	effortDescriptor  json.RawMessage
	usageDescriptor   json.RawMessage
	sessionModels     json.RawMessage
	ContextAvailable  bool
	ContextFields     map[string]string
	ContextMetaShape  map[string]string
	// Kept only in memory for a separately opted-in prompt experiment, never in live diagnostics.
	session                                                            string
	SessionCreated, Advertised, ToolsAvailable, QuerySent, Success     bool
	Commands, Notifications, NotificationBytes, ResultBytes, TextBytes int
	UnknownResultFields                                                int
	NativeNames                                                        map[string]bool
	ResultKinds                                                        map[string]string
	NotificationKinds                                                  map[string]int
	DataKinds                                                          map[string]string
	DataSizes                                                          map[string]int
	ToolEntryKinds                                                     map[string]string
	ListedNativeNames                                                  map[string]bool
	AliasMatched                                                       bool
	AliasNameForm                                                      string
	MCPParamKinds                                                      map[string]string
	MCPDeclaredNameMatches                                             int
	MCPMatches                                                         map[string]int
	ToolMatches                                                        map[string]bool
	ModelCatalog                                                       inventoryCatalogReport
}

type contextShapeReport struct {
	RelativeAgentMatched         bool
	AbsoluteNames, RelativeNames int
	OwnedMatches                 map[string]bool
	Items, MatchedItems          int
	ContextTokens                float64
	OwnedFileMatched             bool
	QuerySent, Success           bool
	Verbose                      bool
	Bytes                        int
	Shape                        map[string]string
}

// This is a single read-only wire experiment, not a generic private-command dispatcher.
func readOnlyContextShow(ctx context.Context, client *acp.Client, inventory *inventoryReport) (contextShapeReport, error) {
	report := contextShapeReport{Shape: map[string]string{}, OwnedMatches: map[string]bool{}}
	if inventory == nil || !inventory.ContextAvailable || inventory.contextQueried || inventory.session == "" {
		return report, errInventoryShape
	}
	fields, err := ndjson.Object(inventory.contextDescriptor)
	if err != nil {
		return report, errInventoryShape
	}
	meta, err := ndjson.Object(fields["meta"])
	var commands []string
	if err != nil || json.Unmarshal(meta["subcommands"], &commands) != nil || len(commands) > 32 {
		return report, errInventoryShape
	}
	show := false
	for _, command := range commands {
		show = show || command == "show"
	}
	if !show {
		return report, errInventoryShape
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	inventory.contextQueried = true
	report.QuerySent = true
	raw, err := client.Call(ctx, "_kiro.dev/commands/execute", map[string]any{"sessionId": inventory.session, "command": map[string]any{"command": "context", "args": map[string]any{"subcommand": "show", "verbose": true}}})
	if err != nil {
		return report, err
	}
	report.Bytes = len(raw)
	if len(raw) > 64<<10 {
		return report, errInventoryLimit
	}
	fields, err = ndjson.Object(raw)
	if err != nil || len(fields) > 32 {
		return report, errInventoryShape
	}
	if string(fields["success"]) != "true" && string(fields["success"]) != "false" {
		return report, errInventoryShape
	}
	report.Success = string(fields["success"]) == "true"
	if data, err := ndjson.Object(fields["data"]); err == nil {
		report.Verbose = string(data["verbose"]) == "true"
		breakdown, err := ndjson.Object(data["breakdown"])
		if err != nil {
			return report, errInventoryShape
		}
		files, err := ndjson.Object(breakdown["contextFiles"])
		if err != nil || inventoryKind(files["tokens"]) != "number" || json.Unmarshal(files["tokens"], &report.ContextTokens) != nil || report.ContextTokens < 0 || report.ContextTokens > 1e9 {
			return report, errInventoryShape
		}
		var items []json.RawMessage
		if len(files["items"]) > 0 && (inventoryKind(files["items"]) != "array" || json.Unmarshal(files["items"], &items) != nil) || len(items) > 128 {
			return report, errInventoryShape
		}
		report.Items = len(items)
		for _, raw := range items {
			item, err := ndjson.Object(raw)
			name, valid := inventoryString(item["name"], 4096)
			if err != nil || !valid || string(item["matched"]) != "true" && string(item["matched"]) != "false" {
				return report, errInventoryShape
			}
			if string(item["matched"]) == "true" {
				report.MatchedItems++
				path := filepath.Clean(strings.TrimPrefix(name, "file://"))
				if filepath.IsAbs(path) {
					report.AbsoluteNames++
				} else {
					report.RelativeNames++
					report.RelativeAgentMatched = report.RelativeAgentMatched || path == "AGENTS.md"
				}
				if inventory.contextFile != "" && path == inventory.contextFile {
					report.OwnedFileMatched = true
				}
				for label, expected := range inventory.contextFiles {
					if path == expected {
						report.OwnedMatches[label] = true
					}
				}
			}
		}
	} else {
		return report, errInventoryShape
	}
	if err := observeContextMeta(raw, "result", 0, report.Shape); err != nil {
		return report, err
	}
	for {
		n, available, err := client.TryNext()
		if err != nil {
			return report, err
		}
		if !available {
			break
		}
		if err := inventory.observe(n, inventory.session); err != nil {
			return report, err
		}
	}
	return report, nil
}

type inventoryPrerequisite struct {
	Skills        *skillInventoryProbe
	WaitForMCP    bool
	Alias         string
	ObservedTools map[string]string
	WaitForAllMCP bool
	SettleWindow  time.Duration
	Catalog       *catalog.Catalog
}

// The only dispatched private command is the advertised argument-free tools inventory. There is
// deliberately no generic command or prompt parameter. The caller owns and joins process cleanup.
func readOnlyToolsInventory(ctx context.Context, client *acp.Client, cwd string, advertisementWait time.Duration) (inventoryReport, error) {
	return readOnlyToolsInventoryAfter(ctx, client, cwd, advertisementWait, inventoryPrerequisite{})
}

func readOnlyToolsInventoryAfter(ctx context.Context, client *acp.Client, cwd string, advertisementWait time.Duration, required inventoryPrerequisite) (inventoryReport, error) {
	report := inventoryReport{NativeNames: map[string]bool{}, ResultKinds: map[string]string{}, NotificationKinds: map[string]int{}, DataKinds: map[string]string{}, DataSizes: map[string]int{}, ToolEntryKinds: map[string]string{}, ListedNativeNames: map[string]bool{}, MCPParamKinds: map[string]string{}}
	report.MCPMatches, report.ToolMatches = map[string]int{}, map[string]bool{}
	report.skills = required.Skills
	if len(required.ObservedTools) > 8 || required.WaitForAllMCP && len(required.ObservedTools) == 0 || required.SettleWindow < 0 || required.SettleWindow > time.Second {
		return report, errInventoryShape
	}
	seenAliases := map[string]bool{}
	for server, alias := range required.ObservedTools {
		if !inventoryMember(server) || !inventoryMember(alias) || seenAliases[alias] {
			return report, errInventoryShape
		}
		seenAliases[alias] = true
		report.MCPMatches[server], report.ToolMatches[server] = 0, false
	}
	if !filepath.IsAbs(cwd) || advertisementWait <= 0 || advertisementWait > 5*time.Second || len(required.Alias) > 256 || strings.IndexFunc(required.Alias, func(c rune) bool { return !inventoryNameCharacter(c) }) != -1 {
		return report, errInventoryShape
	}
	if required.Catalog != nil && len(required.Catalog.Models()) == 0 {
		return report, errInventoryShape
	}
	raw, err := client.Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	if err != nil {
		return report, err
	}
	if len(raw) > 1<<20 {
		return report, errInventoryLimit
	}
	fields, err := ndjson.Object(raw)
	session, valid := inventoryString(fields["sessionId"], 1024)
	if err != nil || !valid {
		return report, errInventoryShape
	}
	report.SessionCreated = true
	report.session = session
	report.sessionModels = append(json.RawMessage(nil), fields["models"]...)
	if required.Catalog != nil {
		report.ModelCatalog, err = compareInventoryCatalog(raw, required.Catalog)
		if err != nil {
			return report, err
		}
	}
	adCtx, cancel := context.WithTimeout(ctx, advertisementWait)
	defer cancel()
	for !report.Advertised || required.WaitForMCP && report.MCPDeclaredNameMatches == 0 || required.WaitForAllMCP && !report.allMCPSeen() {
		if err := adCtx.Err(); err != nil {
			return report, err
		}
		n, err := client.Next(adCtx)
		if err != nil {
			return report, err
		}
		if err := report.observe(n, session); err != nil {
			return report, err
		}
		if report.Advertised && !report.ToolsAvailable {
			return report, nil
		}
	}
	if !report.ToolsAvailable {
		return report, nil
	}
	report.QuerySent = true
	raw, err = client.Call(ctx, "_kiro.dev/commands/execute", map[string]any{
		"sessionId": session, "command": map[string]any{"command": "tools", "args": map[string]any{}},
	})
	if err != nil {
		return report, err
	}
	report.ResultBytes = len(raw)
	if len(raw) > 64<<10 {
		return report, errInventoryLimit
	}
	fields, err = ndjson.Object(raw)
	if err != nil || len(fields) > 32 {
		return report, errInventoryShape
	}
	for key, value := range fields {
		switch key {
		case "success", "output", "message", "content", "data", "error":
			report.ResultKinds[key] = inventoryKind(value)
		default:
			report.UnknownResultFields++
		}
		if key == "output" || key == "message" {
			var text string
			if inventoryKind(value) == "string" && json.Unmarshal(value, &text) == nil {
				report.observeText(text)
			}
		}
	}
	if inventoryKind(fields["data"]) == "object" {
		data, err := ndjson.Object(fields["data"])
		if err != nil || len(data) > 32 {
			return report, errInventoryShape
		}
		for key, value := range data {
			if !inventoryMember(key) {
				return report, errInventoryShape
			}
			// Only schema member names/kinds and container sizes, never nested values or prose.
			report.DataKinds[key] = inventoryKind(value)
			switch inventoryKind(value) {
			case "array":
				var entries []json.RawMessage
				if json.Unmarshal(value, &entries) != nil {
					return report, errInventoryShape
				}
				report.DataSizes[key] = len(entries)
				if key == "tools" {
					if len(entries) > 128 {
						return report, errInventoryLimit
					}
					for _, entry := range entries {
						tool, err := ndjson.Object(entry)
						if err != nil || len(tool) > 32 {
							return report, errInventoryShape
						}
						for field, value := range tool {
							if !inventoryMember(field) {
								return report, errInventoryShape
							}
							report.ToolEntryKinds[field] = inventoryKind(value)
						}
						name, ok := inventoryString(tool["name"], 256)
						if ok && nativeInventoryName(name) {
							report.ListedNativeNames[name] = true
						}
						if ok {
							for server, alias := range required.ObservedTools {
								if name == alias || name == "@"+server+"/"+alias {
									report.ToolMatches[server] = true
								}
							}
						}
						if ok && required.Alias != "" {
							switch name {
							case required.Alias:
								report.AliasMatched, report.AliasNameForm = true, "bare"
							case "@dax_session/" + required.Alias:
								report.AliasMatched, report.AliasNameForm = true, "qualified"
							}
						}
					}
				}
			case "object":
				entries, err := ndjson.Object(value)
				if err != nil {
					return report, errInventoryShape
				}
				report.DataSizes[key] = len(entries)
			}
		}
	}
	switch string(bytes.TrimSpace(fields["success"])) {
	case "true":
		report.Success = true
	case "false":
	default:
		return report, errInventoryShape
	}
	// The transport reader enqueues preceding notifications before completing the response. This
	// drain makes no claim about optional notifications the peer sends after its response.
	for {
		n, ok, err := client.TryNext()
		if err != nil {
			return report, err
		}
		if !ok {
			break
		}
		if err := report.observe(n, session); err != nil {
			return report, err
		}
	}
	if required.SettleWindow > 0 {
		settle, stop := context.WithTimeout(ctx, required.SettleWindow)
		defer stop()
		for {
			n, err := client.Next(settle)
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				break
			}
			if err != nil {
				return report, err
			}
			if err := report.observe(n, session); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

func (r *inventoryReport) allMCPSeen() bool {
	for _, count := range r.MCPMatches {
		if count == 0 {
			return false
		}
	}
	return true
}

func (r *inventoryReport) observe(n acp.Notification, session string) error {
	if r.Notifications >= 64 || len(n.Params) > 64<<10 || len(n.Params) > (1<<20)-r.NotificationBytes {
		return errInventoryLimit
	}
	r.Notifications++
	r.NotificationBytes += len(n.Params)
	switch n.Method {
	case "_kiro.dev/commands/available", "session/update", "_kiro.dev/metadata", "_kiro.dev/mcp/server_initialized":
		r.NotificationKinds[n.Method]++
	default:
		r.NotificationKinds["other"]++
		return nil
	}
	fields, err := ndjson.Object(n.Params)
	if err != nil {
		return errInventoryShape
	}
	owner, ok := inventoryString(fields["sessionId"], 1024)
	if !ok || owner != session {
		return errInventoryBinding
	}
	if r.skills != nil {
		if err := r.skills.observe(n.Method, fields); err != nil {
			return err
		}
	}
	switch n.Method {
	case "_kiro.dev/mcp/server_initialized":
		if len(fields) > 32 {
			return errInventoryShape
		}
		server, ok := inventoryString(fields["serverName"], 256)
		if !ok {
			return errInventoryShape
		}
		for field, raw := range fields {
			if !inventoryMember(field) {
				return errInventoryShape
			}
			r.MCPParamKinds[field] = inventoryKind(raw)
		}
		if server == "dax_session" {
			r.MCPDeclaredNameMatches++
		}
		if _, tracked := r.MCPMatches[server]; tracked {
			r.MCPMatches[server]++
		}
	case "_kiro.dev/commands/available":
		var commands []json.RawMessage
		if inventoryKind(fields["commands"]) != "array" || json.Unmarshal(fields["commands"], &commands) != nil || len(commands) > 128 {
			return errInventoryShape
		}
		seen := make(map[string]bool, len(commands))
		for _, raw := range commands {
			item, err := ndjson.Object(raw)
			name, ok := inventoryString(item["name"], 256)
			if err != nil || !ok {
				return errInventoryShape
			}
			name = strings.TrimPrefix(name, "/")
			if name == "" || seen[name] {
				return errInventoryShape
			}
			seen[name] = true
			if name == "effort" {
				r.effortDescriptor = append(json.RawMessage(nil), raw...)
			}
			if name == "usage" {
				r.usageDescriptor = append(json.RawMessage(nil), raw...)
			}
			if name == "context" {
				r.ContextAvailable = true
				r.contextDescriptor = append(json.RawMessage(nil), raw...)
				r.ContextFields = map[string]string{}
				if len(item) > 32 {
					return errInventoryLimit
				}
				for key, value := range item {
					if !inventoryMember(key) {
						return errInventoryShape
					}
					r.ContextFields[key] = inventoryKind(value)
				}
				r.ContextMetaShape = map[string]string{}
				if err := observeContextMeta(item["meta"], "meta", 0, r.ContextMetaShape); err != nil {
					return err
				}
			}
		}
		r.Commands = len(commands)
		r.ToolsAvailable = seen["tools"]
		r.Advertised = true
	case "session/update":
		update, err := ndjson.Object(fields["update"])
		if err != nil {
			return errInventoryShape
		}
		kind, ok := inventoryString(update["sessionUpdate"], 128)
		if !ok {
			return errInventoryShape
		}
		if kind == "agent_message_chunk" {
			content, err := ndjson.Object(update["content"])
			if err != nil {
				return errInventoryShape
			}
			var text string
			if string(content["type"]) == `"text"` && inventoryKind(content["text"]) == "string" && json.Unmarshal(content["text"], &text) == nil {
				r.observeText(text)
			}
		}
	}
	return nil
}

// Retain schema member paths and kinds, not descriptions, defaults, or arbitrary string values.
func observeContextMeta(raw json.RawMessage, path string, depth int, shape map[string]string) error {
	if len(shape) >= 128 || len(path) > 256 {
		return errInventoryLimit
	}
	kind := inventoryKind(raw)
	shape[path] = kind
	if depth >= 6 {
		return nil
	}
	if kind == "object" {
		fields, err := ndjson.Object(raw)
		if err != nil || len(fields) > 32 {
			return errInventoryShape
		}
		for key, value := range fields {
			if key == "description" || key == "default" || key == "examples" {
				continue
			}
			if !inventoryMember(key) {
				return errInventoryShape
			}
			if err := observeContextMeta(value, path+"."+key, depth+1, shape); err != nil {
				return err
			}
		}
	}
	if kind == "array" {
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil || len(values) > 32 {
			return errInventoryShape
		}
		for _, value := range values {
			if inventoryKind(value) == "object" {
				if err := observeContextMeta(value, path+"[]", depth+1, shape); err != nil {
					return err
				}
			} else if string(value) == `"show"` {
				shape[path+".show"] = "enum"
			}
		}
	}
	return nil
}

func (r *inventoryReport) observeText(value string) {
	r.TextBytes += len(value)
	// Fixed public tool identifiers only; neither arbitrary inventory prose nor paths survive.
	for token := range strings.FieldsFuncSeq(value, func(c rune) bool { return !inventoryNameCharacter(c) }) {
		if nativeInventoryName(token) {
			r.NativeNames[token] = true
		}
	}
}

func nativeInventoryName(value string) bool {
	switch value {
	case "fs_read", "fs_write", "execute_bash", "grep", "glob", "code", "read", "write", "shell", "web_search", "web_fetch", "use_aws", "subagent":
		return true
	}
	return false
}

func inventoryMember(key string) bool {
	return len(key) > 0 && len(key) <= 64 && strings.IndexFunc(key, func(c rune) bool { return !inventoryNameCharacter(c) }) == -1
}

func inventoryNameCharacter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func inventoryString(raw json.RawMessage, limit int) (string, bool) {
	var value string
	if inventoryKind(raw) != "string" || json.Unmarshal(raw, &value) != nil || len(value) == 0 || len(value) > limit {
		return "", false
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return "", false
		}
	}
	return value, true
}

func inventoryKind(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "missing"
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

// The public transport offers only sanitized RemoteError codes. This test-only classifier preserves
// authentication classification while retaining fixed parse markers, never upstream error prose.
type inventoryErrorObserver struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (o *inventoryErrorObserver) Error(code int, message string, data json.RawMessage) bool {
	o.mu.Lock()
	if o.seen == nil {
		o.seen = make(map[string]bool)
	}
	if code == -32700 || code == -32602 {
		if len(message) > 64<<10 || len(data) > 64<<10 {
			o.seen["diagnostic limit"] = true
			o.mu.Unlock()
			return (kiroauth.Classifier{}).Error(code, message, data)
		}
		matches := func(marker string) bool {
			return strings.Contains(message, marker) || strings.Contains(string(data), marker)
		}
		for _, key := range []string{"sessionId", "command", "name", "arguments", "argument", "args", "input", "content", "prompt", "id", "kind", "type"} {
			marker := "missing field `" + key + "`"
			if matches(marker) {
				o.seen[marker] = true
			}
		}
		for _, marker := range []string{"invalid type", "unknown variant", "expected a sequence", "expected a string", "expected a map", "expected struct", "missing field", "parse"} {
			if matches(marker) {
				o.seen[marker] = true
			}
		}
	}
	o.mu.Unlock()
	return (kiroauth.Classifier{}).Error(code, message, data)
}

func (o *inventoryErrorObserver) Stderr(line []byte) bool {
	return (kiroauth.Classifier{}).Stderr(line)
}

func (o *inventoryErrorObserver) markers() map[string]bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make(map[string]bool, len(o.seen))
	for k, v := range o.seen {
		result[k] = v
	}
	return result
}
