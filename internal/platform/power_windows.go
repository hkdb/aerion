//go:build windows

package platform

import (
	"github.com/hkdb/aerion/internal/logging"
)

// WindowsPowerMonitor monitors power state on Windows
type WindowsPowerMonitor struct {
	callback func(PowerInfo)
	stopChan chan struct{}
}

// NewPowerMonitor creates a new power monitor for the current platform
func NewPowerMonitor() PowerMonitor {
	return &WindowsPowerMonitor{
		stopChan: make(chan struct{}),
	}
}

// GetPowerInfo returns the current power state
func (m *WindowsPowerMonitor) GetPowerInfo() (*PowerInfo, error) {
	// TODO: Implement using Win32 Power API
	// For now, return AC as default
	return &PowerInfo{
		State:             PowerStateAC,
		BatteryPercentage: -1,
		IsCharging:        false,
	}, nil
}

// Subscribe registers a callback for power state changes
func (m *WindowsPowerMonitor) Subscribe(callback func(PowerInfo)) error {
	log := logging.WithComponent("power")
	m.callback = callback
	log.Info().Msg("Power monitor subscribed (polling mode)")
	return nil
}

// Unsubscribe removes the callback
func (m *WindowsPowerMonitor) Unsubscribe() error {
	m.callback = nil
	return nil
}

// Close cleans up resources
func (m *WindowsPowerMonitor) Close() error {
	close(m.stopChan)
	return nil
}
