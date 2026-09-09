// Package installation manages a bounded per-user executable and its notice files.
package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"dax-kiro-proxy/internal/ndjson"
	"dax-kiro-proxy/third_party/notices"
)

const MaxExecutableBytes = 128 << 20
const (
	executableName = "dax-kiro-proxy"
	managerName    = ".dax-kiro-proxy-install"
	marker         = "{\"format\":1,\"product\":\"dax-kiro-proxy\"}\n"
	maxManifest    = 16 << 10
	maxFiles       = 16
	maxGenerations = 4
)

var (
	ErrLocation  = errors.New("installation requires an owned directory with safe permissions")
	ErrSource    = errors.New("installation source must be a bounded regular executable")
	ErrUnmanaged = errors.New("installation contains unrecognized or changed files")
	ErrExists    = errors.New("installation already exists")
	ErrBusy      = errors.New("installation is in use")
	ErrIO        = errors.New("installation filesystem operation failed")
	ErrCleanup   = errors.New("installation cleanup is incomplete; retained artifacts require inspection")
	ErrPublished = errors.New("installation publication changed before the operation failed")
)

type fileRecord struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Format     int          `json:"format"`
	Product    string       `json:"product"`
	Generation string       `json:"generation"`
	Files      []fileRecord `json:"files"`
}
type payload struct {
	name string
	data []byte
}
type generation struct {
	name     string
	manifest manifest
	files    map[string]os.FileInfo
	dirs     map[string]os.FileInfo
}
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

