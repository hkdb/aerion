package backend

import (
	"github.com/hkdb/aerion/internal/extensions"
)

// migrations is the per-extension migration sequence for the Calendar
// extension's isolated DB. Each entry runs in version order, idempotent.
//
// Phase 1A (plumbing only) ships a single placeholder table so the migration
// machinery has something concrete to run; the real calendar schema
// (calendar_sources, calendars, events, event_recurrence_overrides, sync_log)
// is added in 1B + 1C as those sub-phases land. See the plan file for the
// target schema shape.
var migrations = []extensions.Migration{
	{
		Version: 1,
		SQL: `
			-- Placeholder: present so Phase 1A has at least one applied
			-- migration to verify the schema-bookkeeping path. Holds the
			-- extension's installed manifest version for diagnostics; not
			-- read on any hot path. Replaced / extended by 1B's real
			-- calendar tables.
			CREATE TABLE meta (
				key        TEXT PRIMARY KEY,
				value      TEXT NOT NULL,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
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
