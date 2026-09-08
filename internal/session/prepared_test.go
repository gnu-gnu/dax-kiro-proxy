package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/schemacheck"
	"dax-kiro-proxy/internal/session"
)

type preparedObservation struct {
	PID              int      `json:"pid"`
	LaunchDirectory  string   `json:"launchDirectory"`
	SessionDirectory string   `json:"sessionDirectory"`
	MCPCount         int      `json:"mcpCount"`
	LaunchAliases    []string `json:"launchAliases"`
	RelayConfig      string   `json:"relayConfig"`
	LaunchMarker     string   `json:"launchMarker"`
}

func TestManagerPreparedPolicyOwnsEachBindingAndKeepsTurnContinuity(t *testing.T) {
	cfg := managerConfig(t, "pool-prepared")
	validator, err := schemacheck.New(schemacheck.Config{Executable: relayBinary, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Close()
	cfg.Session.Validator, cfg.Session.RelayExecutable = validator, relayBinary
	root := t.TempDir()
	var preparations, cleanups atomic.Int32
	var expectedAliases []string
	cfg.Session.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		if preparations.Add(1) == 3 && cleanups.Load() != 1 {
			return session.LaunchResources{}, errors.New("changed tool policy prepared before the previous launch was joined")
		}
		if input.Registry == nil || input.RelayExecutable != relayBinary || input.RelayConfig == "" {
			return session.LaunchResources{}, errors.New("preparation missing exact declared registry or owned relay")
		}
		dir, err := os.MkdirTemp(root, "independent-policy-")
		if err != nil {
			return session.LaunchResources{}, err
		}
		marker := filepath.Join(dir, "owned-launch.json")
		aliases := []string{}
		for _, tool := range input.Registry.Tools() {
			aliases = append(aliases, tool.Alias)
		}
		expectedAliases = append([]string(nil), aliases...)
		raw, _ := json.Marshal(map[string]any{"aliases": aliases, "relayConfig": input.RelayConfig})
		owned := session.LaunchResources{Directory: dir, Args: []string{marker}, RelayAtLaunch: true, Cleanup: func() error { cleanups.Add(1); return os.RemoveAll(dir) }}
		if err := os.WriteFile(marker, raw, 0600); err != nil {
			return owned, err
		}
		return owned, ctx.Err()
	}
	m, err := session.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a := mainRequest(t, "one")
	a.Tools = toolRequest(t).Tools
	b := mainRequest(t, "two")
	b.Tools = a.Tools
	aFirst, aText := managerTurn(t, m, a)
	bFirst, bText := managerTurn(t, m, b)
	if aFirst.PID == bFirst.PID || preparations.Load() != 2 || cleanups.Load() != 0 {
		t.Fatal("bindings shared a launch policy or cleaned a live policy")
	}
	observations := []preparedObservation{}
	for _, text := range []string{aText, bText} {
		var observation preparedObservation
		if json.Unmarshal([]byte(text), &observation) != nil {
			t.Fatal("invalid independent observation")
		}
		if observation.LaunchDirectory == cfg.Session.Process.Directory || observation.SessionDirectory != cfg.Session.Process.Directory || observation.MCPCount != 0 || !slices.Equal(observation.LaunchAliases, expectedAliases) || len(observation.LaunchAliases) != 1 {
			t.Fatal("launch workspace, session cwd, exact registry or launch-owned relay contract lost")
		}
		for _, path := range []string{observation.LaunchMarker, observation.RelayConfig} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("owned configuration absent or permissions widened")
			}
		}
		observations = append(observations, observation)
	}
	if observations[0].LaunchDirectory == observations[1].LaunchDirectory || observations[0].RelayConfig == observations[1].RelayConfig {
		t.Fatal("separate prepared bindings reused mutable paths")
	}
	a.Messages = append(a.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: aText}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "independent next turn"}}})
	aNext, aNextText := managerTurn(t, m, a)
	if aNext.PID != aFirst.PID || aNext.Session != aFirst.Session || aNext.Count != 2 || len(aNext.Prompt) != 1 || preparations.Load() != 2 {
		t.Fatal("prepared policy recreated instead of reusing the same compatible session")
	}
	a.Messages = append(a.Messages, anthropic.Message{Role: "assistant", Content: []anthropic.Block{{Type: "text", Text: aNextText}}}, anthropic.Message{Role: "user", Content: []anthropic.Block{{Type: "text", Text: "continue with client tools disabled"}}})
	a.Extra["tool_choice"] = json.RawMessage(`{"type":"none"}`)
	withoutTools, withoutToolsText := managerTurn(t, m, a)
	var last preparedObservation
	if json.Unmarshal([]byte(withoutToolsText), &last) != nil || len(last.LaunchAliases) != 0 || withoutTools.PID == aFirst.PID || preparations.Load() != 3 || cleanups.Load() != 1 {
		t.Fatal("disabled client tools reused the previous launch allowlist")
	}
	if _, err := os.Stat(observations[0].LaunchMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retired tool allowlist still exists")
	}
	if err := syscall.Kill(-bFirst.PID, 0); err != nil {
		t.Fatal("one policy change interrupted an unrelated session")
	}
	observations = append(observations, last)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if cleanups.Load() != 3 || m.Stats().Processes != 0 {
		t.Fatal("manager failed to join both launch lifetimes")
	}
	for _, observation := range observations {
		if !errors.Is(syscall.Kill(-observation.PID, 0), syscall.ESRCH) {
			t.Fatal("prepared ACP group survived manager shutdown")
		}
		for _, path := range []string{observation.LaunchDirectory, observation.RelayConfig} {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("prepared policy or relay configuration survived shutdown")
			}
		}
	}
}

