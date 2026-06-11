package carddav

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/contact"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// Store handles CardDAV source and contact storage
type Store struct {
	db  *sql.DB
	log zerolog.Logger
}

// NewStore creates a new CardDAV store
func NewStore(db *sql.DB) *Store {
	return &Store{
		db:  db,
		log: logging.WithComponent("carddav-store"),
	}
}

// ============================================================================
// Source CRUD
// ============================================================================

// CreateSource creates a new contact source
func (s *Store) CreateSource(config *SourceConfig) (*Source, error) {
	id := uuid.New().String()
	now := time.Now()

	// Handle account_id (convert empty string to NULL)
	var accountID *string
	if config.AccountID != "" {
		accountID = &config.AccountID
	}

	query := `
		INSERT INTO contact_sources (id, name, type, url, username, account_id, enabled, writable, sync_interval, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		id, config.Name, config.Type, config.URL, config.Username, accountID,
		config.Enabled, config.Writable, config.SyncInterval, now)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}

	source := &Source{
		ID:           id,
		Name:         config.Name,
		Type:         config.Type,
		URL:          config.URL,
		Username:     config.Username,
		AccountID:    accountID,
		Enabled:      config.Enabled,
		Writable:     config.Writable,
		SyncInterval: config.SyncInterval,
		CreatedAt:    now,
	}

	s.log.Info().Str("id", id).Str("name", config.Name).Msg("Contact source created")
	return source, nil
}

// GetSource returns a source by ID
func (s *Store) GetSource(id string) (*Source, error) {
	query := `
		SELECT id, name, type, url, username, account_id, enabled, writable, sync_interval,
		       last_synced_at, last_error, last_error_at, created_at
		FROM contact_sources
		WHERE id = ?
	`

	var source Source
	var lastSyncedAt, lastErrorAt sql.NullTime
	var lastError, accountID sql.NullString

	err := s.db.QueryRow(query, id).Scan(
		&source.ID, &source.Name, &source.Type, &source.URL, &source.Username,
		&accountID, &source.Enabled, &source.Writable, &source.SyncInterval,
		&lastSyncedAt, &lastError, &lastErrorAt, &source.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get source: %w", err)
	}

	if accountID.Valid {
		source.AccountID = &accountID.String
	}
	if lastSyncedAt.Valid {
		source.LastSyncedAt = &lastSyncedAt.Time
	}
	if lastError.Valid {
		source.LastError = lastError.String
	}
	if lastErrorAt.Valid {
		source.LastErrorAt = &lastErrorAt.Time
	}

	return &source, nil
}

// ListSources returns all contact sources
func (s *Store) ListSources() ([]*Source, error) {
	query := `
		SELECT id, name, type, url, username, account_id, enabled, writable, sync_interval,
		       last_synced_at, last_error, last_error_at, created_at
		FROM contact_sources
		ORDER BY created_at ASC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %w", err)
	}
	defer rows.Close()

	var sources []*Source
	for rows.Next() {
		var source Source
		var lastSyncedAt, lastErrorAt sql.NullTime
		var lastError, accountID sql.NullString

		err := rows.Scan(
			&source.ID, &source.Name, &source.Type, &source.URL, &source.Username,
			&accountID, &source.Enabled, &source.Writable, &source.SyncInterval,
			&lastSyncedAt, &lastError, &lastErrorAt, &source.CreatedAt)
		if err != nil {
			s.log.Warn().Err(err).Msg("Failed to scan source row")
			continue
		}

		if accountID.Valid {
			source.AccountID = &accountID.String
		}
		if lastSyncedAt.Valid {
			source.LastSyncedAt = &lastSyncedAt.Time
		}
		if lastError.Valid {
			source.LastError = lastError.String
		}
		if lastErrorAt.Valid {
			source.LastErrorAt = &lastErrorAt.Time
		}

		sources = append(sources, &source)
	}

	return sources, nil
}

