package imap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// IsConnectionError checks if an error indicates a dead/broken connection.
// These errors warrant discarding the connection and getting a new one from the pool.
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	connectionErrors := []string{
		"use of closed network connection",
		"connection reset",
		"broken pipe",
		"EOF",
		"i/o timeout",
		"connection refused",
		"no such host",
		"network is unreachable",
	}
	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}
	return false
}

// PoolConfig configures the connection pool
type PoolConfig struct {
	// MaxConnections is the maximum number of connections per account
	MaxConnections int

	// IdleTimeout is how long a connection can be idle before being closed
	IdleTimeout time.Duration

	// ConnectTimeout is how long to wait for a connection to be established
	ConnectTimeout time.Duration

	// WaiterTimeout is max time to wait for a connection when pool is exhausted
	WaiterTimeout time.Duration
}

// DefaultPoolConfig returns sensible defaults for the pool
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		// 5 per account: a background body fetch can hold one connection for a
		// while, so 3 was too tight — concurrent folder/flag syncs and UI actions
		// (move, flag) would block waiting for a free slot. 5 stays comfortably
		// under common server limits (Gmail allows 15 IMAP connections/account).
		// Note: IMAP IDLE uses its own dedicated connection outside this pool.
		MaxConnections: 5,
		IdleTimeout:    5 * time.Minute,
		ConnectTimeout: 30 * time.Second,
		// Fail fast when the pool is genuinely saturated. 2 minutes meant a stuck
		// or slow operation froze every other operation for two whole minutes;
		// 25s lets the caller log and retry on the next sync cycle instead.
		WaiterTimeout: 25 * time.Second,
	}
}

// PooledConnection wraps a Client with pool metadata
type PooledConnection struct {
	client    *Client
	accountID string
	createdAt time.Time
	lastUsed  time.Time
	inUse     bool
	mu        sync.Mutex
}

// IsHealthy checks if the connection is still usable (acquires lock)
func (pc *PooledConnection) IsHealthy() bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.isHealthyLocked()
}

// isHealthyLocked checks health without acquiring lock (caller must hold lock).
// Sends a NOOP to verify the connection is alive — catches closed sockets
// that still have non-nil client references (e.g., after server-side disconnect).
func (pc *PooledConnection) isHealthyLocked() bool {
	if pc.client == nil || pc.client.client == nil {
		return false
	}

	// Send NOOP to verify the connection is actually alive.
	// This catches closed sockets (e.g., Proton Bridge dropping idle connections).
	if err := pc.client.client.Noop().Wait(); err != nil {
		return false
	}
	return true
}

// Pool manages IMAP connections for multiple accounts
type Pool struct {
	config      PoolConfig
	connections map[string][]*PooledConnection // accountID -> connections
	waiters     map[string][]chan *PooledConnection
	mu          sync.Mutex
	log         zerolog.Logger

	// Credentials provider function
	getCredentials func(accountID string) (*ClientConfig, error)
}

// NewPool creates a new connection pool
func NewPool(config PoolConfig, getCredentials func(accountID string) (*ClientConfig, error)) *Pool {
	return &Pool{
		config:         config,
		connections:    make(map[string][]*PooledConnection),
		waiters:        make(map[string][]chan *PooledConnection),
		log:            logging.WithComponent("imap-pool"),
		getCredentials: getCredentials,
	}
}

