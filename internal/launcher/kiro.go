package launcher

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/ndjson"
)

var ErrKiroVersion = errors.New("Kiro installation or version is not supported")
var ErrLoginCheck = errors.New("Kiro login could not be verified; run kiro-cli login and retry")

// SupportedKiroVersion is the measured Kiro main/helper build behind the recorded evidence. D59
// moved it to 2.21.2 and D114 to 2.21.3 after fresh finite account/catalog checks. D114 admits any
// main/helper pair reporting the same build with this major version; startup reports whether the
// detected build is the measured one rather than treating it as verified.
const SupportedKiroVersion = "2.21.3"

// KiroVersionFromOutput parses "<name> <version>" from a bounded --version output and reports
// whether that build is admitted.
func KiroVersionFromOutput(name string, output []byte) (string, bool) {
	if len(output) > 256 {
		return "", false
	}
	version, named := strings.CutPrefix(strings.TrimSpace(string(output)), name+" ")
	if !named || !CompatibleKiroVersion(version) {
		return "", false
	}
	return version, true
}

// CompatibleKiroOutput is KiroVersionFromOutput's admission result alone.
func CompatibleKiroOutput(name string, output []byte) bool {
	_, ok := KiroVersionFromOutput(name, output)
	return ok
}

// CompatibleKiroVersion reports whether a Kiro build may be used: only the major component is
// compared against SupportedKiroVersion (D114). The main/helper pair must still match exactly.
func CompatibleKiroVersion(version string) bool {
	return compatibleMajor(version, SupportedKiroVersion)
}

type CommandRunner interface {
	Run(context.Context, childproc.Command) (childproc.Result, error)
}
type KiroConfig struct {
	Executable, Home, Directory string
	ScopeKey                    [32]byte
}
type KiroInfo struct {
	Executable, Helper, Version, ProfileScope string
	HadPostamble                              bool
}

// CheckKiro performs finite, read-only checks. It establishes a versioned identity/cache scope;
// it does not prove that the account's next model request will succeed or that tools are restricted.
func CheckKiro(ctx context.Context, runner CommandRunner, cfg KiroConfig) (KiroInfo, error) {
	if cfg.ScopeKey == ([32]byte{}) {
		return KiroInfo{}, ErrConfig
	}
	info, command, err := checkedKiroCommand(ctx, runner, cfg)
	if err != nil {
		return KiroInfo{}, err
	}
	command.Args = []string{"whoami", "--format", "json"}
	result, err := runner.Run(ctx, command)
	if ctx.Err() != nil {
		return KiroInfo{}, ctx.Err()
	}
	if err != nil || result.ExitCode != 0 {
		return KiroInfo{}, ErrLoginCheck
	}
	identity, postamble, err := kiroIdentity(result.Stdout)
	if err != nil {
		return KiroInfo{}, err
	}
	mac := hmac.New(sha256.New, cfg.ScopeKey[:])
	mac.Write([]byte("dax-kiro-identity-v1\x00"))
	mac.Write(identity)
	info.ProfileScope = hex.EncodeToString(mac.Sum(nil))
	info.HadPostamble = postamble
	return info, nil
}

func checkedKiroCommand(ctx context.Context, runner CommandRunner, cfg KiroConfig) (KiroInfo, childproc.Command, error) {
	if runner == nil {
		return KiroInfo{}, childproc.Command{}, ErrConfig
	}
	for _, path := range []string{cfg.Executable, cfg.Home, cfg.Directory} {
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return KiroInfo{}, childproc.Command{}, ErrConfig
		}
	}
	info := KiroInfo{Executable: cfg.Executable, Helper: filepath.Join(filepath.Dir(cfg.Executable), "kiro-cli-chat")}
	env := []string{"HOME=" + cfg.Home, "PATH=" + filepath.Dir(cfg.Executable) + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + cfg.Directory, "TERM=dumb", "LANG=en_US.UTF-8"}
	command := childproc.Command{Executable: cfg.Executable, Directory: cfg.Directory, Environment: env, Args: []string{"--version"}}
	for i, executable := range []string{info.Executable, info.Helper} {
		if ctx.Err() != nil {
			return KiroInfo{}, childproc.Command{}, ctx.Err()
		}
		command.Executable = executable
		result, err := runner.Run(ctx, command)
		if ctx.Err() != nil {
			return KiroInfo{}, childproc.Command{}, ctx.Err()
		}
		name := "kiro-cli"
		if i == 1 {
			name = "kiro-cli-chat"
		}
		version, ok := KiroVersionFromOutput(name, result.Stdout)
		if err != nil || result.ExitCode != 0 || !ok || i == 1 && version != info.Version {
			return KiroInfo{}, childproc.Command{}, ErrKiroVersion
		}
		info.Version = version
	}
	command.Executable = cfg.Executable
	return info, command, nil
}

func kiroIdentity(output []byte) ([]byte, bool, error) {
	if len(output) == 0 || len(output) > 64<<10 || !utf8.Valid(output) || bytes.ContainsRune(output, 0) {
		return nil, false, ErrLoginCheck
	}
	first, tail, _ := bytes.Cut(output, []byte{'\n'})
	if len(first) > 16<<10 {
		return nil, false, ErrLoginCheck
	}
	fields, err := ndjson.Object(first)
	if err != nil || len(fields) > 32 {
		return nil, false, ErrLoginCheck
	}
	// Observed 2.21.1 appends non-JSON CLI text even in JSON mode. It cannot override the first
	// object's identity. Reject a second or incomplete JSON-looking payload and bound the remainder.
	tail = bytes.TrimSpace(tail)
	if len(tail) > 4096 || bytes.Count(tail, []byte{'\n'}) > 16 {
		return nil, false, ErrLoginCheck
	}
	for _, line := range bytes.Split(tail, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 && (json.Valid(line) || line[0] == '{' || line[0] == '[' || line[0] == '"') {
			return nil, false, ErrLoginCheck
		}
	}
	identity := map[string]string{}
	for key, limit := range map[string]int{"accountType": 64, "email": 1024, "region": 128, "startUrl": 2048} {
		value, ok := fields[key]
		if !ok && (key == "region" || key == "startUrl") {
			continue
		}
		var text string
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &text) != nil || len(text) > limit || ((key == "email" || key == "accountType") && strings.TrimSpace(text) == "") {
			return nil, false, ErrLoginCheck
		}
		for _, r := range text {
			if unicode.IsControl(r) {
				return nil, false, ErrLoginCheck
			}
		}
		identity[key] = text
	}
	canonical, _ := json.Marshal(identity)
	return canonical, len(tail) > 0, nil
}
