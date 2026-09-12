package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type nativeSoakLane struct{ Seed, Directory string }
type nativeSoakPlan struct {
	Rounds     int
	LifetimeMS int64
	Witness    string
	Lanes      [2]nativeSoakLane
}
type nativeSoakWitness struct {
	PID, Peer, Lane, Prompts, Calls, Results int
	Config                                   string
}

func nativeSoakLifetime(ms int64) (time.Duration, bool) {
	if ms == 0 {
		return 8 * time.Minute, true
	}
	if ms < 480000 || ms > 2400000 {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

func validNativeSoakResult(raw []byte, id int, lane nativeSoakLane, foreign string, round int, denied bool) bool {
	var r struct {
		JSONRPC string
		ID      int
		Error   json.RawMessage
		Result  struct {
			IsError bool
			Content []struct{ Type, Text string }
		}
	}
	if len(raw) > 64<<10 || json.Unmarshal(raw, &r) != nil || r.JSONRPC != "2.0" || r.ID != id || len(r.Error) != 0 || r.Result.IsError != denied || len(r.Result.Content) < 1 || len(r.Result.Content) > 16 {
		return false
	}
	var text strings.Builder
	for _, b := range r.Result.Content {
		if b.Type != "text" || text.Len()+len(b.Text) > 64<<10 {
			return false
		}
		text.WriteString(b.Text)
	}
	if strings.Contains(text.String(), foreign) {
		return false
	}
	if denied {
		return !strings.Contains(text.String(), lane.Seed) && strings.Contains(text.String(), "independent native refusal")
	}
	return strings.Count(text.String(), lane.Seed+"_VALUE_"+strconv.Itoa(round)+"_END") == 1
}

// This peer reads only its test plan and requests client Read operations through the supplied
// MCP relay. It never opens a requested file, a native transcript or the relay configuration.
func nativeToolSoakFixture() {
	started := time.Now()
	timer := time.AfterFunc(40*time.Minute, func() { os.Exit(88) })
	defer timer.Stop()
	if len(os.Args) != 3 {
		return
	}
	file, err := os.Open(os.Args[2])
	if err != nil {
		return
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	closeErr := file.Close()
	var plan nativeSoakPlan
	if readErr != nil || closeErr != nil || len(data) > 64<<10 || json.Unmarshal(data, &plan) != nil || plan.Rounds < 8 || plan.Rounds > 128 || !filepath.IsAbs(plan.Witness) {
		return
	}
	lifetime, ok := nativeSoakLifetime(plan.LifetimeMS)
	if !ok || time.Since(started) >= lifetime {
		return
	}
	timer.Reset(time.Until(started.Add(lifetime)))
	for _, lane := range plan.Lanes {
		if len(lane.Seed) < 8 || len(lane.Seed) > 128 || !filepath.IsAbs(lane.Directory) {
			return
		}
	}
	if plan.Lanes[0].Seed == plan.Lanes[1].Seed {
		return
	}
	var child *fixtureRelay
	var joined chan struct{}
	defer func() {
		if child == nil {
			return
		}
		_ = child.input.Close()
		select {
		case <-joined:
		case <-time.After(time.Second):
			_ = child.cmd.Process.Kill()
			<-joined
		}
	}()
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 8<<20)
	frames, prompted := 0, false
	const sessionID = "independent-native-tool-soak"
	for input.Scan() {
		frames++
		var q request
		if frames > 16 || json.Unmarshal(input.Bytes(), &q) != nil {
			return
		}
		switch q.Method {
		case "initialize":
			reply(q.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}})
		case "session/new":
			var p struct {
				CWD string
				MCP []json.RawMessage `json:"mcpServers"`
			}
			if child != nil || json.Unmarshal(q.Params, &p) != nil || p.CWD == "" || len(p.MCP) != 1 {
				return
			}
			child = startFixtureRelayNamed(p.MCP[0], p.CWD, false, "Read")
			child.output = bufio.NewReaderSize(child.output, (64<<10)+1)
			joined = make(chan struct{})
			go func() { _ = child.cmd.Wait(); close(joined) }()
			reply(q.ID, map[string]any{"sessionId": sessionID, "models": map[string]any{"currentModelId": "fixture-backend", "availableModels": []any{map[string]string{"modelId": "fixture-backend", "name": "Independent native tools"}}}})
		case "session/set_model":
			reply(q.ID, map[string]any{})
		case "session/prompt":
			var p struct {
				SessionID string
				Prompt    []struct{ Type, Text string }
			}
			if child == nil || prompted || json.Unmarshal(q.Params, &p) != nil || p.SessionID != sessionID || len(p.Prompt) == 0 || len(p.Prompt) > 128 {
				return
			}
			prompted = true
			var projected strings.Builder
			for _, part := range p.Prompt {
				if part.Type != "text" || projected.Len()+len(part.Text) > 8<<20 {
					return
				}
				projected.WriteString(part.Text)
			}
			lane := -1
			for i, choice := range plan.Lanes {
				if strings.Contains(projected.String(), choice.Seed) {
					if lane != -1 {
						return
					}
					lane = i
				}
			}
			if lane == -1 || len(child.cmd.Args) != 4 {
				return
			}
			w := nativeSoakWitness{PID: os.Getpid(), Peer: child.cmd.Process.Pid, Lane: lane, Prompts: 1, Config: child.cmd.Args[3]}
			path := filepath.Join(plan.Witness, "owner-"+strconv.Itoa(lane)+".json")
			publish := func() bool {
				encoded, err := json.Marshal(w)
				if err != nil || len(encoded) > 4096 {
					return false
				}
				tmp := path + ".next"
				return os.WriteFile(tmp, encoded, 0600) == nil && os.Rename(tmp, path) == nil
			}
			if !publish() {
				return
			}
			for round := 1; round <= plan.Rounds; round++ {
				w.Calls = round
				if !publish() {
					return
				}
				id := 100 + round
				name := "read-" + fmtSoakIndex(round)
				child.send(id, "tools/call", map[string]any{"name": child.alias, "arguments": map[string]string{"file_path": filepath.Join(plan.Lanes[lane].Directory, name)}})
				line, err := child.output.ReadSlice('\n')
				if err != nil || !validNativeSoakResult(line, id, plan.Lanes[lane], plan.Lanes[1-lane].Seed, round, lane == 1) {
					return
				}
				w.Results = round
				if !publish() {
					return
				}
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "Native tool sequence complete: " + plan.Lanes[lane].Seed}}}})
			reply(q.ID, map[string]any{"stopReason": "end_turn"})
		case "session/cancel":
			return
		default:
			return
		}
	}
}

func fmtSoakIndex(n int) string {
	s := strconv.Itoa(n)
	return strings.Repeat("0", 3-len(s)) + s
}
