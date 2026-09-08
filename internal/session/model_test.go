package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/kirofeature"
	"dax-kiro-proxy/internal/session"
)

func TestModelSelectionThenOptionalEffortThenPrompt(t *testing.T) {
	for _, mode := range []string{"chat-models", "chat-effort-reject", "chat-config"} {
		t.Run(mode, func(t *testing.T) {
			d := driver(t, mode)
			models, err := d.Models(context.Background())
			if err != nil || len(models) != 3 {
				t.Fatalf("model discovery: %v", err)
			}
			r := sample(t)
			r.Model = models[1].ID
			r.Effort = "high"
			turn, err := d.Start(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := collect(context.Background(), turn)
			if err != nil {
				t.Fatal(err)
			}
			turn.Finish()
			var result struct {
				Model string   `json:"model"`
				Calls []string `json:"calls"`
			}
			if json.Unmarshal([]byte(raw), &result) != nil || result.Model != "fixture-alternate" || turn.Model() != r.Model {
				t.Fatal("requested model was not used")
			}
			method := "session/set_model"
			if mode == "chat-config" {
				method = "session/set_config_option"
			}
			if strings.Join(result.Calls, ",") != "session/new,"+method+",_kiro.dev/commands/execute,session/prompt" {
				t.Fatalf("unsafe model/effort order: %v", result.Calls)
			}
			status := d.EffortStatus()
			if mode == "chat-effort-reject" {
				if !status.Rejected {
					t.Fatal("effort rejection not recorded")
				}
			} else if status.State != kirofeature.Current {
				t.Fatal("effort not synchronized")
			}
		})
	}
}

func TestConfiguredInitialModelWinsOnlyFirstCompletedTurn(t *testing.T) {
	d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Args: []string{"chat-models"}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "dax-test", Version: "0"}}, TurnTimeout: time.Second, InitialModel: "fixture-alternate", InitialEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	models, err := d.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		r := sample(t)
		r.Model = models[0].ID
		turn, err := d.Start(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := collect(context.Background(), turn)
		if err != nil {
			t.Fatal(err)
		}
		turn.Finish()
		var result struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal([]byte(raw), &result)
		want := "fixture-alternate"
		if i == 1 {
			want = "fixture-backend"
		}
		if result.Model != want {
			t.Fatal("initial selection precedence did not end after first completed turn")
		}
	}
}

func TestBrokenTransportDuringEffortRemainsFatal(t *testing.T) {
	d := driver(t, "chat-effort-corrupt")
	models, err := d.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := sample(t)
	r.Model = models[1].ID
	r.Effort = "high"
	if _, err := d.Start(context.Background(), r); !errors.Is(err, acp.ErrProtocol) {
		t.Fatalf("broken optional transport did not fail: %v", err)
	}
	if d.State() != session.Unstarted {
		t.Fatal("corrupted process retained")
	}
}

func TestSetupAndOwnedTurnHaveIndependentDeadlines(t *testing.T) {
	d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Args: []string{"chat-init-delay"}, Directory: t.TempDir(), ClientInfo: acp.Info{Name: "dax-test", Version: "0"}}, TurnTimeout: 100 * time.Millisecond, SetupTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	turn, err := d.Start(context.Background(), sample(t))
	if err != nil {
		t.Fatalf("turn deadline incorrectly shortened initialization: %v", err)
	}
	if _, err := collect(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	turn.Finish()
}

func TestUnknownModelAndInconsistentSelectionAreRejected(t *testing.T) {
	d := driver(t, "chat-models")
	r := sample(t)
	r.Model = "claude-dax-unavailable"
	if _, err := d.Start(context.Background(), r); err == nil {
		t.Fatal("unavailable model was silently replaced")
	}
	if d.State() != session.Unstarted {
		t.Fatal("invalid model retained fresh state")
	}
	d = driver(t, "chat-model-mismatch")
	models, err := d.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r = sample(t)
	r.Model = models[1].ID
	if _, err := d.Start(context.Background(), r); !errors.Is(err, acp.ErrProtocol) {
		t.Fatalf("contradictory selection acknowledged: %v", err)
	}
}

func TestAutoAndRepeatedRejectedEffortInProcessPath(t *testing.T) {
	d := driver(t, "chat-models")
	models, err := d.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := sample(t)
	r.Model = models[2].ID
	r.Effort = "high"
	turn, err := d.Start(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := collect(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	turn.Finish()
	if strings.Contains(raw, "_kiro.dev/commands/execute") {
		t.Fatal("auto model received effort")
	}
	d = driver(t, "chat-effort-reject")
	models, err = d.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r = sample(t)
	r.Model = models[1].ID
	r.Effort = "high"
	for i := range 2 {
		turn, err = d.Start(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		raw, err = collect(context.Background(), turn)
		if err != nil {
			t.Fatal(err)
		}
		turn.Finish()
		if strings.Contains(raw, "_kiro.dev/commands/execute") != (i == 0) {
			t.Fatal("unknown effort probe repeated across process recreation")
		}
	}
}
