// Package database provides SQLite database functionality
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	_ "modernc.org/sqlite"

	"github.com/rs/zerolog"
)

// Connection pool constants
const (
	// MaxOpenConns limits concurrent database connections.
	// SQLite with WAL mode only supports one writer at a time, so having many
	// connections just increases lock contention. Keep this modest.
	MaxOpenConns = 12

	// BaseIdleConns is the minimum number of idle connections to keep.
	BaseIdleConns = 3

	// MaxIdleConns is the maximum number of idle connections to keep.
	// This is capped to prevent excessive memory usage from warm connections.
	MaxIdleConns = 6

	// IdleConnsPerAccount is how many additional idle connections to keep per account.
	IdleConnsPerAccount = 1

	// CheckpointInterval is how often to run automatic WAL checkpoints.
	// This prevents the WAL file from growing too large.
	CheckpointInterval = 5 * time.Minute
)

// DB wraps the SQL database connection
type DB struct {
	*sql.DB
	path string
	log  zerolog.Logger
}

// Open opens or creates a SQLite database at the given path
func Open(path string) (*DB, error) {
	// Ensure directory exists with secure permissions (owner only)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database with PRAGMAs embedded in the DSN.
	// SQLite PRAGMAs are per-connection, and Go's database/sql creates connections
	// lazily in a pool. Using _pragma in the DSN ensures every new connection gets
	// the same configuration (busy_timeout, WAL, etc.), preventing SQLITE_BUSY
	// errors when a pooled connection lacks busy_timeout.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=cache_size(-64000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for SQLite
	// MaxOpenConns: Modest ceiling - SQLite WAL only allows one writer at a time
	// MaxIdleConns: Start low, will be scaled dynamically based on account count
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(BaseIdleConns)

	// Test connection - this actually creates the file if it doesn't exist
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Ensure database file has secure permissions (owner read/write only)
	// This prevents other users on the system from reading email data
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set database permissions: %w", err)
	}

	return &DB{
		DB:   db,
		path: path,
		log:  logging.WithComponent("database"),
	}, nil
}

// UpdateIdleConns adjusts the number of idle connections based on account count.
// This should be called when accounts are added or removed.
// Formula: BaseIdleConns + (numAccounts * IdleConnsPerAccount), capped at MaxIdleConns
func (db *DB) UpdateIdleConns(numAccounts int) {
	idleConns := BaseIdleConns + (numAccounts * IdleConnsPerAccount)

	// Apply bounds
	if idleConns < BaseIdleConns {
		idleConns = BaseIdleConns
	}
	if idleConns > MaxIdleConns {
		idleConns = MaxIdleConns
	}

	db.SetMaxIdleConns(idleConns)

	db.log.Debug().
		Int("accounts", numAccounts).
		Int("idleConns", idleConns).
		Msg("Updated database connection pool")
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

// Checkpoint runs a WAL checkpoint to merge the write-ahead log back into
// the main database file. This prevents the WAL file from growing too large.
// Uses PASSIVE mode which checkpoints as much as possible without blocking.
func (db *DB) Checkpoint() error {
	_, err := db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	if err != nil {
		return fmt.Errorf("failed to checkpoint WAL: %w", err)
	}
	return nil
}

// StartCheckpointRoutine starts a background goroutine that periodically
// checkpoints the WAL file. This should be called once at application startup.
// The routine will stop when the context is cancelled.
func (db *DB) StartCheckpointRoutine(ctx context.Context) {
	ticker := time.NewTicker(CheckpointInterval)
	defer ticker.Stop()

	db.log.Debug().Dur("interval", CheckpointInterval).Msg("WAL checkpoint routine started")

	for {
		select {
		case <-ticker.C:
			if err := db.Checkpoint(); err != nil {
				db.log.Error().Err(err).Msg("Periodic WAL checkpoint failed")
				continue
			}
			db.log.Debug().Msg("Periodic WAL checkpoint completed")
		case <-ctx.Done():
			db.log.Debug().Msg("WAL checkpoint routine stopped")
			return
		}
	}
}

// Path returns the database file path
func (db *DB) Path() string {
	return db.path
}

// ErrSchemaTooNew is returned by Migrate when the database's recorded migration
// version is HIGHER than the highest migration this build knows about. This
// happens when a user downgrades Aerion after a newer version applied a
// forward-only migration. Callers (App.Startup) surface a friendly dialog
// pointing the user at docs/SQL_ROLLBACK.md.
type ErrSchemaTooNew struct {
	DBVersion    int
	BuildVersion int
}

func (e *ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("database schema version %d is newer than this Aerion build (max known: %d). See https://github.com/hkdb/aerion/blob/main/docs/SQL_ROLLBACK.md", e.DBVersion, e.BuildVersion)
}

// Migrate runs all pending migrations
func (db *DB) Migrate() error {
	// Create migrations table if not exists
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get current version
	var currentVersion int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	// Schema-version gate: refuse if the DB was written by a newer Aerion. The
	// DB has migrations this build doesn't know how to interpret — opening it
	// would query columns/tables in an unexpected shape and likely corrupt
	// autocomplete or crash. Surface a typed error the app can catch.
	maxKnown := 0
	if len(migrations) > 0 {
		maxKnown = migrations[len(migrations)-1].Version
	}
	if currentVersion > maxKnown {
		return &ErrSchemaTooNew{DBVersion: currentVersion, BuildVersion: maxKnown}
	}

	// Apply migrations
	for _, m := range migrations {
		if m.Version > currentVersion {
			if err := db.applyMigration(m); err != nil {
				return fmt.Errorf("failed to apply migration %d: %w", m.Version, err)
			}
		}
	}

	return nil
}

func (db *DB) applyMigration(m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Execute migration
	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("migration SQL failed: %w", err)
	}

	// Record migration
	if _, err := tx.Exec("INSERT INTO migrations (version) VALUES (?)", m.Version); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}
