package launcher

import (
	"errors"
	"os"
	"path/filepath"

	"dax-kiro-proxy/internal/ndjson"
)

// This is a conservative admission check for an optional default, not a settings resolver.
// The client resolves scopes. Any existing status key or uncertainty suppresses our entire
// object so its refreshInterval cannot merge into a different command. Ancestors cover a
// repository-local setting when starting below its root; linked worktrees are left to the client.
func clientStatusDefaultAllowed(project string) bool {
	remaining := MaxSettingsBytes
	for depth, dir := 0, filepath.Clean(project); depth < 64; depth, dir = depth+1, filepath.Dir(dir) {
		base := filepath.Join(dir, ".claude")
		info, err := os.Lstat(base)
		if err == nil {
			if !safeClientAssetDirectory(info) {
				return false
			}
			for _, name := range []string{"settings.json", "settings.local.json"} {
				data, err := readSettings(filepath.Join(base, name))
				if err != nil || len(data) > remaining {
					return false
				}
				remaining -= len(data)
				fields, err := ndjson.Object(data)
				if err != nil {
					return false
				}
				if _, exists := fields["statusLine"]; exists {
					return false
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		metadata, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			// Do not open git metadata, invoke Git, or follow a worktree to another checkout.
			return safeClientAssetDirectory(metadata)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if filepath.Dir(dir) == dir {
			return true
		}
	}
	return false
}