// GetConnection gets or creates a connection for an account
func (p *Pool) GetConnection(ctx context.Context, accountID string) (*PooledConnection, error) {
	p.mu.Lock()

	// Try to find an available connection
	if conns, ok := p.connections[accountID]; ok {
		for _, conn := range conns {
			conn.mu.Lock()
			if !conn.inUse && conn.isHealthyLocked() {
				conn.inUse = true
				conn.lastUsed = time.Now()
				conn.mu.Unlock()
				p.mu.Unlock()

				p.log.Debug().
					Str("account", accountID).
					Msg("Reusing existing connection")
				return conn, nil
			}
			conn.mu.Unlock()
		}
	}

	// Count current connections for this account
	currentCount := len(p.connections[accountID])

	// Can we create a new one?
	if currentCount < p.config.MaxConnections {
		p.mu.Unlock()
		return p.createConnection(ctx, accountID)
	}

	// At limit - must wait
	p.log.Debug().
		Str("account", accountID).
		Int("current", currentCount).
		Int("max", p.config.MaxConnections).
		Msg("Connection pool exhausted, waiting")

	waiter := make(chan *PooledConnection, 1)
	p.waiters[accountID] = append(p.waiters[accountID], waiter)
	p.mu.Unlock()

	// Wait for a connection, context cancellation, or timeout
	select {
	case conn := <-waiter:
		if conn == nil {
			// Channel was closed by CloseAccount/CloseAll — pool is being cleared
			return nil, fmt.Errorf("connection pool closed")
		}
		return conn, nil
	case <-ctx.Done():
		// Remove ourselves from waiters
		p.mu.Lock()
		waiters := p.waiters[accountID]
		for i, w := range waiters {
			if w == waiter {
				p.waiters[accountID] = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
		return nil, ctx.Err()
	case <-time.After(p.config.WaiterTimeout):
		// Timeout waiting for connection - pool may be deadlocked
		p.mu.Lock()
		waiters := p.waiters[accountID]
		for i, w := range waiters {
			if w == waiter {
				p.waiters[accountID] = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
		p.log.Warn().
			Str("account", accountID).
			Dur("timeout", p.config.WaiterTimeout).
			Msg("Timed out waiting for connection from pool")
		return nil, fmt.Errorf("timed out waiting for connection from pool")
	}
}

// createConnection creates a new connection for an account
func (p *Pool) createConnection(ctx context.Context, accountID string) (*PooledConnection, error) {
	return p.createConnectionWithRetry(ctx, accountID, 0)
}

// createConnectionWithRetry creates a connection with retry logic for transient errors
// like "max connections exceeded" (server still has ghost connections after network change).
func (p *Pool) createConnectionWithRetry(ctx context.Context, accountID string, attempt int) (*PooledConnection, error) {
	p.log.Debug().
		Str("account", accountID).
		Msg("Creating new connection")

	// Get credentials for this account
	config, err := p.getCredentials(accountID)
	if err != nil {
		p.log.Error().Err(err).Str("account", accountID).Msg("Failed to get credentials")
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	p.log.Debug().
		Str("account", accountID).
		Str("host", config.Host).
		Int("port", config.Port).
		Str("authType", string(config.AuthType)).
		Msg("Got credentials, connecting to IMAP server")

	// Create and connect the client
	client := NewClient(*config)

	// Use a goroutine with context for connection timeout
	done := make(chan error, 1)
	go func() {
		if err := client.Connect(); err != nil {
			p.log.Error().Err(err).Str("account", accountID).Msg("IMAP Connect failed")
			done <- err
			return
		}
		p.log.Debug().Str("account", accountID).Msg("IMAP connected, logging in")
		if err := client.Login(); err != nil {
			p.log.Error().Err(err).Str("account", accountID).Msg("IMAP Login failed")
			client.ForceClose()
			done <- err
			return
		}
		p.log.Debug().Str("account", accountID).Msg("IMAP login successful")
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			// Retry once for "max connections" errors — the server may still
			// have ghost connections from force-closed sessions (network change).
			// Wait 15s to give the server time to drop stale connections.
			if attempt == 0 && strings.Contains(err.Error(), "Maximum number of connections") {
				p.log.Warn().Str("account", accountID).Msg("Max connections exceeded, retrying after 15s")
				select {
				case <-time.After(15 * time.Second):
					return p.createConnectionWithRetry(ctx, accountID, attempt+1)
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			p.log.Error().Err(err).Str("account", accountID).Msg("Connection failed")
			return nil, fmt.Errorf("failed to connect: %w", err)
		}
	case <-ctx.Done():
		// Try to close the client if it was created
		p.log.Warn().Str("account", accountID).Msg("Connection timed out (context cancelled)")
		go client.ForceClose()
		return nil, ctx.Err()
	}

	conn := &PooledConnection{
		client:    client,
		accountID: accountID,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     true,
	}

	p.mu.Lock()
	p.connections[accountID] = append(p.connections[accountID], conn)
	p.mu.Unlock()

	p.log.Info().
		Str("account", accountID).
		Msg("New connection created")

	return conn, nil
}

// Release returns a connection to the pool
func (p *Pool) Release(conn *PooledConnection) {
	if conn == nil {
		return
	}

	conn.mu.Lock()
	conn.inUse = false
	conn.lastUsed = time.Now()
	healthy := conn.isHealthyLocked()
	conn.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Verify the connection is still tracked by the pool and healthy before
	// handing it to a waiter. After CloseAll (sleep), dead connections from
	// deferred Release calls must not be given to waiters.
	if !healthy {
		p.log.Debug().
			Str("account", conn.accountID).
			Msg("Released connection is unhealthy, discarding")
		return
	}

	inPool := false
	if conns, ok := p.connections[conn.accountID]; ok {
		for _, c := range conns {
			if c == conn {
				inPool = true
				break
			}
		}
	}
	if !inPool {
		p.log.Debug().
			Str("account", conn.accountID).
			Msg("Released connection no longer in pool, discarding")
		return
	}

	// Check if anyone is waiting for a connection for this account
	if waiters, ok := p.waiters[conn.accountID]; ok && len(waiters) > 0 {
		waiter := waiters[0]
		p.waiters[conn.accountID] = waiters[1:]

		conn.mu.Lock()
		conn.inUse = true
		conn.mu.Unlock()

		waiter <- conn
		return
	}

	p.log.Debug().
		Str("account", conn.accountID).
		Msg("Connection released to pool")
}

// Discard removes a connection from the pool without returning it for reuse.
// Use this when a connection is known to be dead/unhealthy (e.g., after connection errors).
// Uses ForceClose to avoid blocking on dead TCP sockets.
func (p *Pool) Discard(conn *PooledConnection) {
	if conn == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Force-close the connection (known dead, skip graceful logout)
	conn.mu.Lock()
	if conn.client != nil {
		conn.client.ForceClose()
		conn.client = nil
	}
	conn.mu.Unlock()

	// Remove from pool
	if conns, ok := p.connections[conn.accountID]; ok {
		for i, c := range conns {
			if c == conn {
				p.connections[conn.accountID] = append(conns[:i], conns[i+1:]...)
				break
			}
		}
		// Clean up empty account entry
		if len(p.connections[conn.accountID]) == 0 {
			delete(p.connections, conn.accountID)
		}
	}

	p.log.Debug().
		Str("account", conn.accountID).
		Msg("Discarded dead connection from pool")
}

// CloseAccount closes all connections for a specific account.
// Uses ForceClose to avoid blocking on dead TCP sockets (e.g., after network changes).
func (p *Pool) CloseAccount(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conns, ok := p.connections[accountID]
	if !ok {
		return
	}

	for _, conn := range conns {
		conn.mu.Lock()
		if conn.client != nil {
			conn.client.ForceClose()
			conn.client = nil
		}
		conn.mu.Unlock()
	}

	delete(p.connections, accountID)

	// Notify any waiters that we're closing
	if waiters, ok := p.waiters[accountID]; ok {
		for _, w := range waiters {
			close(w)
		}
		delete(p.waiters, accountID)
	}

	p.log.Info().
		Str("account", accountID).
		Int("closed", len(conns)).
		Msg("Closed all connections for account")
}

// CloseAll closes all connections in the pool
func (p *Pool) CloseAll() {
	p.mu.Lock()
	accountIDs := make([]string, 0, len(p.connections))
	for accountID := range p.connections {
		accountIDs = append(accountIDs, accountID)
	}
	p.mu.Unlock()

	for _, accountID := range accountIDs {
		p.CloseAccount(accountID)
	}

	p.log.Info().Msg("Closed all connections")
}

// CleanupIdle closes connections that have been idle too long
func (p *Pool) CleanupIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	cleaned := 0

	for accountID, conns := range p.connections {
		var remaining []*PooledConnection

		for _, conn := range conns {
			conn.mu.Lock()
			idle := !conn.inUse && now.Sub(conn.lastUsed) > p.config.IdleTimeout
			conn.mu.Unlock()

			if idle {
				conn.mu.Lock()
				if conn.client != nil {
					conn.client.ForceClose()
				}
				conn.mu.Unlock()
				cleaned++
			} else {
				remaining = append(remaining, conn)
			}
		}

		if len(remaining) == 0 {
			delete(p.connections, accountID)
		} else {
			p.connections[accountID] = remaining
		}
	}

	if cleaned > 0 {
		p.log.Debug().
			Int("cleaned", cleaned).
			Msg("Cleaned up idle connections")
	}
}

// StartCleanupRoutine starts a background goroutine that periodically cleans up idle connections
func (p *Pool) StartCleanupRoutine(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.CleanupIdle()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stats returns pool statistics
type PoolStats struct {
	TotalConnections  int
	ActiveConnections int
	IdleConnections   int
	AccountCount      int
}

// GetStats returns current pool statistics
func (p *Pool) GetStats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := PoolStats{
		AccountCount: len(p.connections),
	}

	for _, conns := range p.connections {
		for _, conn := range conns {
			stats.TotalConnections++
			conn.mu.Lock()
			if conn.inUse {
				stats.ActiveConnections++
			} else {
				stats.IdleConnections++
			}
			conn.mu.Unlock()
		}
	}

	return stats
}

// Client returns the underlying IMAP client from a pooled connection
func (pc *PooledConnection) Client() *Client {
	return pc.client
}
