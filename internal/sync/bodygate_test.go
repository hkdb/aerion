package sync

import (
	"context"
	"testing"
	"time"
)

func TestBodyFetchConcurrency(t *testing.T) {
	if bodyFetchConcurrency != 3 {
		t.Fatalf("bodyFetchConcurrency = %d, want 3", bodyFetchConcurrency)
	}
}

func TestAccountGate_BoundsPerAccount(t *testing.T) {
	var g accountGate
	ctx := context.Background()

	releases := make([]func(), 0, bodyFetchConcurrency)
	for i := 0; i < bodyFetchConcurrency; i++ {
		release, err := g.acquire(ctx, "a")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	entered := make(chan struct{})
	go func() {
		release, err := g.acquire(ctx, "a")
		if err == nil {
			release()
		}
		close(entered)
	}()
	select {
	case <-entered:
		t.Fatal("third fetch for the same account entered while the gate was full")
	case <-time.After(100 * time.Millisecond):
	}

	// Another account is independent.
	releaseB, err := g.acquire(ctx, "b")
	if err != nil {
		t.Fatalf("acquire for another account: %v", err)
	}
	releaseB()

	// Releasing one slot lets the queued fetch in.
	releases[0]()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("queued fetch did not proceed after a release")
	}
	releases[1]()
}

func TestAccountGate_CancelWhileWaiting(t *testing.T) {
	var g accountGate
	ctx := context.Background()
	for i := 0; i < bodyFetchConcurrency; i++ {
		if _, err := g.acquire(ctx, "a"); err != nil {
			t.Fatal(err)
		}
	}

	waitCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := g.acquire(waitCtx, "a")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("acquire returned nil error after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled acquire did not return")
	}
}
