package session_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/kiroauth"
	"dax-kiro-proxy/internal/session"
)

var fixture string
var relayBinary string
var fixtureClientID string

func TestMain(m *testing.M) {
	digest := sha256.Sum256([]byte("fixture-backend"))
	fixtureClientID = fmt.Sprintf("claude-dax-fixture-backend-%x", digest[:8])
	dir, err := os.MkdirTemp("", "dax-session-fixture-")
	if err != nil {
		panic(err)
	}
	fixture = filepath.Join(dir, "fake-acp")
	cmd := exec.Command("go", "build", "-o", fixture, "../acp/testdata/fake")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	relayBinary = filepath.Join(dir, "dax-kiro-proxy")
	cmd = exec.Command("go", "build", "-o", relayBinary, "../../cmd/dax-kiro-proxy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Run() != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
func driver(t *testing.T, mode string) *session.Driver {
	t.Helper()
	d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Args: []string{mode}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "dax-test", Version: "0"}, Auth: kiroauth.Classifier{}, Limits: acp.Limits{GracePeriod: 50 * time.Millisecond, TermPeriod: 50 * time.Millisecond, KillPeriod: time.Second}}, TurnTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}
func sample(t *testing.T) *anthropic.Request {
	t.Helper()
	r, err := anthropic.DecodeRequest([]byte(strings.ReplaceAll(`{"model":"claude-dax-fixture","max_tokens":128,"system":[{"type":"text","text":"context A"},{"type":"text","text":"context B"}],"messages":[{"role":"user","content":"earlier"},{"role":"assistant","content":"previous reply"},{"role":"user","content":[{"type":"text","text":"latest A"},{"type":"text","text":"latest B"}]}]}`, "claude-dax-fixture", fixtureClientID)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func collect(ctx context.Context, turn inference.Turn) (string, error) {
	var text strings.Builder
	for {
		e, err := turn.Next(ctx)
		if err != nil {
			return text.String(), err
		}
		if e.Kind == inference.End {
			if e.StopReason != "end_turn" {
				return text.String(), errors.New("unexpected stop")
			}
			return text.String(), nil
		}
		if e.Kind != inference.Text {
			return text.String(), errors.New("unexpected event")
		}
		text.WriteString(e.Text)
	}
}

func TestPublicTextPathAndOrderedCompletion(t *testing.T) {
	for _, mode := range []string{"chat", "chat-order"} {
		t.Run(mode, func(t *testing.T) {
			d := driver(t, mode)
			turn, err := d.Start(context.Background(), sample(t))
			if err != nil {
				t.Fatal(err)
			}
			// Let the reader observe the final RPC reply before the HTTP consumer drains text.
			time.Sleep(25 * time.Millisecond)
			text, err := collect(context.Background(), turn)
			if err != nil {
				t.Fatal(err)
			}
			want := "birch stone"
			if mode == "chat-order" {
				want = ""
				for i := range 32 {
					want += fmt.Sprintf("%d,", i)
				}
			}
			if text != want {
				t.Fatalf("response notifications lost or reordered: %q", text)
			}
			if _, err := turn.Next(context.Background()); !errors.Is(err, io.EOF) {
				t.Fatal("completed turn emitted another result")
			}
			turn.Finish()
			if d.State() != session.Idle {
				t.Fatal("successful delivery not idle")
			}
		})
	}
}

func TestOrderedPromptProjection(t *testing.T) {
	d := driver(t, "chat-project")
	turn, err := d.Start(context.Background(), sample(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := collect(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	turn.Finish()
	var p struct {
		Prompt []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if json.Unmarshal([]byte(raw), &p) != nil {
		t.Fatal("invalid synthetic capture")
	}
	if len(p.Prompt) != 5 {
		t.Fatalf("unexpected block count %d", len(p.Prompt))
	}
	var contextData struct {
		System  []string `json:"system"`
		History []struct {
			Role    string   `json:"role"`
			Content []string `json:"content"`
		} `json:"history"`
	}
	if p.Prompt[0].Text != "Conversation context follows as JSON; preserve its role and content order." || json.Unmarshal([]byte(p.Prompt[1].Text), &contextData) != nil || p.Prompt[2].Text != "Current user content follows." {
		t.Fatal("context projection framing")
	}
	if strings.Join(contextData.System, "|") != "context A|context B" || len(contextData.History) != 2 || contextData.History[0].Role != "user" || contextData.History[1].Content[0] != "previous reply" || p.Prompt[3].Text != "latest A" || p.Prompt[4].Text != "latest B" {
		t.Fatal("prompt order/role lost")
	}
}

func TestUnsafeStateIsDiscarded(t *testing.T) {
	for _, mode := range []string{"chat-no-id", "chat-wrong-session", "chat-cancelled", "chat-bad-stop", "chat-auth"} {
		t.Run(mode, func(t *testing.T) {
			d := driver(t, mode)
			turn, err := d.Start(context.Background(), sample(t))
			if err == nil {
				_, err = collect(context.Background(), turn)
				turn.Cancel()
			}
			if err == nil {
				t.Fatal("ambiguous turn succeeded")
			}
			if mode == "chat-auth" && !errors.Is(err, acp.ErrAuthentication) {
				t.Fatal("auth classification lost")
			}
			if d.State() != session.Unstarted {
				t.Fatalf("failed state retained: %v", d.State())
			}
		})
	}
}

func TestCancellationAndSingleSessionAdmission(t *testing.T) {
	d := driver(t, "chat-slow")
	turn, err := d.Start(context.Background(), sample(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Start(context.Background(), sample(t)); !errors.Is(err, session.ErrBusy) {
		t.Fatal("concurrent prompt admitted on one session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := collect(ctx, turn); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	var callers sync.WaitGroup
	for range 8 {
		callers.Go(turn.Cancel)
	}
	callers.Wait()
	if d.State() != session.Unstarted {
		t.Fatal("cancelled session reused")
	}
}

func TestFreshSnapshotCannotAccumulateUnrelatedHistory(t *testing.T) {
	d := driver(t, "chat")
	r := sample(t)
	for range 2 {
		turn, err := d.Start(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := collect(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
		turn.Finish()
	}
}
