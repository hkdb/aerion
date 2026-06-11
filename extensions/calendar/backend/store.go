package backend

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/extensions"
)

// migrations is the per-extension migration sequence for the Calendar
// extension's isolated DB. Each entry runs in version order, idempotent.
var migrations = []extensions.Migration{
	{
		Version: 1,
		SQL: `
			-- Phase 1A placeholder. Kept in place so deployments that ran 1A
			-- before 1B don't see an empty schema-bookkeeping table. Inert
			-- otherwise.
			CREATE TABLE IF NOT EXISTS meta (
				key        TEXT PRIMARY KEY,
				value      TEXT NOT NULL,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
		`,
	},
	{
		Version: 2,
		SQL: `
			-- Phase 1B: real CalDAV source + calendars schema.
			--
			-- calendar_sources: one row per CalDAV server the user has
			-- connected. Password is NOT stored here — it lives in
			-- coreapi.Storage.Secrets (keyring-first with encrypted-DB
			-- fallback in core's extension_secrets table).
			--
			-- calendars: one row per calendar within a source. CASCADE on
			-- source delete; the source row is the only handle the user has
			-- on this data. ctag + last_synced_at populated by 1C sync.

			CREATE TABLE IF NOT EXISTS calendar_sources (
				id                  TEXT PRIMARY KEY,
				type                TEXT NOT NULL,
				name                TEXT NOT NULL,
				url                 TEXT NOT NULL,
				username            TEXT NOT NULL,
				sync_interval_min   INTEGER NOT NULL DEFAULT 15,
				last_synced_at      INTEGER,
				last_error          TEXT,
				last_error_at       INTEGER,
				account_id          TEXT,
				enabled             INTEGER NOT NULL DEFAULT 1,
				created_at          INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_calendar_sources_type ON calendar_sources(type);

			CREATE TABLE IF NOT EXISTS calendars (
				id              TEXT PRIMARY KEY,
				source_id       TEXT NOT NULL REFERENCES calendar_sources(id) ON DELETE CASCADE,
				url             TEXT NOT NULL,
				display_name    TEXT NOT NULL,
				description     TEXT,
				color           TEXT,
				visible         INTEGER NOT NULL DEFAULT 1,
				ctag            TEXT,
				last_synced_at  INTEGER,
				created_at      INTEGER NOT NULL,
				UNIQUE(source_id, url)
			);
			CREATE INDEX IF NOT EXISTS idx_calendars_source ON calendars(source_id);
		`,
	},
	{
		Version: 3,
		SQL: `
			-- Phase 1C: events + RECURRENCE-ID overrides + sync log.
			--
			-- events.ics_blob is the source of truth for recurrence expansion;
			-- rrule_text is denormalized as a query convenience (NULL = non-
			-- recurring). dtstart_unix is in epoch seconds; tz_name is the
			-- IANA tz the original VEVENT used (empty = floating or UTC).
			-- Recurring events are stored as ONE row per UID with their RRULE;
			-- per-instance overrides land in event_recurrence_overrides.

			CREATE TABLE IF NOT EXISTS events (
				id              TEXT PRIMARY KEY,
				calendar_id     TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
				uid             TEXT NOT NULL,
				etag            TEXT NOT NULL,
				href            TEXT NOT NULL,
				summary         TEXT NOT NULL,
				description     TEXT,
				location        TEXT,
				dtstart_unix    INTEGER NOT NULL,
				dtend_unix      INTEGER NOT NULL,
				is_all_day      INTEGER NOT NULL DEFAULT 0,
				tz_name         TEXT,
				rrule_text      TEXT,
				ics_blob        TEXT NOT NULL,
				UNIQUE(calendar_id, uid)
			);
			CREATE INDEX IF NOT EXISTS idx_events_calendar ON events(calendar_id);
			CREATE INDEX IF NOT EXISTS idx_events_dtstart ON events(dtstart_unix);

			CREATE TABLE IF NOT EXISTS event_recurrence_overrides (
				event_id            TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
				recurrence_id_unix  INTEGER NOT NULL,
				ics_blob            TEXT NOT NULL,
				PRIMARY KEY (event_id, recurrence_id_unix)
			);

			CREATE TABLE IF NOT EXISTS sync_log (
				id           INTEGER PRIMARY KEY AUTOINCREMENT,
				source_id    TEXT,
				started_at   INTEGER NOT NULL,
				finished_at  INTEGER,
				status       TEXT NOT NULL,
				message      TEXT
			);
			CREATE INDEX IF NOT EXISTS idx_sync_log_source ON sync_log(source_id);
		`,
	},
	{
		Version: 4,
		SQL: `
			-- Phase 1G: VALARM notifications.
			--
			-- One row per (event-instance × VALARM) within the scheduler's 24h
			-- horizon. instance_unix is the occurrence DTSTART; trigger_unix is
			-- the resolved fire-time. status flows pending → fired | dismissed.
			-- The unique index makes per-sync re-evaluation idempotent.

			CREATE TABLE IF NOT EXISTS event_alarms (
				id                  TEXT PRIMARY KEY,
				event_id            TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
				instance_unix       INTEGER NOT NULL,
				trigger_unix        INTEGER NOT NULL,
				status              TEXT NOT NULL DEFAULT 'pending',
				action              TEXT NOT NULL DEFAULT 'display',
				description         TEXT,
				fired_at            INTEGER,
				created_at          INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_event_alarms_status ON event_alarms(status);
			CREATE INDEX IF NOT EXISTS idx_event_alarms_trigger ON event_alarms(trigger_unix);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_event_alarms_unique
				ON event_alarms(event_id, instance_unix, trigger_unix);
		`,
	},
	{
		Version: 5,
		SQL: `
			-- Phase 2 groundwork: writability flag + provider event ID + offline
			-- write outbox. Lays the schema before Chunks 2–7 (CalDAV write,
			-- Google + Microsoft providers, offline queue) add their consumers.
			--
			-- writable: 1 = the source supports event CRUD. Set at insert time
			-- from the provider's Capabilities().CanWrite. Local sources flip
			-- to 1 here so existing Phase 3 users keep their Edit/Delete
			-- affordances after upgrade; CalDAV stays 0 until Chunk 2 wires
			-- PUT/DELETE and we re-probe.
			--
			-- provider_event_id: server-assigned event identifier — Google's
			-- eventId, Microsoft Graph's id. CalDAV uses events.href; local
			-- leaves this empty. Not derivable from events.uid (Google assigns
			-- its own id distinct from the ICS UID), so it must be stored
			-- separately when its provider populates it.
			--
			-- pending_writes: outbox row per offline / failed write attempt.
			-- Drained by the offline queue (Chunk 5) on system:network-online
			-- and on each successful sync. Empty until Chunk 5's enqueue path
			-- ships.

			ALTER TABLE calendar_sources ADD COLUMN writable INTEGER NOT NULL DEFAULT 0;
			UPDATE calendar_sources SET writable = 1 WHERE type = 'local';

			ALTER TABLE events ADD COLUMN provider_event_id TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS pending_writes (
				id                TEXT PRIMARY KEY,
				source_id         TEXT NOT NULL REFERENCES calendar_sources(id) ON DELETE CASCADE,
				calendar_id       TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
				event_id          TEXT,
				op                TEXT NOT NULL,
				scope             TEXT,
				payload_json      TEXT NOT NULL,
				attempt           INTEGER NOT NULL DEFAULT 0,
				last_attempt_unix INTEGER,
				last_error        TEXT,
				created_unix      INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_pending_writes_source
				ON pending_writes(source_id, created_unix);
		`,
	},
	{
		Version: 6,
		SQL: `
			-- Per-calendar writability. Mirrors calendar_sources.writable but
			-- at calendar granularity, so providers that expose mixed-permission
			-- calendars under a single account (Google's "Contacts Birthdays"
			-- via AccessRole=reader, Nextcloud shares via DAV current-user-
			-- privilege-set, Microsoft's canEdit=false) can mark individual
			-- calendars read-only without flipping the whole source.
			--
			-- DEFAULT 1 is the safe migration value — existing rows assume
			-- writable until each provider's next sync re-evaluates. Local
			-- calendars always stay 1 (we own the storage).

			ALTER TABLE calendars ADD COLUMN writable INTEGER NOT NULL DEFAULT 1;
		`,
	},
	{
		Version: 7,
		SQL: `
			-- Attendees + organizer for events. JSON column carries the full
			-- shape (mirrors the ICS blob as source-of-truth); the side index
			-- exists so "events where I'm an attendee awaiting RSVP" and
			-- similar queries don't have to scan + parse every event row's
			-- JSON. The index is purely derived from attendees_json and is
			-- rebuilt wholesale on every UpsertEventTx — never written
			-- independently.
			--
			-- attendees_json defaults to '[]' so existing rows after the v7
			-- migration are well-formed without a backfill; the next sync
			-- re-parses each event's ics_blob and populates real attendees.
			-- organizer_json stays NULL when the event has no ORGANIZER (the
			-- common case for local single-user calendars).

			ALTER TABLE events ADD COLUMN attendees_json TEXT NOT NULL DEFAULT '[]';
			ALTER TABLE events ADD COLUMN organizer_json TEXT;

			CREATE TABLE event_attendee_index (
				event_id   TEXT NOT NULL,
				email_lc   TEXT NOT NULL,
				is_self    INTEGER NOT NULL DEFAULT 0,
				part_stat  TEXT NOT NULL,
				PRIMARY KEY (event_id, email_lc),
				FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
			);
			CREATE INDEX idx_event_attendee_email ON event_attendee_index(email_lc);
		`,
	},
	{
		Version: 8,
		SQL: `
			-- Per-source iTIP delivery mode. Probed at source-add time:
			--   'server' → provider handles invitation delivery (Google +
			--              Microsoft always; CalDAV servers with RFC 6638's
			--              calendar-auto-schedule feature).
			--   'none'   → CalDAV server that doesn't support 6638; the user's
			--              attendees won't receive invitations from Aerion in
			--              this release. (SMTP-only 'client' mode is out of
			--              scope per the v0.3.0 plan.)
			--
			-- DEFAULT 'server' makes the migration safe: existing Google and
			-- Microsoft sources stay correct without re-probing; existing
			-- CalDAV sources will be re-probed lazily when the user next
			-- touches an attendee-bearing event (Phase E: probe runs at
			-- source-add; existing rows fall through to 'server' which works
			-- if the server supports it, or surfaces as silent no-delivery
			-- if it doesn't — same as the pre-v0.3.0 behavior).

			ALTER TABLE calendar_sources ADD COLUMN itip_mode TEXT NOT NULL DEFAULT 'server';
		`,
	},
	{
		Version: 9,
		SQL: `
			-- Per-source organizer identity list. Populated at source-add time
			-- from provider-specific discovery (Google/Microsoft: the bound
			-- account's email; CalDAV: PROPFIND <C:calendar-user-address-set>
			-- on the principal). Empty for Local sources — attendees / RSVP /
			-- Find-a-time are gated off entirely in the composer when this
			-- list is empty (no invitation pathway exists in v0.3.0 for
			-- standalone local events).
			--
			-- Stored as JSON-encoded []string. DEFAULT '[]' keeps existing
			-- rows well-formed without a cross-DB backfill — the calendar
			-- extension's per-extension SQLite can't join the host's accounts
			-- table at migration time. Frontend falls back to live lookup
			-- via accountStore for legacy Google/Microsoft rows whose stored
			-- identity list is empty; for CalDAV the user re-runs the probe
			-- (or types an organizer email) from the per-source settings
			-- row in CalendarSettingsDialog.

			ALTER TABLE calendar_sources ADD COLUMN organizer_identities TEXT NOT NULL DEFAULT '[]';
		`,
	},
}

