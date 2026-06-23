// Package extensions provides infrastructure for Aerion's first-party
// extension system. The per-extension Store opens an isolated SQLite file
// per extension under <dataDir>/extensions/<name>/data.db and exposes a
// scoped KV namespace alongside whatever extension-specific tables the
// extension itself defines via migrations.
//
// Extensions never query each other's tables. Cross-extension data access
// flows through Go interfaces in internal/core/api/v1.
package extensions

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/hkdb/aerion/internal/database"
	"github.com/hkdb/aerion/internal/logging"

	"github.com/rs/zerolog"
)

// Migration is one schema-version step for an extension's database. Versions
// must start at 1 and increment monotonically. SQL is executed inside a
// transaction; multi-statement SQL is allowed.
type Migration struct {
	Version int
	SQL     string
}

// Store wraps an extension's isolated SQLite database. It exposes raw *sql.DB
// access for the extension's own tables and a KV() namespace for small
// key-value config that doesn't warrant a dedicated table.
type Store struct {
	db   *database.DB
	name string
	log  zerolog.Logger
}

// OpenStore opens (or creates) the per-extension SQLite file at
// <dataDir>/extensions/<name>/data.db, applies the provided migrations
// idempotently, and ensures the canonical ext_kv table exists for the
// Store's KV namespace. The store is opened whether or not the extension
// is currently enabled, so the schema stays valid across enable/disable
// cycles (per EXTENSION_ARCHITECTURE.md).
func OpenStore(dataDir, name string, migrations []Migration) (*Store, error) {
	if name == "" {
		return nil, fmt.Errorf("extensions: empty extension name")
	}

	path := filepath.Join(dataDir, "extensions", name, "data.db")
	db, err := database.Open(path)
	if err != nil {
		return nil, fmt.Errorf("extensions: open %s: %w", path, err)
	}

	s := &Store{
		db:   db,
		name: name,
		log:  logging.WithComponent("extensions").With().Str("extension", name).Logger(),
	}

	if err := s.ensureKVTable(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("extensions: ensure kv table for %s: %w", name, err)
	}

	if err := s.applyMigrations(migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("extensions: migrate %s: %w", name, err)
	}

	s.log.Debug().Str("path", path).Int("migrations", len(migrations)).Msg("Extension store opened")
	return s, nil
}

// DB returns the underlying *sql.DB for the extension to run its own queries
// against its own tables. The extension MUST NOT query tables owned by
// another extension or by core.
func (s *Store) DB() *sql.DB {
	return s.db.DB
}

// Path returns the on-disk path of the extension's SQLite file.
func (s *Store) Path() string {
	return s.db.Path()
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ensureKVTable creates the canonical KV table if it does not already exist.
// The table is created BEFORE user migrations run so KV is always available,
// even for an extension with zero user-defined tables.
func (s *Store) ensureKVTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS ext_kv (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// applyMigrations applies pending migrations in version order, recording each
// in a per-extension migrations table. Runs idempotently — already-applied
// migrations are skipped.
func (s *Store) applyMigrations(migrations []Migration) error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			version    INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	var current int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM migrations").Scan(&current); err != nil {
		return fmt.Errorf("read current migration version: %w", err)
	}

	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		if err := s.applyOne(m); err != nil {
			return fmt.Errorf("apply migration %d: %w", m.Version, err)
		}
		s.log.Info().Int("version", m.Version).Msg("Extension migration applied")
	}
	return nil
}

func (s *Store) applyOne(m Migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("migration sql: %w", err)
	}
	if _, err := tx.Exec("INSERT INTO migrations (version) VALUES (?)", m.Version); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return tx.Commit()
}
