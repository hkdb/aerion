package backend

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hkdb/aerion/internal/extensions"
)

// migrations is the per-extension migration sequence for the Contacts
// extension's isolated DB. Each entry runs in version order, idempotent —
// already-applied versions are skipped on every startup.
var migrations = []extensions.Migration{
	{
		Version: 1,
		SQL: `
			-- Phase 2b.3: per-record version stamp for OAuth-provider
			-- contacts (Google People, MS Graph).
			--
			-- Google encodes its ETag at person.metadata.sources[0].etag and
			-- REQUIRES it in update bodies (rejects with 400 failedPrecondition
			-- on mismatch). MS Graph has @odata.etag but doesn't strictly
			-- enforce If-Match on contacts (effectively last-writer-wins).
			-- Same column shape works for both.
			--
			-- Lives here in the extension's per-extension SQLite (not in
			-- core's contact_records schema) because OAuth ETag is contacts-
			-- extension-only state with zero core consumers. record_id mirrors
			-- the row id in core's contact_records table; no FK across DBs.
			-- Orphans (etag row whose contact_records row was deleted out
			-- of band) are inert; cleaned up opportunistically when the
			-- write path notices the contact is gone.
			--
			-- READ-side sync (internal/carddav/sync.go, host-side) does NOT
			-- populate this table — host code can't reach the extension's
			-- SQLite without a coreapi pass-through that doesn't exist yet.
			-- Trade-off documented in 2b.3 plan: first write after a sync
			-- sends empty etag, may 412, write path performs a GET to refresh
			-- the etag, retries the PATCH once.

			CREATE TABLE oauth_record_state (
				record_id  TEXT PRIMARY KEY,
				etag       TEXT NOT NULL,
				updated_at INTEGER NOT NULL
			);
		`,
	},
}

// Store wraps the per-extension DB for the Contacts extension. Phase 2b.3
// gives it its first real use: per-record OAuth ETag tracking for Google
// People and MS Graph write paths.
type Store struct {
	*extensions.Store
}

// NewStore opens the Contacts extension's isolated SQLite DB at
// <dataDir>/extensions/contacts/data.db and applies any pending migrations.
// Called from App.Startup eagerly (whether or not the extension is enabled)
// so the schema stays valid across enable/disable cycles.
func NewStore(dataDir string) (*Store, error) {
	s, err := extensions.OpenStore(dataDir, "contacts", migrations)
	if err != nil {
		return nil, err
	}
	return &Store{Store: s}, nil
}

// GetETag returns the stored OAuth ETag for a record, or empty string when
// no row exists (first write after sync, or a record without a tracked
// version stamp). Never returns sql.ErrNoRows — empty string is the
// "no etag known" signal.
func (s *Store) GetETag(recordID string) (string, error) {
	if recordID == "" {
		return "", nil
	}
	var etag string
	err := s.DB().QueryRow(
		`SELECT etag FROM oauth_record_state WHERE record_id = ?`,
		recordID,
	).Scan(&etag)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get oauth etag: %w", err)
	}
	return etag, nil
}

// SetETag stores or updates the OAuth ETag for a record. Empty etag clears
// the row (same effect as DeleteETag) — useful after a write that returns
// no fresh version stamp.
func (s *Store) SetETag(recordID, etag string) error {
	if recordID == "" {
		return fmt.Errorf("SetETag: record_id is required")
	}
	if etag == "" {
		return s.DeleteETag(recordID)
	}
	_, err := s.DB().Exec(
		`INSERT INTO oauth_record_state (record_id, etag, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(record_id) DO UPDATE SET
		     etag = excluded.etag,
		     updated_at = excluded.updated_at`,
		recordID, etag, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("set oauth etag: %w", err)
	}
	return nil
}

// DeleteETag removes the stored ETag for a record. Idempotent — no error
// when no row exists. Called after a Delete write to clean up state.
func (s *Store) DeleteETag(recordID string) error {
	if recordID == "" {
		return nil
	}
	_, err := s.DB().Exec(
		`DELETE FROM oauth_record_state WHERE record_id = ?`,
		recordID,
	)
	if err != nil {
		return fmt.Errorf("delete oauth etag: %w", err)
	}
	return nil
}
