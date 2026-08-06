package sync

import (
	"context"
	"errors"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestLocks() *folderLocks {
	return &folderLocks{locks: map[string]chan struct{}{}}
}

func TestFolderLocks_SerializesSameKey(t *testing.T) {
	l := newTestLocks()
	var inCritical atomic.Int32
	var maxSeen atomic.Int32
	var wg gosync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := l.lock(context.Background(), "folder-1")
			if err != nil {
				t.Errorf("lock: %v", err)
				return
			}
			n := inCritical.Add(1)
			if n > maxSeen.Load() {
				maxSeen.Store(n)
			}
			time.Sleep(2 * time.Millisecond)
			inCritical.Add(-1)
			unlock()
		}()
	}
	wg.Wait()

	if maxSeen.Load() != 1 {
		t.Errorf("max concurrent holders = %d, want 1", maxSeen.Load())
	}
}

func TestFolderLocks_IndependentKeys(t *testing.T) {
	l := newTestLocks()

	unlockA, err := l.lock(context.Background(), "folder-a")
	if err != nil {
		t.Fatalf("lock a: %v", err)
	}
	defer unlockA()

	// A held lock on another key must not block this one.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlockB, err := l.lock(ctx, "folder-b")
	if err != nil {
		t.Fatalf("lock b blocked by unrelated key: %v", err)
	}
	unlockB()
}

func TestFolderLocks_WaiterCancellation(t *testing.T) {
	l := newTestLocks()

	unlock, err := l.lock(context.Background(), "folder-1")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, waitErr := l.lock(ctx, "folder-1")
		done <- waitErr
	}()

	cancel()
	select {
	case waitErr := <-done:
		if !errors.Is(waitErr, context.Canceled) {
			t.Errorf("waiter error = %v, want context.Canceled", waitErr)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	// The holder's release must still work, and the lock must be acquirable
	// again after the cancelled wait.
	unlock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	unlock2, err := l.lock(ctx2, "folder-1")
	if err != nil {
		t.Fatalf("re-acquire after cancelled wait: %v", err)
	}
	unlock2()
}
