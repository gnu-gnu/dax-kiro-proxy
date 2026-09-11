package launcher

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"dax-kiro-proxy/internal/ndjson"
)

// The client records the user's answer to its workspace-trust dialog in the private profile,
// which is discarded at exit, so a project opened only through the proxy asked again on every
// launch. Native Claude Code records the same answer in HOME/.claude.json and never asks again.
// D118 writes that one answer back as the client itself would have: exactly
// projects[<the client's own key>].hasTrustDialogAccepted = true, spliced into otherwise unchanged
// bytes, only when the user answered yes in this launch, the source lacks the value, and the source
// is byte-identical to what this launch read. Any doubt skips the write and the dialog repeats.
// This is the only write the launcher makes to a source client file (reviewed exception to D64).
const trustKey = "hasTrustDialogAccepted"

type trustSource struct {
	exists bool
	digest [32]byte
}

// acceptedProjectTrust reports the client's own project key when the private profile records an
// accepted trust dialog for the launch project.
func acceptedProjectTrust(private []byte, project string) (string, bool) {
	fields, err := ndjson.Object(private)
	if err != nil || len(fields["projects"]) == 0 {
		return "", false
	}
	projects, err := ndjson.Object(fields["projects"])
	if err != nil {
		return "", false
	}
	for key, raw := range projects {
		entry, err := ndjson.Object(raw)
		if err != nil || string(entry[trustKey]) != "true" || !identityPath(key) {
			continue
		}
		if key == project {
			return key, true
		}
		resolvedKey, keyErr := filepath.EvalSymlinks(key)
		resolvedProject, projectErr := filepath.EvalSymlinks(project)
		if keyErr == nil && projectErr == nil && resolvedKey == resolvedProject {
			return key, true
		}
	}
	return "", false
}

// insertProjectTrust splices the trust key for one project into the raw document. Every other
// byte is preserved; a false value is replaced by true; an existing true value changes nothing.
func insertProjectTrust(raw []byte, key string) ([]byte, bool, error) {
	if len(raw) > MaxSettingsBytes {
		return nil, false, ErrSettings
	}
	if _, err := ndjson.Object(raw); err != nil {
		return nil, false, ErrSettings
	}
	encodedKey, err := json.Marshal(key)
	if err != nil {
		return nil, false, ErrSettings
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	open := func() (int64, error) {
		tok, err := dec.Token()
		if d, ok := tok.(json.Delim); err != nil || !ok || d != '{' {
			return 0, ErrSettings
		}
		return dec.InputOffset(), nil
	}
	name := func() (string, error) {
		tok, err := dec.Token()
		s, ok := tok.(string)
		if err != nil || !ok {
			return "", ErrSettings
		}
		return s, nil
	}
	splice := func(at int64, text string, nonEmpty bool) []byte {
		if nonEmpty {
			text += ","
		}
		out := make([]byte, 0, len(raw)+len(text))
		out = append(out, raw[:at]...)
		out = append(out, text...)
		return append(out, raw[at:]...)
	}
	const flag = `"` + trustKey + `":true`
	topAfter, err := open()
	if err != nil {
		return nil, false, err
	}
	topNonEmpty := dec.More()
	for dec.More() {
		field, err := name()
		if err != nil {
			return nil, false, err
		}
		if field != "projects" {
			if err := skipJSONValue(dec); err != nil {
				return nil, false, err
			}
			continue
		}
		projectsAfter, err := open()
		if err != nil {
			return nil, false, err
		}
		projectsNonEmpty := dec.More()
		for dec.More() {
			entryName, err := name()
			if err != nil {
				return nil, false, err
			}
			if entryName != key {
				if err := skipJSONValue(dec); err != nil {
					return nil, false, err
				}
				continue
			}
			entryAfter, err := open()
			if err != nil {
				return nil, false, err
			}
			entryNonEmpty := dec.More()
			for dec.More() {
				valueName, err := name()
				if err != nil {
					return nil, false, err
				}
				if valueName != trustKey {
					if err := skipJSONValue(dec); err != nil {
						return nil, false, err
					}
					continue
				}
				tok, err := dec.Token()
				value, ok := tok.(bool)
				if err != nil || !ok {
					return nil, false, ErrSettings
				}
				if value {
					return raw, false, nil
				}
				end := dec.InputOffset()
				start := end - int64(len("false"))
				if start < 0 || string(raw[start:end]) != "false" {
					return nil, false, ErrSettings
				}
				out := append(append(append(make([]byte, 0, len(raw)), raw[:start]...), "true"...), raw[end:]...)
				return out, true, nil
			}
			return splice(entryAfter, flag, entryNonEmpty), true, nil
		}
		return splice(projectsAfter, string(encodedKey)+`:{`+flag+`}`, projectsNonEmpty), true, nil
	}
	return splice(topAfter, `"projects":{`+string(encodedKey)+`:{`+flag+`}}`, topNonEmpty), true, nil
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return ErrSettings
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if d != '{' && d != '[' {
		return ErrSettings
	}
	for depth := 1; depth > 0; {
		tok, err := dec.Token()
		if err != nil {
			return ErrSettings
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// persistProjectTrust writes the accepted trust answer of this launch back to the source global
// file. It reports whether a write happened; every skip condition is silent by design.
func (p *ClientProfile) persistProjectTrust() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || !p.source.exists || p.home == "" || p.project == "" {
		return false, nil
	}
	private, err := readSettings(filepath.Join(p.path, "client", ".claude.json"))
	if err != nil {
		return false, nil
	}
	key, accepted := acceptedProjectTrust(private, p.project)
	if !accepted {
		return false, nil
	}
	sourcePath := filepath.Join(p.home, ".claude.json")
	source, err := readSettings(sourcePath)
	if err != nil || sha256.Sum256(source) != p.source.digest {
		return false, nil
	}
	out, changed, err := insertProjectTrust(source, key)
	if err != nil || !changed {
		return false, err
	}
	// The next launch must accept the written file, and it must carry exactly the one new value.
	if _, err := clientMCPProjection(out); err != nil {
		return false, ErrSettings
	}
	if written, ok := acceptedProjectTrust(out, p.project); !ok || written != key {
		return false, ErrSettings
	}
	return true, replaceSettingsFile(p.home, ".claude.json", out)
}

// replaceSettingsFile writes data beside the source with the source's permission bits and renames
// it into place; a link or a changed file identity aborts the replacement.
func replaceSettingsFile(dir, name string, data []byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ErrSettings
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil || !safeSettings(info) {
		return ErrSettings
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return ErrSettings
	}
	temp := name + ".dax-" + hex.EncodeToString(suffix[:])
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return ErrSettings
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || root.Rename(temp, name) != nil {
		_ = root.Remove(temp)
		return ErrSettings
	}
	return nil
}