// Store wraps the per-extension DB for the Calendar extension. Lives in an
// isolated SQLite file at <dataDir>/extensions/calendar/data.db, separate
// from Aerion's main DB. No tables in this file are read or written by
// core code; cross-extension access (none exists yet for Calendar) flows
// through coreapi only.
type Store struct {
	*extensions.Store
}

// NewStore opens the Calendar extension's isolated SQLite DB and applies
// pending migrations. Called eagerly from App.Startup whether or not the
// extension is enabled — keeps the schema valid across enable/disable
// cycles. The same pattern Contacts uses.
func NewStore(dataDir string) (*Store, error) {
	s, err := extensions.OpenStore(dataDir, "calendar", migrations)
	if err != nil {
		return nil, err
	}
	return &Store{Store: s}, nil
}

// Source-type constants. Stored as plain strings in calendar_sources.type
// (no enum / DB CHECK constraint).
const (
	SourceTypeCalDAV    = "caldav"
	SourceTypeLocal     = "local"
	SourceTypeGoogle    = "google"    // Phase 2 Chunk 3
	SourceTypeMicrosoft = "microsoft" // Phase 2 Chunk 4
)

// Source is the Go type returned by the API + Wails methods for a calendar
// source row. JSON tags drive the TS binding shape — keep stable.
//
// Writable is set at insert time from the provider's Capabilities().CanWrite
// and re-evaluated on capability change (e.g., a CalDAV server gains PUT
// support). The frontend gates Edit / Delete / "+ Event" affordances on
// this flag rather than on source.type, so write semantics are uniform
// across local + CalDAV + Google + Microsoft once each provider lands.
type Source struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	Username        string `json:"username"`
	SyncIntervalMin int    `json:"syncIntervalMin"`
	LastSyncedAt    int64  `json:"lastSyncedAt"`
	LastError       string `json:"lastError,omitempty"`
	LastErrorAt     int64  `json:"lastErrorAt,omitempty"`
	AccountID       string `json:"accountId,omitempty"`
	Enabled         bool   `json:"enabled"`
	Writable        bool   `json:"writable"`
	CreatedAt       int64  `json:"createdAt"`

	// ITIPMode: "server" | "none". Probed at source-add time for CalDAV
	// sources (Phase E). Google + Microsoft are always "server" (provider
	// handles iTIP delivery natively). Frontend's "Send invitations"
	// toggle on AttendeesSection grays its choices when this is "none".
	ITIPMode string `json:"itipMode,omitempty"`

	// OrganizerIdentities lists the email addresses the user is authorized
	// to act as for events on this source's calendars. Populated at
	// source-add time from provider-specific discovery:
	//   - Google / Microsoft: the bound account's email (1 entry).
	//   - CalDAV: PROPFIND <C:calendar-user-address-set> on the principal
	//     (may be 0+ entries; user enters one manually when empty).
	//   - Local: empty (attendees feature gated off in the composer).
	//
	// EventComposerDialog reads this through `source.organizerIdentities`
	// to populate the "Organizing as" picker. Length 0 hides the attendees
	// section entirely; length 1 renders a static label; length 2+ shows
	// a picker constrained to these addresses.
	OrganizerIdentities []string `json:"organizerIdentities"`
}

