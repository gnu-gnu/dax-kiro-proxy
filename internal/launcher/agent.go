package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"dax-kiro-proxy/internal/privatefs"
	"dax-kiro-proxy/internal/toolregistry"
)

type AgentConfig struct {
	Directory                    string
	Registry                     *toolregistry.Registry
	RelayExecutable, RelayConfig string
}
type CandidateAgent struct {
	Name, Path, PolicyDigest string
	ExecutionVerified        bool
}

// WriteCandidateAgent makes a reviewable input for restricted-agent interoperability tests. Syntax
// or a generated allowlist cannot establish effective Kiro restrictions; this function never marks
// an agent verified or starts a Kiro process.
func WriteCandidateAgent(cfg AgentConfig) (CandidateAgent, error) {
	if cfg.Registry == nil {
		return CandidateAgent{}, ErrConfig
	}
	for _, path := range []string{cfg.Directory, cfg.RelayExecutable, cfg.RelayConfig} {
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return CandidateAgent{}, ErrConfig
		}
	}
	refs := []string{}
	for _, tool := range cfg.Registry.Tools() {
		refs = append(refs, "@dax_session/"+tool.Alias)
	}
	fields := map[string]any{
		"description": "Client tool relay for an independent local ACP proxy",
		"tools":       refs, "allowedTools": refs, "resources": []string{}, "hooks": map[string]any{}, "includeMcpJson": false,
		"mcpServers": map[string]any{"dax_session": map[string]any{"command": cfg.RelayExecutable, "args": []string{"relay", "--config", cfg.RelayConfig}, "env": map[string]string{}}},
	}
	policy, _ := json.Marshal(struct {
		Version  int
		Kiro     string
		Registry string
		Agent    any
	}{1, SupportedKiroVersion, cfg.Registry.Fingerprint(), fields})
	digest := sha256.Sum256(policy)
	result := CandidateAgent{PolicyDigest: hex.EncodeToString(digest[:])}
	result.Name = "dax-" + result.PolicyDigest[:32]
	fields["name"] = result.Name
	data, err := json.Marshal(fields)
	if err != nil || len(data) > 64<<10 {
		return CandidateAgent{}, ErrConfig
	}
	if _, err := privatefs.New(cfg.Directory); err != nil {
		return CandidateAgent{}, ErrRuntime
	}
	root, err := os.OpenRoot(cfg.Directory)
	if err != nil {
		return CandidateAgent{}, ErrRuntime
	}
	defer root.Close()
	for _, part := range []string{".kiro", ".kiro/agents"} {
		if err := root.Mkdir(part, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return CandidateAgent{}, ErrRuntime
		}
		info, err := root.Lstat(part)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return CandidateAgent{}, ErrRuntime
		}
	}
	dir, err := privatefs.New(filepath.Join(cfg.Directory, ".kiro", "agents"))
	if err != nil {
		return CandidateAgent{}, ErrRuntime
	}
	name := result.Name + ".json"
	if old, err := dir.Read(name, 64<<10); err == nil {
		if string(old) != string(data) {
			return CandidateAgent{}, ErrRuntime
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return CandidateAgent{}, ErrRuntime
	}
	if dir.Write(name, data) != nil {
		return CandidateAgent{}, ErrRuntime
	}
	result.Path = filepath.Join(cfg.Directory, ".kiro", "agents", name)
	return result, nil
}
