package installation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Install publishes a private generation of source in binPath. Force only replaces
// a fully validated managed installation. It never replaces a foreign entry point.
func Install(ctx context.Context, binPath, source string, force bool) error {
	return install(ctx, binPath, source, force, nil)
}

// Checkpoints are per-call test injection, not a command or environment override.
type checkpoint func(string) error

func visit(check checkpoint, phase string) error {
	if check != nil {
		return check(phase)
	}
	return nil
}

func install(ctx context.Context, binPath, source string, force bool, check checkpoint) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := readSource(ctx, source)
	if err != nil {
		return err
	}
	s, err := openStore(ctx, binPath, true, false)
	if err != nil {
		return err
	}
	defer s.Close()
	state, err := inspect(ctx, s)
	if err != nil {
		if s.created {
			if cleanupErr := removeManager(s); cleanupErr != nil {
				err = errors.Join(err, ErrCleanup, cleanupErr)
			}
		}
		return err
	}
	if state.public && !force {
		return ErrExists
	}
	if err := ctx.Err(); err != nil {
		if s.created {
			if cleanupErr := removeManager(s); cleanupErr != nil {
				return errors.Join(err, ErrCleanup, cleanupErr)
			}
		}
		return err
	}
	// Only validated inactive generations are recoverable. Incomplete or changed
	// directories were rejected by inspect and are never removed by their names.
	if err := reclaim(s, state); err != nil {
		return errors.Join(ErrCleanup, err)
	}
	var staged *generation
	published := false
	defer func() {
		if err == nil {
			return
		}
		if published {
			err = errors.Join(ErrPublished, err)
			return
		}
		if staged != nil {
			if cleanupErr := removeGeneration(s, staged); cleanupErr != nil {
				err = errors.Join(err, ErrCleanup, cleanupErr)
				return
			}
		}
		if s.created && state.current == "" {
			if cleanupErr := removeManager(s); cleanupErr != nil {
				err = errors.Join(err, ErrCleanup, cleanupErr)
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return ErrIO
	}
	identity := "generation-" + hex.EncodeToString(id[:])
	files, record, err := bundle(data, identity)
	if err != nil {
		return err
	}
	staged, err = stage(ctx, s, "staging-"+hex.EncodeToString(id[:]), files, record, check)
	if err != nil {
		return err
	}
	if err := visit(check, "prepared"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.check(); err != nil {
		return err
	}
	if s.root.Rename(staged.name, identity) != nil {
		return ErrIO
	}
	staged.name = identity
	if syncDirectory(s.root) != nil {
		return ErrIO
	}
	if err := visit(check, "before-publication"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := samePublication(s, state); err != nil {
		return err
	}
	if s.root.Symlink(identity, ".next") != nil {
		return ErrIO
	}
	if s.root.Rename(".next", "current") != nil {
		if removePointer(s.root, ".next", identity) != nil {
			return errors.Join(ErrIO, ErrCleanup)
		}
		return ErrIO
	}
	// From this point the generation can be visible through the existing public
	// link. A later failure retains the new generation and never rolls it back.
	published = true
	if syncDirectory(s.root) != nil {
		return ErrIO
	}
	if !state.public {
		if s.bin.Symlink(managerName+"/current/"+executableName, executableName) != nil {
			return ErrIO
		}
		if syncDirectory(s.bin) != nil {
			return ErrIO
		}
	}
	if err := visit(check, "published"); err != nil {
		return err
	}
	// Finish bounded cleanup even if cancellation arrives after publication.
	for _, old := range state.generations {
		if old.name == state.current {
			if err := removeGeneration(s, old); err != nil {
				return errors.Join(ErrCleanup, err)
			}
		}
	}
	if syncDirectory(s.root) != nil {
		return errors.Join(ErrCleanup, ErrIO)
	}
	return nil
}

func stage(ctx context.Context, s *store, name string, files []payload, record manifest, check checkpoint) (*generation, error) {
	if s.root.Mkdir(name, 0700) != nil {
		return nil, ErrIO
	}
	root, info, err := openDirectory(s.root, name, true)
	if err != nil {
		return nil, errors.Join(ErrCleanup, err)
	}
	defer root.Close()
	g := &generation{name: name, manifest: record, files: map[string]os.FileInfo{}, dirs: map[string]os.FileInfo{".": info}}
	for _, directory := range []string{"notices", "notices/runtime", "notices/reference"} {
		if root.Mkdir(directory, 0700) != nil {
			return g, ErrIO
		}
		info, err := root.Lstat(directory)
		if err != nil {
			return g, errors.Join(ErrCleanup, ErrIO)
		}
		g.dirs[directory] = info
	}
	for _, file := range files {
		if err := trackedWrite(ctx, root, g, file.name, file.data, fileMode(file.name)); err != nil {
			return g, err
		}
		if err := visit(check, "payload"); err != nil {
			return g, err
		}
	}
	data, err := json.Marshal(record)
	if err != nil || len(data) > maxManifest {
		return g, ErrSource
	}
	if err := trackedWrite(ctx, root, g, "manifest.json", data, 0600); err != nil {
		return g, err
	}
	for _, directory := range []string{"notices/runtime", "notices/reference", "notices", "."} {
		child, err := root.OpenRoot(directory)
		if err != nil {
			return g, ErrIO
		}
		err = syncDirectory(child)
		child.Close()
		if err != nil {
			return g, err
		}
	}
	verified, err := verifyGeneration(ctx, s.root, name, record.Generation)
	if err != nil {
		return g, err
	}
	return verified, nil
}

func trackedWrite(ctx context.Context, root *os.Root, g *generation, name string, data []byte, mode os.FileMode) (err error) {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return ErrIO
	}
	defer func() {
		info, statErr := file.Stat()
		if statErr == nil {
			g.files[name] = info
		} else {
			err = errors.Join(err, ErrCleanup, ErrIO)
		}
		if file.Close() != nil {
			err = errors.Join(err, ErrIO)
		}
	}()
	if file.Chmod(mode) != nil {
		return ErrIO
	}
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(len(data), 32<<10)
		n, err := file.Write(data[:end])
		if err != nil || n != end {
			return ErrIO
		}
		data = data[end:]
	}
	if file.Sync() != nil {
		return ErrIO
	}
	return nil
}

func samePublication(s *store, state inventory) error {
	if err := s.check(); err != nil {
		return err
	}
	public, info, err := publicLink(s)
	if err != nil || public != state.public || public && !unchanged(info, state.publicInfo) {
		return ErrUnmanaged
	}
	current, err := pointer(s.root, "current")
	if err != nil || current != state.current {
		return ErrUnmanaged
	}
	return nil
}
func removePointer(root *os.Root, name, expected string) error {
	value, err := pointer(root, name)
	if err != nil || value != expected {
		return ErrUnmanaged
	}
	if root.Remove(name) != nil {
		return ErrIO
	}
	return nil
}
func reclaim(s *store, state inventory) error {
	if err := s.check(); err != nil {
		return err
	}
	if state.next != "" {
		if err := removePointer(s.root, ".next", state.next); err != nil {
			return err
		}
	}
	for _, g := range state.generations {
		if g.name != state.current {
			if err := removeGeneration(s, g); err != nil {
				return err
			}
		}
	}
	return nil
}

// Uninstall removes only an idle, validated installation. User directories and
// settings are not installation contents and are never deleted.
func Uninstall(ctx context.Context, binPath string) error {
	s, err := openStore(ctx, binPath, false, false)
	if err != nil || s == nil {
		return err
	}
	defer s.Close()
	state, err := inspect(ctx, s)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := samePublication(s, state); err != nil {
		return err
	}
	if state.public {
		if s.bin.Remove(executableName) != nil {
			return ErrIO
		}
		if syncDirectory(s.bin) != nil {
			return errors.Join(ErrCleanup, ErrIO)
		}
	}
	// Once removal starts, finish bounded cleanup rather than honor cancellation
	// between unlink operations. Failures retain the remaining files for inspection.
	for name, value := range map[string]string{"current": state.current, ".next": state.next} {
		if value != "" {
			if err := removePointer(s.root, name, value); err != nil {
				return errors.Join(ErrCleanup, err)
			}
		}
	}
	for _, g := range state.generations {
		if err := removeGeneration(s, g); err != nil {
			return errors.Join(ErrCleanup, err)
		}
	}
	if err := removeManager(s); err != nil {
		return errors.Join(ErrCleanup, err)
	}
	return nil
}

// Lease holds a shared lock for an installed process's entire lifetime, including
// its helper cleanup. Unmanaged development executables need no lease. Startup
// racing a replacement can fail; it cannot admit deletion under a held lease.
func Lease(executable string) (io.Closer, error) {
	if !filepath.IsAbs(executable) {
		return nil, ErrLocation
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, ErrLocation
	}
	directory := filepath.Dir(resolved)
	manager := filepath.Dir(directory)
	if filepath.Base(manager) != managerName {
		return nil, nil
	}
	identity := filepath.Base(directory)
	if filepath.Base(resolved) != executableName || !generationName(identity) {
		return nil, ErrUnmanaged
	}
	s, err := openStore(context.Background(), filepath.Dir(manager), false, true)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrUnmanaged
	}
	valid := false
	defer func() {
		if !valid {
			s.Close()
		}
	}()
	current, err := pointer(s.root, "current")
	if err != nil || current != identity {
		return nil, ErrUnmanaged
	}
	public, _, err := publicLink(s)
	if err != nil || !public {
		return nil, ErrUnmanaged
	}
	root, _, err := openDirectory(s.root, identity, true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	record, err := readManifest(context.Background(), root, identity)
	if err != nil {
		return nil, err
	}
	// Launch is not a repeated integrity scan of a potentially 128 MiB executable.
	// The exclusive installer verifies hashes; this shared lease verifies metadata.
	for _, file := range record.Files {
		if file.Path != executableName {
			continue
		}
		info, err := root.Lstat(executableName)
		if err != nil || !safeFile(info, 0700) || info.Size() != file.Bytes {
			return nil, ErrUnmanaged
		}
	}
	valid = true
	return s, nil
}
