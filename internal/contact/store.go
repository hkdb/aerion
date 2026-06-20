// Package contact provides contact management for email autocomplete.
//
// As of migration 31, this package operates on the unified contact-record schema
// (`contact_records` + `contact_emails` + multi-field sub-tables). The public API
// surface (Search/AddOrUpdate/Get/Delete/UpdateName/Create/List/ListByKind/Count)
// is preserved verbatim across the migration so mail's compose/autocomplete code
// paths are unchanged. Internals are rewritten to query the unified tables.
package contact

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// ErrContactExists is returned by Create when the email is already present
// in the unified schema (under any record). The frontend maps this to a
// "this contact already exists" message and offers the Edit flow instead.
var ErrContactExists = errors.New("contact already exists")

// Store handles contact storage and retrieval against the unified
// contact-record schema. The filesystem-based VCardScanner is held as an
// optional secondary autocomplete source (unchanged by migration 31).
type Store struct {
	db           *sql.DB
	vcardScanner *VCardScanner
	log          zerolog.Logger
}

// NewStore creates a new contact store.
func NewStore(db *sql.DB) *Store {
	return &Store{
		db:  db,
		log: logging.WithComponent("contact"),
	}
}

// SetVCardScanner attaches a filesystem-based vCard cache as a secondary
// autocomplete source. Existing behavior preserved across migration 31.
func (s *Store) SetVCardScanner(scanner *VCardScanner) {
	s.vcardScanner = scanner
}

