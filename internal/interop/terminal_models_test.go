package interop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

func terminalModelPlan(c *catalog.Catalog) ([2]string, []string, string, error) {
	var ids [2]string
	if c == nil || len(c.Models()) < 2 || c.Current() == "" {
		return ids, nil, "", errors.New("model control needs an advertised pair")
	}
	ids[0] = c.Current()
	seen := make(map[string]bool)
	var labels []string
	var target string
	for _, model := range c.List() {
		label := strings.ToLower(model.Name)
		if label == "" || strings.ContainsAny(label, "\r\n\t") || seen[label] {
			return ids, nil, "", errors.New("ambiguous model picker labels")
		}
		for prior := range seen {
			if strings.Contains(prior, label) || strings.Contains(label, prior) {
				return ids, nil, "", errors.New("overlapping picker labels")
			}
		}
		seen[label] = true
		labels = append(labels, label)
		backend, err := c.Resolve(model.ID)
		if err != nil {
			return ids, nil, "", errors.New("model alias not reversible")
		}
		if ids[1] == "" && backend.ID != ids[0] {
			ids[1], target = backend.ID, label
		}
	}
	if ids[1] == "" {
		return ids, nil, "", errors.New("missing second advertised model")
	}
	return ids, labels, target, nil
}

// The client paints a picker frame across several terminal writes, and a partially painted row
// shows the selection glyph before its label. The observer acts only on a frame that has stayed
// unchanged for at least one idle observation (the reconstructed-screen observer ticks every 100ms).
const terminalMenuSettle = 100 * time.Millisecond

type terminalModelMenu struct {
	labels                             []string
	target, last, direction, frame     string
	seen, focused                      map[string]bool
	at, frameAt                        time.Time
	moves, reversals                   int
	unadvertisedRows                   int
	lastHeader, lastFooter             bool
	lastGlyphs, lastKnown, lastVisible int
}

func (p *terminalModelMenu) allSeen() bool { return len(p.labels) > 0 && len(p.seen) == len(p.labels) }
func (p *terminalModelMenu) covered() bool { return p.allSeen() && len(p.focused) == len(p.labels) }

func (p *terminalModelMenu) next(screen string, now time.Time) string {
	lower := strings.ToLower(screen)
	header := strings.Index(lower, "select model")
	p.lastHeader, p.lastFooter = header >= 0, strings.Contains(lower, "esc to cancel")
	if header >= 0 {
		lower = lower[header:]
	}
	selected, selectedRow, glyphs, known := "", "", 0, 0
	p.lastVisible = 0
	for _, label := range p.labels {
		if strings.Contains(lower, label) {
			p.lastVisible++
		}
	}
	for _, line := range strings.Split(lower, "\n") {
		if !strings.Contains(line, "❯") {
			continue
		}
		glyphs++
		selectedRow = line
		for _, label := range p.labels {
			if strings.Contains(line, label) {
				known++
				selected = label
			}
		}
	}
	p.lastGlyphs, p.lastKnown = glyphs, known
	if lower != p.frame {
		p.frame, p.frameAt = lower, now
		return ""
	}
	if now.Sub(p.frameAt) < terminalMenuSettle {
		return ""
	}
	if p.moves >= 3*len(p.labels)+4 || header < 0 || glyphs != 1 || known > 1 {
		return ""
	}
	if known == 0 {
		selected = "\x00" + selectedRow
		p.unadvertisedRows++
	}
	if p.seen == nil {
		p.seen, p.focused = make(map[string]bool), make(map[string]bool)
	}
	for _, label := range p.labels {
		if strings.Contains(lower, label) {
			p.seen[label] = true
		}
	}
	if known == 1 {
		p.focused[selected] = true
	}
	key := p.direction
	if key == "" {
		key = "\x1b[B"
	}
	if p.covered() {
		if selected == p.target {
			key = "\r"
		} else if visible, _ := terminalModelPickerKey(lower, p.target); visible != "" {
			key = visible
		}
	}
	if key != "\r" && selected == p.last {
		if now.Sub(p.at) < 750*time.Millisecond || p.reversals >= 2 {
			return ""
		}
		p.reversals++
		if p.direction == "\x1b[B" {
			key = "\x1b[A"
		} else {
			key = "\x1b[B"
		}
	}
	p.moves++
	p.last, p.direction, p.at = selected, key, now
	return key
}

