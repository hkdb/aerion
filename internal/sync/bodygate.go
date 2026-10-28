package sync

import (
	"context"
	gosync "sync"
)

// bodyFetchConcurrency is how many background body fetches may run per
// account at once. Each one holds a pool connection for a folder's whole body
// backlog (minutes on a large folder); unbounded, a sync-all or scheduled pass
// on an account that syncs several folders held every pool slot and the next
// IDLE-triggered sync timed out waiting for a connection. Three matches the
// core sync set (Inbox, Drafts, Sent) so a launch sync never queues bodies;
// it only bites on accounts that sync all or subscribed folders.
const bodyFetchConcurrency = 3

// accountGate is a per-account counting semaphore, lazily created per key.
type accountGate struct {
	mu    gosync.Mutex
	gates map[string]chan struct{}
}

// acquire takes one slot for accountID, waiting until one is free or ctx is
// done. The returned release must be called exactly once.
func (g *accountGate) acquire(ctx context.Context, accountID string) (func(), error) {
	g.mu.Lock()
	if g.gates == nil {
		g.gates = make(map[string]chan struct{})
	}
	gate, ok := g.gates[accountID]
	if !ok {
		gate = make(chan struct{}, bodyFetchConcurrency)
		g.gates[accountID] = gate
	}
	g.mu.Unlock()

	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