// Search returns contacts matching the query for autocomplete. The unified
// tables already include both local AND CardDAV records, so a single SQL
// query covers both — the legacy CardDAVSearchFunc bridge is no longer needed.
// The optional VCardScanner is still consulted for filesystem-based .vcf
// files (unchanged).
//
// Ranked by: send count > recency > source priority. Public signature preserved.
func (s *Store) Search(query string, limit int) ([]*Contact, error) {
	if limit <= 0 {
		limit = 10
	}

	unified, err := s.searchUnified(query, limit)
	if err != nil {
		s.log.Warn().Err(err).Msg("Failed to search unified contacts")
		unified = []*Contact{}
	}

	var vcardContacts []*Contact
	if s.vcardScanner != nil {
		vcardContacts, err = s.vcardScanner.Search(query, limit)
		if err != nil {
			s.log.Warn().Err(err).Msg("Failed to search vCard contacts")
			vcardContacts = []*Contact{}
		}
		if len(unified)+len(vcardContacts) < 3 {
			s.vcardScanner.RefreshIfNeeded()
		}
	}

	merged := MergeResults(unified, vcardContacts)
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// searchUnified queries the unified contact_records + contact_emails tables.
// Returns one *Contact per matching (record, email) pair.
//
// Visibility rules (match the legacy carddav-search bridge's behavior):
//   - Local records (source='local') are always visible.
//   - CardDAV records are visible only when their carddav_record_state row
//     resolves to an ENABLED addressbook under an ENABLED source. Orphaned
//     records (state row missing, addressbook deleted, source deleted) or
//     disabled-source records are filtered OUT so they don't surface in the
//     "All" view but stay invisible under their (disabled/missing) source.
func (s *Store) searchUnified(query string, limit int) ([]*Contact, error) {
	pattern := "%" + strings.ToLower(query) + "%"
	sqlQuery := `
		SELECT ce.email, COALESCE(cr.fn, ''), cr.source, ce.send_count, ce.last_used, cr.created_at
		FROM contact_emails ce
		JOIN contact_records cr ON ce.record_id = cr.id
		WHERE (LOWER(ce.email) LIKE ? OR LOWER(COALESCE(cr.fn, '')) LIKE ?)
		  AND (
		    cr.source = 'local'
		    OR EXISTS (
		      SELECT 1 FROM carddav_record_state crs
		      JOIN contact_source_addressbooks ab ON ab.id = crs.addressbook_id
		      JOIN contact_sources s ON s.id = ab.source_id
		      WHERE crs.record_id = cr.id AND s.enabled = 1 AND ab.enabled = 1
		    )
		  )
		ORDER BY ce.send_count DESC, ce.last_used DESC
		LIMIT ?
	`

	rows, err := s.db.Query(sqlQuery, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search unified contacts: %w", err)
	}
	defer rows.Close()

	var contacts []*Contact
	for rows.Next() {
		var c Contact
		var source string
		var lastUsed, createdAt sql.NullTime
		if err := rows.Scan(&c.Email, &c.DisplayName, &source, &c.SendCount, &lastUsed, &createdAt); err != nil {
			s.log.Warn().Err(err).Msg("Failed to scan contact row")
			continue
		}
		if lastUsed.Valid {
			c.LastUsed = lastUsed.Time
		}
		if createdAt.Valid {
			c.CreatedAt = createdAt.Time
		}
		c.Source = mapSourceForLegacy(source)
		contacts = append(contacts, &c)
	}
	return contacts, nil
}

// mapSourceForLegacy maps the unified `source` column values onto the legacy
// Contact.Source string. The frontend, MergeResults, and a couple of older
// callers expect "aerion" for local contacts — preserving the mapping keeps
// those callers unchanged.
func mapSourceForLegacy(source string) string {
	if source == "local" {
		return "aerion"
	}
	return source
}

// AddOrUpdate inserts or updates a contact for the given email. Called by mail's
// compose flow to record sent-mail recipients. Public signature preserved.
//
// Insert path: creates a new contact_records row (source='local', kind='collected')
// + a contact_emails row pointing at it. send_count starts at 1.
//
// Update path (email already exists): bumps send_count + last_used. The record's
// fn is updated ONLY if no contact_emails row for that record has name_overridden=1
// (preserves user-edited names) AND the new display name is non-empty. Kind is
// NOT touched on conflict — a manual contact stays manual even after mail-send.
func (s *Store) AddOrUpdate(email, displayName string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}

	now := time.Now()

	// Find the highest-ranked existing record for this email (if any).
	var recordID string
	err := s.db.QueryRow(`
		SELECT record_id FROM contact_emails
		WHERE email = ?
		ORDER BY send_count DESC, last_used DESC
		LIMIT 1
	`, email).Scan(&recordID)

	if errors.Is(err, sql.ErrNoRows) {
		// Brand new email — create a local-collected record + email pair.
		// Record id is a UUID (matches CardDAV — vCard UID semantics), NOT
		// derived from email. The email lives in contact_emails as a fully-
		// editable sub-row so future Edit UI can rename it without losing
		// autocomplete metadata.
		recordID = uuid.New().String()
		if _, err := s.db.Exec(`
			INSERT INTO contact_records (id, source, kind, fn, created_at, updated_at)
			VALUES (?, 'local', 'collected', ?, ?, ?)
		`, recordID, displayName, now, now); err != nil {
			return fmt.Errorf("failed to insert contact_records: %w", err)
		}
		if _, err := s.db.Exec(`
			INSERT INTO contact_emails (record_id, email, send_count, last_used, name_overridden, is_primary)
			VALUES (?, ?, 1, ?, 0, 1)
		`, recordID, email, now); err != nil {
			return fmt.Errorf("failed to insert contact_emails: %w", err)
		}
		s.log.Debug().Str("email", email).Str("name", displayName).Msg("Contact created (collected)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to look up existing email: %w", err)
	}

	// Existing row — bump send_count + last_used.
	if _, err := s.db.Exec(`
		UPDATE contact_emails
		SET send_count = send_count + 1, last_used = ?
		WHERE record_id = ? AND email = ?
	`, now, recordID, email); err != nil {
		return fmt.Errorf("failed to update contact_emails: %w", err)
	}

	// Update fn ONLY if not user-overridden AND new name is non-empty.
	if displayName != "" {
		if _, err := s.db.Exec(`
			UPDATE contact_records
			SET fn = ?, updated_at = ?
			WHERE id = ?
			  AND NOT EXISTS (
				SELECT 1 FROM contact_emails ce
				WHERE ce.record_id = contact_records.id AND ce.name_overridden = 1
			  )
		`, displayName, now, recordID); err != nil {
			return fmt.Errorf("failed to update contact_records.fn: %w", err)
		}
	}

	s.log.Debug().Str("email", email).Msg("Contact updated (auto-collected)")
	return nil
}

// UpdateName sets a user-edited display name and marks the contact's email as
// name_overridden so future AddOrUpdate calls (auto-collection) won't clobber.
// Public signature preserved.
//
// Errors if no contact with the given email exists.
func (s *Store) UpdateName(email, newName string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	newName = strings.TrimSpace(newName)
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}

	var recordID string
	err := s.db.QueryRow(`SELECT record_id FROM contact_emails WHERE email = ? LIMIT 1`, email).Scan(&recordID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("contact not found: %s", email)
	}
	if err != nil {
		return fmt.Errorf("failed to look up email: %w", err)
	}

	now := time.Now()
	if _, err := s.db.Exec(`UPDATE contact_records SET fn = ?, updated_at = ? WHERE id = ?`, newName, now, recordID); err != nil {
		return fmt.Errorf("failed to update fn: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE contact_emails SET name_overridden = 1 WHERE record_id = ? AND email = ?`, recordID, email); err != nil {
		return fmt.Errorf("failed to set name_overridden: %w", err)
	}
	s.log.Debug().Str("email", email).Str("name", newName).Msg("Local contact name updated (user-overridden)")
	return nil
}

