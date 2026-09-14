package websearch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/acp"
	"dax-kiro-proxy/internal/anthropic"
	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/inference"
)

func TestSearchACPProcessConversionAndCleanup(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "independent-acp")
	if out, err := exec.Command("go", "build", "-o", binary, "./testdata/peer").CombinedOutput(); err != nil {
		t.Fatalf("independent fixture build failed: %s", out)
	}
	for _, mode := range []string{"normal", "native-output", "native-no-budget", "native-denied", "native-denied-no-budget", "extra-tool", "crash", "hang"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if os.Chmod(dir, 0700) != nil {
				t.Fatal("private fixture")
			}
			peer, err := acp.Start(t.Context(), acp.Config{Executable: binary, Args: []string{mode, dir}, Directory: dir, Environment: []string{}, ClientInfo: acp.Info{Name: "independent-search-client", Version: "1"}, Limits: acp.Limits{RequestTimeout: 2 * time.Second, GracePeriod: 50 * time.Millisecond, TermPeriod: 50 * time.Millisecond, CancelTimeout: 50 * time.Millisecond}})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			pid := peer.PID()
			r := searchRequest(t)
			cat, _ := catalog.New([]catalog.Backend{{ID: "fixture-selected"}}, "fixture-selected")
			r.Model, _ = cat.ClientID("fixture-selected")
			s, err := Prepare(t.Context(), peer, dir, dir, r, anthropic.SearchSpec{MaxUses: 1})
			if mode == "extra-tool" {
				if err == nil {
					t.Fatal("extra native tool reached prompt")
				}
				if _, e := os.Stat(filepath.Join(dir, "prompt-seen")); !os.IsNotExist(e) {
					t.Fatal("unsafe inventory was prompted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if mode == "native-denied" && !TakeBudget(dir, 1) {
					t.Fatal("cannot preseed exhausted fixture budget")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
				defer cancel()
				var events []inference.Event
				err = s.Run(ctx, func(e inference.Event) bool { events = append(events, e); return true })
				if mode == "normal" || mode == "native-output" || mode == "native-denied" {
					searches := 0
					terminal := false
					for _, e := range events {
						if e.Kind == inference.Search {
							searches += len(e.Searches)
							if mode == "native-denied" && (len(e.Searches) != 1 || e.Searches[0].ErrorCode != "unavailable" || len(e.Searches[0].Results) != 0) {
								t.Fatal("native denial did not produce a single error without results")
							}
						}
						terminal = terminal || e.Kind == inference.End
					}
					if err != nil || searches != 1 || !terminal {
						t.Fatal("complete search result missing")
					}
				} else if err == nil {
					t.Fatal("failed process succeeded")
				}
				if mode == "native-no-budget" || mode == "native-denied-no-budget" {
					if !errors.Is(err, acp.ErrProtocol) {
						t.Fatal("missing hook record did not fail closed")
					}
					for _, event := range events {
						if event.Kind == inference.Search || event.Kind == inference.End {
							t.Fatal("unbudgeted result escaped")
						}
					}
				}
			}
			if peer.Close() != nil || !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
				t.Fatal("search process group retained")
			}
		})
	}
}
