package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
