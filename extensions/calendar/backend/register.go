package backend

import (
	"fmt"

	"github.com/hkdb/aerion/extensions/calendar"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// Extension is the Calendar extension's lifecycle handle. Tiny — just the
// manifest plus the Register handshake. The Wails-bound surface lives on
// Bridge (bridge.go); the actual Calendar logic (stores, sync, API) will
// be owned by an API constructed inside Bridge's lazy-init path in later
// sub-phases.
type Extension struct {
	manifest coreapi.Manifest
}

// NewExtension constructs the Extension lifecycle handle. Allocates only
// a manifest copy + this struct — no stores, no SQLite, no API.
func NewExtension() *Extension {
	return &Extension{manifest: calendar.Manifest()}
}

// Manifest returns the parsed manifest embedded at build time.
func (e *Extension) Manifest() coreapi.Manifest { return e.manifest }

// Register wires the Calendar extension's UI surfaces. Phase 1A registers
// only the rail tab; the account-setup hook (for OAuth providers) lands
// in Phase 2 alongside the Google/Microsoft integrations. CalDAV setup
// goes through an in-pane "Add CalDAV source" dialog (1D), not through
// the account-setup hook.
//
// Runs once per Aerion process lifetime at App.Startup, regardless of
// enabled state — descriptive registrations persist across enable/disable
// cycles. The frontend filters by enabled state at render time.
func (e *Extension) Register(core coreapi.Core) (coreapi.Unregister, error) {
	unregRail, err := core.UI().RegisterRailTab(coreapi.RailTabRequest{
		ExtensionID: e.manifest.ID,
		Label:       e.manifest.Name,
		Icon:        "mdi:calendar-month",
		Component:   "CalendarPane",
		Order:       20,
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: register rail tab: %w", err)
	}

	return func() {
		unregRail()
	}, nil
}

// compile-time check: *Extension satisfies coreapi.Extension
var _ coreapi.Extension = (*Extension)(nil)
