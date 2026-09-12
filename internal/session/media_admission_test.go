package session_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/session"
)

type mediaPromptFacts struct {
	PID, Prompts int
	Parts        []struct {
		Type, MIME, Digest string
		Bytes              int
	}
}

func boundedMediaDriver(t *testing.T) (*session.Driver, string) {
	t.Helper()
	dir := t.TempDir()
	witness := filepath.Join(dir, "owned-media-events")
	d, err := session.New(session.Config{Process: acp.Config{Executable: fixture, Args: []string{"media-history-peer", witness}, Directory: dir, ClientInfo: acp.Info{Name: "owned-media-control", Version: "1"}}, TurnTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal("cannot prepare independent media driver")
	}
	t.Cleanup(func() {
		if d.Close() != nil {
			t.Error("media driver cleanup failed")
		}
	})
	return d, witness
}

func decodeMediaHistory(t *testing.T, messages []any) *anthropic.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{"model": fixtureClientID, "max_tokens": 32, "messages": messages})
	if err != nil || len(body) > anthropic.MaxBodyBytes {
		t.Fatal("independent history exceeds its HTTP bound")
	}
	r, err := anthropic.DecodeRequest(body)
	if err != nil {
		t.Fatalf("bounded complete media history rejected: messages=%d bytes=%d", len(messages), len(body))
	}
	return r
}

func mediaTurn(t *testing.T, d *session.Driver, r *anthropic.Request) (mediaPromptFacts, string) {
	t.Helper()
	turn, err := d.Start(t.Context(), r)
	if err != nil {
		t.Fatal("media turn failed before dispatch")
	}
	defer turn.Cancel()
	text, err := collect(t.Context(), turn)
	if err != nil {
		t.Fatal("independent media turn failed")
	}
	turn.Finish()
	var facts mediaPromptFacts
	if json.Unmarshal([]byte(text), &facts) != nil || facts.PID <= 1 || facts.Prompts < 1 {
		t.Fatal("invalid fixed media observation")
	}
	return facts, text
}

func checkMediaOwners(t *testing.T, d *session.Driver, witness string, wantPrompts int) {
	t.Helper()
	if d.Close() != nil {
		t.Fatal("media owner cleanup failed")
	}
	data, err := os.ReadFile(witness)
	if err != nil || len(data) > 16384 {
		t.Fatal("missing bounded media ownership facts")
	}
	owners := map[int]int{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var fact struct{ PID, Prompts int }
		if json.Unmarshal(line, &fact) != nil || fact.PID <= 1 || fact.Prompts < 0 || fact.Prompts > 40 {
			t.Fatal("invalid owned media facts")
		}
		if old, ok := owners[fact.PID]; ok && fact.Prompts != old+1 || !ok && fact.Prompts != 0 {
			t.Fatal("media prompt ownership was reordered")
		}
		owners[fact.PID] = fact.Prompts
	}
	total := 0
	for pid, prompts := range owners {
		total += prompts
		if !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			t.Fatal("owned media process group survived cleanup")
		}
	}
	if total != wantPrompts {
		t.Fatalf("unexpected dispatched media prompts: got=%d want=%d", total, wantPrompts)
	}
}

func TestMediaHistoryBeyondDispatchLimitsKeepsProvenDeltas(t *testing.T) {
	for _, kind := range []string{"image-count", "document-bytes"} {
		t.Run(kind, func(t *testing.T) {
			d, witness := boundedMediaDriver(t)
			var block any
			var payload, partType string
			turns := 21
			if kind == "image-count" {
				b := mediaRequest(t).Messages[0].Content[0]
				block = json.RawMessage(b.Raw)
				m, _ := b.Media()
				payload, partType = m.Data, "image"
			} else {
				text := strings.Repeat("x", 3<<20)
				block = map[string]any{"type": "document", "source": map[string]string{"type": "text", "media_type": "text/plain", "data": text}}
				payload = `{"type":"document","text":"` + text + `"}`
				partType, turns = "text", 3
			}
			digest := sha256.Sum256([]byte(payload))
			var messages []any
			owner := 0
			for i := 0; i < turns; i++ {
				messages = append(messages, map[string]any{"role": "user", "content": []any{block}})
				facts, reply := mediaTurn(t, d, decodeMediaHistory(t, messages))
				if owner == 0 {
					owner = facts.PID
				}
				matches, images := 0, 0
				for _, part := range facts.Parts {
					if part.Type == "image" {
						images++
					}
					if part.Type == partType && part.Bytes == len(payload) && part.Digest == hex.EncodeToString(digest[:]) {
						matches++
					}
				}
				if facts.PID != owner || facts.Prompts != i+1 || matches != 1 || partType == "image" && images != 1 {
					t.Fatal("committed media replayed, changed or lost owner continuity")
				}
				messages = append(messages, map[string]any{"role": "assistant", "content": reply})
			}
			const next = "New text question."
			messages = append(messages, map[string]any{"role": "user", "content": next})
			r := decodeMediaHistory(t, messages)
			facts, _ := mediaTurn(t, d, r)
			lastDigest := sha256.Sum256([]byte(next))
			if facts.PID != owner || facts.Prompts != turns+1 || len(facts.Parts) != 1 || facts.Parts[0].Type != "text" || facts.Parts[0].Digest != hex.EncodeToString(lastDigest[:]) {
				t.Fatal("historical media blocked or contaminated the new text delta")
			}
			checkMediaOwners(t, d, witness, turns+1)
			fresh, freshWitness := boundedMediaDriver(t)
			if _, err := fresh.Start(t.Context(), r); !errors.Is(err, inference.ErrRequest) {
				t.Fatal("oversized full reconstruction dispatched or silently dropped history")
			}
			checkMediaOwners(t, fresh, freshWitness, 0)
			t.Logf("completed_media_turns=%d text_delta=true owner_reused=true full_reconstruction_rejected=true all_groups_joined=true", turns)
		})
	}
}
