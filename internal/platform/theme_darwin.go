//go:build darwin

package platform

import (
	"context"
	"fmt"

	"github.com/hkdb/aerion/internal/logging"
)

// DarwinThemeMonitor is a stub for macOS (frontend uses matchMedia fallback)
type DarwinThemeMonitor struct {
	events   chan SystemTheme
	stopChan chan struct{}
}

// NewThemeMonitor creates a new theme monitor for macOS
func NewThemeMonitor() ThemeMonitor {
	return &DarwinThemeMonitor{
		events:   make(chan SystemTheme, 10),
		stopChan: make(chan struct{}),
	}
}

// Start is not implemented on macOS; the frontend falls back to matchMedia
func (m *DarwinThemeMonitor) Start(ctx context.Context) error {
	log := logging.WithComponent("theme-monitor")
	log.Info().Msg("Theme monitor not available on macOS (using frontend matchMedia)")
	return fmt.Errorf("theme monitor not available on macOS")
}

// GetTheme always returns no preference on macOS
func (m *DarwinThemeMonitor) GetTheme() SystemTheme {
	return SystemThemeNoPreference
}

// Events returns the channel for receiving theme change events
func (m *DarwinThemeMonitor) Events() <-chan SystemTheme {
	return m.events
}

// Stop is a no-op on macOS
func (m *DarwinThemeMonitor) Stop() error {
	return nil
}

// ReadSystemTheme returns empty on macOS (frontend uses matchMedia)
func ReadSystemTheme() string {
	return string(SystemThemeNoPreference)
}
