package launcher

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

const (
	MaxClientAssetBytes   = 2 << 20
	MaxClientAssetsBytes  = 32 << 20
	MaxClientAssetEntries = 1024
	MaxClientAssetDepth   = 16
)

type clientAsset struct {
	path                  string
	reference             string
	data                  []byte
	directory, executable bool
}

// Tool assets and output styles are private snapshots. Rules keep their original import bases and exclusion
// paths through a native reference; this is not an immutable source snapshot or a tool sandbox.
// Personal CLAUDE.md has no verified adapter. Interpret neither frontmatter nor commands.
func clientCustomizations(home string) ([]clientAsset, error) {
	owner, err := os.OpenRoot(home)
	if err != nil {
		return nil, ErrSettings
	}
	defer owner.Close()
	if _, err := owner.Lstat(".claude"); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	base, err := openClientAssetDirectory(owner, ".claude")
	if err != nil {
		return nil, err
	}
	defer base.Close()
	var assets []clientAsset
	total, entries := 0, 0
	var walk func(*os.Root, string, string, int, bool) error
	walk = func(parent *os.Root, name, relative string, depth int, retain bool) error {
		entries++
		if entries > MaxClientAssetEntries || depth > MaxClientAssetDepth || len(relative) > 4096 {
			return ErrSettings
		}
		info, err := parent.Lstat(name)
		if err != nil {
			return ErrSettings
		}
		if info.IsDir() {
			dir, err := openClientAssetDirectory(parent, name)
			if err != nil {
				return err
			}
			defer dir.Close()
			file, err := dir.Open(".")
			if err != nil {
				return ErrSettings
			}
			defer file.Close()
			if retain {
				assets = append(assets, clientAsset{path: relative, directory: true})
			}
			for {
				names, err := file.Readdirnames(64)
				if err != nil && !errors.Is(err, io.EOF) {
					return ErrSettings
				}
				for _, child := range names {
					if err := walk(dir, child, filepath.Join(relative, child), depth+1, retain); err != nil {
						return err
					}
				}
				if errors.Is(err, io.EOF) {
					return nil
				}
			}
		}
		if !safeSettings(info) || info.Size() > MaxClientAssetBytes || info.Size() > int64(MaxClientAssetsBytes-total) {
			return ErrSettings
		}
		file, err := parent.OpenFile(name, settingsReadFlags, 0)
		if err != nil {
			return ErrSettings
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !safeSettings(opened) || !os.SameFile(info, opened) || opened.Size() != info.Size() || !opened.ModTime().Equal(info.ModTime()) {
			return ErrSettings
		}
		data, err := io.ReadAll(io.LimitReader(file, MaxClientAssetBytes+1))
		if err != nil || len(data) > MaxClientAssetBytes || len(data) > MaxClientAssetsBytes-total || int64(len(data)) != opened.Size() {
			return ErrSettings
		}
		after, err := file.Stat()
		if err != nil || !safeSettings(after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || after.Mode() != opened.Mode() {
			return ErrSettings
		}
		total += len(data)
		if retain {
			assets = append(assets, clientAsset{path: relative, data: data, executable: opened.Mode().Perm()&0100 != 0})
		}
		return nil
	}
	for _, name := range []string{"skills", "commands", "agents", "output-styles", "rules"} {
		info, err := base.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !safeClientAssetDirectory(info) {
			return nil, ErrSettings
		}
		reference := name == "rules"
		if err := walk(base, name, name, 1, !reference); err != nil {
			return nil, err
		}
		if reference {
			assets = append(assets, clientAsset{path: name, reference: filepath.Join(home, ".claude", name)})
		}
	}
	return assets, nil
}

func openClientAssetDirectory(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil || !safeClientAssetDirectory(info) {
		return nil, ErrSettings
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, ErrSettings
	}
	opened, err := root.Stat(".")
	if err != nil || !safeClientAssetDirectory(opened) || !os.SameFile(info, opened) {
		root.Close()
		return nil, ErrSettings
	}
	return root, nil
}

func writeClientCustomizations(profile string, assets []clientAsset) error {
	for _, asset := range assets {
		path := filepath.Join(profile, asset.path)
		if asset.reference != "" {
			if os.Symlink(asset.reference, path) != nil {
				return ErrRuntime
			}
			continue
		}
		if asset.directory {
			if os.Mkdir(path, 0700) != nil {
				return ErrRuntime
			}
			continue
		}
		mode := os.FileMode(0600)
		if asset.executable {
			mode = 0700
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return ErrRuntime
		}
		n, err := file.Write(asset.data)
		closeErr := file.Close()
		if err != nil || closeErr != nil || n != len(asset.data) {
			return ErrRuntime
		}
	}
	return nil
}
