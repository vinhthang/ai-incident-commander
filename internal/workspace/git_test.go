package workspace

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkspaceLock_SerializesConcurrentAccess(t *testing.T) {
	var currentHolders atomic.Int32
	var maxHolders atomic.Int32
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Lock()
			defer Unlock()

			cur := currentHolders.Add(1)
			for {
				prev := maxHolders.Load()
				if cur <= prev || maxHolders.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			currentHolders.Add(-1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if maxHolders.Load() != 1 {
		t.Fatalf("expected workspace.Lock() to limit concurrent holders to 1, got %d", maxHolders.Load())
	}
	if elapsed < 70*time.Millisecond {
		t.Fatalf("expected 3 serialized 25ms critical sections to take >= 70ms, took %v", elapsed)
	}
}
