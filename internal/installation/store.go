package installation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type store struct {
	binPath           string
	bin, root         *os.Root
	binInfo, rootInfo os.FileInfo
	lock              *os.File
	created           bool
	once              sync.Once
	closeErr          error
}

func (s *store) Close() error {
	s.once.Do(func() {
		if s.root != nil {
			s.closeErr = errors.Join(s.closeErr, s.root.Close())
		}
		if s.bin != nil {
			s.closeErr = errors.Join(s.closeErr, s.bin.Close())
		}
		if s.lock != nil {
			s.closeErr = errors.Join(s.closeErr, s.lock.Close())
		}
	})
	return s.closeErr
}
func (s *store) check() error {
	binInfo, err := os.Lstat(s.binPath)
	if err != nil || !safeDirectory(binInfo, false) || !os.SameFile(binInfo, s.binInfo) {
		return ErrUnmanaged
	}
	rootInfo, err := s.bin.Lstat(managerName)
	if err != nil || !safeDirectory(rootInfo, true) || !os.SameFile(rootInfo, s.rootInfo) {
		return ErrUnmanaged
	}
	info, err := s.root.Lstat("lock")
	if err != nil || !safeFile(info, 0600) || info.Size() != 0 {
		return ErrUnmanaged
	}
	opened, err := s.lock.Stat()
	if err != nil || !unchanged(info, opened) {
		return ErrUnmanaged
	}
	return nil
}

