package launcher

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func validNativeSessionID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(decoded) == 16
}

// The native client owns transcript formats, memory, retention and writes under projects.
// This opt-in reference neither copies that tree nor puts its contents in proxy session records.
// Validate directory identities during preparation; it is not an immutable snapshot or a lock
// against subsequent changes by the same account. Cleanup removes only the private link.
func referenceClientHistory(home, profile string) error {
	info, err := os.Lstat(home)
	if err != nil || !safeClientAssetDirectory(info) {
		return ErrSettings
	}
	owner, err := os.OpenRoot(home)
	if err != nil {
		return ErrSettings
	}
	defer owner.Close()
	opened, err := owner.Stat(".")
	if err != nil || !safeClientAssetDirectory(opened) || !os.SameFile(info, opened) {
		return ErrSettings
	}
	ensure := func(parent *os.Root, name string) (*os.Root, error) {
		if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrSettings
		}
		return openClientAssetDirectory(parent, name)
	}
	base, err := ensure(owner, ".claude")
	if err != nil {
		return err
	}
	defer base.Close()
	data, err := ensure(base, "projects")
	if err != nil {
		return err
	}
	defer data.Close()
	target := filepath.Join(home, ".claude", "projects")
	if err := os.Symlink(target, filepath.Join(profile, "projects")); err != nil {
		return ErrRuntime
	}
	// Do not publish a link to a directory replaced while the reference was being prepared.
	for _, pair := range []struct {
		path string
		root *os.Root
	}{{home, owner}, {filepath.Join(home, ".claude"), base}, {target, data}} {
		current, err := os.Lstat(pair.path)
		held, heldErr := pair.root.Stat(".")
		if err != nil || heldErr != nil || !safeClientAssetDirectory(current) || !safeClientAssetDirectory(held) || !os.SameFile(current, held) {
			return ErrSettings
		}
	}
	return nil
}
