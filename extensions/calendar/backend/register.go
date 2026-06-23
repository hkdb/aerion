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

// Register wires the Calendar extension's UI surfaces: rail tab + two
// account-setup hooks (Google and Microsoft). The hooks fire after the
// user adds a Gmail / Outlook mail account through the standard
// AccountDialog flow — the host renders the matching hook panel inline
// so the user can subscribe the account's calendars in the same step.
// CalDAV setup goes through the standalone "Add CalDAV source" dialog,
// not through this account-setup-hook path.
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

	unregGoogleHook, err := core.UI().RegisterAccountSetupHook(coreapi.AccountSetupHookRequest{
		ExtensionID: e.manifest.ID,
		Providers:   []string{"google"},
		ButtonLabel: "Also set up Google Calendar",
		Description: "Sync your Google calendars and edit events from here.",
		Component:   "AccountCalendarHookPanelGoogle",
	})
	if err != nil {
		unregRail()
		return nil, fmt.Errorf("calendar: register google account-setup hook: %w", err)
	}

	unregMicrosoftHook, err := core.UI().RegisterAccountSetupHook(coreapi.AccountSetupHookRequest{
		ExtensionID: e.manifest.ID,
		Providers:   []string{"microsoft"},
		ButtonLabel: "Also set up Outlook Calendar",
		Description: "Sync your Outlook calendars and edit events from here.",
		Component:   "AccountCalendarHookPanelMicrosoft",
	})
	if err != nil {
		unregGoogleHook()
		unregRail()
		return nil, fmt.Errorf("calendar: register microsoft account-setup hook: %w", err)
	}

	return func() {
		unregMicrosoftHook()
		unregGoogleHook()
		unregRail()
	}, nil
}

// compile-time check: *Extension satisfies coreapi.Extension
var _ coreapi.Extension = (*Extension)(nil)
