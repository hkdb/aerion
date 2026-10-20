//go:build windows

package platform

import (
	"context"
	"fmt"

	"github.com/hkdb/aerion/internal/logging"
)

// WindowsThemeMonitor is a stub for Windows (frontend uses matchMedia fallback)
type WindowsThemeMonitor struct {
	events   chan SystemTheme
	stopChan chan struct{}
}

// NewThemeMonitor creates a new theme monitor for Windows
func NewThemeMonitor() ThemeMonitor {
	return &WindowsThemeMonitor{
		events:   make(chan SystemTheme, 10),
		stopChan: make(chan struct{}),
	}
}

// Start is not implemented on Windows; the frontend falls back to matchMedia
func (m *WindowsThemeMonitor) Start(ctx context.Context) error {
	log := logging.WithComponent("theme-monitor")
	log.Info().Msg("Theme monitor not available on Windows (using frontend matchMedia)")
	return fmt.Errorf("theme monitor not available on Windows")
}

// GetTheme always returns no preference on Windows
func (m *WindowsThemeMonitor) GetTheme() SystemTheme {
	return SystemThemeNoPreference
}

// Events returns the channel for receiving theme change events
func (m *WindowsThemeMonitor) Events() <-chan SystemTheme {
	return m.events
}

// Stop is a no-op on Windows
func (m *WindowsThemeMonitor) Stop() error {
	return nil
}

// ReadSystemTheme returns empty on Windows (frontend uses matchMedia)
func ReadSystemTheme() string {
	return string(SystemThemeNoPreference)
}
