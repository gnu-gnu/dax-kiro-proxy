// Package catalog validates backend model identities and reversible client aliases.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/ndjson"
)

var ErrCatalog = errors.New("invalid or unavailable Kiro model catalog")
var ErrModel = errors.New("model is absent from the current Kiro catalog")

const MaxBytes = 1 << 20
const MaxModels = 256

type Backend struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Multiplier  *float64 `json:"multiplier,omitempty"`
}
type Catalog struct {
	models    []Backend
	clientIDs []string
	reverse   map[string]int
	backend   map[string]int
	current   string
}

func New(models []Backend, current string) (*Catalog, error) {
	return newWithID(models, current, clientID)
}

func newWithID(models []Backend, current string, derive func(string) string) (*Catalog, error) {
	if len(models) > MaxModels || current != "" && !validID(current) {
		return nil, ErrCatalog
	}
	c := &Catalog{current: current, reverse: make(map[string]int), backend: make(map[string]int)}
	for _, model := range models {
		if !validID(model.ID) || !displayText(model.Name, 256) || !displayText(model.Description, 8192) {
			return nil, ErrCatalog
		}
		if _, ok := c.backend[model.ID]; ok {
			return nil, ErrCatalog
		}
		if model.Multiplier != nil {
			value := *model.Multiplier
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1000 {
				return nil, ErrCatalog
			}
			model.Multiplier = &value
		}
		c.backend[model.ID] = len(c.models)
		c.models = append(c.models, model)
	}
	if current != "" {
		if _, ok := c.backend[current]; !ok {
			c.backend[current] = len(c.models)
			c.models = append(c.models, Backend{ID: current, Name: current})
		}
	}
	if len(c.models) == 0 || len(c.models) > MaxModels {
		return nil, ErrCatalog
	}
	for i, model := range c.models {
		id := derive(model.ID)
		if _, ok := c.reverse[id]; ok {
			return nil, ErrCatalog
		}
		c.reverse[id] = i
		c.clientIDs = append(c.clientIDs, id)
	}
	encoded, _ := json.Marshal(c.models)
	if len(encoded) > MaxBytes {
		return nil, ErrCatalog
	}
	return c, nil
}
func clientID(backend string) string {
	var normalized strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(backend) {
		if normalized.Len() >= 48 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			normalized.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			normalized.WriteByte('-')
			lastDash = true
		}
	}
	name := strings.Trim(normalized.String(), "-")
	if name == "" {
		name = "model"
	}
	digest := sha256.Sum256([]byte(backend))
	return "claude-dax-" + name + "-" + hex.EncodeToString(digest[:8])
}
func validID(s string) bool {
	if s == "" || len(s) > 256 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func displayText(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return false
		}
	}
	return true
}
func (c *Catalog) Current() string { return c.current }
func (c *Catalog) Models() []Backend {
	out := make([]Backend, len(c.models))
	for i, m := range c.models {
		out[i] = copyBackend(m)
	}
	return out
}
func (c *Catalog) Resolve(id string) (Backend, error) {
	index, ok := c.reverse[id]
	if !ok {
		return Backend{}, ErrModel
	}
	return copyBackend(c.models[index]), nil
}
func (c *Catalog) ClientID(backend string) (string, error) {
	index, ok := c.backend[backend]
	if !ok {
		return "", ErrModel
	}
	return c.clientIDs[index], nil
}
func (c *Catalog) Backend(id string) (Backend, error) {
	index, ok := c.backend[id]
	if !ok {
		return Backend{}, ErrModel
	}
	return copyBackend(c.models[index]), nil
}
func copyBackend(m Backend) Backend {
	if m.Multiplier != nil {
		value := *m.Multiplier
		m.Multiplier = &value
	}
	return m
}
func (c *Catalog) List() []inference.Model {
	out := make([]inference.Model, 0, len(c.models))
	for i, m := range c.models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		name += " (Kiro)"
		if m.Multiplier != nil {
			name += fmt.Sprintf(" · %g×", *m.Multiplier)
		}
		description := "Kiro model"
		if m.Description != "" {
			description = "Kiro: " + m.Description
		}
		out = append(out, inference.Model{ID: c.clientIDs[i], Name: name, Description: description, Object: "model", OwnedBy: "kiro"})
	}
	return out
}

type Session struct {
	ID       string
	Catalog  *Catalog
	ConfigID string
}

func DecodeSession(raw []byte) (Session, error) {
	fields, err := ndjson.Object(raw)
	var session Session
	if err != nil || !stringValue(fields["sessionId"], &session.ID) || session.ID == "" || len(session.ID) > 1024 {
		return Session{}, ErrCatalog
	}
	session.Catalog, session.ConfigID, err = DecodeModels(fields)
	return session, err
}

// DecodeModels supports the repository's legacy Kiro model state and the advertised public
// select option. No mode, permission or other agent configuration is changed by this adapter.
func DecodeModels(fields map[string]json.RawMessage) (*Catalog, string, error) {
	if options, ok := fields["configOptions"]; ok {
		if len(options) > MaxBytes {
			return nil, "", ErrCatalog
		}
		var configs []json.RawMessage
		if len(options) == 0 || options[0] != '[' || json.Unmarshal(options, &configs) != nil || len(configs) > 64 {
			return nil, "", ErrCatalog
		}
		var chosen *Catalog
		var selector string
		for _, raw := range configs {
			obj, err := ndjson.Object(raw)
			if err != nil {
				return nil, "", ErrCatalog
			}
			var category string
			if value, ok := obj["category"]; ok && !stringValue(value, &category) {
				return nil, "", ErrCatalog
			}
			if category != "model" {
				continue
			}
			var id, typ, current string
			if chosen != nil || !stringValue(obj["id"], &id) || !validID(id) || !stringValue(obj["type"], &typ) || typ != "select" || !stringValue(obj["currentValue"], &current) {
				return nil, "", ErrCatalog
			}
			models, err := decodeList(obj["options"], "value")
			if err != nil {
				return nil, "", err
			}
			chosen, err = New(models, current)
			if err != nil {
				return nil, "", err
			}
			selector = id
		}
		if chosen != nil {
			return chosen, selector, nil
		}
	}
	raw := fields["models"]
	if len(raw) > MaxBytes {
		return nil, "", ErrCatalog
	}
	state, err := ndjson.Object(raw)
	if err != nil {
		return nil, "", ErrCatalog
	}
	var current string
	if raw, ok := state["currentModelId"]; ok && !stringValue(raw, &current) {
		return nil, "", ErrCatalog
	}
	var models []Backend
	if list, ok := state["availableModels"]; ok {
		models, err = decodeList(list, "modelId")
		if err != nil {
			return nil, "", err
		}
	}
	c, err := New(models, current)
	return c, "", err
}
func decodeList(raw json.RawMessage, key string) ([]Backend, error) {
	var entries []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &entries) != nil || len(entries) > MaxModels {
		return nil, ErrCatalog
	}
	models := make([]Backend, 0, len(entries))
	for _, entry := range entries {
		obj, err := ndjson.Object(entry)
		if err != nil {
			return nil, ErrCatalog
		}
		var m Backend
		if !stringValue(obj[key], &m.ID) {
			return nil, ErrCatalog
		}
		if v, ok := obj["name"]; ok && !stringValue(v, &m.Name) {
			return nil, ErrCatalog
		}
		if v, ok := obj["description"]; ok && !stringValue(v, &m.Description) {
			return nil, ErrCatalog
		}
		models = append(models, m)
	}
	return models, nil
}
func stringValue(raw json.RawMessage, value *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, value) == nil
}