func TestPreparedRelayPolicySurvivesHandoffAndJoinsOnDeliveryOrCancellation(t *testing.T) {
	for _, cancelTurn := range []bool{false, true} {
		name := "delivered"
		if cancelTurn {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			var prepared, cleaned atomic.Int32
			var marker, relayPath string
			d := toolDriver(t, "chat-tools-launch", 2*time.Second, func(cfg *session.Config) {
				cfg.SetupTimeout = 2 * time.Second
				cfg.Process.Limits.RequestTimeout = cfg.TurnTimeout
				pool, err := acppool.New(acppool.Config{Process: cfg.Process, SetupTimeout: cfg.SetupTimeout})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := pool.Close(); err != nil {
						t.Error(err)
					}
				})
				cfg.Pool = pool
				cfg.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
					prepared.Add(1)
					marker, relayPath = filepath.Join(root, "owned-relay.json"), input.RelayConfig
					raw, _ := json.Marshal(map[string]any{"name": "independent-launch-relay", "command": input.RelayExecutable, "args": []string{"relay", "--config", relayPath}, "env": []any{}})
					owned := session.LaunchResources{Directory: root, Args: []string{marker}, RelayAtLaunch: true, Cleanup: func() error { cleaned.Add(1); return os.Remove(marker) }}
					if err := os.WriteFile(marker, raw, 0600); err != nil {
						return owned, err
					}
					return owned, ctx.Err()
				}
			})
			r := toolRequest(t)
			ctx, cancelHTTP := context.WithCancel(t.Context())
			defer cancelHTTP()
			turn, err := d.Start(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			prefix, uses := toolHandoff(t, turn)
			turn.Finish()
			cancelHTTP()
			if d.State() != session.WaitingTools || prepared.Load() != 1 || cleaned.Load() != 0 {
				t.Fatal("HTTP handoff lost launch ownership")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("live relay launch configuration removed early")
			}
			if !cancelTurn {
				next, err := d.Start(t.Context(), followup(t, r, prefix, uses))
				if err != nil {
					t.Fatal(err)
				}
				text, err := collect(t.Context(), next)
				if err != nil || !strings.Contains(text, `"promptCount":1`) {
					t.Fatal("prepared relay resumed a different prompt", err)
				}
				next.Finish()
				if d.State() != session.Idle {
					t.Fatal("delivered prepared turn failed idle transition")
				}
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			if prepared.Load() != 1 || cleaned.Load() != 1 {
				t.Fatal("prepared policy leaked or cleaned repeatedly")
			}
			for _, path := range []string{marker, relayPath} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("prepared tool lifetime left an artifact")
				}
			}
		})
	}
}

func TestFailedLaunchPreparationAlsoRevokesItsSessionRelay(t *testing.T) {
	cfg := managerConfig(t, "pool-normal")
	validator, err := schemacheck.New(schemacheck.Config{Executable: relayBinary, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Close()
	cfg.Session.Validator, cfg.Session.RelayExecutable = validator, relayBinary
	var relayPath string
	var cleaned atomic.Int32
	cfg.Session.PrepareLaunch = func(context.Context, session.LaunchInput) (session.LaunchResources, error) {
		return session.LaunchResources{}, nil
	}
	if _, err := session.New(cfg.Session); !errors.Is(err, acp.ErrParameters) {
		t.Fatal("prepared driver accepted no process-capacity owner")
	}
	cfg.Session.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
		relayPath = input.RelayConfig
		return session.LaunchResources{Cleanup: func() error { cleaned.Add(1); return nil }}, acp.ErrParameters
	}
	m, err := session.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Start(t.Context(), mainRequest(t, "failed")); !errors.Is(err, acp.ErrParameters) {
		t.Fatal("failed launch preparation started a turn")
	}
	if relayPath == "" || cleaned.Load() != 1 || m.Stats().Processes != 0 {
		t.Fatal("partial launch or slot was retained")
	}
	if _, err := os.Stat(relayPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed prepared launch left an authorized relay config")
	}
}