// Create inserts a new manually-added local contact. Used by the Add Contact UI.
// Sets kind='manual', name_overridden=1, send_count=0. Errors with
// ErrContactExists if the email is already present under any record.
//
// Public signature preserved.
func (s *Store) Create(email, displayName string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}

	// Check for existing email — any source. Conflict prevents the manual create.
	var existing string
	err := s.db.QueryRow(`SELECT record_id FROM contact_emails WHERE email = ? LIMIT 1`, email).Scan(&existing)
	if err == nil {
		return ErrContactExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to check existing email: %w", err)
	}

	now := time.Now()
	// Record id is a UUID, NOT derived from email (matches CardDAV — vCard
	// UID semantics). The email goes in contact_emails as a fully-editable
	// sub-row.
	recordID := uuid.New().String()
	if _, err := s.db.Exec(`
		INSERT INTO contact_records (id, source, kind, fn, created_at, updated_at)
		VALUES (?, 'local', 'manual', ?, ?, ?)
	`, recordID, displayName, now, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "constraint") {
			return ErrContactExists
		}
		return fmt.Errorf("failed to insert contact_records: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO contact_emails (record_id, email, send_count, last_used, name_overridden, is_primary)
		VALUES (?, ?, 0, NULL, 1, 1)
	`, recordID, email); err != nil {
		// Roll back the records row to keep state consistent on failure.
		_, _ = s.db.Exec(`DELETE FROM contact_records WHERE id = ?`, recordID)
		return fmt.Errorf("failed to insert contact_emails: %w", err)
	}

	s.log.Debug().Str("email", email).Str("name", displayName).Msg("Local contact created (manual)")
	return nil
}

// DeleteRecord removes a contact record by ID. Cascades to contact_emails and
// all sub-tables via FK ON DELETE CASCADE. Used by the extension API when the
// caller has a record id (UUID — both local and CardDAV records share the
// vCard-UID identity shape as of migration 32). The older Delete(email)
// method stays for back-compat with mail-side callers.
func (s *Store) DeleteRecord(id string) error {
	if id == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM contact_records WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete contact_records: %w", err)
	}
	s.log.Debug().Str("id", id).Msg("Contact record deleted")
	return nil
}

// UpdateRecordName sets the display name on a record and marks ALL emails on
// it as name_overridden, so future AddOrUpdate calls don't clobber the edit
// for any of them. Used by the extension API when the caller has a record id.
//
// Returns nil silently when the record doesn't exist (idempotent).
func (s *Store) UpdateRecordName(id, newName string) error {
	if id == "" {
		return nil
	}
	newName = strings.TrimSpace(newName)
	now := time.Now()
	if _, err := s.db.Exec(`UPDATE contact_records SET fn = ?, updated_at = ? WHERE id = ?`, newName, now, id); err != nil {
		return fmt.Errorf("update contact_records.fn: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE contact_emails SET name_overridden = 1 WHERE record_id = ?`, id); err != nil {
		return fmt.Errorf("set name_overridden: %w", err)
	}
	return nil
}

// AddFromSentMail adds contacts from a sent email's recipients. Public signature preserved.
func (s *Store) AddFromSentMail(recipients []struct{ Email, Name string }) error {
	for _, r := range recipients {
		if err := s.AddOrUpdate(r.Email, r.Name); err != nil {
			s.log.Warn().Err(err).
				Str("email", r.Email).
				Msg("Failed to add contact from sent mail")
		}
	}
	return nil
}

