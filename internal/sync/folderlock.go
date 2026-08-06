package sync

import (
	"context"
	gosync "sync"
)

// folderLocks serializes sync runs per folder. Two engine.SyncMessages runs
// overlapping on one folder corrupt state three ways: duplicate attachment
// rows (plain INSERT, body_fetched=1 set in a separate transaction),
// body_fetched resets via the header Upsert's ON CONFLICT clause, and
// last-writer-wins rollback of the UIDNext/HighestModSeq watermarks. The
// caller-side tracking maps (app syncContexts, scheduler syncing) are
// mutually blind and two engine call sites are tracked by neither, so the
// serialization lives here at the single choke point.
//
// Channel-based rather than a mutex so waiters can abandon on context
// cancellation (a blocked mutex acquire is uncancellable).
type folderLocks struct {
	mu    gosync.Mutex
	locks map[string]chan struct{} // key -> held marker (cap-1 channel)
}

// lock acquires the lock for key, waiting until the current holder releases
// or ctx is cancelled. On success it returns the release func; on
// cancellation it returns ctx's error and the lock is untouched.
func (l *folderLocks) lock(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	ch, ok := l.locks[key]
	if !ok {
		// Entries live for the process (bounded by folder count), matching
		// flagSweepCounter's lifetime.
		ch = make(chan struct{}, 1)
		l.locks[key] = ch
	}
	l.mu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
