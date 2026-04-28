package app

import (
	"github.com/hkdb/aerion/internal/appstate"
	"github.com/hkdb/aerion/internal/platform"
)

// ============================================================================
// UI State Persistence
// ============================================================================

// GetUIState retrieves the last saved UI state
func (a *App) GetUIState() (*appstate.UIState, error) {
	return a.appStateStore.GetUIState()
}

// SaveUIState persists the current UI state
func (a *App) SaveUIState(state *appstate.UIState) error {
	return a.appStateStore.SaveUIState(state)
}

// ============================================================================
// App Info API - Exposed to frontend via Wails bindings
// ============================================================================

// AppInfo contains application metadata
type AppInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Website     string `json:"website"`
	License     string `json:"license"`
}

// GetAppInfo returns application metadata for the About dialog
func (a *App) GetAppInfo() AppInfo {
	return AppInfo{
		Name:        "Aerion",
		Version:     "0.2.0",
		Description: "An Open Source Lightweight E-Mail Client",
		Website:     "https://github.com/hkdb/aerion",
		License:     "Apache 2.0",
	}
}

// IsFlatpak returns true if the application is running inside a Flatpak sandbox.
func (a *App) IsFlatpak() bool {
	return platform.IsFlatpak()
}

// GetPendingMailto returns and clears any pending mailto: URL data.
// This is used when Aerion is launched with a mailto: URL argument.
func (a *App) GetPendingMailto() *MailtoData {
	data := a.PendingMailto
	a.PendingMailto = nil // Clear after reading
	return data
}
