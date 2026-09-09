package interop_test

import (
	"errors"
	"sync"
	"testing"

	"dax-kiro-proxy/internal/inference"
)

func TestCancellationProbeNeverDispatchesAnotherRequest(t *testing.T) {
	guard, driver := newDenialBackendFixture()
	backend := &cancellationProbeBackend{guard: guard}
	turn, err := backend.Start(t.Context(), denialRequestFixture("", "", false))
	if err != nil {
		t.Fatal("initial owned cancellation request rejected")
	}
	if _, err := turn.Next(t.Context()); err != nil {
		t.Fatal("owned pending tool was not exposed")
	}
	if backend.delivered.Load() != 0 {
		t.Fatal("tool exposure was mistaken for HTTP completion")
	}
	turn.Finish()
	if backend.delivered.Load() != 1 {
		t.Fatal("completed HTTP handoff was not observed")
	}
	var jobs sync.WaitGroup
	for range 8 {
		jobs.Go(func() {
			_, err := backend.Start(t.Context(), denialRequestFixture("fixture-use", clientDenialReason, true))
			if !errors.Is(err, inference.ErrRequest) {
				t.Error("cancellation observer dispatched a continuation")
			}
		})
	}
	jobs.Wait()
	if driver.starts != 1 || guard.snapshot().Starts != 1 || guard.snapshot().Results != 0 {
		t.Fatal("cancellation request budget was not isolated")
	}
}