// Get returns a contact by email (best match by send_count/last_used). Returns
// (nil, nil) on miss. Public signature preserved.
func (s *Store) Get(email string) (*Contact, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT ce.email, COALESCE(cr.fn, ''), cr.source, COALESCE(cr.kind, ''), ce.send_count, ce.last_used, cr.created_at
		FROM contact_emails ce
		JOIN contact_records cr ON ce.record_id = cr.id
		WHERE ce.email = ?
		ORDER BY ce.send_count DESC, ce.last_used DESC
		LIMIT 1
	`, email)

	var c Contact
	var source, kind string
	var lastUsed, createdAt sql.NullTime
	err := row.Scan(&c.Email, &c.DisplayName, &source, &kind, &c.SendCount, &lastUsed, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get contact: %w", err)
	}
	if lastUsed.Valid {
		c.LastUsed = lastUsed.Time
	}
	if createdAt.Valid {
		c.CreatedAt = createdAt.Time
	}
	c.Source = mapSourceForLegacy(source)
	c.Kind = kind
	return &c, nil
}

// Delete removes the email's contact_emails row. If the parent record is empty
// AND local-collected, the record is also deleted; manual and CardDAV records
// survive (so the user can re-link emails). Public signature preserved.
func (s *Store) Delete(email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}

	// Look up all records this email is attached to.
	rows, err := s.db.Query(`SELECT record_id FROM contact_emails WHERE email = ?`, email)
	if err != nil {
		return fmt.Errorf("failed to look up email: %w", err)
	}
	var recordIDs []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			return fmt.Errorf("failed to scan record_id: %w", scanErr)
		}
		recordIDs = append(recordIDs, id)
	}
	rows.Close()
	if len(recordIDs) == 0 {
		return nil
	}

	if _, err := s.db.Exec(`DELETE FROM contact_emails WHERE email = ?`, email); err != nil {
		return fmt.Errorf("failed to delete email: %w", err)
	}

	// Cascade-clean local-collected records that are now empty.
	for _, recordID := range recordIDs {
		var remaining int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM contact_emails WHERE record_id = ?`, recordID).Scan(&remaining); err != nil {
			continue
		}
		if remaining > 0 {
			continue
		}
		var source, kind string
		if err := s.db.QueryRow(`SELECT source, COALESCE(kind, '') FROM contact_records WHERE id = ?`, recordID).Scan(&source, &kind); err != nil {
			continue
		}
		if source == "local" && kind == "collected" {
			_, _ = s.db.Exec(`DELETE FROM contact_records WHERE id = ?`, recordID)
		}
	}
	return nil
}

// List returns local contacts (one row per email), optionally limited.
// Public signature preserved.
func (s *Store) List(limit int) ([]*Contact, error) {
	return s.ListByKind("", limit)
}

// ListByKind returns local contacts filtered by their kind. Pass "" for no
// filter. Valid kinds: "manual" (user-added), "collected" (auto-collected).
// Public signature preserved.
//
// Returns local-source rows only (records with source='local'). CardDAV
// records are queried via carddav.Store.
func (s *Store) ListByKind(kind string, limit int) ([]*Contact, error) {
	args := []any{}
	query := `
		SELECT ce.email, COALESCE(cr.fn, ''), cr.source, COALESCE(cr.kind, ''), ce.send_count, ce.last_used, cr.created_at
		FROM contact_emails ce
		JOIN contact_records cr ON ce.record_id = cr.id
		WHERE cr.source = 'local'
	`
	if kind != "" {
		query += ` AND cr.kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY ce.send_count DESC, ce.last_used DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list contacts: %w", err)
	}
	defer rows.Close()

	var contacts []*Contact
	for rows.Next() {
		var c Contact
		var source, k string
		var lastUsed, createdAt sql.NullTime
		if err := rows.Scan(&c.Email, &c.DisplayName, &source, &k, &c.SendCount, &lastUsed, &createdAt); err != nil {
			continue
		}
		if lastUsed.Valid {
			c.LastUsed = lastUsed.Time
		}
		if createdAt.Valid {
			c.CreatedAt = createdAt.Time
		}
		c.Source = mapSourceForLegacy(source)
		c.Kind = k
		contacts = append(contacts, &c)
	}
	return contacts, nil
}

// Count returns the total number of contact_emails rows. Public signature preserved.
func (s *Store) Count() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM contact_emails`).Scan(&count)
	return count, err
}

