package app

import (
	"github.com/hkdb/aerion/extensions/calendar"
	extcalendarbe "github.com/hkdb/aerion/extensions/calendar/backend"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/oauth2"
)

// initCalendarExtension wires the Calendar extension's Bridge into App
// during Startup. All bridge logic lives in extensions/calendar/backend/
// bridge.go; this file exists ONLY so the host can supply the bridge with
// its host-provided dependencies (settings store, paths, db, core handle)
// and so the embedded-field promotion makes the bridge methods Wails-bindable.
//
// The bridge holds the extension disabled-by-default; no calendar sync runs
// until the user enables Calendar in Settings. The per-extension SQLite is
// opened eagerly (Store init) so the schema stays valid across enable/disable
// cycles — same pattern Contacts uses.
//
// Phase 1A is plumbing only; the bridge has a single Calendar_HealthCheck
// method. Real Wails methods (source CRUD, event queries, sync triggers)
// land in 1B and 1C.
func (a *App) initCalendarExtension() {
	calendarCore := newCoreForExtension(a, a.calendarExt)

	a.CalendarBridge = extcalendarbe.NewCalendarBridge(extcalendarbe.CalendarBridgeDeps{
		SettingsStore: a.settingsStore,
		Paths:         a.paths,
		DB:            a.db,
		Core:          calendarCore,
	})

	// Eagerly open the per-extension SQLite so the schema is valid across
	// enable/disable cycles. Same eager-open pattern Contacts uses for its
	// Store. The Bridge itself stays lazy (initOnce inside ensureInit).
	store, err := extcalendarbe.NewStore(a.paths.Data)
	if err != nil {
		log := logging.WithComponent("app")
		log.Warn().Err(err).Msg("Failed to open calendar extension store")
		// Non-fatal — extension stays disabled functionally but UI tab still
		// renders the placeholder state. Same failure mode Contacts has if
		// its store fails to open.
		return
	}
	a.calendarStore = store

	// Register the extension's declared OAuth client configs with the global
	// resolver. Phase 1A: both slots have empty client IDs (unless ldflag-
	// injected at build time); the resolver returns (zero, false) for empty
	// slots and the chain falls through normally. Phase 2 wires Google /
	// Microsoft providers behind these slots.
	oauth2.RegisterCredentialsProvider(extensionOAuthProvider(calendar.OAuthClients()))
}