func TestTerminalModelPlanRequiresDistinctExactAdvertisedModels(t *testing.T) {
	for _, tc := range []struct {
		name    string
		models  []catalog.Backend
		current string
		valid   bool
	}{
		{"two", []catalog.Backend{{ID: "fixture-z", Name: "First"}, {ID: "fixture-a", Name: "Second"}}, "fixture-z", true},
		{"default-later", []catalog.Backend{{ID: "fixture-a", Name: "Second"}, {ID: "fixture-z", Name: "First"}}, "fixture-z", true},
		{"single", []catalog.Backend{{ID: "fixture-z", Name: "First"}}, "fixture-z", false},
		{"ambiguous-names", []catalog.Backend{{ID: "fixture-a", Name: "Same"}, {ID: "fixture-z", Name: "Same"}}, "fixture-z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := catalog.New(tc.models, tc.current)
			if err != nil {
				t.Fatal("fixture")
			}
			ids, labels, target, err := terminalModelPlan(c)
			if (err == nil) != tc.valid {
				t.Fatal("invalid model plan admitted")
			}
			if tc.valid && (ids[0] != "fixture-z" || ids[1] != "fixture-a" || len(labels) != 2 || target != "second (kiro)") {
				t.Fatal("model plan substituted a catalog identity")
			}
		})
	}
	if _, _, _, err := terminalModelPlan(nil); err == nil {
		t.Fatal("absent catalog admitted")
	}
}

func TestModelMenuTracksLabelsAcrossScrollingAndBoundsStalledKeys(t *testing.T) {
	p := terminalModelMenu{labels: []string{"first (kiro)", "middle (kiro)", "last (kiro)"}, target: "first (kiro)"}
	at := time.Unix(10, 0)
	// Each frame is observed twice: as it arrives and again one settle interval later. A changed
	// frame yields no key until it settles; an unchanged frame is bounded by the hold and reversals.
	for _, tc := range []struct {
		screen, arrived, settled string
		after                    time.Duration
	}{
		{"Select model\n middle (Kiro)\n❯ last (Kiro)", "", "\x1b[B", 0},
		{"Select model\n middle (Kiro)\n❯ last (Kiro)", "", "", 300 * time.Millisecond},
		{"Select model\n middle (Kiro)\n❯ last (Kiro)", "\x1b[A", "", time.Second},
		{"Select model\n first (Kiro)\n❯ middle (Kiro)", "", "\x1b[A", 2 * time.Second},
		{"Select model\n❯ first (Kiro)\n middle (Kiro)", "", "\r", 3 * time.Second},
	} {
		if key := p.next(tc.screen, at.Add(tc.after)); key != tc.arrived {
			t.Fatal("unsettled or unchanged-frame control differs")
		}
		if key := p.next(tc.screen, at.Add(tc.after+terminalMenuSettle)); key != tc.settled {
			t.Fatal("scrolling or settled-frame control differs")
		}
	}
	if len(p.seen) != 3 || p.moves != 4 || p.reversals != 1 {
		t.Fatal("catalog visibility and action bounds missing")
	}
	for _, screen := range []string{"❯ first (Kiro)", "Select model\n first (Kiro)", "Select model\n❯ first (Kiro)\n❯ middle (Kiro)"} {
		q := terminalModelMenu{labels: p.labels, target: p.target}
		if q.next(screen, at) != "" || q.next(screen, at.Add(terminalMenuSettle)) != "" || len(q.seen) != 0 {
			t.Fatal("ambiguous screen counted as model menu")
		}
	}
	q := terminalModelMenu{labels: p.labels, target: p.target}
	for i := 0; i < 100; i++ {
		q.next("Select model\n❯ middle (Kiro)", at.Add(time.Duration(i)*time.Second))
	}
	if q.reversals != 2 || q.moves != 3 || q.allSeen() {
		t.Fatal("stalled menu escaped finite guard")
	}
	q = terminalModelMenu{labels: p.labels, target: p.target}
	if q.next("❯ /model\nSelect model\n❯ first (Kiro)\n middle (Kiro)", at) != "" || q.next("❯ /model\nSelect model\n❯ first (Kiro)\n middle (Kiro)", at.Add(terminalMenuSettle)) != "\x1b[B" || len(q.focused) != 1 {
		t.Fatal("input prompt outside menu was treated as a selected model row")
	}
	if q.next("❯ middle (Kiro)\nEsc to cancel", at.Add(time.Second)) != "" || q.next("❯ middle (Kiro)\nEsc to cancel", at.Add(time.Second+terminalMenuSettle)) != "" {
		t.Fatal("menu inferred from footer")
	}
}

