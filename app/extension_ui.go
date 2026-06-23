package app

import (
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
	"github.com/hkdb/aerion/internal/settings"
)

// ListEnabledExtensions returns the names of all enabled first-party
// extensions. Order is stable but not meaningful — the frontend renders
// rail tabs in coreapi.RailTabRequest.Order order, not list order.
//
// Phase 2a knows about two extension keys: contacts and calendar. As more
// first-party extensions land, add their keys to settings.AllExtensionKeys
// (which this method iterates).
func (a *App) ListEnabledExtensions() ([]string, error) {
	out := make([]string, 0, len(settings.AllExtensionKeys))
	for _, name := range settings.AllExtensionKeys {
		enabled, err := a.settingsStore.IsExtensionEnabled(name)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// ListExtensionRailTabs returns rail-tab registrations for currently enabled
// extensions only. The frontend uses this to render ExtensionRail.svelte.
// The rail renders when len() >= 1 — one enabled extension plus the implicit
// always-on Mail tab gives the user something to switch between.
func (a *App) ListExtensionRailTabs() ([]coreapi.RailTabRequest, error) {
	if a.uiRegistry == nil {
		return nil, nil
	}
	enabled, err := a.enabledExtensionSet()
	if err != nil {
		return nil, err
	}
	all := a.uiRegistry.ListRailTabs()
	out := make([]coreapi.RailTabRequest, 0, len(all))
	for _, tab := range all {
		if !enabled[tab.ExtensionID] {
			continue
		}
		out = append(out, tab)
	}
	return out, nil
}

// ListAccountSetupHooksForProvider returns hook panels matching the given
// provider. Called by AccountDialog.svelte after a new account is created,
// to render any "Also set up X" panels.
//
// Hooks are returned regardless of whether their extension is currently
// enabled — the hook itself is the discovery surface, and the user enabling
// the extension is what the hook's "Set up" handler does. Filtering by
// enabled state here would hide first-party features from new users
// (extensions default to disabled).
func (a *App) ListAccountSetupHooksForProvider(provider string) ([]coreapi.AccountSetupHookRequest, error) {
	if a.uiRegistry == nil {
		return nil, nil
	}
	return a.uiRegistry.ListAccountSetupHooksForProvider(provider), nil
}

// enabledExtensionSet returns a set of currently-enabled extension names.
func (a *App) enabledExtensionSet() (map[string]bool, error) {
	set := make(map[string]bool, len(settings.AllExtensionKeys))
	for _, name := range settings.AllExtensionKeys {
		enabled, err := a.settingsStore.IsExtensionEnabled(name)
		if err != nil {
			return nil, err
		}
		if enabled {
			set[name] = true
		}
	}
	return set, nil
}

// ExtensionInfo is the row shape returned by ListExtensions — manifest fields
// plus the current enable state. Wails-friendly (all primitive/slice types).
type ExtensionInfo struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Description      string   `json:"description"`
	Author           string   `json:"author"`
	MinAerionVersion string   `json:"minAerionVersion"`
	Capabilities     []string `json:"capabilities"`
	Enabled          bool     `json:"enabled"`
}

// ListExtensions returns the full extension listing for the Settings UI.
// Iterates all known first-party extensions (a.knownExtensions, populated at
// Startup), pulls each manifest, and pairs it with the current enable state.
//
// Used by Settings → Extensions tab to render the "Core extensions" list with
// a per-row toggle. Order matches a.knownExtensions slice order.
func (a *App) ListExtensions() ([]ExtensionInfo, error) {
	out := make([]ExtensionInfo, 0, len(a.knownExtensions))
	for _, ext := range a.knownExtensions {
		m := ext.Manifest()
		enabled, err := a.settingsStore.IsExtensionEnabled(m.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ExtensionInfo{
			ID:               m.ID,
			Name:             m.Name,
			Version:          m.Version,
			Description:      m.Description,
			Author:           m.Author,
			MinAerionVersion: m.MinAerionVersion,
			Capabilities:     m.Capabilities,
			Enabled:          enabled,
		})
	}
	return out, nil
}