// Calendar is the Go type for one calendar row within a source.
//
// Writable defaults true on insert; providers that expose per-calendar
// permissions (Google AccessRole, CalDAV current-user-privilege-set,
// Microsoft canEdit) flip it false for read-only calendars at add time
// and re-evaluate on periodic sync. The frontend composer hides
// !Writable calendars from the "New event" picker.
type Calendar struct {
	ID           string `json:"id"`
	SourceID     string `json:"sourceId"`
	URL          string `json:"url"`
	DisplayName  string `json:"displayName"`
	Description  string `json:"description,omitempty"`
	Color        string `json:"color,omitempty"`
	Visible      bool   `json:"visible"`
	Writable     bool   `json:"writable"`
	Ctag         string `json:"ctag,omitempty"`
	LastSyncedAt int64  `json:"lastSyncedAt"`
	CreatedAt    int64  `json:"createdAt"`
}

// WithTx runs fn inside a SQLite transaction on the per-extension DB.
// Rolls back on any error returned from fn; commits on nil. Matches the
// transaction helper carddav.Store uses for multi-row atomic operations.
func (s *Store) WithTx(fn func(*sql.Tx) error) error {
	tx, err := s.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CreateSourceTx inserts a calendar_sources row inside an existing
// transaction. The caller is responsible for committing.
func (s *Store) CreateSourceTx(tx *sql.Tx, src Source) error {
	if src.ID == "" {
		return errors.New("calendar.Store: source ID required")
	}
	if src.CreatedAt == 0 {
		src.CreatedAt = time.Now().Unix()
	}
	if src.SyncIntervalMin == 0 {
		src.SyncIntervalMin = 15
	}
	if src.ITIPMode == "" {
		src.ITIPMode = "server"
	}
	identitiesJSON, err := marshalOrganizerIdentities(src.OrganizerIdentities)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO calendar_sources (
			id, type, name, url, username, sync_interval_min,
			last_synced_at, last_error, last_error_at,
			account_id, enabled, writable, created_at, itip_mode,
			organizer_identities
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		src.ID, src.Type, src.Name, src.URL, src.Username, src.SyncIntervalMin,
		nullIfZero(src.LastSyncedAt), nullIfEmpty(src.LastError), nullIfZero(src.LastErrorAt),
		nullIfEmpty(src.AccountID), boolToInt(src.Enabled), boolToInt(src.Writable), src.CreatedAt,
		src.ITIPMode, identitiesJSON,
	)
	if err != nil {
		return fmt.Errorf("insert calendar_source: %w", err)
	}
	return nil
}

// SetOrganizerIdentities replaces the stored organizer identity list for
// a source. Called by the per-source settings UI (manual edit) and by
// re-probe operations. Empty input clears the list (composer then hides
// attendees end-to-end for that source's calendars).
func (s *Store) SetOrganizerIdentities(sourceID string, identities []string) error {
	encoded, err := marshalOrganizerIdentities(identities)
	if err != nil {
		return err
	}
	_, err = s.DB().Exec(`UPDATE calendar_sources SET organizer_identities = ? WHERE id = ?`,
		encoded, sourceID)
	if err != nil {
		return fmt.Errorf("set organizer identities: %w", err)
	}
	return nil
}

// marshalOrganizerIdentities normalizes (lowercase + trim + dedupe) and
// JSON-encodes the identity list for storage. Empty / all-blank input
// yields "[]" — never stored as NULL or "null".
func marshalOrganizerIdentities(in []string) (string, error) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		v := strings.ToLower(strings.TrimSpace(s))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshal organizer identities: %w", err)
	}
	if len(b) == 0 || string(b) == "null" {
		return "[]", nil
	}
	return string(b), nil
}