func TestModelMenuNavigatesUnadvertisedRowsWithoutAttributingAModel(t *testing.T) {
	q := terminalModelMenu{labels: []string{"first (kiro)", "second (kiro)"}, target: "second (kiro)"}
	at := time.Unix(10, 0)
	settled := func(screen string, at time.Time) string {
		if q.next(screen, at) != "" {
			t.Fatal("acted on a frame that had not settled")
		}
		return q.next(screen, at.Add(terminalMenuSettle))
	}
	if settled("Select model\n❯ Navigation row\n first (Kiro)\n second (Kiro)", at) != "\x1b[B" || len(q.focused) != 0 {
		t.Fatal("unadvertised row was selected or counted as catalog coverage")
	}
	if settled("Select model\n❯ first (Kiro)\n second (Kiro)", at.Add(time.Second)) != "\x1b[B" {
		t.Fatal("advertised first row")
	}
	if settled("Select model\n first (Kiro)\n❯ second (Kiro)", at.Add(2*time.Second)) != "\r" || !q.covered() || q.unadvertisedRows != 1 {
		t.Fatal("target selection did not follow complete advertised focus coverage")
	}
}

func TestKiroTerminalModelDiscovery(t *testing.T) {
	kiro := os.Getenv("DAX_INTEROP_KIRO_BINARY")
	if kiro == "" {
		t.Skip("set pinned Kiro for finite model discovery; no ACP or prompt")
	}
	runner, err := childproc.New(childproc.Config{Timeout: 15 * time.Second, MaxProcesses: 1, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal("model discovery runner")
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	c := terminalDiscoverModels(t, ctx, runner, kiro, t.TempDir())
	ids, labels, _, err := terminalModelPlan(c)
	if err != nil {
		t.Fatal("no unambiguous current model pair")
	}
	t.Logf("catalog_entries=%d two_distinct_models=%v picker_labels=%d session_created=false prompt_sent=false", len(c.Models()), ids[0] != ids[1], len(labels))
}

func terminalDiscoverModels(t *testing.T, ctx context.Context, runner *childproc.Runner, kiro, root string) *catalog.Catalog {
	t.Helper()
	configuration := filepath.Join(root, "model-catalog-config")
	if os.Mkdir(configuration, 0700) != nil {
		t.Fatal("owned model discovery configuration")
	}
	calls := 0
	observed := kiroHomeIdentityRunner(func(ctx context.Context, command childproc.Command) (childproc.Result, error) {
		calls++
		if calls > 3 {
			return childproc.Result{}, errors.New("model discovery command budget")
		}
		want, args := kiro, []string{"--version"}
		if calls == 2 {
			want = filepath.Join(filepath.Dir(kiro), "kiro-cli-chat")
		}
		if calls == 3 {
			args = []string{"chat", "--list-models", "--format", "json"}
		}
		if command.Executable != want || !slices.Equal(command.Args, args) {
			return childproc.Result{}, errors.New("model discovery command mismatch")
		}
		command.Environment = append(command.Environment, "KIRO_HOME="+configuration)
		result, err := runner.Run(ctx, command)
		if runner.Active() != 0 || result.PID > 0 && !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
			return result, errors.New("model discovery cleanup")
		}
		return result, err
	})
	c, err := launcher.ReadKiroCatalog(ctx, observed, launcher.KiroConfig{Executable: kiro, Home: os.Getenv("HOME"), Directory: root})
	if err != nil || calls != 3 {
		t.Fatal("finite pinned model discovery failed")
	}
	return c
}
