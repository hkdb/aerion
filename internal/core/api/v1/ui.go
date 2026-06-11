package v1

// RailTabRequest registers an icon in the left activity rail. The rail
// renders when at least one extension is enabled (1+ extensions plus the
// always-on Mail tab = at least two icons to switch between).
type RailTabRequest struct {
	ExtensionID string `json:"extensionId"`
	Label       string `json:"label"`
	Icon        string `json:"icon"`      // iconify identifier or asset path
	Component   string `json:"component"` // Svelte component identifier (first-party)
	Order       int    `json:"order,omitempty"`
}

// SettingsTabRequest registers a tab in the Settings dialog.
type SettingsTabRequest struct {
	ExtensionID string `json:"extensionId"`
	Label       string `json:"label"`
	Icon        string `json:"icon,omitempty"`
	Component   string `json:"component"`
}

// ContextMenuTarget classifies which core context menus an extension can extend.
type ContextMenuTarget string

const (
	ContextMenuMessageRow ContextMenuTarget = "message-row"
	ContextMenuFolderRow  ContextMenuTarget = "folder-row"
	ContextMenuContactRow ContextMenuTarget = "contact-row"
)

// ContextMenuRequest registers an item in a core context menu.
type ContextMenuRequest struct {
	ExtensionID string            `json:"extensionId"`
	Target      ContextMenuTarget `json:"target"`
	Label       string            `json:"label"`
	Icon        string            `json:"icon,omitempty"`
	HandlerID   string            `json:"handlerId"`
}

// InboxViewRequest registers an alternate rendering of the inbox (e.g., the
// people-centric grouping view).
type InboxViewRequest struct {
	ExtensionID string `json:"extensionId"`
	Label       string `json:"label"`
	Component   string `json:"component"`
}

// AccountSetupHookRequest registers an "Also set up X for this account?"
// button into the post-account-add flow in AccountForm.
//
// Intended flow (Phase 2 implementation, interface frozen here):
//  1. User completes adding a Mail account in AccountForm.
//  2. Frontend queries all registered AccountSetupHooks matching the new account's provider.
//  3. Renders each as a labeled button/checkbox.
//  4. User clicks → extension's onboarding handler runs (incremental OAuth + provider discovery).
type AccountSetupHookRequest struct {
	ExtensionID string   `json:"extensionId"`
	Providers   []string `json:"providers"`            // e.g., ["google", "microsoft", "imap"]
	ButtonLabel string   `json:"buttonLabel"`          // e.g., "Also set up your calendar"
	Description string   `json:"description,omitempty"`
	Component   string   `json:"component"`            // Svelte component handling the onboarding flow
}

// UI is the surface for extension-driven UI registrations and UI actions.
//
// All registrations are interface-only in Phase 1; the host implementation is
// Phase 2+ once the frontend slot pattern lands. Returning the Unregister func
// is required by the interface but the Phase 1 no-op implementation simply
// returns a func that does nothing.
type UI interface {
	RegisterRailTab(req RailTabRequest) (Unregister, error)
	RegisterSettingsTab(req SettingsTabRequest) (Unregister, error)
	RegisterContextMenuItem(req ContextMenuRequest) (Unregister, error)
	RegisterInboxView(req InboxViewRequest) (Unregister, error)
	RegisterAccountSetupHook(req AccountSetupHookRequest) (Unregister, error)

	// OpenURL opens the given URL in the user's system browser via the
	// host's hardened resolver (protocol allowlist, portal-first on Linux,
	// xdg-open fallback). Extensions consume this instead of reaching for
	// Wails' BrowserOpenURL directly so the security gates stay centralized.
	OpenURL(url string) error
}