// unmarshalOrganizerIdentities decodes the JSON column back into a slice.
// Empty / unparseable input returns nil (composer treats as "no
// identities" — same UX as Local sources).
func unmarshalOrganizerIdentities(raw string) []string {
	if raw == "" || raw == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// SetSourceWritable updates the writable flag on a calendar_sources row.
// Called when a provider's capability changes (e.g., a CalDAV server gains
// PUT support, or a Google OAuth scope flips from .readonly to .events).
// Idempotent.
func (s *Store) SetSourceWritable(id string, writable bool) error {
	_, err := s.DB().Exec(
		`UPDATE calendar_sources SET writable = ? WHERE id = ?`,
		boolToInt(writable), id,
	)
	if err != nil {
		return fmt.Errorf("set source writable: %w", err)
	}
	return nil
}

// GetSource returns one source by id, or sql.ErrNoRows when missing.
func (s *Store) GetSource(id string) (*Source, error) {
	row := s.DB().QueryRow(`
		SELECT id, type, name, url, username, sync_interval_min,
		       COALESCE(last_synced_at, 0), COALESCE(last_error, ''), COALESCE(last_error_at, 0),
		       COALESCE(account_id, ''), enabled, writable, created_at, COALESCE(itip_mode, 'server'),
		       COALESCE(organizer_identities, '[]')
		FROM calendar_sources WHERE id = ?`, id)
	src := &Source{}
	var enabled, writable int
	var identitiesRaw string
	if err := row.Scan(
		&src.ID, &src.Type, &src.Name, &src.URL, &src.Username, &src.SyncIntervalMin,
		&src.LastSyncedAt, &src.LastError, &src.LastErrorAt,
		&src.AccountID, &enabled, &writable, &src.CreatedAt, &src.ITIPMode,
		&identitiesRaw,
	); err != nil {
		return nil, err
	}
	src.Enabled = enabled != 0
	src.Writable = writable != 0
	src.OrganizerIdentities = unmarshalOrganizerIdentities(identitiesRaw)
	return src, nil
}

// ListSources returns all configured calendar sources ordered by created_at.
func (s *Store) ListSources() ([]Source, error) {
	rows, err := s.DB().Query(`
		SELECT id, type, name, url, username, sync_interval_min,
		       COALESCE(last_synced_at, 0), COALESCE(last_error, ''), COALESCE(last_error_at, 0),
		       COALESCE(account_id, ''), enabled, writable, created_at, COALESCE(itip_mode, 'server'),
		       COALESCE(organizer_identities, '[]')
		FROM calendar_sources ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query calendar_sources: %w", err)
	}
	defer rows.Close()

	var out []Source
	for rows.Next() {
		var src Source
		var enabled, writable int
		var identitiesRaw string
		if err := rows.Scan(
			&src.ID, &src.Type, &src.Name, &src.URL, &src.Username, &src.SyncIntervalMin,
			&src.LastSyncedAt, &src.LastError, &src.LastErrorAt,
			&src.AccountID, &enabled, &writable, &src.CreatedAt, &src.ITIPMode,
			&identitiesRaw,
		); err != nil {
			return nil, fmt.Errorf("scan calendar_source: %w", err)
		}
		src.Enabled = enabled != 0
		src.Writable = writable != 0
		src.OrganizerIdentities = unmarshalOrganizerIdentities(identitiesRaw)
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate calendar_sources: %w", err)
	}
	return out, nil
}

// DeleteSource removes a calendar_sources row. CASCADE removes the
// associated calendars rows. Idempotent — deleting a non-existent source
// is not an error.
func (s *Store) DeleteSource(id string) error {
	_, err := s.DB().Exec(`DELETE FROM calendar_sources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete calendar_source: %w", err)
	}
	return nil
}

// DeleteCalendar removes a single calendars row. CASCADE walks down to
// events → event_recurrence_overrides → event_alarms. Idempotent.
func (s *Store) DeleteCalendar(id string) error {
	_, err := s.DB().Exec(`DELETE FROM calendars WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete calendar: %w", err)
	}
	return nil
}

// GetCalendar reads a single calendars row by ID. Returns nil + nil error
// when missing.
func (s *Store) GetCalendar(id string) (*Calendar, error) {
	row := s.DB().QueryRow(`
		SELECT id, source_id, url, display_name, COALESCE(description, ''),
		       COALESCE(color, ''), visible, writable, COALESCE(ctag, ''),
		       COALESCE(last_synced_at, 0), created_at
		FROM calendars WHERE id = ?`, id)
	var c Calendar
	var visibleInt, writableInt int
	err := row.Scan(&c.ID, &c.SourceID, &c.URL, &c.DisplayName, &c.Description,
		&c.Color, &visibleInt, &writableInt, &c.Ctag, &c.LastSyncedAt, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get calendar: %w", err)
	}
	c.Visible = visibleInt != 0
	c.Writable = writableInt != 0
	return &c, nil
}

// CreateCalendarTx inserts a calendars row inside an existing transaction.
func (s *Store) CreateCalendarTx(tx *sql.Tx, cal Calendar) error {
	if cal.ID == "" {
		return errors.New("calendar.Store: calendar ID required")
	}
	if cal.SourceID == "" {
		return errors.New("calendar.Store: source ID required")
	}
	if cal.CreatedAt == 0 {
		cal.CreatedAt = time.Now().Unix()
	}
	_, err := tx.Exec(`
		INSERT INTO calendars (
			id, source_id, url, display_name, description, color,
			visible, writable, ctag, last_synced_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cal.ID, cal.SourceID, cal.URL, cal.DisplayName, nullIfEmpty(cal.Description),
		nullIfEmpty(cal.Color), boolToInt(cal.Visible), boolToInt(cal.Writable),
		nullIfEmpty(cal.Ctag), nullIfZero(cal.LastSyncedAt), cal.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert calendar: %w", err)
	}
	return nil
}

// ListCalendars returns all calendar rows for a source, ordered by display
// name for stable UI rendering.
func (s *Store) ListCalendars(sourceID string) ([]Calendar, error) {
	rows, err := s.DB().Query(`
		SELECT id, source_id, url, display_name,
		       COALESCE(description, ''), COALESCE(color, ''),
		       visible, writable, COALESCE(ctag, ''),
		       COALESCE(last_synced_at, 0), created_at
		FROM calendars WHERE source_id = ? ORDER BY display_name ASC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("query calendars: %w", err)
	}
	defer rows.Close()

	var out []Calendar
	for rows.Next() {
		var cal Calendar
		var visible, writable int
		if err := rows.Scan(
			&cal.ID, &cal.SourceID, &cal.URL, &cal.DisplayName,
			&cal.Description, &cal.Color,
			&visible, &writable, &cal.Ctag, &cal.LastSyncedAt, &cal.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan calendar: %w", err)
		}
		cal.Visible = visible != 0
		cal.Writable = writable != 0
		out = append(out, cal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate calendars: %w", err)
	}
	return out, nil
}

// nullIfEmpty returns the input wrapped in sql.NullString — empty strings
// become SQL NULL so COALESCE works on read.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullIfZero returns SQL NULL for zero int64 inputs.
func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// boolToInt converts a Go bool to SQLite's 0/1 integer convention.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- Event types ------------------------------------------------------------

// Event is the master representation of a calendar event in storage. For
// recurring events, exactly one Event row exists per UID; per-instance
// overrides live in EventOverride. JSON tags drive the TS binding shape.
//
// Server identity is provider-specific:
//   - CalDAV: Href (e.g. /calendars/user/personal/abc.ics) + ETag for If-Match.
//   - Google: ProviderEventID (server-assigned eventId) + ETag for If-Match.
//   - Microsoft: ProviderEventID (Graph event id) + ETag.
//   - Local: all empty.
//
// Provider-specific fields stay alongside ICS-shaped fields rather than
// branching the struct per provider — keeps the read path uniform across
// providers per Phase 2's ICS-blob-as-source-of-truth decision.
type Event struct {
	ID              string `json:"id"`
	CalendarID      string `json:"calendarId"`
	UID             string `json:"uid"`
	ETag            string `json:"etag"`
	Href            string `json:"href"`
	ProviderEventID string `json:"providerEventId,omitempty"`
	Summary         string `json:"summary"`
	Description     string `json:"description,omitempty"`
	Location        string `json:"location,omitempty"`
	DTStartUnix     int64  `json:"dtstartUnix"`
	DTEndUnix       int64  `json:"dtendUnix"`
	IsAllDay        bool   `json:"isAllDay"`
	TZName          string `json:"tzName,omitempty"`
	RRuleText       string `json:"rruleText,omitempty"`
	ICSBlob         string `json:"-"` // not exposed to frontend; used by rrule_expand

	// Attendees + Organizer. Populated by the ICS parser on read; written
	// back into the JSON columns by UpsertEventTx. Types defined in
	// attendee_types.go.
	Attendees []Attendee `json:"attendees,omitempty"`
	Organizer *Organizer `json:"organizer,omitempty"`

	// SendUpdates is a transient write-time hint copied from EventInput by
	// the create/update flows; providers append it to their request URL or
	// header as appropriate. NOT persisted (excluded from JSON for the
	// frontend; not written by UpsertEventTx). Values: "all" |
	// "externalOnly" | "none" | "". Empty falls through to the provider's
	// default behavior. Phase E of the v0.3.0 attendees plan.
	SendUpdates string `json:"-"`
}

// EventInstance is one occurrence of an Event in a queried time window.
// Non-recurring events produce exactly one EventInstance per Event. Recurring
// events produce zero or more (depends on the window). RECURRENCE-ID
// overrides replace the matching default-expanded instance.
type EventInstance struct {
	Event              // embed for field reuse; serialized flat in JSON
	InstanceStartUnix  int64 `json:"instanceStartUnix"`
	InstanceEndUnix    int64 `json:"instanceEndUnix"`
	IsRecurrenceOverride bool `json:"isRecurrenceOverride,omitempty"`
}

// EventOverride is one RECURRENCE-ID exception to a master recurring event.
// recurrence_id_unix matches the instance time of the occurrence being
// overridden (epoch seconds). ics_blob holds the full overriding VEVENT.
type EventOverride struct {
	EventID          string
	RecurrenceIDUnix int64
	ICSBlob          string
}

// --- Event helpers (called by sync.go + rrule_expand.go) --------------------

// UpsertEventTx inserts or replaces an events row inside a transaction.
// On conflict (calendar_id + uid), all mutable columns are updated.
// Attendees + Organizer are serialized to their JSON columns, then the
// derived event_attendee_index rows are rebuilt wholesale (delete + insert).
func (s *Store) UpsertEventTx(tx *sql.Tx, ev Event) error {
	if ev.ID == "" {
		return errors.New("calendar.Store: event ID required")
	}
	if ev.CalendarID == "" {
		return errors.New("calendar.Store: calendar ID required")
	}
	if ev.UID == "" {
		return errors.New("calendar.Store: event UID required")
	}

	attendeesJSON, err := json.Marshal(ev.Attendees)
	if err != nil {
		return fmt.Errorf("marshal attendees: %w", err)
	}
	if len(ev.Attendees) == 0 {
		// Normalize nil slices to "[]" so the column never holds NULL or
		// "null" — keeps downstream consumers from branching.
		attendeesJSON = []byte("[]")
	}
	var organizerJSON sql.NullString
	if ev.Organizer != nil {
		b, err := json.Marshal(ev.Organizer)
		if err != nil {
			return fmt.Errorf("marshal organizer: %w", err)
		}
		organizerJSON = sql.NullString{String: string(b), Valid: true}
	}

	_, err = tx.Exec(`
		INSERT INTO events (
			id, calendar_id, uid, etag, href, provider_event_id,
			summary, description, location,
			dtstart_unix, dtend_unix, is_all_day, tz_name,
			rrule_text, ics_blob, attendees_json, organizer_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(calendar_id, uid) DO UPDATE SET
			etag = excluded.etag,
			href = excluded.href,
			provider_event_id = excluded.provider_event_id,
			summary = excluded.summary,
			description = excluded.description,
			location = excluded.location,
			dtstart_unix = excluded.dtstart_unix,
			dtend_unix = excluded.dtend_unix,
			is_all_day = excluded.is_all_day,
			tz_name = excluded.tz_name,
			rrule_text = excluded.rrule_text,
			ics_blob = excluded.ics_blob,
			attendees_json = excluded.attendees_json,
			organizer_json = excluded.organizer_json`,
		ev.ID, ev.CalendarID, ev.UID, ev.ETag, ev.Href, ev.ProviderEventID,
		ev.Summary, nullIfEmpty(ev.Description), nullIfEmpty(ev.Location),
		ev.DTStartUnix, ev.DTEndUnix, boolToInt(ev.IsAllDay), nullIfEmpty(ev.TZName),
		nullIfEmpty(ev.RRuleText), ev.ICSBlob, string(attendeesJSON), organizerJSON,
	)
	if err != nil {
		return fmt.Errorf("upsert event: %w", err)
	}

	// Rebuild the side index from the freshly-stored attendees. The
	// `events` row's ID may have just been inserted, but we use the
	// caller-supplied ID — which matches ON CONFLICT semantics (the
	// stored ID survives on update too).
	if _, err := tx.Exec(`DELETE FROM event_attendee_index WHERE event_id = ?`, ev.ID); err != nil {
		return fmt.Errorf("rebuild attendee index: %w", err)
	}
	for _, a := range ev.Attendees {
		email := strings.ToLower(strings.TrimSpace(a.Email))
		if email == "" {
			continue
		}
		partStat := a.PartStat
		if partStat == "" {
			partStat = PartStatNeedsAction
		}
		// is_self is populated to 0 here; the API layer (Phase D) sets it
		// based on the union of account + identity emails when it loads the
		// event for RSVP UI. The index is just a query accelerator; the
		// is_self flag is a denormalization the caller can refresh on
		// demand.
		if _, err := tx.Exec(`
			INSERT INTO event_attendee_index (event_id, email_lc, is_self, part_stat)
			VALUES (?, ?, 0, ?)
			ON CONFLICT(event_id, email_lc) DO UPDATE SET part_stat = excluded.part_stat
		`, ev.ID, email, partStat); err != nil {
			return fmt.Errorf("insert attendee index: %w", err)
		}
	}
	return nil
}

// DeleteEventByUIDTx removes an event row inside a transaction. CASCADE
// removes any associated event_recurrence_overrides.
func (s *Store) DeleteEventByUIDTx(tx *sql.Tx, calendarID, uid string) error {
	_, err := tx.Exec(`DELETE FROM events WHERE calendar_id = ? AND uid = ?`, calendarID, uid)
	if err != nil {
		return fmt.Errorf("delete event by uid: %w", err)
	}
	return nil
}

// ListEventETags returns a (uid → etag) map for one calendar. Used by sync
// to diff against the server's REPORT response. Skips events with empty
// ETag (shouldn't exist in practice; defensive).
func (s *Store) ListEventETags(calendarID string) (map[string]string, error) {
	rows, err := s.DB().Query(`SELECT uid, etag FROM events WHERE calendar_id = ?`, calendarID)
	if err != nil {
		return nil, fmt.Errorf("query event etags: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var uid, etag string
		if err := rows.Scan(&uid, &etag); err != nil {
			return nil, fmt.Errorf("scan event etag: %w", err)
		}
		if etag == "" {
			continue
		}
		out[uid] = etag
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event etags: %w", err)
	}
	return out, nil
}

// GetEvent returns a single event by ID, or sql.ErrNoRows when missing.
func (s *Store) GetEvent(id string) (*Event, error) {
	row := s.DB().QueryRow(`
		SELECT id, calendar_id, uid, etag, href, provider_event_id,
		       summary, COALESCE(description, ''), COALESCE(location, ''),
		       dtstart_unix, dtend_unix, is_all_day, COALESCE(tz_name, ''),
		       COALESCE(rrule_text, ''), ics_blob,
		       attendees_json, organizer_json
		FROM events WHERE id = ?`, id)
	ev := &Event{}
	var isAllDay int
	var attendeesJSON string
	var organizerJSON sql.NullString
	if err := row.Scan(
		&ev.ID, &ev.CalendarID, &ev.UID, &ev.ETag, &ev.Href, &ev.ProviderEventID,
		&ev.Summary, &ev.Description, &ev.Location,
		&ev.DTStartUnix, &ev.DTEndUnix, &isAllDay, &ev.TZName,
		&ev.RRuleText, &ev.ICSBlob, &attendeesJSON, &organizerJSON,
	); err != nil {
		return nil, err
	}
	ev.IsAllDay = isAllDay != 0
	if attendeesJSON != "" && attendeesJSON != "null" {
		if err := json.Unmarshal([]byte(attendeesJSON), &ev.Attendees); err != nil {
			return nil, fmt.Errorf("unmarshal attendees: %w", err)
		}
	}
	if organizerJSON.Valid && organizerJSON.String != "" && organizerJSON.String != "null" {
		var org Organizer
		if err := json.Unmarshal([]byte(organizerJSON.String), &org); err != nil {
			return nil, fmt.Errorf("unmarshal organizer: %w", err)
		}
		ev.Organizer = &org
	}
	return ev, nil
}

// ListEventsForExpansion returns all events for the given calendars. The
// recurrence expansion step (rrule_expand) then filters by the requested
// window. v1 fetches everything — fine for typical calendars (<1k events
// each). Time-bounded fetching is a future optimization.
func (s *Store) ListEventsForExpansion(calendarIDs []string) ([]Event, error) {
	if len(calendarIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(calendarIDs))
	args := make([]any, len(calendarIDs))
	for i, id := range calendarIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(`
		SELECT id, calendar_id, uid, etag, href, provider_event_id,
		       summary, COALESCE(description, ''), COALESCE(location, ''),
		       dtstart_unix, dtend_unix, is_all_day, COALESCE(tz_name, ''),
		       COALESCE(rrule_text, ''), ics_blob,
		       attendees_json, organizer_json
		FROM events WHERE calendar_id IN (%s)`,
		strings.Join(placeholders, ","))

	rows, err := s.DB().Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query events for expansion: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var ev Event
		var isAllDay int
		var attendeesJSON string
		var organizerJSON sql.NullString
		if err := rows.Scan(
			&ev.ID, &ev.CalendarID, &ev.UID, &ev.ETag, &ev.Href, &ev.ProviderEventID,
			&ev.Summary, &ev.Description, &ev.Location,
			&ev.DTStartUnix, &ev.DTEndUnix, &isAllDay, &ev.TZName,
			&ev.RRuleText, &ev.ICSBlob, &attendeesJSON, &organizerJSON,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		ev.IsAllDay = isAllDay != 0
		if attendeesJSON != "" && attendeesJSON != "null" {
			if err := json.Unmarshal([]byte(attendeesJSON), &ev.Attendees); err != nil {
				return nil, fmt.Errorf("unmarshal attendees: %w", err)
			}
		}
		if organizerJSON.Valid && organizerJSON.String != "" && organizerJSON.String != "null" {
			var org Organizer
			if err := json.Unmarshal([]byte(organizerJSON.String), &org); err != nil {
				return nil, fmt.Errorf("unmarshal organizer: %w", err)
			}
			ev.Organizer = &org
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

// UpsertOverrideTx inserts or replaces a RECURRENCE-ID override row.
func (s *Store) UpsertOverrideTx(tx *sql.Tx, eventID string, recurrenceIDUnix int64, blob string) error {
	_, err := tx.Exec(`
		INSERT INTO event_recurrence_overrides (event_id, recurrence_id_unix, ics_blob)
		VALUES (?, ?, ?)
		ON CONFLICT(event_id, recurrence_id_unix) DO UPDATE SET ics_blob = excluded.ics_blob`,
		eventID, recurrenceIDUnix, blob,
	)
	if err != nil {
		return fmt.Errorf("upsert override: %w", err)
	}
	return nil
}

// ListOverrides returns all RECURRENCE-ID overrides for one event.
func (s *Store) ListOverrides(eventID string) ([]EventOverride, error) {
	rows, err := s.DB().Query(`
		SELECT event_id, recurrence_id_unix, ics_blob
		FROM event_recurrence_overrides WHERE event_id = ?`, eventID)
	if err != nil {
		return nil, fmt.Errorf("query overrides: %w", err)
	}
	defer rows.Close()
	var out []EventOverride
	for rows.Next() {
		var o EventOverride
		if err := rows.Scan(&o.EventID, &o.RecurrenceIDUnix, &o.ICSBlob); err != nil {
			return nil, fmt.Errorf("scan override: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate overrides: %w", err)
	}
	return out, nil
}

// UpdateCalendarCtagTx updates a calendar's ctag + last_synced_at after a
// successful sync pass.
func (s *Store) UpdateCalendarCtagTx(tx *sql.Tx, calendarID, ctag string, syncedAt int64) error {
	_, err := tx.Exec(`
		UPDATE calendars SET ctag = ?, last_synced_at = ? WHERE id = ?`,
		nullIfEmpty(ctag), nullIfZero(syncedAt), calendarID,
	)
	if err != nil {
		return fmt.Errorf("update calendar ctag: %w", err)
	}
	return nil
}

// UpdateSourceSyncStatus marks a source as synced (on success: clears
// last_error; on failure: stores the error message). Single statement; not
// inside a transaction.
func (s *Store) UpdateSourceSyncStatus(sourceID, errMsg string) error {
	now := time.Now().Unix()
	if errMsg == "" {
		_, err := s.DB().Exec(`
			UPDATE calendar_sources
			SET last_synced_at = ?, last_error = NULL, last_error_at = NULL
			WHERE id = ?`, now, sourceID)
		if err != nil {
			return fmt.Errorf("update source sync status (success): %w", err)
		}
		return nil
	}
	_, err := s.DB().Exec(`
		UPDATE calendar_sources
		SET last_error = ?, last_error_at = ?
		WHERE id = ?`, errMsg, now, sourceID)
	if err != nil {
		return fmt.Errorf("update source sync status (error): %w", err)
	}
	return nil
}

// SetCalendarVisible flips the per-calendar visibility flag.
func (s *Store) SetCalendarVisible(calendarID string, visible bool) error {
	_, err := s.DB().Exec(`UPDATE calendars SET visible = ? WHERE id = ?`,
		boolToInt(visible), calendarID)
	if err != nil {
		return fmt.Errorf("set calendar visible: %w", err)
	}
	return nil
}

// SetCalendarColor stores the user's chosen color (hex "#rrggbb") on a
// calendar. Empty string clears the override.
func (s *Store) SetCalendarColor(calendarID, hex string) error {
	_, err := s.DB().Exec(`UPDATE calendars SET color = ? WHERE id = ?`,
		nullIfEmpty(hex), calendarID)
	if err != nil {
		return fmt.Errorf("set calendar color: %w", err)
	}
	return nil
}

// UpdateCalendarWritable flips the per-calendar writable flag. Called by
// each provider's discovery + periodic sync when the calendar's permission
// signal (Google AccessRole, CalDAV current-user-privilege-set, Microsoft
// canEdit) changes. Idempotent.
func (s *Store) UpdateCalendarWritable(calendarID string, writable bool) error {
	_, err := s.DB().Exec(`UPDATE calendars SET writable = ? WHERE id = ?`,
		boolToInt(writable), calendarID)
	if err != nil {
		return fmt.Errorf("update calendar writable: %w", err)
	}
	return nil
}

// UpdateSourceName changes the display name on a source row. Validation
// (non-empty, length cap) happens at the API layer.
func (s *Store) UpdateSourceName(sourceID, name string) error {
	_, err := s.DB().Exec(`UPDATE calendar_sources SET name = ? WHERE id = ?`,
		name, sourceID)
	if err != nil {
		return fmt.Errorf("update source name: %w", err)
	}
	return nil
}

// UpdateSyncInterval changes the per-source poll interval. Validation
// happens at the API layer; this helper just writes.
func (s *Store) UpdateSyncInterval(sourceID string, minutes int) error {
	_, err := s.DB().Exec(`UPDATE calendar_sources SET sync_interval_min = ? WHERE id = ?`,
		minutes, sourceID)
	if err != nil {
		return fmt.Errorf("update sync interval: %w", err)
	}
	return nil
}

// --- Alarms (Phase 1G) -----------------------------------------------------

// Alarm is one VALARM instance materialized for the scheduler. Each row in
// event_alarms maps to one Alarm; recurring events produce one Alarm per
// occurrence × VALARM block.
type Alarm struct {
	ID           string `json:"id"`
	EventID      string `json:"eventId"`
	InstanceUnix int64  `json:"instanceUnix"` // DTSTART of this occurrence
	TriggerUnix  int64  `json:"triggerUnix"`  // resolved fire time (UTC seconds)
	Status       string `json:"status"`       // 'pending' | 'fired' | 'dismissed'
	Action       string `json:"action"`       // 'display' / 'audio' / 'email'
	Description  string `json:"description,omitempty"`
	FiredAt      int64  `json:"firedAt,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
}

// UpsertAlarmTx inserts an alarm if no row already covers the same
// (event_id, instance_unix, trigger_unix). Idempotent — safe to call
// after every sync. Existing rows are NOT updated (we don't want to
// clobber a 'fired' status by re-evaluating).
func (s *Store) UpsertAlarmTx(tx *sql.Tx, a Alarm) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = time.Now().Unix()
	}
	if a.Status == "" {
		a.Status = "pending"
	}
	if a.Action == "" {
		a.Action = "display"
	}
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO event_alarms
			(id, event_id, instance_unix, trigger_unix, status, action, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.EventID, a.InstanceUnix, a.TriggerUnix,
		a.Status, a.Action, nullIfEmpty(a.Description), a.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert alarm: %w", err)
	}
	return nil
}

// PendingAlarmsInRange returns all 'pending' alarms with trigger_unix in
// [from, to]. Used by the scheduler to find what to arm.
func (s *Store) PendingAlarmsInRange(from, to int64) ([]Alarm, error) {
	rows, err := s.DB().Query(`
		SELECT id, event_id, instance_unix, trigger_unix, status, action,
		       COALESCE(description, ''), COALESCE(fired_at, 0), created_at
		FROM event_alarms
		WHERE status = 'pending' AND trigger_unix >= ? AND trigger_unix <= ?
		ORDER BY trigger_unix ASC`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query pending alarms: %w", err)
	}
	defer rows.Close()
	var out []Alarm
	for rows.Next() {
		var a Alarm
		if err := rows.Scan(&a.ID, &a.EventID, &a.InstanceUnix, &a.TriggerUnix,
			&a.Status, &a.Action, &a.Description, &a.FiredAt, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan alarm: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAlarm reads a single alarm by ID. Returns nil + nil error when missing.
func (s *Store) GetAlarm(id string) (*Alarm, error) {
	row := s.DB().QueryRow(`
		SELECT id, event_id, instance_unix, trigger_unix, status, action,
		       COALESCE(description, ''), COALESCE(fired_at, 0), created_at
		FROM event_alarms WHERE id = ?`, id)
	var a Alarm
	err := row.Scan(&a.ID, &a.EventID, &a.InstanceUnix, &a.TriggerUnix,
		&a.Status, &a.Action, &a.Description, &a.FiredAt, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get alarm: %w", err)
	}
	return &a, nil
}

// MarkAlarmFired transitions a pending alarm to 'fired' and records when.
func (s *Store) MarkAlarmFired(alarmID string, firedAt int64) error {
	_, err := s.DB().Exec(`
		UPDATE event_alarms
		SET status = 'fired', fired_at = ?
		WHERE id = ? AND status = 'pending'`, firedAt, alarmID)
	if err != nil {
		return fmt.Errorf("mark alarm fired: %w", err)
	}
	return nil
}

// MarkAlarmDismissed transitions a pending alarm to 'dismissed'.
func (s *Store) MarkAlarmDismissed(alarmID string) error {
	_, err := s.DB().Exec(`
		UPDATE event_alarms
		SET status = 'dismissed'
		WHERE id = ? AND status = 'pending'`, alarmID)
	if err != nil {
		return fmt.Errorf("mark alarm dismissed: %w", err)
	}
	return nil
}

// MarkPastAlarmsFired sweeps any pending alarms with trigger_unix in the
// past to 'fired' status without sending a notification. Used on app
// resume / startup so missed alarms don't fire-after-the-fact.
func (s *Store) MarkPastAlarmsFired(now int64) error {
	_, err := s.DB().Exec(`
		UPDATE event_alarms
		SET status = 'fired', fired_at = ?
		WHERE status = 'pending' AND trigger_unix < ?`, now, now)
	if err != nil {
		return fmt.Errorf("mark past alarms fired: %w", err)
	}
	return nil
}
