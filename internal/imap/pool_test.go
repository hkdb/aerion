package imap

import (
	"testing"
	"time"
)

// newTestPoolWithDeadConn builds a pool holding one PooledConnection whose
// client is nil — the shape of a connection whose socket is already closed
// (isHealthyLocked returns false without touching the network).
func newTestPoolWithDeadConn(t *testing.T, lastUsed time.Time) (*Pool, *PooledConnection) {
	t.Helper()
	p := NewPool(DefaultPoolConfig(), nil)
	conn := &PooledConnection{accountID: "acct", createdAt: time.Now(), lastUsed: lastUsed}
	p.connections["acct"] = []*PooledConnection{conn}
	return p, conn
}

// A dead connection must leave the pool on Release. Before this, Release only
// withheld it from waiters and left it listed with a fresh lastUsed, so the
// fresh-window fast path in GetConnection kept handing it out (Proton Bridge).
func TestRelease_DiscardsUnhealthy(t *testing.T) {
	p, conn := newTestPoolWithDeadConn(t, time.Now())
	conn.inUse = true

	p.Release(conn)

	if conns := p.connections["acct"]; len(conns) != 0 {
		t.Fatalf("dead connection still in pool after Release: %d entries", len(conns))
	}
}

// The fresh window must never trust a connection without a live client.
func TestProbeIfStale_NeverTrustsNilClient(t *testing.T) {
	p, conn := newTestPoolWithDeadConn(t, time.Now())
	if p.probeIfStaleLocked(conn, "acct") {
		t.Fatal("probeIfStaleLocked trusted a fresh connection with a nil client")
	}
}

// Sanity on the window itself: short enough that a localhost bridge dropping
// idle connections cannot be trusted for long, long enough to cover a STATUS
// burst that reuses connections within milliseconds.
func TestHealthProbeFreshWindow(t *testing.T) {
	if healthProbeFreshWindow != 10*time.Second {
		t.Fatalf("healthProbeFreshWindow = %v, want 10s", healthProbeFreshWindow)
	}
	p, conn := newTestPoolWithDeadConn(t, time.Now().Add(-2*healthProbeFreshWindow))
	if p.probeIfStaleLocked(conn, "acct") {
		t.Fatal("stale nil-client connection reported healthy")
	}
}

// Reservation must count in-flight creations so concurrent acquisitions can't
// all pass the size check and overshoot MaxConnections (the burst that made
// Gmail slow-walk the last login by 25 s).
func TestReserveSlot_NeverExceedsMax(t *testing.T) {
	cfg := DefaultPoolConfig()
	cfg.MaxConnections = 3 // pin the cap: the arithmetic below assumes it, whatever the default is
	p := NewPool(cfg, nil)
	p.connections["a"] = []*PooledConnection{
		{accountID: "a", inUse: true},
		{accountID: "a", inUse: true},
	}

	p.mu.Lock()
	first := p.reserveSlotLocked("a")
	second := p.reserveSlotLocked("a")
	p.mu.Unlock()
	if !first {
		t.Fatal("first reservation should succeed (2 live + 0 pending < 3)")
	}
	if second {
		t.Fatal("second reservation should fail (2 live + 1 pending == 3)")
	}
	if p.pending["a"] != 1 {
		t.Fatalf("pending = %d, want 1", p.pending["a"])
	}

	p.unreserveSlot("a")
	if _, ok := p.pending["a"]; ok {
		t.Fatal("pending entry should be removed once it reaches zero")
	}
	p.mu.Lock()
	again := p.reserveSlotLocked("a")
	p.mu.Unlock()
	if !again {
		t.Fatal("reservation should succeed again after unreserve")
	}
}

// The chosen gate width — documents the trade-off (see createConcurrency).
func TestCreateConcurrency(t *testing.T) {
	if createConcurrency != 3 {
		t.Fatalf("createConcurrency = %d, want 3", createConcurrency)
	}
}

// At most createConcurrency logins in flight per account: the next creator
// for the same account waits for the gate; another account is not held up.
func TestCreateGate_BoundsPerAccount(t *testing.T) {
	p := NewPool(DefaultPoolConfig(), nil)

	p.mu.Lock()
	gateA := p.gateForLocked("a")
	gateB := p.gateForLocked("b")
	p.mu.Unlock()

	for i := 0; i < createConcurrency; i++ {
		gateA <- struct{}{} // createConcurrency creators for "a" hold the gate
	}

	entered := make(chan struct{})
	go func() {
		gateA <- struct{}{} // one more creator for "a" must block here
		close(entered)
	}()

	select {
	case <-entered:
		t.Fatal("extra creator for the same account entered while the gate was full")
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case gateB <- struct{}{}: // a different account is independent
		<-gateB
	default:
		t.Fatal("gate for another account should be free")
	}

	<-gateA // one creator finishes
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("waiting creator did not proceed after a gate slot was released")
	}
}
