package backend

import (
	"errors"
	"sync"

	"github.com/hkdb/aerion/internal/database"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/platform"
)

// CalendarBridge is the Wails-bindable surface for the Calendar extension. It's
// embedded into the host `*app.App` struct; Go's method-promotion makes
// every CalendarBridge method appear on App so Wails' reflection-based bind
// generator picks them up. All Calendar-specific logic lives here, not
// in the host. The host's `app/extension_calendar.go` is reduced to a
// dozen lines of construction wiring.
//
// Method naming: all Wails-bound bridge methods use the `Calendar_` prefix
// so they can't collide with another extension's methods after embedding
// into the same App.
//
// Lightweight-by-default invariant (same as Contacts): when the user has
// the Calendar extension disabled, NOTHING is loaded beyond the ~80-byte
// CalendarBridge struct itself. The per-extension SQLite is opened eagerly in
// Store init (schema validity invariant) but no event sync runs, no
// frontend renders. Sub-phase 1B+ adds the lazy-init path for the API
// wrapper once there's real work to lazy-init.
type CalendarBridge struct {
	deps CalendarBridgeDeps

	// Lazy-initialized Calendar state. Empty in 1A; 1B/1C add real
	// caldav client + API references here.
	initOnce sync.Once
	initErr  error
}

// CalendarBridgeDeps bundles the host-provided dependencies the bridge needs.
// Grouped into a struct so adding a new dep doesn't churn every call
// site in the host. Phase 1A only needs SettingsStore + Paths + DB +
// Core; later sub-phases will add credentials access + emitter for
// sync events.
type CalendarBridgeDeps struct {
	// SettingsStore is consulted on every bridge call for the enabled
	// gate (lightweight invariant — disabled calls short-circuit before
	// any work).
	SettingsStore SettingsStore

	// Paths gives the bridge access to the OS-appropriate data directory
	// for opening the extension's per-extension SQLite.
	Paths *platform.Paths

	// DB is the shared application database. Not used in Phase 1A — the
	// calendar extension keeps its event data in its own per-extension
	// SQLite (see Store) — but kept on CalendarBridgeDeps for symmetry with
	// Contacts and for cross-extension queries that may land in later
	// phases (e.g., resolving an OAuth account by ID).
	DB *database.DB

	// Core is the coreapi.Core handle the bridge uses to call host-owned
	// cross-extension surfaces. Phase 1A doesn't use it; reserved for
	// Phase 2 when standalone-calendar OAuth sources need to call into
	// host source management.
	Core coreapi.Core
}

// SettingsStore is the narrow interface the bridge needs from the host's
// settings store. Defined here (rather than importing the concrete type)
// so 3rd-party extensions can swap in their own implementation for tests
// and so this file doesn't grow a host-package dependency.
type SettingsStore interface {
	IsExtensionEnabled(id string) (bool, error)
}

// NewCalendarBridge constructs the bridge with its dependencies. Does NOT touch
// the DB or open any extension state — that's the Store's job (called
// eagerly from app/extension_calendar.go to keep schema valid across
// enable/disable cycles).
func NewCalendarBridge(deps CalendarBridgeDeps) *CalendarBridge {
	return &CalendarBridge{deps: deps}
}

// extensionID is the key the bridge looks up in settings for the
// enabled-state check. Kept as a const so a typo doesn't silently
// disable every bridge method.
const extensionID = "calendar"

// gateEnabled returns true when the extension is currently enabled AND
// the host gave us a SettingsStore. Returns false (silently) when the
// store is nil or when the settings read errors out.
func (b *CalendarBridge) gateEnabled() bool {
	if b.deps.SettingsStore == nil {
		return false
	}
	enabled, err := b.deps.SettingsStore.IsExtensionEnabled(extensionID)
	if err != nil {
		return false
	}
	return enabled
}

// ensureInit is the lazy-init slot that later sub-phases will fill in.
// Phase 1A's only purpose is to verify the gate + Store wiring, so this
// is a no-op that just propagates `initErr` if it was set elsewhere.
func (b *CalendarBridge) ensureInit() error {
	b.initOnce.Do(func() {
		if b.deps.DB == nil || b.deps.Paths == nil {
			b.initErr = errors.New("calendar.CalendarBridge: missing DB or Paths in deps")
			return
		}
	})
	return b.initErr
}

// Calendar_HealthCheck is a 1A bind-test method. Returns a short string so
// the frontend can verify the bridge is wired up end-to-end. Removed (or
// repurposed) in a later sub-phase when there are real methods to verify
// against. Disabled-extension callers get "disabled" back — no error —
// because this is just a probe.
func (b *CalendarBridge) Calendar_HealthCheck() string {
	if !b.gateEnabled() {
		return "disabled"
	}
	if err := b.ensureInit(); err != nil {
		return "init error: " + err.Error()
	}
	return "ok"
}