func generationName(name string) bool {
	if !strings.HasPrefix(name, "generation-") || len(name) != 43 {
		return false
	}
	_, err := hex.DecodeString(name[11:])
	return err == nil && strings.ToLower(name) == name
}
func recordPath(name string) bool {
	if name == executableName {
		return true
	}
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[0] != "notices" || (parts[1] != "runtime" && parts[1] != "reference") || len(parts[2]) > 96 || !strings.HasSuffix(parts[2], ".txt") {
		return false
	}
	for _, c := range parts[2] {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return parts[2] != ".txt" && path.Clean(name) == name
}
func fileMode(name string) os.FileMode {
	if name == executableName {
		return 0700
	}
	return 0600
}
func unchanged(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func openFile(root *os.Root, name string, mode os.FileMode, limit int64) (*os.File, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil || !safeFile(before, mode) || before.Size() < 0 || before.Size() > limit {
		return nil, nil, ErrUnmanaged
	}
	file, err := root.OpenFile(name, readFlags, 0)
	if err != nil {
		return nil, nil, ErrUnmanaged
	}
	opened, err := file.Stat()
	if err != nil || !safeFile(opened, mode) || !unchanged(before, opened) {
		file.Close()
		return nil, nil, ErrUnmanaged
	}
	return file, opened, nil
}
func readFile(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	file, before, err := openFile(root, name, 0600, limit)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(contextReader{ctx, io.LimitReader(file, limit+1)})
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !unchanged(before, after) || int64(len(data)) != before.Size() {
		return nil, ErrUnmanaged
	}
	return data, nil
}
func readSource(ctx context.Context, name string) ([]byte, error) {
	if !filepath.IsAbs(name) {
		return nil, ErrSource
	}
	file, err := os.OpenFile(name, readFlags, 0)
	if err != nil {
		return nil, ErrSource
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !safeFile(before, 0) || before.Size() < 1 || before.Size() > MaxExecutableBytes {
		return nil, ErrSource
	}
	data, err := io.ReadAll(contextReader{ctx, io.LimitReader(file, MaxExecutableBytes+1)})
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !unchanged(before, after) || int64(len(data)) != before.Size() {
		return nil, ErrSource
	}
	return data, nil
}
func bundle(data []byte, name string) ([]payload, manifest, error) {
	files := []payload{{executableName, data}}
	total := 0
	err := fs.WalkDir(notices.Files, ".", func(name string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		content, err := notices.Files.ReadFile(name)
		if err != nil || len(content) == 0 || len(content) > 64<<10 || !recordPath("notices/"+name) {
			return ErrSource
		}
		total += len(content)
		if len(files) >= maxFiles || total > 1<<20 {
			return ErrSource
		}
		files = append(files, payload{"notices/" + name, content})
		return nil
	})
	if err != nil {
		return nil, manifest{}, ErrSource
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	record := manifest{Format: 1, Product: executableName, Generation: name}
	for _, file := range files {
		sum := sha256.Sum256(file.data)
		record.Files = append(record.Files, fileRecord{file.name, int64(len(file.data)), hex.EncodeToString(sum[:])})
	}
	return files, record, nil
}
func readManifest(ctx context.Context, root *os.Root, name string) (manifest, error) {
	data, err := readFile(ctx, root, "manifest.json", maxManifest)
	if err != nil {
		return manifest{}, err
	}
	if _, err := ndjson.Object(data); err != nil {
		return manifest{}, ErrUnmanaged
	}
	var record manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || record.Format != 1 || record.Product != executableName || record.Generation != name || !generationName(name) || len(record.Files) < 2 || len(record.Files) > maxFiles {
		return manifest{}, ErrUnmanaged
	}
	seen, total := map[string]bool{}, int64(0)
	for _, file := range record.Files {
		if !recordPath(file.Path) || seen[file.Path] || file.Bytes < 1 {
			return manifest{}, ErrUnmanaged
		}
		seen[file.Path] = true
		limit := int64(64 << 10)
		if file.Path == executableName {
			limit = MaxExecutableBytes
		} else {
			total += file.Bytes
		}
		decoded, err := hex.DecodeString(file.SHA256)
		if file.Bytes > limit || total > 1<<20 || err != nil || len(decoded) != 32 {
			return manifest{}, ErrUnmanaged
		}
	}
	if !seen[executableName] {
		return manifest{}, ErrUnmanaged
	}
	return record, nil
}
func openDirectory(root *os.Root, name string, private bool) (*os.Root, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil || !safeDirectory(before, private) {
		return nil, nil, ErrUnmanaged
	}
	directory, err := root.OpenRoot(name)
	if err != nil {
		return nil, nil, ErrUnmanaged
	}
	after, err := directory.Stat(".")
	if err != nil || !safeDirectory(after, private) || !os.SameFile(before, after) {
		directory.Close()
		return nil, nil, ErrUnmanaged
	}
	return directory, after, nil
}
func names(root *os.Root, limit int) ([]string, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, ErrIO
	}
	defer file.Close()
	items, err := file.Readdirnames(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) || len(items) > limit {
		return nil, ErrUnmanaged
	}
	sort.Strings(items)
	return items, nil
}
func verifyGeneration(ctx context.Context, manager *os.Root, name, identity string) (*generation, error) {
	root, rootInfo, err := openDirectory(manager, name, true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	manifestBefore, err := root.Lstat("manifest.json")
	if err != nil {
		return nil, ErrUnmanaged
	}
	record, err := readManifest(ctx, root, identity)
	if err != nil {
		return nil, err
	}
	g := &generation{name: name, manifest: record, files: map[string]os.FileInfo{}, dirs: map[string]os.FileInfo{".": rootInfo}}
	expected := map[string]bool{"manifest.json": true}
	for _, file := range record.Files {
		expected[file.Path] = true
		for directory := path.Dir(file.Path); directory != "."; directory = path.Dir(directory) {
			expected[directory] = true
		}
		opened, before, err := openFile(root, file.Path, fileMode(file.Path), file.Bytes)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		n, copyErr := io.CopyBuffer(hash, contextReader{ctx, io.LimitReader(opened, file.Bytes+1)}, make([]byte, 32<<10))
		after, statErr := opened.Stat()
		closeErr := opened.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if n != file.Bytes || statErr != nil || closeErr != nil || !unchanged(before, after) || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
			return nil, ErrUnmanaged
		}
		g.files[file.Path] = after
	}
	var walk func(*os.Root, string) error
	walk = func(directory *os.Root, prefix string) error {
		items, err := names(directory, maxFiles+4)
		if err != nil {
			return err
		}
		for _, item := range items {
			relative := path.Join(prefix, item)
			if !expected[relative] {
				return ErrUnmanaged
			}
			info, err := directory.Lstat(item)
			if err != nil {
				return ErrUnmanaged
			}
			if info.IsDir() {
				child, opened, err := openDirectory(directory, item, true)
				if err != nil {
					return err
				}
				g.dirs[relative] = opened
				err = walk(child, relative)
				child.Close()
				if err != nil {
					return err
				}
			} else if relative == "manifest.json" {
				if !safeFile(info, 0600) || !unchanged(manifestBefore, info) {
					return ErrUnmanaged
				}
				g.files[relative] = info
			} else if before, ok := g.files[relative]; !ok || !unchanged(before, info) {
				return ErrUnmanaged
			}
		}
		return nil
	}
	if err := walk(root, "."); err != nil {
		return nil, err
	}
	return g, nil
}
func writeFile(ctx context.Context, root *os.Root, name string, data []byte, mode os.FileMode) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return ErrIO
	}
	defer file.Close()
	if file.Chmod(mode) != nil {
		return ErrIO
	}
	n, err := io.CopyBuffer(file, contextReader{ctx, bytes.NewReader(data)}, make([]byte, 32<<10))
	if err != nil {
		return err
	}
	if n != int64(len(data)) || file.Sync() != nil || file.Close() != nil {
		return ErrIO
	}
	return nil
}
func syncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return ErrIO
	}
	defer file.Close()
	if file.Sync() != nil {
		return ErrIO
	}
	return nil
}
