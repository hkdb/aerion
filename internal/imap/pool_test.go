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
