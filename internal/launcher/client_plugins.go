package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
)

// The pinned client consumes this read-only source during full session startup. Its mutable
// registry/cache and plugin enable decisions remain inside the private client configuration.
func clientPluginSeed(home string) (string, error) {
	base := filepath.Join(home, ".claude")
	seed := filepath.Join(base, "plugins")
	for _, path := range []string{base, seed} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil || !safeClientAssetDirectory(info) {
			return "", ErrSettings
		}
	}
	// Seed paths are a platform-separated list; a source name containing the separator would
	// introduce additional roots. Do not reinterpret such a name as configuration.
	if strings.ContainsRune(seed, os.PathListSeparator) {
		return "", ErrSettings
	}
	return seed, nil
}

// Seed discovery alone does not make the pinned client's first skill/hook lookup see installed
// registrations. Preserve the two native JSON records in its private mutable root; plugin content
// remains at the read-only seed. Values and scopes stay opaque and are interpreted by the client.
func clientPluginRegistrations(seed string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	if seed == "" {
		return result, nil
	}
	for _, name := range []string{"installed_plugins.json", "known_marketplaces.json"} {
		path := filepath.Join(seed, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, ErrSettings
		}
		data, err := readSettings(path)
		if err != nil {
			return nil, err
		}
		if _, err := ndjson.Object(data); err != nil {
			return nil, ErrSettings
		}
		result[name] = data
	}
	return result, nil
}