// MergeResults merges contacts from multiple sources and deduplicates by email.
// Public function preserved for back-compat.
//
// Ranking: send count > recency > source priority.
func MergeResults(sources ...[]*Contact) []*Contact {
	byEmail := make(map[string]*Contact)
	for _, contacts := range sources {
		for _, c := range contacts {
			email := strings.ToLower(c.Email)
			existing, exists := byEmail[email]
			if !exists {
				byEmail[email] = c
				continue
			}
			if c.SendCount > existing.SendCount {
				byEmail[email] = c
				continue
			}
			if c.SendCount == existing.SendCount && c.LastUsed.After(existing.LastUsed) {
				byEmail[email] = c
				continue
			}
			if c.SendCount == existing.SendCount && sourcePriority(c.Source) > sourcePriority(existing.Source) {
				byEmail[email] = c
			}
		}
	}
	result := make([]*Contact, 0, len(byEmail))
	for _, c := range byEmail {
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SendCount != result[j].SendCount {
			return result[i].SendCount > result[j].SendCount
		}
		if !result[i].LastUsed.Equal(result[j].LastUsed) {
			return result[i].LastUsed.After(result[j].LastUsed)
		}
		return result[i].Email < result[j].Email
	})
	return result
}

// sourcePriority returns the priority of a contact source (higher is better).
// "aerion" and "local" are equivalent (legacy / new naming for the same thing).
func sourcePriority(source string) int {
	switch source {
	case "aerion", "local":
		return 4
	case "vcard":
		return 3
	case "carddav":
		return 2
	case "google":
		return 1
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// Record API — multi-field record reads. Used by the Contacts extension.
// These are NEW methods (not preserving any legacy signature). They expose the
// rich per-record shape that the unified schema enables.
// ---------------------------------------------------------------------------

// GetRecordByEmail returns the contact record whose contact_emails row
// matches the given email. Picks the most autocomplete-relevant record when
// the email appears under multiple records (ranks by send_count + last_used).
// Returns (nil, nil) when no email matches.
func (s *Store) GetRecordByEmail(email string) (*Record, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, nil
	}
	var recordID string
	err := s.db.QueryRow(`
		SELECT record_id FROM contact_emails
		WHERE email = ?
		ORDER BY send_count DESC, last_used DESC
		LIMIT 1
	`, email).Scan(&recordID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up record by email: %w", err)
	}
	return s.GetRecord(recordID)
}

// GetRecord returns a single contact record by id, with all sub-table data
// populated (emails, phones, addresses, urls, impps, categories).
// Returns (nil, nil) when not found.
func (s *Store) GetRecord(id string) (*Record, error) {
	if id == "" {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT id, source, COALESCE(kind, ''), COALESCE(source_ref, ''),
		       COALESCE(fn, ''), COALESCE(n_given, ''), COALESCE(n_family, ''),
		       COALESCE(org, ''), COALESCE(title, ''), COALESCE(note, ''),
		       COALESCE(bday, ''), COALESCE(nickname, ''),
		       COALESCE(photo_data, ''), COALESCE(photo_media_type, ''), COALESCE(photo_url, ''),
		       COALESCE(vcard_raw, ''), created_at, updated_at
		FROM contact_records
		WHERE id = ?
	`, id)

	rec := &Record{}
	var createdAt, updatedAt sql.NullTime
	err := row.Scan(
		&rec.ID, &rec.Source, &rec.Kind, &rec.SourceRef,
		&rec.Fn, &rec.NGiven, &rec.NFamily,
		&rec.Org, &rec.Title, &rec.Note,
		&rec.Bday, &rec.Nickname,
		&rec.PhotoData, &rec.PhotoMediaType, &rec.PhotoURL,
		&rec.VCardRaw, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get record: %w", err)
	}
	if createdAt.Valid {
		rec.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		rec.UpdatedAt = updatedAt.Time
	}

	if err := s.loadRecordSubTables(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// loadRecordSubTables populates Emails/Phones/Addresses/URLs/IMPPs/Categories
// on the given record. Called by GetRecord and (optionally) by ListRecords.
func (s *Store) loadRecordSubTables(rec *Record) error {
	emailRows, err := s.db.Query(`
		SELECT email, COALESCE(email_type, ''), is_primary, send_count, last_used, name_overridden
		FROM contact_emails
		WHERE record_id = ?
		ORDER BY is_primary DESC, send_count DESC, email ASC
	`, rec.ID)
	if err != nil {
		return fmt.Errorf("load emails: %w", err)
	}
	for emailRows.Next() {
		var re RecordEmail
		var lastUsed sql.NullTime
		var isPrimary, nameOverridden int
		if err := emailRows.Scan(&re.Email, &re.EmailType, &isPrimary, &re.SendCount, &lastUsed, &nameOverridden); err != nil {
			continue
		}
		re.IsPrimary = isPrimary != 0
		re.NameOverridden = nameOverridden != 0
		if lastUsed.Valid {
			re.LastUsed = lastUsed.Time
		}
		rec.Emails = append(rec.Emails, re)
	}
	emailRows.Close()

	phoneRows, err := s.db.Query(`
		SELECT number, COALESCE(phone_type, ''), is_primary
		FROM contact_phones WHERE record_id = ?
		ORDER BY is_primary DESC, number ASC
	`, rec.ID)
	if err != nil {
		return fmt.Errorf("load phones: %w", err)
	}
	for phoneRows.Next() {
		var rp RecordPhone
		var isPrimary int
		if err := phoneRows.Scan(&rp.Number, &rp.PhoneType, &isPrimary); err != nil {
			continue
		}
		rp.IsPrimary = isPrimary != 0
		rec.Phones = append(rec.Phones, rp)
	}
	phoneRows.Close()

	addrRows, err := s.db.Query(`
		SELECT COALESCE(addr_type, ''), COALESCE(street, ''), COALESCE(city, ''),
		       COALESCE(region, ''), COALESCE(postcode, ''), COALESCE(country, '')
		FROM contact_addresses WHERE record_id = ?
		ORDER BY idx ASC
	`, rec.ID)
	if err != nil {
		return fmt.Errorf("load addresses: %w", err)
	}
	for addrRows.Next() {
		var ra RecordAddress
		if err := addrRows.Scan(&ra.AddrType, &ra.Street, &ra.City, &ra.Region, &ra.Postcode, &ra.Country); err != nil {
			continue
		}
		rec.Addresses = append(rec.Addresses, ra)
	}
	addrRows.Close()

	urlRows, err := s.db.Query(`
		SELECT url, COALESCE(url_type, '')
		FROM contact_urls WHERE record_id = ?
		ORDER BY url ASC
	`, rec.ID)
	if err != nil {
		return fmt.Errorf("load urls: %w", err)
	}
	for urlRows.Next() {
		var ru RecordURL
		if err := urlRows.Scan(&ru.URL, &ru.URLType); err != nil {
			continue
		}
		rec.URLs = append(rec.URLs, ru)
	}
	urlRows.Close()

	imppRows, err := s.db.Query(`
		SELECT handle, COALESCE(impp_type, '')
		FROM contact_impps WHERE record_id = ?
		ORDER BY handle ASC
	`, rec.ID)
	if err != nil {
		return fmt.Errorf("load impps: %w", err)
	}
	for imppRows.Next() {
		var ri RecordIMPP
		if err := imppRows.Scan(&ri.Handle, &ri.IMPPType); err != nil {
			continue
		}
		rec.IMPPs = append(rec.IMPPs, ri)
	}
	imppRows.Close()

	catRows, err := s.db.Query(`SELECT category FROM contact_categories WHERE record_id = ? ORDER BY category ASC`, rec.ID)
	if err != nil {
		return fmt.Errorf("load categories: %w", err)
	}
	for catRows.Next() {
		var cat string
		if err := catRows.Scan(&cat); err != nil {
			continue
		}
		rec.Categories = append(rec.Categories, cat)
	}
	catRows.Close()

	return nil
}

// ListRecords returns contact records matching the filter, with sub-table data
// populated for each. Records are ordered by fn ASC for stable display in the
// Contacts pane. The filter scopes by source/kind/source_ref and supports a
// case-insensitive name/email substring query.
func (s *Store) ListRecords(filter RecordFilter) ([]*Record, error) {
	conds := []string{}
	args := []any{}

	if filter.Source != "" {
		conds = append(conds, `cr.source = ?`)
		args = append(args, filter.Source)
	}
	if filter.Kind != "" {
		conds = append(conds, `cr.kind = ?`)
		args = append(args, filter.Kind)
	}
	if filter.SourceRef != "" {
		conds = append(conds, `cr.source_ref = ?`)
		args = append(args, filter.SourceRef)
	}
	if filter.Query != "" {
		// Match on fn OR any email belonging to the record.
		pattern := "%" + strings.ToLower(filter.Query) + "%"
		conds = append(conds, `(LOWER(COALESCE(cr.fn, '')) LIKE ? OR cr.id IN (SELECT record_id FROM contact_emails WHERE LOWER(email) LIKE ?))`)
		args = append(args, pattern, pattern)
	}

	query := `SELECT cr.id FROM contact_records cr`
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	query += ` ORDER BY COALESCE(cr.fn, '') ASC, cr.id ASC`
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", filter.Offset)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list records: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}

	records := make([]*Record, 0, len(ids))
	for _, id := range ids {
		rec, err := s.GetRecord(id)
		if err != nil {
			s.log.Warn().Err(err).Str("id", id).Msg("Failed to fetch record")
			continue
		}
		if rec == nil {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

// DBOrTx is the minimal interface satisfied by both *sql.DB and *sql.Tx —
// used by UpsertRecordTx so callers can compose record writes with their own
// table ops in a shared transaction (e.g., carddav.Store writing
// carddav_record_state alongside).
type DBOrTx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// UpsertRecord writes a full contact record (header + all sub-tables) in a
// single transaction. Used by the CardDAV sync engine to land vCards with
// their full multi-field shape.
//
// See UpsertRecordTx for the semantics — this wrapper just opens a tx,
// delegates, and commits. Callers that want to compose with carddav_record_state
// writes (or any other table) use UpsertRecordTx directly with their own tx.
func (s *Store) UpsertRecord(rec *Record) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := UpsertRecordTx(tx, rec); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertRecordTx writes the record + all sub-tables using the caller's tx.
// Semantics:
//   - If rec.ID is empty, returns an error (caller must assign).
//   - Record row is INSERT-or-UPDATE on (id). All scalar fields (fn, n_*,
//     org, title, note, bday, nickname, vcard_raw) are written verbatim.
//   - Sub-tables (contact_emails, contact_phones, contact_addresses,
//     contact_urls, contact_impps, contact_categories) are REPLACED wholesale:
//     existing rows for this record are deleted, then re-inserted from the
//     provided slices. Matches vCard semantics — the vCard is the source of
//     truth; each sync overwrites sub-table contents.
//   - For contact_emails: send_count + last_used + name_overridden are
//     PRESERVED across the replace when a matching email exists (so
//     autocomplete metadata survives a CardDAV re-sync).
//
// Does NOT touch carddav_record_state — the caller handles that sidecar.
// Works the same for source='local' contacts (no sidecar in that case).
func UpsertRecordTx(tx DBOrTx, rec *Record) error {
	if rec == nil {
		return fmt.Errorf("UpsertRecordTx: nil record")
	}
	if rec.Source == "" {
		return fmt.Errorf("UpsertRecordTx: source is required")
	}
	if rec.ID == "" {
		return fmt.Errorf("UpsertRecordTx: id is required")
	}

	now := time.Now()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now

	// Upsert the record row.
	if _, err := tx.Exec(`
		INSERT INTO contact_records
			(id, source, kind, source_ref, fn, n_given, n_family, org, title, note, bday, nickname,
			 photo_data, photo_media_type, photo_url, vcard_raw, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			kind = excluded.kind,
			source_ref = excluded.source_ref,
			fn = excluded.fn,
			n_given = excluded.n_given,
			n_family = excluded.n_family,
			org = excluded.org,
			title = excluded.title,
			note = excluded.note,
			bday = excluded.bday,
			nickname = excluded.nickname,
			photo_data = excluded.photo_data,
			photo_media_type = excluded.photo_media_type,
			photo_url = excluded.photo_url,
			vcard_raw = excluded.vcard_raw,
			updated_at = excluded.updated_at
	`,
		rec.ID, rec.Source, nullableString(rec.Kind), nullableString(rec.SourceRef),
		rec.Fn, rec.NGiven, rec.NFamily, rec.Org, rec.Title, rec.Note, rec.Bday, rec.Nickname,
		nullableString(rec.PhotoData), nullableString(rec.PhotoMediaType), nullableString(rec.PhotoURL),
		nullableString(rec.VCardRaw),
		rec.CreatedAt, rec.UpdatedAt,
	); err != nil {
		return fmt.Errorf("upsert contact_records: %w", err)
	}

	// Replace contact_emails. Preserve send_count/last_used/name_overridden when
	// the same email is present in both old and new sets.
	existingMeta := map[string]struct {
		SendCount      int
		LastUsed       sql.NullTime
		NameOverridden int
	}{}
	rows, err := tx.Query(`SELECT email, send_count, last_used, name_overridden FROM contact_emails WHERE record_id = ?`, rec.ID)
	if err != nil {
		return fmt.Errorf("read existing emails: %w", err)
	}
	for rows.Next() {
		var email string
		var sc, no int
		var lu sql.NullTime
		if err := rows.Scan(&email, &sc, &lu, &no); err != nil {
			rows.Close()
			return fmt.Errorf("scan existing email: %w", err)
		}
		existingMeta[email] = struct {
			SendCount      int
			LastUsed       sql.NullTime
			NameOverridden int
		}{sc, lu, no}
	}
	rows.Close()

	if _, err := tx.Exec(`DELETE FROM contact_emails WHERE record_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("clear contact_emails: %w", err)
	}
	for i, e := range rec.Emails {
		email := strings.ToLower(strings.TrimSpace(e.Email))
		if email == "" {
			continue
		}
		isPrimary := 0
		if e.IsPrimary || i == 0 {
			isPrimary = 1
		}
		sendCount := e.SendCount
		var lastUsed sql.NullTime
		if !e.LastUsed.IsZero() {
			lastUsed = sql.NullTime{Time: e.LastUsed, Valid: true}
		}
		nameOverridden := 0
		if e.NameOverridden {
			nameOverridden = 1
		}
		if prev, ok := existingMeta[email]; ok {
			// Preserve autocomplete metadata across re-sync.
			sendCount = prev.SendCount
			lastUsed = prev.LastUsed
			if prev.NameOverridden == 1 {
				nameOverridden = 1
			}
		}
		// OR IGNORE: a single contact can legitimately list the same address
		// twice (common in MS365 exports; also two case/whitespace variants that
		// normalize to the same value). Without this the duplicate trips the
		// PRIMARY KEY(record_id, email) and fails the record — which, mid-batch,
		// drops it from the sync. Mirrors the sibling sub-tables
		// (phones/urls/impps/categories) and the legacy UpsertContact.
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO contact_emails (record_id, email, email_type, is_primary, send_count, last_used, name_overridden)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, rec.ID, email, nullableString(e.EmailType), isPrimary, sendCount, lastUsed, nameOverridden); err != nil {
			return fmt.Errorf("insert contact_emails: %w", err)
		}
	}

	// Replace contact_phones.
	if _, err := tx.Exec(`DELETE FROM contact_phones WHERE record_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("clear contact_phones: %w", err)
	}
	for i, p := range rec.Phones {
		number := strings.TrimSpace(p.Number)
		if number == "" {
			continue
		}
		isPrimary := 0
		if p.IsPrimary || i == 0 {
			isPrimary = 1
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO contact_phones (record_id, number, phone_type, is_primary)
			VALUES (?, ?, ?, ?)
		`, rec.ID, number, nullableString(p.PhoneType), isPrimary); err != nil {
			return fmt.Errorf("insert contact_phones: %w", err)
		}
	}

	// Replace contact_addresses.
	if _, err := tx.Exec(`DELETE FROM contact_addresses WHERE record_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("clear contact_addresses: %w", err)
	}
	for i, a := range rec.Addresses {
		if a.Street == "" && a.City == "" && a.Region == "" && a.Postcode == "" && a.Country == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO contact_addresses (record_id, addr_type, street, city, region, postcode, country, idx)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, rec.ID, nullableString(a.AddrType), a.Street, a.City, a.Region, a.Postcode, a.Country, i); err != nil {
			return fmt.Errorf("insert contact_addresses: %w", err)
		}
	}

	// Replace contact_urls.
	if _, err := tx.Exec(`DELETE FROM contact_urls WHERE record_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("clear contact_urls: %w", err)
	}
	for _, u := range rec.URLs {
		url := strings.TrimSpace(u.URL)
		if url == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO contact_urls (record_id, url, url_type) VALUES (?, ?, ?)
		`, rec.ID, url, nullableString(u.URLType)); err != nil {
			return fmt.Errorf("insert contact_urls: %w", err)
		}
	}

	// Replace contact_impps.
	if _, err := tx.Exec(`DELETE FROM contact_impps WHERE record_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("clear contact_impps: %w", err)
	}
	for _, i := range rec.IMPPs {
		handle := strings.TrimSpace(i.Handle)
		if handle == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO contact_impps (record_id, handle, impp_type) VALUES (?, ?, ?)
		`, rec.ID, handle, nullableString(i.IMPPType)); err != nil {
			return fmt.Errorf("insert contact_impps: %w", err)
		}
	}

	// Replace contact_categories.
	if _, err := tx.Exec(`DELETE FROM contact_categories WHERE record_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("clear contact_categories: %w", err)
	}
	for _, c := range rec.Categories {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO contact_categories (record_id, category) VALUES (?, ?)
		`, rec.ID, c); err != nil {
			return fmt.Errorf("insert contact_categories: %w", err)
		}
	}

	return nil
}

// nullableString returns sql.NullString — Valid only when the string is
// non-empty. Lets callers pass plain Go strings while writing NULL for the
// "absent" case.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
