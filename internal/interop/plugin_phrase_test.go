package interop_test

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/inference"
)

func TestPluginResultPhraseRequiresFreshFinalText(t *testing.T) {
	const token = "ABCDEFGHIJKLM"
	const phrase = "VERIFIED " + token
	for _, denied := range []bool{false, true} {
		for _, mode := range []string{"complete", "split", "token-only", "prefix-only", "prompt-echo", "early-token", "early-phrase", "tool-phrase"} {
			t.Run(mode+map[bool]string{false: "-allow", true: "-deny"}[denied], func(t *testing.T) {
				g, fixture := newPluginSequenceFixture(denied)
				g.finalMarker, g.resultSuffix = phrase, token
				fixture.turns[2][0].Text = phrase
				switch mode {
				case "split":
					fixture.turns[2] = []inference.Event{{Kind: inference.Text, Text: "VERI"}, {Kind: inference.Text, Text: "FIED " + token[:5]}, {Kind: inference.Text, Text: token[5:]}, {Kind: inference.End, StopReason: "end_turn"}}
				case "token-only":
					fixture.turns[2][0].Text = token
				case "prefix-only":
					fixture.turns[2][0].Text = "VERIFIED"
				case "prompt-echo":
					fixture.turns[2][0].Text = "Reply exactly VERIFIED Y, using Y from the result."
				case "early-token", "early-phrase":
					early := token
					if mode == "early-phrase" {
						early = phrase
					}
					fixture.turns[0] = append([]inference.Event{{Kind: inference.Text, Text: early[:5]}, {Kind: inference.Text, Text: early[5:]}}, fixture.turns[0]...)
				}
				for stage := range 2 {
					err := consumePluginSequence(t, g, stage, denied)
					if stage == 0 && strings.HasPrefix(mode, "early-") {
						if !errors.Is(err, inference.ErrRequest) || g.snapshot().Uses != 0 {
							t.Fatal("early result disclosure reached a tool")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				request := pluginSequenceRequest(2, denied)
				text := "independent client asset result; Y=" + token
				if denied {
					text = clientDenialReason + "; Y=" + token
				}
				if mode == "tool-phrase" {
					text += "; " + phrase
				}
				raw, _ := json.Marshal(map[string]any{"type": "tool_result", "tool_use_id": "plugin-use", "is_error": denied, "content": text})
				request.Messages[0].Content[0].Raw = raw
				turn, err := g.Start(t.Context(), request)
				if mode == "tool-phrase" {
					if !errors.Is(err, inference.ErrRequest) || fixture.starts != 2 {
						t.Fatal("whole phrase from tool/hook was accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				for {
					_, err = turn.Next(t.Context())
					if err != nil {
						break
					}
				}
				if mode == "complete" || mode == "split" {
					if !errors.Is(err, io.EOF) || !g.snapshot().FinalMarker || g.snapshot().Completions != 1 {
						t.Fatal("final fresh-result phrase missing", err)
					}
					turn.Finish()
				} else {
					turn.Cancel()
					if !errors.Is(err, inference.ErrRequest) || g.snapshot().FinalMarker || g.snapshot().Completions != 0 {
						t.Fatal("partial or echoed wording accepted")
					}
				}
			})
		}
	}
}
