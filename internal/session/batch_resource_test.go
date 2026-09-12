//go:build darwin || linux

package session

import (
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"testing"
)

type batchResources struct {
	FDs, Goroutines int
	Heap            uint64
}

func measureBatchResources() (batchResources, error) {
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	path := "/dev/fd"
	if runtime.GOOS == "linux" {
		path = "/proc/self/fd"
	}
	file, err := os.Open(path)
	if err != nil {
		return batchResources{}, errors.New("owned descriptor observer unavailable")
	}
	entries, readErr := file.ReadDir(4097)
	closeErr := file.Close()
	if (readErr != nil && readErr != io.EOF) || closeErr != nil || len(entries) > 4096 {
		return batchResources{}, errors.New("owned descriptor observer bound")
	}
	return batchResources{len(entries), runtime.NumGoroutine(), memory.HeapAlloc}, nil
}

func withinBatchResources(base, now batchResources) bool {
	return now.FDs <= base.FDs+2 && now.Goroutines <= base.Goroutines+16 && now.Heap <= base.Heap+(8<<20)
}

func TestBatchResourceObserverRejectsRetainedResources(t *testing.T) {
	for _, kind := range []string{"descriptor", "goroutine", "heap"} {
		t.Run(kind, func(t *testing.T) {
			base, err := measureBatchResources()
			if err != nil {
				t.Fatal(err)
			}
			var handles []*os.File
			var retained []byte
			var workers sync.WaitGroup
			release := make(chan struct{})
			var once sync.Once
			cleanup := func() {
				once.Do(func() {
					close(release)
					workers.Wait()
					for _, f := range handles {
						if f.Close() != nil {
							t.Error("owned retained descriptor cleanup")
						}
					}
				})
			}
			defer cleanup()
			switch kind {
			case "descriptor":
				for range 8 {
					f, err := os.Open(os.DevNull)
					if err != nil {
						t.Fatal("owned retained descriptor")
					}
					handles = append(handles, f)
				}
			case "goroutine":
				for range 24 {
					workers.Go(func() { <-release })
				}
			case "heap":
				retained = make([]byte, 12<<20)
				for i := 0; i < len(retained); i += 4096 {
					retained[i] = byte(i / 4096)
				}
			}
			now, err := measureBatchResources()
			runtime.KeepAlive(retained)
			targetSeen := kind == "descriptor" && now.FDs >= base.FDs+8 || kind == "goroutine" && now.Goroutines >= base.Goroutines+24 || kind == "heap" && now.Heap >= base.Heap+(10<<20)
			if err != nil || !targetSeen || withinBatchResources(base, now) {
				t.Fatal("retained resources escaped the declared observer envelope")
			}
			cleanup()
			retained = nil
			settled, err := measureBatchResources()
			if err != nil || !withinBatchResources(base, settled) {
				t.Fatal("released resource control did not settle")
			}
		})
	}
}
