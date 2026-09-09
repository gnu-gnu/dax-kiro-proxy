package launcher

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/privatefs"
)

var ErrState = errors.New("private launcher state is unavailable or invalid")

// Contended initialization fails immediately; callers can retry without replacing a retained key.
// The lock inode stays on disk. Neither malformed state nor cancellation authorizes regeneration.
func loadScopeKey(ctx context.Context, directory string) (key [32]byte, err error) {
	if ctx.Err() != nil {
		return key, ctx.Err()
	}
	if !identityPath(directory) {
		return key, ErrState
	}
	directory = filepath.Clean(directory)
	store, err := privatefs.New(directory)
	if err != nil {
		return key, ErrState
	}
	root, err := os.Lstat(directory)
	if err != nil {
		return key, ErrState
	}
	if ctx.Err() != nil {
		return key, ctx.Err()
	}
	lock, err := store.TryLock("scope-key.lock")
	if err != nil {
		if ctx.Err() != nil {
			return key, ctx.Err()
		}
		if errors.Is(err, privatefs.ErrLocked) {
			return key, errors.Join(ErrState, privatefs.ErrLocked)
		}
		return key, ErrState
	}
	defer func() {
		if lock.Close() != nil {
			key, err = [32]byte{}, errors.Join(err, ErrState)
		}
	}()
	if ctx.Err() != nil {
		return key, ctx.Err()
	}
	if !scopeOwnerUnchanged(directory, root, lock) {
		return key, ErrState
	}
	path := filepath.Join(directory, "scope-key.bin")
	before, statErr := os.Lstat(path)
	var created [32]byte
	if errors.Is(statErr, os.ErrNotExist) {
		if _, err := rand.Read(created[:]); err != nil || created == ([32]byte{}) {
			return key, ErrState
		}
		if ctx.Err() != nil {
			return key, ctx.Err()
		}
		if !scopeOwnerUnchanged(directory, root, lock) || store.Write("scope-key.bin", created[:]) != nil {
			return key, ErrState
		}
		before, statErr = os.Lstat(path)
	}
	if statErr != nil {
		return key, ErrState
	}
	data, readErr := store.Read("scope-key.bin", len(key))
	if readErr != nil || len(data) != len(key) {
		return key, ErrState
	}
	after, statErr := os.Lstat(path)
	if statErr != nil || !scopeFileUnchanged(before, after) || !scopeOwnerUnchanged(directory, root, lock) {
		return key, ErrState
	}
	copy(key[:], data)
	if key == ([32]byte{}) || created != ([32]byte{}) && key != created {
		return [32]byte{}, ErrState
	}
	if ctx.Err() != nil {
		return [32]byte{}, ctx.Err()
	}
	return key, nil
}

func scopeOwnerUnchanged(directory string, original os.FileInfo, lock *privatefs.Lock) bool {
	current, err := os.Lstat(directory)
	return err == nil && original != nil && os.SameFile(original, current) && lock.Check() == nil
}

func scopeFileUnchanged(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) &&
		before.Size() == after.Size() && before.Mode() == after.Mode() && before.ModTime().Equal(after.ModTime())
}

// Catalog identity includes the measured development launch policy. ACP capabilities remain unknown
// until negotiated by each process; a cached catalog itself confers no execution authority.
func makeLaunchIdentity(key [32]byte, home string, info KiroInfo, model, effort string) (catalog.Identity, error) {
	if key == ([32]byte{}) || !identityPath(home) || !identityPath(info.Executable) ||
		!identityToken(info.Version, 256, false) || !identityToken(model, 256, true) ||
		effort != "" && kirofeature.Normalize(effort) != effort {
		return catalog.Identity{}, ErrState
	}
	account, err := hex.DecodeString(info.ProfileScope)
	if err != nil || len(account) != 32 || hex.EncodeToString(account) != info.ProfileScope {
		return catalog.Identity{}, ErrState
	}
	profile, _ := json.Marshal(struct{ Home, Account string }{filepath.Clean(home), info.ProfileScope})
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte("dax-launch-profile-v1\x00"))
	mac.Write(profile)
	agent := sha256.Sum256([]byte(kiroDevelopmentPolicy))
	capabilities := sha256.Sum256([]byte("dax-acp-capability-scope-v1\x00unknown\x00not-negotiated"))
	identity := catalog.Identity{
		Executable: info.Executable, Version: info.Version,
		ProfileDigest: hex.EncodeToString(mac.Sum(nil)), AgentDigest: hex.EncodeToString(agent[:]),
		CapabilitiesDigest: hex.EncodeToString(capabilities[:]), InitialModel: model, InitialEffort: effort,
	}
	if _, err := identity.Digest(); err != nil {
		return catalog.Identity{}, ErrState
	}
	return identity, nil
}

func identityPath(path string) bool {
	if !filepath.IsAbs(path) || len(path) > 4096 || !utf8.ValidString(path) {
		return false
	}
	return !strings.ContainsFunc(path, unicode.IsControl)
}

func identityToken(value string, limit int, empty bool) bool {
	if len(value) > limit || value == "" && !empty || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}