// SetSourceWritable flips the writable flag for a CardDAV source. Used by the
// "Enable write access" toggle in the source-settings dialog.
//
// CardDAV writes use the source's existing per-source credentials (basic auth),
// so toggling writable is a pure flag flip — no consent flow needed. OAuth-
// based sources (Google/Microsoft) gain their toggle in 2b.3 alongside the
// incremental-consent flow.
func (s *Store) SetSourceWritable(id string, writable bool) error {
	result, err := s.db.Exec(`UPDATE contact_sources SET writable = ? WHERE id = ?`, writable, id)
	if err != nil {
		return fmt.Errorf("failed to update source writable: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("source not found: %s", id)
	}
	s.log.Info().Str("id", id).Bool("writable", writable).Msg("Contact source writable toggled")
	return nil
}

// UpdateSource updates a source's configuration
func (s *Store) UpdateSource(id string, config *SourceConfig) error {
	query := `
		UPDATE contact_sources
		SET name = ?, url = ?, username = ?, enabled = ?, sync_interval = ?
		WHERE id = ?
	`

	result, err := s.db.Exec(query,
		config.Name, config.URL, config.Username, config.Enabled, config.SyncInterval, id)
	if err != nil {
		return fmt.Errorf("failed to update source: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("source not found: %s", id)
	}

	s.log.Info().Str("id", id).Msg("Contact source updated")
	return nil
}

// DeleteSource deletes a source and ALL its data — addressbooks, contact
// records, and the records' state + email + sub-table rows — in one
// transaction. Removing a provider must scrub everything that was synced
// from it; leaving local copies of contacts behind would be a privacy leak.
//
// The schema has a coverage gap that requires explicit handling: although
// contact_sources → contact_source_addressbooks has ON DELETE CASCADE, and
// contact_records → carddav_record_state has ON DELETE CASCADE, there is
// NO FK from carddav_record_state.addressbook_id to
// contact_source_addressbooks(id). So a plain "DELETE FROM contact_sources"
// cascades only to the addressbooks; the state rows and the records they
// reference are left as unreachable orphans. This method works around the
// gap by deleting contact_records (and chaining the cascade through them)
// before deleting the source row.
//
// Order inside the transaction:
//  1. Delete contact_records belonging to addressbooks of this source.
//     Cascades through contact_emails, contact_phones, contact_addresses,
//     contact_urls, contact_impps, contact_categories, and
//     carddav_record_state via the FKs on contact_records(id).
//  2. Delete the source row. The existing source→addressbook CASCADE
//     cleans up the addressbook rows.
//
// A regression-test in store_test.go (TestDeleteSource_ScrubsAllData)
// guards against future schema/code changes reintroducing the leak.
func (s *Store) DeleteSource(id string) error {
	if id == "" {
		return fmt.Errorf("source id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		DELETE FROM contact_records
		WHERE id IN (
			SELECT crs.record_id
			FROM carddav_record_state crs
			JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
			WHERE ab.source_id = ?
		)
	`, id); err != nil {
		return fmt.Errorf("delete contact records for source: %w", err)
	}

	result, err := tx.Exec("DELETE FROM contact_sources WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete source: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("source not found: %s", id)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.log.Info().Str("id", id).Msg("Contact source deleted")
	return nil
}

// UpdateSourceSyncStatus updates the sync status after a sync attempt
func (s *Store) UpdateSourceSyncStatus(id string, syncError string) error {
	now := time.Now()

	var query string
	var args []interface{}

	if syncError == "" {
		// Success: update last_synced_at, clear error
		query = `
			UPDATE contact_sources
			SET last_synced_at = ?, last_error = NULL, last_error_at = NULL
			WHERE id = ?
		`
		args = []interface{}{now, id}
	} else {
		// Error: update error fields
		query = `
			UPDATE contact_sources
			SET last_error = ?, last_error_at = ?
			WHERE id = ?
		`
		args = []interface{}{syncError, now, id}
	}

	_, err := s.db.Exec(query, args...)
	return err
}

// GetSourcesWithErrors returns all sources that have errors
func (s *Store) GetSourcesWithErrors() ([]*SourceError, error) {
	query := `
		SELECT id, name, last_error, last_error_at
		FROM contact_sources
		WHERE last_error IS NOT NULL AND last_error != ''
		ORDER BY last_error_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get sources with errors: %w", err)
	}
	defer rows.Close()

	var errors []*SourceError
	for rows.Next() {
		var se SourceError
		var errorAt sql.NullTime

		err := rows.Scan(&se.SourceID, &se.SourceName, &se.Error, &errorAt)
		if err != nil {
			continue
		}

		if errorAt.Valid {
			se.ErrorAt = errorAt.Time
		}

		errors = append(errors, &se)
	}

	return errors, nil
}

// ============================================================================
// Addressbook CRUD
// ============================================================================

// CreateAddressbook creates a new addressbook for a source
func (s *Store) CreateAddressbook(sourceID, path, name string, enabled bool) (*Addressbook, error) {
	id := uuid.New().String()

	query := `
		INSERT INTO contact_source_addressbooks (id, source_id, path, name, enabled)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query, id, sourceID, path, name, enabled)
	if err != nil {
		return nil, fmt.Errorf("failed to create addressbook: %w", err)
	}

	return &Addressbook{
		ID:       id,
		SourceID: sourceID,
		Path:     path,
		Name:     name,
		Enabled:  enabled,
	}, nil
}

// GetAddressbook returns an addressbook by ID
func (s *Store) GetAddressbook(id string) (*Addressbook, error) {
	query := `
		SELECT id, source_id, path, name, enabled, sync_token, last_synced_at
		FROM contact_source_addressbooks
		WHERE id = ?
	`

	var ab Addressbook
	var syncToken sql.NullString
	var lastSyncedAt sql.NullTime

	err := s.db.QueryRow(query, id).Scan(
		&ab.ID, &ab.SourceID, &ab.Path, &ab.Name, &ab.Enabled,
		&syncToken, &lastSyncedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get addressbook: %w", err)
	}

	if syncToken.Valid {
		ab.SyncToken = syncToken.String
	}
	if lastSyncedAt.Valid {
		ab.LastSyncedAt = &lastSyncedAt.Time
	}

	return &ab, nil
}

// GetSourceIDForRecord returns the CardDAV source UUID that owns the given
// contact_records.id, by joining carddav_record_state → contact_source_addressbooks.
// Returns "" (no error) when the record doesn't belong to any CardDAV source
// (e.g., a local-only record). Used by the API layer to enrich
// coreapi.Contact.SourceID with the actual sidebar source UUID rather than
// the literal "carddav" string that fromRecord defaults to — see the comment
// on fromRecord in extensions/contacts/backend/convert.go for context.
func (s *Store) GetSourceIDForRecord(recordID string) (string, error) {
	if recordID == "" {
		return "", nil
	}
	query := `
		SELECT sa.source_id
		FROM carddav_record_state crs
		JOIN contact_source_addressbooks sa ON sa.id = crs.addressbook_id
		WHERE crs.record_id = ?
		LIMIT 1
	`
	var sourceID string
	err := s.db.QueryRow(query, recordID).Scan(&sourceID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get source for record: %w", err)
	}
	return sourceID, nil
}

// ListAddressbooks returns all addressbooks for a source
func (s *Store) ListAddressbooks(sourceID string) ([]*Addressbook, error) {
	query := `
		SELECT id, source_id, path, name, enabled, sync_token, last_synced_at
		FROM contact_source_addressbooks
		WHERE source_id = ?
		ORDER BY name ASC
	`

	rows, err := s.db.Query(query, sourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list addressbooks: %w", err)
	}
	defer rows.Close()

	var addressbooks []*Addressbook
	for rows.Next() {
		var ab Addressbook
		var syncToken sql.NullString
		var lastSyncedAt sql.NullTime

		err := rows.Scan(
			&ab.ID, &ab.SourceID, &ab.Path, &ab.Name, &ab.Enabled,
			&syncToken, &lastSyncedAt)
		if err != nil {
			continue
		}

		if syncToken.Valid {
			ab.SyncToken = syncToken.String
		}
		if lastSyncedAt.Valid {
			ab.LastSyncedAt = &lastSyncedAt.Time
		}

		addressbooks = append(addressbooks, &ab)
	}

	return addressbooks, nil
}

// ListEnabledAddressbooks returns all enabled addressbooks for a source
func (s *Store) ListEnabledAddressbooks(sourceID string) ([]*Addressbook, error) {
	query := `
		SELECT id, source_id, path, name, enabled, sync_token, last_synced_at
		FROM contact_source_addressbooks
		WHERE source_id = ? AND enabled = 1
		ORDER BY name ASC
	`

	rows, err := s.db.Query(query, sourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list enabled addressbooks: %w", err)
	}
	defer rows.Close()

	var addressbooks []*Addressbook
	for rows.Next() {
		var ab Addressbook
		var syncToken sql.NullString
		var lastSyncedAt sql.NullTime

		err := rows.Scan(
			&ab.ID, &ab.SourceID, &ab.Path, &ab.Name, &ab.Enabled,
			&syncToken, &lastSyncedAt)
		if err != nil {
			continue
		}

		if syncToken.Valid {
			ab.SyncToken = syncToken.String
		}
		if lastSyncedAt.Valid {
			ab.LastSyncedAt = &lastSyncedAt.Time
		}

		addressbooks = append(addressbooks, &ab)
	}

	return addressbooks, nil
}

// SetAddressbookEnabled enables or disables an addressbook
func (s *Store) SetAddressbookEnabled(id string, enabled bool) error {
	query := `UPDATE contact_source_addressbooks SET enabled = ? WHERE id = ?`
	_, err := s.db.Exec(query, enabled, id)
	return err
}

// UpdateAddressbookSyncToken updates the sync token after a sync
func (s *Store) UpdateAddressbookSyncToken(id, syncToken string) error {
	now := time.Now()
	query := `
		UPDATE contact_source_addressbooks
		SET sync_token = ?, last_synced_at = ?
		WHERE id = ?
	`
	_, err := s.db.Exec(query, syncToken, now, id)
	return err
}

// DeleteAddressbooksForSource deletes all addressbooks for a source
func (s *Store) DeleteAddressbooksForSource(sourceID string) error {
	_, err := s.db.Exec("DELETE FROM contact_source_addressbooks WHERE source_id = ?", sourceID)
	return err
}

// DeleteAddressbookByID removes a single addressbook AND all the CardDAV
// records it holds, in one transaction. Deleting contact_records cascades
// through contact_emails + sub-tables AND carddav_record_state (which has
// ON DELETE CASCADE on record_id), so the local cache for that addressbook
// is fully torn down before the addressbook row itself is removed.
//
// Use this when the user explicitly removes an addressbook from their
// selection. The plain "DELETE FROM contact_source_addressbooks" path has
// no FK cascade into carddav_record_state — it would orphan every record
// row, which is the exact bug the 2b.2.b.1 writable-toggle save exposed.
func (s *Store) DeleteAddressbookByID(id string) error {
	if id == "" {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		DELETE FROM contact_records
		WHERE id IN (SELECT record_id FROM carddav_record_state WHERE addressbook_id = ?)
	`, id); err != nil {
		return fmt.Errorf("delete contact records: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM contact_source_addressbooks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete addressbook row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.log.Info().Str("id", id).Msg("Addressbook deleted")
	return nil
}

// ============================================================================
// Contact CRUD — Phase 2b.2.a
//
// As of migration 31, CardDAV contacts live in the unified contact_records
// schema rather than the legacy `carddav_contacts` table. These methods keep
// their public signatures (Contact struct return shape) so callers — the sync
// engine, the extension API, and tests — don't change. Internally:
//
//   - One contact_records row per vCard (identified by carddav_record_state.href
//     within an addressbook). The legacy "one row per email" fan-out is gone;
//     emails are now sub-rows in contact_emails.
//   - For back-compat, read methods fan results back out: a vCard with 3 emails
//     returns 3 *Contact rows (one per email). Each fan-out row carries the
//     SAME contact_records.id as Contact.ID; the extension API or sync caller
//     can group by ID if needed.
//   - Writes consolidate at upsert time: multiple UpsertContact calls with the
//     same (addressbook_id, href) converge on a single record_id.
//
// Two delete semantics are now distinct:
//   - DeleteContactByHref / DeleteContactsByHrefs: delete the entire RECORD
//     (and cascade-delete all its emails). Used by sync's delta-deletion path.
//   - DeleteContactsForAddressbook: delete all records for an addressbook.
// ============================================================================

// execQueryer is satisfied by both *sql.DB and *sql.Tx; lets upsertContactTx
// share logic across single-call and batched paths.
type execQueryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// UpsertContact creates or updates a CardDAV contact. Identity is
// (addressbook_id, href). Multiple calls with the same href but different
// emails accumulate on a single contact_records row.
//
// Back-compat: contact.ID is treated as the legacy synthetic id. On new-record
// insert, ID is reused as the record_id when non-empty (lets tests seed with
// explicit IDs). On subsequent calls for the same href, the caller's ID is
// OVERWRITTEN by the existing record_id.
func (s *Store) UpsertContact(contact *Contact) error {
	if contact.Email == "" {
		return fmt.Errorf("UpsertContact: email is required")
	}
	if contact.Href == "" {
		// Synthesize a per-row href for test-style seeds. Production sync
		// always sets href from the CardDAV server.
		if contact.ID == "" {
			contact.ID = uuid.New().String()
		}
		contact.Href = "/__synth__/" + contact.ID + ".vcf"
	}
	contact.SyncedAt = time.Now()
	return s.upsertContactTx(s.db, contact)
}

// upsertContactTx performs the actual upsert against either *sql.DB or *sql.Tx.
// Extracted so UpsertContactsBatch can share the logic inside a transaction.
func (s *Store) upsertContactTx(eq execQueryer, contact *Contact) error {
	// Look up existing record by (addressbook_id, href).
	var existingRecordID string
	err := eq.QueryRow(`
		SELECT record_id FROM carddav_record_state
		WHERE addressbook_id = ? AND href = ?
	`, contact.AddressbookID, contact.Href).Scan(&existingRecordID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// New record path.
		recordID := contact.ID
		if recordID == "" {
			recordID = uuid.New().String()
			contact.ID = recordID
		}
		if _, err := eq.Exec(`
			INSERT INTO contact_records (id, source, source_ref, fn, created_at, updated_at)
			VALUES (?, 'carddav', ?, ?, ?, ?)
		`, recordID, contact.AddressbookID, contact.DisplayName, contact.SyncedAt, contact.SyncedAt); err != nil {
			return fmt.Errorf("insert contact_records: %w", err)
		}
		if _, err := eq.Exec(`
			INSERT INTO carddav_record_state (record_id, addressbook_id, href, etag, synced_at)
			VALUES (?, ?, ?, ?, ?)
		`, recordID, contact.AddressbookID, contact.Href, contact.ETag, contact.SyncedAt); err != nil {
			return fmt.Errorf("insert carddav_record_state: %w", err)
		}
	case err != nil:
		return fmt.Errorf("lookup record by href: %w", err)
	default:
		// Existing record path — update fn/etag/synced_at and back-propagate id.
		contact.ID = existingRecordID
		if _, err := eq.Exec(`
			UPDATE contact_records SET fn = ?, updated_at = ? WHERE id = ?
		`, contact.DisplayName, contact.SyncedAt, existingRecordID); err != nil {
			return fmt.Errorf("update contact_records: %w", err)
		}
		if _, err := eq.Exec(`
			UPDATE carddav_record_state SET etag = ?, synced_at = ? WHERE record_id = ?
		`, contact.ETag, contact.SyncedAt, existingRecordID); err != nil {
			return fmt.Errorf("update carddav_record_state: %w", err)
		}
	}

	// Attach the email idempotently — same email twice is a no-op.
	if _, err := eq.Exec(`
		INSERT OR IGNORE INTO contact_emails (record_id, email, is_primary)
		VALUES (?, ?, 1)
	`, contact.ID, contact.Email); err != nil {
		return fmt.Errorf("attach contact_email: %w", err)
	}
	return nil
}

// RecordSyncEntry bundles a parsed multi-field contact record with the
// addressbook/href/etag triplet needed to write its carddav_record_state row.
// Used by UpsertRecordsBatch — the multi-field replacement for UpsertContactsBatch.
type RecordSyncEntry struct {
	Record        *contact.Record
	AddressbookID string
	Href          string
	ETag          string
}

// UpsertRecordsBatch is the Phase 2b.2.a multi-field replacement for
// UpsertContactsBatch. For each entry it:
//
//  1. Looks up an existing record by (addressbook_id, href) in
//     carddav_record_state; if found, reuses that record_id (so re-sync hits
//     UPDATE paths rather than creating duplicate records).
//  2. Calls contact.UpsertRecordTx to write the record + all sub-tables
//     (replacing existing sub-table rows wholesale; preserving per-email
//     send_count/last_used/name_overridden).
//  3. Upserts the carddav_record_state row with the new href/etag/synced_at.
//
// All entries in one transaction. Used by the sync engine to land batches of
// vCards from sync-collection/multiget.
func (s *Store) UpsertRecordsBatch(entries []RecordSyncEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	inserted := 0
	for _, e := range entries {
		if e.Record == nil {
			continue
		}
		if e.AddressbookID == "" || e.Href == "" {
			s.log.Warn().Msg("Skipping record with missing addressbook_id or href")
			continue
		}

		// Reuse existing record_id when (addressbook_id, href) matches; new
		// otherwise.
		var existingID string
		err := tx.QueryRow(`
			SELECT record_id FROM carddav_record_state
			WHERE addressbook_id = ? AND href = ?
		`, e.AddressbookID, e.Href).Scan(&existingID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if e.Record.ID == "" {
				e.Record.ID = uuid.New().String()
			}
		case err != nil:
			s.log.Warn().Err(err).Str("href", e.Href).Msg("Failed to look up existing record")
			continue
		default:
			e.Record.ID = existingID
		}

		e.Record.Source = "carddav"
		e.Record.SourceRef = e.AddressbookID

		if err := contact.UpsertRecordTx(tx, e.Record); err != nil {
			s.log.Warn().Err(err).Str("href", e.Href).Msg("Failed to upsert record in batch")
			continue
		}

		if _, err := tx.Exec(`
			INSERT INTO carddav_record_state (record_id, addressbook_id, href, etag, synced_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(record_id) DO UPDATE SET
				addressbook_id = excluded.addressbook_id,
				href = excluded.href,
				etag = excluded.etag,
				synced_at = excluded.synced_at
		`, e.Record.ID, e.AddressbookID, e.Href, e.ETag, now); err != nil {
			s.log.Warn().Err(err).Str("href", e.Href).Msg("Failed to upsert carddav_record_state")
			continue
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch upsert: %w", err)
	}
	s.log.Debug().Int("inserted", inserted).Int("total", len(entries)).Msg("Batch record upsert complete")
	return nil
}

// UpsertContactsBatch creates or updates multiple CardDAV contacts in a single
// transaction. Each contact goes through the same per-call upsert as
// UpsertContact; entries sharing (addressbook_id, href) consolidate.
//
// Deprecated: legacy fan-out shape (one *Contact per email). Use UpsertRecordsBatch
// for multi-field writes from the sync engine.
func (s *Store) UpsertContactsBatch(contacts []*Contact) error {
	if len(contacts) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	inserted := 0
	for _, c := range contacts {
		if c.Email == "" {
			s.log.Warn().Str("href", c.Href).Msg("Skipping batch contact with empty email")
			continue
		}
		if c.Href == "" {
			if c.ID == "" {
				c.ID = uuid.New().String()
			}
			c.Href = "/__synth__/" + c.ID + ".vcf"
		}
		c.SyncedAt = now
		if err := s.upsertContactTx(tx, c); err != nil {
			s.log.Warn().Err(err).Str("email", c.Email).Str("href", c.Href).Msg("Failed to upsert contact in batch")
			continue
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch upsert: %w", err)
	}
	s.log.Debug().Int("inserted", inserted).Int("total", len(contacts)).Msg("Batch upsert complete")
	return nil
}

// UpdateRecord PUTs the given record to its CardDAV server via the supplied
// Client (basic-auth already applied), then mirrors the server's accepted
// state locally:
//
//  1. Look up the record's state row (href, etag, addressbook_id, addressbook
//     path). Refuse if the record isn't a CardDAV record we know about.
//  2. Build the vCard via BuildVCard (preserves unknown properties from the
//     stored vcard_raw).
//  3. PutContact with If-Match: "<current etag>".
//  4. On success: call contact.UpsertRecordTx to replace sub-table rows from
//     the record's current shape, then update carddav_record_state with the
//     server's new ETag.
//
// On *ErrPreconditionFailed: local state is NOT mutated. The caller (the
// extension API) re-fetches the server's current vCard via FetchContactByPath,
// syncs locally, and surfaces a conflict event to the UI.
//
// Used by the Contacts extension's UpdateContact dispatch when the source is
// CardDAV-writable. The single-name Edit dialog in 2b.2.b.1 passes a record
// whose Fn has been updated; the rest of the record (emails, phones, etc.)
// comes from the local cache so they survive the rename verbatim.
func (s *Store) UpdateRecord(rec *contact.Record, client *Client) error {
	if rec == nil {
		return fmt.Errorf("UpdateRecord: nil record")
	}
	if rec.ID == "" {
		return fmt.Errorf("UpdateRecord: id is required")
	}
	if client == nil {
		return fmt.Errorf("UpdateRecord: nil client")
	}

	var href, etag, addressbookPath string
	err := s.db.QueryRow(`
		SELECT crs.href, COALESCE(crs.etag, ''), ab.path
		FROM carddav_record_state crs
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		WHERE crs.record_id = ?
	`, rec.ID).Scan(&href, &etag, &addressbookPath)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("UpdateRecord: no carddav state for record %s", rec.ID)
	}
	if err != nil {
		return fmt.Errorf("UpdateRecord: lookup state: %w", err)
	}

	card, err := BuildVCard(rec, rec.VCardRaw)
	if err != nil {
		return fmt.Errorf("UpdateRecord: build vcard: %w", err)
	}

	newETag, err := client.PutContact(addressbookPath, href, etag, false, card)
	if err != nil {
		// Includes *ErrPreconditionFailed unchanged for the caller to type-check.
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("UpdateRecord: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rec.Source = "carddav"
	if err := contact.UpsertRecordTx(tx, rec); err != nil {
		return fmt.Errorf("UpdateRecord: upsert local record: %w", err)
	}

	now := time.Now()
	if _, err := tx.Exec(`
		UPDATE carddav_record_state SET etag = ?, synced_at = ? WHERE record_id = ?
	`, newETag, now, rec.ID); err != nil {
		return fmt.Errorf("UpdateRecord: update state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("UpdateRecord: commit: %w", err)
	}
	s.log.Info().Str("id", rec.ID).Str("href", href).Msg("CardDAV record updated")
	return nil
}

// CreateRecord PUTs a new vCard to the given addressbook (If-None-Match: *) and
// inserts both the contact_records row and the carddav_record_state row on
// success. Mirrors the tail of UpdateRecord, but with INSERT semantics:
//   - href is synthesized as "<addressbookPath><uuid>.vcf"
//   - record id is rec.ID if pre-set, otherwise a fresh UUID
//   - rec.Source is forced to "carddav" and rec.SourceRef to the addressbook id
//   - server-side 412 means the resource already exists at that href (extremely
//     unlikely with a freshly-generated UUID; surfaced as *ErrPreconditionFailed
//     for the caller to handle).
//
// Returns the assigned record id on success.
func (s *Store) CreateRecord(addressbookID string, rec *contact.Record, client *Client) (string, error) {
	if rec == nil {
		return "", fmt.Errorf("CreateRecord: nil record")
	}
	if addressbookID == "" {
		return "", fmt.Errorf("CreateRecord: addressbook id is required")
	}
	if client == nil {
		return "", fmt.Errorf("CreateRecord: nil client")
	}

	ab, err := s.GetAddressbook(addressbookID)
	if err != nil {
		return "", fmt.Errorf("CreateRecord: lookup addressbook: %w", err)
	}
	if ab == nil {
		return "", fmt.Errorf("CreateRecord: addressbook %s not found", addressbookID)
	}

	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	rec.Source = "carddav"
	rec.SourceRef = addressbookID

	// Synthesize the href under the addressbook's path. The CardDAV server
	// accepts arbitrary <uuid>.vcf names within an addressbook collection.
	addressbookPath := ab.Path
	if !strings.HasSuffix(addressbookPath, "/") {
		addressbookPath += "/"
	}
	href := addressbookPath + rec.ID + ".vcf"

	// Empty originalRaw — BuildVCard synthesizes a minimal vCard 3.0 from
	// the record's fields. PHOTO and unknown-property preservation don't
	// apply to brand-new records.
	card, err := BuildVCard(rec, "")
	if err != nil {
		return "", fmt.Errorf("CreateRecord: build vcard: %w", err)
	}

	newETag, err := client.PutContact(addressbookPath, href, "", true, card)
	if err != nil {
		// Includes *ErrPreconditionFailed unchanged. On create that signals
		// the resource already exists at href — rare with a fresh UUID but
		// the caller can surface it cleanly.
		return "", err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", fmt.Errorf("CreateRecord: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := contact.UpsertRecordTx(tx, rec); err != nil {
		return "", fmt.Errorf("CreateRecord: upsert local record: %w", err)
	}

	now := time.Now()
	if _, err := tx.Exec(`
		INSERT INTO carddav_record_state (record_id, addressbook_id, href, etag, synced_at)
		VALUES (?, ?, ?, ?, ?)
	`, rec.ID, addressbookID, href, newETag, now); err != nil {
		return "", fmt.Errorf("CreateRecord: insert state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("CreateRecord: commit: %w", err)
	}
	s.log.Info().Str("id", rec.ID).Str("href", href).Str("addressbook", addressbookID).Msg("CardDAV record created")
	return rec.ID, nil
}

// DeleteRecord DELETEs the given record from its CardDAV server via the
// supplied Client, then cascade-deletes the local record (which removes the
// emails/phones/addresses/etc. sub-tables via FK ON DELETE CASCADE, plus the
// carddav_record_state row).
//
// On *ErrPreconditionFailed: local state is NOT mutated. The caller refreshes
// the local cache from the server's current state and surfaces a conflict.
//
// 404 from the server is treated as success (the resource is already gone —
// matches PutContact / DeleteContact's idempotency).
func (s *Store) DeleteRecord(recordID string, client *Client) error {
	if recordID == "" {
		return fmt.Errorf("DeleteRecord: id is required")
	}
	if client == nil {
		return fmt.Errorf("DeleteRecord: nil client")
	}

	var href, etag, addressbookPath string
	err := s.db.QueryRow(`
		SELECT crs.href, COALESCE(crs.etag, ''), ab.path
		FROM carddav_record_state crs
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		WHERE crs.record_id = ?
	`, recordID).Scan(&href, &etag, &addressbookPath)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("DeleteRecord: no carddav state for record %s", recordID)
	}
	if err != nil {
		return fmt.Errorf("DeleteRecord: lookup state: %w", err)
	}

	if err := client.DeleteContact(addressbookPath, href, etag); err != nil {
		return err
	}

	if _, err := s.db.Exec(`DELETE FROM contact_records WHERE id = ?`, recordID); err != nil {
		return fmt.Errorf("DeleteRecord: delete local: %w", err)
	}
	s.log.Info().Str("id", recordID).Str("href", href).Msg("CardDAV record deleted")
	return nil
}

// RefreshRecordFromServer is the 412-recovery helper: after a precondition
// failure, the caller refetches the server's current vCard and syncs locally
// so the next read reflects what the server actually has. The record's
// addressbook_id + href are resolved from carddav_record_state; the fetched
// ParsedRecord is upserted via contact.UpsertRecordTx with full sub-table
// replacement.
func (s *Store) RefreshRecordFromServer(recordID string, client *Client) error {
	if recordID == "" {
		return fmt.Errorf("RefreshRecordFromServer: id is required")
	}
	if client == nil {
		return fmt.Errorf("RefreshRecordFromServer: nil client")
	}
	var addressbookID, href, addressbookPath string
	err := s.db.QueryRow(`
		SELECT crs.addressbook_id, crs.href, ab.path
		FROM carddav_record_state crs
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		WHERE crs.record_id = ?
	`, recordID).Scan(&addressbookID, &href, &addressbookPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("RefreshRecordFromServer: lookup state: %w", err)
	}

	parsed, err := client.FetchContactByPath(addressbookPath, href)
	if err != nil {
		return fmt.Errorf("RefreshRecordFromServer: fetch: %w", err)
	}
	if parsed == nil {
		return nil
	}

	rec := ParsedRecordToContactRecord(parsed, recordID, addressbookID)
	return s.UpsertRecordsBatch([]RecordSyncEntry{{
		Record:        rec,
		AddressbookID: addressbookID,
		Href:          href,
		ETag:          parsed.ETag,
	}})
}

// ParsedRecordToContactRecord converts a parser output into the rich
// contact.Record shape the unified store consumes. Exported so the
// extension API can build records from a server fetch when handling
// conflicts. recordID is the existing local UUID (preserved across
// re-fetches); addressbookID is the source_ref.
func ParsedRecordToContactRecord(p *ParsedRecord, recordID, addressbookID string) *contact.Record {
	if p == nil {
		return nil
	}
	rec := &contact.Record{
		ID:        recordID,
		Source:    "carddav",
		SourceRef: addressbookID,
		Fn:        p.FN,
		NGiven:    p.NGiven,
		NFamily:   p.NFamily,
		Org:       p.Org,
		Title:     p.Title,
		Note:      p.Note,
		Bday:      p.Bday,
		Nickname:  p.Nickname,
		PhotoData:      p.PhotoData,
		PhotoMediaType: p.PhotoMediaType,
		PhotoURL:       p.PhotoURL,
		VCardRaw:  p.VCardRaw,
		Categories: p.Categories,
	}
	for _, e := range p.Emails {
		rec.Emails = append(rec.Emails, contact.RecordEmail{
			Email:     e.Value,
			EmailType: e.Type,
			IsPrimary: e.IsPrimary,
		})
	}
	for _, ph := range p.Phones {
		rec.Phones = append(rec.Phones, contact.RecordPhone{
			Number:    ph.Value,
			PhoneType: ph.Type,
			IsPrimary: ph.IsPrimary,
		})
	}
	for _, a := range p.Addresses {
		rec.Addresses = append(rec.Addresses, contact.RecordAddress{
			AddrType: a.Type,
			Street:   a.Street,
			City:     a.City,
			Region:   a.Region,
			Postcode: a.Postcode,
			Country:  a.Country,
		})
	}
	for _, u := range p.URLs {
		rec.URLs = append(rec.URLs, contact.RecordURL{
			URL:     u.Value,
			URLType: u.Type,
		})
	}
	for _, i := range p.IMPPs {
		rec.IMPPs = append(rec.IMPPs, contact.RecordIMPP{
			Handle:   i.Handle,
			IMPPType: i.Type,
		})
	}
	return rec
}

// DeleteContactsForAddressbook deletes all CardDAV contact records belonging to
// an addressbook. Cascades to contact_emails and carddav_record_state via FK.
func (s *Store) DeleteContactsForAddressbook(addressbookID string) error {
	_, err := s.db.Exec(`
		DELETE FROM contact_records
		WHERE source = 'carddav'
		  AND id IN (SELECT record_id FROM carddav_record_state WHERE addressbook_id = ?)
	`, addressbookID)
	return err
}

// DeleteContactByHref deletes a CardDAV contact (entire record) by its href.
// Cascades to contact_emails and carddav_record_state.
func (s *Store) DeleteContactByHref(addressbookID, href string) error {
	_, err := s.db.Exec(`
		DELETE FROM contact_records
		WHERE id IN (
			SELECT record_id FROM carddav_record_state
			WHERE addressbook_id = ? AND href = ?
		)
	`, addressbookID, href)
	return err
}

// DeleteContactsByHrefs deletes multiple CardDAV records by href in one transaction.
func (s *Store) DeleteContactsByHrefs(addressbookID string, hrefs []string) error {
	if len(hrefs) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		DELETE FROM contact_records
		WHERE id IN (
			SELECT record_id FROM carddav_record_state
			WHERE addressbook_id = ? AND href = ?
		)
	`)
	if err != nil {
		return fmt.Errorf("prepare delete: %w", err)
	}
	defer stmt.Close()

	for _, href := range hrefs {
		if _, err := stmt.Exec(addressbookID, href); err != nil {
			return fmt.Errorf("delete contact with href %s: %w", href, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch delete: %w", err)
	}
	s.log.Debug().Int("count", len(hrefs)).Msg("Batch delete complete")
	return nil
}

// SearchContacts searches CardDAV contacts by query. Returns one *Contact per
// (record, email) pair — fan-out preserved for caller compatibility. Only
// contacts from enabled sources + addressbooks are returned.
func (s *Store) SearchContacts(query string, limit int) ([]*Contact, error) {
	if limit <= 0 {
		limit = 10
	}
	pattern := "%" + strings.ToLower(query) + "%"

	sqlQuery := `
		SELECT cr.id, crs.addressbook_id, ce.email, COALESCE(cr.fn, ''),
		       crs.href, COALESCE(crs.etag, ''), COALESCE(crs.synced_at, cr.updated_at)
		FROM contact_records cr
		JOIN carddav_record_state crs ON crs.record_id = cr.id
		JOIN contact_emails ce ON ce.record_id = cr.id
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		JOIN contact_sources s ON s.id = ab.source_id
		WHERE cr.source = 'carddav'
		  AND s.enabled = 1 AND ab.enabled = 1
		  AND (LOWER(ce.email) LIKE ? OR LOWER(COALESCE(cr.fn, '')) LIKE ?)
		ORDER BY cr.fn ASC, ce.email ASC
		LIMIT ?
	`
	return s.scanContactRows(sqlQuery, pattern, pattern, limit)
}

// ListContactsPaged returns contacts for a single source in fn order, with
// offset/limit paging and optional case-insensitive query filter. Only enabled
// sources + addressbooks visible. Fan-out shape preserved.
func (s *Store) ListContactsPaged(sourceID, query string, offset, limit int) ([]*Contact, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	sqlQuery := `
		SELECT cr.id, crs.addressbook_id, ce.email, COALESCE(cr.fn, ''),
		       crs.href, COALESCE(crs.etag, ''), COALESCE(crs.synced_at, cr.updated_at)
		FROM contact_records cr
		JOIN carddav_record_state crs ON crs.record_id = cr.id
		JOIN contact_emails ce ON ce.record_id = cr.id
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		JOIN contact_sources s ON s.id = ab.source_id
		WHERE cr.source = 'carddav'
		  AND s.id = ? AND s.enabled = 1 AND ab.enabled = 1
		  AND (? = '' OR LOWER(ce.email) LIKE ? OR LOWER(COALESCE(cr.fn, '')) LIKE ?)
		ORDER BY cr.fn ASC, ce.email ASC
		LIMIT ? OFFSET ?
	`
	pattern := "%" + strings.ToLower(query) + "%"
	return s.scanContactRows(sqlQuery, sourceID, query, pattern, pattern, limit, offset)
}

// ListRecordIDsForSource returns the contact_record IDs belonging to a
// CardDAV source, in fn ASC order with offset/limit paging. The caller
// (typically the extension API) hydrates each id via contact.Store.GetRecord
// to populate the multi-field record shape.
//
// Visibility filtering matches SearchContacts: only contacts from enabled
// sources + addressbooks. Empty `query` returns everything. Non-empty `query`
// case-insensitively matches against fn OR any email belonging to the record.
//
// Phase 2b.2.a — used by the Contacts pane's per-source listing to fix the
// duplicate-row UX wart (one record per vCard, not one row per email).
func (s *Store) ListRecordIDsForSource(sourceID, query string, offset, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	pattern := "%" + strings.ToLower(query) + "%"

	sqlQuery := `
		SELECT DISTINCT cr.id, COALESCE(cr.fn, '')
		FROM contact_records cr
		JOIN carddav_record_state crs ON crs.record_id = cr.id
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		JOIN contact_sources s ON s.id = ab.source_id
		WHERE cr.source = 'carddav'
		  AND s.id = ? AND s.enabled = 1 AND ab.enabled = 1
		  AND (
		    ? = ''
		    OR LOWER(COALESCE(cr.fn, '')) LIKE ?
		    OR cr.id IN (SELECT record_id FROM contact_emails WHERE LOWER(email) LIKE ?)
		  )
		ORDER BY COALESCE(cr.fn, '') ASC, cr.id ASC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.Query(sqlQuery, sourceID, query, pattern, pattern, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list record ids for source %s: %w", sourceID, err)
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id, fn string
		if err := rows.Scan(&id, &fn); err != nil {
			s.log.Warn().Err(err).Msg("Failed to scan record id row")
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// scanContactRows runs a SELECT producing the standard (id, addressbook_id,
// email, fn, href, etag, synced_at) shape and returns *Contact rows.
//
// synced_at is scanned as sql.NullTime because COALESCE(crs.synced_at, cr.updated_at)
// can produce a TEXT result that the SQLite driver doesn't always convert to
// time.Time cleanly. sql.NullTime accepts both.
func (s *Store) scanContactRows(sqlQuery string, args ...any) ([]*Contact, error) {
	rows, err := s.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("contacts query failed: %w", err)
	}
	defer rows.Close()

	var contacts []*Contact
	for rows.Next() {
		var c Contact
		var syncedAt sql.NullString
		if err := rows.Scan(&c.ID, &c.AddressbookID, &c.Email, &c.DisplayName, &c.Href, &c.ETag, &syncedAt); err != nil {
			s.log.Warn().Err(err).Msg("Failed to scan contact row")
			continue
		}
		if syncedAt.Valid {
			c.SyncedAt = parseSyncedAt(syncedAt.String)
		}
		contacts = append(contacts, &c)
	}
	return contacts, rows.Err()
}

// GetContactByID returns a CardDAV contact by record_id. Returns one
// representative (record, primary-email) pair. Returns (nil, nil) when not found.
func (s *Store) GetContactByID(id string) (*Contact, error) {
	query := `
		SELECT cr.id, crs.addressbook_id, ce.email, COALESCE(cr.fn, ''),
		       crs.href, COALESCE(crs.etag, ''), COALESCE(crs.synced_at, cr.updated_at)
		FROM contact_records cr
		JOIN carddav_record_state crs ON crs.record_id = cr.id
		JOIN contact_emails ce ON ce.record_id = cr.id
		WHERE cr.id = ? AND cr.source = 'carddav'
		ORDER BY ce.is_primary DESC, ce.email ASC
		LIMIT 1
	`
	var c Contact
	var syncedAt sql.NullString
	err := s.db.QueryRow(query, id).Scan(&c.ID, &c.AddressbookID, &c.Email, &c.DisplayName, &c.Href, &c.ETag, &syncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get contact by id: %w", err)
	}
	if syncedAt.Valid {
		c.SyncedAt = parseSyncedAt(syncedAt.String)
	}
	return &c, nil
}

// GetContactByEmail returns the most-recently-synced CardDAV contact matching
// the given email across all enabled sources + addressbooks. Returns (nil, nil)
// on no match.
//
// If the same email is on multiple records, the most recently synced one wins
// (ORDER BY synced_at DESC). Used by the Contacts extension's "All" view to
// resolve emails that came in via the search merge.
func (s *Store) GetContactByEmail(email string) (*Contact, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, nil
	}
	query := `
		SELECT cr.id, crs.addressbook_id, ce.email, COALESCE(cr.fn, ''),
		       crs.href, COALESCE(crs.etag, ''), COALESCE(crs.synced_at, cr.updated_at)
		FROM contact_records cr
		JOIN carddav_record_state crs ON crs.record_id = cr.id
		JOIN contact_emails ce ON ce.record_id = cr.id
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		JOIN contact_sources s ON s.id = ab.source_id
		WHERE cr.source = 'carddav'
		  AND s.enabled = 1 AND ab.enabled = 1
		  AND LOWER(ce.email) = ?
		ORDER BY crs.synced_at DESC, cr.updated_at DESC
		LIMIT 1
	`
	var c Contact
	var syncedAt sql.NullString
	err := s.db.QueryRow(query, email).Scan(&c.ID, &c.AddressbookID, &c.Email, &c.DisplayName, &c.Href, &c.ETag, &syncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get contact by email: %w", err)
	}
	if syncedAt.Valid {
		c.SyncedAt = parseSyncedAt(syncedAt.String)
	}
	return &c, nil
}

// GetContactByHref returns a contact by its href within an addressbook. Used
// by sync to check the existing record/etag before updating. Returns one
// representative (record, primary-email) pair.
func (s *Store) GetContactByHref(addressbookID, href string) (*Contact, error) {
	query := `
		SELECT cr.id, crs.addressbook_id, ce.email, COALESCE(cr.fn, ''),
		       crs.href, COALESCE(crs.etag, ''), COALESCE(crs.synced_at, cr.updated_at)
		FROM contact_records cr
		JOIN carddav_record_state crs ON crs.record_id = cr.id
		JOIN contact_emails ce ON ce.record_id = cr.id
		WHERE crs.addressbook_id = ? AND crs.href = ?
		ORDER BY ce.is_primary DESC, ce.email ASC
		LIMIT 1
	`
	var c Contact
	var syncedAt sql.NullString
	err := s.db.QueryRow(query, addressbookID, href).Scan(&c.ID, &c.AddressbookID, &c.Email, &c.DisplayName, &c.Href, &c.ETag, &syncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get contact by href: %w", err)
	}
	if syncedAt.Valid {
		c.SyncedAt = parseSyncedAt(syncedAt.String)
	}
	return &c, nil
}

// CountContacts returns the total number of CardDAV contact records (one count
// per vCard regardless of how many emails it carries).
func (s *Store) CountContacts() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM contact_records WHERE source = 'carddav'`).Scan(&count)
	return count, err
}

// CountContactsForSource returns the number of CardDAV records for a source.
func (s *Store) CountContactsForSource(sourceID string) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM contact_records cr
		JOIN carddav_record_state crs ON crs.record_id = cr.id
		JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		WHERE ab.source_id = ? AND cr.source = 'carddav'
	`
	var count int
	err := s.db.QueryRow(query, sourceID).Scan(&count)
	return count, err
}

// GetSourceForAddressbook returns the source that owns the given addressbook
// via JOIN. Used by the Contacts extension's write dispatch (Phase 2b.2.b) to
// gate writes on the source's `writable` flag and look up `username` / `url`
// for the basic-auth CardDAV client. Returns (nil, nil) when the addressbook
// has been deleted out from under the caller.
func (s *Store) GetSourceForAddressbook(addressbookID string) (*Source, error) {
	if addressbookID == "" {
		return nil, nil
	}
	query := `
		SELECT s.id, s.name, s.type, s.url, s.username, s.account_id, s.enabled, s.writable, s.sync_interval,
		       s.last_synced_at, s.last_error, s.last_error_at, s.created_at
		FROM contact_sources s
		JOIN contact_source_addressbooks ab ON ab.source_id = s.id
		WHERE ab.id = ?
	`
	var source Source
	var lastSyncedAt, lastErrorAt sql.NullTime
	var lastError, accountID sql.NullString
	err := s.db.QueryRow(query, addressbookID).Scan(
		&source.ID, &source.Name, &source.Type, &source.URL, &source.Username,
		&accountID, &source.Enabled, &source.Writable, &source.SyncInterval,
		&lastSyncedAt, &lastError, &lastErrorAt, &source.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get source for addressbook: %w", err)
	}
	if accountID.Valid {
		source.AccountID = &accountID.String
	}
	if lastSyncedAt.Valid {
		source.LastSyncedAt = &lastSyncedAt.Time
	}
	if lastError.Valid {
		source.LastError = lastError.String
	}
	if lastErrorAt.Valid {
		source.LastErrorAt = &lastErrorAt.Time
	}
	return &source, nil
}

// GetSourceByAccountID returns a contact source linked to an email account
func (s *Store) GetSourceByAccountID(accountID string) (*Source, error) {
	query := `
		SELECT id, name, type, url, username, account_id, enabled, writable, sync_interval,
		       last_synced_at, last_error, last_error_at, created_at
		FROM contact_sources
		WHERE account_id = ?
	`

	var source Source
	var lastSyncedAt, lastErrorAt sql.NullTime
	var lastError, accID sql.NullString

	err := s.db.QueryRow(query, accountID).Scan(
		&source.ID, &source.Name, &source.Type, &source.URL, &source.Username,
		&accID, &source.Enabled, &source.Writable, &source.SyncInterval,
		&lastSyncedAt, &lastError, &lastErrorAt, &source.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get source by account ID: %w", err)
	}

	if accID.Valid {
		source.AccountID = &accID.String
	}
	if lastSyncedAt.Valid {
		source.LastSyncedAt = &lastSyncedAt.Time
	}
	if lastError.Valid {
		source.LastError = lastError.String
	}
	if lastErrorAt.Valid {
		source.LastErrorAt = &lastErrorAt.Time
	}

	return &source, nil
}

// parseSyncedAt parses a synced_at string from SQLite into a time.Time. The
// SQLite driver may return DATETIME columns as either time.Time (when stored
// via Go time.Time) or RFC3339 / "YYYY-MM-DD HH:MM:SS" strings (when stored
// via CURRENT_TIMESTAMP or COALESCEd with a TEXT column). This helper tries
// both formats and returns the zero time on parse failure.
func parseSyncedAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