func openStore(ctx context.Context, binPath string, create, shared bool) (_ *store, err error) {
	if !supported || !filepath.IsAbs(binPath) || filepath.Clean(binPath) != binPath || binPath == "/" || len(binPath) > 4096 {
		return nil, ErrLocation
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(binPath)
	if errors.Is(err, os.ErrNotExist) && create {
		if os.MkdirAll(binPath, 0700) != nil {
			return nil, ErrLocation
		}
		info, err = os.Lstat(binPath)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !safeDirectory(info, false) {
		return nil, ErrLocation
	}
	bin, err := os.OpenRoot(binPath)
	if err != nil {
		return nil, ErrLocation
	}
	s := &store{binPath: binPath, bin: bin, binInfo: info}
	defer func() {
		if err != nil {
			if s.created {
				err = errors.Join(err, ErrCleanup)
			}
			s.Close()
		}
	}()
	opened, err := bin.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrLocation
	}
	_, err = bin.Lstat(managerName)
	if errors.Is(err, os.ErrNotExist) {
		if _, publicErr := bin.Lstat(executableName); !errors.Is(publicErr, os.ErrNotExist) {
			return nil, ErrUnmanaged
		}
		if !create {
			s.Close()
			return nil, nil
		}
		if bin.Mkdir(managerName, 0700) != nil {
			return nil, ErrIO
		}
		s.created = true
	} else if err != nil {
		return nil, ErrUnmanaged
	}
	s.root, s.rootInfo, err = openDirectory(bin, managerName, true)
	if err != nil {
		return nil, err
	}
	if s.created {
		s.lock, err = s.root.OpenFile("lock", lockFlags|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, ErrIO
		}
		if s.lock.Chmod(0600) != nil || lockFile(s.lock, false) != nil {
			return nil, ErrIO
		}
		// Complete this small bootstrap once its directory has been created. Install
		// handles cancellation immediately after opening the validated store.
		if err := writeFile(context.Background(), s.root, "format.json", []byte(marker), 0600); err != nil {
			return nil, err
		}
		if s.lock.Sync() != nil || syncDirectory(s.root) != nil || syncDirectory(bin) != nil {
			return nil, ErrIO
		}
	} else {
		data, readErr := readFile(ctx, s.root, "format.json", 128)
		if readErr != nil || string(data) != marker {
			return nil, ErrUnmanaged
		}
		// An existing manager never creates a missing lock. Unlinking cannot split owners
		// across an old inode and a newly created one while removal is in progress.
		s.lock, err = s.root.OpenFile("lock", lockFlags, 0)
		if err != nil {
			return nil, ErrUnmanaged
		}
		info, statErr := s.lock.Stat()
		if statErr != nil || !safeFile(info, 0600) || info.Size() != 0 {
			return nil, ErrUnmanaged
		}
		if err := lockFile(s.lock, shared); err != nil {
			return nil, err
		}
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	return s, nil
}

func publicLink(s *store) (bool, os.FileInfo, error) {
	info, err := s.bin.Lstat(executableName)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil || !safeLink(info) {
		return false, nil, ErrUnmanaged
	}
	value, err := s.bin.Readlink(executableName)
	if err != nil || value != managerName+"/current/"+executableName {
		return false, nil, ErrUnmanaged
	}
	return true, info, nil
}
func pointer(root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !safeLink(info) {
		return "", ErrUnmanaged
	}
	value, err := root.Readlink(name)
	if err != nil || !generationName(value) {
		return "", ErrUnmanaged
	}
	return value, nil
}

type inventory struct {
	current, next string
	public        bool
	publicInfo    os.FileInfo
	generations   []*generation
}

func inspect(ctx context.Context, s *store) (inventory, error) {
	var result inventory
	var err error
	result.public, result.publicInfo, err = publicLink(s)
	if err != nil {
		return result, err
	}
	result.current, err = pointer(s.root, "current")
	if err != nil {
		return result, err
	}
	result.next, err = pointer(s.root, ".next")
	if err != nil {
		return result, err
	}
	items, err := names(s.root, maxGenerations+4)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, name := range items {
		if name == "format.json" || name == "lock" || name == "current" || name == ".next" {
			continue
		}
		identity := name
		if strings.HasPrefix(name, "staging-") {
			identity = "generation-" + strings.TrimPrefix(name, "staging-")
		}
		if !generationName(identity) || len(result.generations) >= maxGenerations {
			return result, ErrUnmanaged
		}
		g, err := verifyGeneration(ctx, s.root, name, identity)
		if err != nil {
			return result, err
		}
		result.generations = append(result.generations, g)
		seen[name] = true
	}
	if result.current != "" && !seen[result.current] || result.next != "" && !seen[result.next] || result.public && result.current == "" {
		return result, ErrUnmanaged
	}
	return result, nil
}
func removeGeneration(s *store, g *generation) error {
	if err := s.check(); err != nil {
		return err
	}
	root, info, err := openDirectory(s.root, g.name, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if !os.SameFile(info, g.dirs["."]) {
		return ErrUnmanaged
	}
	// Recheck the complete tree before the first removal, including partial stages
	// whose files were recorded when created. Unknown entries preserve the whole tree.
	if err := removalTree(root, g); err != nil {
		return err
	}
	files := make([]string, 0, len(g.files))
	for name := range g.files {
		files = append(files, name)
	}
	sort.Strings(files)
	for _, name := range files {
		if root.Remove(name) != nil {
			return ErrIO
		}
	}
	directories := make([]string, 0, len(g.dirs))
	for name := range g.dirs {
		if name != "." {
			directories = append(directories, name)
		}
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, name := range directories {
		info, err := root.Lstat(name)
		if err != nil || !os.SameFile(info, g.dirs[name]) || root.Remove(name) != nil {
			return ErrIO
		}
	}
	if s.root.Remove(g.name) != nil {
		return ErrIO
	}
	return nil
}
func removalTree(root *os.Root, g *generation) error {
	seenFiles, seenDirs := 0, 1
	var walk func(*os.Root, string) error
	walk = func(directory *os.Root, prefix string) error {
		items, err := names(directory, maxFiles+4)
		if err != nil {
			return err
		}
		for _, item := range items {
			name := item
			if prefix != "" {
				name = prefix + "/" + item
			}
			info, err := directory.Lstat(item)
			if err != nil {
				return ErrUnmanaged
			}
			if before, ok := g.dirs[name]; ok {
				if !safeDirectory(info, true) || !os.SameFile(info, before) {
					return ErrUnmanaged
				}
				child, _, err := openDirectory(directory, item, true)
				if err != nil {
					return err
				}
				seenDirs++
				err = walk(child, name)
				child.Close()
				if err != nil {
					return err
				}
			} else if before, ok := g.files[name]; !ok || !unchanged(before, info) {
				return ErrUnmanaged
			} else {
				seenFiles++
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return err
	}
	if seenFiles != len(g.files) || seenDirs != len(g.dirs) {
		return ErrUnmanaged
	}
	return nil
}
func removeManager(s *store) error {
	if err := s.check(); err != nil {
		return err
	}
	items, err := names(s.root, 2)
	if err != nil || len(items) != 2 || items[0] != "format.json" || items[1] != "lock" {
		return ErrUnmanaged
	}
	data, err := readFile(context.Background(), s.root, "format.json", 128)
	if err != nil || string(data) != marker {
		return ErrUnmanaged
	}
	if s.root.Remove("format.json") != nil || s.root.Remove("lock") != nil || s.bin.Remove(managerName) != nil || syncDirectory(s.bin) != nil {
		return ErrIO
	}
	return nil
}
