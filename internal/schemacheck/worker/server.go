// Package worker contains only schema processing, with no schema resource retrieval or tool effects.
package worker

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"runtime/debug"
	"strconv"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/internal/schemawire"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type denyLoader struct{}

func (denyLoader) Load(string) (any, error) {
	return nil, errors.New("schema resource retrieval is disabled")
}

type cached struct {
	key    [32]byte
	schema *jsonschema.Schema
}
type compiler struct{ cache []cached }

// compileCacheEntries covers a client tool set with its MCP additions so repeated checks of the
// same schemas do not recompile on every request; entries are bounded by the schema byte limit.
const compileCacheEntries = 64

func (c *compiler) compile(raw []byte) (*jsonschema.Schema, error) {
	document, err := schemawire.Schema(raw)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(raw)
	for i, entry := range c.cache {
		if entry.key == key {
			copy(c.cache[1:i+1], c.cache[:i])
			c.cache[0] = entry
			return entry.schema, nil
		}
	}
	engine := jsonschema.NewCompiler()
	engine.DefaultDraft(jsonschema.Draft2020)
	engine.UseLoader(denyLoader{})
	const location = "https://dax-kiro-proxy.invalid/submitted-schema"
	if err := engine.AddResource(location, document); err != nil {
		return nil, err
	}
	schema, err := engine.Compile(location)
	if err != nil {
		return nil, err
	}
	c.cache = append([]cached{{key, schema}}, c.cache...)
	if len(c.cache) > compileCacheEntries {
		c.cache[compileCacheEntries] = cached{}
		c.cache = c.cache[:compileCacheEntries]
	}
	return schema, nil
}

// Run uses the already-tested ACP process transport's version-1 handshake solely as an internal
// process/RPC carrier. Its schema/* methods are product-internal and are never sent to Kiro.
func Run(input io.Reader, output io.Writer) error {
	runtime.GOMAXPROCS(1)
	debug.SetMemoryLimit(64 << 20)
	debug.SetMaxStack(4 << 20)
	debug.SetMaxThreads(16)
	reader := ndjson.NewReader(input, 4<<20)
	initialized := false
	schemas := &compiler{}
	for {
		raw, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		fields, err := ndjson.Object(raw)
		if err != nil {
			return err
		}
		var version, method string
		if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" || json.Unmarshal(fields["method"], &method) != nil {
			return errors.New("invalid schema worker envelope")
		}
		id, hasID := fields["id"]
		if !hasID {
			continue
		}
		number, err := strconv.ParseUint(string(id), 10, 64)
		if err != nil || number < 1 || number > 1<<53-1 {
			return errors.New("invalid schema worker request ID")
		}
		response := map[string]any{"jsonrpc": "2.0", "id": id}
		failure := func(code int) { response["error"] = map[string]any{"code": code, "message": "Schema request rejected"} }
		if !initialized {
			params, err := ndjson.Object(fields["params"])
			if method != "initialize" || err != nil || string(params["protocolVersion"]) != "1" {
				failure(-32602)
			} else {
				initialized = true
				response["result"] = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
			}
		} else if method != "schema/check" && method != "schema/validate" {
			failure(-32601)
		} else {
			params, err := ndjson.Object(fields["params"])
			if err != nil {
				failure(-32602)
			} else {
				schema, err := schemas.compile(params["schema"])
				if err != nil {
					failure(schemawire.SchemaCode)
				} else if method == "schema/check" {
					response["result"] = map[string]any{"valid": true}
				} else {
					arguments, err := schemawire.Arguments(params["arguments"])
					if err != nil || schema.Validate(arguments) != nil {
						failure(schemawire.ArgumentCode)
					} else {
						response["result"] = map[string]any{"valid": true}
					}
				}
			}
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if _, err := output.Write(append(encoded, '\n')); err != nil {
			return err
		}
	}
}
