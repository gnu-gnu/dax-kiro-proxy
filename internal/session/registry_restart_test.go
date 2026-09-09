package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/acppool"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/session"
)

func registryHandoff(t *testing.T, ctx context.Context, d *session.Driver, r *anthropic.Request) (*anthropic.Request, int) {
	t.Helper()
	turn, err := d.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	text, calls := toolHandoff(t, turn)
	if _, err := d.Start(t.Context(), followup(t, r, text, calls)); !errors.Is(err, inference.ErrBusy) {
		t.Fatal("undelivered result admitted")
	}
	turn.Finish()
	var pid int
	if n, _ := fmt.Sscanf(text, "first process %d", &pid); n != 1 || pid <= 1 {
		t.Fatal("missing owned process")
	}
	return followup(t, r, text, calls), pid
}

func registrySequence(t *testing.T, ctx context.Context, options ...func(*session.Config)) (*session.Driver, *anthropic.Request, int, int) {
	t.Helper()
	options = append([]func(*session.Config){func(c *session.Config) { c.TurnTimeout = 10 * time.Second }}, options...)
	d := toolDriver(t, "chat-tools-system-repeat", 10*time.Second, options...)
	r := toolRequest(t)
	r.Messages = append(r.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Independent original instruction."}}})
	next, first := registryHandoff(t, ctx, d, r)
	next.Tools = []json.RawMessage{json.RawMessage(strings.Replace(string(r.Tools[0]), "client_action", "client_next", 1))}
	next.Messages = append(next.Messages, anthropic.Message{Role: "system", Content: []anthropic.Block{{Type: "text", Text: "Independent updated instruction."}}})
	last, second := registryHandoff(t, t.Context(), d, next)
	last.Messages = append(last.Messages, next.Messages[4])
	if first == second || !errors.Is(syscall.Kill(-first, 0), syscall.ESRCH) || !strings.Contains(string(last.Messages[5].Content[1].Raw), "client_next") {
		t.Fatal("registry replacement did not join old ownership or expose the new tool")
	}
	return d, last, first, second
}

func TestRegistryReplacementRetainsExactHistoryAndResumesFollowingTool(t *testing.T) {
	d, last, _, second := registrySequence(t, t.Context())
	for _, mutate := range []func(*anthropic.Request){
		func(r *anthropic.Request) { r.Identity.Session = "other-owner" },
		func(r *anthropic.Request) { r.Model = "claude-dax-wrong" },
		func(r *anthropic.Request) { r.Effort = "high" },
		func(r *anthropic.Request) { r.System = []anthropic.Block{{Type: "text", Text: "Changed policy"}} },
		func(r *anthropic.Request) {
			r.Extra = map[string]json.RawMessage{"metadata": json.RawMessage(`{"owner":"changed"}`)}
		},
		func(r *anthropic.Request) {
			r.Tools = []json.RawMessage{json.RawMessage(`{"name":"bad","input_schema":{"type":"array"}}`)}
		},
		func(r *anthropic.Request) { r.Messages[0].Content[0].Text = "changed question" },
		func(r *anthropic.Request) {
			r.Tools = []json.RawMessage{json.RawMessage(strings.Replace(string(r.Tools[0]), "client_next", "client_third", 1))}
			r.Messages = r.Messages[2:]
		},
		func(r *anthropic.Request) {
			r.Messages[3].Content[0].Raw = json.RawMessage(`{"type":"tool_result","tool_use_id":"wrong","content":"altered"}`)
		},
		func(r *anthropic.Request) { r.Messages[6].Content[0] = r.Messages[3].Content[0] },
		func(r *anthropic.Request) {
			r.Messages[6].Content = append(r.Messages[6].Content, r.Messages[6].Content[0])
		},
		func(r *anthropic.Request) {
			// Duplicate response identities can make a client regroup its earlier messages.
			// This unproven history still rejects, including with a changed registry.
			a := append(slices.Clone(r.Messages[2].Content), r.Messages[5].Content...)
			u := append(slices.Clone(r.Messages[3].Content), r.Messages[6].Content...)
			r.Messages = append(slices.Clone(r.Messages[:2]), anthropic.Message{Role: "assistant", Content: a}, anthropic.Message{Role: "user", Content: u}, r.Messages[7])
		},
		func(r *anthropic.Request) {
			result, err := anthropic.DecodeToolResult(r.Messages[6].Content[0].Raw)
			if err != nil {
				t.Fatal(err)
			}
			r.Messages[6].Content[0].Raw, _ = json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": result.ID, "content": strings.Repeat("x", relay.MaxFrameBytes)})
		},
	} {
		bad := cloneInterruptionRequest(last)
		mutate(bad)
		if _, err := d.Start(t.Context(), bad); !errors.Is(err, inference.ErrRequest) {
			t.Fatal("invalid registry continuation accepted", err)
		}
		if d.State() != session.WaitingTools || syscall.Kill(second, 0) != nil {
			t.Fatal("invalid candidate changed pending ownership")
		}
	}
	turn, err := d.Start(t.Context(), last)
	if err != nil {
		t.Fatal(err)
	}
	text, err := collect(t.Context(), turn)
	if err != nil || !strings.Contains(text, `"promptCount":1`) || !strings.Contains(text, "synthetic tool result") {
		turn.Cancel()
		t.Fatal("following tool did not resume the replacement prompt", err)
	}
	turn.Finish()
	if d.State() != session.Idle || syscall.Kill(second, 0) != nil {
		t.Fatal("compatible continuation replaced its healthy process")
	}
	if _, err := d.Start(t.Context(), last); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("completed result was replayed")
	}
}

func TestRegistryReplacementRetainsOriginalDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	d, last, _, second := registrySequence(t, ctx)
	for d.State() != session.Unstarted && time.Now().Before(deadline.Add(time.Second)) {
		time.Sleep(5 * time.Millisecond)
	}
	if d.State() != session.Unstarted || time.Now().After(deadline.Add(700*time.Millisecond)) || !errors.Is(syscall.Kill(-second, 0), syscall.ESRCH) {
		t.Fatal("registry replacement extended deadline or retained process")
	}
	bad := cloneInterruptionRequest(last)
	bad.Identity.Session = "wrong-owner"
	if _, err := d.Start(t.Context(), bad); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("terminal outcome crossed owners", err)
	}
	// Either the turn owner or its ACP request can observe the same original deadline first.
	if _, err := d.Start(t.Context(), last); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, acp.ErrTimeout) {
		t.Fatal("matching history lost its timeout", err)
	}
	if _, err := d.Start(t.Context(), last); !errors.Is(err, inference.ErrRequest) {
		t.Fatal("terminal outcome replayed", err)
	}
}

func TestRegistryRecreationBoundPreservesPendingBatch(t *testing.T) {
	d, last, _, second := registrySequence(t, t.Context(), func(c *session.Config) { c.MaxRecreations = 1 })
	changed := cloneInterruptionRequest(last)
	changed.Tools = []json.RawMessage{json.RawMessage(strings.Replace(string(last.Tools[0]), "client_next", "client_third", 1))}
	if _, err := d.Start(t.Context(), changed); !errors.Is(err, inference.ErrRequest) || d.State() != session.WaitingTools || syscall.Kill(second, 0) != nil {
		t.Fatal("restart limit changed pending ownership", err)
	}
	turn, err := d.Start(t.Context(), last)
	if err != nil {
		t.Fatal("restart limit broke compatible continuation", err)
	}
	if _, err := collect(t.Context(), turn); err != nil {
		turn.Cancel()
		t.Fatal(err)
	}
	turn.Finish()
	if d.Close() != nil || !errors.Is(syscall.Kill(-second, 0), syscall.ESRCH) {
		t.Fatal("limited continuation cleanup failed")
	}
}

func TestRegistryReplacementWithoutChangedStandingInstructions(t *testing.T) {
	d := toolDriver(t, "chat-tools-system-repeat", 5*time.Second, func(c *session.Config) { c.TurnTimeout = 5 * time.Second })
	r := toolRequest(t)
	next, first := registryHandoff(t, t.Context(), d, r)
	next.Tools = []json.RawMessage{json.RawMessage(strings.Replace(string(r.Tools[0]), "client_action", "client_next", 1))}
	last, second := registryHandoff(t, t.Context(), d, next)
	if first == second || !errors.Is(syscall.Kill(-first, 0), syscall.ESRCH) {
		t.Fatal("registry-only change did not recreate")
	}
	turn, err := d.Start(t.Context(), last)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := collect(t.Context(), turn); err != nil || !strings.Contains(text, `"promptCount":1`) {
		turn.Cancel()
		t.Fatal("registry-only continuation failed", err)
	}
	turn.Finish()
}

func TestRegistryRecreationLimitRejectsUnboundedConfiguration(t *testing.T) {
	for _, limit := range []int{-1, 65} {
		if d, err := session.New(session.Config{MaxRecreations: limit}); err == nil {
			d.Close()
			t.Fatal("invalid recreation bound accepted")
		}
	}
}

func TestRepeatedRegistryReplacementUsesFreshPreparedPolicies(t *testing.T) {
	root := t.TempDir()
	var prepared, cleaned atomic.Int32
	var paths []string
	d, last, _, second := registrySequence(t, t.Context(), func(c *session.Config) {
		c.SetupTimeout = 2 * time.Second
		c.Process.Limits.RequestTimeout = c.TurnTimeout
		pool, err := acppool.New(acppool.Config{Process: c.Process, SetupTimeout: c.SetupTimeout})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := pool.Close(); err != nil {
				t.Error(err)
			}
		})
		c.Pool = pool
		c.PrepareLaunch = func(ctx context.Context, input session.LaunchInput) (session.LaunchResources, error) {
			n := prepared.Add(1)
			names := []string{"client_action", "client_next", "client_third"}
			if n < 1 || n > 3 || cleaned.Load() != n-1 || input.Registry == nil || len(input.Registry.Tools()) != 1 || input.Registry.Tools()[0].Name != names[n-1] {
				return session.LaunchResources{}, errors.New("old policy not joined or wrong registry")
			}
			path, err := os.MkdirTemp(root, "owned-policy-")
			if err != nil {
				return session.LaunchResources{}, err
			}
			paths = append(paths, path, input.RelayConfig)
			return session.LaunchResources{Directory: path, Cleanup: func() error { cleaned.Add(1); return os.RemoveAll(path) }}, ctx.Err()
		}
	})
	last.Tools = []json.RawMessage{json.RawMessage(strings.Replace(string(last.Tools[0]), "client_next", "client_third", 1))}
	final, third := registryHandoff(t, t.Context(), d, last)
	final.Messages = append(final.Messages, last.Messages[len(last.Messages)-1])
	if second == third || !errors.Is(syscall.Kill(-second, 0), syscall.ESRCH) || prepared.Load() != 3 || cleaned.Load() != 2 {
		t.Fatal("repeated registry replacement did not join prior policy")
	}
	turn, err := d.Start(t.Context(), final)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t.Context(), turn); err != nil {
		turn.Cancel()
		t.Fatal(err)
	}
	turn.Finish()
	if d.Close() != nil || cleaned.Load() != 3 || !errors.Is(syscall.Kill(-third, 0), syscall.ESRCH) {
		t.Fatal("prepared replacement cleanup failed")
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("prepared artifact survived")
		}
	}
}
