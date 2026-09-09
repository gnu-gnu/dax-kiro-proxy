package installation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func transactionFixture(t *testing.T, existing bool) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	bin, source := filepath.Join(root, "bin"), filepath.Join(root, "source")
	if os.Mkdir(bin, 0700) != nil || os.WriteFile(source, []byte("Independent old executable."), 0700) != nil {
		t.Fatal("fixture")
	}
	old := ""
	if existing {
		if err := Install(t.Context(), bin, source, false); err != nil {
			t.Fatal(err)
		}
		old, _ = filepath.EvalSymlinks(filepath.Join(bin, executableName))
		if os.WriteFile(source, []byte("Independent new executable."), 0700) != nil {
			t.Fatal("fixture")
		}
	}
	return bin, source, old
}

func TestPrepublicationFailuresCleanOwnedStagesAndKeepCurrent(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, phase := range []string{"payload", "prepared", "before-publication"} {
			for _, canceled := range []bool{false, true} {
				t.Run(phase+map[bool]string{false: "-new", true: "-replace"}[existing]+map[bool]string{false: "-io", true: "-cancel"}[canceled], func(t *testing.T) {
					bin, source, old := transactionFixture(t, existing)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					err := install(ctx, bin, source, true, func(at string) error {
						if at != phase {
							return nil
						}
						if canceled {
							cancel()
							return nil
						}
						return ErrIO
					})
					want := ErrIO
					if canceled {
						want = context.Canceled
					}
					if !errors.Is(err, want) || errors.Is(err, ErrPublished) || errors.Is(err, ErrCleanup) {
						t.Fatal("wrong transaction outcome", err)
					}
					if !existing {
						items, err := os.ReadDir(bin)
						if err != nil || len(items) != 0 {
							t.Fatal("failed first install retained artifacts")
						}
						return
					}
					current, err := filepath.EvalSymlinks(filepath.Join(bin, executableName))
					if err != nil || current != old {
						t.Fatal("failed replacement changed current executable")
					}
					if data, err := os.ReadFile(current); err != nil || string(data) != "Independent old executable." {
						t.Fatal("old bytes changed")
					}
					if err := Uninstall(t.Context(), bin); err != nil {
						t.Fatal("failed replacement left invalid state", err)
					}
				})
			}
		}
	}
}

func TestPostpublicationFailureRetainsNewGenerationAndRecovers(t *testing.T) {
	for _, existing := range []bool{false, true} {
		bin, source, old := transactionFixture(t, existing)
		err := install(t.Context(), bin, source, true, func(at string) error {
			if at == "published" {
				return ErrIO
			}
			return nil
		})
		if !errors.Is(err, ErrPublished) {
			t.Fatal("partial publication was concealed", err)
		}
		current, err := filepath.EvalSymlinks(filepath.Join(bin, executableName))
		if err != nil || current == old {
			t.Fatal("publication was rolled back")
		}
		lease, err := Lease(current)
		if err != nil || lease == nil {
			t.Fatal("published generation unusable", err)
		}
		lease.Close()
		if old != "" {
			if _, err := os.Stat(old); err != nil {
				t.Fatal("pre-cleanup failure removed prior generation")
			}
		}
		if err := Install(t.Context(), bin, source, true); err != nil {
			t.Fatal("valid retained generation not recovered", err)
		}
		items, err := os.ReadDir(filepath.Join(bin, managerName))
		if err != nil || len(items) != 4 {
			t.Fatal("recovery retained inactive generations")
		}
		if err := Uninstall(t.Context(), bin); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostpublicationCancellationFinishesCommittedCleanup(t *testing.T) {
	bin, source, old := transactionFixture(t, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := install(ctx, bin, source, true, func(at string) error {
		if at == "published" {
			cancel()
		}
		return nil
	})
	if err != nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("committed cancellation interrupted cleanup", err)
	}
	if current, err := filepath.EvalSymlinks(filepath.Join(bin, executableName)); err != nil || current == old {
		t.Fatal("committed cancellation rolled back publication")
	}
	if _, err := os.Lstat(filepath.Dir(old)); !os.IsNotExist(err) {
		t.Fatal("committed cancellation retained retired generation")
	}
	if err := Uninstall(t.Context(), bin); err != nil {
		t.Fatal(err)
	}
}

func TestAmbiguousPartialStageIsPreservedAndActiveGenerationStillLeases(t *testing.T) {
	bin, source, old := transactionFixture(t, true)
	unknown := ""
	err := install(t.Context(), bin, source, true, func(at string) error {
		if at != "payload" {
			return nil
		}
		matches, err := filepath.Glob(filepath.Join(bin, managerName, "staging-*"))
		if err != nil || len(matches) != 1 {
			t.Fatal("stage fixture")
		}
		unknown = filepath.Join(matches[0], "unrelated")
		if os.WriteFile(unknown, []byte("Preserve unknown owned data."), 0600) != nil {
			t.Fatal("fixture")
		}
		return ErrIO
	})
	if !errors.Is(err, ErrCleanup) || errors.Is(err, ErrPublished) {
		t.Fatal("ambiguous cleanup not reported", err)
	}
	if data, err := os.ReadFile(unknown); err != nil || string(data) != "Preserve unknown owned data." {
		t.Fatal("unknown entry removed")
	}
	if err := Install(t.Context(), bin, source, true); !errors.Is(err, ErrUnmanaged) {
		t.Fatal("ambiguous stage admitted", err)
	}
	if err := Uninstall(t.Context(), bin); !errors.Is(err, ErrUnmanaged) {
		t.Fatal("ambiguous uninstall admitted", err)
	}
	lease, err := Lease(old)
	if err != nil || lease == nil {
		t.Fatal("old generation lost availability", err)
	}
	lease.Close()
}

func TestLeaseRejectsRetiredGenerationAndExclusiveLockBlocksStartup(t *testing.T) {
	bin, source, old := transactionFixture(t, true)
	err := install(t.Context(), bin, source, true, func(at string) error {
		if at == "published" {
			return ErrIO
		}
		return nil
	})
	if !errors.Is(err, ErrPublished) {
		t.Fatal(err)
	}
	if lease, err := Lease(old); err == nil || lease != nil {
		t.Fatal("retired generation admitted")
	}
	s, err := openStore(t.Context(), bin, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if lease, err := Lease(filepath.Join(bin, executableName)); !errors.Is(err, ErrBusy) || lease != nil {
		t.Fatal("startup admitted during mutation", err)
	}
}
